package indexer

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"raph/internal/db"
)

type indexCaptureStore struct {
	nodes []db.Node
}

func (s *indexCaptureStore) SaveNode(_ context.Context, node db.Node) error {
	s.nodes = append(s.nodes, node)
	return nil
}
func (*indexCaptureStore) SaveEdge(context.Context, db.Edge) error { return nil }
func (*indexCaptureStore) VectorSearch(context.Context, []float32, int) ([]db.Node, error) {
	return nil, nil
}
func (*indexCaptureStore) VectorSearchWorkspace(context.Context, string, []float32, int) ([]db.Node, error) {
	return nil, nil
}
func (*indexCaptureStore) KeywordSearch(context.Context, string, int) ([]db.Node, error) {
	return nil, nil
}
func (*indexCaptureStore) KeywordSearchWorkspace(context.Context, string, string, int) ([]db.Node, error) {
	return nil, nil
}
func (*indexCaptureStore) GetNodeByID(context.Context, string) (db.Node, error) {
	return db.Node{}, nil
}
func (*indexCaptureStore) GetNeighbors(context.Context, string) ([]db.Node, []db.Edge, error) {
	return nil, nil, nil
}
func (*indexCaptureStore) GetAllGraphElements(context.Context) ([]db.Node, []db.Edge, error) {
	return nil, nil, nil
}
func (*indexCaptureStore) DeleteNodeByID(context.Context, string) error          { return nil }
func (*indexCaptureStore) DeleteFileNodes(context.Context, string, string) error { return nil }
func (*indexCaptureStore) DeleteWorkspace(context.Context, string) error         { return nil }
func (*indexCaptureStore) ClearAll(context.Context) error                        { return nil }
func (*indexCaptureStore) Close() error                                          { return nil }

func TestSplitDocumentSections(t *testing.T) {
	t.Parallel()

	sections := splitDocumentSections("# Intro\nHello\n## Details\nWorld")
	if len(sections) != 2 {
		t.Fatalf("expected 2 sections, got %d", len(sections))
	}
	if sections[0].Title != "Intro" {
		t.Fatalf("expected first section title Intro, got %q", sections[0].Title)
	}
	if sections[1].Title != "Details" {
		t.Fatalf("expected second section title Details, got %q", sections[1].Title)
	}
}

func TestIndexedNodesCarryAbsoluteCodebasePath(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := &indexCaptureStore{}
	idx, err := New(store, nil, root, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := idx.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.nodes) == 0 {
		t.Fatal("expected indexed nodes")
	}
	ids := make(map[string]struct{}, len(store.nodes))
	for _, node := range store.nodes {
		if node.ID == "" {
			t.Fatalf("expected unique node ID: %+v", node)
		}
		if _, exists := ids[node.ID]; exists {
			t.Fatalf("duplicate node ID %q", node.ID)
		}
		ids[node.ID] = struct{}{}
		if node.Path != root {
			t.Fatalf("expected codebase path %q, got %+v", root, node)
		}
	}
}
