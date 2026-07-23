package studio

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"raph/internal/config"
	"raph/internal/crawler"
	"raph/internal/db"
	"raph/internal/indexer"
	"raph/internal/knowledge"
	"raph/internal/memory"
	"raph/internal/verbose"
)

type StudioServer struct {
	store         *db.LibSQLStore
	config        *config.Config
	host          string
	port          int
	workspaceRoot string
	seedURL       string
}

type GraphPayload struct {
	Nodes []db.Node `json:"nodes"`
	Edges []db.Edge `json:"edges"`
}

type SearchRequest struct {
	Query string `json:"query"`
	Limit int    `json:"limit,omitempty"`
}

type SearchResponse struct {
	Mode    string    `json:"mode"`
	Matches []db.Node `json:"matches"`
}

type NeighborRequest struct {
	NodeID string `json:"node_id"`
}

type NeighborResponse struct {
	Nodes []db.Node `json:"nodes"`
	Edges []db.Edge `json:"edges"`
}

type SQLiteResponse struct {
	Tables []db.TableDump `json:"tables"`
}

const (
	defaultStudioHost       = "127.0.0.1"
	studioReadHeaderTimeout = 5 * time.Second
	studioReadTimeout       = 15 * time.Second
	studioWriteTimeout      = 30 * time.Second
	studioIdleTimeout       = 60 * time.Second
)

func NewStudioServer(store *db.LibSQLStore, host string, port int) *StudioServer {
	workspaceRoot, err := os.Getwd()
	if err != nil {
		workspaceRoot = "."
	}
	host = strings.TrimSpace(host)
	if host == "" {
		host = defaultStudioHost
	}
	return &StudioServer{
		store:         store,
		host:          host,
		port:          port,
		workspaceRoot: workspaceRoot,
		seedURL:       "https://example.com",
	}
}

func (s *StudioServer) SetConfig(cfg *config.Config) {
	s.config = cfg
}

func (s *StudioServer) SetWorkspaceRoot(root string) {
	root = strings.TrimSpace(root)
	if root != "" {
		s.workspaceRoot = root
	}
}

func (s *StudioServer) SetSeedURL(rawURL string) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL != "" {
		s.seedURL = rawURL
	}
}

// Start serves the studio UI until ctx is cancelled, then drains in-flight
// requests with a bounded graceful shutdown.
func (s *StudioServer) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/graph", s.handleGetGraph)
	mux.HandleFunc("/api/node", s.handleGetNode)
	mux.HandleFunc("/api/node/delete", s.handleDeleteNode)
	mux.HandleFunc("/api/edge/create", s.handleCreateEdge)
	mux.HandleFunc("/api/search", s.handleSearch)
	mux.HandleFunc("/api/neighbors", s.handleNeighbors)
	mux.HandleFunc("/api/sqlite", s.handleSQLite)
	mux.HandleFunc("/api/activity", s.handleActivity)
	mux.HandleFunc("/api/stats", s.handleStats)
	mux.HandleFunc("/api/analytics", s.handleAnalytics)
	mux.HandleFunc("/api/timeline", s.handleTimeline)
	mux.HandleFunc("/api/memories", s.handleListMemories)
	mux.HandleFunc("/api/memory", s.handleGetMemory)
	mux.HandleFunc("/api/memory/update", s.handleUpdateMemory)
	mux.HandleFunc("/api/memory/delete", s.handleDeleteMemory)
	mux.HandleFunc("/api/handoffs", s.handleListHandoffs)
	mux.HandleFunc("/api/document", s.handleGetDocument)
	mux.HandleFunc("/api/document/update", s.handleUpdateDocument)
	mux.HandleFunc("/api/actions/clear", s.handleClearDB)
	mux.HandleFunc("/api/actions/init", s.handleInitDemo)

	addr := s.host + ":" + strconv.Itoa(s.port)
	displayHost := s.host
	if displayHost == defaultStudioHost {
		displayHost = "localhost"
	}
	fmt.Printf("raph Studio active at http://%s:%d\n", displayHost, s.port)
	verbose.Printf("studio routes ready at %s", addr)
	server := &http.Server{
		Addr:              addr,
		Handler:           s.withSecurity(mux),
		ReadHeaderTimeout: studioReadHeaderTimeout,
		ReadTimeout:       studioReadTimeout,
		WriteTimeout:      studioWriteTimeout,
		IdleTimeout:       studioIdleTimeout,
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		verbose.Printf("studio shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), studioWriteTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return err
		}
		return nil
	}
}

func (s *StudioServer) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(indexHTML))
}

func (s *StudioServer) handleGetGraph(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Lean read: content is capped in SQL and embeddings are never loaded — the
	// graph view only needs a short preview per node.
	nodes, edges, err := s.store.GraphElementsLean(r.Context(), 640)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	verbose.Printf("studio graph request served nodes=%d edges=%d", len(nodes), len(edges))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(GraphPayload{Nodes: nodes, Edges: edges})
}

func (s *StudioServer) handleGetNode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		http.Error(w, "id is required", http.StatusBadRequest)
		return
	}
	details, err := s.store.GetStudioNodeDetails(r.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "node not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = s.store.RecordAccess(r.Context(), id, "view", "")
	verbose.Printf("studio node request id=%s", id)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(details)
}

func (s *StudioServer) handleDeleteNode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json body", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.ID) == "" {
		http.Error(w, "id is required", http.StatusBadRequest)
		return
	}
	if err := s.store.DeleteNodeByID(r.Context(), req.ID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	verbose.Printf("studio delete node id=%s", req.ID)
	w.WriteHeader(http.StatusOK)
}

func (s *StudioServer) handleCreateEdge(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var edge db.Edge
	if err := json.NewDecoder(r.Body).Decode(&edge); err != nil {
		http.Error(w, "invalid json body", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(edge.SourceID) == "" || strings.TrimSpace(edge.TargetID) == "" || strings.TrimSpace(edge.Type) == "" {
		http.Error(w, "source_id, target_id, and type are required", http.StatusBadRequest)
		return
	}
	if err := s.store.SaveEdge(r.Context(), edge); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	verbose.Printf("studio create edge %s -> %s type=%s", edge.SourceID, edge.TargetID, edge.Type)
	w.WriteHeader(http.StatusCreated)
}

func (s *StudioServer) handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req SearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json body", http.StatusBadRequest)
		return
	}
	query := strings.TrimSpace(req.Query)
	if query == "" {
		http.Error(w, "query is required", http.StatusBadRequest)
		return
	}
	if req.Limit <= 0 {
		req.Limit = 5
	}
	_ = s.store.RecordAccess(r.Context(), "", "search", query)

	resp := SearchResponse{Mode: "keyword"}
	verbose.Printf("studio search query=%q limit=%d", query, req.Limit)

	// Try semantic search first if embeddings are configured
	if s.config != nil && s.config.HasEmbeddingProvider() {
		vec, err := config.GenerateEmbedding(r.Context(), s.config, query)
		if err == nil && len(vec) > 0 {
			nodes, searchErr := s.store.VectorSearch(r.Context(), vec, req.Limit)
			if searchErr == nil && len(nodes) > 0 {
				resp.Mode = "semantic"
				resp.Matches = nodes
				verbose.Printf("studio search semantic hits=%d", len(nodes))
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(resp)
				return
			}
		}
	}

	// Fall back to keyword search
	nodes, err := s.store.KeywordSearch(r.Context(), query, req.Limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	resp.Matches = nodes
	verbose.Printf("studio search keyword hits=%d", len(nodes))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *StudioServer) handleNeighbors(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req NeighborRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json body", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.NodeID) == "" {
		http.Error(w, "node_id is required", http.StatusBadRequest)
		return
	}

	nodes, edges, err := s.store.GetNeighbors(r.Context(), req.NodeID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = s.store.RecordAccess(r.Context(), req.NodeID, "neighbors", "")
	verbose.Printf("studio neighbors node=%s nodes=%d edges=%d", req.NodeID, len(nodes), len(edges))

	resp := NeighborResponse{Nodes: nodes, Edges: edges}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *StudioServer) handleSQLite(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	limit := 250
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	if limit > 1000 {
		limit = 1000
	}
	tables, err := s.store.InspectTables(r.Context(), limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	verbose.Printf("studio sqlite request tables=%d limit=%d", len(tables), limit)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(SQLiteResponse{Tables: tables})
}

func (s *StudioServer) handleClearDB(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := s.store.ClearAll(r.Context()); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	verbose.Printf("studio clear db")
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"message": "Cleared local graph database",
	})
}

func (s *StudioServer) handleInitDemo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	result, err := s.initDemoData(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	verbose.Printf("studio init workspace=%s seed=%s files=%d pages=%d", result.WorkspaceRoot, result.SeedURL, result.Index.FilesIndexed, result.Crawl.PagesIndexed)
	writeJSON(w, http.StatusOK, result)
}

type InitDemoResponse struct {
	OK            bool          `json:"ok"`
	WorkspaceRoot string        `json:"workspace_root"`
	SeedURL       string        `json:"seed_url"`
	Index         indexer.Stats `json:"index"`
	Crawl         crawler.Stats `json:"crawl"`
}

func (s *StudioServer) initDemoData(ctx context.Context) (InitDemoResponse, error) {
	if err := s.store.ClearAll(ctx); err != nil {
		return InitDemoResponse{}, fmt.Errorf("clear local graph database: %w", err)
	}

	idx, err := indexer.New(s.store, s.config, s.workspaceRoot, false)
	if err != nil {
		return InitDemoResponse{}, fmt.Errorf("prepare indexer: %w", err)
	}
	indexStats, err := idx.Run(ctx)
	if err != nil {
		return InitDemoResponse{}, fmt.Errorf("index workspace: %w", err)
	}

	docCrawler, err := crawler.NewDocumentationCrawler(s.store, s.config, s.seedURL)
	if err != nil {
		return InitDemoResponse{}, fmt.Errorf("prepare crawler: %w", err)
	}
	if err := docCrawler.Run(ctx); err != nil {
		return InitDemoResponse{}, fmt.Errorf("crawl seed URL: %w", err)
	}

	return InitDemoResponse{
		OK:            true,
		WorkspaceRoot: s.workspaceRoot,
		SeedURL:       s.seedURL,
		Index:         indexStats,
		Crawl:         docCrawler.Stats(),
	}, nil
}

// withSecurity locks down the local studio server. The graph holds the user's
// private memory, rules, handoffs and indexed source, and there is no auth, so
// the browser threat model matters: any site the developer visits can reach a
// loopback server. This middleware closes three holes that a reflected-origin,
// permissive CORS policy left open:
//
//  1. DNS-rebinding — reject requests whose Host header isn't a loopback name
//     (or the operator's explicitly-chosen --host).
//  2. Cross-origin reads — only echo Access-Control-Allow-Origin for an
//     allowlisted origin, so a foreign page can't read /api/sqlite etc.
//  3. Cross-origin state changes (CSRF) — reject mutating requests carrying a
//     non-allowlisted Origin, so a foreign page can't trigger ClearAll/delete.
//
// The hosted dashboard (a different origin) can be allowlisted explicitly via
// RAPH_STUDIO_ALLOWED_ORIGINS (comma-separated) instead of reflecting everything.
func (s *StudioServer) withSecurity(next http.Handler) http.Handler {
	allowedHosts := s.allowedHosts()
	extraOrigins := extraAllowedOrigins()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !hostAllowed(r.Host, allowedHosts) {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}

		origin := strings.TrimSpace(r.Header.Get("Origin"))
		originOK := origin != "" && s.originAllowed(origin, extraOrigins)
		if originOK {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		}
		if r.Method == http.MethodOptions {
			if originOK {
				w.WriteHeader(http.StatusNoContent)
			} else {
				http.Error(w, "forbidden origin", http.StatusForbidden)
			}
			return
		}

		// A cross-origin page can issue a "simple" POST without a preflight, so
		// CORS alone doesn't stop CSRF against the mutating endpoints. Reject any
		// state-changing request that carries a foreign Origin.
		if r.Method != http.MethodGet && r.Method != http.MethodHead && origin != "" && !originOK {
			http.Error(w, "forbidden origin", http.StatusForbidden)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// allowedHosts is the set of Host header values (host portion, no port) served.
func (s *StudioServer) allowedHosts() map[string]struct{} {
	hosts := map[string]struct{}{
		"127.0.0.1": {},
		"localhost": {},
		"::1":       {},
	}
	if h := strings.ToLower(strings.TrimSpace(s.host)); h != "" {
		hosts[h] = struct{}{}
	}
	return hosts
}

// hostedStudioOrigin is the deployed dashboard that legitimately reads a local
// studio server cross-origin. Additional origins can be added via
// RAPH_STUDIO_ALLOWED_ORIGINS.
const hostedStudioOrigin = "https://raph-studio.pages.dev"

// originAllowed reports whether a browser origin may read/mutate. It permits any
// loopback origin regardless of port — those come from the developer's own
// machine (the studio UI on :4545, or a dev build of the dashboard on some other
// localhost port), and a drive-by attacker's page is never served from loopback.
// The Host-header guard independently blocks DNS-rebinding. The hosted dashboard
// and any RAPH_STUDIO_ALLOWED_ORIGINS entries are also permitted.
func (s *StudioServer) originAllowed(origin string, extra map[string]struct{}) bool {
	lower := strings.ToLower(origin)
	if lower == hostedStudioOrigin {
		return true
	}
	if _, ok := extra[lower]; ok {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	if isLoopbackHost(u.Hostname()) {
		return true
	}
	// An operator serving on an explicit non-loopback --host may reach it from a
	// same-host origin.
	if h := strings.ToLower(strings.TrimSpace(s.host)); h != "" && h != defaultStudioHost && strings.EqualFold(u.Hostname(), h) {
		return true
	}
	return false
}

func isLoopbackHost(host string) bool {
	switch strings.ToLower(host) {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// extraAllowedOrigins parses RAPH_STUDIO_ALLOWED_ORIGINS (comma-separated).
func extraAllowedOrigins() map[string]struct{} {
	origins := map[string]struct{}{}
	for _, o := range strings.Split(os.Getenv("RAPH_STUDIO_ALLOWED_ORIGINS"), ",") {
		if o = strings.ToLower(strings.TrimSpace(o)); o != "" {
			origins[o] = struct{}{}
		}
	}
	return origins
}

func hostAllowed(hostHeader string, allowed map[string]struct{}) bool {
	h := hostHeader
	if host, _, err := net.SplitHostPort(hostHeader); err == nil {
		h = host
	}
	h = strings.ToLower(strings.TrimSpace(strings.Trim(h, "[]")))
	if h == "" {
		return false
	}
	_, ok := allowed[h]
	return ok
}

// ActivityItem is a recently changed node, surfaced as a near-realtime feed of
// what agents and the sync worker are doing.
type ActivityItem struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Domain    string `json:"domain"`
	Name      string `json:"name"`
	URL       string `json:"url,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
	DocType   string `json:"doc_type,omitempty"`
	Status    string `json:"status,omitempty"`
}

func (s *StudioServer) handleActivity(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	limit := 40
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 200 {
			limit = parsed
		}
	}
	nodes, err := s.store.ListNodes(r.Context(), db.NodeFilter{Limit: limit})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	items := make([]ActivityItem, 0, len(nodes))
	for _, n := range nodes {
		items = append(items, ActivityItem{
			ID: n.ID, Type: n.Type, Domain: n.Domain, Name: n.Name, URL: n.URL,
			UpdatedAt: n.UpdatedAt, DocType: n.Prop("doc_type"), Status: n.Prop("status"),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *StudioServer) handleAnalytics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	limit := 10
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 50 {
			limit = parsed
		}
	}
	analytics, err := s.store.Analytics(r.Context(), limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, analytics)
}

func (s *StudioServer) handleTimeline(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	days := 30
	if raw := strings.TrimSpace(r.URL.Query().Get("days")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 365 {
			days = parsed
		}
	}
	timeline, err := s.store.Timeline(r.Context(), days)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, timeline)
}

// Memory & handoff browsing/editing. These power the studio's Memory and
// Handovers pages: list, read (with revision history), and edit.

func (s *StudioServer) handleListMemories(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	limit := 200
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 1000 {
			limit = parsed
		}
	}
	// No lifecycle filter: surface active and deprecated so users see the full
	// picture (the UI badges the state).
	records, err := s.store.SearchMemoryRecords(r.Context(), db.MemorySearchFilter{
		Query:         strings.TrimSpace(r.URL.Query().Get("query")),
		KnowledgeType: strings.TrimSpace(r.URL.Query().Get("type")),
		ScopeType:     strings.TrimSpace(r.URL.Query().Get("scope")),
		Limit:         limit,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": records})
}

func (s *StudioServer) handleGetMemory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		http.Error(w, "id is required", http.StatusBadRequest)
		return
	}
	record, err := s.store.GetMemoryRecord(r.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "memory not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	revisions, err := s.store.ListMemoryRevisions(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = s.store.RecordAccess(r.Context(), id, "view", "")
	writeJSON(w, http.StatusOK, map[string]any{"record": record, "revisions": revisions})
}

func (s *StudioServer) handleUpdateMemory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		NodeID  string   `json:"node_id"`
		Title   string   `json:"title"`
		Content string   `json:"content"`
		Tags    []string `json:"tags"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json body", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.NodeID) == "" {
		http.Error(w, "node_id is required", http.StatusBadRequest)
		return
	}
	// Invalid input is a 400; a failed persistence/embedding call below is a 500.
	if strings.TrimSpace(req.Content) == "" {
		http.Error(w, "content is required", http.StatusBadRequest)
		return
	}
	existing, err := s.store.GetMemoryRecord(r.Context(), req.NodeID)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "memory not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Edit content/title/tags while preserving the record's identity (scope,
	// knowledge type, key) and authorship. Update bumps the revision and appends
	// the prior version to history.
	out, err := memory.Update(r.Context(), s.store, s.config, memory.UpdateInput{
		ScopeType:     existing.ScopeType,
		ScopeID:       existing.ScopeID,
		KnowledgeType: existing.KnowledgeType,
		MemoryKey:     existing.MemoryKey,
		Title:         req.Title,
		Content:       req.Content,
		Source:        existing.Source,
		WriterID:      existing.WriterID,
		Tags:          req.Tags,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	verbose.Printf("studio update memory id=%s revision=%d", req.NodeID, out.Record.Revision)
	writeJSON(w, http.StatusOK, out.Record)
}

func (s *StudioServer) handleDeleteMemory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		NodeID string `json:"node_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json body", http.StatusBadRequest)
		return
	}
	id := strings.TrimSpace(req.NodeID)
	if id == "" {
		http.Error(w, "node_id is required", http.StatusBadRequest)
		return
	}
	// Confirm it's a memory before deleting so this endpoint can't be used to
	// remove arbitrary graph nodes.
	if _, err := s.store.GetMemoryRecord(r.Context(), id); err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "memory not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Hard delete: removing the node cascades to its memory_records row,
	// revisions, and edges (all FK ON DELETE CASCADE).
	if err := s.store.DeleteNodeByID(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	verbose.Printf("studio delete memory id=%s", id)
	w.WriteHeader(http.StatusOK)
}

func (s *StudioServer) handleListHandoffs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	nodes, err := knowledge.List(r.Context(), s.store, knowledge.ListFilter{
		DocType: knowledge.DocHandoff,
		Status:  strings.TrimSpace(r.URL.Query().Get("status")),
		Query:   strings.TrimSpace(r.URL.Query().Get("query")),
		Limit:   200,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": nodes})
}

func (s *StudioServer) handleGetDocument(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		http.Error(w, "id is required", http.StatusBadRequest)
		return
	}
	// Peek (markUsed=false): browsing a handoff in studio must not claim it out
	// from under the next agent.
	doc, err := knowledge.Read(r.Context(), s.store, id, false, "studio")
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "document not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = s.store.RecordAccess(r.Context(), id, "view", "")
	writeJSON(w, http.StatusOK, doc)
}

func (s *StudioServer) handleUpdateDocument(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		ID      string   `json:"id"`
		Title   string   `json:"title"`
		Content string   `json:"content"`
		Tags    []string `json:"tags"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json body", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.ID) == "" {
		http.Error(w, "id is required", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Content) == "" {
		http.Error(w, "content is required", http.StatusBadRequest)
		return
	}
	node, err := s.store.GetNodeByID(r.Context(), req.ID)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "document not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// A doc's stable key lives in its URL: knowledge://<workspace>/<key>. We need
	// it so Add overwrites this document in place instead of creating a new one.
	key := strings.TrimPrefix(node.URL, "knowledge://"+node.Workspace+"/")
	if key == "" || key == node.URL {
		http.Error(w, "cannot resolve document key", http.StatusUnprocessableEntity)
		return
	}
	docType := node.Prop("doc_type")
	if docType == "" {
		docType = knowledge.DocHandoff
	}
	tags := req.Tags
	if len(tags) == 0 {
		if existing := strings.TrimSpace(node.Prop("tags")); existing != "" {
			tags = strings.Split(existing, ",")
		}
	}
	// Carry over all existing properties so lifecycle metadata (status, used_at,
	// used_by, freshness) survives the edit; Add overrides the fields it manages.
	props := make(map[string]string, len(node.Properties))
	for k, v := range node.Properties {
		props[k] = v
	}
	doc, err := knowledge.Add(r.Context(), s.store, s.config, knowledge.AddInput{
		Workspace:  node.Workspace,
		Key:        key,
		Title:      req.Title,
		Content:    req.Content,
		DocType:    docType,
		Source:     node.Prop("source"),
		WriterID:   node.Prop("writer_id"),
		Tags:       tags,
		Properties: props,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	verbose.Printf("studio update document id=%s", req.ID)
	writeJSON(w, http.StatusOK, doc)
}

func (s *StudioServer) handleStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Counts come from SQL aggregates, not a full node/edge scan, so polling this
	// on a large graph stays cheap.
	stats, err := s.store.GraphStats(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"nodes":      stats.Nodes,
		"edges":      stats.Edges,
		"workspaces": stats.Workspaces,
		"by_type":    stats.ByType,
		"by_domain":  stats.ByDomain,
	})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
