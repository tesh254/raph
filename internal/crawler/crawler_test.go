package crawler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"raph/internal/config"
	"raph/internal/db"
)

type crawlStore struct {
	mu    sync.Mutex
	nodes []db.Node
}

func (s *crawlStore) SaveNode(_ context.Context, node db.Node) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nodes = append(s.nodes, node)
	return nil
}
func (*crawlStore) SaveEdge(context.Context, db.Edge) error { return nil }
func (*crawlStore) VectorSearch(context.Context, []float32, int) ([]db.Node, error) {
	return nil, nil
}
func (*crawlStore) VectorSearchWorkspace(context.Context, string, []float32, int) ([]db.Node, error) {
	return nil, nil
}
func (*crawlStore) KeywordSearch(context.Context, string, int) ([]db.Node, error) {
	return nil, nil
}
func (*crawlStore) KeywordSearchWorkspace(context.Context, string, string, int) ([]db.Node, error) {
	return nil, nil
}
func (*crawlStore) GetNodeByID(context.Context, string) (db.Node, error) { return db.Node{}, nil }
func (*crawlStore) GetNeighbors(context.Context, string) ([]db.Node, []db.Edge, error) {
	return nil, nil, nil
}
func (*crawlStore) GetAllGraphElements(context.Context) ([]db.Node, []db.Edge, error) {
	return nil, nil, nil
}
func (*crawlStore) UpsertMemoryRecord(context.Context, db.MemoryRecord) error { return nil }
func (*crawlStore) GetMemoryRecord(context.Context, string) (db.MemoryRecord, error) {
	return db.MemoryRecord{}, nil
}
func (*crawlStore) GetMemoryRecordByKey(context.Context, string, string, string, string) (db.MemoryRecord, error) {
	return db.MemoryRecord{}, nil
}
func (*crawlStore) InsertMemoryRevision(context.Context, db.MemoryRevision) error { return nil }
func (*crawlStore) ListMemoryRevisions(context.Context, string) ([]db.MemoryRevision, error) {
	return nil, nil
}
func (*crawlStore) SearchMemoryRecords(context.Context, db.MemorySearchFilter) ([]db.MemoryRecord, error) {
	return nil, nil
}
func (*crawlStore) SetMemoryLifecycle(context.Context, string, string, string, string) error {
	return nil
}
func (*crawlStore) SaveWebCorpus(context.Context, db.WebCorpus) error             { return nil }
func (*crawlStore) SaveWebCrawlVersion(context.Context, db.WebCrawlVersion) error { return nil }
func (*crawlStore) DeleteNodeByID(context.Context, string) error                  { return nil }
func (*crawlStore) DeleteFileNodes(context.Context, string, string) error         { return nil }
func (*crawlStore) DeleteWorkspace(context.Context, string) error                 { return nil }
func (*crawlStore) ClearAll(context.Context) error                                { return nil }
func (*crawlStore) Close() error                                                  { return nil }

func TestSinglePageCrawlerDoesNotFollowLinksAndEmbedsChunks(t *testing.T) {
	var linkedPageRequests int
	pageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/linked" {
			linkedPageRequests++
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head><title>Page</title></head><body><main><h1>Intro</h1><p>Useful content.</p><a href="/linked">next</a></main></body></html>`))
	}))
	defer pageServer.Close()

	embeddingServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"embedding":[0.4,0.5]}]}`))
	}))
	defer embeddingServer.Close()

	cfg := &config.Config{Vector: config.VectorSettings{
		CurrentProvider: "openrouter",
		Providers: config.ProviderContainer{OpenRouter: config.OpenRouterConfig{
			APIKey:  "test",
			Model:   "test-model",
			BaseURL: embeddingServer.URL,
		}},
	}}
	store := &crawlStore{}
	docCrawler, err := NewSinglePageCrawler(store, cfg, pageServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := docCrawler.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	stats := docCrawler.Stats()
	if stats.PagesIndexed != 1 || stats.ChunksIndexed == 0 || stats.EmbeddingsCreated != stats.ChunksIndexed {
		t.Fatalf("unexpected crawl stats: %+v", stats)
	}
	if linkedPageRequests != 0 {
		t.Fatalf("single-page crawler followed a link %d times", linkedPageRequests)
	}

	foundEmbeddedChunk := false
	for _, node := range store.nodes {
		if node.Type == "markdown_chunk" && len(node.Embedding) == 2 {
			foundEmbeddedChunk = true
		}
	}
	if !foundEmbeddedChunk {
		t.Fatal("expected an embedded markdown chunk")
	}
}

func TestSinglePageCrawlerPersistsEmbeddingsForLookup(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	pageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head><title>Persisted Page</title></head><body><main><h1>Intro</h1><p>Persisted semantic content.</p></main></body></html>`))
	}))
	defer pageServer.Close()

	embeddingServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"embedding":[0.4,0.5]}]}`))
	}))
	defer embeddingServer.Close()

	cfg := &config.Config{Vector: config.VectorSettings{
		CurrentProvider: "openrouter",
		Providers: config.ProviderContainer{OpenRouter: config.OpenRouterConfig{
			APIKey:  "test",
			Model:   "test-model",
			BaseURL: embeddingServer.URL,
		}},
	}}
	store, err := db.InitStorage()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	docCrawler, err := NewSinglePageCrawler(store, cfg, pageServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := docCrawler.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	nodes, _, err := store.GetAllGraphElements(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	foundPersistedEmbedding := false
	for _, node := range nodes {
		if node.Type == "markdown_chunk" && node.EmbeddingLength == 2 {
			foundPersistedEmbedding = true
		}
	}
	if !foundPersistedEmbedding {
		t.Fatalf("persisted graph did not report embedded markdown chunk: %+v", nodes)
	}

	matches, err := store.VectorSearch(context.Background(), []float32{0.4, 0.5}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0].Type != "markdown_chunk" || matches[0].EmbeddingLength != 2 {
		t.Fatalf("persisted embedding was not available for vector lookup: %+v", matches)
	}
}

func TestCrawlerWorksWithoutEmbeddingConfig(t *testing.T) {
	pageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head><title>Page</title></head><body><main><h1>Intro</h1><p>Keyword-only content.</p></main></body></html>`))
	}))
	defer pageServer.Close()

	store := &crawlStore{}
	docCrawler, err := NewSinglePageCrawler(store, nil, pageServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := docCrawler.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	stats := docCrawler.Stats()
	if stats.PagesIndexed != 1 || stats.ChunksIndexed != 1 || stats.EmbeddingsCreated != 0 {
		t.Fatalf("unexpected keyword-only crawl stats: %+v", stats)
	}
}
