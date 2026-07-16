package db

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

func TestLazyStoreDefersOpenUntilFirstCall(t *testing.T) {
	opens := 0
	lazy := NewLazyStore(func() (GraphStore, error) {
		opens++
		return newTestStore(t), nil
	})

	if opens != 0 {
		t.Fatalf("expected no open before first use, got %d", opens)
	}

	ctx := context.Background()
	if err := lazy.SaveNode(ctx, Node{ID: "n", Workspace: "ws", Domain: "code", Type: "file", Name: "n.go"}); err != nil {
		t.Fatal(err)
	}
	if _, err := lazy.GetNodeByID(ctx, "n"); err != nil {
		t.Fatal(err)
	}
	if opens != 1 {
		t.Fatalf("expected exactly one open across calls, got %d", opens)
	}
}

func TestLazyStoreCloseWithoutUseDoesNotOpen(t *testing.T) {
	opens := 0
	lazy := NewLazyStore(func() (GraphStore, error) {
		opens++
		return newTestStore(t), nil
	})
	if err := lazy.Close(); err != nil {
		t.Fatal(err)
	}
	if opens != 0 {
		t.Fatalf("expected Close to not open the store, got %d opens", opens)
	}
}

func TestLazyStoreRetriesAfterOpenFailure(t *testing.T) {
	opens := 0
	lazy := NewLazyStore(func() (GraphStore, error) {
		opens++
		if opens == 1 {
			return nil, errors.New("transient: database is locked")
		}
		return newTestStore(t), nil
	})

	ctx := context.Background()
	if err := lazy.SaveNode(ctx, Node{ID: "n", Workspace: "ws", Domain: "code", Type: "file", Name: "n.go"}); err == nil {
		t.Fatal("expected first call to surface the open error")
	}
	if err := lazy.SaveNode(ctx, Node{ID: "n", Workspace: "ws", Domain: "code", Type: "file", Name: "n.go"}); err != nil {
		t.Fatalf("expected retry after failed open to succeed: %v", err)
	}
	if opens != 2 {
		t.Fatalf("expected two open attempts, got %d", opens)
	}
}

func TestLazyStoreCloseClosesUnderlyingStore(t *testing.T) {
	rawDB, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	store := &LibSQLStore{db: rawDB}
	if err := store.migrateIfNeeded(); err != nil {
		t.Fatal(err)
	}

	lazy := NewLazyStore(func() (GraphStore, error) { return store, nil })
	ctx := context.Background()
	if err := lazy.SaveNode(ctx, Node{ID: "n", Workspace: "ws", Domain: "code", Type: "file", Name: "n.go"}); err != nil {
		t.Fatal(err)
	}
	if err := lazy.Close(); err != nil {
		t.Fatal(err)
	}
	if err := rawDB.Ping(); err == nil {
		t.Fatal("expected underlying database handle to be closed")
	}
}

func TestLazyStoreConcurrentCallsOpenExactlyOnce(t *testing.T) {
	var opens int32
	real := newTestStore(t)
	lazy := NewLazyStore(func() (GraphStore, error) {
		atomic.AddInt32(&opens, 1)
		return real, nil
	})

	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := lazy.KeywordSearch(ctx, "anything", 1); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&opens); got != 1 {
		t.Fatalf("expected exactly one open under concurrency, got %d", got)
	}
}

func TestLazyStoreDoubleCloseIsSafe(t *testing.T) {
	lazy := NewLazyStore(func() (GraphStore, error) { return newTestStore(t), nil })
	ctx := context.Background()
	if err := lazy.SaveNode(ctx, Node{ID: "n", Workspace: "ws", Domain: "code", Type: "file", Name: "n.go"}); err != nil {
		t.Fatal(err)
	}
	if err := lazy.Close(); err != nil {
		t.Fatal(err)
	}
	if err := lazy.Close(); err != nil {
		t.Fatalf("expected second Close to be a no-op, got %v", err)
	}
}

func TestLazyStoreCloseAfterFailedOpenReturnsNil(t *testing.T) {
	lazy := NewLazyStore(func() (GraphStore, error) { return nil, errors.New("open failed") })
	ctx := context.Background()
	if _, err := lazy.GetNodeByID(ctx, "n"); err == nil {
		t.Fatal("expected open failure to surface")
	}
	if err := lazy.Close(); err != nil {
		t.Fatalf("expected Close after failed open to be a no-op, got %v", err)
	}
}

// TestLazyStoreDelegatesEveryMethod drives each GraphStore method through the
// lazy wrapper against a real store, so a mis-wired delegate (calling the
// wrong underlying method or dropping a result) fails loudly.
func TestLazyStoreDelegatesEveryMethod(t *testing.T) {
	lazy := NewLazyStore(func() (GraphStore, error) { return newTestStore(t), nil })
	defer lazy.Close()
	ctx := context.Background()

	for _, node := range []Node{
		{ID: "file-a", Workspace: "ws", Domain: "code", Type: "file", Name: "a.go", Path: "a.go", Content: "package a", Embedding: []float32{1, 0}},
		{ID: "func-a", Workspace: "ws", Domain: "code", Type: "func", Name: "A", Path: "a.go", Content: "func A() {}", URL: "a.go#A"},
		{ID: "doc-b", Workspace: "other", Domain: "docs", Type: "chunk", Name: "b.md", Path: "b.md", Content: "documentation chunk"},
	} {
		if err := lazy.SaveNode(ctx, node); err != nil {
			t.Fatal(err)
		}
	}
	if err := lazy.SaveEdge(ctx, Edge{SourceID: "file-a", TargetID: "func-a", Type: "DECLARES"}); err != nil {
		t.Fatal(err)
	}

	if got, err := lazy.GetNodeByID(ctx, "file-a"); err != nil || got.Name != "a.go" {
		t.Fatalf("GetNodeByID = %+v, %v", got, err)
	}
	if nodes, edges, err := lazy.GetNeighbors(ctx, "file-a"); err != nil || len(nodes) != 1 || len(edges) != 1 {
		t.Fatalf("GetNeighbors = %d nodes %d edges, %v", len(nodes), len(edges), err)
	}
	if nodes, edges, err := lazy.GetAllGraphElements(ctx); err != nil || len(nodes) != 3 || len(edges) != 1 {
		t.Fatalf("GetAllGraphElements = %d nodes %d edges, %v", len(nodes), len(edges), err)
	}
	if nodes, err := lazy.KeywordSearch(ctx, "documentation", 10); err != nil || len(nodes) != 1 {
		t.Fatalf("KeywordSearch = %d nodes, %v", len(nodes), err)
	}
	if nodes, err := lazy.KeywordSearchWorkspace(ctx, "ws", "package", 10); err != nil || len(nodes) != 1 {
		t.Fatalf("KeywordSearchWorkspace = %d nodes, %v", len(nodes), err)
	}
	if nodes, err := lazy.LexicalSearch(ctx, "ws", "func A", 10); err != nil || len(nodes) == 0 {
		t.Fatalf("LexicalSearch = %d nodes, %v", len(nodes), err)
	}
	if nodes, err := lazy.VectorSearch(ctx, []float32{1, 0}, 1); err != nil || len(nodes) != 1 || nodes[0].ID != "file-a" {
		t.Fatalf("VectorSearch = %+v, %v", nodes, err)
	}
	if nodes, err := lazy.VectorSearchWorkspace(ctx, "other", []float32{1, 0}, 1); err != nil || len(nodes) != 0 {
		t.Fatalf("VectorSearchWorkspace leaked across workspaces: %+v, %v", nodes, err)
	}
	if err := lazy.SetNodeProperties(ctx, "file-a", map[string]string{"lang": "go"}); err != nil {
		t.Fatal(err)
	}
	if nodes, err := lazy.ListNodes(ctx, NodeFilter{Workspace: "ws"}); err != nil || len(nodes) != 2 {
		t.Fatalf("ListNodes = %d nodes, %v", len(nodes), err)
	}

	record := MemoryRecord{
		Node: Node{ID: "func-a"}, ScopeType: "shared", ScopeID: "team", LifecycleState: "active",
		KnowledgeType: "decision", Source: "user", WriterID: "w", MemoryKey: "k",
		CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z", Revision: 1,
	}
	if err := lazy.UpsertMemoryRecord(ctx, record); err != nil {
		t.Fatal(err)
	}
	if got, err := lazy.GetMemoryRecord(ctx, "func-a"); err != nil || got.MemoryKey != "k" {
		t.Fatalf("GetMemoryRecord = %+v, %v", got, err)
	}
	if got, err := lazy.GetMemoryRecordByKey(ctx, "shared", "team", "decision", "k"); err != nil || got.Node.ID != "func-a" {
		t.Fatalf("GetMemoryRecordByKey = %+v, %v", got, err)
	}
	if err := lazy.InsertMemoryRevision(ctx, MemoryRevision{
		NodeID: "func-a", Revision: 1, Title: "t", Content: "c", Source: "user",
		WriterID: "w", LifecycleState: "active", CreatedAt: "2026-01-01T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	if revisions, err := lazy.ListMemoryRevisions(ctx, "func-a"); err != nil || len(revisions) != 1 {
		t.Fatalf("ListMemoryRevisions = %d, %v", len(revisions), err)
	}
	if records, err := lazy.SearchMemoryRecords(ctx, MemorySearchFilter{ScopeType: "shared", ScopeID: "team"}); err != nil || len(records) != 1 {
		t.Fatalf("SearchMemoryRecords = %d, %v", len(records), err)
	}
	if err := lazy.SetMemoryLifecycle(ctx, "func-a", "deprecated", "", "superseded"); err != nil {
		t.Fatal(err)
	}
	if got, err := lazy.GetMemoryRecord(ctx, "func-a"); err != nil || got.LifecycleState != "deprecated" {
		t.Fatalf("SetMemoryLifecycle not applied: %+v, %v", got, err)
	}

	if err := lazy.SaveWebCorpus(ctx, WebCorpus{
		ID: "corpus", ScopeType: "project", ScopeID: "p", Source: "web",
		BaseURL: "https://example.com", CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	if err := lazy.SaveWebCrawlVersion(ctx, WebCrawlVersion{
		ID: "crawl", CorpusID: "corpus", SeedURL: "https://example.com", CreatedAt: "2026-01-01T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}

	if err := lazy.DeleteNodeByID(ctx, "doc-b"); err != nil {
		t.Fatal(err)
	}
	if _, err := lazy.GetNodeByID(ctx, "doc-b"); err == nil {
		t.Fatal("expected doc-b deleted")
	}
	if err := lazy.DeleteFileNodes(ctx, "ws", "a.go"); err != nil {
		t.Fatal(err)
	}
	if err := lazy.DeleteWorkspace(ctx, "ws"); err != nil {
		t.Fatal(err)
	}
	if err := lazy.ClearAll(ctx); err != nil {
		t.Fatal(err)
	}
	if nodes, _, err := lazy.GetAllGraphElements(ctx); err != nil || len(nodes) != 0 {
		t.Fatalf("expected empty graph after ClearAll, got %d nodes, %v", len(nodes), err)
	}
}
