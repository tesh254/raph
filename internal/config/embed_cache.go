package config

import (
	"container/list"
	"context"
	"sync"
)

// queryEmbedCacheCap bounds how many distinct query embeddings are kept. Query
// vectors are ~1536 float32 (~6KB) each, so a few hundred entries is a small,
// fixed memory cost that comfortably covers a working set of repeated searches.
const queryEmbedCacheCap = 256

// embedCache is a tiny thread-safe LRU over query text → embedding vector.
type embedCache struct {
	mu    sync.Mutex
	cap   int
	ll    *list.List
	items map[string]*list.Element
}

type embedEntry struct {
	key string
	vec []float32
}

func newEmbedCache(capacity int) *embedCache {
	return &embedCache{cap: capacity, ll: list.New(), items: map[string]*list.Element{}}
}

func (c *embedCache) get(key string) ([]float32, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		return nil, false
	}
	c.ll.MoveToFront(el)
	// Return a copy so a caller can't mutate the cached vector out from under
	// the next lookup.
	stored := el.Value.(*embedEntry).vec
	out := make([]float32, len(stored))
	copy(out, stored)
	return out, true
}

func (c *embedCache) put(key string, vec []float32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		c.ll.MoveToFront(el)
		el.Value.(*embedEntry).vec = vec
		return
	}
	c.items[key] = c.ll.PushFront(&embedEntry{key: key, vec: vec})
	for c.ll.Len() > c.cap {
		back := c.ll.Back()
		if back == nil {
			break
		}
		c.ll.Remove(back)
		delete(c.items, back.Value.(*embedEntry).key)
	}
}

var queryEmbedCache = newEmbedCache(queryEmbedCacheCap)

// EmbedQuery returns the embedding for a search query, reusing a recently
// computed vector when the same text (for the same provider+model) was embedded
// before. Query embedding is the one network round trip on the hot search path
// — and it runs serially up to 20 times inside multi_query_search — so caching
// it removes that latency for any repeated query. Content embeddings (indexing,
// storing memories, crawling) deliberately stay on GenerateEmbedding: their
// text is unique per call, so caching it would only churn this LRU.
//
// The cache key folds in the provider, endpoint, and model, so rotating the
// model — or pointing at a different OpenRouter-compatible base URL — never
// returns a vector produced by a different embedder.
func EmbedQuery(ctx context.Context, cfg *Config, query string) ([]float32, error) {
	if cfg == nil {
		// Preserve GenerateEmbedding's nil-config error path.
		return GenerateEmbedding(ctx, cfg, query)
	}
	key := queryCacheKey(cfg, query)
	if vec, ok := queryEmbedCache.get(key); ok {
		return vec, nil
	}
	vec, err := GenerateEmbedding(ctx, cfg, query)
	if err != nil {
		return nil, err
	}
	queryEmbedCache.put(key, vec)
	// Hand back a copy: the stored slice must stay private to the cache.
	out := make([]float32, len(vec))
	copy(out, vec)
	return out, nil
}

func queryCacheKey(cfg *Config, query string) string {
	provider := cfg.Vector.CurrentProvider
	model := ""
	baseURL := ""
	if provider == "openrouter" {
		model = cfg.Vector.Providers.OpenRouter.Model
		baseURL = cfg.Vector.Providers.OpenRouter.BaseURL
	}
	return provider + "\x00" + baseURL + "\x00" + model + "\x00" + query
}
