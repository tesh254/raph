package studio

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"raph/internal/config"
	"raph/internal/db"
	"raph/internal/verbose"
)

type StudioServer struct {
	store  *db.LibSQLStore
	config *config.Config
	port   int
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

func NewStudioServer(store *db.LibSQLStore, port int) *StudioServer {
	return &StudioServer{store: store, port: port}
}

func (s *StudioServer) SetConfig(cfg *config.Config) {
	s.config = cfg
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

	addr := ":" + strconv.Itoa(s.port)
	fmt.Printf("raph Studio active at http://localhost:%d\n", s.port)
	verbose.Printf("studio routes ready at %s", addr)
	return http.ListenAndServe(addr, mux)
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
	node, err := s.store.GetNodeByID(r.Context(), id)
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
	_ = json.NewEncoder(w).Encode(node)
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
	tables, err := s.store.InspectTables(r.Context(), limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	verbose.Printf("studio sqlite request tables=%d limit=%d", len(tables), limit)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(SQLiteResponse{Tables: tables})
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
