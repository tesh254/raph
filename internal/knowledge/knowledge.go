// Package knowledge manages local documents attached to the graph: architecture
// notes, handoffs, references, and user-supplied facts. Documents carry typed
// properties so an agent can distinguish durable context (architecture) from
// transient work transfer (handoff), are chunked for retrieval, and are linked
// to other nodes so related material is one hop away instead of another search.
package knowledge

import (
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"raph/internal/config"
	"raph/internal/db"
	"raph/internal/project"
	"raph/internal/verbose"
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
	// Only documents are updatable through this path — mirrors the delete guard.
	// Without it, editing a chunk or other node would create a bogus document
	// under a key derived from its URL. sql.ErrNoRows so callers map to 404.
	if node.Type != TypeDoc {
		return Document{}, sql.ErrNoRows
	}
	// A doc's stable key lives in its URL: knowledge://<workspace>/<key>.
	key := strings.TrimPrefix(node.URL, "knowledge://"+node.Workspace+"/")
	if key == "" || key == node.URL {
		return Document{}, fmt.Errorf("cannot resolve document key for %s", in.ID)
	}
	// Match Add's default so a content-only edit of an untyped document doesn't
	// silently reclassify it as a handoff.
	docType := node.Prop("doc_type")
	if docType == "" {
		docType = DocNote
	}
	// Carry over all existing properties so lifecycle metadata survives the edit.
	props := make(map[string]string, len(node.Properties))
	for k, v := range node.Properties {
		props[k] = v
	}
	// Tags: nil means "keep current"; a non-nil (possibly empty) slice replaces
	// them — an explicit empty slice clears the tags, so drop the carried-over
	// property (Add won't re-set it for an empty list).
	tags := in.Tags
	if in.Tags == nil {
		if existing := strings.TrimSpace(node.Prop("tags")); existing != "" {
			tags = strings.Split(existing, ",")
		}
	} else if len(in.Tags) == 0 {
		delete(props, "tags")
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

// ProjectWorkspace returns the bucket documents belonging to a project live in.
//
// It is deliberately NOT the indexer's workspace id. Those two used to be the
// same value, which made `raph init` destroy documents: a full index run clears
// its workspace wholesale, and a handoff is user-authored knowledge that nothing
// regenerates. Keying off the project identity also means one bucket per
// project rather than one per directory, so a document written from a
// subdirectory is visible from the repository root — the rule memory already
// follows.
func ProjectWorkspace(projectID string) string {
	return "knowledge:" + strings.TrimSpace(projectID)
}

// IsLegacyProjectWorkspace reports whether a workspace id is an indexer
// workspace that documents were stored under before they got their own bucket.
func IsLegacyProjectWorkspace(workspace string) bool {
	workspace = strings.TrimSpace(workspace)
	return strings.HasPrefix(workspace, "ws:") && workspace != GlobalWorkspace
}

// nodeRelocator is the store capability a workspace migration needs: re-keying
// a node so its id matches where it now lives.
type nodeRelocator interface {
	RelocateNode(ctx context.Context, oldID, newID, newWorkspace, newURL string) error
}

// MigrateWorkspace moves every document in oldWorkspace to newWorkspace,
// preserving content, properties, embeddings, chunks, and relation edges.
//
// Documents are re-keyed rather than relabelled because a document's node id is
// derived from its workspace and key: leaving the id alone would make the next
// write for that key mint a second node instead of updating this one.
func MigrateWorkspace(ctx context.Context, store db.GraphStore, oldWorkspace, newWorkspace string) (int, error) {
	oldWorkspace = strings.TrimSpace(oldWorkspace)
	newWorkspace = strings.TrimSpace(newWorkspace)
	if oldWorkspace == "" || newWorkspace == "" || oldWorkspace == newWorkspace {
		return 0, nil
	}
	relocator, ok := store.(nodeRelocator)
	if !ok {
		return 0, fmt.Errorf("store cannot relocate nodes")
	}

	moved := 0
	// Documents that cannot move stay in the source workspace, so the same page
	// would be re-read forever without remembering them.
	skipped := map[string]bool{}
	for {
		docs, err := store.ListNodes(ctx, db.NodeFilter{
			Workspace: oldWorkspace,
			Types:     []string{TypeDoc},
			Lean:      true,
			Limit:     migrationPageSize,
		})
		if err != nil {
			return moved, fmt.Errorf("list documents in %s: %w", oldWorkspace, err)
		}
		if len(docs) == 0 {
			return moved, nil
		}

		progressed := false
		for _, old := range docs {
			if err := ctx.Err(); err != nil {
				return moved, err
			}
			if skipped[old.ID] {
				continue
			}
			key := keyFromURL(old.URL, oldWorkspace)
			if key == "" {
				return moved, fmt.Errorf("document %s has no recoverable key (url %q)", old.ID, old.URL)
			}
			newDocID := nodeID(TypeDoc, newWorkspace+"|"+key)
			newDocURL := "knowledge://" + newWorkspace + "/" + key

			// Check the destination before moving anything. Chunks relocate
			// first, so a document whose key is already taken would otherwise
			// fail partway through — after some chunks had already been moved
			// under the destination document's id.
			if _, err := store.GetNodeByID(ctx, newDocID); err == nil {
				verbose.Printf("skipping document %s: %s already holds key %q", old.ID, newWorkspace, key)
				skipped[old.ID] = true
				continue
			}

			// Chunks first, while their doc_id property still points at the
			// original: that property is how they are found. Paged rather than
			// capped — a fixed limit would silently strand the tail of a long
			// document in the old bucket, outside project-scoped retrieval.
			for {
				chunks, err := store.ListNodes(ctx, db.NodeFilter{
					Workspace:      oldWorkspace,
					Types:          []string{TypeDocChunk},
					PropertyEquals: map[string]string{"doc_id": old.ID},
					Lean:           true,
					Limit:          migrationPageSize,
				})
				if err != nil {
					return moved, fmt.Errorf("list chunks of %s: %w", old.ID, err)
				}
				if len(chunks) == 0 {
					break
				}
				relocated := 0
				for _, chunkNode := range chunks {
					idx := chunkIndexFromURL(chunkNode.URL)
					if idx < 0 {
						continue
					}
					newChunkID := nodeID(TypeDocChunk, fmt.Sprintf("%s|%s|%d", newWorkspace, key, idx))
					newChunkURL := newDocURL + fmt.Sprintf("#chunk-%d", idx+1)
					if err := relocator.RelocateNode(ctx, chunkNode.ID, newChunkID, newWorkspace, newChunkURL); err != nil {
						return moved, fmt.Errorf("relocate chunk %s: %w", chunkNode.ID, err)
					}
					// Repointed immediately after the move: a run interrupted
					// between the two would otherwise leave a chunk in the new
					// bucket still claiming the old document, and the query that
					// finds chunks (by doc_id, in the OLD workspace) would never
					// see it again to fix it.
					if err := store.SetNodeProperties(ctx, newChunkID, map[string]string{"doc_id": newDocID}); err != nil {
						return moved, fmt.Errorf("repoint chunk %s at its document: %w", newChunkID, err)
					}
					relocated++
				}
				// Relocated chunks leave this workspace, so the same query
				// returns the next batch. A page that moved nothing would
				// otherwise repeat forever.
				if relocated == 0 {
					break
				}
			}

			if err := relocator.RelocateNode(ctx, old.ID, newDocID, newWorkspace, newDocURL); err != nil {
				// A document already occupying that key in the destination is a
				// conflict, not a failure: skip it so the remaining legacy
				// documents still migrate instead of the whole run aborting on
				// the first collision.
				if isDuplicateKeyErr(err) {
					verbose.Printf("skipping document %s: %s already holds key %q", old.ID, newWorkspace, key)
					skipped[old.ID] = true
					continue
				}
				return moved, fmt.Errorf("relocate document %s: %w", old.ID, err)
			}
			moved++
			progressed = true
		}
		if !progressed {
			return moved, nil
		}
	}
}

// isDuplicateKeyErr reports whether a write failed because the destination row
// already exists.
func isDuplicateKeyErr(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique constraint") || strings.Contains(msg, "primary key")
}

// chunkIndexFromURL recovers a chunk's position from its url suffix
// ("...#chunk-N", 1-based), returning a 0-based index or -1.
func chunkIndexFromURL(url string) int {
	const marker = "#chunk-"
	idx := strings.LastIndex(url, marker)
	if idx < 0 {
		return -1
	}
	n, err := strconv.Atoi(url[idx+len(marker):])
	if err != nil || n < 1 {
		return -1
	}
	return n - 1
}

// keyFromURL recovers a document's stable key from its url
// ("knowledge://<workspace>/<key>"), which is where Add encodes it.
//
// The workspace is required to locate the boundary: keys are routinely
// path-like ("release/handoff"), so taking the last url segment would silently
// truncate them — and a truncated key re-keys the document to an id that later
// writes for the real key never match, duplicating it instead of updating.
func keyFromURL(url, workspace string) string {
	prefix := "knowledge://" + strings.TrimSpace(workspace) + "/"
	url = strings.TrimSpace(url)
	if rest, ok := strings.CutPrefix(url, prefix); ok {
		return rest
	}
	return ""
}

const migrationPageSize = 200

// MigrationStats reports what a legacy document migration moved.
type MigrationStats struct {
	Workspaces int `json:"workspaces"`
	Documents  int `json:"documents"`
}

// MigrateLegacyProjectDocs relocates documents written before project documents
// got their own bucket, when they shared the indexer's workspace id.
//
// workspaceRoots maps an indexer workspace id to the root it was indexed from,
// which is how a legacy bucket is resolved to a project. A bucket with no known
// root (documents written for a directory that was never indexed) cannot be
// un-hashed, so its digest is carried over as-is: that is exactly right when the
// document was written from the project root — the common case, and the same
// digest its memories already use — and no worse than the status quo otherwise.
// Nothing is dropped either way.
func MigrateLegacyProjectDocs(ctx context.Context, store db.GraphStore, cfg *config.Config, workspaceRoots map[string]string) (MigrationStats, error) {
	var stats MigrationStats

	legacy := map[string]struct{}{}
	for offset := 0; ; offset += migrationPageSize {
		docs, err := store.ListNodes(ctx, db.NodeFilter{
			Types:  []string{TypeDoc},
			Lean:   true,
			Limit:  migrationPageSize,
			Offset: offset,
		})
		if err != nil {
			return stats, fmt.Errorf("list documents: %w", err)
		}
		for _, doc := range docs {
			if IsLegacyProjectWorkspace(doc.Workspace) {
				legacy[doc.Workspace] = struct{}{}
			}
		}
		if len(docs) < migrationPageSize {
			break
		}
	}

	for old := range legacy {
		if err := ctx.Err(); err != nil {
			return stats, err
		}
		var target string
		if root, ok := workspaceRoots[old]; ok && strings.TrimSpace(root) != "" {
			projectID, err := project.ID(cfg, root)
			if err != nil {
				return stats, fmt.Errorf("resolve project for %s: %w", root, err)
			}
			target = ProjectWorkspace(projectID)
		} else {
			target = ProjectWorkspace("project:" + strings.TrimPrefix(old, "ws:"))
		}

		moved, err := MigrateWorkspace(ctx, store, old, target)
		if err != nil {
			return stats, err
		}
		if moved > 0 {
			stats.Workspaces++
			stats.Documents += moved
		}
	}
	return stats, nil
}
