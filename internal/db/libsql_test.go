package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestCosineSimilarity(t *testing.T) {
	t.Parallel()

	got := cosineSimilarity([]float32{1, 0}, []float32{1, 0})
	if got < 0.999 {
		t.Fatalf("expected vectors to be nearly identical, got %f", got)
	}

	got = cosineSimilarity([]float32{1, 0}, []float32{0, 1})
	if got != 0 {
		t.Fatalf("expected orthogonal vectors to have zero similarity, got %f", got)
	}

	got = cosineSimilarity([]float32{1, 0}, []float32{1, 0, 0})
	if got != 0 {
		t.Fatalf("expected mismatched dimensions to return zero, got %f", got)
	}
}

func TestSearchResultsReportEmbeddingLength(t *testing.T) {
	rawDB, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	store := &LibSQLStore{db: rawDB}
	defer store.Close()
	if err := store.migrate(); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveNode(context.Background(), Node{
		ID: "node", Workspace: "test", Domain: "memory", Type: "memory",
		Name: "Searchable", Content: "vector content", Embedding: []float32{1, 0},
	}); err != nil {
		t.Fatal(err)
	}

	keyword, err := store.KeywordSearch(context.Background(), "vector", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(keyword) != 1 || keyword[0].EmbeddingLength != 2 {
		t.Fatalf("keyword search did not report embedding length: %+v", keyword)
	}

	semantic, err := store.VectorSearch(context.Background(), []float32{1, 0}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(semantic) != 1 || semantic[0].EmbeddingLength != 2 {
		t.Fatalf("semantic search did not report embedding length: %+v", semantic)
	}
}

func TestNodePathPersistsAndScopedSearchExcludesOtherWorkspaces(t *testing.T) {
	rawDB, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	store := &LibSQLStore{db: rawDB}
	defer store.Close()
	if err := store.migrate(); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	for _, node := range []Node{
		{ID: "one", Workspace: "ws-one", Domain: "code", Type: "file", Name: "one.go", Content: "shared query", Path: "/code/one", Embedding: []float32{1, 0}},
		{ID: "two", Workspace: "ws-two", Domain: "code", Type: "file", Name: "two.go", Content: "shared query", Path: "/code/two", Embedding: []float32{1, 0}},
	} {
		if err := store.SaveNode(ctx, node); err != nil {
			t.Fatal(err)
		}
	}

	got, err := store.GetNodeByID(ctx, "one")
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != "/code/one" {
		t.Fatalf("expected persisted path, got %+v", got)
	}
	keyword, err := store.KeywordSearchWorkspace(ctx, "ws-one", "shared query", 5)
	if err != nil {
		t.Fatal(err)
	}
	vector, err := store.VectorSearchWorkspace(ctx, "ws-one", []float32{1, 0}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(keyword) != 1 || keyword[0].ID != "one" || len(vector) != 1 || vector[0].ID != "one" {
		t.Fatalf("workspace searches leaked nodes: keyword=%+v vector=%+v", keyword, vector)
	}
}

func TestMigrationAddsPathToExistingNodesTable(t *testing.T) {
	rawDB, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	if _, err := rawDB.Exec(`CREATE TABLE nodes (
		id TEXT PRIMARY KEY,
		workspace TEXT NOT NULL,
		domain TEXT NOT NULL,
		type TEXT NOT NULL,
		name TEXT NOT NULL,
		content TEXT NOT NULL,
		url TEXT,
		embedding_json TEXT NOT NULL DEFAULT '[]'
	)`); err != nil {
		t.Fatal(err)
	}

	store := &LibSQLStore{db: rawDB}
	if err := store.migrate(); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveNode(context.Background(), Node{
		ID: "node", Workspace: "ws", Domain: "code", Type: "file", Name: "node.go", Path: "/code",
	}); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetNodeByID(context.Background(), "node")
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != "/code" {
		t.Fatalf("expected migrated path column, got %+v", got)
	}
}

func TestDeleteFileNodesRemovesGeneratedChildrenOnly(t *testing.T) {
	rawDB, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	store := &LibSQLStore{db: rawDB}
	defer store.Close()
	if err := store.migrate(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, node := range []Node{
		{ID: "file-a", Workspace: "ws", Domain: "code", Type: "file", Name: "a.go", URL: "a.go"},
		{ID: "func-a", Workspace: "ws", Domain: "code", Type: "func", Name: "A", URL: "a.go#A"},
		{ID: "file-b", Workspace: "ws", Domain: "code", Type: "file", Name: "b.go", URL: "b.go"},
	} {
		if err := store.SaveNode(ctx, node); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SaveEdge(ctx, Edge{SourceID: "file-a", TargetID: "func-a", Type: "DECLARES"}); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteFileNodes(ctx, "ws", "a.go"); err != nil {
		t.Fatal(err)
	}
	nodes, edges, err := store.GetAllGraphElements(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].ID != "file-b" || len(edges) != 0 {
		t.Fatalf("unexpected graph after file cleanup: nodes=%+v edges=%+v", nodes, edges)
	}
}

func newTestStore(t *testing.T) *LibSQLStore {
	t.Helper()
	rawDB, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	store := &LibSQLStore{db: rawDB}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.migrate(); err != nil {
		t.Fatal(err)
	}
	return store
}

func TestFTSKeywordSearchRanksAndPersistsProperties(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	nodes := []Node{
		{ID: "a", Workspace: "ws", Domain: "code", Type: "func", Name: "ConnectDatabase", Content: "open a database connection pool", Properties: map[string]string{"doc_type": "architecture"}},
		{ID: "b", Workspace: "ws", Domain: "code", Type: "func", Name: "ParseConfig", Content: "read configuration values from disk"},
		{ID: "c", Workspace: "ws", Domain: "code", Type: "file", Name: "database.go", Content: "package db has database helpers and a connection wrapper"},
	}
	for _, n := range nodes {
		if err := store.SaveNode(ctx, n); err != nil {
			t.Fatal(err)
		}
	}

	got, err := store.KeywordSearch(ctx, "database connection", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) < 2 {
		t.Fatalf("expected >=2 matches for 'database connection', got %d: %+v", len(got), got)
	}
	// ParseConfig (no match) must not appear.
	for _, n := range got {
		if n.ID == "b" {
			t.Fatalf("non-matching node leaked into results: %+v", got)
		}
	}

	// Properties round-trip through SaveNode -> GetNodeByID.
	a, err := store.GetNodeByID(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	if a.Prop("doc_type") != "architecture" {
		t.Fatalf("properties not persisted: %+v", a.Properties)
	}
	if a.CreatedAt == "" || a.UpdatedAt == "" {
		t.Fatalf("timestamps not set: created=%q updated=%q", a.CreatedAt, a.UpdatedAt)
	}
}

func TestLexicalSearchLiteralSubstring(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	if err := store.SaveNode(ctx, Node{ID: "x", Workspace: "ws", Domain: "code", Type: "func", Name: "handleRequest", Content: "func handleRequest(w http.ResponseWriter) {}"}); err != nil {
		t.Fatal(err)
	}
	// Substring across an identifier — literal trigram match.
	got, err := store.LexicalSearch(ctx, "ws", "ResponseWriter", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "x" {
		t.Fatalf("literal lexical search failed: %+v", got)
	}
}

func TestListNodesByPropertyAndSetProperties(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	for _, n := range []Node{
		{ID: "h1", Workspace: "ws", Domain: "knowledge", Type: "doc", Name: "Handoff A", Content: "x", Properties: map[string]string{"doc_type": "handoff", "status": "fresh"}},
		{ID: "d1", Workspace: "ws", Domain: "knowledge", Type: "doc", Name: "Arch", Content: "y", Properties: map[string]string{"doc_type": "architecture"}},
	} {
		if err := store.SaveNode(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	got, err := store.ListNodes(ctx, NodeFilter{Workspace: "ws", PropertyEquals: map[string]string{"doc_type": "handoff"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "h1" {
		t.Fatalf("property filter failed: %+v", got)
	}
	if err := store.SetNodeProperties(ctx, "h1", map[string]string{"status": "used"}); err != nil {
		t.Fatal(err)
	}
	h, err := store.GetNodeByID(ctx, "h1")
	if err != nil {
		t.Fatal(err)
	}
	if h.Prop("status") != "used" || h.Prop("doc_type") != "handoff" {
		t.Fatalf("set/merge properties failed: %+v", h.Properties)
	}
}

func TestKeywordSearchUpdatesAfterContentChange(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	id := "n"
	if err := store.SaveNode(ctx, Node{ID: id, Workspace: "ws", Domain: "code", Type: "file", Name: "f.go", Content: "alpha beta gamma"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveNode(ctx, Node{ID: id, Workspace: "ws", Domain: "code", Type: "file", Name: "f.go", Content: "delta epsilon zeta"}); err != nil {
		t.Fatal(err)
	}
	old, err := store.KeywordSearch(ctx, "alpha", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(old) != 0 {
		t.Fatalf("stale FTS row served after update: %+v", old)
	}
	fresh, err := store.KeywordSearch(ctx, "epsilon", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(fresh) != 1 {
		t.Fatalf("updated FTS content not found: %+v", fresh)
	}
}
