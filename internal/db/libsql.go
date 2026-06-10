package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strings"

	"raph/internal/config"

	_ "modernc.org/sqlite"
)

type Node struct {
	ID              string    `json:"id"`
	Workspace       string    `json:"-"`
	Domain          string    `json:"domain"`
	Type            string    `json:"type"`
	Name            string    `json:"name"`
	Content         string    `json:"content"`
	URL             string    `json:"url,omitempty"`
	Path            string    `json:"path,omitempty"`
	Embedding       []float32 `json:"-"`
	EmbeddingLength int       `json:"embedding_length,omitempty"`
}

type Edge struct {
	SourceID string `json:"source_id"`
	TargetID string `json:"target_id"`
	Type     string `json:"type"`
}

type GraphStore interface {
	SaveNode(ctx context.Context, node Node) error
	SaveEdge(ctx context.Context, edge Edge) error
	VectorSearch(ctx context.Context, embedding []float32, limit int) ([]Node, error)
	VectorSearchWorkspace(ctx context.Context, workspace string, embedding []float32, limit int) ([]Node, error)
	KeywordSearch(ctx context.Context, query string, limit int) ([]Node, error)
	KeywordSearchWorkspace(ctx context.Context, workspace string, query string, limit int) ([]Node, error)
	GetNodeByID(ctx context.Context, id string) (Node, error)
	GetNeighbors(ctx context.Context, nodeID string) ([]Node, []Edge, error)
	GetAllGraphElements(ctx context.Context) ([]Node, []Edge, error)
	DeleteNodeByID(ctx context.Context, id string) error
	DeleteFileNodes(ctx context.Context, workspace string, relativePath string) error
	DeleteWorkspace(ctx context.Context, workspace string) error
	ClearAll(ctx context.Context) error
	Close() error
}

type LibSQLStore struct {
	db *sql.DB
}

type TableDump struct {
	Name    string              `json:"name"`
	Columns []string            `json:"columns"`
	Rows    []map[string]string `json:"rows"`
}

func InitStorage() (*LibSQLStore, error) {
	paths, err := config.EnsureBaseLayout()
	if err != nil {
		return nil, err
	}

	dbFile := filepath.Join(paths.DataDir, "brain.db")
	db, err := sql.Open("sqlite", dbFile)
	if err != nil {
		return nil, fmt.Errorf("failed to open embedded database: %w", err)
	}

	db.SetMaxOpenConns(1)
	store := &LibSQLStore{db: db}
	if err := store.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *LibSQLStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *LibSQLStore) migrate() error {
	queries := []string{
		`PRAGMA foreign_keys = ON;`,
		`PRAGMA journal_mode = WAL;`,
		`PRAGMA busy_timeout = 5000;`,
		`CREATE TABLE IF NOT EXISTS nodes (
			id TEXT PRIMARY KEY,
			workspace TEXT NOT NULL,
			domain TEXT NOT NULL,
			type TEXT NOT NULL,
			name TEXT NOT NULL,
			content TEXT NOT NULL,
			url TEXT,
			path TEXT NOT NULL DEFAULT '',
			embedding_json TEXT NOT NULL DEFAULT '[]'
		);`,
		`CREATE TABLE IF NOT EXISTS edges (
			source_id TEXT NOT NULL,
			target_id TEXT NOT NULL,
			type TEXT NOT NULL,
			PRIMARY KEY (source_id, target_id, type),
			FOREIGN KEY (source_id) REFERENCES nodes(id) ON DELETE CASCADE,
			FOREIGN KEY (target_id) REFERENCES nodes(id) ON DELETE CASCADE
		);`,
		`CREATE INDEX IF NOT EXISTS idx_nodes_workspace ON nodes (workspace);`,
		`CREATE INDEX IF NOT EXISTS idx_nodes_domain_type ON nodes (domain, type);`,
		`CREATE INDEX IF NOT EXISTS idx_edges_source_id ON edges (source_id);`,
		`CREATE INDEX IF NOT EXISTS idx_edges_target_id ON edges (target_id);`,
	}

	for _, q := range queries {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("migration execution failure: %w", err)
		}
	}
	if err := s.ensureNodePathColumn(); err != nil {
		return err
	}
	return nil
}

func (s *LibSQLStore) ensureNodePathColumn() error {
	rows, err := s.db.Query(`PRAGMA table_info(nodes)`)
	if err != nil {
		return fmt.Errorf("inspect nodes schema: %w", err)
	}

	found := false
	for rows.Next() {
		var cid int
		var name string
		var columnType string
		var notNull int
		var defaultValue any
		var primaryKey int
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return fmt.Errorf("scan nodes schema: %w", err)
		}
		if name == "path" {
			found = true
			break
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("inspect nodes schema: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close nodes schema rows: %w", err)
	}
	if found {
		return nil
	}
	if _, err := s.db.Exec(`ALTER TABLE nodes ADD COLUMN path TEXT NOT NULL DEFAULT ''`); err != nil {
		return fmt.Errorf("add nodes path column: %w", err)
	}
	return nil
}

func (s *LibSQLStore) InspectTables(ctx context.Context, limit int) ([]TableDump, error) {
	if limit <= 0 {
		limit = 250
	}
	rows, err := s.db.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list tables: %w", err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scan table name: %w", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate tables: %w", err)
	}

	var dumps []TableDump
	for _, name := range names {
		dump, err := s.inspectTable(ctx, name, limit)
		if err != nil {
			return nil, err
		}
		dumps = append(dumps, dump)
	}
	return dumps, nil
}

func (s *LibSQLStore) inspectTable(ctx context.Context, name string, limit int) (TableDump, error) {
	infoRows, err := s.db.QueryContext(ctx, fmt.Sprintf(`PRAGMA table_info(%s)`, quoteIdentifier(name)))
	if err != nil {
		return TableDump{}, fmt.Errorf("inspect %s columns: %w", name, err)
	}
	defer infoRows.Close()

	dump := TableDump{Name: name}
	for infoRows.Next() {
		var cid int
		var column string
		var columnType string
		var notNull int
		var defaultValue any
		var primaryKey int
		if err := infoRows.Scan(&cid, &column, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return TableDump{}, fmt.Errorf("scan %s columns: %w", name, err)
		}
		dump.Columns = append(dump.Columns, column)
	}
	if err := infoRows.Err(); err != nil {
		return TableDump{}, fmt.Errorf("iterate %s columns: %w", name, err)
	}

	query := fmt.Sprintf(`SELECT * FROM %s LIMIT %d`, quoteIdentifier(name), limit)
	dataRows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return TableDump{}, fmt.Errorf("read %s rows: %w", name, err)
	}
	defer dataRows.Close()

	columns, err := dataRows.Columns()
	if err != nil {
		return TableDump{}, fmt.Errorf("columns for %s: %w", name, err)
	}
	values := make([]any, len(columns))
	pointers := make([]any, len(columns))
	for i := range values {
		pointers[i] = &values[i]
	}

	for dataRows.Next() {
		if err := dataRows.Scan(pointers...); err != nil {
			return TableDump{}, fmt.Errorf("scan %s row: %w", name, err)
		}
		row := make(map[string]string, len(columns))
		for i, column := range columns {
			row[column] = stringifySQLiteValue(values[i])
		}
		dump.Rows = append(dump.Rows, row)
	}
	if err := dataRows.Err(); err != nil {
		return TableDump{}, fmt.Errorf("iterate %s rows: %w", name, err)
	}
	return dump, nil
}

func quoteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func stringifySQLiteValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return "NULL"
	case []byte:
		return string(typed)
	default:
		return fmt.Sprint(typed)
	}
}

func (s *LibSQLStore) SaveNode(ctx context.Context, node Node) error {
	embeddingJSON, err := json.Marshal(node.Embedding)
	if err != nil {
		return fmt.Errorf("marshal embedding: %w", err)
	}

	query := `INSERT INTO nodes (id, workspace, domain, type, name, content, url, path, embedding_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			workspace = excluded.workspace,
			domain = excluded.domain,
			type = excluded.type,
			name = excluded.name,
			content = excluded.content,
			url = excluded.url,
			path = excluded.path,
			embedding_json = excluded.embedding_json;`

	_, err = s.db.ExecContext(ctx, query, node.ID, node.Workspace, node.Domain, node.Type, node.Name, node.Content, node.URL, node.Path, string(embeddingJSON))
	return err
}

func (s *LibSQLStore) SaveEdge(ctx context.Context, edge Edge) error {
	query := `INSERT INTO edges (source_id, target_id, type) VALUES (?, ?, ?) ON CONFLICT DO NOTHING;`
	_, err := s.db.ExecContext(ctx, query, edge.SourceID, edge.TargetID, edge.Type)
	return err
}

func (s *LibSQLStore) VectorSearch(ctx context.Context, queryVector []float32, limit int) ([]Node, error) {
	return s.vectorSearch(ctx, "", queryVector, limit)
}

func (s *LibSQLStore) VectorSearchWorkspace(ctx context.Context, workspace string, queryVector []float32, limit int) ([]Node, error) {
	return s.vectorSearch(ctx, strings.TrimSpace(workspace), queryVector, limit)
}

func (s *LibSQLStore) vectorSearch(ctx context.Context, workspace string, queryVector []float32, limit int) ([]Node, error) {
	if limit <= 0 {
		limit = 5
	}
	if len(queryVector) == 0 {
		return nil, nil
	}

	query := `SELECT id, workspace, domain, type, name, content, COALESCE(url, ''), COALESCE(path, ''), embedding_json FROM nodes`
	var rows *sql.Rows
	var err error
	if workspace == "" {
		rows, err = s.db.QueryContext(ctx, query)
	} else {
		rows, err = s.db.QueryContext(ctx, query+` WHERE workspace = ?`, workspace)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type rankedNode struct {
		node  Node
		score float64
	}

	var ranked []rankedNode
	for rows.Next() {
		var n Node
		var embeddingJSON string
		if err := rows.Scan(&n.ID, &n.Workspace, &n.Domain, &n.Type, &n.Name, &n.Content, &n.URL, &n.Path, &embeddingJSON); err != nil {
			return nil, err
		}

		if err := json.Unmarshal([]byte(embeddingJSON), &n.Embedding); err != nil {
			continue
		}
		if len(n.Embedding) == 0 {
			continue
		}
		n.EmbeddingLength = len(n.Embedding)

		score := cosineSimilarity(queryVector, n.Embedding)
		if math.IsNaN(score) || score <= 0 {
			continue
		}
		ranked = append(ranked, rankedNode{node: n, score: score})
	}

	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].score == ranked[j].score {
			return ranked[i].node.ID < ranked[j].node.ID
		}
		return ranked[i].score > ranked[j].score
	})

	if len(ranked) > limit {
		ranked = ranked[:limit]
	}

	results := make([]Node, 0, len(ranked))
	for _, item := range ranked {
		item.node.Embedding = nil
		results = append(results, item.node)
	}
	return results, nil
}

func (s *LibSQLStore) KeywordSearch(ctx context.Context, query string, limit int) ([]Node, error) {
	return s.keywordSearch(ctx, "", query, limit)
}

func (s *LibSQLStore) KeywordSearchWorkspace(ctx context.Context, workspace string, query string, limit int) ([]Node, error) {
	return s.keywordSearch(ctx, strings.TrimSpace(workspace), query, limit)
}

func (s *LibSQLStore) keywordSearch(ctx context.Context, workspace string, query string, limit int) ([]Node, error) {
	if limit <= 0 {
		limit = 5
	}
	query = strings.TrimSpace(strings.ToLower(query))
	if query == "" {
		return nil, nil
	}

	sqlQuery := `SELECT id, workspace, domain, type, name, content, COALESCE(url, ''), COALESCE(path, ''), COALESCE(embedding_json, '[]') FROM nodes`
	var rows *sql.Rows
	var err error
	if workspace == "" {
		rows, err = s.db.QueryContext(ctx, sqlQuery)
	} else {
		rows, err = s.db.QueryContext(ctx, sqlQuery+` WHERE workspace = ?`, workspace)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	terms := strings.Fields(query)
	type rankedNode struct {
		node  Node
		score int
	}
	var ranked []rankedNode

	for rows.Next() {
		var n Node
		var embeddingJSON string
		if err := rows.Scan(&n.ID, &n.Workspace, &n.Domain, &n.Type, &n.Name, &n.Content, &n.URL, &n.Path, &embeddingJSON); err != nil {
			return nil, err
		}
		n.EmbeddingLength = embeddingLength(embeddingJSON)

		name := strings.ToLower(n.Name)
		content := strings.ToLower(n.Content)
		score := 0
		if strings.Contains(name, query) {
			score += 10
		}
		if strings.Contains(content, query) {
			score += 5
		}
		for _, term := range terms {
			if strings.Contains(name, term) {
				score += 3
			}
			if strings.Contains(content, term) {
				score += 1
			}
		}
		if score > 0 {
			ranked = append(ranked, rankedNode{node: n, score: score})
		}
	}

	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].score == ranked[j].score {
			return ranked[i].node.ID < ranked[j].node.ID
		}
		return ranked[i].score > ranked[j].score
	})

	if len(ranked) > limit {
		ranked = ranked[:limit]
	}

	results := make([]Node, 0, len(ranked))
	for _, item := range ranked {
		results = append(results, item.node)
	}
	return results, nil
}

func (s *LibSQLStore) GetNodeByID(ctx context.Context, id string) (Node, error) {
	var n Node
	var embeddingJSON string
	err := s.db.QueryRowContext(ctx, `SELECT id, workspace, domain, type, name, content, COALESCE(url, ''), COALESCE(path, ''), COALESCE(embedding_json, '[]') FROM nodes WHERE id = ?`, id).
		Scan(&n.ID, &n.Workspace, &n.Domain, &n.Type, &n.Name, &n.Content, &n.URL, &n.Path, &embeddingJSON)
	if err != nil {
		return Node{}, err
	}
	n.EmbeddingLength = embeddingLength(embeddingJSON)
	return n, nil
}

func (s *LibSQLStore) GetNeighbors(ctx context.Context, nodeID string) ([]Node, []Edge, error) {
	query := `SELECT n.id, n.workspace, n.domain, n.type, n.name, n.content, COALESCE(n.url, ''), COALESCE(n.path, ''), COALESCE(n.embedding_json, '[]'), e.source_id, e.target_id, e.type
		FROM edges e
		JOIN nodes n ON (n.id = e.target_id AND e.source_id = ?) OR (n.id = e.source_id AND e.target_id = ?)`

	rows, err := s.db.QueryContext(ctx, query, nodeID, nodeID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	nodes := make([]Node, 0)
	edges := make([]Edge, 0)
	seenNodes := map[string]struct{}{}
	seenEdges := map[string]struct{}{}

	for rows.Next() {
		var n Node
		var e Edge
		var embeddingJSON string
		if err := rows.Scan(&n.ID, &n.Workspace, &n.Domain, &n.Type, &n.Name, &n.Content, &n.URL, &n.Path, &embeddingJSON, &e.SourceID, &e.TargetID, &e.Type); err != nil {
			return nil, nil, err
		}
		n.EmbeddingLength = embeddingLength(embeddingJSON)
		if _, ok := seenNodes[n.ID]; !ok {
			seenNodes[n.ID] = struct{}{}
			nodes = append(nodes, n)
		}
		edgeKey := e.SourceID + "|" + e.TargetID + "|" + e.Type
		if _, ok := seenEdges[edgeKey]; !ok {
			seenEdges[edgeKey] = struct{}{}
			edges = append(edges, e)
		}
	}
	return nodes, edges, nil
}

func (s *LibSQLStore) GetAllGraphElements(ctx context.Context) ([]Node, []Edge, error) {
	nodeRows, err := s.db.QueryContext(ctx, `SELECT id, workspace, domain, type, name, content, COALESCE(url, ''), COALESCE(path, ''), COALESCE(embedding_json, '[]') FROM nodes ORDER BY domain, type, name`)
	if err != nil {
		return nil, nil, err
	}
	defer nodeRows.Close()

	var nodes []Node
	for nodeRows.Next() {
		var n Node
		var embeddingJSON string
		if err := nodeRows.Scan(&n.ID, &n.Workspace, &n.Domain, &n.Type, &n.Name, &n.Content, &n.URL, &n.Path, &embeddingJSON); err != nil {
			return nil, nil, err
		}
		n.EmbeddingLength = embeddingLength(embeddingJSON)
		nodes = append(nodes, n)
	}

	edgeRows, err := s.db.QueryContext(ctx, `SELECT source_id, target_id, type FROM edges ORDER BY source_id, target_id, type`)
	if err != nil {
		return nil, nil, err
	}
	defer edgeRows.Close()

	var edges []Edge
	for edgeRows.Next() {
		var e Edge
		if err := edgeRows.Scan(&e.SourceID, &e.TargetID, &e.Type); err != nil {
			return nil, nil, err
		}
		edges = append(edges, e)
	}
	return nodes, edges, nil
}

func (s *LibSQLStore) DeleteNodeByID(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `PRAGMA foreign_keys = ON`)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM edges WHERE source_id = ? OR target_id = ?`, id, id)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM nodes WHERE id = ?`, id)
	return err
}

func (s *LibSQLStore) DeleteFileNodes(ctx context.Context, workspace string, relativePath string) error {
	relativePath = filepath.ToSlash(relativePath)
	_, err := s.db.ExecContext(ctx, `DELETE FROM edges
		WHERE source_id IN (
			SELECT id FROM nodes WHERE workspace = ?1
			AND (url = ?2 OR (substr(url, 1, length(?2)) = ?2 AND substr(url, length(?2) + 1, 1) = '#'))
		)
		OR target_id IN (
			SELECT id FROM nodes WHERE workspace = ?1
			AND (url = ?2 OR (substr(url, 1, length(?2)) = ?2 AND substr(url, length(?2) + 1, 1) = '#'))
		)`, workspace, relativePath)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM nodes WHERE workspace = ?1
		AND (url = ?2 OR (substr(url, 1, length(?2)) = ?2 AND substr(url, length(?2) + 1, 1) = '#'))`,
		workspace, relativePath)
	return err
}

func (s *LibSQLStore) DeleteWorkspace(ctx context.Context, workspace string) error {
	_, err := s.db.ExecContext(ctx, `PRAGMA foreign_keys = ON`)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM edges WHERE source_id IN (SELECT id FROM nodes WHERE workspace = ?) OR target_id IN (SELECT id FROM nodes WHERE workspace = ?)`, workspace, workspace)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM nodes WHERE workspace = ?`, workspace)
	return err
}

func (s *LibSQLStore) ClearAll(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `PRAGMA foreign_keys = ON`)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM edges`)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM nodes`)
	return err
}

func embeddingLength(jsonStr string) int {
	if jsonStr == "" || jsonStr == "[]" {
		return 0
	}
	var v []float32
	if err := json.Unmarshal([]byte(jsonStr), &v); err != nil {
		return 0
	}
	return len(v)
}

func cosineSimilarity(a []float32, b []float32) float64 {
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
		return 0
	}

	var dot float64
	var normA float64
	var normB float64
	for i := range a {
		av := float64(a[i])
		bv := float64(b[i])
		dot += av * bv
		normA += av * av
		normB += bv * bv
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}
