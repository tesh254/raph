package memory

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
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
