package memory

import (
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"raph/internal/config"
	"raph/internal/db"
)

const (
	lifecycleActive     = "active"
	lifecycleDeprecated = "deprecated"
	lifecycleReplaced   = "replaced"
)

type StoreInput struct {
	ScopeType     string
	ScopeID       string
	KnowledgeType string
	Title         string
	Content       string
	Source        string
	WriterID      string
	Tags          []string
	MemoryKey     string
}

type UpdateInput struct {
	ScopeType     string
	ScopeID       string
	KnowledgeType string
	Title         string
	Content       string
	Source        string
	WriterID      string
	Tags          []string
	MemoryKey     string
}

type DeprecateInput struct {
	NodeID            string
	ReplacementNodeID string
	WriterID          string
	Reason            string
}

type SearchInput struct {
	Query         string
	ScopeType     string
	ScopeID       string
	KnowledgeType string
	// ProjectID biases ranking toward memories belonging to one project
	// without filtering anything out. Empty means no bias.
	ProjectID string
	Limit     int
}

type StoreOutput struct {
	Record   db.MemoryRecord `json:"record"`
	Embedded bool            `json:"embedded"`
}

type SearchOutput struct {
	Mode    string            `json:"mode"` // "semantic" or "keyword"
	Matches []db.MemoryRecord `json:"matches"`
}

func Store(ctx context.Context, store db.GraphStore, cfg *config.Config, input StoreInput) (StoreOutput, error) {
	prepared, err := prepareInput(input.ScopeType, input.ScopeID, input.KnowledgeType, input.Title, input.Content, input.Source, input.WriterID, input.Tags, input.MemoryKey)
	if err != nil {
		return StoreOutput{}, err
	}

	if _, err := store.GetMemoryRecordByKey(ctx, prepared.ScopeType, prepared.ScopeID, prepared.KnowledgeType, prepared.MemoryKey); err == nil {
		return StoreOutput{}, fmt.Errorf("memory already exists for scope=%s scope_id=%s knowledge_type=%s memory_key=%s", prepared.ScopeType, prepared.ScopeID, prepared.KnowledgeType, prepared.MemoryKey)
	} else if err != nil && err != sql.ErrNoRows {
		return StoreOutput{}, fmt.Errorf("load existing memory: %w", err)
	}

	now := nowUTC()
	record, embedded, err := upsertRecord(ctx, store, cfg, db.MemoryRecord{
		Node: db.Node{
			ID:        memoryNodeID(prepared.ScopeType, prepared.ScopeID, prepared.KnowledgeType, prepared.MemoryKey),
			Workspace: memoryWorkspace(prepared.ScopeType, prepared.ScopeID),
			Domain:    "memory",
			Type:      "memory",
			Name:      prepared.Title,
			Content:   prepared.Content,
			URL:       memoryURL(prepared.ScopeType, prepared.ScopeID, prepared.KnowledgeType, prepared.MemoryKey),
		},
		MemoryKey:      prepared.MemoryKey,
		ScopeType:      prepared.ScopeType,
		ScopeID:        prepared.ScopeID,
		LifecycleState: lifecycleActive,
		KnowledgeType:  prepared.KnowledgeType,
		Source:         prepared.Source,
		WriterID:       prepared.WriterID,
		CreatedAt:      now,
		UpdatedAt:      now,
		NormalizedTags: prepared.NormalizedTags,
		DisplayTags:    prepared.DisplayTags,
		Revision:       1,
	})
	if err != nil {
		return StoreOutput{}, err
	}
	return StoreOutput{Record: record, Embedded: embedded}, nil
}

// Put creates a memory if absent, or updates it if one already exists for the
// same scope/knowledge/key. It gives CLI and agents idempotent write semantics.
func Put(ctx context.Context, store db.GraphStore, cfg *config.Config, input StoreInput) (StoreOutput, error) {
	prepared, err := prepareInput(input.ScopeType, input.ScopeID, input.KnowledgeType, input.Title, input.Content, input.Source, input.WriterID, input.Tags, input.MemoryKey)
	if err != nil {
		return StoreOutput{}, err
	}
	_, err = store.GetMemoryRecordByKey(ctx, prepared.ScopeType, prepared.ScopeID, prepared.KnowledgeType, prepared.MemoryKey)
	if err == nil {
		return Update(ctx, store, cfg, UpdateInput(input))
	}
	if err != sql.ErrNoRows {
		return StoreOutput{}, fmt.Errorf("load existing memory: %w", err)
	}
	return Store(ctx, store, cfg, input)
}

func Update(ctx context.Context, store db.GraphStore, cfg *config.Config, input UpdateInput) (StoreOutput, error) {
	prepared, err := prepareInput(input.ScopeType, input.ScopeID, input.KnowledgeType, input.Title, input.Content, input.Source, input.WriterID, input.Tags, input.MemoryKey)
	if err != nil {
		return StoreOutput{}, err
	}

	existing, err := store.GetMemoryRecordByKey(ctx, prepared.ScopeType, prepared.ScopeID, prepared.KnowledgeType, prepared.MemoryKey)
	if err != nil {
		if err == sql.ErrNoRows {
			return StoreOutput{}, fmt.Errorf("memory not found for scope=%s scope_id=%s knowledge_type=%s memory_key=%s", prepared.ScopeType, prepared.ScopeID, prepared.KnowledgeType, prepared.MemoryKey)
		}
		return StoreOutput{}, fmt.Errorf("load memory: %w", err)
	}

	// Apply the new field values; the revision bump and history append are done
	// atomically inside commitMemory from the current stored state.
	existing.Node.Name = prepared.Title
	existing.Node.Content = prepared.Content
	existing.Source = prepared.Source
	existing.WriterID = prepared.WriterID
	existing.NormalizedTags = prepared.NormalizedTags
	existing.DisplayTags = prepared.DisplayTags
	existing.UpdatedAt = nowUTC()
	existing.LifecycleState = lifecycleActive
	existing.ReplacedByNodeID = ""
	existing.DeprecatedMessage = ""

	record, embedded, err := upsertRecord(ctx, store, cfg, existing)
	if err != nil {
		return StoreOutput{}, err
	}
	return StoreOutput{Record: record, Embedded: embedded}, nil
}

func Deprecate(ctx context.Context, store db.GraphStore, input DeprecateInput) (db.MemoryRecord, error) {
	nodeID := strings.TrimSpace(input.NodeID)
	if nodeID == "" {
		return db.MemoryRecord{}, fmt.Errorf("node_id is required")
	}

	record, err := store.GetMemoryRecord(ctx, nodeID)
	if err != nil {
		return db.MemoryRecord{}, err
	}
	state := lifecycleDeprecated
	replacement := strings.TrimSpace(input.ReplacementNodeID)
	if replacement != "" {
		state = lifecycleReplaced
	}
	record.LifecycleState = state
	record.ReplacedByNodeID = replacement
	record.DeprecatedMessage = strings.TrimSpace(input.Reason)
	record.UpdatedAt = nowUTC()
	// Lifecycle change only: the revision bump and history append happen atomically
	// inside commitMemory; the content node is unchanged so it isn't re-saved.
	if _, err := commitMemory(ctx, store, record, false); err != nil {
		return db.MemoryRecord{}, fmt.Errorf("update lifecycle: %w", err)
	}
	return store.GetMemoryRecord(ctx, record.Node.ID)
}

func Search(ctx context.Context, store db.GraphStore, cfg *config.Config, input SearchInput) (SearchOutput, error) {
	limit := input.Limit
	if limit <= 0 {
		limit = 10
	}
	projectID := strings.TrimSpace(input.ProjectID)

	// Both passes fetch beyond the caller's limit when a project boost applies.
	// The boost re-ranks what was retrieved, so a project memory that sits just
	// outside the page could never be lifted into it — the candidate pool has to
	// be deep enough for the boost to reach. Widening by the boost's own size is
	// exactly that depth: no memory the boost could promote is left unfetched.
	candidateLimit := limit
	if projectID != "" {
		candidateLimit = limit + affinityBoostPositions
	}

	filter := db.MemorySearchFilter{
		ScopeType:       strings.TrimSpace(input.ScopeType),
		ScopeID:         strings.TrimSpace(input.ScopeID),
		KnowledgeType:   strings.TrimSpace(input.KnowledgeType),
		LifecycleStates: []string{lifecycleActive},
		Limit:           candidateLimit,
	}
	query := strings.TrimSpace(input.Query)

	// Semantic pass: rank active memories by meaning, so agents recall by intent
	// rather than exact wording. It embeds the query via the configured provider
	// — a network call — so it's bounded by a short timeout and is entirely
	// optional: no provider, an unembeddable/offline query, or a timeout just
	// leaves this empty. A read never hangs on the provider.
	var semantic []db.MemoryRecord
	if query != "" && cfg != nil && cfg.HasEmbeddingProvider() {
		embedCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		vec, err := config.EmbedQuery(embedCtx, cfg, query)
		cancel()
		if err == nil && len(vec) > 0 {
			matches, err := store.VectorSearchMemoryRecords(ctx, vec, filter)
			if err != nil {
				return SearchOutput{}, err
			}
			semantic = matches
		}
	}

	// Keyword pass ALWAYS runs and is merged in — not just used as a fallback
	// when semantic finds nothing. Otherwise a memory the agent literally named
	// (or one just written whose exact terms the vectors rank below older,
	// similar entries) would be crowded out of a full semantic result set and
	// look "missing". Over the small memory table this second query is cheap.
	filter.Query = query
	keyword, err := store.SearchMemoryRecords(ctx, filter)
	if err != nil {
		return SearchOutput{}, err
	}

	// Merge over the widened pool, re-rank, and only then cut to the page the
	// caller asked for.
	matches, mode := mergeMemoryMatches(semantic, keyword, candidateLimit)
	matches = applyProjectAffinity(matches, projectID)
	if len(matches) > limit {
		matches = matches[:limit]
	}
	if len(matches) == 0 {
		// Reporting a pass that found nothing (or never ran, when no embedding
		// provider is configured) as the mode tells an agent the lookup was
		// semantic when it wasn't — the signal it uses to decide a memory
		// doesn't exist.
		mode = "none"
	}
	return SearchOutput{Mode: mode, Matches: matches}, nil
}

// affinityBoostPositions is what belonging to the caller's project is worth,
// measured in rank positions. It is deliberately small: a boost, not a tier.
// A memory from this project climbs a few places, so it beats a comparably
// ranked memory from elsewhere — but a global preference or a shared decision
// that the query matched far more strongly still wins. Making this large enough
// to always float project memories to the top would reintroduce, as an ordering
// bias, exactly the scope filter this replaced.
const affinityBoostPositions = 3

// applyProjectAffinity re-ranks merged matches so memories scoped to projectID
// move up by affinityBoostPositions. It never drops a record: the caller asked
// for the best matches across every scope, and a project-local memory is a
// better default answer only when relevance is close.
func applyProjectAffinity(records []db.MemoryRecord, projectID string) []db.MemoryRecord {
	if projectID == "" || len(records) < 2 {
		return records
	}

	type ranked struct {
		record db.MemoryRecord
		score  float64
	}
	scored := make([]ranked, 0, len(records))
	boosted := false
	for i, record := range records {
		score := float64(i)
		if record.ScopeID == projectID {
			score -= affinityBoostPositions
			boosted = true
		}
		scored = append(scored, ranked{record: record, score: score})
	}
	if !boosted {
		return records
	}

	// Stable so records that tie after the boost keep their merged relevance
	// order, which is the only signal distinguishing them.
	sort.SliceStable(scored, func(a, b int) bool { return scored[a].score < scored[b].score })

	out := make([]db.MemoryRecord, 0, len(scored))
	for _, r := range scored {
		out = append(out, r.record)
	}
	return out
}

// mergeMemoryMatches unions the semantic and keyword result sets, de-duplicated
// by node id. Semantic hits keep their (by-meaning) ordering and lead. The
// catch it guards against: a keyword-only hit — an exact-term or just-written
// memory the vector ranking buried — must not be crowded out just because the
// semantic pass already filled every slot. So it RESERVES up to a third of the
// result slots for keyword-only hits before truncating, then fills the rest
// (and any reserve the keyword pass didn't use) with semantic results. The mode
// reports which passes actually contributed.
func mergeMemoryMatches(semantic, keyword []db.MemoryRecord, limit int) ([]db.MemoryRecord, string) {
	if limit <= 0 {
		limit = 10
	}
	inSemantic := make(map[string]struct{}, len(semantic))
	for _, r := range semantic {
		inSemantic[r.Node.ID] = struct{}{}
	}
	keywordOnly := make([]db.MemoryRecord, 0, len(keyword))
	for _, r := range keyword {
		if _, ok := inSemantic[r.Node.ID]; !ok {
			keywordOnly = append(keywordOnly, r)
		}
	}

	// Reserve slots for keyword-only hits (capped at how many there are), so a
	// full semantic result set still leaves room for them.
	reserve := 0
	if len(keywordOnly) > 0 {
		reserve = (limit + 2) / 3 // ceil(limit/3)
		if reserve > len(keywordOnly) {
			reserve = len(keywordOnly)
		}
	}
	semanticSlots := limit - reserve

	seen := make(map[string]struct{}, limit)
	out := make([]db.MemoryRecord, 0, limit)
	take := func(r db.MemoryRecord, cap int) bool {
		if len(out) >= cap {
			return false
		}
		if _, ok := seen[r.Node.ID]; ok {
			return false
		}
		seen[r.Node.ID] = struct{}{}
		out = append(out, r)
		return true
	}

	usedSemantic := false
	for _, r := range semantic {
		if take(r, semanticSlots) {
			usedSemantic = true
		}
	}
	usedKeyword := false
	for _, r := range keywordOnly {
		if take(r, limit) {
			usedKeyword = true
		}
	}
	// Top up any slots the keyword reserve didn't consume with leftover semantic.
	for _, r := range semantic {
		if take(r, limit) {
			usedSemantic = true
		}
	}

	switch {
	case usedSemantic && usedKeyword:
		return out, "hybrid"
	case usedKeyword:
		return out, "keyword"
	default:
		return out, "semantic"
	}
}

func History(ctx context.Context, store db.GraphStore, nodeID string) ([]db.MemoryRevision, error) {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return nil, fmt.Errorf("node_id is required")
	}
	return store.ListMemoryRevisions(ctx, nodeID)
}

type preparedInput struct {
	ScopeType      string
	ScopeID        string
	KnowledgeType  string
	Title          string
	Content        string
	Source         string
	WriterID       string
	MemoryKey      string
	NormalizedTags []string
	DisplayTags    []string
}

func prepareInput(scopeType string, scopeID string, knowledgeType string, title string, content string, source string, writerID string, tags []string, memoryKey string) (preparedInput, error) {
	scopeType = strings.TrimSpace(scopeType)
	scopeID = strings.TrimSpace(scopeID)
	knowledgeType = strings.TrimSpace(knowledgeType)
	title = strings.TrimSpace(title)
	content = strings.TrimSpace(content)
	source = strings.TrimSpace(source)
	writerID = strings.TrimSpace(writerID)
	memoryKey = strings.TrimSpace(memoryKey)
	if scopeType == "" {
		return preparedInput{}, fmt.Errorf("scope_type is required")
	}
	if scopeID == "" {
		return preparedInput{}, fmt.Errorf("scope_id is required")
	}
	if knowledgeType == "" {
		return preparedInput{}, fmt.Errorf("knowledge_type is required")
	}
	if content == "" {
		return preparedInput{}, fmt.Errorf("content is required")
	}
	if title == "" {
		title = preview(content, 80)
	}
	if source == "" {
		return preparedInput{}, fmt.Errorf("source is required")
	}
	if writerID == "" {
		return preparedInput{}, fmt.Errorf("writer_id is required")
	}
	if memoryKey == "" {
		return preparedInput{}, fmt.Errorf("memory_key is required")
	}
	normalizedTags, displayTags := normalizeTags(tags)
	return preparedInput{
		ScopeType:      scopeType,
		ScopeID:        scopeID,
		KnowledgeType:  knowledgeType,
		Title:          title,
		Content:        content,
		Source:         source,
		WriterID:       writerID,
		MemoryKey:      memoryKey,
		NormalizedTags: normalizedTags,
		DisplayTags:    displayTags,
	}, nil
}

// memoryCommitter is the optional store capability that persists a memory write
// in a single transaction, assigning the revision under the write lock. Stores
// without it (e.g. test mocks) fall back to sequential, non-atomic writes.
type memoryCommitter interface {
	CommitMemoryRecord(ctx context.Context, record db.MemoryRecord, saveNode bool) (db.MemoryRecord, error)
}

// commitMemory persists a memory write, returning the record with its assigned
// revision. The caller supplies the desired end state (fields + node identity);
// the revision read-modify-write and revision-history append are decided here
// from the CURRENT stored state, not from a value the caller read earlier — so
// concurrent writers can't both land the same revision. With a transactional
// store this happens atomically inside one write-locked transaction; the
// fallback (test mocks) does the same steps sequentially.
func commitMemory(ctx context.Context, store db.GraphStore, record db.MemoryRecord, saveNode bool) (db.MemoryRecord, error) {
	if c, ok := store.(memoryCommitter); ok {
		return c.CommitMemoryRecord(ctx, record, saveNode)
	}
	current, cerr := store.GetMemoryRecord(ctx, record.Node.ID)
	switch {
	case cerr == nil:
		if err := store.InsertMemoryRevision(ctx, revisionSnapshot(current)); err != nil {
			return db.MemoryRecord{}, fmt.Errorf("save memory revision: %w", err)
		}
		record.Revision = current.Revision + 1
		record.CreatedAt = current.CreatedAt
	case cerr == sql.ErrNoRows:
		record.Revision = 1
	default:
		return db.MemoryRecord{}, fmt.Errorf("load current memory: %w", cerr)
	}
	if saveNode {
		if err := store.SaveNode(ctx, record.Node); err != nil {
			return db.MemoryRecord{}, fmt.Errorf("save memory node: %w", err)
		}
	}
	if err := store.UpsertMemoryRecord(ctx, record); err != nil {
		return db.MemoryRecord{}, fmt.Errorf("save memory metadata: %w", err)
	}
	return record, nil
}

// revisionSnapshot captures a record's current state as a history row.
func revisionSnapshot(r db.MemoryRecord) db.MemoryRevision {
	return db.MemoryRevision{
		NodeID:           r.Node.ID,
		Revision:         r.Revision,
		Title:            r.Node.Name,
		Content:          r.Node.Content,
		Source:           r.Source,
		WriterID:         r.WriterID,
		LifecycleState:   r.LifecycleState,
		NormalizedTags:   r.NormalizedTags,
		DisplayTags:      r.DisplayTags,
		CreatedAt:        r.UpdatedAt,
		DeprecatedReason: r.DeprecatedMessage,
	}
}

// upsertRecord embeds (outside any transaction — it's a network call) then
// commits the node + record atomically, returning the record with its assigned
// revision.
func upsertRecord(ctx context.Context, store db.GraphStore, cfg *config.Config, record db.MemoryRecord) (db.MemoryRecord, bool, error) {
	if cfg != nil && cfg.HasEmbeddingProvider() {
		embedding, err := config.GenerateEmbedding(ctx, cfg, record.Node.Name+"\n\n"+record.Node.Content)
		if err != nil {
			return db.MemoryRecord{}, false, fmt.Errorf("generate memory embedding: %w", err)
		}
		record.Node.Embedding = embedding
		record.Node.EmbeddingLength = len(embedding)
	}
	embeddingLength := record.Node.EmbeddingLength
	committed, err := commitMemory(ctx, store, record, true)
	if err != nil {
		return db.MemoryRecord{}, false, err
	}
	committed.Node.Embedding = nil
	return committed, embeddingLength > 0, nil
}

func normalizeTags(tags []string) ([]string, []string) {
	normalizedSet := make(map[string]string)
	displaySet := make(map[string]struct{})
	for _, tag := range tags {
		display := strings.TrimSpace(tag)
		if display == "" {
			continue
		}
		displaySet[display] = struct{}{}
		normalized := strings.ToLower(display)
		normalizedSet[normalized] = display
	}
	normalized := make([]string, 0, len(normalizedSet))
	for value := range normalizedSet {
		normalized = append(normalized, value)
	}
	sort.Strings(normalized)
	display := make([]string, 0, len(displaySet))
	for value := range displaySet {
		display = append(display, value)
	}
	sort.Strings(display)
	return normalized, display
}

func memoryNodeID(scopeType string, scopeID string, knowledgeType string, memoryKey string) string {
	sum := sha1.Sum([]byte(scopeType + "|" + scopeID + "|" + knowledgeType + "|" + memoryKey))
	return "memory:" + hex.EncodeToString(sum[:])
}

func memoryWorkspace(scopeType string, scopeID string) string {
	return "memory:" + scopeType + ":" + scopeID
}

func memoryURL(scopeType string, scopeID string, knowledgeType string, memoryKey string) string {
	return "memory://" + scopeType + "/" + scopeID + "/" + knowledgeType + "/" + memoryKey
}

func nowUTC() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func preview(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "..."
}

// memoryRelocator is the store capability a scope migration needs: re-keying a
// memory node while carrying its record and revision history.
type memoryRelocator interface {
	RelocateMemory(ctx context.Context, oldID, newID, newWorkspace, newURL, newScopeID string) error
}

// ScopeMigration reports what moving one project scope did.
type ScopeMigration struct {
	Moved    int `json:"moved"`
	Conflict int `json:"conflict"`
}

// MigrateProjectScope moves every active memory from oldScopeID to newScopeID.
//
// A memory's node id, workspace, and url are all derived from its scope, so
// changing the scope means re-keying the node — not just rewriting a column.
// Leaving the id alone would make the next write for that key mint a duplicate
// instead of updating the record.
//
// Memories whose key is already taken under the new scope are counted and left
// where they are: the destination was written deliberately and must win over a
// migration.
func MigrateProjectScope(ctx context.Context, store db.GraphStore, oldScopeID, newScopeID string) (ScopeMigration, error) {
	var stats ScopeMigration
	oldScopeID = strings.TrimSpace(oldScopeID)
	newScopeID = strings.TrimSpace(newScopeID)
	if oldScopeID == "" || newScopeID == "" || oldScopeID == newScopeID {
		return stats, nil
	}
	relocator, ok := store.(memoryRelocator)
	if !ok {
		return stats, fmt.Errorf("store cannot relocate memories")
	}

	for {
		records, err := store.SearchMemoryRecords(ctx, db.MemorySearchFilter{
			ScopeType: scopeProject,
			ScopeID:   oldScopeID,
			Limit:     scopeMigrationPageSize,
		})
		if err != nil {
			return stats, fmt.Errorf("list memories in %s: %w", oldScopeID, err)
		}
		if len(records) == 0 {
			return stats, nil
		}

		progressed := false
		for _, record := range records {
			if err := ctx.Err(); err != nil {
				return stats, err
			}
			newID := memoryNodeID(record.ScopeType, newScopeID, record.KnowledgeType, record.MemoryKey)
			err := relocator.RelocateMemory(ctx, record.Node.ID, newID,
				memoryWorkspace(record.ScopeType, newScopeID),
				memoryURL(record.ScopeType, newScopeID, record.KnowledgeType, record.MemoryKey),
				newScopeID)
			switch {
			case err == nil:
				stats.Moved++
				progressed = true
			case errors.Is(err, db.ErrMemoryExists):
				stats.Conflict++
			default:
				return stats, fmt.Errorf("relocate memory %s: %w", record.Node.ID, err)
			}
		}
		// Conflicts stay in the source scope, so a page made entirely of them
		// would otherwise be re-read forever.
		if !progressed {
			return stats, nil
		}
	}
}

const (
	scopeProject           = "project"
	scopeMigrationPageSize = 200
)
