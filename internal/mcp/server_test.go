package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"raph/internal/config"
	"raph/internal/db"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type protocolStore struct {
	nodes             map[string]db.Node
	records           map[string]db.MemoryRecord
	vectorSearchLimit int
	vectorSearchNodes []db.Node
	accesses          []db.AccessEvent
}

func (s *protocolStore) RecordAccess(_ context.Context, nodeID, kind, query string) error {
	s.accesses = append(s.accesses, db.AccessEvent{NodeID: nodeID, Kind: kind, Query: query})
	return nil
}
func (s *protocolStore) RecordAccessBatch(_ context.Context, events []db.AccessEvent) error {
	s.accesses = append(s.accesses, events...)
	return nil
}

func newProtocolStore() *protocolStore {
	return &protocolStore{
		nodes:   make(map[string]db.Node),
		records: make(map[string]db.MemoryRecord),
	}
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
func (s *protocolStore) LexicalSearch(ctx context.Context, _ string, query string, limit int) ([]db.Node, error) {
	return s.KeywordSearch(ctx, query, limit)
}
func (s *protocolStore) ListNodes(_ context.Context, f db.NodeFilter) ([]db.Node, error) {
	out := make([]db.Node, 0, len(s.nodes))
	for _, node := range s.nodes {
		if len(f.Types) > 0 {
			matchType := false
			for _, t := range f.Types {
				if node.Type == t {
					matchType = true
					break
				}
			}
			if !matchType {
				continue
			}
		}
		matchProps := true
		for k, v := range f.PropertyEquals {
			if node.Prop(k) != v {
				matchProps = false
				break
			}
		}
		if !matchProps {
			continue
		}
		if q := strings.TrimSpace(strings.ToLower(f.Query)); q != "" {
			if !strings.Contains(strings.ToLower(node.Name), q) && !strings.Contains(strings.ToLower(node.Content), q) {
				continue
			}
		}
		out = append(out, node)
	}
	return out, nil
}
func (s *protocolStore) SetNodeProperties(_ context.Context, id string, props map[string]string) error {
	node := s.nodes[id]
	if node.Properties == nil {
		node.Properties = map[string]string{}
	}
	for k, v := range props {
		node.Properties[k] = v
	}
	s.nodes[id] = node
	return nil
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
func (s *protocolStore) UpsertMemoryRecord(_ context.Context, record db.MemoryRecord) error {
	s.records[record.Node.ID] = record
	return nil
}
func (s *protocolStore) GetMemoryRecord(_ context.Context, nodeID string) (db.MemoryRecord, error) {
	record, ok := s.records[nodeID]
	if !ok {
		return db.MemoryRecord{}, sql.ErrNoRows
	}
	return record, nil
}
func (s *protocolStore) GetMemoryRecordByKey(_ context.Context, scopeType string, scopeID string, knowledgeType string, memoryKey string) (db.MemoryRecord, error) {
	for _, record := range s.records {
		if record.ScopeType == scopeType && record.ScopeID == scopeID && record.KnowledgeType == knowledgeType && record.MemoryKey == memoryKey {
			return record, nil
		}
	}
	return db.MemoryRecord{}, sql.ErrNoRows
}
func (*protocolStore) InsertMemoryRevision(context.Context, db.MemoryRevision) error { return nil }
func (*protocolStore) ListMemoryRevisions(context.Context, string) ([]db.MemoryRevision, error) {
	return nil, nil
}
func (s *protocolStore) SearchMemoryRecords(_ context.Context, filter db.MemorySearchFilter) ([]db.MemoryRecord, error) {
	var out []db.MemoryRecord
	for _, record := range s.records {
		if filter.ScopeType != "" && record.ScopeType != filter.ScopeType {
			continue
		}
		if filter.ScopeID != "" && record.ScopeID != filter.ScopeID {
			continue
		}
		if filter.KnowledgeType != "" && record.KnowledgeType != filter.KnowledgeType {
			continue
		}
		out = append(out, record)
	}
	return out, nil
}
func (*protocolStore) SetMemoryLifecycle(context.Context, string, string, string, string) error {
	return nil
}
func (*protocolStore) SaveWebCorpus(context.Context, db.WebCorpus) error             { return nil }
func (*protocolStore) SaveWebCrawlVersion(context.Context, db.WebCrawlVersion) error { return nil }
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
	for _, name := range []string{"hybrid_semantic_search", "multi_query_search", "best_vector_match", "graph_neighbors", "graph_neighbors_cross_corpus", "store_memory", "update_memory", "deprecate_memory", "search_project_knowledge", "search_shared_knowledge", "search_global_preferences", "get_memory_history", "crawl_url", "crawl_website", "index_codebase", "search_codebase"} {
		if !names[name] {
			t.Fatalf("expected MCP tool %q, got %v", name, names)
		}
	}

	result, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "store_memory",
		Arguments: map[string]any{
			"scope_type":     "shared",
			"scope_id":       "team",
			"knowledge_type": "decision",
			"memory_key":     "test",
			"title":          "Test memory",
			"content":        "Remember protocol behavior.",
			"source":         "user",
			"writer_id":      "agent:test",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("store_memory returned tool error: %+v", result.Content)
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

	result, err = session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "search_codebase",
		Arguments: map[string]any{
			"path":  codebase,
			"query": "Agent-indexed",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("search_codebase returned tool error: %+v", result.Content)
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

func TestSearchRecordsAttribution(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	store := newProtocolStore()
	// KeywordSearch on the fake returns one node, so a search yields one hit.
	store.nodes["func:foo"] = db.Node{ID: "func:foo", Type: "func", Name: "Foo", Content: "does foo"}

	wrapper := NewMCPServerWrapper(store, nil)
	clientTransport, serverTransport := mcpsdk.NewInMemoryTransports()
	go func() { _ = wrapper.server.Run(ctx, serverTransport) }()
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "raph-test", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	result, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "hybrid_semantic_search",
		Arguments: map[string]any{"query": "foo"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("search returned tool error: %+v", result.Content)
	}

	// A search must attribute both the query and the node it surfaced — this is
	// what the studio "most accessed" view reads. (Regression guard for search
	// attribution silently going missing.)
	var searches, hits int
	for _, e := range store.accesses {
		switch e.Kind {
		case "search":
			if e.Query != "foo" {
				t.Fatalf("expected search query 'foo', got %q", e.Query)
			}
			searches++
		case "hit":
			if e.NodeID == "" {
				t.Fatalf("hit event must reference a node id")
			}
			hits++
		}
	}
	if searches != 1 {
		t.Fatalf("expected 1 search event, got %d (%+v)", searches, store.accesses)
	}
	if hits != 1 {
		t.Fatalf("expected 1 hit event, got %d (%+v)", hits, store.accesses)
	}
}

func TestUpdateMemoryByNodeID(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	store := newProtocolStore()
	rec := db.MemoryRecord{
		Node:           db.Node{ID: "memory:abc", Type: "memory", Name: "Old title", Content: "old content"},
		ScopeType:      "project",
		ScopeID:        "proj",
		KnowledgeType:  "decision",
		MemoryKey:      "the-key",
		Source:         "agent",
		WriterID:       "agent:one",
		LifecycleState: "active",
		Revision:       1,
	}
	store.records[rec.Node.ID] = rec
	store.nodes[rec.Node.ID] = rec.Node

	wrapper := NewMCPServerWrapper(store, nil)
	clientTransport, serverTransport := mcpsdk.NewInMemoryTransports()
	go func() { _ = wrapper.server.Run(ctx, serverTransport) }()
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "raph-test", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	// Update by node id alone — no scope/knowledge_type/memory_key supplied.
	result, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "update_memory",
		Arguments: map[string]any{
			"node_id": "memory:abc",
			"title":   "New title",
			"content": "new content",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("update_memory by node id returned tool error: %+v", result.Content)
	}

	got := store.records["memory:abc"]
	if got.Node.Content != "new content" || got.Node.Name != "New title" {
		t.Fatalf("memory not updated: name=%q content=%q", got.Node.Name, got.Node.Content)
	}
	// Authorship defaulted from the record since the caller omitted it.
	if got.WriterID != "agent:one" {
		t.Fatalf("expected writer preserved, got %q", got.WriterID)
	}
}

func TestReadDocumentResolvesByIDOrQuery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	store := newProtocolStore()
	handoff := func(id, name string) db.Node {
		return db.Node{
			ID: id, Type: "doc", Name: name, Content: name + " body",
			URL:        "knowledge://ws/" + id,
			Properties: map[string]string{"doc_type": "handoff", "status": "fresh"},
		}
	}
	// Two handoffs match "deploy"; one uniquely matches "billing".
	store.nodes["doc:h1"] = handoff("doc:h1", "Deploy pipeline handoff")
	store.nodes["doc:h2"] = handoff("doc:h2", "Deploy rollback handoff")
	store.nodes["doc:h3"] = handoff("doc:h3", "Billing migration handoff")

	wrapper := NewMCPServerWrapper(store, nil)
	clientTransport, serverTransport := mcpsdk.NewInMemoryTransports()
	go func() { _ = wrapper.server.Run(ctx, serverTransport) }()
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "raph-test", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	readDoc := func(args map[string]any) ReadDocumentOutput {
		t.Helper()
		result, err := session.CallTool(ctx, &mcpsdk.CallToolParams{Name: "read_document", Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		if result.IsError {
			t.Fatalf("read_document returned tool error: %+v", result.Content)
		}
		text, ok := result.Content[0].(*mcpsdk.TextContent)
		if !ok {
			t.Fatalf("expected text content, got %T", result.Content[0])
		}
		var out ReadDocumentOutput
		if err := json.Unmarshal([]byte(text.Text), &out); err != nil {
			t.Fatalf("unmarshal output: %v", err)
		}
		return out
	}

	// (1) Ambiguous query → candidates, and nothing is read or claimed.
	out := readDoc(map[string]any{"query": "Deploy", "doc_type": "handoff"})
	if len(out.Candidates) != 2 {
		t.Fatalf("expected 2 candidates for ambiguous query, got %d (%+v)", len(out.Candidates), out.Candidates)
	}
	if out.Document != nil {
		t.Fatalf("ambiguous query must not read a document, got %+v", out.Document)
	}
	if s := store.nodes["doc:h1"].Prop("status"); s != "fresh" {
		t.Fatalf("ambiguous query must not claim a handoff, doc:h1 status=%q", s)
	}

	// (2) Unique query → document read, but as a peek (status stays fresh).
	out = readDoc(map[string]any{"query": "Billing", "doc_type": "handoff"})
	if out.Document == nil || out.Document.Node.ID != "doc:h3" {
		t.Fatalf("expected unique query to read doc:h3, got %+v", out.Document)
	}
	if out.Resolved != "query" {
		t.Fatalf("expected resolved=query, got %q", out.Resolved)
	}
	if s := store.nodes["doc:h3"].Prop("status"); s != "fresh" {
		t.Fatalf("query-resolved read must be a peek, doc:h3 status=%q", s)
	}

	// (3) Explicit id → read and claimed (status becomes used).
	out = readDoc(map[string]any{"id": "doc:h3"})
	if out.Document == nil || out.Resolved != "id" {
		t.Fatalf("expected id read of doc:h3, got %+v (resolved=%q)", out.Document, out.Resolved)
	}
	if s := store.nodes["doc:h3"].Prop("status"); s != "used" {
		t.Fatalf("id read of a handoff must claim it, doc:h3 status=%q", s)
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
