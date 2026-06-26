package exporter

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"raph/internal/db"
	"raph/internal/knowledge"
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

func TestDocumentMarkdownAndWrite(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	doc, err := knowledge.Add(ctx, store, nil, knowledge.AddInput{
		Workspace: "ws", Title: "My Arch", DocType: knowledge.DocArchitecture,
		Content: "the design", NoEmbed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	art, err := Document(ctx, store, doc.Node.ID, FormatMarkdown)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(art.Content, "doc_type: architecture") || !strings.Contains(art.Content, "# My Arch") {
		t.Fatalf("markdown export missing frontmatter/title:\n%s", art.Content)
	}

	dir := t.TempDir()
	target, err := Write(art, dir)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(target) != dir {
		t.Fatalf("expected file written into %s, got %s", dir, target)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("export file missing: %v", err)
	}
}

func TestBundleJSON(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	for _, in := range []knowledge.AddInput{
		{Workspace: "ws", Title: "A", DocType: knowledge.DocNote, Content: "a", NoEmbed: true},
		{Workspace: "ws", Title: "B", DocType: knowledge.DocNote, Content: "b", NoEmbed: true},
	} {
		if _, err := knowledge.Add(ctx, store, nil, in); err != nil {
			t.Fatal(err)
		}
	}
	art, err := Bundle(ctx, store, "ws", FormatJSON)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(art.Content, "\"nodes\"") ||
		!strings.Contains(art.Content, "\"raph_export_version\"") || art.Bytes == 0 {
		t.Fatalf("bundle json malformed: %s", art.Content)
	}
}

// TestExportImportRoundTrip exports a bundle to JSON and imports it into a fresh
// store, confirming the documents come back with their content and doc_type.
func TestExportImportRoundTrip(t *testing.T) {
	ctx := context.Background()
	src := newStore(t)
	for _, in := range []knowledge.AddInput{
		{Workspace: "ws", Title: "Arch", DocType: knowledge.DocArchitecture, Content: "the design doc", NoEmbed: true},
		{Workspace: "ws", Title: "Handoff", DocType: knowledge.DocHandoff, Content: "the handoff notes", NoEmbed: true},
	} {
		if _, err := knowledge.Add(ctx, src, nil, in); err != nil {
			t.Fatal(err)
		}
	}
	art, err := Bundle(ctx, src, "ws", FormatJSON)
	if err != nil {
		t.Fatal(err)
	}

	dst := newStore(t)
	res, err := Import(ctx, dst, nil, "ws2", []byte(art.Content), true)
	if err != nil {
		t.Fatal(err)
	}
	if res.Documents != 2 {
		t.Fatalf("expected 2 documents imported, got %d", res.Documents)
	}
	docs, err := knowledge.List(ctx, dst, knowledge.ListFilter{Workspace: "ws2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 {
		t.Fatalf("expected 2 docs in target workspace, got %d", len(docs))
	}
	var found bool
	for _, d := range docs {
		if d.Name == "Arch" && strings.Contains(d.Content, "the design doc") && d.Prop("doc_type") == knowledge.DocArchitecture {
			found = true
		}
	}
	if !found {
		t.Fatalf("imported Arch doc not found intact: %+v", docs)
	}
}

// TestParseEnvelopeRejectsNewer guards the version gate.
func TestParseEnvelopeRejectsNewer(t *testing.T) {
	_, err := ParseEnvelope([]byte(`{"raph_export_version":9999,"kind":"bundle","nodes":[{"id":"x"}]}`))
	if err == nil {
		t.Fatal("expected newer-version envelope to be rejected")
	}
}
