package indexer

import (
	"context"
	"path/filepath"
	"strings"

	"raph/internal/db"
	"raph/internal/verbose"

	ts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

// langSpec maps a tree-sitter grammar's node-type names to raph symbol kinds.
// Adding a language is just adding an entry here — the grammar itself ships in
// gotreesitter (200+ embedded, pure Go).
type langSpec struct {
	funcs   map[string]bool // functions and methods
	types   map[string]bool // classes, structs, enums, traits, interfaces, type aliases
	globals map[string]bool // top-level var/const declarations
	consts  map[string]bool // declaration node types that should be tagged const
}

func set(values ...string) map[string]bool {
	m := make(map[string]bool, len(values))
	for _, v := range values {
		m[v] = true
	}
	return m
}

// langSpecs is keyed by gotreesitter grammar name (LangEntry.Name).
var langSpecs = map[string]langSpec{
	"python": {
		funcs:   set("function_definition"),
		types:   set("class_definition"),
		globals: set("assignment"),
	},
	"javascript": {
		funcs:   set("function_declaration", "method_definition", "generator_function_declaration"),
		types:   set("class_declaration"),
		globals: set("lexical_declaration", "variable_declaration"),
	},
	"jsx": {
		funcs:   set("function_declaration", "method_definition", "generator_function_declaration"),
		types:   set("class_declaration"),
		globals: set("lexical_declaration", "variable_declaration"),
	},
	"typescript": {
		funcs:   set("function_declaration", "method_definition", "method_signature"),
		types:   set("class_declaration", "interface_declaration", "type_alias_declaration", "enum_declaration"),
		globals: set("lexical_declaration", "variable_declaration"),
	},
	"tsx": {
		funcs:   set("function_declaration", "method_definition", "method_signature"),
		types:   set("class_declaration", "interface_declaration", "type_alias_declaration", "enum_declaration"),
		globals: set("lexical_declaration", "variable_declaration"),
	},
	"rust": {
		funcs:   set("function_item"),
		types:   set("struct_item", "enum_item", "trait_item", "type_item", "union_item"),
		globals: set("static_item", "const_item"),
		consts:  set("const_item"),
	},
	"elixir": {
		funcs: set("call"), // def/defp/defmodule are macro calls; handled heuristically
		types: set(),
	},
	"ruby": {
		funcs:   set("method", "singleton_method"),
		types:   set("class", "module"),
		globals: set("assignment"),
	},
	"java": {
		funcs:   set("method_declaration", "constructor_declaration"),
		types:   set("class_declaration", "interface_declaration", "enum_declaration", "record_declaration"),
		globals: set("field_declaration"),
	},
	"c": {
		funcs:   set("function_definition"),
		types:   set("struct_specifier", "enum_specifier", "union_specifier", "type_definition"),
		globals: set("declaration"),
	},
	"cpp": {
		funcs:   set("function_definition"),
		types:   set("class_specifier", "struct_specifier", "enum_specifier", "union_specifier", "type_definition"),
		globals: set("declaration"),
	},
	"c_sharp": {
		funcs:   set("method_declaration", "constructor_declaration", "local_function_statement"),
		types:   set("class_declaration", "interface_declaration", "struct_declaration", "enum_declaration", "record_declaration"),
		globals: set("field_declaration"),
	},
	"go": { // routed via go/ast normally; spec kept for completeness
		funcs:   set("function_declaration", "method_declaration"),
		types:   set("type_declaration"),
		globals: set("var_declaration", "const_declaration"),
	},
	"php": {
		funcs:   set("function_definition", "method_declaration"),
		types:   set("class_declaration", "interface_declaration", "trait_declaration", "enum_declaration"),
		globals: set("const_declaration"),
	},
}

// treeSitterExtensions are routed to the tree-sitter extractor (Go and Markdown
// keep their dedicated paths).
var treeSitterExtensions = set(
	".py", ".js", ".jsx", ".mjs", ".cjs", ".ts", ".tsx", ".rs", ".ex", ".exs",
	".rb", ".java", ".c", ".h", ".cc", ".cpp", ".hpp", ".cxx", ".cs", ".php",
)

func isTreeSitterFile(path string) bool {
	return treeSitterExtensions[strings.ToLower(filepath.Ext(path))]
}

// indexTreeSitterFile parses a source file with the pure-Go tree-sitter runtime
// and emits symbol nodes (functions, types, globals) plus within-file USES
// edges so agents can see what references a symbol — especially globals.
func (i *Indexer) indexTreeSitterFile(ctx context.Context, relPath, parentID, content string, stats *Stats) (err error) {
	// gotreesitter is young; never let a parser panic abort indexing.
	defer func() {
		if r := recover(); r != nil {
			verbose.Printf("tree-sitter recovered for %s: %v", relPath, r)
			err = i.indexFallbackChunks(ctx, relPath, parentID, content, stats)
		}
	}()

	entry := grammars.DetectLanguage(relPath)
	if entry == nil || entry.Language == nil {
		return i.indexFallbackChunks(ctx, relPath, parentID, content, stats)
	}
	spec, ok := langSpecs[entry.Name]
	if !ok {
		return i.indexFallbackChunks(ctx, relPath, parentID, content, stats)
	}
	lang := entry.Language()
	if lang == nil {
		return i.indexFallbackChunks(ctx, relPath, parentID, content, stats)
	}
	parser := ts.NewParser(lang)
	tree, parseErr := parser.Parse([]byte(content))
	if parseErr != nil || tree == nil || tree.RootNode() == nil {
		verbose.Printf("tree-sitter parse failed for %s: %v", relPath, parseErr)
		return i.indexFallbackChunks(ctx, relPath, parentID, content, stats)
	}

	src := []byte(content)
	// Pass 1: declarations -> nodes + DECLARES edges, and a name->nodeID index.
	declared := map[string]string{}
	var walk func(n *ts.Node)
	walk = func(n *ts.Node) {
		if n == nil {
			return
		}
		t := n.Type(lang)
		var kind string
		switch {
		case spec.funcs[t]:
			kind = "func"
		case spec.types[t]:
			kind = "type"
		case spec.globals[t]:
			kind = "" // handled below per-name
		}
		if kind != "" {
			if name := symbolName(n, lang, src); name != "" {
				id := i.saveTSSymbol(ctx, kind, relPath, name, n, src, parentID, nil, stats)
				declared[name] = id
			}
		} else if spec.globals[t] {
			gk := "var"
			if spec.consts[t] || declIsConst(n, src) {
				gk = "const"
			}
			for _, name := range globalNames(n, lang, src) {
				if name == "" {
					continue
				}
				props := map[string]string{"global": "true", "decl": gk, "lang": entry.Name}
				id := i.saveTSSymbol(ctx, gk, relPath, name, n, src, parentID, props, stats)
				declared[name] = id
			}
		}
		for _, c := range n.Children() {
			walk(c)
		}
	}
	walk(tree.RootNode())

	// Pass 2: references -> USES edges (owner symbol -> referenced declaration).
	// Skipped when a compiler-backed SCIP indexer covers this language (its
	// cross-file accurate edges supersede), or when an import-aware spec exists
	// (the post-walk fallback resolves within-file AND cross-file for it).
	if i.scipCovered[entry.Name] {
		verbose.Printf("tree-sitter usage pass skipped for %s (SCIP covers %s)", relPath, entry.Name)
		return nil
	}
	if _, ok := importSpecs[entry.Name]; ok {
		return nil // handled by linkImportAwareUsages post-walk
	}
	i.linkTreeSitterUsages(ctx, tree.RootNode(), lang, src, spec, declared, stats)
	return nil
}

func (i *Indexer) saveTSSymbol(ctx context.Context, kind, relPath, name string, n *ts.Node, src []byte, parentID string, props map[string]string, stats *Stats) string {
	snippet := nodeSnippet(n, src)
	if err := i.saveSymbol(ctx, kind, relPath, name, name, snippet, parentID, props, stats); err != nil {
		verbose.Printf("save %s symbol %q failed: %v", kind, name, err)
	}
	return i.nodeID(kind, relPath+"::"+name)
}

// linkTreeSitterUsages walks the tree tracking the nearest enclosing declared
// symbol, and links identifier references to declarations in the same file.
func (i *Indexer) linkTreeSitterUsages(ctx context.Context, root *ts.Node, lang *ts.Language, src []byte, spec langSpec, declared map[string]string, stats *Stats) {
	if len(declared) == 0 {
		return
	}
	var batch []db.Edge
	seen := map[string]bool{} // dedupe owner|target within this file
	var walk func(n *ts.Node, ownerID string)
	walk = func(n *ts.Node, ownerID string) {
		if n == nil {
			return
		}
		t := n.Type(lang)
		if spec.funcs[t] || spec.types[t] {
			if name := symbolName(n, lang, src); name != "" {
				if id, ok := declared[name]; ok {
					ownerID = id
				}
			}
		}
		if t == "identifier" || t == "type_identifier" || t == "constant" {
			name := n.Text(src)
			if targetID, ok := declared[name]; ok && ownerID != "" && targetID != ownerID {
				key := ownerID + "\x00" + targetID
				if !seen[key] {
					seen[key] = true
					batch = append(batch, db.Edge{SourceID: ownerID, TargetID: targetID, Type: "USES"})
				}
			}
		}
		for _, c := range n.Children() {
			walk(c, ownerID)
		}
	}
	walk(root, "")
	stats.EdgesSaved += i.saveEdges(ctx, batch)
}

// symbolName extracts a declaration's name via the grammar's "name" field, with
// fallbacks for grammars that nest the identifier.
func symbolName(n *ts.Node, lang *ts.Language, src []byte) string {
	if name := n.ChildByFieldName("name", lang); name != nil {
		return name.Text(src)
	}
	// Fallback: first identifier/type_identifier child.
	for _, c := range n.Children() {
		switch c.Type(lang) {
		case "identifier", "type_identifier", "constant":
			return c.Text(src)
		}
	}
	return ""
}

// globalNames extracts the identifier name(s) introduced by a top-level
// var/const declaration node across language shapes.
func globalNames(n *ts.Node, lang *ts.Language, src []byte) []string {
	var out []string
	var collectIdents func(node *ts.Node, depth int)
	collectIdents = func(node *ts.Node, depth int) {
		if node == nil || depth > 3 {
			return
		}
		switch node.Type(lang) {
		case "variable_declarator", "init_declarator", "assignment":
			if name := node.ChildByFieldName("name", lang); name != nil {
				out = append(out, name.Text(src))
				return
			}
			if left := node.ChildByFieldName("left", lang); left != nil && left.Type(lang) == "identifier" {
				out = append(out, left.Text(src))
				return
			}
		case "identifier", "type_identifier", "constant":
			if depth >= 1 {
				out = append(out, node.Text(src))
				return
			}
		}
		for _, c := range node.Children() {
			collectIdents(c, depth+1)
		}
	}
	collectIdents(n, 0)
	return dedupeStrings(out)
}

// declIsConst reports whether a declaration's source begins with the `const`
// keyword (covers JS/TS `const x` vs `let`/`var x`).
func declIsConst(n *ts.Node, src []byte) bool {
	text := strings.TrimSpace(nodeSnippet(n, src))
	return strings.HasPrefix(text, "const ") || strings.HasPrefix(text, "const\t")
}

func nodeSnippet(n *ts.Node, src []byte) string {
	start := n.StartByte()
	end := n.EndByte()
	if int(end) > len(src) || start >= end {
		return ""
	}
	snippet := string(src[start:end])
	if len(snippet) > 2000 {
		snippet = snippet[:2000] + "\n..."
	}
	return snippet
}

func dedupeStrings(in []string) []string {
	seen := map[string]struct{}{}
	out := in[:0]
	for _, s := range in {
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
