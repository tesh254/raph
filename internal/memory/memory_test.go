package memory

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"raph/internal/config"
	"raph/internal/db"
)

type captureStore struct {
	node    db.Node
	records map[string]db.MemoryRecord
}

func (s *captureStore) SaveNode(_ context.Context, node db.Node) error {
	s.node = node
	return nil
}
func (*captureStore) SaveEdge(context.Context, db.Edge) error { return nil }
func (*captureStore) VectorSearch(context.Context, []float32, int) ([]db.Node, error) {
	return nil, nil
}
func (*captureStore) VectorSearchWorkspace(context.Context, string, []float32, int) ([]db.Node, error) {
	return nil, nil
}
func (*captureStore) KeywordSearch(context.Context, string, int) ([]db.Node, error) {
	return nil, nil
}
func (*captureStore) KeywordSearchWorkspace(context.Context, string, string, int) ([]db.Node, error) {
	return nil, nil
}
func (*captureStore) LexicalSearch(context.Context, string, string, int) ([]db.Node, error) {
	return nil, nil
}
func (*captureStore) ListNodes(context.Context, db.NodeFilter) ([]db.Node, error) { return nil, nil }
func (*captureStore) SetNodeProperties(context.Context, string, map[string]string) error {
	return nil
}
func (*captureStore) GetNodeByID(context.Context, string) (db.Node, error) { return db.Node{}, nil }
func (*captureStore) GetNeighbors(context.Context, string) ([]db.Node, []db.Edge, error) {
	return nil, nil, nil
}
func (*captureStore) GetAllGraphElements(context.Context) ([]db.Node, []db.Edge, error) {
	return nil, nil, nil
}
func (s *captureStore) UpsertMemoryRecord(_ context.Context, record db.MemoryRecord) error {
	if s.records == nil {
		s.records = make(map[string]db.MemoryRecord)
	}
	s.records[record.Node.ID] = record
	return nil
}
func (s *captureStore) GetMemoryRecord(_ context.Context, nodeID string) (db.MemoryRecord, error) {
	record, ok := s.records[nodeID]
	if !ok {
		return db.MemoryRecord{}, sql.ErrNoRows
	}
	return record, nil
}
func (s *captureStore) GetMemoryRecordByKey(_ context.Context, scopeType string, scopeID string, knowledgeType string, memoryKey string) (db.MemoryRecord, error) {
	for _, record := range s.records {
		if record.ScopeType == scopeType && record.ScopeID == scopeID && record.KnowledgeType == knowledgeType && record.MemoryKey == memoryKey {
			return record, nil
		}
	}
	return db.MemoryRecord{}, sql.ErrNoRows
}
func (*captureStore) InsertMemoryRevision(context.Context, db.MemoryRevision) error { return nil }
func (*captureStore) ListMemoryRevisions(context.Context, string) ([]db.MemoryRevision, error) {
	return nil, nil
}
func (*captureStore) SearchMemoryRecords(context.Context, db.MemorySearchFilter) ([]db.MemoryRecord, error) {
	return nil, nil
}
func (*captureStore) VectorSearchMemoryRecords(context.Context, []float32, db.MemorySearchFilter) ([]db.MemoryRecord, error) {
	return nil, nil
}
func (*captureStore) SetMemoryLifecycle(context.Context, string, string, string, string) error {
	return nil
}
func (*captureStore) SaveWebCorpus(context.Context, db.WebCorpus) error             { return nil }
func (*captureStore) SaveWebCrawlVersion(context.Context, db.WebCrawlVersion) error { return nil }
func (*captureStore) DeleteNodeByID(context.Context, string) error                  { return nil }
func (*captureStore) DeleteDocumentNode(context.Context, string) error              { return nil }
func (*captureStore) DeleteFileNodes(context.Context, string, string) error         { return nil }
func (*captureStore) DeleteWorkspace(context.Context, string) error                 { return nil }
func (*captureStore) ClearAll(context.Context) error                                { return nil }
func (*captureStore) ListWorkspaces(context.Context) ([]db.Workspace, error)        { return nil, nil }
func (*captureStore) Close() error                                                  { return nil }

func TestStoreGeneratesAndPersistsEmbedding(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"embedding":[0.1,0.2,0.3]}]}`))
	}))
	defer server.Close()

	cfg := &config.Config{Vector: config.VectorSettings{
		CurrentProvider: "openrouter",
		Providers: config.ProviderContainer{OpenRouter: config.OpenRouterConfig{
			APIKey:  "test",
			Model:   "test-model",
			BaseURL: server.URL,
		}},
	}}
	store := &captureStore{}

	output, err := Store(context.Background(), store, cfg, StoreInput{
		ScopeType:     "project",
		ScopeID:       "project:test",
		KnowledgeType: "preference",
		MemoryKey:     "project-style",
		Title:         "Project style",
		Content:       "Use focused changes and run tests.",
		Source:        "user",
		WriterID:      "agent:test",
		Tags:          []string{"Style", "Tests"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !output.Embedded || output.Record.Node.EmbeddingLength != 3 {
		t.Fatalf("expected a 3-float embedding, got %+v", output)
	}
	if len(store.node.Embedding) != 3 {
		t.Fatalf("expected persisted embedding, got %d floats", len(store.node.Embedding))
	}
	if output.Record.ScopeType != "project" || output.Record.MemoryKey != "project-style" {
		t.Fatalf("expected scoped record metadata, got %+v", output.Record)
	}
}

func ids(records []db.MemoryRecord) string {
	out := make([]string, len(records))
	for i, r := range records {
		out[i] = r.Node.ID
	}
	return strings.Join(out, ",")
}

func TestMergeMemoryMatchesUnionsAndDedupes(t *testing.T) {
	sem := []db.MemoryRecord{{Node: db.Node{ID: "a"}}, {Node: db.Node{ID: "b"}}}
	kw := []db.MemoryRecord{{Node: db.Node{ID: "b"}}, {Node: db.Node{ID: "c"}}}

	out, mode := mergeMemoryMatches(sem, kw, 10)
	if ids(out) != "a,b,c" {
		t.Fatalf("expected semantic order then keyword extras deduped, got %v", ids(out))
	}
	if mode != "hybrid" {
		t.Fatalf("both passes contributed, want hybrid mode, got %q", mode)
	}

	// Keyword-only still reports the keyword mode.
	if _, mode := mergeMemoryMatches(nil, kw, 10); mode != "keyword" {
		t.Fatalf("no semantic pass should be keyword mode, got %q", mode)
	}
}

// TestMergeReservesSlotsForKeywordOnly is the regression for the cubic finding:
// a keyword-only hit (exact/just-written) must survive even when the semantic
// pass already produced `limit` results. The reserve keeps a slot for it.
func TestMergeReservesSlotsForKeywordOnly(t *testing.T) {
	// Semantic fills the whole limit; d is a keyword-only exact hit.
	sem := []db.MemoryRecord{{Node: db.Node{ID: "a"}}, {Node: db.Node{ID: "b"}}, {Node: db.Node{ID: "c"}}}
	kw := []db.MemoryRecord{{Node: db.Node{ID: "d"}}}

	out, mode := mergeMemoryMatches(sem, kw, 3)
	if ids(out) != "a,b,d" {
		t.Fatalf("keyword-only hit should claim a reserved slot, got %v", ids(out))
	}
	if mode != "hybrid" {
		t.Fatalf("both passes contributed, want hybrid, got %q", mode)
	}
	if len(out) != 3 {
		t.Fatalf("result must respect the limit, got %d", len(out))
	}
}

// TestSearchUnscopedSpansScopes is the regression for the "memory looks missing"
// report: without a scope filter, Search must return memories from every scope,
// while an explicit scope still narrows. cfg is nil so this exercises the
// always-on keyword pass (no embedding provider).
func TestSearchUnscopedSpansScopes(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store, err := db.InitStorage()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	for _, scopeID := range []string{"p1", "p2"} {
		if _, err := Store(ctx, store, nil, StoreInput{
			ScopeType: "project", ScopeID: scopeID, KnowledgeType: "decision",
			MemoryKey: "deploy-" + scopeID, Title: "Deploy " + scopeID,
			Content: "we deploy through CI", Source: "user", WriterID: "agent",
		}); err != nil {
			t.Fatal(err)
		}
	}

	all, err := Search(ctx, store, nil, SearchInput{Query: "deploy"})
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Matches) != 2 {
		t.Fatalf("unscoped search should span both scope ids, got %d matches", len(all.Matches))
	}

	one, err := Search(ctx, store, nil, SearchInput{Query: "deploy", ScopeType: "project", ScopeID: "p1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(one.Matches) != 1 || one.Matches[0].ScopeID != "p1" {
		t.Fatalf("explicit scope should still narrow to p1, got %+v", one.Matches)
	}
}

func TestPutCreatesThenUpdates(t *testing.T) {
	store := &captureStore{}
	ctx := context.Background()
	in := StoreInput{
		ScopeType: "global", ScopeID: "global", KnowledgeType: "rule",
		MemoryKey: "no-cgo", Title: "No CGO", Content: "Keep CGO disabled.",
		Source: "cli", WriterID: "cli",
	}
	first, err := Put(ctx, store, nil, in)
	if err != nil {
		t.Fatal(err)
	}
	if first.Record.Revision != 1 {
		t.Fatalf("expected revision 1 on create, got %d", first.Record.Revision)
	}
	in.Content = "Keep CGO disabled for portability."
	second, err := Put(ctx, store, nil, in)
	if err != nil {
		t.Fatal(err)
	}
	if second.Record.Revision != 2 {
		t.Fatalf("expected revision 2 on update, got %d", second.Record.Revision)
	}
	if second.Record.Node.ID != first.Record.Node.ID {
		t.Fatalf("Put changed node id across update: %s != %s", second.Record.Node.ID, first.Record.Node.ID)
	}
	if second.Record.Node.Content != "Keep CGO disabled for portability." {
		t.Fatalf("Put did not update content: %q", second.Record.Node.Content)
	}
}

// TestUpdateAtomicRevisionOnRealStore exercises the transactional commit path
// (LibSQLStore.CommitMemoryRecord) rather than the mock fallback: an update must
// bump the record revision AND append exactly one revision-history row, and the
// two must land together.
func TestUpdateAtomicRevisionOnRealStore(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store, err := db.InitStorage()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()

	in := StoreInput{
		ScopeType: "global", ScopeID: "global", KnowledgeType: "decision",
		MemoryKey: "db-choice", Title: "DB", Content: "sqlite",
		Source: "cli", WriterID: "cli",
	}
	created, err := Store(ctx, store, nil, in)
	if err != nil {
		t.Fatal(err)
	}
	if revs, err := store.ListMemoryRevisions(ctx, created.Record.Node.ID); err != nil {
		t.Fatal(err)
	} else if len(revs) != 0 {
		t.Fatalf("create should not write a history row yet, got %d", len(revs))
	}

	in.Content = "libsql"
	updated, err := Update(ctx, store, nil, UpdateInput(in))
	if err != nil {
		t.Fatal(err)
	}
	if updated.Record.Revision != 2 {
		t.Fatalf("expected revision 2 after update, got %d", updated.Record.Revision)
	}
	revs, err := store.ListMemoryRevisions(ctx, updated.Record.Node.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(revs) != 1 {
		t.Fatalf("expected exactly 1 history row after one update, got %d", len(revs))
	}
	if revs[0].Content != "sqlite" || revs[0].Revision != 1 {
		t.Fatalf("history row should snapshot the pre-update state (rev1/sqlite), got rev%d/%q", revs[0].Revision, revs[0].Content)
	}
	// The live record reflects the new content.
	live, err := store.GetMemoryRecord(ctx, updated.Record.Node.ID)
	if err != nil {
		t.Fatal(err)
	}
	if live.Node.Content != "libsql" || live.Revision != 2 {
		t.Fatalf("live record not updated atomically: rev%d/%q", live.Revision, live.Node.Content)
	}
}

// TestConcurrentUpdatesAssignUniqueRevisions drives concurrent updates to the
// same key through TWO independent store handles on one DB file (simulating two
// raph processes). With the in-transaction revision read-modify-write and
// IMMEDIATE transactions, every update must land a distinct, monotonic revision
// — no lost updates, no duplicate revision numbers.
func TestConcurrentUpdatesAssignUniqueRevisions(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	storeA, err := db.InitStorage()
	if err != nil {
		t.Fatal(err)
	}
	defer storeA.Close()
	storeB, err := db.InitStorage() // separate connection pool -> acts like a 2nd process
	if err != nil {
		t.Fatal(err)
	}
	defer storeB.Close()
	ctx := context.Background()

	base := StoreInput{
		ScopeType: "global", ScopeID: "global", KnowledgeType: "decision",
		MemoryKey: "hot-key", Title: "K", Content: "v0", Source: "cli", WriterID: "cli",
	}
	created, err := Store(ctx, storeA, nil, base)
	if err != nil {
		t.Fatal(err)
	}
	nodeID := created.Record.Node.ID

	const writers = 8
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			store := storeA
			if i%2 == 1 {
				store = storeB
			}
			in := base
			in.Content = fmt.Sprintf("v%d", i+1)
			_, err := Update(ctx, store, nil, UpdateInput(in))
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent update failed: %v", err)
		}
	}

	live, err := storeA.GetMemoryRecordByKey(ctx, "global", "global", "decision", "hot-key")
	if err != nil {
		t.Fatal(err)
	}
	if live.Revision != writers+1 {
		t.Fatalf("expected final revision %d after %d concurrent updates, got %d", writers+1, writers, live.Revision)
	}
	revs, err := storeA.ListMemoryRevisions(ctx, nodeID)
	if err != nil {
		t.Fatal(err)
	}
	if len(revs) != writers {
		t.Fatalf("expected %d history rows, got %d", writers, len(revs))
	}
	seen := map[int]bool{}
	for _, r := range revs {
		if seen[r.Revision] {
			t.Fatalf("duplicate revision number %d in history (lost-update race)", r.Revision)
		}
		seen[r.Revision] = true
	}
}

func affinityRecord(id, scopeID string) db.MemoryRecord {
	return db.MemoryRecord{Node: db.Node{ID: id}, ScopeType: "project", ScopeID: scopeID}
}

func affinityIDs(records []db.MemoryRecord) []string {
	out := make([]string, 0, len(records))
	for _, r := range records {
		out = append(out, r.Node.ID)
	}
	return out
}

func TestApplyProjectAffinityLiftsProjectMemories(t *testing.T) {
	records := []db.MemoryRecord{
		affinityRecord("other-1", "project:other"),
		affinityRecord("other-2", "project:other"),
		affinityRecord("mine-1", "project:mine"),
		affinityRecord("other-3", "project:other"),
	}

	got := affinityIDs(applyProjectAffinity(records, "project:mine"))
	want := []string{"mine-1", "other-1", "other-2", "other-3"}
	if !slices.Equal(got, want) {
		t.Fatalf("expected the project memory boosted past nearby matches, got %v", got)
	}
}

// The boost is worth exactly affinityBoostPositions places, and relevance wins
// ties: a project memory that far behind draws level with the leader and lands
// just after it. Pinning the boundary keeps the constant honest — if someone
// raises it, this is where the change shows up.
func TestApplyProjectAffinityBoundaryTieKeepsRelevanceFirst(t *testing.T) {
	records := []db.MemoryRecord{
		affinityRecord("other-1", "project:other"),
		affinityRecord("other-2", "project:other"),
		affinityRecord("other-3", "project:other"),
		affinityRecord("mine-1", "project:mine"),
	}
	if affinityBoostPositions != 3 {
		t.Skipf("boundary fixture assumes a boost of 3, got %d", affinityBoostPositions)
	}

	got := affinityIDs(applyProjectAffinity(records, "project:mine"))
	want := []string{"other-1", "mine-1", "other-2", "other-3"}
	if !slices.Equal(got, want) {
		t.Fatalf("expected a tie at the boundary to keep the stronger match first, got %v", got)
	}
}

// The boost is bounded on purpose: a much better match from another scope must
// still win, otherwise this is a scope filter wearing a different hat.
func TestApplyProjectAffinityDoesNotOutrankAFarBetterMatch(t *testing.T) {
	records := []db.MemoryRecord{
		affinityRecord("global-best", "global"),
		affinityRecord("other-1", "project:other"),
		affinityRecord("other-2", "project:other"),
		affinityRecord("other-3", "project:other"),
		affinityRecord("other-4", "project:other"),
		affinityRecord("mine-far-down", "project:mine"),
	}

	got := affinityIDs(applyProjectAffinity(records, "project:mine"))
	if got[0] != "global-best" {
		t.Fatalf("a far stronger match was buried by the affinity boost: %v", got)
	}
	if !slices.Contains(got, "mine-far-down") {
		t.Fatalf("affinity dropped a record instead of re-ranking it: %v", got)
	}
}

func TestApplyProjectAffinityNeverDropsRecords(t *testing.T) {
	records := []db.MemoryRecord{
		affinityRecord("a", "project:mine"),
		affinityRecord("b", "global"),
		affinityRecord("c", "shared:team"),
		affinityRecord("d", "project:other"),
	}

	got := applyProjectAffinity(records, "project:mine")
	if len(got) != len(records) {
		t.Fatalf("expected all %d records retained, got %d", len(records), len(got))
	}
	seen := map[string]bool{}
	for _, r := range got {
		seen[r.Node.ID] = true
	}
	for _, r := range records {
		if !seen[r.Node.ID] {
			t.Fatalf("record %s lost during affinity re-ranking", r.Node.ID)
		}
	}
}

func TestApplyProjectAffinityIsStableWithinGroups(t *testing.T) {
	records := []db.MemoryRecord{
		affinityRecord("mine-1", "project:mine"),
		affinityRecord("mine-2", "project:mine"),
		affinityRecord("mine-3", "project:mine"),
	}

	got := affinityIDs(applyProjectAffinity(records, "project:mine"))
	want := []string{"mine-1", "mine-2", "mine-3"}
	if !slices.Equal(got, want) {
		t.Fatalf("expected relevance order preserved when every record is boosted, got %v", got)
	}
}

func TestApplyProjectAffinityNoProjectIsIdentity(t *testing.T) {
	records := []db.MemoryRecord{
		affinityRecord("a", "project:one"),
		affinityRecord("b", "project:two"),
	}

	got := affinityIDs(applyProjectAffinity(records, ""))
	if !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("expected untouched ordering without a project, got %v", got)
	}
}

// affinityStore returns records in a fixed order and records the limit it was
// asked for, so tests can see whether Search widened the candidate pool.
type affinityStore struct {
	db.GraphStore
	records    []db.MemoryRecord
	seenLimits []int
}

func (s *affinityStore) SearchMemoryRecords(_ context.Context, filter db.MemorySearchFilter) ([]db.MemoryRecord, error) {
	s.seenLimits = append(s.seenLimits, filter.Limit)
	if filter.Limit > 0 && filter.Limit < len(s.records) {
		return s.records[:filter.Limit], nil
	}
	return s.records, nil
}

// The boost must be able to pull a project memory INTO the page, not merely
// reorder a page it was already in. Ranked 6th globally with a limit of 5, the
// project memory is outside the page until the candidate pool is widened.
func TestSearchWidensCandidatePoolSoAffinityCanPromote(t *testing.T) {
	records := []db.MemoryRecord{
		affinityRecord("other-1", "project:other"),
		affinityRecord("other-2", "project:other"),
		affinityRecord("other-3", "project:other"),
		affinityRecord("other-4", "project:other"),
		affinityRecord("other-5", "project:other"),
		affinityRecord("mine", "project:mine"),
	}
	store := &affinityStore{records: records}

	out, err := Search(context.Background(), store, nil, SearchInput{
		Query:     "anything",
		ProjectID: "project:mine",
		Limit:     5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Matches) != 5 {
		t.Fatalf("expected the caller's limit honored, got %d", len(out.Matches))
	}
	if !slices.Contains(affinityIDs(out.Matches), "mine") {
		t.Fatalf("project memory ranked just outside the page was never promoted: %v", affinityIDs(out.Matches))
	}
	for _, limit := range store.seenLimits {
		if limit <= 5 {
			t.Fatalf("expected a widened candidate fetch, store saw limit %d", limit)
		}
	}
}

func TestSearchWithoutProjectDoesNotOverfetch(t *testing.T) {
	store := &affinityStore{records: []db.MemoryRecord{affinityRecord("a", "project:other")}}
	if _, err := Search(context.Background(), store, nil, SearchInput{Query: "anything", Limit: 5}); err != nil {
		t.Fatal(err)
	}
	for _, limit := range store.seenLimits {
		if limit != 5 {
			t.Fatalf("expected the plain limit without a project boost, got %d", limit)
		}
	}
}

// An empty result must not claim a semantic lookup happened — that is the
// signal an agent uses to decide a memory does not exist.
func TestSearchReportsNoneWhenNothingMatched(t *testing.T) {
	store := &affinityStore{}
	out, err := Search(context.Background(), store, nil, SearchInput{Query: "nothing here", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Matches) != 0 {
		t.Fatalf("expected no matches, got %d", len(out.Matches))
	}
	if out.Mode != "none" {
		t.Fatalf("expected mode \"none\" for an empty result, got %q", out.Mode)
	}
}
