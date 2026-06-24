package query

import (
	"context"
	"testing"

	"raph/internal/db"
)

func seedStore(t *testing.T) *db.LibSQLStore {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	store, err := db.InitStorage()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	nodes := []db.Node{
		{ID: "f1", Workspace: "ws", Domain: "code", Type: "func", Name: "OpenDatabase", Content: "open a sqlite database connection"},
		{ID: "f2", Workspace: "ws", Domain: "code", Type: "func", Name: "ParseFlags", Content: "parse command line flags"},
		{ID: "file1", Workspace: "ws", Domain: "code", Type: "file", Name: "db.go", Content: "database layer with OpenDatabase helper"},
		{ID: "other", Workspace: "ws2", Domain: "code", Type: "func", Name: "OpenDatabase", Content: "different workspace database opener"},
	}
	for _, n := range nodes {
		if err := store.SaveNode(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	return store
}

func TestSearchAutoKeyword(t *testing.T) {
	store := seedStore(t)
	res, err := Search(context.Background(), store, nil, Options{Query: "database connection", Workspace: "ws", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if res.Count == 0 {
		t.Fatalf("expected matches, got none")
	}
	for _, m := range res.Matches {
		if m.ID == "f2" {
			t.Fatalf("non-matching node returned: %+v", res.Matches)
		}
		if m.ID == "other" {
			t.Fatalf("workspace scope leaked: %+v", res.Matches)
		}
	}
}

func TestSearchTypeFilter(t *testing.T) {
	store := seedStore(t)
	res, err := Search(context.Background(), store, nil, Options{Query: "OpenDatabase", Workspace: "ws", Types: []string{"file"}, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if res.Count != 1 || res.Matches[0].Type != "file" {
		t.Fatalf("type filter failed: %+v", res.Matches)
	}
}

func TestSearchRegex(t *testing.T) {
	store := seedStore(t)
	res, err := Search(context.Background(), store, nil, Options{Query: "Open[A-Z]\\w+", Workspace: "ws", Mode: ModeRegex, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if res.Count == 0 {
		t.Fatalf("regex search returned nothing")
	}
}

func TestSearchVectorRequiresProvider(t *testing.T) {
	store := seedStore(t)
	if _, err := Search(context.Background(), store, nil, Options{Query: "x", Mode: ModeVector}); err == nil {
		t.Fatal("expected error without embedding provider")
	}
}
