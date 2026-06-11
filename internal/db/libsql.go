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

type MemoryRecord struct {
	Node              Node     `json:"node"`
	MemoryKey         string   `json:"memory_key"`
	ScopeType         string   `json:"scope_type"`
	ScopeID           string   `json:"scope_id"`
	LifecycleState    string   `json:"lifecycle_state"`
	KnowledgeType     string   `json:"knowledge_type"`
	Source            string   `json:"source"`
	WriterID          string   `json:"writer_id"`
	CreatedAt         string   `json:"created_at"`
	UpdatedAt         string   `json:"updated_at"`
	NormalizedTags    []string `json:"normalized_tags"`
	DisplayTags       []string `json:"display_tags"`
	Revision          int      `json:"revision"`
	ReplacedByNodeID  string   `json:"replaced_by_node_id,omitempty"`
	DeprecatedMessage string   `json:"deprecated_message,omitempty"`
}

type MemoryRevision struct {
	NodeID           string   `json:"node_id"`
	Revision         int      `json:"revision"`
	Title            string   `json:"title"`
	Content          string   `json:"content"`
	Source           string   `json:"source"`
	WriterID         string   `json:"writer_id"`
	LifecycleState   string   `json:"lifecycle_state"`
	NormalizedTags   []string `json:"normalized_tags"`
	DisplayTags      []string `json:"display_tags"`
	CreatedAt        string   `json:"created_at"`
	DeprecatedReason string   `json:"deprecated_reason,omitempty"`
}

type MemorySearchFilter struct {
	Query           string
	ScopeType       string
	ScopeID         string
	KnowledgeType   string
	LifecycleStates []string
	Limit           int
}

type WebCorpus struct {
	ID        string `json:"id"`
	ScopeType string `json:"scope_type"`
	ScopeID   string `json:"scope_id"`
	Source    string `json:"source"`
	BaseURL   string `json:"base_url"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type WebCrawlVersion struct {
	ID        string `json:"id"`
	CorpusID  string `json:"corpus_id"`
	SeedURL   string `json:"seed_url"`
	CreatedAt string `json:"created_at"`
}

type StudioNodeDetails struct {
	Node
	Memory          *MemoryRecord    `json:"memory,omitempty"`
	WebCorpus       *WebCorpus       `json:"web_corpus,omitempty"`
	WebCrawlVersion *WebCrawlVersion `json:"web_crawl_version,omitempty"`
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
	UpsertMemoryRecord(ctx context.Context, record MemoryRecord) error
	GetMemoryRecord(ctx context.Context, nodeID string) (MemoryRecord, error)
	GetMemoryRecordByKey(ctx context.Context, scopeType string, scopeID string, knowledgeType string, memoryKey string) (MemoryRecord, error)
	InsertMemoryRevision(ctx context.Context, revision MemoryRevision) error
	ListMemoryRevisions(ctx context.Context, nodeID string) ([]MemoryRevision, error)
	SearchMemoryRecords(ctx context.Context, filter MemorySearchFilter) ([]MemoryRecord, error)
	SetMemoryLifecycle(ctx context.Context, nodeID string, lifecycleState string, replacedByNodeID string, deprecatedMessage string) error
	SaveWebCorpus(ctx context.Context, corpus WebCorpus) error
	SaveWebCrawlVersion(ctx context.Context, version WebCrawlVersion) error
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
		`CREATE TABLE IF NOT EXISTS memory_records (
			node_id TEXT PRIMARY KEY,
			scope_type TEXT NOT NULL,
			scope_id TEXT NOT NULL,
			lifecycle_state TEXT NOT NULL,
			knowledge_type TEXT NOT NULL,
			source TEXT NOT NULL,
			writer_id TEXT NOT NULL,
			memory_key TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			normalized_tags_json TEXT NOT NULL DEFAULT '[]',
			display_tags_json TEXT NOT NULL DEFAULT '[]',
			revision INTEGER NOT NULL DEFAULT 1,
			replaced_by_node_id TEXT NOT NULL DEFAULT '',
			deprecated_message TEXT NOT NULL DEFAULT '',
			FOREIGN KEY (node_id) REFERENCES nodes(id) ON DELETE CASCADE
		);`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_memory_records_natural_key
			ON memory_records (scope_type, scope_id, knowledge_type, memory_key);`,
		`CREATE INDEX IF NOT EXISTS idx_memory_records_scope
			ON memory_records (scope_type, scope_id, lifecycle_state, knowledge_type);`,
		`CREATE TABLE IF NOT EXISTS memory_revisions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			node_id TEXT NOT NULL,
			revision INTEGER NOT NULL,
			title TEXT NOT NULL,
			content TEXT NOT NULL,
			source TEXT NOT NULL,
			writer_id TEXT NOT NULL,
			lifecycle_state TEXT NOT NULL,
			normalized_tags_json TEXT NOT NULL DEFAULT '[]',
			display_tags_json TEXT NOT NULL DEFAULT '[]',
			created_at TEXT NOT NULL,
			deprecated_reason TEXT NOT NULL DEFAULT '',
			FOREIGN KEY (node_id) REFERENCES nodes(id) ON DELETE CASCADE
		);`,
		`CREATE INDEX IF NOT EXISTS idx_memory_revisions_node_id ON memory_revisions (node_id, revision DESC);`,
		`CREATE TABLE IF NOT EXISTS web_corpora (
			id TEXT PRIMARY KEY,
			scope_type TEXT NOT NULL,
			scope_id TEXT NOT NULL,
			source TEXT NOT NULL,
			base_url TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_web_corpora_scope ON web_corpora (scope_type, scope_id);`,
		`CREATE TABLE IF NOT EXISTS web_crawl_versions (
			id TEXT PRIMARY KEY,
			corpus_id TEXT NOT NULL,
			seed_url TEXT NOT NULL,
			created_at TEXT NOT NULL,
			FOREIGN KEY (corpus_id) REFERENCES web_corpora(id) ON DELETE CASCADE
		);`,
		`CREATE INDEX IF NOT EXISTS idx_web_crawl_versions_corpus ON web_crawl_versions (corpus_id, created_at DESC);`,
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

func (s *LibSQLStore) GetStudioNodeDetails(ctx context.Context, id string) (StudioNodeDetails, error) {
	node, err := s.GetNodeByID(ctx, id)
	if err != nil {
		return StudioNodeDetails{}, err
	}

	details := StudioNodeDetails{Node: node}

	record, err := s.GetMemoryRecord(ctx, id)
	if err == nil {
		details.Memory = &record
	} else if err != sql.ErrNoRows {
		return StudioNodeDetails{}, err
	}

	corpus, err := s.findWebCorpusForNode(ctx, node)
	if err == nil {
		details.WebCorpus = &corpus
		version, versionErr := s.findLatestWebCrawlVersion(ctx, corpus.ID)
		if versionErr == nil {
			details.WebCrawlVersion = &version
		} else if versionErr != sql.ErrNoRows {
			return StudioNodeDetails{}, versionErr
		}
	} else if err != sql.ErrNoRows {
		return StudioNodeDetails{}, err
	}

	return details, nil
}

func (s *LibSQLStore) findWebCorpusForNode(ctx context.Context, node Node) (WebCorpus, error) {
	rawURL := strings.TrimSpace(node.URL)
	if rawURL == "" {
		return WebCorpus{}, sql.ErrNoRows
	}

	query := `SELECT id, scope_type, scope_id, source, base_url, created_at, updated_at
		FROM web_corpora
		WHERE ? = base_url OR ? LIKE base_url || '/%' OR ? LIKE base_url || '#%'
		ORDER BY LENGTH(base_url) DESC
		LIMIT 1`

	var corpus WebCorpus
	err := s.db.QueryRowContext(ctx, query, rawURL, rawURL, rawURL).Scan(
		&corpus.ID, &corpus.ScopeType, &corpus.ScopeID, &corpus.Source, &corpus.BaseURL, &corpus.CreatedAt, &corpus.UpdatedAt,
	)
	if err != nil {
		return WebCorpus{}, err
	}
	return corpus, nil
}

func (s *LibSQLStore) findLatestWebCrawlVersion(ctx context.Context, corpusID string) (WebCrawlVersion, error) {
	var version WebCrawlVersion
	err := s.db.QueryRowContext(ctx, `SELECT id, corpus_id, seed_url, created_at
		FROM web_crawl_versions
		WHERE corpus_id = ?
		ORDER BY created_at DESC, id DESC
		LIMIT 1`, corpusID).Scan(&version.ID, &version.CorpusID, &version.SeedURL, &version.CreatedAt)
	if err != nil {
		return WebCrawlVersion{}, err
	}
	return version, nil
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

func (s *LibSQLStore) UpsertMemoryRecord(ctx context.Context, record MemoryRecord) error {
	normalizedTagsJSON, err := json.Marshal(record.NormalizedTags)
	if err != nil {
		return fmt.Errorf("marshal normalized tags: %w", err)
	}
	displayTagsJSON, err := json.Marshal(record.DisplayTags)
	if err != nil {
		return fmt.Errorf("marshal display tags: %w", err)
	}

	query := `INSERT INTO memory_records (
		node_id, scope_type, scope_id, lifecycle_state, knowledge_type, source, writer_id, memory_key,
		created_at, updated_at, normalized_tags_json, display_tags_json, revision, replaced_by_node_id, deprecated_message
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(node_id) DO UPDATE SET
		scope_type = excluded.scope_type,
		scope_id = excluded.scope_id,
		lifecycle_state = excluded.lifecycle_state,
		knowledge_type = excluded.knowledge_type,
		source = excluded.source,
		writer_id = excluded.writer_id,
		memory_key = excluded.memory_key,
		created_at = excluded.created_at,
		updated_at = excluded.updated_at,
		normalized_tags_json = excluded.normalized_tags_json,
		display_tags_json = excluded.display_tags_json,
		revision = excluded.revision,
		replaced_by_node_id = excluded.replaced_by_node_id,
		deprecated_message = excluded.deprecated_message`
	_, err = s.db.ExecContext(ctx, query,
		record.Node.ID,
		record.ScopeType,
		record.ScopeID,
		record.LifecycleState,
		record.KnowledgeType,
		record.Source,
		record.WriterID,
		record.MemoryKey,
		record.CreatedAt,
		record.UpdatedAt,
		string(normalizedTagsJSON),
		string(displayTagsJSON),
		record.Revision,
		record.ReplacedByNodeID,
		record.DeprecatedMessage,
	)
	return err
}

func (s *LibSQLStore) GetMemoryRecord(ctx context.Context, nodeID string) (MemoryRecord, error) {
	return s.getMemoryRecord(ctx, `mr.node_id = ?`, nodeID)
}

func (s *LibSQLStore) GetMemoryRecordByKey(ctx context.Context, scopeType string, scopeID string, knowledgeType string, memoryKey string) (MemoryRecord, error) {
	return s.getMemoryRecord(ctx, `mr.scope_type = ? AND mr.scope_id = ? AND mr.knowledge_type = ? AND mr.memory_key = ?`, scopeType, scopeID, knowledgeType, memoryKey)
}

func (s *LibSQLStore) getMemoryRecord(ctx context.Context, where string, args ...any) (MemoryRecord, error) {
	query := `SELECT
		n.id, n.workspace, n.domain, n.type, n.name, n.content, COALESCE(n.url, ''), COALESCE(n.path, ''), COALESCE(n.embedding_json, '[]'),
		mr.memory_key, mr.scope_type, mr.scope_id, mr.lifecycle_state, mr.knowledge_type, mr.source, mr.writer_id,
		mr.created_at, mr.updated_at, mr.normalized_tags_json, mr.display_tags_json, mr.revision,
		COALESCE(mr.replaced_by_node_id, ''), COALESCE(mr.deprecated_message, '')
		FROM memory_records mr
		JOIN nodes n ON n.id = mr.node_id
		WHERE ` + where + ` LIMIT 1`

	var record MemoryRecord
	var normalizedTagsJSON string
	var displayTagsJSON string
	var embeddingJSON string
	err := s.db.QueryRowContext(ctx, query, args...).Scan(
		&record.Node.ID, &record.Node.Workspace, &record.Node.Domain, &record.Node.Type, &record.Node.Name, &record.Node.Content,
		&record.Node.URL, &record.Node.Path, &embeddingJSON,
		&record.MemoryKey, &record.ScopeType, &record.ScopeID, &record.LifecycleState, &record.KnowledgeType, &record.Source, &record.WriterID,
		&record.CreatedAt, &record.UpdatedAt, &normalizedTagsJSON, &displayTagsJSON, &record.Revision,
		&record.ReplacedByNodeID, &record.DeprecatedMessage,
	)
	if err != nil {
		return MemoryRecord{}, err
	}
	record.Node.EmbeddingLength = embeddingLength(embeddingJSON)
	record.NormalizedTags = jsonStringSlice(normalizedTagsJSON)
	record.DisplayTags = jsonStringSlice(displayTagsJSON)
	return record, nil
}

func (s *LibSQLStore) InsertMemoryRevision(ctx context.Context, revision MemoryRevision) error {
	normalizedTagsJSON, err := json.Marshal(revision.NormalizedTags)
	if err != nil {
		return fmt.Errorf("marshal revision normalized tags: %w", err)
	}
	displayTagsJSON, err := json.Marshal(revision.DisplayTags)
	if err != nil {
		return fmt.Errorf("marshal revision display tags: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO memory_revisions (
		node_id, revision, title, content, source, writer_id, lifecycle_state,
		normalized_tags_json, display_tags_json, created_at, deprecated_reason
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		revision.NodeID, revision.Revision, revision.Title, revision.Content, revision.Source,
		revision.WriterID, revision.LifecycleState, string(normalizedTagsJSON), string(displayTagsJSON),
		revision.CreatedAt, revision.DeprecatedReason,
	)
	return err
}

func (s *LibSQLStore) ListMemoryRevisions(ctx context.Context, nodeID string) ([]MemoryRevision, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT node_id, revision, title, content, source, writer_id, lifecycle_state,
		normalized_tags_json, display_tags_json, created_at, deprecated_reason
		FROM memory_revisions WHERE node_id = ? ORDER BY revision DESC, id DESC`, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var revisions []MemoryRevision
	for rows.Next() {
		var revision MemoryRevision
		var normalizedTagsJSON string
		var displayTagsJSON string
		if err := rows.Scan(
			&revision.NodeID, &revision.Revision, &revision.Title, &revision.Content, &revision.Source, &revision.WriterID,
			&revision.LifecycleState, &normalizedTagsJSON, &displayTagsJSON, &revision.CreatedAt, &revision.DeprecatedReason,
		); err != nil {
			return nil, err
		}
		revision.NormalizedTags = jsonStringSlice(normalizedTagsJSON)
		revision.DisplayTags = jsonStringSlice(displayTagsJSON)
		revisions = append(revisions, revision)
	}
	return revisions, rows.Err()
}

func (s *LibSQLStore) SearchMemoryRecords(ctx context.Context, filter MemorySearchFilter) ([]MemoryRecord, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 10
	}
	query := `SELECT
		n.id, n.workspace, n.domain, n.type, n.name, n.content, COALESCE(n.url, ''), COALESCE(n.path, ''), COALESCE(n.embedding_json, '[]'),
		mr.memory_key, mr.scope_type, mr.scope_id, mr.lifecycle_state, mr.knowledge_type, mr.source, mr.writer_id,
		mr.created_at, mr.updated_at, mr.normalized_tags_json, mr.display_tags_json, mr.revision,
		COALESCE(mr.replaced_by_node_id, ''), COALESCE(mr.deprecated_message, '')
		FROM memory_records mr
		JOIN nodes n ON n.id = mr.node_id`

	var where []string
	var args []any
	if q := strings.TrimSpace(filter.ScopeType); q != "" {
		where = append(where, `mr.scope_type = ?`)
		args = append(args, q)
	}
	if q := strings.TrimSpace(filter.ScopeID); q != "" {
		where = append(where, `mr.scope_id = ?`)
		args = append(args, q)
	}
	if q := strings.TrimSpace(filter.KnowledgeType); q != "" {
		where = append(where, `mr.knowledge_type = ?`)
		args = append(args, q)
	}
	if len(filter.LifecycleStates) > 0 {
		placeholders := make([]string, 0, len(filter.LifecycleStates))
		for _, state := range filter.LifecycleStates {
			state = strings.TrimSpace(state)
			if state == "" {
				continue
			}
			placeholders = append(placeholders, "?")
			args = append(args, state)
		}
		if len(placeholders) > 0 {
			where = append(where, `mr.lifecycle_state IN (`+strings.Join(placeholders, ", ")+`)`)
		}
	}
	if q := strings.TrimSpace(strings.ToLower(filter.Query)); q != "" {
		where = append(where, `(LOWER(n.name) LIKE ? OR LOWER(n.content) LIKE ? OR LOWER(mr.display_tags_json) LIKE ? OR LOWER(mr.normalized_tags_json) LIKE ?)`)
		like := "%" + q + "%"
		args = append(args, like, like, like, like)
	}
	if len(where) > 0 {
		query += ` WHERE ` + strings.Join(where, ` AND `)
	}
	query += ` ORDER BY mr.updated_at DESC, n.id ASC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []MemoryRecord
	for rows.Next() {
		var record MemoryRecord
		var normalizedTagsJSON string
		var displayTagsJSON string
		var embeddingJSON string
		if err := rows.Scan(
			&record.Node.ID, &record.Node.Workspace, &record.Node.Domain, &record.Node.Type, &record.Node.Name, &record.Node.Content,
			&record.Node.URL, &record.Node.Path, &embeddingJSON,
			&record.MemoryKey, &record.ScopeType, &record.ScopeID, &record.LifecycleState, &record.KnowledgeType, &record.Source, &record.WriterID,
			&record.CreatedAt, &record.UpdatedAt, &normalizedTagsJSON, &displayTagsJSON, &record.Revision,
			&record.ReplacedByNodeID, &record.DeprecatedMessage,
		); err != nil {
			return nil, err
		}
		record.Node.EmbeddingLength = embeddingLength(embeddingJSON)
		record.NormalizedTags = jsonStringSlice(normalizedTagsJSON)
		record.DisplayTags = jsonStringSlice(displayTagsJSON)
		records = append(records, record)
	}
	return records, rows.Err()
}

func (s *LibSQLStore) SetMemoryLifecycle(ctx context.Context, nodeID string, lifecycleState string, replacedByNodeID string, deprecatedMessage string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE memory_records
		SET lifecycle_state = ?, replaced_by_node_id = ?, deprecated_message = ?, updated_at = CURRENT_TIMESTAMP
		WHERE node_id = ?`, lifecycleState, replacedByNodeID, deprecatedMessage, nodeID)
	return err
}

func (s *LibSQLStore) SaveWebCorpus(ctx context.Context, corpus WebCorpus) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO web_corpora
		(id, scope_type, scope_id, source, base_url, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			scope_type = excluded.scope_type,
			scope_id = excluded.scope_id,
			source = excluded.source,
			base_url = excluded.base_url,
			created_at = excluded.created_at,
			updated_at = excluded.updated_at`,
		corpus.ID, corpus.ScopeType, corpus.ScopeID, corpus.Source, corpus.BaseURL, corpus.CreatedAt, corpus.UpdatedAt,
	)
	return err
}

func (s *LibSQLStore) SaveWebCrawlVersion(ctx context.Context, version WebCrawlVersion) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO web_crawl_versions
		(id, corpus_id, seed_url, created_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			corpus_id = excluded.corpus_id,
			seed_url = excluded.seed_url,
			created_at = excluded.created_at`,
		version.ID, version.CorpusID, version.SeedURL, version.CreatedAt,
	)
	return err
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
	if err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM memory_revisions`); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM memory_records`); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM web_crawl_versions`); err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM web_corpora`)
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

func jsonStringSlice(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(value), &out); err != nil {
		return nil
	}
	return out
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
