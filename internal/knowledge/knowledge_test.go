package knowledge

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"raph/internal/db"
	"raph/internal/project"
)

func newStore(t *testing.T) *db.LibSQLStore {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	store, err := db.InitStorage()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestAddChunksAndProperties(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	doc, err := Add(ctx, store, nil, AddInput{
		Workspace: "ws", Title: "Arch", DocType: DocArchitecture,
		Content: "# Overview\nFirst part.\n\n# Details\nSecond part.", NoEmbed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if doc.ChunkCount < 2 {
		t.Fatalf("expected the headed document to chunk into >=2 parts, got %d", doc.ChunkCount)
	}
	if doc.Node.Prop("doc_type") != DocArchitecture || doc.Node.Prop("status") != StatusFresh {
		t.Fatalf("doc properties not set: %+v", doc.Node.Properties)
	}
}

func TestReadMarksHandoffUsedOnce(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	doc, err := Add(ctx, store, nil, AddInput{Workspace: "ws", Title: "Handoff", DocType: DocHandoff, Content: "do the next thing", NoEmbed: true})
	if err != nil {
		t.Fatal(err)
	}

	// Peek without marking.
	peek, err := Read(ctx, store, doc.Node.ID, false, "agent-a")
	if err != nil {
		t.Fatal(err)
	}
	if peek.Node.Prop("status") != StatusFresh {
		t.Fatalf("peek should not mark used, got %q", peek.Node.Prop("status"))
	}

	// Claim it.
	claimed, err := Read(ctx, store, doc.Node.ID, true, "agent-a")
	if err != nil {
		t.Fatal(err)
	}
	if claimed.Node.Prop("status") != StatusUsed || claimed.Node.Prop("used_by") != "agent-a" {
		t.Fatalf("handoff not marked used: %+v", claimed.Node.Properties)
	}
}

func TestReadDoesNotMarkNonHandoff(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	doc, err := Add(ctx, store, nil, AddInput{Workspace: "ws", Title: "Ref", DocType: DocReference, Content: "a fact", NoEmbed: true})
	if err != nil {
		t.Fatal(err)
	}
	got, err := Read(ctx, store, doc.Node.ID, true, "agent")
	if err != nil {
		t.Fatal(err)
	}
	if got.Node.Prop("status") != StatusFresh {
		t.Fatalf("reference should stay fresh, got %q", got.Node.Prop("status"))
	}
}

func TestLinkSurfacesRelatedOnRead(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	// A code node to relate to.
	if err := store.SaveNode(ctx, db.Node{ID: "func:x", Workspace: "ws", Domain: "code", Type: "func", Name: "DoThing", Content: "func DoThing(){}"}); err != nil {
		t.Fatal(err)
	}
	doc, err := Add(ctx, store, nil, AddInput{Workspace: "ws", Title: "Note", DocType: DocNote, Content: "see DoThing", Links: []string{"func:x"}, NoEmbed: true})
	if err != nil {
		t.Fatal(err)
	}
	got, err := Read(ctx, store, doc.Node.ID, false, "agent")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range got.Related {
		if r.ID == "func:x" {
			found = true
		}
	}
	if !found {
		t.Fatalf("linked node not surfaced as related: %+v", got.Related)
	}
}

func TestListFiltersByType(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	for _, in := range []AddInput{
		{Workspace: "ws", Title: "A", DocType: DocArchitecture, Content: "a", NoEmbed: true},
		{Workspace: "ws", Title: "H", DocType: DocHandoff, Content: "h", NoEmbed: true},
	} {
		if _, err := Add(ctx, store, nil, in); err != nil {
			t.Fatal(err)
		}
	}
	got, err := List(ctx, store, ListFilter{Workspace: "ws", DocType: DocHandoff})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Prop("doc_type") != DocHandoff {
		t.Fatalf("type filter failed: %+v", got)
	}
}

func TestUpdateGuardsTypeTagsAndDocType(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()

	doc, err := Add(ctx, store, nil, AddInput{
		Workspace: "ws", Key: "k", Title: "T", Content: "body one",
		DocType: DocArchitecture, Tags: []string{"a", "b"}, NoEmbed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	id := doc.Node.ID

	// nil tags keeps existing tags and preserves doc_type.
	d1, err := Update(ctx, store, nil, UpdateInput{ID: id, Title: "T", Content: "body two"})
	if err != nil {
		t.Fatal(err)
	}
	if d1.Node.Prop("tags") != "a,b" {
		t.Fatalf("nil tags should keep existing, got %q", d1.Node.Prop("tags"))
	}
	if d1.Node.Prop("doc_type") != DocArchitecture {
		t.Fatalf("doc_type should be preserved, got %q", d1.Node.Prop("doc_type"))
	}

	// Explicit empty tags clears them.
	d2, err := Update(ctx, store, nil, UpdateInput{ID: id, Title: "T", Content: "body three", Tags: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(d2.Node.Prop("tags")) != "" {
		t.Fatalf("explicit empty tags should clear, got %q", d2.Node.Prop("tags"))
	}

	// A document with no doc_type defaults to note (not handoff) on update.
	if err := store.SaveNode(ctx, db.Node{
		ID: "doc:untyped", Workspace: "ws", Domain: DomainKnowledge, Type: TypeDoc,
		Name: "U", Content: "c", URL: "knowledge://ws/untyped",
	}); err != nil {
		t.Fatal(err)
	}
	d3, err := Update(ctx, store, nil, UpdateInput{ID: "doc:untyped", Content: "new"})
	if err != nil {
		t.Fatal(err)
	}
	if d3.Node.Prop("doc_type") != DocNote {
		t.Fatalf("untyped doc should default to note, got %q", d3.Node.Prop("doc_type"))
	}

	// Non-document nodes are refused.
	if err := store.SaveNode(ctx, db.Node{
		ID: "func:x", Workspace: "ws", Domain: "code", Type: "func",
		Name: "F", Content: "c", URL: "knowledge://ws/whatever",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := Update(ctx, store, nil, UpdateInput{ID: "func:x", Content: "x"}); err != sql.ErrNoRows {
		t.Fatalf("expected ErrNoRows updating a non-document, got %v", err)
	}
}

func seedDoc(t *testing.T, store db.GraphStore, workspace, key, title, content string) Document {
	t.Helper()
	doc, err := Add(context.Background(), store, nil, AddInput{
		Workspace: workspace, Key: key, Title: title, Content: content,
		DocType: DocHandoff, Source: "user", WriterID: "agent:test", NoEmbed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

// Moving a document must preserve it whole: content, lifecycle properties,
// chunks, relations, and its embedding — a migration that re-embedded would
// spend real API calls to reproduce vectors the graph already has.
func TestMigrateWorkspaceMovesDocumentsIntact(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	const old = "ws:legacy"
	const target = "knowledge:project:abc"

	if err := store.SaveNode(ctx, db.Node{ID: "related", Workspace: "other", Domain: "code", Type: "file", Name: "x.go"}); err != nil {
		t.Fatal(err)
	}
	doc, err := Add(ctx, store, nil, AddInput{
		Workspace: old, Key: "release/handoff", Title: "Release handoff",
		Content: strings.Repeat("durable handoff content. ", 200),
		DocType: DocHandoff, Source: "user", WriterID: "agent:test",
		Tags: []string{"release"}, Links: []string{"related"},
		Properties: map[string]string{"status": StatusUsed}, NoEmbed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Give the document and its chunks vectors, as a real one would have.
	stored := doc.Node
	stored.Embedding = []float32{1, 0}
	if err := store.SaveNode(ctx, stored); err != nil {
		t.Fatal(err)
	}
	full, err := Read(ctx, store, doc.Node.ID, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(full.Chunks) < 2 {
		t.Fatalf("expected a multi-chunk document, got %d", len(full.Chunks))
	}
	for _, c := range full.Chunks {
		c.Embedding = []float32{0, 1}
		if err := store.SaveNode(ctx, c); err != nil {
			t.Fatal(err)
		}
	}

	moved, err := MigrateWorkspace(ctx, store, old, target)
	if err != nil {
		t.Fatal(err)
	}
	if moved != 1 {
		t.Fatalf("expected 1 document moved, got %d", moved)
	}

	// Gone from the old bucket, present in the new one.
	if remaining, err := List(ctx, store, ListFilter{Workspace: old}); err != nil || len(remaining) != 0 {
		t.Fatalf("expected the legacy bucket emptied, got %d (%v)", len(remaining), err)
	}
	docs, err := List(ctx, store, ListFilter{Workspace: target})
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected the document in the new bucket, got %d", len(docs))
	}

	migrated, err := Read(ctx, store, docs[0].ID, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if migrated.Node.Content != stored.Content {
		t.Fatal("content changed during migration")
	}
	if migrated.Node.Prop("status") != StatusUsed {
		t.Fatalf("lifecycle metadata lost: %+v", migrated.Node.Properties)
	}
	if migrated.Node.Prop("writer_id") != "agent:test" || migrated.Node.Prop("doc_type") != DocHandoff {
		t.Fatalf("document properties lost: %+v", migrated.Node.Properties)
	}
	// Reads report an embedding's length rather than its vector, so that is
	// what proves the vector survived the move.
	if migrated.Node.EmbeddingLength != 2 {
		t.Fatalf("document embedding not carried across: length %d", migrated.Node.EmbeddingLength)
	}
	if len(migrated.Chunks) != len(full.Chunks) {
		t.Fatalf("expected %d chunks, got %d", len(full.Chunks), len(migrated.Chunks))
	}
	for _, c := range migrated.Chunks {
		if c.EmbeddingLength != 2 {
			t.Fatalf("chunk embedding not carried across: %s has length %d", c.ID, c.EmbeddingLength)
		}
	}
	related := false
	for _, rel := range migrated.Related {
		if rel.ID == "related" {
			related = true
		}
	}
	if !related {
		t.Fatalf("relation edge lost during migration: %+v", migrated.Related)
	}

	// A later write for the same key must update the migrated document rather
	// than mint a second one — the reason ids are recomputed, not relabelled.
	if _, err := Add(ctx, store, nil, AddInput{
		Workspace: target, Key: "release/handoff", Title: "Release handoff",
		Content: "revised", DocType: DocHandoff, Source: "user", NoEmbed: true,
	}); err != nil {
		t.Fatal(err)
	}
	docs, err = List(ctx, store, ListFilter{Workspace: target})
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected the re-write to update the migrated document, found %d", len(docs))
	}
}

func TestMigrateWorkspaceIsNoOpForSameOrEmptyTarget(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	seedDoc(t, store, "ws:legacy", "k", "T", "content")

	for _, tc := range [][2]string{{"ws:legacy", "ws:legacy"}, {"", "knowledge:project:x"}, {"ws:legacy", ""}} {
		moved, err := MigrateWorkspace(ctx, store, tc[0], tc[1])
		if err != nil {
			t.Fatal(err)
		}
		if moved != 0 {
			t.Fatalf("expected no move for %v, got %d", tc, moved)
		}
	}
	if docs, err := List(ctx, store, ListFilter{Workspace: "ws:legacy"}); err != nil || len(docs) != 1 {
		t.Fatalf("expected the document untouched, got %d (%v)", len(docs), err)
	}
}

func TestMigrateLegacyProjectDocsUsesKnownRootsAndPreservesUnknown(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	root := t.TempDir()

	known := "ws:known"
	unknown := "ws:deadbeef"
	seedDoc(t, store, known, "a", "Known", "content a")
	seedDoc(t, store, unknown, "b", "Unknown", "content b")
	// Global documents are not project-scoped and must not be touched.
	seedDoc(t, store, GlobalWorkspace, "c", "Global", "content c")

	stats, err := MigrateLegacyProjectDocs(ctx, store, nil, map[string]string{known: root})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Documents != 2 || stats.Workspaces != 2 {
		t.Fatalf("expected both project documents moved, got %+v", stats)
	}

	projectID, err := project.ID(nil, root)
	if err != nil {
		t.Fatal(err)
	}
	if docs, err := List(ctx, store, ListFilter{Workspace: ProjectWorkspace(projectID)}); err != nil || len(docs) != 1 {
		t.Fatalf("expected the known-root document under its resolved project, got %d (%v)", len(docs), err)
	}
	// An unresolvable bucket keeps its digest so nothing is lost, and lands
	// where that directory's memories already live.
	if docs, err := List(ctx, store, ListFilter{Workspace: ProjectWorkspace("project:deadbeef")}); err != nil || len(docs) != 1 {
		t.Fatalf("expected the unknown-root document preserved by digest, got %d (%v)", len(docs), err)
	}
	if docs, err := List(ctx, store, ListFilter{Workspace: GlobalWorkspace}); err != nil || len(docs) != 1 {
		t.Fatalf("global documents must not be migrated, got %d (%v)", len(docs), err)
	}

	// Re-running must be a no-op.
	again, err := MigrateLegacyProjectDocs(ctx, store, nil, map[string]string{known: root})
	if err != nil {
		t.Fatal(err)
	}
	if again.Documents != 0 {
		t.Fatalf("expected a repeat migration to move nothing, got %+v", again)
	}
}
