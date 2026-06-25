package indexer

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"raph/internal/db"
	"raph/internal/verbose"

	"google.golang.org/protobuf/encoding/protowire"
)

// SCIP tier: when a compiler-backed code indexer is installed (scip-typescript
// uses tsc, scip-python uses pyright, rust-analyzer, etc.) raph runs it on a
// full index, decodes the emitted SCIP protobuf, and links cross-file USES /
// MUTATES edges with the same accuracy go/types gives Go. The bundled
// tree-sitter pass stays as the always-on fallback; for any language a SCIP
// tool covers, its (within-file, name-based) usage pass is skipped so the
// authoritative SCIP edges are the only ones emitted.
//
// SCIP_ROLE_DEFINITION etc. are the symbol_roles bitset values from scip.proto.
const (
	scipRoleDefinition  = 0x1
	scipRoleWriteAccess = 0x4
)

// scipTool describes an external SCIP indexer raph knows how to drive.
type scipTool struct {
	label    string                          // language label for logs
	bin      string                          // executable looked up on PATH
	exts     []string                        // file extensions that signal this language is present
	grammars []string                        // gotreesitter grammar names this tool supersedes
	build    func(root, out string) *exec.Cmd // command writing a SCIP index to out (cmd.Dir = root)
}

// scipTools is the registry. Adding a language = one entry; the tool stays
// optional (only used when found on PATH and matching files exist).
var scipTools = []scipTool{
	{
		label:    "typescript",
		bin:      "scip-typescript",
		exts:     []string{".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs"},
		grammars: []string{"typescript", "tsx", "javascript", "jsx"},
		build: func(root, out string) *exec.Cmd {
			c := exec.Command("scip-typescript", "index", "--infer-tsconfig", "--output", out)
			c.Dir = root
			return c
		},
	},
	{
		label:    "python",
		bin:      "scip-python",
		exts:     []string{".py"},
		grammars: []string{"python"},
		build: func(root, out string) *exec.Cmd {
			c := exec.Command("scip-python", "index", ".", "--output", out, "--project-name", "raph-index")
			c.Dir = root
			return c
		},
	},
	{
		label:    "rust",
		bin:      "rust-analyzer",
		exts:     []string{".rs"},
		grammars: []string{"rust"},
		build: func(root, out string) *exec.Cmd {
			c := exec.Command("rust-analyzer", "scip", ".", "--output", out)
			c.Dir = root
			return c
		},
	},
	{
		label:    "ruby",
		bin:      "scip-ruby",
		exts:     []string{".rb"},
		grammars: []string{"ruby"},
		build: func(root, out string) *exec.Cmd {
			c := exec.Command("scip-ruby", "--index-file", out, "--gem-metadata", "raph@0.0.1", ".")
			c.Dir = root
			return c
		},
	},
	{
		label:    "java",
		bin:      "scip-java",
		exts:     []string{".java"},
		grammars: []string{"java"},
		build: func(root, out string) *exec.Cmd {
			c := exec.Command("scip-java", "index", "--output", out)
			c.Dir = root
			return c
		},
	},
	{
		label:    "clang",
		bin:      "scip-clang",
		exts:     []string{".c", ".h", ".cc", ".cpp", ".hpp", ".cxx"},
		grammars: []string{"c", "cpp"},
		build: func(root, out string) *exec.Cmd {
			c := exec.Command("scip-clang", "--index-output-path", out, "--", ".")
			c.Dir = root
			return c
		},
	},
}

// detectSCIPTools returns the registered tools available on PATH, and the set
// of gotreesitter grammar names they cover. Disabled entirely by RAPH_NO_SCIP.
func detectSCIPTools() ([]scipTool, map[string]bool) {
	covered := map[string]bool{}
	if strings.TrimSpace(os.Getenv("RAPH_NO_SCIP")) != "" {
		return nil, covered
	}
	var found []scipTool
	for _, t := range scipTools {
		if _, err := exec.LookPath(t.bin); err != nil {
			continue
		}
		found = append(found, t)
		for _, g := range t.grammars {
			covered[g] = true
		}
	}
	return found, covered
}

// SCIPToolStatus reports whether a registered compiler-grade indexer is
// installed, for the `raph scip` discoverability command.
type SCIPToolStatus struct {
	Language  string   `json:"language"`
	Binary    string   `json:"binary"`
	Installed bool     `json:"installed"`
	Path      string   `json:"path,omitempty"`
	Languages []string `json:"covers_extensions"`
	Install   string   `json:"install_hint,omitempty"`
}

// scipInstallHints map a tool binary to a one-line install command.
var scipInstallHints = map[string]string{
	"scip-typescript": "npm install -g @sourcegraph/scip-typescript",
	"scip-python":     "pip install scip-python  # or: npm i -g @sourcegraph/scip-python",
	"rust-analyzer":   "rustup component add rust-analyzer",
	"scip-ruby":       "gem install scip-ruby",
	"scip-java":       "see github.com/sourcegraph/scip-java (coursier install scip-java)",
	"scip-clang":      "see github.com/sourcegraph/scip-clang releases",
}

// SCIPStatus reports the install state of every registered SCIP indexer.
func SCIPStatus() []SCIPToolStatus {
	out := make([]SCIPToolStatus, 0, len(scipTools))
	for _, t := range scipTools {
		st := SCIPToolStatus{
			Language:  t.label,
			Binary:    t.bin,
			Languages: append([]string(nil), t.exts...),
			Install:   scipInstallHints[t.bin],
		}
		if p, err := exec.LookPath(t.bin); err == nil {
			st.Installed = true
			st.Path = p
		}
		out = append(out, st)
	}
	return out
}

// runSCIP runs every available SCIP tool whose language is present in the
// workspace, decodes the index, and links accurate reference edges. Best-effort:
// a tool that fails or is missing source never blocks indexing.
func (i *Indexer) runSCIP(ctx context.Context, tools []scipTool, stats *Stats) {
	if i.store == nil || len(tools) == 0 {
		return
	}
	nodeIdx := i.buildSymbolIndex(ctx)
	if len(nodeIdx) == 0 {
		return
	}
	tmp, err := os.MkdirTemp("", "raph-scip-")
	if err != nil {
		verbose.Printf("scip: temp dir failed: %v", err)
		return
	}
	defer os.RemoveAll(tmp)

	for _, t := range tools {
		if !i.languagePresent(t.exts) {
			continue
		}
		out := filepath.Join(tmp, t.label+".scip")
		cmd := t.build(i.root, out)
		cmd.Stdout, cmd.Stderr = nil, nil
		verbose.Printf("scip: running %s indexer", t.label)
		if err := cmd.Run(); err != nil {
			verbose.Printf("scip: %s indexer failed (skipped): %v", t.label, err)
			continue
		}
		data, err := os.ReadFile(out)
		if err != nil {
			verbose.Printf("scip: %s produced no index: %v", t.label, err)
			continue
		}
		n := i.linkSCIPIndex(ctx, data, nodeIdx, stats)
		verbose.Printf("scip: %s linked %d accurate reference edges", t.label, n)
	}
}

// languagePresent reports whether any file with the given extensions was indexed.
func (i *Indexer) languagePresent(exts []string) bool {
	for _, e := range exts {
		if i.seenExts[e] {
			return true
		}
	}
	return false
}

// symbolNode identifies a graph node by (relPath, name).
type symbolNode struct {
	id    string
	isFn  bool
	isVar bool
}

// scipSpan is a function/method definition's line range (the owner of any
// references inside it).
type scipSpan struct {
	startLine, endLine int32
	node               symbolNode
}

// buildSymbolIndex maps (relPath, name) -> node for code symbols, so SCIP
// symbols can be resolved to existing graph nodes by their definition site.
func (i *Indexer) buildSymbolIndex(ctx context.Context) map[string]symbolNode {
	idx := map[string]symbolNode{}
	nodes, err := i.store.ListNodes(ctx, db.NodeFilter{
		Workspace: i.workspaceID,
		Domain:    "code",
		Types:     []string{"func", "type", "var", "const", "method"},
		Limit:     1_000_000,
	})
	if err != nil {
		verbose.Printf("scip: node index load failed: %v", err)
		return idx
	}
	for _, n := range nodes {
		rel := n.URL
		if h := strings.IndexByte(rel, '#'); h >= 0 {
			rel = rel[:h]
		}
		key := rel + "\x00" + n.Name
		// First definition wins on collision (same name twice in a file is rare).
		if _, exists := idx[key]; exists {
			continue
		}
		idx[key] = symbolNode{
			id:    n.ID,
			isFn:  n.Type == "func" || n.Type == "method",
			isVar: n.Type == "var" || n.Type == "const",
		}
	}
	return idx
}

// scipDoc is a decoded SCIP document: just what edge linking needs.
type scipDoc struct {
	relPath string
	occs    []scipOcc
}

type scipOcc struct {
	symbol      string
	startLine   int32
	startChar   int32
	endChar     int32
	endLine     int32 // enclosing-range end when present, else == startLine
	roles       int32
	enclosesEnd int32 // last line of enclosing range (for definitions), -1 if none
}

// linkSCIPIndex decodes a SCIP index and emits USES / MUTATES edges from each
// enclosing function/method to the symbols it references, resolved by the
// indexer's compiler-grade analysis.
func (i *Indexer) linkSCIPIndex(ctx context.Context, data []byte, nodeIdx map[string]symbolNode, stats *Stats) int {
	docs := decodeSCIP(data)
	if len(docs) == 0 {
		return 0
	}

	// Pass 1: resolve each SCIP symbol to a node via its definition site, and
	// record per-doc owner spans (function/method definitions).
	symToNode := map[string]symbolNode{}
	owners := map[string][]scipSpan{} // relPath -> function definition spans

	for _, d := range docs {
		lines := i.fileLines(d.relPath)
		for _, o := range d.occs {
			if o.roles&scipRoleDefinition == 0 {
				continue
			}
			name := identAt(lines, o)
			if name == "" {
				continue
			}
			node, ok := nodeIdx[d.relPath+"\x00"+name]
			if !ok {
				continue
			}
			symToNode[o.symbol] = node
			if node.isFn {
				end := o.startLine
				if o.enclosesEnd >= 0 {
					end = o.enclosesEnd
				}
				owners[d.relPath] = append(owners[d.relPath], scipSpan{o.startLine, end, node})
			}
		}
	}
	if len(symToNode) == 0 {
		return 0
	}
	// Sort owner spans by start line so the nearest enclosing owner can be found.
	for rel := range owners {
		s := owners[rel]
		sort.Slice(s, func(a, b int) bool { return s[a].startLine < s[b].startLine })
		owners[rel] = s
	}

	edges := 0
	for _, d := range docs {
		spans := owners[d.relPath]
		if len(spans) == 0 {
			continue
		}
		for _, o := range d.occs {
			if o.roles&scipRoleDefinition != 0 {
				continue // skip the definition occurrence itself
			}
			target, ok := symToNode[o.symbol]
			if !ok {
				continue
			}
			owner, ok := enclosingOwner(spans, o.startLine)
			if !ok || owner.id == target.id {
				continue
			}
			edgeType := "USES"
			if target.isVar && o.roles&scipRoleWriteAccess != 0 {
				edgeType = "MUTATES"
			}
			if err := i.store.SaveEdge(ctx, db.Edge{SourceID: owner.id, TargetID: target.id, Type: edgeType}); err == nil {
				edges++
			}
		}
	}
	stats.EdgesSaved += edges
	return edges
}

// enclosingOwner returns the smallest function span whose line range contains
// line. spans are sorted by startLine; falls back to nearest preceding owner
// when explicit enclosing ranges are unavailable.
func enclosingOwner(spans []scipSpan, line int32) (symbolNode, bool) {
	var best symbolNode
	found := false
	for _, s := range spans {
		if s.startLine > line {
			break
		}
		// Containment when an enclosing range is known; otherwise nearest preceding.
		if s.endLine >= line || s.endLine == s.startLine {
			best = s.node
			found = true
		}
	}
	return best, found
}

// fileLines reads and splits a workspace file (cached per index run).
func (i *Indexer) fileLines(relPath string) []string {
	if i.lineCache == nil {
		i.lineCache = map[string][]string{}
	}
	if v, ok := i.lineCache[relPath]; ok {
		return v
	}
	var lines []string
	f, err := os.Open(filepath.Join(i.root, relPath))
	if err != nil {
		i.lineCache[relPath] = lines
		return lines
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	i.lineCache[relPath] = lines
	return lines
}

// identAt extracts the identifier text at a definition occurrence's range.
func identAt(lines []string, o scipOcc) string {
	if int(o.startLine) >= len(lines) {
		return ""
	}
	line := lines[o.startLine]
	// SCIP character offsets are UTF-16/UTF-8 code units; for identifiers (ASCII
	// in the overwhelming majority) byte offsets line up. Guard the bounds.
	start, end := int(o.startChar), int(o.endChar)
	if o.endLine != o.startLine || end <= start || start < 0 || end > len(line) {
		// Multi-line or out-of-range: fall back to a leading identifier scan.
		return leadingIdent(line[clampLen(start, len(line)):])
	}
	return line[start:end]
}

func clampLen(n, max int) int {
	if n < max {
		return n
	}
	return max
}

func leadingIdent(s string) string {
	s = strings.TrimLeft(s, " \t")
	end := 0
	for end < len(s) {
		c := s[end]
		if c == '_' || c == '$' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (end > 0 && c >= '0' && c <= '9') {
			end++
			continue
		}
		break
	}
	return s[:end]
}

// decodeSCIP parses a SCIP index protobuf using the low-level wire decoder,
// extracting only documents -> occurrences (relative_path, range, symbol,
// symbol_roles, enclosing_range). Field numbers come from scip.proto.
func decodeSCIP(data []byte) []scipDoc {
	var docs []scipDoc
	b := data
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			break
		}
		b = b[n:]
		if num == 2 && typ == protowire.BytesType { // Index.documents
			v, n := protowire.ConsumeBytes(b)
			if n < 0 {
				break
			}
			b = b[n:]
			if d, ok := decodeSCIPDocument(v); ok {
				docs = append(docs, d)
			}
			continue
		}
		m := protowire.ConsumeFieldValue(num, typ, b)
		if m < 0 {
			break
		}
		b = b[m:]
	}
	return docs
}

func decodeSCIPDocument(data []byte) (scipDoc, bool) {
	var doc scipDoc
	b := data
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return doc, false
		}
		b = b[n:]
		switch {
		case num == 1 && typ == protowire.BytesType: // relative_path
			v, n := protowire.ConsumeBytes(b)
			if n < 0 {
				return doc, false
			}
			doc.relPath = filepath.ToSlash(string(v))
			b = b[n:]
		case num == 2 && typ == protowire.BytesType: // occurrences
			v, n := protowire.ConsumeBytes(b)
			if n < 0 {
				return doc, false
			}
			b = b[n:]
			if o, ok := decodeSCIPOccurrence(v); ok {
				doc.occs = append(doc.occs, o)
			}
		default:
			m := protowire.ConsumeFieldValue(num, typ, b)
			if m < 0 {
				return doc, false
			}
			b = b[m:]
		}
	}
	return doc, doc.relPath != ""
}

func decodeSCIPOccurrence(data []byte) (scipOcc, bool) {
	o := scipOcc{enclosesEnd: -1}
	var rng, enc []int32
	b := data
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return o, false
		}
		b = b[n:]
		switch {
		case num == 1: // range (packed or repeated int32)
			vals, n := consumeInt32Field(typ, b)
			if n < 0 {
				return o, false
			}
			rng = append(rng, vals...)
			b = b[n:]
		case num == 2 && typ == protowire.BytesType: // symbol
			v, n := protowire.ConsumeBytes(b)
			if n < 0 {
				return o, false
			}
			o.symbol = string(v)
			b = b[n:]
		case num == 3 && typ == protowire.VarintType: // symbol_roles
			v, n := protowire.ConsumeVarint(b)
			if n < 0 {
				return o, false
			}
			o.roles = int32(v)
			b = b[n:]
		case num == 7: // enclosing_range
			vals, n := consumeInt32Field(typ, b)
			if n < 0 {
				return o, false
			}
			enc = append(enc, vals...)
			b = b[n:]
		default:
			m := protowire.ConsumeFieldValue(num, typ, b)
			if m < 0 {
				return o, false
			}
			b = b[m:]
		}
	}
	// SCIP range is [startLine, startChar, endLine, endChar] or 3-element
	// [startLine, startChar, endChar] when start/end lines match.
	switch len(rng) {
	case 3:
		o.startLine, o.startChar, o.endChar = rng[0], rng[1], rng[2]
		o.endLine = o.startLine
	case 4:
		o.startLine, o.startChar, o.endLine, o.endChar = rng[0], rng[1], rng[2], rng[3]
	default:
		return o, false
	}
	if len(enc) >= 3 {
		// enclosing range end line is index 2 (4-elem) or index 0 stays; use the
		// largest line value present as the span end.
		o.enclosesEnd = enc[0]
		if len(enc) == 4 && enc[2] > o.enclosesEnd {
			o.enclosesEnd = enc[2]
		}
	}
	return o, o.symbol != ""
}

// consumeInt32Field reads a packed or single int32 field value.
func consumeInt32Field(typ protowire.Type, b []byte) ([]int32, int) {
	if typ == protowire.BytesType { // packed
		raw, n := protowire.ConsumeBytes(b)
		if n < 0 {
			return nil, n
		}
		var out []int32
		for len(raw) > 0 {
			v, m := protowire.ConsumeVarint(raw)
			if m < 0 {
				return nil, -1
			}
			out = append(out, int32(v))
			raw = raw[m:]
		}
		return out, n
	}
	if typ == protowire.VarintType {
		v, n := protowire.ConsumeVarint(b)
		if n < 0 {
			return nil, n
		}
		return []int32{int32(v)}, n
	}
	return nil, -1
}
