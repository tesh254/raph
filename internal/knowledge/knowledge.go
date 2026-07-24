// Package knowledge manages local documents attached to the graph: architecture
// notes, handoffs, references, and user-supplied facts. Documents carry typed
// properties so an agent can distinguish durable context (architecture) from
// transient work transfer (handoff), are chunked for retrieval, and are linked
// to other nodes so related material is one hop away instead of another search.
package knowledge

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"raph/internal/config"
	"raph/internal/db"
)

const (
	DomainKnowledge = "knowledge"
	TypeDoc         = "doc"
	TypeDocChunk    = "doc_chunk"

	// GlobalWorkspace holds documents not tied to a specific codebase.
	GlobalWorkspace = "ws:global-knowledge"

	// Edge relation types.
	RelHasChunk  = "HAS_CHUNK"
	RelRelatesTo = "RELATES_TO"

	// Doc lifecycle/status values.
	StatusFresh = "fresh"
	StatusStale = "stale"
	StatusUsed  = "used"

	maxChunkRunes = 1800
)

// DocType values describe what role a document plays. Free-form strings are
// allowed, but these are the well-known kinds agents reason about.
const (
	DocArchitecture = "architecture"
	DocHandoff      = "handoff"
	DocReference    = "reference"
	DocNote         = "note"
)

type AddInput struct {
	Workspace  string // empty -> GlobalWorkspace
	Key        string // stable key; defaults to slug(Title)
	Title      string
	Content    string
	DocType    string // architecture, handoff, reference, note, ...
	Source     string // local, user, web, ...
	WriterID   string
	Tags       []string
	Links      []string // node ids to relate this doc to
	Properties map[string]string
	NoEmbed    bool
}

type Document struct {
	Node       db.Node   `json:"node"`
	Chunks     []db.Node `json:"chunks,omitempty"`
	Related    []db.Node `json:"related,omitempty"`
	ChunkCount int       `json:"chunk_count"`
}

// Add creates or replaces a document, its chunk children, and any relation
// edges to other nodes.
func Add(ctx context.Context, store db.GraphStore, cfg *config.Config, in AddInput) (Document, error) {
	title := strings.TrimSpace(in.Title)
	content := strings.TrimSpace(in.Content)
	if content == "" {
		return Document{}, fmt.Errorf("content is required")
	}
	if title == "" {
		title = preview(content, 60)
	}
	workspace := strings.TrimSpace(in.Workspace)
	if workspace == "" {
		workspace = GlobalWorkspace
	}
	key := strings.TrimSpace(in.Key)
	if key == "" {
		key = slugify(title)
	}
	docType := strings.TrimSpace(in.DocType)
	if docType == "" {
		docType = DocNote
	}
	source := strings.TrimSpace(in.Source)
	if source == "" {
		source = "local"
	}
	now := time.Now().UTC().Format(time.RFC3339)

	props := map[string]string{}
	for k, v := range in.Properties {
		props[k] = v
	}
	props["doc_type"] = docType
	props["source"] = source
	// Preserve incoming lifecycle metadata (e.g. a handoff imported as already
	// `used`) instead of resetting it to fresh — resetting would resurrect a
	// claimed handoff while its stale used_at/used_by fields still say otherwise.
	// Default to fresh only when the caller supplied no status.
	if strings.TrimSpace(props["status"]) == "" {
		props["status"] = StatusFresh
	}
	if strings.TrimSpace(props["freshness"]) == "" {
		props["freshness"] = now
	}
	if w := strings.TrimSpace(in.WriterID); w != "" {
		props["writer_id"] = w
	}
	if len(in.Tags) > 0 {
		props["tags"] = strings.Join(in.Tags, ",")
	}

	docID := nodeID(TypeDoc, workspace+"|"+key)
	docNode := db.Node{
		ID:         docID,
		Workspace:  workspace,
		Domain:     DomainKnowledge,
		Type:       TypeDoc,
		Name:       title,
		Content:    content,
		URL:        "knowledge://" + workspace + "/" + key,
		Properties: props,
	}
	if !in.NoEmbed {
		if emb := embed(ctx, cfg, title+"\n\n"+content); len(emb) > 0 {
			docNode.Embedding = emb
		}
	}
	if err := store.SaveNode(ctx, docNode); err != nil {
		return Document{}, fmt.Errorf("save doc: %w", err)
	}

	// Replace chunk children: re-derive them from current content.
	chunks := chunk(content)
	newChunkIDs := make(map[string]struct{}, len(chunks))
	for idx, c := range chunks {
		chunkNode := db.Node{
			ID:        nodeID(TypeDocChunk, fmt.Sprintf("%s|%s|%d", workspace, key, idx)),
			Workspace: workspace,
			Domain:    DomainKnowledge,
			Type:      TypeDocChunk,
			Name:      fmt.Sprintf("%s chunk %d", title, idx+1),
			Content:   c,
			URL:       docNode.URL + fmt.Sprintf("#chunk-%d", idx+1),
			Properties: map[string]string{
				"doc_type": docType,
				"doc_id":   docID,
			},
		}
		newChunkIDs[chunkNode.ID] = struct{}{}
		if !in.NoEmbed {
			if emb := embed(ctx, cfg, c); len(emb) > 0 {
				chunkNode.Embedding = emb
			}
		}
		if err := store.SaveNode(ctx, chunkNode); err != nil {
			return Document{}, fmt.Errorf("save chunk: %w", err)
		}
		if err := store.SaveEdge(ctx, db.Edge{SourceID: docID, TargetID: chunkNode.ID, Type: RelHasChunk}); err != nil {
			return Document{}, fmt.Errorf("link chunk: %w", err)
		}
	}

	// Prune stale chunks left over from a previous, longer version of this doc.
	// Chunk IDs are deterministic per (workspace,key,index), so overwriting only
	// covers indices [0,len(chunks)); anything beyond would otherwise linger in
	// FTS/vector search with contradicted content. DeleteNodeByID also clears the
	// HAS_CHUNK edge.
	existing, err := store.ListNodes(ctx, db.NodeFilter{
		Workspace:      workspace,
		Types:          []string{TypeDocChunk},
		PropertyEquals: map[string]string{"doc_id": docID},
		Lean:           true,
		Limit:          10000,
	})
	if err != nil {
		return Document{}, fmt.Errorf("list existing chunks: %w", err)
	}
	for _, old := range existing {
		if _, keep := newChunkIDs[old.ID]; keep {
			continue
		}
		if err := store.DeleteNodeByID(ctx, old.ID); err != nil {
			return Document{}, fmt.Errorf("prune stale chunk %s: %w", old.ID, err)
		}
	}

	for _, target := range in.Links {
		target = strings.TrimSpace(target)
		if target == "" {
			continue
		}
		if err := store.SaveEdge(ctx, db.Edge{SourceID: docID, TargetID: target, Type: RelRelatesTo}); err != nil {
			return Document{}, fmt.Errorf("link related node %s: %w", target, err)
		}
	}

	saved, err := store.GetNodeByID(ctx, docID)
	if err != nil {
		return Document{}, err
	}
	return Document{Node: saved, ChunkCount: len(chunks)}, nil
}

type ListFilter struct {
	Workspace string
	DocType   string
	Status    string
	Query     string
	Limit     int
}

// UpdateInput edits an existing document in place, keyed by its node id.
type UpdateInput struct {
	ID      string
	Title   string
	Content string
	Tags    []string // nil keeps the document's current tags
}

// Update rewrites a document's title/content/tags while preserving its
// identity (workspace + key), doc_type, source, writer, and lifecycle metadata
// (status/used_at/used_by/freshness). Returns sql.ErrNoRows when the id is
// unknown.
func Update(ctx context.Context, store db.GraphStore, cfg *config.Config, in UpdateInput) (Document, error) {
	node, err := store.GetNodeByID(ctx, strings.TrimSpace(in.ID))
	if err != nil {
		return Document{}, err
	}
	// A doc's stable key lives in its URL: knowledge://<workspace>/<key>.
	key := strings.TrimPrefix(node.URL, "knowledge://"+node.Workspace+"/")
	if key == "" || key == node.URL {
		return Document{}, fmt.Errorf("cannot resolve document key for %s", in.ID)
	}
	docType := node.Prop("doc_type")
	if docType == "" {
		docType = DocHandoff
	}
	tags := in.Tags
	if len(tags) == 0 {
		if existing := strings.TrimSpace(node.Prop("tags")); existing != "" {
			tags = strings.Split(existing, ",")
		}
	}
	// Carry over all existing properties so lifecycle metadata survives the
	// edit; Add overrides the fields it manages.
	props := make(map[string]string, len(node.Properties))
	for k, v := range node.Properties {
		props[k] = v
	}
	return Add(ctx, store, cfg, AddInput{
		Workspace:  node.Workspace,
		Key:        key,
		Title:      in.Title,
		Content:    in.Content,
		DocType:    docType,
		Source:     node.Prop("source"),
		WriterID:   node.Prop("writer_id"),
		Tags:       tags,
		Properties: props,
	})
}

// Delete removes a document and its chunk children atomically. Returns
// sql.ErrNoRows when the id is not a document.
func Delete(ctx context.Context, store db.GraphStore, id string) error {
	return store.DeleteDocumentNode(ctx, strings.TrimSpace(id))
}

func List(ctx context.Context, store db.GraphStore, f ListFilter) ([]db.Node, error) {
	props := map[string]string{}
	if t := strings.TrimSpace(f.DocType); t != "" {
		props["doc_type"] = t
	}
	if s := strings.TrimSpace(f.Status); s != "" {
		props["status"] = s
	}
	return store.ListNodes(ctx, db.NodeFilter{
		Workspace:      strings.TrimSpace(f.Workspace),
		Types:          []string{TypeDoc},
		PropertyEquals: props,
		Query:          f.Query,
		Limit:          f.Limit,
	})
}

// Read returns a document with its chunks and related nodes. When markUsed is
// true and the document is a handoff, its status is flipped to "used" so the
// next agent knows the work has been picked up.
func Read(ctx context.Context, store db.GraphStore, id string, markUsed bool, readerID string) (Document, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Document{}, fmt.Errorf("document id is required")
	}
	node, err := store.GetNodeByID(ctx, id)
	if err != nil {
		return Document{}, err
	}

	if markUsed && node.Prop("doc_type") == DocHandoff && node.Prop("status") != StatusUsed {
		update := map[string]string{
			"status":  StatusUsed,
			"used_at": time.Now().UTC().Format(time.RFC3339),
		}
		if r := strings.TrimSpace(readerID); r != "" {
			update["used_by"] = r
		}
		if err := store.SetNodeProperties(ctx, id, update); err != nil {
			return Document{}, fmt.Errorf("mark handoff used: %w", err)
		}
		node, err = store.GetNodeByID(ctx, id)
		if err != nil {
			return Document{}, err
		}
	}

	doc := Document{Node: node}
	neighbors, _, err := store.GetNeighbors(ctx, id)
	if err != nil {
		return Document{}, err
	}
	for _, n := range neighbors {
		if n.Type == TypeDocChunk {
			doc.Chunks = append(doc.Chunks, n)
		} else {
			doc.Related = append(doc.Related, n)
		}
	}
	doc.ChunkCount = len(doc.Chunks)
	return doc, nil
}

// Link relates two nodes with a relation type (default RELATES_TO).
func Link(ctx context.Context, store db.GraphStore, from, to, rel string) error {
	from = strings.TrimSpace(from)
	to = strings.TrimSpace(to)
	if from == "" || to == "" {
		return fmt.Errorf("both from and to node ids are required")
	}
	rel = strings.TrimSpace(rel)
	if rel == "" {
		rel = RelRelatesTo
	}
	return store.SaveEdge(ctx, db.Edge{SourceID: from, TargetID: to, Type: rel})
}

// chunk splits document content on markdown headings, further splitting any
// section that exceeds the chunk size.
func chunk(content string) []string {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil
	}
	var sections []string
	var current strings.Builder
	flush := func() {
		if s := strings.TrimSpace(current.String()); s != "" {
			sections = append(sections, s)
		}
		current.Reset()
	}
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			flush()
		}
		current.WriteString(line)
		current.WriteString("\n")
	}
	flush()
	if len(sections) == 0 {
		sections = []string{content}
	}

	var out []string
	for _, s := range sections {
		runes := []rune(s)
		if len(runes) <= maxChunkRunes {
			out = append(out, s)
			continue
		}
		for start := 0; start < len(runes); start += maxChunkRunes {
			end := start + maxChunkRunes
			if end > len(runes) {
				end = len(runes)
			}
			piece := strings.TrimSpace(string(runes[start:end]))
			if piece != "" {
				out = append(out, piece)
			}
		}
	}
	return out
}

func embed(ctx context.Context, cfg *config.Config, text string) []float32 {
	if cfg == nil || !cfg.HasEmbeddingProvider() {
		return nil
	}
	emb, err := config.GenerateEmbedding(ctx, cfg, text)
	if err != nil {
		return nil
	}
	return emb
}

func nodeID(kind, raw string) string {
	h := sha1.Sum([]byte(kind + "|" + raw))
	return kind + ":" + hex.EncodeToString(h[:])
}

func slugify(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash && b.Len() > 0 {
				b.WriteRune('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "doc"
	}
	if len(out) > 60 {
		out = strings.Trim(out[:60], "-")
	}
	return out
}

func preview(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "..."
}
