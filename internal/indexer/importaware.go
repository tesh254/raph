package indexer

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"raph/internal/db"
	"raph/internal/verbose"

	ts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

// Import-aware fallback resolver: for tree-sitter languages a compiler-backed
// SCIP indexer did NOT cover, raph resolves references through import bindings
// so a symbol used in one file links to its declaration in another — pure Go,
// no external tool. This is the always-on fallback beneath the SCIP tier.
//
// Sacrifices vs a real type-checker: no type inference (method calls on inferred
// receivers), no macro/decorator expansion, namespace/default imports and
// dynamic imports are skipped. It nails the common, high-value case: top-level
// functions, types, and globals referenced across files via explicit imports.

type importBinding struct {
	local    string // name as used in the importing file
	original string // exported name in the target module
}

type importSpec struct {
	stmtTypes map[string]bool
	extract   func(n *ts.Node, lang *ts.Language, src []byte) (module string, bindings []importBinding)
	resolve   func(importerRel, module string, known map[string]bool) string
}

var importSpecs = map[string]importSpec{
	"javascript": jsImportSpec(),
	"jsx":        jsImportSpec(),
	"typescript": jsImportSpec(),
	"tsx":        jsImportSpec(),
	"python":     pyImportSpec(),
}

// linkImportAwareUsages runs after the main walk (full index only). It links
// within-file AND cross-file references for fallback languages.
func (i *Indexer) linkImportAwareUsages(ctx context.Context, stats *Stats) {
	if i.store == nil {
		return
	}
	nodeIdx := i.buildSymbolIndex(ctx)
	if len(nodeIdx) == 0 {
		return
	}
	known := map[string]bool{}
	for key := range nodeIdx {
		if h := strings.IndexByte(key, 0); h >= 0 {
			known[key[:h]] = true
		}
	}

	edges := 0
	_ = filepath.WalkDir(i.root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if shouldSkipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || !isTreeSitterFile(path) {
			return nil
		}
		entry := grammars.DetectLanguage(path)
		if entry == nil || entry.Language == nil {
			return nil
		}
		// Only fallback languages: SCIP covers some, and only those with an
		// import spec resolve cross-file (others used the within-file pass).
		if i.scipCovered[entry.Name] {
			return nil
		}
		ispec, ok := importSpecs[entry.Name]
		if !ok {
			return nil
		}
		spec, ok := langSpecs[entry.Name]
		if !ok {
			return nil
		}
		rel, err := filepath.Rel(i.root, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		n := i.linkImportFile(ctx, rel, entry, ispec, spec, nodeIdx, known)
		edges += n
		return nil
	})
	stats.EdgesSaved += edges
	verbose.Printf("import-aware fallback linked %d cross-file/within-file reference edges", edges)
}

func (i *Indexer) linkImportFile(ctx context.Context, rel string, entry *grammars.LangEntry, ispec importSpec, spec langSpec, nodeIdx map[string]symbolNode, known map[string]bool) int {
	content, err := os.ReadFile(filepath.Join(i.root, rel))
	if err != nil {
		return 0
	}
	lang := entry.Language()
	if lang == nil {
		return 0
	}
	defer func() { _ = recover() }() // never let a parser panic abort the pass

	parser := ts.NewParser(lang)
	tree, perr := parser.Parse(content)
	if perr != nil || tree == nil || tree.RootNode() == nil {
		return 0
	}
	src := content

	// Resolve imports: local name -> target node id (cross-file).
	imported := map[string]string{}
	var collectImports func(n *ts.Node)
	collectImports = func(n *ts.Node) {
		if n == nil {
			return
		}
		if ispec.stmtTypes[n.Type(lang)] {
			module, bindings := ispec.extract(n, lang, src)
			if module != "" && len(bindings) > 0 {
				if target := ispec.resolve(rel, module, known); target != "" {
					for _, b := range bindings {
						if tn, ok := nodeIdx[target+"\x00"+b.original]; ok {
							imported[b.local] = tn.id
						}
					}
				}
			}
		}
		for _, c := range n.Children() {
			collectImports(c)
		}
	}
	collectImports(tree.RootNode())

	// Local declarations in this file (name -> node id), from stored nodes.
	local := map[string]string{}
	for key, sn := range nodeIdx {
		if h := strings.IndexByte(key, 0); h >= 0 && key[:h] == rel {
			local[key[h+1:]] = sn.id
		}
	}
	if len(local) == 0 && len(imported) == 0 {
		return 0
	}

	// Walk references, tracking the nearest enclosing declared symbol (owner).
	edges := 0
	var walk func(n *ts.Node, ownerID string)
	walk = func(n *ts.Node, ownerID string) {
		if n == nil {
			return
		}
		t := n.Type(lang)
		if spec.funcs[t] || spec.types[t] {
			if name := symbolName(n, lang, src); name != "" {
				if id, ok := local[name]; ok {
					ownerID = id
				}
			}
		}
		if t == "identifier" || t == "type_identifier" || t == "constant" {
			name := n.Text(src)
			var targetID string
			if id, ok := local[name]; ok {
				targetID = id
			} else if id, ok := imported[name]; ok {
				targetID = id
			}
			if targetID != "" && ownerID != "" && targetID != ownerID {
				if err := i.store.SaveEdge(ctx, db.Edge{SourceID: ownerID, TargetID: targetID, Type: "USES"}); err == nil {
					edges++
				}
			}
		}
		for _, c := range n.Children() {
			walk(c, ownerID)
		}
	}
	walk(tree.RootNode(), "")
	return edges
}

// --- JavaScript / TypeScript ---

func jsImportSpec() importSpec {
	return importSpec{
		stmtTypes: set("import_statement"),
		extract:   extractJSImports,
		resolve:   resolveJSModule,
	}
}

func extractJSImports(n *ts.Node, lang *ts.Language, src []byte) (string, []importBinding) {
	var module string
	if s := n.ChildByFieldName("source", lang); s != nil {
		module = strings.Trim(s.Text(src), "\"'`")
	}
	var bindings []importBinding
	var walk func(node *ts.Node)
	walk = func(node *ts.Node) {
		if node == nil {
			return
		}
		if node.Type(lang) == "import_specifier" {
			// { name } or { name as alias }
			nameNode := node.ChildByFieldName("name", lang)
			aliasNode := node.ChildByFieldName("alias", lang)
			if nameNode != nil {
				orig := nameNode.Text(src)
				local := orig
				if aliasNode != nil {
					local = aliasNode.Text(src)
				}
				bindings = append(bindings, importBinding{local: local, original: orig})
			}
			return
		}
		for _, c := range node.Children() {
			walk(c)
		}
	}
	walk(n)
	return module, bindings
}

// resolveJSModule resolves a relative ES module specifier to an indexed file.
func resolveJSModule(importerRel, module string, known map[string]bool) string {
	if !strings.HasPrefix(module, ".") {
		return "" // bare/package imports are out of scope for the fallback
	}
	base := filepath.ToSlash(filepath.Join(filepath.Dir(importerRel), module))
	exts := []string{"", ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs"}
	for _, e := range exts {
		if cand := base + e; known[cand] {
			return cand
		}
	}
	for _, idx := range []string{"/index.ts", "/index.tsx", "/index.js", "/index.jsx"} {
		if cand := base + idx; known[cand] {
			return cand
		}
	}
	return ""
}

// --- Python ---

func pyImportSpec() importSpec {
	return importSpec{
		stmtTypes: set("import_from_statement"),
		extract:   extractPyImports,
		resolve:   resolvePyModule,
	}
}

func extractPyImports(n *ts.Node, lang *ts.Language, src []byte) (string, []importBinding) {
	// from <module_name> import <name>[, <name as alias>] ...
	var module string
	if m := n.ChildByFieldName("module_name", lang); m != nil {
		module = m.Text(src)
	}
	var bindings []importBinding
	for _, c := range n.Children() {
		switch c.Type(lang) {
		case "dotted_name", "identifier":
			// skip the module_name child itself (it's the field "module_name")
			if m := n.ChildByFieldName("module_name", lang); m != nil && c.StartByte() == m.StartByte() {
				continue
			}
			name := c.Text(src)
			bindings = append(bindings, importBinding{local: name, original: name})
		case "aliased_import":
			nameNode := c.ChildByFieldName("name", lang)
			aliasNode := c.ChildByFieldName("alias", lang)
			if nameNode != nil {
				orig := nameNode.Text(src)
				local := orig
				if aliasNode != nil {
					local = aliasNode.Text(src)
				}
				bindings = append(bindings, importBinding{local: local, original: orig})
			}
		}
	}
	return module, bindings
}

// resolvePyModule resolves a Python from-import module to an indexed file.
// Handles relative (leading dots) and absolute (from workspace root) dotted
// module paths.
func resolvePyModule(importerRel, module string, known map[string]bool) string {
	if module == "" {
		return ""
	}
	var base string
	dots := 0
	for dots < len(module) && module[dots] == '.' {
		dots++
	}
	rest := module[dots:]
	if dots > 0 {
		dir := filepath.Dir(importerRel)
		for k := 1; k < dots; k++ {
			dir = filepath.Dir(dir)
		}
		base = filepath.ToSlash(filepath.Join(dir, strings.ReplaceAll(rest, ".", "/")))
	} else {
		base = strings.ReplaceAll(rest, ".", "/")
	}
	base = strings.TrimPrefix(base, "./")
	for _, cand := range []string{base + ".py", base + "/__init__.py"} {
		if known[cand] {
			return cand
		}
	}
	return ""
}
