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
	"time"

	"raph/internal/config"
	"raph/internal/verbose"

	_ "modernc.org/sqlite"
)

type Node struct {
	ID              string            `json:"id"`
	Workspace       string            `json:"-"`
	Domain          string            `json:"domain"`
	Type            string            `json:"type"`
	Name            string            `json:"name"`
	Content         string            `json:"content"`
	URL             string            `json:"url,omitempty"`
	Path            string            `json:"path,omitempty"`
	Properties      map[string]string `json:"properties,omitempty"`
	CreatedAt       string            `json:"created_at,omitempty"`
	UpdatedAt       string            `json:"updated_at,omitempty"`
	Embedding       []float32         `json:"-"`
	EmbeddingLength int               `json:"embedding_length,omitempty"`
}

// Prop returns a node property value (empty if unset).
func (n Node) Prop(key string) string {
	if n.Properties == nil {
		return ""
	}
	return n.Properties[key]
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
	LexicalSearch(ctx context.Context, workspace string, query string, limit int) ([]Node, error)
	ListNodes(ctx context.Context, filter NodeFilter) ([]Node, error)
	SetNodeProperties(ctx context.Context, id string, props map[string]string) error
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
	verbose.Printf("opening database file=%s", dbFile)
	// Encode the per-connection PRAGMAs in the DSN so EVERY connection the pool
	// hands out has them — not just the one that happened to run migrate(). If
	// database/sql discards and reopens a connection, a DSN-less open would come
	// back with busy_timeout=0 (instant SQLITE_BUSY under concurrency) and
	// foreign_keys=OFF (cascade deletes silently stop firing).
	dsn := dbFile + "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open embedded database: %w", err)
	}

	db.SetMaxOpenConns(1)
	store := &LibSQLStore{db: db}
	verbose.Printf("running database migrations...")
	if err := store.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	verbose.Printf("database migrations complete")
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
		// Access events power the studio analytics view: what nodes agents and
		// users read, and what they searched for.
		`CREATE TABLE IF NOT EXISTS access_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			node_id TEXT NOT NULL DEFAULT '',
			kind TEXT NOT NULL,
			query TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_access_events_node ON access_events (node_id);`,
		`CREATE INDEX IF NOT EXISTS idx_access_events_created ON access_events (created_at DESC);`,
		// Trigram FTS5 index over searchable node text. Serves both ranked keyword
		// (bm25) and literal substring (rg-like) lookups without scanning every row.
		`CREATE VIRTUAL TABLE IF NOT EXISTS nodes_fts USING fts5(
			node_id UNINDEXED,
			workspace UNINDEXED,
			domain UNINDEXED,
			type UNINDEXED,
			name,
			content,
			path UNINDEXED,
			tokenize = 'trigram'
		);`,
	}

	for _, q := range queries {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("migration execution failure: %w", err)
		}
	}
	if err := s.ensureNodeColumns(); err != nil {
		return err
	}
	if err := s.backfillNodesFTS(); err != nil {
		return err
	}
	return nil
}

// ensureNodeColumns adds columns introduced after the original schema. SQLite
// ALTER TABLE ADD COLUMN is cheap and idempotent here because we inspect first.
func (s *LibSQLStore) ensureNodeColumns() error {
	existing := map[string]bool{}
	rows, err := s.db.Query(`PRAGMA table_info(nodes)`)
	if err != nil {
		return fmt.Errorf("inspect nodes schema: %w", err)
	}
	for rows.Next() {
		var cid int
		var name string
		var columnType string
		var notNull int
		var defaultValue any
		var primaryKey int
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan nodes schema: %w", err)
		}
		existing[name] = true
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("inspect nodes schema: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close nodes schema rows: %w", err)
	}

	additions := []struct {
		name string
		ddl  string
	}{
		{"path", `ALTER TABLE nodes ADD COLUMN path TEXT NOT NULL DEFAULT ''`},
		{"properties_json", `ALTER TABLE nodes ADD COLUMN properties_json TEXT NOT NULL DEFAULT '{}'`},
		{"created_at", `ALTER TABLE nodes ADD COLUMN created_at TEXT NOT NULL DEFAULT ''`},
		{"updated_at", `ALTER TABLE nodes ADD COLUMN updated_at TEXT NOT NULL DEFAULT ''`},
	}
	for _, add := range additions {
		if existing[add.name] {
			continue
		}
		if _, err := s.db.Exec(add.ddl); err != nil {
			return fmt.Errorf("add nodes column %s: %w", add.name, err)
		}
	}
	return nil
}

// backfillNodesFTS seeds the FTS index from existing rows the first time the
// virtual table is created on a pre-existing database.
func (s *LibSQLStore) backfillNodesFTS() error {
	var count int
	if err := s.db.QueryRow(`SELECT count(*) FROM nodes_fts`).Scan(&count); err != nil {
		return fmt.Errorf("count nodes_fts: %w", err)
	}
	if count > 0 {
		return nil
	}
	if _, err := s.db.Exec(`INSERT INTO nodes_fts (node_id, workspace, domain, type, name, content, path)
		SELECT id, workspace, domain, type, name, content, COALESCE(path, '') FROM nodes`); err != nil {
		return fmt.Errorf("backfill nodes_fts: %w", err)
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
	propertiesJSON, err := marshalProperties(node.Properties)
	if err != nil {
		return fmt.Errorf("marshal properties: %w", err)
	}
	now := node.UpdatedAt
	if strings.TrimSpace(now) == "" {
		now = nowTimestamp()
	}
	createdAt := node.CreatedAt
	if strings.TrimSpace(createdAt) == "" {
		createdAt = now
	}

	query := `INSERT INTO nodes (id, workspace, domain, type, name, content, url, path, properties_json, created_at, updated_at, embedding_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			workspace = excluded.workspace,
			domain = excluded.domain,
			type = excluded.type,
			name = excluded.name,
			content = excluded.content,
			url = excluded.url,
			path = excluded.path,
			properties_json = excluded.properties_json,
			created_at = CASE WHEN nodes.created_at = '' THEN excluded.created_at ELSE nodes.created_at END,
			updated_at = excluded.updated_at,
			embedding_json = excluded.embedding_json;`

	if _, err = s.db.ExecContext(ctx, query, node.ID, node.Workspace, node.Domain, node.Type, node.Name, node.Content, node.URL, node.Path, propertiesJSON, createdAt, now, string(embeddingJSON)); err != nil {
		return err
	}
	return s.syncNodeFTS(ctx, node)
}

// syncNodeFTS keeps the trigram FTS index in lockstep with a node row.
func (s *LibSQLStore) syncNodeFTS(ctx context.Context, node Node) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM nodes_fts WHERE node_id = ?`, node.ID); err != nil {
		return fmt.Errorf("clear fts row: %w", err)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO nodes_fts (node_id, workspace, domain, type, name, content, path)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		node.ID, node.Workspace, node.Domain, node.Type, node.Name, node.Content, node.Path)
	if err != nil {
		return fmt.Errorf("index fts row: %w", err)
	}
	return nil
}

func (s *LibSQLStore) SaveEdge(ctx context.Context, edge Edge) error {
	query := `INSERT INTO edges (source_id, target_id, type) VALUES (?, ?, ?) ON CONFLICT DO NOTHING;`
	_, err := s.db.ExecContext(ctx, query, edge.SourceID, edge.TargetID, edge.Type)
	return err
}

// SaveEdges persists many edges in a single transaction. Because the store runs
// on one connection in WAL mode, the per-edge autocommit fsync dominates the
// cost of edge-heavy index passes; batching collapses thousands of commits into
// one. It is intentionally NOT part of the GraphStore interface — callers detect
// it via an optional-capability type assertion and fall back to SaveEdge.
func (s *LibSQLStore) SaveEdges(ctx context.Context, edges []Edge) error {
	if len(edges) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO edges (source_id, target_id, type) VALUES (?, ?, ?) ON CONFLICT DO NOTHING;`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer stmt.Close()
	for _, edge := range edges {
		if _, err := stmt.ExecContext(ctx, edge.SourceID, edge.TargetID, edge.Type); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
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

	// Only scan rows that actually carry an embedding — skipping the (often
	// numerous) embedding-less code nodes at the SQL layer avoids loading their
	// content and attempting to decode an empty vector for every query.
	query := `SELECT id, workspace, domain, type, name, content, COALESCE(url, ''), COALESCE(path, ''), embedding_json FROM nodes WHERE embedding_json IS NOT NULL AND embedding_json <> '' AND embedding_json <> '[]'`
	var rows *sql.Rows
	var err error
	if workspace == "" {
		rows, err = s.db.QueryContext(ctx, query)
	} else {
		rows, err = s.db.QueryContext(ctx, query+` AND workspace = ?`, workspace)
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
		n.Embedding = nil // scored — drop the vector so it doesn't sit in memory
		ranked = append(ranked, rankedNode{node: n, score: score})
	}
	if err := rows.Err(); err != nil {
		return nil, err
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

const nodeColumns = `id, workspace, domain, type, name, content, COALESCE(url, ''), COALESCE(path, ''), COALESCE(properties_json, '{}'), COALESCE(created_at, ''), COALESCE(updated_at, ''), COALESCE(embedding_json, '[]')`

// scanNode reads a full node row (including properties and timestamps) selected
// with the nodeColumns column list.
func scanNode(rows interface{ Scan(...any) error }) (Node, error) {
	var n Node
	var propertiesJSON string
	var embeddingJSON string
	if err := rows.Scan(&n.ID, &n.Workspace, &n.Domain, &n.Type, &n.Name, &n.Content, &n.URL, &n.Path, &propertiesJSON, &n.CreatedAt, &n.UpdatedAt, &embeddingJSON); err != nil {
		return Node{}, err
	}
	n.Properties = unmarshalProperties(propertiesJSON)
	n.EmbeddingLength = embeddingLength(embeddingJSON)
	return n, nil
}

func (s *LibSQLStore) keywordSearch(ctx context.Context, workspace string, query string, limit int) ([]Node, error) {
	if limit <= 0 {
		limit = 5
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}

	// Primary path: trigram FTS with bm25 ranking. AND of query terms.
	if match := buildFTSMatch(query); match != "" {
		nodes, err := s.ftsSearch(ctx, workspace, match, limit)
		if err != nil {
			return nil, err
		}
		if len(nodes) > 0 {
			return nodes, nil
		}
	}
	// Fallback for very short queries (trigram needs >=3 chars) or empty FTS.
	return s.likeSearch(ctx, workspace, query, limit)
}

// LexicalSearch performs a literal substring match (rg-style) over indexed node
// text using the trigram index, ranked by bm25.
func (s *LibSQLStore) LexicalSearch(ctx context.Context, workspace string, query string, limit int) ([]Node, error) {
	if limit <= 0 {
		limit = 10
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	if len([]rune(query)) >= 3 {
		nodes, err := s.ftsSearch(ctx, strings.TrimSpace(workspace), `"`+escapeFTS(query)+`"`, limit)
		if err != nil {
			return nil, err
		}
		if len(nodes) > 0 {
			return nodes, nil
		}
	}
	return s.likeSearch(ctx, strings.TrimSpace(workspace), query, limit)
}

func (s *LibSQLStore) ftsSearch(ctx context.Context, workspace string, match string, limit int) ([]Node, error) {
	query := `SELECT n.id, n.workspace, n.domain, n.type, n.name, n.content, COALESCE(n.url, ''), COALESCE(n.path, ''), COALESCE(n.properties_json, '{}'), COALESCE(n.created_at, ''), COALESCE(n.updated_at, ''), COALESCE(n.embedding_json, '[]'), bm25(nodes_fts) AS score
		FROM nodes_fts JOIN nodes n ON n.id = nodes_fts.node_id
		WHERE nodes_fts MATCH ?`
	args := []any{match}
	if workspace != "" {
		query += ` AND nodes_fts.workspace = ?`
		args = append(args, workspace)
	}
	query += ` ORDER BY score ASC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []Node
	for rows.Next() {
		var n Node
		var propertiesJSON string
		var embeddingJSON string
		var score float64
		if err := rows.Scan(&n.ID, &n.Workspace, &n.Domain, &n.Type, &n.Name, &n.Content, &n.URL, &n.Path, &propertiesJSON, &n.CreatedAt, &n.UpdatedAt, &embeddingJSON, &score); err != nil {
			return nil, err
		}
		n.Properties = unmarshalProperties(propertiesJSON)
		n.EmbeddingLength = embeddingLength(embeddingJSON)
		results = append(results, n)
	}
	return results, rows.Err()
}

// likeSearch is the substring fallback used when the trigram index cannot serve
// a query (e.g. patterns shorter than three characters).
func (s *LibSQLStore) likeSearch(ctx context.Context, workspace string, query string, limit int) ([]Node, error) {
	like := "%" + strings.ToLower(query) + "%"
	sqlQuery := `SELECT ` + nodeColumns + ` FROM nodes WHERE (LOWER(name) LIKE ? OR LOWER(content) LIKE ?)`
	args := []any{like, like}
	if workspace != "" {
		sqlQuery += ` AND workspace = ?`
		args = append(args, workspace)
	}
	sqlQuery += ` ORDER BY updated_at DESC, id ASC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []Node
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		results = append(results, n)
	}
	return results, rows.Err()
}

// buildFTSMatch turns a free-text query into a trigram FTS5 MATCH expression by
// AND-ing each term of length >= 3. Returns "" when no term qualifies.
func buildFTSMatch(query string) string {
	terms := strings.Fields(query)
	parts := make([]string, 0, len(terms))
	for _, term := range terms {
		if len([]rune(term)) < 3 {
			continue
		}
		parts = append(parts, `"`+escapeFTS(term)+`"`)
	}
	return strings.Join(parts, " ")
}

func escapeFTS(value string) string {
	return strings.ReplaceAll(value, `"`, `""`)
}

// NodeFilter selects nodes structurally (without text search) for listings such
// as docs, rules, and handoffs.
type NodeFilter struct {
	Workspace      string
	Domain         string
	Types          []string
	PropertyEquals map[string]string
	Query          string
	Limit          int
	// Lean selects only id/type/name/url and leaves content, properties, and
	// embeddings empty. Use it for large listings (e.g. the indexer's symbol
	// index) that never touch the heavy columns, to avoid pulling every node's
	// content and embedding JSON into memory.
	Lean bool
}

func (s *LibSQLStore) ListNodes(ctx context.Context, filter NodeFilter) ([]Node, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	selectCols := nodeColumns
	if filter.Lean {
		selectCols = `id, type, name, COALESCE(url, '')`
	}
	sqlQuery := `SELECT ` + selectCols + ` FROM nodes`
	var where []string
	var args []any
	if ws := strings.TrimSpace(filter.Workspace); ws != "" {
		where = append(where, `workspace = ?`)
		args = append(args, ws)
	}
	if d := strings.TrimSpace(filter.Domain); d != "" {
		where = append(where, `domain = ?`)
		args = append(args, d)
	}
	if len(filter.Types) > 0 {
		placeholders := make([]string, 0, len(filter.Types))
		for _, t := range filter.Types {
			t = strings.TrimSpace(t)
			if t == "" {
				continue
			}
			placeholders = append(placeholders, "?")
			args = append(args, t)
		}
		if len(placeholders) > 0 {
			where = append(where, `type IN (`+strings.Join(placeholders, ", ")+`)`)
		}
	}
	for key, value := range filter.PropertyEquals {
		where = append(where, `json_extract(properties_json, '$.'||?) = ?`)
		args = append(args, key, value)
	}
	if q := strings.TrimSpace(strings.ToLower(filter.Query)); q != "" {
		where = append(where, `(LOWER(name) LIKE ? OR LOWER(content) LIKE ?)`)
		like := "%" + q + "%"
		args = append(args, like, like)
	}
	if len(where) > 0 {
		sqlQuery += ` WHERE ` + strings.Join(where, ` AND `)
	}
	sqlQuery += ` ORDER BY updated_at DESC, id ASC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []Node
	for rows.Next() {
		if filter.Lean {
			var n Node
			if err := rows.Scan(&n.ID, &n.Type, &n.Name, &n.URL); err != nil {
				return nil, err
			}
			results = append(results, n)
			continue
		}
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		results = append(results, n)
	}
	return results, rows.Err()
}

// SetNodeProperties merges properties into an existing node and bumps its
// updated_at timestamp. Existing keys not present in props are preserved.
func (s *LibSQLStore) SetNodeProperties(ctx context.Context, id string, props map[string]string) error {
	var current string
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(properties_json, '{}') FROM nodes WHERE id = ?`, id).Scan(&current)
	if err != nil {
		return err
	}
	merged := unmarshalProperties(current)
	if merged == nil {
		merged = map[string]string{}
	}
	for k, v := range props {
		merged[k] = v
	}
	encoded, err := marshalProperties(merged)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE nodes SET properties_json = ?, updated_at = ? WHERE id = ?`, encoded, nowTimestamp(), id)
	return err
}

func (s *LibSQLStore) GetNodeByID(ctx context.Context, id string) (Node, error) {
	n, err := scanNode(s.db.QueryRowContext(ctx, `SELECT `+nodeColumns+` FROM nodes WHERE id = ?`, id))
	if err != nil {
		return Node{}, err
	}
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
	return nodes, edges, rows.Err()
}

// GraphElementsLean returns the whole graph without loading embeddings, and
// with node content capped to contentLimit bytes in SQL (0 = no content). It
// exists so the studio graph/stats views don't pull every node's full content
// and parse every embedding JSON just to render a summary. Not on the
// GraphStore interface — callers detect it via a type assertion and fall back
// to GetAllGraphElements.
func (s *LibSQLStore) GraphElementsLean(ctx context.Context, contentLimit int) ([]Node, []Edge, error) {
	if contentLimit < 0 {
		contentLimit = 0
	}
	nodeRows, err := s.db.QueryContext(ctx,
		`SELECT id, workspace, domain, type, name, substr(content, 1, ?), COALESCE(url, ''), COALESCE(path, '') FROM nodes ORDER BY domain, type, name`,
		contentLimit)
	if err != nil {
		return nil, nil, err
	}
	defer nodeRows.Close()

	var nodes []Node
	for nodeRows.Next() {
		var n Node
		if err := nodeRows.Scan(&n.ID, &n.Workspace, &n.Domain, &n.Type, &n.Name, &n.Content, &n.URL, &n.Path); err != nil {
			return nil, nil, err
		}
		nodes = append(nodes, n)
	}
	if err := nodeRows.Err(); err != nil {
		return nil, nil, err
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
	return nodes, edges, edgeRows.Err()
}

func (s *LibSQLStore) GetAllGraphElements(ctx context.Context) ([]Node, []Edge, error) {
	nodeRows, err := s.db.QueryContext(ctx, `SELECT `+nodeColumns+` FROM nodes ORDER BY domain, type, name`)
	if err != nil {
		return nil, nil, err
	}
	defer nodeRows.Close()

	var nodes []Node
	for nodeRows.Next() {
		n, err := scanNode(nodeRows)
		if err != nil {
			return nil, nil, err
		}
		nodes = append(nodes, n)
	}
	if err := nodeRows.Err(); err != nil {
		return nil, nil, err
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
	return nodes, edges, edgeRows.Err()
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
	// Use nowTimestamp() (RFC3339) to match every other writer. SQLite's
	// CURRENT_TIMESTAMP renders "2006-01-02 15:04:05" (space-separated), which
	// sorts before the "T"-separated RFC3339 values in the string-ordered
	// `ORDER BY updated_at DESC` used by SearchMemoryRecords, corrupting recency.
	_, err := s.db.ExecContext(ctx, `UPDATE memory_records
		SET lifecycle_state = ?, replaced_by_node_id = ?, deprecated_message = ?, updated_at = ?
		WHERE node_id = ?`, lifecycleState, replacedByNodeID, deprecatedMessage, nowTimestamp(), nodeID)
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
	if _, err = s.db.ExecContext(ctx, `DELETE FROM nodes_fts WHERE node_id = ?`, id); err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM nodes WHERE id = ?`, id)
	return err
}

func (s *LibSQLStore) DeleteFileNodes(ctx context.Context, workspace string, relativePath string) error {
	relativePath = filepath.ToSlash(relativePath)
	if _, err := s.db.ExecContext(ctx, `DELETE FROM nodes_fts
		WHERE node_id IN (
			SELECT id FROM nodes WHERE workspace = ?1
			AND (url = ?2 OR (substr(url, 1, length(?2)) = ?2 AND substr(url, length(?2) + 1, 1) = '#'))
		)`, workspace, relativePath); err != nil {
		return err
	}
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
	if _, err = s.db.ExecContext(ctx, `DELETE FROM nodes_fts WHERE workspace = ?`, workspace); err != nil {
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
	if _, err := s.db.ExecContext(ctx, `DELETE FROM nodes_fts`); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM access_events`); err != nil {
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

func nowTimestamp() string {
	return time.Now().UTC().Format(time.RFC3339)
}

// AccessNode is a node ranked by how often it has been accessed.
type AccessNode struct {
	NodeID string `json:"node_id"`
	Name   string `json:"name"`
	Type   string `json:"type"`
	URL    string `json:"url,omitempty"`
	Count  int    `json:"count"`
}

type AccessKind struct {
	Kind  string `json:"kind"`
	Count int    `json:"count"`
}

type AccessSearch struct {
	Query string `json:"query"`
	Count int    `json:"count"`
}

type RecentAccess struct {
	NodeID    string `json:"node_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Type      string `json:"type,omitempty"`
	Kind      string `json:"kind"`
	Query     string `json:"query,omitempty"`
	CreatedAt string `json:"created_at"`
}

// Analytics summarizes access activity for the studio dashboard.
type Analytics struct {
	TotalEvents int            `json:"total_events"`
	Last24h     int            `json:"last_24h"`
	UniqueNodes int            `json:"unique_nodes"`
	Searches    int            `json:"searches"`
	TopNodes    []AccessNode   `json:"top_nodes"`
	ByKind      []AccessKind   `json:"by_kind"`
	TopSearches []AccessSearch `json:"top_searches"`
	Recent      []RecentAccess `json:"recent"`
}

// RecordAccess logs an access event (a node view/read or a search). Failures
// are non-fatal to callers — analytics is best-effort telemetry.
func (s *LibSQLStore) RecordAccess(ctx context.Context, nodeID string, kind string, query string) error {
	kind = strings.TrimSpace(kind)
	if kind == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO access_events (node_id, kind, query, created_at) VALUES (?, ?, ?, ?)`,
		strings.TrimSpace(nodeID), kind, strings.TrimSpace(query), nowTimestamp())
	return err
}

func (s *LibSQLStore) Analytics(ctx context.Context, limit int) (Analytics, error) {
	if limit <= 0 {
		limit = 10
	}
	var a Analytics

	if err := s.db.QueryRowContext(ctx, `SELECT count(*), count(DISTINCT CASE WHEN node_id <> '' THEN node_id END) FROM access_events`).
		Scan(&a.TotalEvents, &a.UniqueNodes); err != nil {
		return Analytics{}, err
	}
	cutoff := time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339)
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM access_events WHERE created_at >= ?`, cutoff).Scan(&a.Last24h); err != nil {
		return Analytics{}, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM access_events WHERE kind = 'search'`).Scan(&a.Searches); err != nil {
		return Analytics{}, err
	}

	topRows, err := s.db.QueryContext(ctx, `SELECT e.node_id, COALESCE(n.name, ''), COALESCE(n.type, ''), COALESCE(n.url, ''), count(*) AS c
		FROM access_events e LEFT JOIN nodes n ON n.id = e.node_id
		WHERE e.node_id <> '' GROUP BY e.node_id ORDER BY c DESC, e.node_id LIMIT ?`, limit)
	if err != nil {
		return Analytics{}, err
	}
	for topRows.Next() {
		var an AccessNode
		if err := topRows.Scan(&an.NodeID, &an.Name, &an.Type, &an.URL, &an.Count); err != nil {
			_ = topRows.Close()
			return Analytics{}, err
		}
		a.TopNodes = append(a.TopNodes, an)
	}
	_ = topRows.Close()

	kindRows, err := s.db.QueryContext(ctx, `SELECT kind, count(*) c FROM access_events GROUP BY kind ORDER BY c DESC`)
	if err != nil {
		return Analytics{}, err
	}
	for kindRows.Next() {
		var k AccessKind
		if err := kindRows.Scan(&k.Kind, &k.Count); err != nil {
			_ = kindRows.Close()
			return Analytics{}, err
		}
		a.ByKind = append(a.ByKind, k)
	}
	_ = kindRows.Close()

	searchRows, err := s.db.QueryContext(ctx, `SELECT query, count(*) c FROM access_events WHERE kind = 'search' AND query <> '' GROUP BY query ORDER BY c DESC, query LIMIT ?`, limit)
	if err != nil {
		return Analytics{}, err
	}
	for searchRows.Next() {
		var sr AccessSearch
		if err := searchRows.Scan(&sr.Query, &sr.Count); err != nil {
			_ = searchRows.Close()
			return Analytics{}, err
		}
		a.TopSearches = append(a.TopSearches, sr)
	}
	_ = searchRows.Close()

	recentRows, err := s.db.QueryContext(ctx, `SELECT e.node_id, COALESCE(n.name, ''), COALESCE(n.type, ''), e.kind, e.query, e.created_at
		FROM access_events e LEFT JOIN nodes n ON n.id = e.node_id
		ORDER BY e.id DESC LIMIT ?`, limit*2)
	if err != nil {
		return Analytics{}, err
	}
	for recentRows.Next() {
		var ra RecentAccess
		if err := recentRows.Scan(&ra.NodeID, &ra.Name, &ra.Type, &ra.Kind, &ra.Query, &ra.CreatedAt); err != nil {
			_ = recentRows.Close()
			return Analytics{}, err
		}
		a.Recent = append(a.Recent, ra)
	}
	_ = recentRows.Close()

	return a, nil
}

func marshalProperties(props map[string]string) (string, error) {
	if len(props) == 0 {
		return "{}", nil
	}
	data, err := json.Marshal(props)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func unmarshalProperties(value string) map[string]string {
	value = strings.TrimSpace(value)
	if value == "" || value == "{}" {
		return nil
	}
	out := map[string]string{}
	if err := json.Unmarshal([]byte(value), &out); err != nil {
		return nil
	}
	if len(out) == 0 {
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
