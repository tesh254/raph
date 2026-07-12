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
func (*indexCaptureStore) LexicalSearch(context.Context, string, string, int) ([]db.Node, error) {
	return nil, nil
}
func (*indexCaptureStore) ListNodes(context.Context, db.NodeFilter) ([]db.Node, error) {
	return nil, nil
}
func (*indexCaptureStore) SetNodeProperties(context.Context, string, map[string]string) error {
	return nil
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
func (*indexCaptureStore) UpsertMemoryRecord(context.Context, db.MemoryRecord) error { return nil }
func (*indexCaptureStore) GetMemoryRecord(context.Context, string) (db.MemoryRecord, error) {
	return db.MemoryRecord{}, nil
}
func (*indexCaptureStore) GetMemoryRecordByKey(context.Context, string, string, string, string) (db.MemoryRecord, error) {
	return db.MemoryRecord{}, nil
}
func (*indexCaptureStore) InsertMemoryRevision(context.Context, db.MemoryRevision) error { return nil }
func (*indexCaptureStore) ListMemoryRevisions(context.Context, string) ([]db.MemoryRevision, error) {
	return nil, nil
}
func (*indexCaptureStore) SearchMemoryRecords(context.Context, db.MemorySearchFilter) ([]db.MemoryRecord, error) {
	return nil, nil
}
func (*indexCaptureStore) SetMemoryLifecycle(context.Context, string, string, string, string) error {
	return nil
}
func (*indexCaptureStore) SaveWebCorpus(context.Context, db.WebCorpus) error             { return nil }
func (*indexCaptureStore) SaveWebCrawlVersion(context.Context, db.WebCrawlVersion) error { return nil }
func (*indexCaptureStore) DeleteNodeByID(context.Context, string) error                  { return nil }
func (*indexCaptureStore) DeleteFileNodes(context.Context, string, string) error         { return nil }
func (*indexCaptureStore) DeleteWorkspace(context.Context, string) error                 { return nil }
func (*indexCaptureStore) ClearAll(context.Context) error                                { return nil }
func (*indexCaptureStore) Close() error                                                  { return nil }

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

func TestGoGlobalsAndUsageEdges(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store, err := db.InitStorage()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	root := t.TempDir()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module testmod\n\ngo 1.21\n")
	write("main.go", `package main

var Counter int
const Max = 10

func Inc() { Counter++ }
func Read() int { return Counter + Max }
func main() { Inc(); _ = Read() }
`)

	idx, err := New(store, nil, root, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := idx.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	nodes, edges, err := store.GetAllGraphElements(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]db.Node{}
	var counterID string
	var globalFound, constFound bool
	for _, n := range nodes {
		byID[n.ID] = n
		if n.Type == "var" && n.Name == "Counter" {
			constFound = constFound || false
			if n.Prop("global") == "true" {
				globalFound = true
			}
			counterID = n.ID
		}
		if n.Type == "const" && n.Name == "Max" {
			constFound = true
		}
	}
	if !globalFound {
		t.Fatalf("global var Counter not indexed with global=true; nodes=%v", nodeNames(nodes))
	}
	if !constFound {
		t.Fatalf("const Max not indexed; nodes=%v", nodeNames(nodes))
	}

	var mutates, uses bool
	for _, e := range edges {
		src := byID[e.SourceID]
		tgt := byID[e.TargetID]
		if e.Type == "MUTATES" && src.Name == "Inc" && tgt.ID == counterID {
			mutates = true
		}
		if e.Type == "USES" && src.Name == "Read" && tgt.ID == counterID {
			uses = true
		}
	}
	if !mutates {
		t.Fatalf("expected MUTATES edge Inc->Counter; edges=%v", edgeSummary(byID, edges))
	}
	if !uses {
		t.Fatalf("expected USES edge Read->Counter; edges=%v", edgeSummary(byID, edges))
	}
}

func nodeNames(nodes []db.Node) []string {
	var out []string
	for _, n := range nodes {
		out = append(out, n.Type+":"+n.Name)
	}
	return out
}

func edgeSummary(byID map[string]db.Node, edges []db.Edge) []string {
	var out []string
	for _, e := range edges {
		out = append(out, byID[e.SourceID].Name+"-"+e.Type+"->"+byID[e.TargetID].Name)
	}
	return out
}
