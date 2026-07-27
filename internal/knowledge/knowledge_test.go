package knowledge

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"raph/internal/db"
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
