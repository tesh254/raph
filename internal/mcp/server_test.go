package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"raph/internal/config"
	"raph/internal/db"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type protocolStore struct {
	nodes             map[string]db.Node
	vectorSearchLimit int
	vectorSearchNodes []db.Node
}

func newProtocolStore() *protocolStore {
	return &protocolStore{nodes: make(map[string]db.Node)}
}

func (s *protocolStore) SaveNode(_ context.Context, node db.Node) error {
	s.nodes[node.ID] = node
	return nil
}
func (*protocolStore) SaveEdge(context.Context, db.Edge) error { return nil }
func (s *protocolStore) VectorSearch(_ context.Context, _ []float32, limit int) ([]db.Node, error) {
	s.vectorSearchLimit = limit
	return s.vectorSearchNodes, nil
}
func (s *protocolStore) VectorSearchWorkspace(_ context.Context, _ string, _ []float32, limit int) ([]db.Node, error) {
	s.vectorSearchLimit = limit
	return s.vectorSearchNodes, nil
}
func (s *protocolStore) KeywordSearch(context.Context, string, int) ([]db.Node, error) {
	for _, node := range s.nodes {
		return []db.Node{node}, nil
	}
	return nil, nil
}
func (s *protocolStore) KeywordSearchWorkspace(ctx context.Context, _ string, query string, limit int) ([]db.Node, error) {
	return s.KeywordSearch(ctx, query, limit)
}
func (s *protocolStore) GetNodeByID(_ context.Context, id string) (db.Node, error) {
	return s.nodes[id], nil
}
func (*protocolStore) GetNeighbors(context.Context, string) ([]db.Node, []db.Edge, error) {
	return nil, nil, nil
}
func (*protocolStore) GetAllGraphElements(context.Context) ([]db.Node, []db.Edge, error) {
	return nil, nil, nil
}
func (s *protocolStore) DeleteNodeByID(_ context.Context, id string) error {
	delete(s.nodes, id)
	return nil
}
func (*protocolStore) DeleteFileNodes(context.Context, string, string) error { return nil }
func (*protocolStore) DeleteWorkspace(context.Context, string) error         { return nil }
func (*protocolStore) ClearAll(context.Context) error                        { return nil }
func (*protocolStore) Close() error                                          { return nil }

func TestMCPProtocolListsAndCallsMemoryTools(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	wrapper := NewMCPServerWrapper(newProtocolStore(), nil)
	clientTransport, serverTransport := mcpsdk.NewInMemoryTransports()
	go func() {
		_ = wrapper.server.Run(ctx, serverTransport)
	}()

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "raph-test", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	names := make(map[string]bool)
	for _, tool := range tools.Tools {
		names[tool.Name] = true
	}
	for _, name := range []string{"hybrid_semantic_search", "multi_query_search", "best_vector_match", "graph_neighbors", "memory_store", "memory_delete", "crawl_url", "crawl_website", "index_codebase"} {
		if !names[name] {
			t.Fatalf("expected MCP tool %q, got %v", name, names)
		}
	}

	result, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "memory_store",
		Arguments: map[string]any{
			"key":     "test",
			"title":   "Test memory",
			"content": "Remember protocol behavior.",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("memory_store returned tool error: %+v", result.Content)
	}

	result, err = session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "hybrid_semantic_search",
		Arguments: map[string]any{"query": "protocol"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("hybrid_semantic_search returned tool error: %+v", result.Content)
	}

	result, err = session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "multi_query_search",
		Arguments: map[string]any{"queries": []string{"protocol", "behavior"}, "limit": 100},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("multi_query_search returned tool error: %+v", result.Content)
	}

	codebase := t.TempDir()
	if err := os.WriteFile(filepath.Join(codebase, "README.md"), []byte("# Agent-indexed codebase"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err = session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "index_codebase",
		Arguments: map[string]any{
			"path":          codebase,
			"no_embeddings": true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("index_codebase returned tool error: %+v", result.Content)
	}
	foundCodebaseNode := false
	for _, node := range wrapper.store.(*protocolStore).nodes {
		if node.Path == codebase {
			foundCodebaseNode = true
			break
		}
	}
	if !foundCodebaseNode {
		t.Fatalf("expected indexed node with codebase path %q", codebase)
	}
}

func TestBestVectorMatchReturnsSingleMatch(t *testing.T) {
	embeddingServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"embedding": []float32{1, 0}}},
		})
	}))
	defer embeddingServer.Close()

	store := newProtocolStore()
	store.vectorSearchNodes = []db.Node{{ID: "best", Name: "Best match"}}
	cfg := &config.Config{Vector: config.VectorSettings{
		CurrentProvider: "openrouter",
		Providers: config.ProviderContainer{OpenRouter: config.OpenRouterConfig{
			APIKey: "test", BaseURL: embeddingServer.URL,
		}},
	}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wrapper := NewMCPServerWrapper(store, cfg)
	clientTransport, serverTransport := mcpsdk.NewInMemoryTransports()
	go func() { _ = wrapper.server.Run(ctx, serverTransport) }()

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "raph-test", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	result, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "best_vector_match",
		Arguments: map[string]any{"query": "closest node"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("best_vector_match returned tool error: %+v", result.Content)
	}
	if store.vectorSearchLimit != 1 {
		t.Fatalf("expected vector search limit 1, got %d", store.vectorSearchLimit)
	}
}

func TestCompactExcerptBoundsReturnedContent(t *testing.T) {
	got := compactExcerpt("  one \n two   three four  ", 9)
	if got != "one two t..." {
		t.Fatalf("unexpected compact excerpt %q", got)
	}
	matches := compactMatches([]db.Node{
		{ID: "page", URL: "https://example.com", Content: "same content"},
		{ID: "chunk", URL: "https://example.com", Content: "same content"},
	}, 100)
	if len(matches) != 1 {
		t.Fatalf("expected duplicate compact content to be removed, got %+v", matches)
	}
	if compactResultLimit(100) != 10 || compactExcerptLimit(10_000) != 2_000 {
		t.Fatal("expected compact response limits to be capped")
	}
}
