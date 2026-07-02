package memory

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"raph/internal/config"
	"raph/internal/db"
)

type captureStore struct {
	node    db.Node
	records map[string]db.MemoryRecord
}

func (s *captureStore) SaveNode(_ context.Context, node db.Node) error {
	s.node = node
	return nil
}
func (*captureStore) SaveEdge(context.Context, db.Edge) error { return nil }
func (*captureStore) VectorSearch(context.Context, []float32, int) ([]db.Node, error) {
	return nil, nil
}
func (*captureStore) VectorSearchWorkspace(context.Context, string, []float32, int) ([]db.Node, error) {
	return nil, nil
}
func (*captureStore) KeywordSearch(context.Context, string, int) ([]db.Node, error) {
	return nil, nil
}
func (*captureStore) KeywordSearchWorkspace(context.Context, string, string, int) ([]db.Node, error) {
	return nil, nil
}
func (*captureStore) LexicalSearch(context.Context, string, string, int) ([]db.Node, error) {
	return nil, nil
}
func (*captureStore) ListNodes(context.Context, db.NodeFilter) ([]db.Node, error) { return nil, nil }
func (*captureStore) SetNodeProperties(context.Context, string, map[string]string) error {
	return nil
}
func (*captureStore) GetNodeByID(context.Context, string) (db.Node, error) { return db.Node{}, nil }
func (*captureStore) GetNeighbors(context.Context, string) ([]db.Node, []db.Edge, error) {
	return nil, nil, nil
}
func (*captureStore) GetAllGraphElements(context.Context) ([]db.Node, []db.Edge, error) {
	return nil, nil, nil
}
func (s *captureStore) UpsertMemoryRecord(_ context.Context, record db.MemoryRecord) error {
	if s.records == nil {
		s.records = make(map[string]db.MemoryRecord)
	}
	s.records[record.Node.ID] = record
	return nil
}
func (s *captureStore) GetMemoryRecord(_ context.Context, nodeID string) (db.MemoryRecord, error) {
	record, ok := s.records[nodeID]
	if !ok {
		return db.MemoryRecord{}, sql.ErrNoRows
	}
	return record, nil
}
func (s *captureStore) GetMemoryRecordByKey(_ context.Context, scopeType string, scopeID string, knowledgeType string, memoryKey string) (db.MemoryRecord, error) {
	for _, record := range s.records {
		if record.ScopeType == scopeType && record.ScopeID == scopeID && record.KnowledgeType == knowledgeType && record.MemoryKey == memoryKey {
			return record, nil
		}
	}
	return db.MemoryRecord{}, sql.ErrNoRows
}
func (*captureStore) InsertMemoryRevision(context.Context, db.MemoryRevision) error { return nil }
func (*captureStore) ListMemoryRevisions(context.Context, string) ([]db.MemoryRevision, error) {
	return nil, nil
}
func (*captureStore) SearchMemoryRecords(context.Context, db.MemorySearchFilter) ([]db.MemoryRecord, error) {
	return nil, nil
}
func (*captureStore) SetMemoryLifecycle(context.Context, string, string, string, string) error {
	return nil
}
func (*captureStore) SaveWebCorpus(context.Context, db.WebCorpus) error             { return nil }
func (*captureStore) SaveWebCrawlVersion(context.Context, db.WebCrawlVersion) error { return nil }
func (*captureStore) DeleteNodeByID(context.Context, string) error                  { return nil }
func (*captureStore) DeleteFileNodes(context.Context, string, string) error         { return nil }
func (*captureStore) DeleteWorkspace(context.Context, string) error                 { return nil }
func (*captureStore) ClearAll(context.Context) error                                { return nil }
func (*captureStore) Close() error                                                  { return nil }

func TestStoreGeneratesAndPersistsEmbedding(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"embedding":[0.1,0.2,0.3]}]}`))
	}))
	defer server.Close()

	cfg := &config.Config{Vector: config.VectorSettings{
		CurrentProvider: "openrouter",
		Providers: config.ProviderContainer{OpenRouter: config.OpenRouterConfig{
			APIKey:  "test",
			Model:   "test-model",
			BaseURL: server.URL,
		}},
	}}
	store := &captureStore{}

	output, err := Store(context.Background(), store, cfg, StoreInput{
		ScopeType:     "project",
		ScopeID:       "project:test",
		KnowledgeType: "preference",
		MemoryKey:     "project-style",
		Title:         "Project style",
		Content:       "Use focused changes and run tests.",
		Source:        "user",
		WriterID:      "agent:test",
		Tags:          []string{"Style", "Tests"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !output.Embedded || output.Record.Node.EmbeddingLength != 3 {
		t.Fatalf("expected a 3-float embedding, got %+v", output)
	}
	if len(store.node.Embedding) != 3 {
		t.Fatalf("expected persisted embedding, got %d floats", len(store.node.Embedding))
	}
	if output.Record.ScopeType != "project" || output.Record.MemoryKey != "project-style" {
		t.Fatalf("expected scoped record metadata, got %+v", output.Record)
	}
}

func TestPutCreatesThenUpdates(t *testing.T) {
	store := &captureStore{}
	ctx := context.Background()
	in := StoreInput{
		ScopeType: "global", ScopeID: "global", KnowledgeType: "rule",
		MemoryKey: "no-cgo", Title: "No CGO", Content: "Keep CGO disabled.",
		Source: "cli", WriterID: "cli",
	}
	first, err := Put(ctx, store, nil, in)
	if err != nil {
		t.Fatal(err)
	}
	if first.Record.Revision != 1 {
		t.Fatalf("expected revision 1 on create, got %d", first.Record.Revision)
	}
	in.Content = "Keep CGO disabled for portability."
	second, err := Put(ctx, store, nil, in)
	if err != nil {
		t.Fatal(err)
	}
	if second.Record.Revision != 2 {
		t.Fatalf("expected revision 2 on update, got %d", second.Record.Revision)
	}
	if second.Record.Node.ID != first.Record.Node.ID {
		t.Fatalf("Put changed node id across update: %s != %s", second.Record.Node.ID, first.Record.Node.ID)
	}
	if second.Record.Node.Content != "Keep CGO disabled for portability." {
		t.Fatalf("Put did not update content: %q", second.Record.Node.Content)
	}
}

// TestUpdateAtomicRevisionOnRealStore exercises the transactional commit path
// (LibSQLStore.CommitMemoryRecord) rather than the mock fallback: an update must
// bump the record revision AND append exactly one revision-history row, and the
// two must land together.
func TestUpdateAtomicRevisionOnRealStore(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store, err := db.InitStorage()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()

	in := StoreInput{
		ScopeType: "global", ScopeID: "global", KnowledgeType: "decision",
		MemoryKey: "db-choice", Title: "DB", Content: "sqlite",
		Source: "cli", WriterID: "cli",
	}
	created, err := Store(ctx, store, nil, in)
	if err != nil {
		t.Fatal(err)
	}
	if revs, err := store.ListMemoryRevisions(ctx, created.Record.Node.ID); err != nil {
		t.Fatal(err)
	} else if len(revs) != 0 {
		t.Fatalf("create should not write a history row yet, got %d", len(revs))
	}

	in.Content = "libsql"
	updated, err := Update(ctx, store, nil, UpdateInput(in))
	if err != nil {
		t.Fatal(err)
	}
	if updated.Record.Revision != 2 {
		t.Fatalf("expected revision 2 after update, got %d", updated.Record.Revision)
	}
	revs, err := store.ListMemoryRevisions(ctx, updated.Record.Node.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(revs) != 1 {
		t.Fatalf("expected exactly 1 history row after one update, got %d", len(revs))
	}
	if revs[0].Content != "sqlite" || revs[0].Revision != 1 {
		t.Fatalf("history row should snapshot the pre-update state (rev1/sqlite), got rev%d/%q", revs[0].Revision, revs[0].Content)
	}
	// The live record reflects the new content.
	live, err := store.GetMemoryRecord(ctx, updated.Record.Node.ID)
	if err != nil {
		t.Fatal(err)
	}
	if live.Node.Content != "libsql" || live.Revision != 2 {
		t.Fatalf("live record not updated atomically: rev%d/%q", live.Revision, live.Node.Content)
	}
}

// TestConcurrentUpdatesAssignUniqueRevisions drives concurrent updates to the
// same key through TWO independent store handles on one DB file (simulating two
// raph processes). With the in-transaction revision read-modify-write and
// IMMEDIATE transactions, every update must land a distinct, monotonic revision
// — no lost updates, no duplicate revision numbers.
func TestConcurrentUpdatesAssignUniqueRevisions(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	storeA, err := db.InitStorage()
	if err != nil {
		t.Fatal(err)
	}
	defer storeA.Close()
	storeB, err := db.InitStorage() // separate connection pool -> acts like a 2nd process
	if err != nil {
		t.Fatal(err)
	}
	defer storeB.Close()
	ctx := context.Background()

	base := StoreInput{
		ScopeType: "global", ScopeID: "global", KnowledgeType: "decision",
		MemoryKey: "hot-key", Title: "K", Content: "v0", Source: "cli", WriterID: "cli",
	}
	created, err := Store(ctx, storeA, nil, base)
	if err != nil {
		t.Fatal(err)
	}
	nodeID := created.Record.Node.ID

	const writers = 8
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			store := storeA
			if i%2 == 1 {
				store = storeB
			}
			in := base
			in.Content = fmt.Sprintf("v%d", i+1)
			_, err := Update(ctx, store, nil, UpdateInput(in))
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent update failed: %v", err)
		}
	}

	live, err := storeA.GetMemoryRecordByKey(ctx, "global", "global", "decision", "hot-key")
	if err != nil {
		t.Fatal(err)
	}
	if live.Revision != writers+1 {
		t.Fatalf("expected final revision %d after %d concurrent updates, got %d", writers+1, writers, live.Revision)
	}
	revs, err := storeA.ListMemoryRevisions(ctx, nodeID)
	if err != nil {
		t.Fatal(err)
	}
	if len(revs) != writers {
		t.Fatalf("expected %d history rows, got %d", writers, len(revs))
	}
	seen := map[int]bool{}
	for _, r := range revs {
		if seen[r.Revision] {
			t.Fatalf("duplicate revision number %d in history (lost-update race)", r.Revision)
		}
		seen[r.Revision] = true
	}
}
