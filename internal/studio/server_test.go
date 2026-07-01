package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"raph/internal/db"
)

func TestStudioInitAndClearActions(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("RAPH_CRAWL_ALLOW_PRIVATE", "1") // init crawls a loopback httptest server

	store, err := db.InitStorage()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "README.md"), []byte("# raph\n\nStudio bootstrap data."), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head><title>Example</title></head><body><main><h1>Example Domain</h1><p>Studio init test.</p></main></body></html>`))
	}))
	defer site.Close()

	srv := NewStudioServer(store, "", 0)
	srv.SetWorkspaceRoot(workspace)
	srv.SetSeedURL(site.URL)

	initReq := httptest.NewRequest(http.MethodPost, "/api/actions/init", nil)
	initRec := httptest.NewRecorder()
	srv.handleInitDemo(initRec, initReq)

	if initRec.Code != http.StatusOK {
		t.Fatalf("unexpected init status: %d body=%s", initRec.Code, initRec.Body.String())
	}

	var initResp InitDemoResponse
	if err := json.NewDecoder(initRec.Body).Decode(&initResp); err != nil {
		t.Fatal(err)
	}
	if !initResp.OK {
		t.Fatalf("expected successful init response: %+v", initResp)
	}
	if initResp.WorkspaceRoot != workspace {
		t.Fatalf("unexpected workspace root: %q", initResp.WorkspaceRoot)
	}
	if initResp.SeedURL != site.URL {
		t.Fatalf("unexpected seed URL: %q", initResp.SeedURL)
	}
	if initResp.Index.FilesIndexed == 0 {
		t.Fatalf("expected indexed files: %+v", initResp.Index)
	}
	if initResp.Crawl.PagesIndexed == 0 {
		t.Fatalf("expected crawled pages: %+v", initResp.Crawl)
	}

	nodes, _, err := store.GetAllGraphElements(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) == 0 {
		t.Fatal("expected graph nodes after studio init")
	}

	clearReq := httptest.NewRequest(http.MethodPost, "/api/actions/clear", nil)
	clearRec := httptest.NewRecorder()
	srv.handleClearDB(clearRec, clearReq)
	if clearRec.Code != http.StatusOK {
		t.Fatalf("unexpected clear status: %d body=%s", clearRec.Code, clearRec.Body.String())
	}

	nodes, _, err = store.GetAllGraphElements(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 0 {
		t.Fatalf("expected empty graph after clear, still had %d nodes", len(nodes))
	}
}

func TestStudioNodeEndpointIncludesMetadata(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	store, err := db.InitStorage()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	node := db.Node{
		ID:        "memory:test",
		Workspace: "ws:test",
		Domain:    "memory",
		Type:      "memory",
		Name:      "Stored preference",
		Content:   "Use explicit scopes.",
		URL:       "https://example.com/docs/test",
	}
	if err := store.SaveNode(context.Background(), node); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertMemoryRecord(context.Background(), db.MemoryRecord{
		Node:           node,
		MemoryKey:      "prefs/scopes",
		ScopeType:      "project",
		ScopeID:        "project:test",
		LifecycleState: "active",
		KnowledgeType:  "preference",
		Source:         "user",
		WriterID:       "agent:test",
		CreatedAt:      "2026-06-11T00:00:00Z",
		UpdatedAt:      "2026-06-11T01:00:00Z",
		NormalizedTags: []string{"explicit-scopes", "studio"},
		DisplayTags:    []string{"Explicit Scopes", "Studio"},
		Revision:       2,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveWebCorpus(context.Background(), db.WebCorpus{
		ID:        "corpus:test",
		ScopeType: "web",
		ScopeID:   "https://example.com",
		Source:    "crawl",
		BaseURL:   "https://example.com",
		CreatedAt: "2026-06-11T00:00:00Z",
		UpdatedAt: "2026-06-11T01:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveWebCrawlVersion(context.Background(), db.WebCrawlVersion{
		ID:        "crawl:test",
		CorpusID:  "corpus:test",
		SeedURL:   "https://example.com",
		CreatedAt: "2026-06-11T02:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}

	srv := NewStudioServer(store, "", 0)
	req := httptest.NewRequest(http.MethodGet, "/api/node?id="+node.ID, nil)
	rec := httptest.NewRecorder()
	srv.handleGetNode(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}

	var payload map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}

	if payload["id"] != node.ID {
		t.Fatalf("expected node id in payload, got %+v", payload)
	}
	if payload["memory"] == nil {
		t.Fatalf("expected memory metadata in payload, got %+v", payload)
	}
	if payload["web_corpus"] == nil {
		t.Fatalf("expected web corpus metadata in payload, got %+v", payload)
	}
	if payload["web_crawl_version"] == nil {
		t.Fatalf("expected web crawl version metadata in payload, got %+v", payload)
	}
}

func TestStudioSQLiteEndpointCapsRequestedLimit(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	store, err := db.InitStorage()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	for i := 0; i < 1005; i++ {
		if err := store.SaveNode(context.Background(), db.Node{
			ID:        fmt.Sprintf("node:%04d", i),
			Workspace: "ws:test",
			Domain:    "test",
			Type:      "chunk",
			Name:      "Test node",
			Content:   "SQLite cap test",
		}); err != nil {
			t.Fatal(err)
		}
	}

	srv := NewStudioServer(store, "", 0)
	req := httptest.NewRequest(http.MethodGet, "/api/sqlite?limit=5000", nil)
	rec := httptest.NewRecorder()
	srv.handleSQLite(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}

	var resp SQLiteResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	for _, table := range resp.Tables {
		if table.Name == "nodes" && len(table.Rows) != 1000 {
			t.Fatalf("expected nodes table capped to 1000 rows, got %d", len(table.Rows))
		}
	}
}

func TestStudioActivityAndStats(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store, err := db.InitStorage()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	for _, n := range []db.Node{
		{ID: "f1", Workspace: "ws", Domain: "code", Type: "func", Name: "A", Content: "a"},
		{ID: "d1", Workspace: "ws", Domain: "knowledge", Type: "doc", Name: "Doc", Content: "d", Properties: map[string]string{"doc_type": "handoff", "status": "fresh"}},
	} {
		if err := store.SaveNode(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	srv := NewStudioServer(store, "", 0)

	actRec := httptest.NewRecorder()
	srv.handleActivity(actRec, httptest.NewRequest(http.MethodGet, "/api/activity", nil))
	if actRec.Code != http.StatusOK {
		t.Fatalf("activity status %d", actRec.Code)
	}
	var act struct {
		Items []ActivityItem `json:"items"`
	}
	if err := json.Unmarshal(actRec.Body.Bytes(), &act); err != nil {
		t.Fatal(err)
	}
	if len(act.Items) != 2 {
		t.Fatalf("expected 2 activity items, got %d", len(act.Items))
	}

	statsRec := httptest.NewRecorder()
	srv.handleStats(statsRec, httptest.NewRequest(http.MethodGet, "/api/stats", nil))
	if statsRec.Code != http.StatusOK {
		t.Fatalf("stats status %d", statsRec.Code)
	}
	if !strings.Contains(statsRec.Body.String(), "\"nodes\": 2") && !strings.Contains(statsRec.Body.String(), "\"nodes\":2") {
		t.Fatalf("stats missing node count: %s", statsRec.Body.String())
	}
}

func TestStudioCORSPreflight(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store, err := db.InitStorage()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	srv := NewStudioServer(store, "127.0.0.1", 4545)
	handler := srv.withSecurity(http.NewServeMux())

	// Allowlisted hosted dashboard: preflight succeeds and echoes the origin.
	req := httptest.NewRequest(http.MethodOptions, "/api/graph", nil)
	req.Host = "127.0.0.1:4545"
	req.Header.Set("Origin", hostedStudioOrigin)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("preflight status %d", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != hostedStudioOrigin {
		t.Fatalf("missing CORS origin header: %v", rec.Header())
	}
}

func TestStudioRejectsForeignOrigin(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store, err := db.InitStorage()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	srv := NewStudioServer(store, "127.0.0.1", 4545)
	handler := srv.withSecurity(http.NewServeMux())

	// A foreign origin must not have the response exposed to it (no ACAO) and a
	// mutating request from it must be refused outright.
	get := httptest.NewRequest(http.MethodGet, "/api/sqlite", nil)
	get.Host = "127.0.0.1:4545"
	get.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, get)
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("leaked ACAO to foreign origin: %v", rec.Header())
	}

	post := httptest.NewRequest(http.MethodPost, "/api/actions/clear", nil)
	post.Host = "127.0.0.1:4545"
	post.Header.Set("Origin", "https://evil.example")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, post)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for cross-origin mutation, got %d", rec.Code)
	}
}

func TestStudioRejectsRebindingHost(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store, err := db.InitStorage()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	srv := NewStudioServer(store, "127.0.0.1", 4545)
	handler := srv.withSecurity(http.NewServeMux())

	req := httptest.NewRequest(http.MethodGet, "/api/sqlite", nil)
	req.Host = "attacker.example"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for foreign Host header, got %d", rec.Code)
	}
}
