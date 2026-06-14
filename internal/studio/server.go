package studio

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"raph/internal/config"
	"raph/internal/crawler"
	"raph/internal/db"
	"raph/internal/indexer"
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

func (s *StudioServer) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/graph", s.handleGetGraph)
	mux.HandleFunc("/api/node", s.handleGetNode)
	mux.HandleFunc("/api/node/delete", s.handleDeleteNode)
	mux.HandleFunc("/api/edge/create", s.handleCreateEdge)
	mux.HandleFunc("/api/search", s.handleSearch)
	mux.HandleFunc("/api/neighbors", s.handleNeighbors)
	mux.HandleFunc("/api/sqlite", s.handleSQLite)
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
		Handler:           mux,
		ReadHeaderTimeout: studioReadHeaderTimeout,
		ReadTimeout:       studioReadTimeout,
		WriteTimeout:      studioWriteTimeout,
		IdleTimeout:       studioIdleTimeout,
	}
	return server.ListenAndServe()
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

	nodes, edges, err := s.store.GetAllGraphElements(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for idx := range nodes {
		nodes[idx].Content = previewContent(nodes[idx].Content, 640)
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

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func previewContent(content string, maxRunes int) string {
	if maxRunes <= 0 || len(content) == 0 {
		return ""
	}
	count := 0
	for idx := range content {
		if count == maxRunes {
			return content[:idx] + "\n..."
		}
		count++
	}
	return content
}
