package indexer

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"raph/internal/config"
	"raph/internal/db"
	"raph/internal/verbose"
)

const (
	maxStoredContent   = 12_000
	maxEmbeddedContent = 3_500
	maxFileSizeBytes   = 512 * 1024
)

type Stats struct {
	FilesIndexed      int `json:"files_indexed"`
	NodesSaved        int `json:"nodes_saved"`
	EdgesSaved        int `json:"edges_saved"`
	EmbeddingsCreated int `json:"embeddings_created"`

	// Resolution tier reporting (full index only). SCIPActive lists languages
	// resolved compiler-grade this run; SCIPSuggestions lists languages present
	// whose compiler-grade indexer is not installed, with install commands — so
	// `raph init` (and the MCP index tool) can prompt the user or let an agent
	// install the tool itself and re-index.
	SCIPActive      []string         `json:"scip_active,omitempty"`
	SCIPSuggestions []SCIPSuggestion `json:"scip_suggestions,omitempty"`
}

type Indexer struct {
	store          db.GraphStore
	cfg            *config.Config
	root           string
	workspaceID    string
	projectID      string
	skipEmbeddings bool

	// SCIP tier state (set per full index run).
	scipCovered map[string]bool     // gotreesitter grammar names a SCIP tool will resolve
	seenExts    map[string]bool     // file extensions encountered this run
	lineCache   map[string][]string // relPath -> source lines, for SCIP name extraction
}

func New(store db.GraphStore, cfg *config.Config, root string, skipEmbeddings bool) (*Indexer, error) {
	verbose.Printf("creating indexer root=%s skipEmbeddings=%t", root, skipEmbeddings)
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve root path: %w", err)
	}

	projectID, err := ResolveProjectIdentity(cfg, absRoot)
	if err != nil {
		return nil, err
	}
	verbose.Printf("resolved project identity=%s workspace=%s", projectID, workspaceID(absRoot))

	return &Indexer{
		store:          store,
		cfg:            cfg,
		root:           absRoot,
		workspaceID:    workspaceID(absRoot),
		projectID:      projectID,
		skipEmbeddings: skipEmbeddings,
	}, nil
}

func (i *Indexer) Run(ctx context.Context) (Stats, error) {
	var stats Stats

	// Detect compiler-backed SCIP indexers up front: for any language they
	// cover, the tree-sitter usage pass is skipped so the authoritative SCIP
	// edges are the only USES/MUTATES emitted for that language.
	scipToolsAvailable, scipCovered := detectSCIPTools()
	i.scipCovered = scipCovered
	i.seenExts = map[string]bool{}
	i.lineCache = map[string][]string{}

	verbose.Printf("clearing existing workspace graph workspace=%s", i.workspaceID)
	if err := i.store.DeleteWorkspace(ctx, i.workspaceID); err != nil {
		return stats, fmt.Errorf("clear existing workspace graph: %w", err)
	}

	verbose.Printf("walking directory tree root=%s", i.root)
	err := filepath.WalkDir(i.root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if shouldSkipDir(d.Name()) {
				verbose.Printf("skipping directory name=%s", d.Name())
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || !shouldIndexFile(path) {
			return nil
		}
		if err := i.indexFile(ctx, path, &stats); err != nil {
			return fmt.Errorf("index %s: %w", path, err)
		}
		return nil
	})
	if err != nil {
		return stats, err
	}

	// Type-accurate Go reference linking (globals, calls, type usage). Runs on
	// full index only; best-effort so a non-building tree never blocks indexing.
	i.linkGoUsages(ctx, &stats)

	// (relPath, name) -> node index, built once from the now-complete node set
	// and shared by both reference-linking passes below (each previously rebuilt
	// it from a full ListNodes scan).
	nodeIdx := i.buildSymbolIndex(ctx)

	// Compiler-grade reference linking for other languages, when an external
	// SCIP indexer is installed. Cross-file accurate; best-effort.
	i.runSCIP(ctx, scipToolsAvailable, nodeIdx, &stats)
	i.lineCache = nil // only SCIP name extraction needs it; release the source cache

	// Import-aware cross-file fallback for tree-sitter languages a SCIP tool did
	// not cover — resolves references through imports without any external tool.
	i.linkImportAwareUsages(ctx, nodeIdx, &stats)

	// Report which languages got compiler-grade resolution and which could, so
	// the CLI/MCP can nudge the user (or agent) to install the missing tool.
	stats.SCIPActive, stats.SCIPSuggestions = i.scipReport(scipToolsAvailable)

	verbose.Printf("walk complete files=%d nodes=%d edges=%d embeddings=%d", stats.FilesIndexed, stats.NodesSaved, stats.EdgesSaved, stats.EmbeddingsCreated)
	return stats, nil
}

func (i *Indexer) SyncFile(ctx context.Context, path string) (Stats, error) {
	var stats Stats
	absPath, err := filepath.Abs(path)
	if err != nil {
		return stats, fmt.Errorf("resolve file path: %w", err)
	}
	relPath, err := filepath.Rel(i.root, absPath)
	if err != nil || relPath == ".." || strings.HasPrefix(relPath, ".."+string(filepath.Separator)) {
		return stats, fmt.Errorf("file %s is outside workspace %s", absPath, i.root)
	}
	relPath = filepath.ToSlash(relPath)
	if err := i.store.DeleteFileNodes(ctx, i.workspaceID, relPath); err != nil {
		return stats, fmt.Errorf("clear existing file graph: %w", err)
	}
	if _, err := os.Stat(absPath); errors.Is(err, os.ErrNotExist) {
		return stats, nil
	} else if err != nil {
		return stats, err
	}
	if !shouldIndexFile(absPath) {
		return stats, nil
	}
	if err := i.indexFile(ctx, absPath, &stats); err != nil {
		return stats, err
	}
	return stats, nil
}

func (i *Indexer) RemoveFile(ctx context.Context, relativePath string) error {
	return i.store.DeleteFileNodes(ctx, i.workspaceID, filepath.ToSlash(relativePath))
}

func (i *Indexer) WorkspaceID() string {
	return i.workspaceID
}

func (i *Indexer) ProjectID() string {
	return i.projectID
}

func CollectFileStates(root string) (map[string]FileState, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	states := make(map[string]FileState)
	err = filepath.WalkDir(absRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if shouldSkipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || !shouldIndexFile(path) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Size() > maxFileSizeBytes {
			return nil
		}
		relPath, err := filepath.Rel(absRoot, path)
		if err != nil {
			return err
		}
		states[filepath.ToSlash(relPath)] = FileState{Size: info.Size(), ModTimeUnixNano: info.ModTime().UnixNano()}
		return nil
	})
	return states, err
}

type FileState struct {
	Size            int64 `json:"size"`
	ModTimeUnixNano int64 `json:"mod_time_unix_nano"`
}

func (i *Indexer) indexFile(ctx context.Context, path string, stats *Stats) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Size() > maxFileSizeBytes {
		verbose.Printf("skipping oversized file path=%s size=%d", path, info.Size())
		return nil
	}

	relPath, err := filepath.Rel(i.root, path)
	if err != nil {
		return err
	}
	relPath = filepath.ToSlash(relPath)
	if i.seenExts != nil {
		i.seenExts[strings.ToLower(filepath.Ext(relPath))] = true
	}
	verbose.Printf("indexing file=%s domain=%s size=%d", relPath, detectDomain(relPath), info.Size())

	contentBytes, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	content := string(contentBytes)

	fileNode := db.Node{
		ID:        i.nodeID("file", relPath),
		Workspace: i.workspaceID,
		Domain:    detectDomain(relPath),
		Type:      "file",
		Name:      relPath,
		Content:   truncateRunes(content, maxStoredContent),
		URL:       relPath,
		Path:      i.root,
	}
	embedding, err := i.embed(ctx, relPath+"\n\n"+content, stats)
	if err != nil {
		return fmt.Errorf("embed file: %w", err)
	}
	if len(embedding) > 0 {
		fileNode.Embedding = embedding
	}
	if err := i.store.SaveNode(ctx, fileNode); err != nil {
		return err
	}
	stats.FilesIndexed++
	stats.NodesSaved++

	ext := strings.ToLower(filepath.Ext(relPath))
	beforeNodes := stats.NodesSaved
	beforeEdges := stats.EdgesSaved
	switch ext {
	case ".md", ".markdown", ".txt", ".rst":
		if err := i.indexDocumentSections(ctx, relPath, fileNode.ID, content, stats); err != nil {
			return err
		}
	case ".go":
		if err := i.indexGoFile(ctx, relPath, fileNode.ID, contentBytes, stats); err != nil {
			return err
		}
	default:
		if isTreeSitterFile(relPath) {
			if err := i.indexTreeSitterFile(ctx, relPath, fileNode.ID, content, stats); err != nil {
				return err
			}
		} else if err := i.indexFallbackChunks(ctx, relPath, fileNode.ID, content, stats); err != nil {
			return err
		}
	}

	verbose.Printf("indexed file=%s nodes=+%d edges=+%d", relPath, stats.NodesSaved-beforeNodes, stats.EdgesSaved-beforeEdges)
	return nil
}

func (i *Indexer) indexDocumentSections(ctx context.Context, relPath string, parentID string, content string, stats *Stats) error {
	sections := splitDocumentSections(content)
	for idx, section := range sections {
		sectionNode := db.Node{
			ID:        i.nodeID("markdown_chunk", fmt.Sprintf("%s#%d", relPath, idx)),
			Workspace: i.workspaceID,
			Domain:    "documentation",
			Type:      "markdown_chunk",
			Name:      section.Title,
			Content:   truncateRunes(section.Content, maxStoredContent),
			URL:       fmt.Sprintf("%s#section-%d", relPath, idx+1),
			Path:      i.root,
		}
		embedding, err := i.embed(ctx, section.Title+"\n\n"+section.Content, stats)
		if err != nil {
			return fmt.Errorf("embed section %q: %w", section.Title, err)
		}
		if len(embedding) > 0 {
			sectionNode.Embedding = embedding
		}
		if err := i.store.SaveNode(ctx, sectionNode); err != nil {
			return err
		}
		if err := i.store.SaveEdge(ctx, db.Edge{SourceID: parentID, TargetID: sectionNode.ID, Type: "HAS_SECTION"}); err != nil {
			return err
		}
		stats.NodesSaved++
		stats.EdgesSaved++
	}
	return nil
}

func (i *Indexer) indexGoFile(ctx context.Context, relPath string, parentID string, content []byte, stats *Stats) error {
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, relPath, content, parser.ParseComments)
	if err != nil {
		return nil
	}

	for _, decl := range parsed.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			name := d.Name.Name
			key := name
			if d.Recv != nil && len(d.Recv.List) > 0 {
				// Qualify methods by receiver type so e.g. two String() methods
				// on different types don't collide and agents can tell them apart.
				recv := receiverTypeName(d.Recv.List[0].Type)
				if recv != "" {
					name = "(" + recv + ") " + name
					key = recv + "." + d.Name.Name
				}
			}
			snippet := snippetForNode(fset, content, d.Pos(), d.End())
			if err := i.saveSymbol(ctx, "func", relPath, key, name, snippet, parentID, nil, stats); err != nil {
				return err
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					name := s.Name.Name
					snippet := snippetForNode(fset, content, s.Pos(), s.End())
					if err := i.saveSymbol(ctx, "type", relPath, name, name, snippet, parentID, nil, stats); err != nil {
						return err
					}
				case *ast.ValueSpec:
					// Package-level var/const — the globals agents most often
					// hallucinate about. Index each name as its own node.
					kind := "var"
					if d.Tok == token.CONST {
						kind = "const"
					}
					snippet := snippetForNode(fset, content, s.Pos(), s.End())
					for _, ident := range s.Names {
						if ident.Name == "_" {
							continue
						}
						props := map[string]string{"global": "true", "decl": kind}
						if err := i.saveSymbol(ctx, kind, relPath, ident.Name, ident.Name, snippet, parentID, props, stats); err != nil {
							return err
						}
					}
				}
			}
		}
	}
	return nil
}

// saveSymbol persists a code symbol node (with optional embedding and
// properties) and links it to its declaring file.
func (i *Indexer) saveSymbol(ctx context.Context, kind, relPath, key, name, snippet, parentID string, props map[string]string, stats *Stats) error {
	node := db.Node{
		ID:         i.nodeID(kind, relPath+"::"+key),
		Workspace:  i.workspaceID,
		Domain:     "code",
		Type:       kind,
		Name:       name,
		Content:    truncateRunes(snippet, maxStoredContent),
		URL:        relPath + "#" + key,
		Path:       i.root,
		Properties: props,
	}
	embedding, err := i.embed(ctx, name+"\n\n"+snippet, stats)
	if err != nil {
		return fmt.Errorf("embed %s %q: %w", kind, name, err)
	}
	if len(embedding) > 0 {
		node.Embedding = embedding
	}
	if err := i.store.SaveNode(ctx, node); err != nil {
		return err
	}
	if err := i.store.SaveEdge(ctx, db.Edge{SourceID: parentID, TargetID: node.ID, Type: "DECLARES"}); err != nil {
		return err
	}
	stats.NodesSaved++
	stats.EdgesSaved++
	return nil
}

// receiverTypeName extracts the base type name from a method receiver, e.g.
// *Indexer -> "Indexer".
func receiverTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.StarExpr:
		return receiverTypeName(t.X)
	case *ast.Ident:
		return t.Name
	case *ast.IndexExpr: // generic receiver: T[P]
		return receiverTypeName(t.X)
	case *ast.IndexListExpr:
		return receiverTypeName(t.X)
	}
	return ""
}

func (i *Indexer) indexFallbackChunks(ctx context.Context, relPath string, parentID string, content string, stats *Stats) error {
	chunks := splitFallbackChunks(content, 1800)
	for idx, chunk := range chunks {
		node := db.Node{
			ID:        i.nodeID("file_chunk", fmt.Sprintf("%s#%d", relPath, idx)),
			Workspace: i.workspaceID,
			Domain:    detectDomain(relPath),
			Type:      "file_chunk",
			Name:      fmt.Sprintf("%s chunk %d", relPath, idx+1),
			Content:   truncateRunes(chunk, maxStoredContent),
			URL:       fmt.Sprintf("%s#chunk-%d", relPath, idx+1),
			Path:      i.root,
		}
		embedding, err := i.embed(ctx, chunk, stats)
		if err != nil {
			return fmt.Errorf("embed fallback chunk %q: %w", relPath, err)
		}
		if len(embedding) > 0 {
			node.Embedding = embedding
		}
		if err := i.store.SaveNode(ctx, node); err != nil {
			return err
		}
		if err := i.store.SaveEdge(ctx, db.Edge{SourceID: parentID, TargetID: node.ID, Type: "HAS_CHUNK"}); err != nil {
			return err
		}
		stats.NodesSaved++
		stats.EdgesSaved++
	}
	return nil
}

func (i *Indexer) embed(ctx context.Context, text string, stats *Stats) ([]float32, error) {
	if i.skipEmbeddings || i.cfg == nil || !i.cfg.HasEmbeddingProvider() {
		return nil, nil
	}

	text = strings.TrimSpace(truncateRunes(text, maxEmbeddedContent))
	if text == "" {
		return nil, nil
	}

	embedding, err := config.GenerateEmbedding(ctx, i.cfg, text)
	if err != nil {
		return nil, err
	}
	stats.EmbeddingsCreated++
	return embedding, nil
}

// edgeFlushChunk bounds how many edges a streaming pass accumulates before
// flushing, so a large workspace doesn't hold every pending edge in memory.
const edgeFlushChunk = 4096

// edgeBatcher is the optional batch-write capability some stores expose. The
// indexer uses it when present (one transaction for an edge-heavy pass) and
// falls back to per-edge SaveEdge otherwise, so mock stores need no changes.
type edgeBatcher interface {
	SaveEdges(ctx context.Context, edges []db.Edge) error
}

// saveEdges persists a batch of edges and returns how many were written. It
// prefers the store's batch path (single transaction); on its failure, or when
// the store lacks one, it writes them one at a time counting successes.
func (i *Indexer) saveEdges(ctx context.Context, edges []db.Edge) int {
	if len(edges) == 0 {
		return 0
	}
	if b, ok := i.store.(edgeBatcher); ok {
		if err := b.SaveEdges(ctx, edges); err == nil {
			return len(edges)
		}
		// Batch failed (and rolled back atomically): fall through to per-edge.
	}
	n := 0
	for _, e := range edges {
		if err := i.store.SaveEdge(ctx, e); err == nil {
			n++
		}
	}
	return n
}

func (i *Indexer) nodeID(kind string, raw string) string {
	h := sha1.Sum([]byte(i.workspaceID + "|" + kind + "|" + raw))
	return kind + ":" + hex.EncodeToString(h[:])
}

func workspaceID(root string) string {
	h := sha1.Sum([]byte(root))
	return "ws:" + hex.EncodeToString(h[:])
}

func ResolveProjectIdentity(cfg *config.Config, root string) (string, error) {
	if cfg != nil && strings.TrimSpace(cfg.Project.IdentityOverride) != "" {
		return "project:" + strings.TrimSpace(cfg.Project.IdentityOverride), nil
	}
	gitTopLevel, err := gitRoot(root)
	if err == nil && gitTopLevel != "" {
		sum := sha1.Sum([]byte(gitTopLevel))
		return "project:" + hex.EncodeToString(sum[:]), nil
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve project identity root: %w", err)
	}
	sum := sha1.Sum([]byte(absRoot))
	return "project:" + hex.EncodeToString(sum[:]), nil
}

func gitRoot(root string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = root
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

// ShouldSkipDir reports whether a directory name should be excluded from
// indexing and filesystem watching. Exported for the syncer's watcher.
func ShouldSkipDir(name string) bool {
	return shouldSkipDir(name)
}

// IndexablePath reports whether a file path is eligible for indexing. Exported
// for the syncer's watcher to filter filesystem events.
func IndexablePath(path string) bool {
	return shouldIndexFile(path)
}

func shouldSkipDir(name string) bool {
	switch name {
	case ".git", ".svn", ".hg", "node_modules", "vendor", "dist", "build", ".next", ".turbo", ".raph":
		return true
	default:
		return false
	}
}

func shouldIndexFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go", ".md", ".markdown", ".txt", ".rst", ".json", ".yaml", ".yml":
		return true
	}
	return isTreeSitterFile(path)
}

func detectDomain(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".markdown", ".rst", ".txt":
		return "documentation"
	default:
		return "code"
	}
}

type section struct {
	Title   string
	Content string
}

func splitDocumentSections(content string) []section {
	lines := strings.Split(content, "\n")
	sections := make([]section, 0)
	current := section{Title: "Document"}

	flush := func() {
		trimmed := strings.TrimSpace(current.Content)
		if trimmed == "" {
			return
		}
		current.Content = trimmed
		sections = append(sections, current)
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			flush()
			current = section{Title: strings.TrimSpace(strings.TrimLeft(trimmed, "#"))}
			if current.Title == "" {
				current.Title = "Section"
			}
			continue
		}
		if current.Content != "" {
			current.Content += "\n"
		}
		current.Content += line
	}
	flush()

	if len(sections) == 0 {
		trimmed := strings.TrimSpace(content)
		if trimmed == "" {
			return nil
		}
		return []section{{Title: "Document", Content: trimmed}}
	}
	return sections
}

func snippetForNode(fset *token.FileSet, src []byte, start token.Pos, end token.Pos) string {
	file := fset.File(start)
	if file == nil {
		return ""
	}
	startOffset := file.Offset(start)
	endOffset := file.Offset(end)
	if startOffset < 0 || endOffset > len(src) || startOffset >= endOffset {
		return ""
	}
	return string(bytes.TrimSpace(src[startOffset:endOffset]))
}

func truncateRunes(s string, limit int) string {
	if limit <= 0 || len(s) == 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	out := make([]rune, 0, limit)
	for i, r := range s {
		if i >= len(s) {
			break
		}
		out = append(out, r)
		if len(out) == limit {
			break
		}
	}
	return string(out) + "\n..."
}

func splitFallbackChunks(content string, maxRunes int) []string {
	content = strings.TrimSpace(content)
	if content == "" || maxRunes <= 0 {
		return nil
	}
	runes := []rune(content)
	if len(runes) <= maxRunes {
		return []string{content}
	}
	chunks := make([]string, 0, (len(runes)/maxRunes)+1)
	for start := 0; start < len(runes); start += maxRunes {
		end := start + maxRunes
		if end > len(runes) {
			end = len(runes)
		}
		chunk := strings.TrimSpace(string(runes[start:end]))
		if chunk != "" {
			chunks = append(chunks, chunk)
		}
	}
	return chunks
}
