package memory

import (
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
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
	Limit         int
}

type StoreOutput struct {
	Record   db.MemoryRecord `json:"record"`
	Embedded bool            `json:"embedded"`
}

type SearchOutput struct {
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

func Search(ctx context.Context, store db.GraphStore, input SearchInput) (SearchOutput, error) {
	matches, err := store.SearchMemoryRecords(ctx, db.MemorySearchFilter{
		Query:           input.Query,
		ScopeType:       input.ScopeType,
		ScopeID:         input.ScopeID,
		KnowledgeType:   input.KnowledgeType,
		LifecycleStates: []string{lifecycleActive},
		Limit:           input.Limit,
	})
	if err != nil {
		return SearchOutput{}, err
	}
	return SearchOutput{Matches: matches}, nil
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
