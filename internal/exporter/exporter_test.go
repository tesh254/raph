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
	if !strings.Contains(art.Content, "\"documents\"") || art.Bytes == 0 {
		t.Fatalf("bundle json malformed: %s", art.Content)
	}
}
