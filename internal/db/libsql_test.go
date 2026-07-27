package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"path/filepath"
	"testing"
)

func TestCosineSimilarity(t *testing.T) {
	t.Parallel()

	got := cosineSimilarity([]float32{1, 0}, []float32{1, 0})
	if got < 0.999 {
		t.Fatalf("expected vectors to be nearly identical, got %f", got)
	}

	got = cosineSimilarity([]float32{1, 0}, []float32{0, 1})
	if got != 0 {
		t.Fatalf("expected orthogonal vectors to have zero similarity, got %f", got)
	}

	got = cosineSimilarity([]float32{1, 0}, []float32{1, 0, 0})
	if got != 0 {
		t.Fatalf("expected mismatched dimensions to return zero, got %f", got)
	}
}

func TestSearchResultsReportEmbeddingLength(t *testing.T) {
	rawDB, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	store := &LibSQLStore{db: rawDB}
	defer store.Close()
	if err := store.migrate(); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveNode(context.Background(), Node{
		ID: "node", Workspace: "test", Domain: "memory", Type: "memory",
		Name: "Searchable", Content: "vector content", Embedding: []float32{1, 0},
	}); err != nil {
		t.Fatal(err)
	}

	keyword, err := store.KeywordSearch(context.Background(), "vector", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(keyword) != 1 || keyword[0].EmbeddingLength != 2 {
		t.Fatalf("keyword search did not report embedding length: %+v", keyword)
	}

	semantic, err := store.VectorSearch(context.Background(), []float32{1, 0}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(semantic) != 1 || semantic[0].EmbeddingLength != 2 {
		t.Fatalf("semantic search did not report embedding length: %+v", semantic)
	}
}

func TestNodePathPersistsAndScopedSearchExcludesOtherWorkspaces(t *testing.T) {
	rawDB, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	store := &LibSQLStore{db: rawDB}
	defer store.Close()
	if err := store.migrate(); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	for _, node := range []Node{
		{ID: "one", Workspace: "ws-one", Domain: "code", Type: "file", Name: "one.go", Content: "shared query", Path: "/code/one", Embedding: []float32{1, 0}},
		{ID: "two", Workspace: "ws-two", Domain: "code", Type: "file", Name: "two.go", Content: "shared query", Path: "/code/two", Embedding: []float32{1, 0}},
	} {
		if err := store.SaveNode(ctx, node); err != nil {
			t.Fatal(err)
		}
	}

	got, err := store.GetNodeByID(ctx, "one")
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != "/code/one" {
		t.Fatalf("expected persisted path, got %+v", got)
	}
	keyword, err := store.KeywordSearchWorkspace(ctx, "ws-one", "shared query", 5)
	if err != nil {
		t.Fatal(err)
	}
	vector, err := store.VectorSearchWorkspace(ctx, "ws-one", []float32{1, 0}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(keyword) != 1 || keyword[0].ID != "one" || len(vector) != 1 || vector[0].ID != "one" {
		t.Fatalf("workspace searches leaked nodes: keyword=%+v vector=%+v", keyword, vector)
	}
}

func TestMigrationAddsPathToExistingNodesTable(t *testing.T) {
	rawDB, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	if _, err := rawDB.Exec(`CREATE TABLE nodes (
		id TEXT PRIMARY KEY,
		workspace TEXT NOT NULL,
		domain TEXT NOT NULL,
		type TEXT NOT NULL,
		name TEXT NOT NULL,
		content TEXT NOT NULL,
		url TEXT,
		embedding_json TEXT NOT NULL DEFAULT '[]'
	)`); err != nil {
		t.Fatal(err)
	}

	store := &LibSQLStore{db: rawDB}
	if err := store.migrate(); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveNode(context.Background(), Node{
		ID: "node", Workspace: "ws", Domain: "code", Type: "file", Name: "node.go", Path: "/code",
	}); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetNodeByID(context.Background(), "node")
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != "/code" {
		t.Fatalf("expected migrated path column, got %+v", got)
	}
}

func TestDeleteFileNodesRemovesGeneratedChildrenOnly(t *testing.T) {
	rawDB, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	store := &LibSQLStore{db: rawDB}
	defer store.Close()
	if err := store.migrate(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, node := range []Node{
		{ID: "file-a", Workspace: "ws", Domain: "code", Type: "file", Name: "a.go", URL: "a.go"},
		{ID: "func-a", Workspace: "ws", Domain: "code", Type: "func", Name: "A", URL: "a.go#A"},
		{ID: "file-b", Workspace: "ws", Domain: "code", Type: "file", Name: "b.go", URL: "b.go"},
	} {
		if err := store.SaveNode(ctx, node); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SaveEdge(ctx, Edge{SourceID: "file-a", TargetID: "func-a", Type: "DECLARES"}); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteFileNodes(ctx, "ws", "a.go"); err != nil {
		t.Fatal(err)
	}
	nodes, edges, err := store.GetAllGraphElements(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].ID != "file-b" || len(edges) != 0 {
		t.Fatalf("unexpected graph after file cleanup: nodes=%+v edges=%+v", nodes, edges)
	}
}

func newTestStore(t *testing.T) *LibSQLStore {
	t.Helper()
	rawDB, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	store := &LibSQLStore{db: rawDB}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.migrate(); err != nil {
		t.Fatal(err)
	}
	return store
}

func TestFTSKeywordSearchRanksAndPersistsProperties(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	nodes := []Node{
		{ID: "a", Workspace: "ws", Domain: "code", Type: "func", Name: "ConnectDatabase", Content: "open a database connection pool", Properties: map[string]string{"doc_type": "architecture"}},
		{ID: "b", Workspace: "ws", Domain: "code", Type: "func", Name: "ParseConfig", Content: "read configuration values from disk"},
		{ID: "c", Workspace: "ws", Domain: "code", Type: "file", Name: "database.go", Content: "package db has database helpers and a connection wrapper"},
	}
	for _, n := range nodes {
		if err := store.SaveNode(ctx, n); err != nil {
			t.Fatal(err)
		}
	}

	got, err := store.KeywordSearch(ctx, "database connection", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) < 2 {
		t.Fatalf("expected >=2 matches for 'database connection', got %d: %+v", len(got), got)
	}
	// ParseConfig (no match) must not appear.
	for _, n := range got {
		if n.ID == "b" {
			t.Fatalf("non-matching node leaked into results: %+v", got)
		}
	}

	// Properties round-trip through SaveNode -> GetNodeByID.
	a, err := store.GetNodeByID(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	if a.Prop("doc_type") != "architecture" {
		t.Fatalf("properties not persisted: %+v", a.Properties)
	}
	if a.CreatedAt == "" || a.UpdatedAt == "" {
		t.Fatalf("timestamps not set: created=%q updated=%q", a.CreatedAt, a.UpdatedAt)
	}
}

func TestLexicalSearchLiteralSubstring(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	if err := store.SaveNode(ctx, Node{ID: "x", Workspace: "ws", Domain: "code", Type: "func", Name: "handleRequest", Content: "func handleRequest(w http.ResponseWriter) {}"}); err != nil {
		t.Fatal(err)
	}
	// Substring across an identifier — literal trigram match.
	got, err := store.LexicalSearch(ctx, "ws", "ResponseWriter", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "x" {
		t.Fatalf("literal lexical search failed: %+v", got)
	}
}

func TestListNodesByPropertyAndSetProperties(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	for _, n := range []Node{
		{ID: "h1", Workspace: "ws", Domain: "knowledge", Type: "doc", Name: "Handoff A", Content: "x", Properties: map[string]string{"doc_type": "handoff", "status": "fresh"}},
		{ID: "d1", Workspace: "ws", Domain: "knowledge", Type: "doc", Name: "Arch", Content: "y", Properties: map[string]string{"doc_type": "architecture"}},
	} {
		if err := store.SaveNode(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	got, err := store.ListNodes(ctx, NodeFilter{Workspace: "ws", PropertyEquals: map[string]string{"doc_type": "handoff"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "h1" {
		t.Fatalf("property filter failed: %+v", got)
	}
	if err := store.SetNodeProperties(ctx, "h1", map[string]string{"status": "used"}); err != nil {
		t.Fatal(err)
	}
	h, err := store.GetNodeByID(ctx, "h1")
	if err != nil {
		t.Fatal(err)
	}
	if h.Prop("status") != "used" || h.Prop("doc_type") != "handoff" {
		t.Fatalf("set/merge properties failed: %+v", h.Properties)
	}
}

func TestKeywordSearchUpdatesAfterContentChange(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	id := "n"
	if err := store.SaveNode(ctx, Node{ID: id, Workspace: "ws", Domain: "code", Type: "file", Name: "f.go", Content: "alpha beta gamma"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveNode(ctx, Node{ID: id, Workspace: "ws", Domain: "code", Type: "file", Name: "f.go", Content: "delta epsilon zeta"}); err != nil {
		t.Fatal(err)
	}
	old, err := store.KeywordSearch(ctx, "alpha", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(old) != 0 {
		t.Fatalf("stale FTS row served after update: %+v", old)
	}
	fresh, err := store.KeywordSearch(ctx, "epsilon", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(fresh) != 1 {
		t.Fatalf("updated FTS content not found: %+v", fresh)
	}
}

func TestAccessAnalytics(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	if err := store.SaveNode(ctx, Node{ID: "n1", Workspace: "ws", Domain: "code", Type: "func", Name: "Hot", Content: "x"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := store.RecordAccess(ctx, "n1", "view", ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.RecordAccess(ctx, "", "search", "database"); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordAccess(ctx, "", "search", "database"); err != nil {
		t.Fatal(err)
	}
	a, err := store.Analytics(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if a.TotalEvents != 5 || a.Searches != 2 || a.UniqueNodes != 1 {
		t.Fatalf("analytics totals wrong: %+v", a)
	}
	if len(a.TopNodes) != 1 || a.TopNodes[0].NodeID != "n1" || a.TopNodes[0].Count != 3 || a.TopNodes[0].Name != "Hot" {
		t.Fatalf("top nodes wrong: %+v", a.TopNodes)
	}
	if len(a.TopSearches) != 1 || a.TopSearches[0].Query != "database" || a.TopSearches[0].Count != 2 {
		t.Fatalf("top searches wrong: %+v", a.TopSearches)
	}
	if a.Last24h != 5 {
		t.Fatalf("expected 5 events in last 24h, got %d", a.Last24h)
	}
}

func TestMigrateIfNeededSkipsWhenSchemaVersionCurrent(t *testing.T) {
	rawDB, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	store := &LibSQLStore{db: rawDB}
	defer store.Close()

	if err := store.migrateIfNeeded(); err != nil {
		t.Fatal(err)
	}
	var version int
	if err := rawDB.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != schemaVersion {
		t.Fatalf("expected schema stamped at %d, got %d", schemaVersion, version)
	}

	// Drop a migration-managed table: if the second pass really skips, the
	// table stays gone; if it re-runs, CREATE TABLE IF NOT EXISTS restores it.
	if _, err := rawDB.Exec(`DROP TABLE access_events`); err != nil {
		t.Fatal(err)
	}
	if err := store.migrateIfNeeded(); err != nil {
		t.Fatal(err)
	}
	var count int
	err = rawDB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'access_events'`).Scan(&count)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("expected migration pass to be skipped for a current schema version, but DDL ran again")
	}
}

func TestMigrateIfNeededRunsWhenSchemaVersionBehind(t *testing.T) {
	rawDB, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	store := &LibSQLStore{db: rawDB}
	defer store.Close()

	// A fresh database reports user_version 0, i.e. behind schemaVersion.
	if err := store.migrateIfNeeded(); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveNode(context.Background(), Node{
		ID: "node", Workspace: "ws", Domain: "code", Type: "file", Name: "node.go",
	}); err != nil {
		t.Fatalf("expected migrated schema to accept writes: %v", err)
	}
}

func TestMigrateIfNeededSkipsWhenSchemaVersionAhead(t *testing.T) {
	rawDB, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	store := &LibSQLStore{db: rawDB}
	defer store.Close()

	// A database stamped by a newer raph must not be re-migrated by an older
	// binary: its schema is already a superset of what this binary creates.
	if _, err := rawDB.Exec(`PRAGMA user_version = 99`); err != nil {
		t.Fatal(err)
	}
	if err := store.migrateIfNeeded(); err != nil {
		t.Fatal(err)
	}

	var tables int
	if err := rawDB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table'`).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if tables != 0 {
		t.Fatalf("expected no DDL against a schema stamped ahead, found %d tables", tables)
	}
	var version int
	if err := rawDB.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 99 {
		t.Fatalf("expected stamped version preserved, got %d", version)
	}
}

// TestSchemaVersionBumpedWhenMigrationsChange pins a hash of every migration
// statement to the current schemaVersion. If you edit migrationStatements or
// nodeColumnAdditions without bumping schemaVersion, existing databases would
// silently skip the new DDL forever — this test turns that mistake into a
// build failure with instructions.
func TestSchemaVersionBumpedWhenMigrationsChange(t *testing.T) {
	h := sha256.New()
	for _, q := range migrationStatements {
		h.Write([]byte(q))
		h.Write([]byte{0})
	}
	for _, add := range nodeColumnAdditions {
		h.Write([]byte(add.name))
		h.Write([]byte{0})
		h.Write([]byte(add.ddl))
		h.Write([]byte{0})
	}
	got := hex.EncodeToString(h.Sum(nil))

	// One pinned hash per schemaVersion, ever. When migrations change:
	// 1. bump schemaVersion in libsql.go
	// 2. add the new version with the hash this test prints on failure
	pinned := map[int]string{
		1: "279659415c0a7a8b8b6fc79a9a7332dba1c42d490369f2984f9145ce0cc919f3",
	}

	want, ok := pinned[schemaVersion]
	if !ok {
		t.Fatalf("schemaVersion %d has no pinned migration hash; add {%d: %q} to this test", schemaVersion, schemaVersion, got)
	}
	if got != want {
		t.Fatalf("migration statements changed but schemaVersion is still %d.\n"+
			"Bump schemaVersion in libsql.go, then pin the new version's hash here: %q", schemaVersion, got)
	}
}

func TestVectorSearchMemoryRecordsRanksAndScopes(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	seed := func(id string, scopeID string, embedding []float32) {
		if err := store.SaveNode(ctx, Node{
			ID: id, Workspace: "ws", Domain: "memory", Type: "memory",
			Name: id, Content: "content " + id, Embedding: embedding,
		}); err != nil {
			t.Fatal(err)
		}
		if err := store.UpsertMemoryRecord(ctx, MemoryRecord{
			Node: Node{ID: id}, ScopeType: "project", ScopeID: scopeID, LifecycleState: "active",
			KnowledgeType: "decision", Source: "user", WriterID: "w", MemoryKey: id,
			CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z", Revision: 1,
		}); err != nil {
			t.Fatal(err)
		}
	}
	seed("m-near", "proj", []float32{1, 0})
	seed("m-far", "proj", []float32{0.6, 0.8})    // similar, but less than m-near
	seed("m-other", "otherproj", []float32{1, 0}) // right vector, wrong scope

	got, err := store.VectorSearchMemoryRecords(ctx, []float32{1, 0}, MemorySearchFilter{
		ScopeType: "project", ScopeID: "proj", LifecycleStates: []string{"active"}, Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 in-scope records, got %d (%+v)", len(got), got)
	}
	if got[0].Node.ID != "m-near" {
		t.Fatalf("expected m-near ranked first, got %q", got[0].Node.ID)
	}
	for _, r := range got {
		if r.Node.ID == "m-other" {
			t.Fatal("scope filter leaked a record from another scope")
		}
	}
}

func TestUpdateNodeEmbeddingBackfills(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	if err := store.SaveNode(ctx, Node{
		ID: "m1", Workspace: "ws", Domain: "memory", Type: "memory", Name: "m1", Content: "body",
	}); err != nil {
		t.Fatal(err)
	}
	// No embedding yet → not vector-searchable.
	if got, _ := store.VectorSearch(ctx, []float32{1, 0}, 5); len(got) != 0 {
		t.Fatalf("expected no vector matches before backfill, got %d", len(got))
	}
	if err := store.UpdateNodeEmbedding(ctx, "m1", []float32{1, 0}); err != nil {
		t.Fatal(err)
	}
	got, err := store.VectorSearch(ctx, []float32{1, 0}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "m1" {
		t.Fatalf("expected m1 to be vector-searchable after backfill, got %+v", got)
	}
}

func TestSearchMemoryRecordsPaginatesWithOffset(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	// updated_at DESC, id ASC ordering — stamp distinct times for a stable order.
	timestamps := []string{"2026-01-03T00:00:00Z", "2026-01-02T00:00:00Z", "2026-01-01T00:00:00Z"} // m1 newest
	for i, id := range []string{"m1", "m2", "m3"} {
		if err := store.SaveNode(ctx, Node{ID: id, Workspace: "ws", Domain: "memory", Type: "memory", Name: id, Content: id}); err != nil {
			t.Fatal(err)
		}
		ts := timestamps[i]
		if err := store.UpsertMemoryRecord(ctx, MemoryRecord{
			Node: Node{ID: id}, ScopeType: "project", ScopeID: "p", LifecycleState: "active",
			KnowledgeType: "decision", Source: "u", WriterID: "w", MemoryKey: id,
			CreatedAt: ts, UpdatedAt: ts, Revision: 1,
		}); err != nil {
			t.Fatal(err)
		}
	}
	page1, err := store.SearchMemoryRecords(ctx, MemorySearchFilter{Limit: 2, Offset: 0})
	if err != nil || len(page1) != 2 {
		t.Fatalf("page1 = %d records, %v", len(page1), err)
	}
	page2, err := store.SearchMemoryRecords(ctx, MemorySearchFilter{Limit: 2, Offset: 2})
	if err != nil || len(page2) != 1 {
		t.Fatalf("page2 = %d records, %v", len(page2), err)
	}
	// No overlap between pages.
	if page2[0].Node.ID == page1[0].Node.ID || page2[0].Node.ID == page1[1].Node.ID {
		t.Fatalf("offset page overlaps first page: %q", page2[0].Node.ID)
	}
}

func TestDeleteDocumentNodeRemovesDocAndChunks(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	// A doc with two chunks (linked by properties_json.doc_id), plus a memory
	// node that must be refused.
	if err := store.SaveNode(ctx, Node{ID: "doc:h", Workspace: "ws:global-knowledge", Domain: "knowledge", Type: "doc", Name: "Handoff", Content: "body", URL: "knowledge://ws:global-knowledge/h"}); err != nil {
		t.Fatal(err)
	}
	for _, cid := range []string{"chunk:1", "chunk:2"} {
		if err := store.SaveNode(ctx, Node{ID: cid, Workspace: "ws:global-knowledge", Domain: "knowledge", Type: "doc_chunk", Name: cid, Content: "c", Properties: map[string]string{"doc_id": "doc:h"}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SaveNode(ctx, Node{ID: "mem:x", Workspace: "w", Domain: "memory", Type: "memory", Name: "m", Content: "c"}); err != nil {
		t.Fatal(err)
	}

	// Refuse non-document nodes.
	if err := store.DeleteDocumentNode(ctx, "mem:x"); err != sql.ErrNoRows {
		t.Fatalf("expected ErrNoRows deleting a non-document, got %v", err)
	}
	if _, err := store.GetNodeByID(ctx, "mem:x"); err != nil {
		t.Fatalf("non-document node should be untouched, got %v", err)
	}

	// Delete the doc; its chunks go with it.
	if err := store.DeleteDocumentNode(ctx, "doc:h"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"doc:h", "chunk:1", "chunk:2"} {
		if _, err := store.GetNodeByID(ctx, id); err != sql.ErrNoRows {
			t.Fatalf("expected %s gone, got %v", id, err)
		}
	}
	// Second delete is a no-op 404.
	if err := store.DeleteDocumentNode(ctx, "doc:h"); err != sql.ErrNoRows {
		t.Fatalf("expected ErrNoRows on re-delete, got %v", err)
	}
}
