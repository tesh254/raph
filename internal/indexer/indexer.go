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
}

type Indexer struct {
	store          db.GraphStore
	cfg            *config.Config
	root           string
	workspaceID    string
	projectID      string
	skipEmbeddings bool
}

func New(store db.GraphStore, cfg *config.Config, root string, skipEmbeddings bool) (*Indexer, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve root path: %w", err)
	}

	projectID, err := ResolveProjectIdentity(cfg, absRoot)
	if err != nil {
		return nil, err
	}

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

	if err := i.store.DeleteWorkspace(ctx, i.workspaceID); err != nil {
		return stats, fmt.Errorf("clear existing workspace graph: %w", err)
	}

	err := filepath.WalkDir(i.root, func(path string, d fs.DirEntry, walkErr error) error {
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
		if err := i.indexFile(ctx, path, &stats); err != nil {
			return fmt.Errorf("index %s: %w", path, err)
		}
		return nil
	})
	if err != nil {
		return stats, err
	}

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
		return nil
	}

	contentBytes, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	content := string(contentBytes)
	relPath, err := filepath.Rel(i.root, path)
	if err != nil {
		return err
	}
	relPath = filepath.ToSlash(relPath)

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
		if err := i.indexFallbackChunks(ctx, relPath, fileNode.ID, content, stats); err != nil {
			return err
		}
	}

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
			snippet := snippetForNode(fset, content, d.Pos(), d.End())
			node := db.Node{
				ID:        i.nodeID("func", relPath+"::"+name),
				Workspace: i.workspaceID,
				Domain:    "code",
				Type:      "func",
				Name:      name,
				Content:   truncateRunes(snippet, maxStoredContent),
				URL:       relPath + "#" + name,
				Path:      i.root,
			}
			embedding, err := i.embed(ctx, name+"\n\n"+snippet, stats)
			if err != nil {
				return fmt.Errorf("embed function %q: %w", name, err)
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
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				name := ts.Name.Name
				snippet := snippetForNode(fset, content, ts.Pos(), ts.End())
				node := db.Node{
					ID:        i.nodeID("type", relPath+"::"+name),
					Workspace: i.workspaceID,
					Domain:    "code",
					Type:      "type",
					Name:      name,
					Content:   truncateRunes(snippet, maxStoredContent),
					URL:       relPath + "#" + name,
					Path:      i.root,
				}
				embedding, err := i.embed(ctx, name+"\n\n"+snippet, stats)
				if err != nil {
					return fmt.Errorf("embed type %q: %w", name, err)
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
			}
		}
	}
	return nil
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

func shouldSkipDir(name string) bool {
	switch name {
	case ".git", ".svn", ".hg", "node_modules", "vendor", "dist", "build", ".next", ".turbo", ".raph":
		return true
	default:
		return false
	}
}

func shouldIndexFile(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go", ".md", ".markdown", ".txt", ".rst", ".json", ".yaml", ".yml":
		return true
	default:
		return false
	}
}

func detectDomain(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return "code"
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
