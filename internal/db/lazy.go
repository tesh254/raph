package db

import (
	"context"
	"sync"
)

// LazyStore defers opening the underlying GraphStore until the first call that
// needs it. The MCP stdio server uses it so the initialize handshake is
// answered before the database is touched: opening brain.db can wait on the
// write lock while the sync worker is mid-index, and MCP clients time out
// servers that don't respond within a few seconds.
//
// Open errors are not latched — a failed open (for example a transient
// SQLITE_BUSY) surfaces on that call and the next call retries.
type LazyStore struct {
	open  func() (GraphStore, error)
	mu    sync.Mutex
	store GraphStore
}

var _ GraphStore = (*LazyStore)(nil)

func NewLazyStore(open func() (GraphStore, error)) *LazyStore {
	return &LazyStore{open: open}
}

func (l *LazyStore) ensure() (GraphStore, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.store != nil {
		return l.store, nil
	}
	store, err := l.open()
	if err != nil {
		return nil, err
	}
	l.store = store
	return store, nil
}

// Close closes the underlying store if it was ever opened. It never opens the
// database just to close it.
func (l *LazyStore) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.store == nil {
		return nil
	}
	err := l.store.Close()
	l.store = nil
	return err
}

// RecordAccess and RecordAccessBatch are optional telemetry capabilities that
// are not part of the GraphStore interface. Forward them explicitly so studio
// attribution works when the MCP server (`raph start`) runs behind this lazy
// wrapper — otherwise the wrapper wouldn't expose the methods and the callers'
// capability type-assertion would silently fail, recording no events.
func (l *LazyStore) RecordAccess(ctx context.Context, nodeID, kind, query string) error {
	s, err := l.ensure()
	if err != nil {
		return err
	}
	if rec, ok := s.(interface {
		RecordAccess(context.Context, string, string, string) error
	}); ok {
		return rec.RecordAccess(ctx, nodeID, kind, query)
	}
	return nil
}

func (l *LazyStore) RecordAccessBatch(ctx context.Context, events []AccessEvent) error {
	s, err := l.ensure()
	if err != nil {
		return err
	}
	if rec, ok := s.(interface {
		RecordAccessBatch(context.Context, []AccessEvent) error
	}); ok {
		return rec.RecordAccessBatch(ctx, events)
	}
	return nil
}

func (l *LazyStore) SaveNode(ctx context.Context, node Node) error {
	store, err := l.ensure()
	if err != nil {
		return err
	}
	return store.SaveNode(ctx, node)
}

func (l *LazyStore) SaveEdge(ctx context.Context, edge Edge) error {
	store, err := l.ensure()
	if err != nil {
		return err
	}
	return store.SaveEdge(ctx, edge)
}

func (l *LazyStore) VectorSearch(ctx context.Context, embedding []float32, limit int) ([]Node, error) {
	store, err := l.ensure()
	if err != nil {
		return nil, err
	}
	return store.VectorSearch(ctx, embedding, limit)
}

func (l *LazyStore) VectorSearchWorkspace(ctx context.Context, workspace string, embedding []float32, limit int) ([]Node, error) {
	store, err := l.ensure()
	if err != nil {
		return nil, err
	}
	return store.VectorSearchWorkspace(ctx, workspace, embedding, limit)
}

func (l *LazyStore) KeywordSearch(ctx context.Context, query string, limit int) ([]Node, error) {
	store, err := l.ensure()
	if err != nil {
		return nil, err
	}
	return store.KeywordSearch(ctx, query, limit)
}

func (l *LazyStore) KeywordSearchWorkspace(ctx context.Context, workspace string, query string, limit int) ([]Node, error) {
	store, err := l.ensure()
	if err != nil {
		return nil, err
	}
	return store.KeywordSearchWorkspace(ctx, workspace, query, limit)
}

func (l *LazyStore) LexicalSearch(ctx context.Context, workspace string, query string, limit int) ([]Node, error) {
	store, err := l.ensure()
	if err != nil {
		return nil, err
	}
	return store.LexicalSearch(ctx, workspace, query, limit)
}

func (l *LazyStore) ListNodes(ctx context.Context, filter NodeFilter) ([]Node, error) {
	store, err := l.ensure()
	if err != nil {
		return nil, err
	}
	return store.ListNodes(ctx, filter)
}

func (l *LazyStore) SetNodeProperties(ctx context.Context, id string, props map[string]string) error {
	store, err := l.ensure()
	if err != nil {
		return err
	}
	return store.SetNodeProperties(ctx, id, props)
}

func (l *LazyStore) GetNodeByID(ctx context.Context, id string) (Node, error) {
	store, err := l.ensure()
	if err != nil {
		return Node{}, err
	}
	return store.GetNodeByID(ctx, id)
}

func (l *LazyStore) GetNeighbors(ctx context.Context, nodeID string) ([]Node, []Edge, error) {
	store, err := l.ensure()
	if err != nil {
		return nil, nil, err
	}
	return store.GetNeighbors(ctx, nodeID)
}

func (l *LazyStore) GetAllGraphElements(ctx context.Context) ([]Node, []Edge, error) {
	store, err := l.ensure()
	if err != nil {
		return nil, nil, err
	}
	return store.GetAllGraphElements(ctx)
}

func (l *LazyStore) UpsertMemoryRecord(ctx context.Context, record MemoryRecord) error {
	store, err := l.ensure()
	if err != nil {
		return err
	}
	return store.UpsertMemoryRecord(ctx, record)
}

func (l *LazyStore) GetMemoryRecord(ctx context.Context, nodeID string) (MemoryRecord, error) {
	store, err := l.ensure()
	if err != nil {
		return MemoryRecord{}, err
	}
	return store.GetMemoryRecord(ctx, nodeID)
}

func (l *LazyStore) GetMemoryRecordByKey(ctx context.Context, scopeType string, scopeID string, knowledgeType string, memoryKey string) (MemoryRecord, error) {
	store, err := l.ensure()
	if err != nil {
		return MemoryRecord{}, err
	}
	return store.GetMemoryRecordByKey(ctx, scopeType, scopeID, knowledgeType, memoryKey)
}

func (l *LazyStore) InsertMemoryRevision(ctx context.Context, revision MemoryRevision) error {
	store, err := l.ensure()
	if err != nil {
		return err
	}
	return store.InsertMemoryRevision(ctx, revision)
}

func (l *LazyStore) ListMemoryRevisions(ctx context.Context, nodeID string) ([]MemoryRevision, error) {
	store, err := l.ensure()
	if err != nil {
		return nil, err
	}
	return store.ListMemoryRevisions(ctx, nodeID)
}

func (l *LazyStore) SearchMemoryRecords(ctx context.Context, filter MemorySearchFilter) ([]MemoryRecord, error) {
	store, err := l.ensure()
	if err != nil {
		return nil, err
	}
	return store.SearchMemoryRecords(ctx, filter)
}

func (l *LazyStore) VectorSearchMemoryRecords(ctx context.Context, embedding []float32, filter MemorySearchFilter) ([]MemoryRecord, error) {
	store, err := l.ensure()
	if err != nil {
		return nil, err
	}
	return store.VectorSearchMemoryRecords(ctx, embedding, filter)
}

func (l *LazyStore) SetMemoryLifecycle(ctx context.Context, nodeID string, lifecycleState string, replacedByNodeID string, deprecatedMessage string) error {
	store, err := l.ensure()
	if err != nil {
		return err
	}
	return store.SetMemoryLifecycle(ctx, nodeID, lifecycleState, replacedByNodeID, deprecatedMessage)
}

func (l *LazyStore) SaveWebCorpus(ctx context.Context, corpus WebCorpus) error {
	store, err := l.ensure()
	if err != nil {
		return err
	}
	return store.SaveWebCorpus(ctx, corpus)
}

func (l *LazyStore) SaveWebCrawlVersion(ctx context.Context, version WebCrawlVersion) error {
	store, err := l.ensure()
	if err != nil {
		return err
	}
	return store.SaveWebCrawlVersion(ctx, version)
}

func (l *LazyStore) DeleteNodeByID(ctx context.Context, id string) error {
	store, err := l.ensure()
	if err != nil {
		return err
	}
	return store.DeleteNodeByID(ctx, id)
}

func (l *LazyStore) DeleteFileNodes(ctx context.Context, workspace string, relativePath string) error {
	store, err := l.ensure()
	if err != nil {
		return err
	}
	return store.DeleteFileNodes(ctx, workspace, relativePath)
}

func (l *LazyStore) DeleteWorkspace(ctx context.Context, workspace string) error {
	store, err := l.ensure()
	if err != nil {
		return err
	}
	return store.DeleteWorkspace(ctx, workspace)
}

func (l *LazyStore) ClearAll(ctx context.Context) error {
	store, err := l.ensure()
	if err != nil {
		return err
	}
	return store.ClearAll(ctx)
}
