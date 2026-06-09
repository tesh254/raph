package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"raph/internal/config"
	"raph/internal/crawler"
	"raph/internal/db"
	"raph/internal/indexer"
	"raph/internal/memory"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type MCPServerWrapper struct {
	server *mcpsdk.Server
	store  db.GraphStore
	config *config.Config
}

type SemanticSearchArgs struct {
	Query string `json:"query" jsonschema:"The text search query target"`
	Limit int    `json:"limit,omitempty" jsonschema:"Maximum result count"`
}

type MultiSearchArgs struct {
	Queries []string `json:"queries" jsonschema:"Text search queries to execute in one call"`
	Limit   int      `json:"limit,omitempty" jsonschema:"Maximum result count per query"`
}

type BestVectorMatchArgs struct {
	Query string `json:"query" jsonschema:"The text query whose embedding should be matched"`
}

type NeighborArgs struct {
	NodeID string `json:"node_id" jsonschema:"The node ID to inspect"`
}

type StoreMemoryArgs struct {
	Key     string `json:"key,omitempty" jsonschema:"Stable optional key used to update the same memory later"`
	Title   string `json:"title,omitempty" jsonschema:"Short descriptive title"`
	Content string `json:"content" jsonschema:"Durable information for coding agents to remember"`
}

type DeleteMemoryArgs struct {
	NodeID string `json:"node_id" jsonschema:"The memory node ID to delete"`
}

type CrawlURLArgs struct {
	URL string `json:"url" jsonschema:"A single HTTP or HTTPS page to fetch, extract, and embed"`
}

type CrawlWebsiteArgs struct {
	URL      string `json:"url" jsonschema:"The HTTP or HTTPS website prefix to crawl"`
	Query    string `json:"query" jsonschema:"The question or information to retrieve from the website"`
	Limit    int    `json:"limit,omitempty" jsonschema:"Maximum compact result count"`
	MaxChars int    `json:"max_chars,omitempty" jsonschema:"Maximum characters per returned excerpt"`
}

type IndexCodebaseArgs struct {
	Path         string `json:"path,omitempty" jsonschema:"Codebase directory to index. Defaults to the MCP server working directory."`
	NoEmbeddings bool   `json:"no_embeddings,omitempty" jsonschema:"Skip embedding generation while indexing"`
}

type DeleteMemoryOutput struct {
	DeletedNodeID string `json:"deleted_node_id"`
}

type CrawlURLOutput struct {
	URL   string        `json:"url"`
	Stats crawler.Stats `json:"stats"`
}

type CompactMatch struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	URL     string `json:"url,omitempty"`
	Excerpt string `json:"excerpt"`
}

type CrawlWebsiteOutput struct {
	URL     string         `json:"url"`
	Query   string         `json:"query"`
	Mode    string         `json:"mode"`
	Stats   crawler.Stats  `json:"stats"`
	Matches []CompactMatch `json:"matches"`
}

type IndexCodebaseOutput struct {
	Path        string        `json:"path"`
	WorkspaceID string        `json:"workspace_id"`
	Stats       indexer.Stats `json:"stats"`
}

type SearchOutput struct {
	Mode    string    `json:"mode"`
	Matches []db.Node `json:"matches"`
}

type QuerySearchOutput struct {
	Query   string    `json:"query"`
	Mode    string    `json:"mode"`
	Matches []db.Node `json:"matches"`
}

type MultiSearchOutput struct {
	Results []QuerySearchOutput `json:"results"`
}

type BestVectorMatchOutput struct {
	Query string   `json:"query"`
	Match *db.Node `json:"match"`
}

type NeighborOutput struct {
	Nodes []db.Node `json:"nodes"`
	Edges []db.Edge `json:"edges"`
}

func NewMCPServerWrapper(store db.GraphStore, cfg *config.Config) *MCPServerWrapper {
	s := mcpsdk.NewServer(&mcpsdk.Implementation{
		Name:    "raph",
		Version: "1.0.0",
	}, nil)

	wrapper := &MCPServerWrapper{server: s, store: store, config: cfg}
	wrapper.registerTools()
	return wrapper
}

func (m *MCPServerWrapper) registerTools() {
	mcpsdk.AddTool(m.server, &mcpsdk.Tool{
		Name:        "hybrid_semantic_search",
		Description: "Queries semantic codebase components and documentation chunks using embeddings when configured, with keyword fallback.",
	}, func(ctx context.Context, req *mcpsdk.CallToolRequest, args SemanticSearchArgs) (*mcpsdk.CallToolResult, SearchOutput, error) {
		query := strings.TrimSpace(args.Query)
		if query == "" {
			return nil, SearchOutput{}, fmt.Errorf("query is required")
		}

		output, err := m.hybridSearch(ctx, query, args.Limit)
		if err != nil {
			return nil, SearchOutput{}, err
		}
		return textResult(renderJSON(output)), output, nil
	})

	mcpsdk.AddTool(m.server, &mcpsdk.Tool{
		Name:        "multi_query_search",
		Description: "Executes multiple semantic-or-keyword searches in one call and returns matches grouped by query.",
	}, func(ctx context.Context, req *mcpsdk.CallToolRequest, args MultiSearchArgs) (*mcpsdk.CallToolResult, MultiSearchOutput, error) {
		if len(args.Queries) == 0 {
			return nil, MultiSearchOutput{}, fmt.Errorf("queries must contain at least one query")
		}
		if len(args.Queries) > 20 {
			return nil, MultiSearchOutput{}, fmt.Errorf("queries cannot contain more than 20 queries")
		}

		output := MultiSearchOutput{Results: make([]QuerySearchOutput, 0, len(args.Queries))}
		for _, rawQuery := range args.Queries {
			query := strings.TrimSpace(rawQuery)
			if query == "" {
				return nil, MultiSearchOutput{}, fmt.Errorf("queries cannot contain an empty query")
			}
			result, err := m.hybridSearch(ctx, query, args.Limit)
			if err != nil {
				return nil, MultiSearchOutput{}, fmt.Errorf("search %q: %w", query, err)
			}
			output.Results = append(output.Results, QuerySearchOutput{
				Query: query, Mode: result.Mode, Matches: result.Matches,
			})
		}
		return textResult(renderJSON(output)), output, nil
	})

	mcpsdk.AddTool(m.server, &mcpsdk.Tool{
		Name:        "best_vector_match",
		Description: "Embeds one query and returns only the single closest indexed vector match. Requires a configured embedding provider.",
	}, func(ctx context.Context, req *mcpsdk.CallToolRequest, args BestVectorMatchArgs) (*mcpsdk.CallToolResult, BestVectorMatchOutput, error) {
		query := strings.TrimSpace(args.Query)
		if query == "" {
			return nil, BestVectorMatchOutput{}, fmt.Errorf("query is required")
		}
		if m.config == nil || !m.config.HasEmbeddingProvider() {
			return nil, BestVectorMatchOutput{}, fmt.Errorf("best_vector_match requires a configured embedding provider")
		}

		vec, err := config.GenerateEmbedding(ctx, m.config, query)
		if err != nil {
			return nil, BestVectorMatchOutput{}, fmt.Errorf("generate query embedding: %w", err)
		}
		nodes, err := m.store.VectorSearch(ctx, vec, 1)
		if err != nil {
			return nil, BestVectorMatchOutput{}, err
		}
		output := BestVectorMatchOutput{Query: query}
		if len(nodes) > 0 {
			output.Match = &nodes[0]
		}
		return textResult(renderJSON(output)), output, nil
	})

	mcpsdk.AddTool(m.server, &mcpsdk.Tool{
		Name:        "graph_neighbors",
		Description: "Returns neighboring nodes and edges for a given graph node.",
	}, func(ctx context.Context, req *mcpsdk.CallToolRequest, args NeighborArgs) (*mcpsdk.CallToolResult, NeighborOutput, error) {
		if strings.TrimSpace(args.NodeID) == "" {
			return nil, NeighborOutput{}, fmt.Errorf("node_id is required")
		}

		nodes, edges, err := m.store.GetNeighbors(ctx, args.NodeID)
		if err != nil {
			return nil, NeighborOutput{}, err
		}
		output := NeighborOutput{Nodes: nodes, Edges: edges}
		return textResult(renderJSON(output)), output, nil
	})

	mcpsdk.AddTool(m.server, &mcpsdk.Tool{
		Name:        "memory_store",
		Description: "Stores or updates durable agent memory. Generates an embedding when an embedding provider is configured.",
	}, func(ctx context.Context, req *mcpsdk.CallToolRequest, args StoreMemoryArgs) (*mcpsdk.CallToolResult, memory.StoreOutput, error) {
		output, err := memory.Store(ctx, m.store, m.config, memory.StoreInput{
			Key:     args.Key,
			Title:   args.Title,
			Content: args.Content,
		})
		if err != nil {
			return nil, memory.StoreOutput{}, err
		}
		return textResult(renderJSON(output)), output, nil
	})

	mcpsdk.AddTool(m.server, &mcpsdk.Tool{
		Name:        "memory_delete",
		Description: "Deletes a durable memory node by ID.",
	}, func(ctx context.Context, req *mcpsdk.CallToolRequest, args DeleteMemoryArgs) (*mcpsdk.CallToolResult, DeleteMemoryOutput, error) {
		nodeID := strings.TrimSpace(args.NodeID)
		if !strings.HasPrefix(nodeID, "memory:") {
			return nil, DeleteMemoryOutput{}, fmt.Errorf("node_id must identify a memory node")
		}
		if err := m.store.DeleteNodeByID(ctx, nodeID); err != nil {
			return nil, DeleteMemoryOutput{}, err
		}
		output := DeleteMemoryOutput{DeletedNodeID: nodeID}
		return textResult(renderJSON(output)), output, nil
	})

	mcpsdk.AddTool(m.server, &mcpsdk.Tool{
		Name:        "crawl_url",
		Description: "Fetches exactly one user-provided HTTP or HTTPS page, extracts readable content, creates chunks, and generates embeddings.",
	}, func(ctx context.Context, req *mcpsdk.CallToolRequest, args CrawlURLArgs) (*mcpsdk.CallToolResult, CrawlURLOutput, error) {
		if m.config == nil || !m.config.HasEmbeddingProvider() {
			return nil, CrawlURLOutput{}, fmt.Errorf("crawl_url requires a configured embedding provider")
		}
		docCrawler, err := crawler.NewSinglePageCrawler(m.store, m.config, args.URL)
		if err != nil {
			return nil, CrawlURLOutput{}, err
		}
		if err := docCrawler.Run(ctx); err != nil {
			return nil, CrawlURLOutput{}, err
		}
		output := CrawlURLOutput{URL: strings.TrimSpace(args.URL), Stats: docCrawler.Stats()}
		return textResult(renderJSON(output)), output, nil
	})

	mcpsdk.AddTool(m.server, &mcpsdk.Tool{
		Name:        "crawl_website",
		Description: "Crawls a website, retrieves information relevant to a question using semantic search or keyword fallback, and returns only compact excerpts.",
	}, func(ctx context.Context, req *mcpsdk.CallToolRequest, args CrawlWebsiteArgs) (*mcpsdk.CallToolResult, CrawlWebsiteOutput, error) {
		query := strings.TrimSpace(args.Query)
		if query == "" {
			return nil, CrawlWebsiteOutput{}, fmt.Errorf("query is required")
		}
		args.Limit = compactResultLimit(args.Limit)
		args.MaxChars = compactExcerptLimit(args.MaxChars)

		docCrawler, err := crawler.NewDocumentationCrawler(m.store, m.config, args.URL)
		if err != nil {
			return nil, CrawlWebsiteOutput{}, err
		}
		if err := docCrawler.Run(ctx); err != nil {
			return nil, CrawlWebsiteOutput{}, err
		}

		mode, nodes, err := m.searchWorkspace(ctx, docCrawler.WorkspaceID(), query, args.Limit)
		if err != nil {
			return nil, CrawlWebsiteOutput{}, err
		}
		output := CrawlWebsiteOutput{
			URL:     strings.TrimSpace(args.URL),
			Query:   query,
			Mode:    mode,
			Stats:   docCrawler.Stats(),
			Matches: compactMatches(nodes, args.MaxChars),
		}
		return textResult(renderJSON(output)), output, nil
	})

	mcpsdk.AddTool(m.server, &mcpsdk.Tool{
		Name:        "index_codebase",
		Description: "Indexes a local codebase into the graph. Defaults to the MCP server working directory and replaces that workspace's existing indexed nodes.",
	}, func(ctx context.Context, req *mcpsdk.CallToolRequest, args IndexCodebaseArgs) (*mcpsdk.CallToolResult, IndexCodebaseOutput, error) {
		path := strings.TrimSpace(args.Path)
		if path == "" {
			path = "."
		}
		absPath, err := filepath.Abs(path)
		if err != nil {
			return nil, IndexCodebaseOutput{}, fmt.Errorf("resolve codebase path: %w", err)
		}
		info, err := os.Stat(absPath)
		if err != nil {
			return nil, IndexCodebaseOutput{}, fmt.Errorf("inspect codebase path: %w", err)
		}
		if !info.IsDir() {
			return nil, IndexCodebaseOutput{}, fmt.Errorf("codebase path must be a directory")
		}

		idx, err := indexer.New(m.store, m.config, absPath, args.NoEmbeddings)
		if err != nil {
			return nil, IndexCodebaseOutput{}, err
		}
		stats, err := idx.Run(ctx)
		if err != nil {
			return nil, IndexCodebaseOutput{}, err
		}
		output := IndexCodebaseOutput{Path: absPath, WorkspaceID: idx.WorkspaceID(), Stats: stats}
		return textResult(renderJSON(output)), output, nil
	})
}

func (m *MCPServerWrapper) hybridSearch(ctx context.Context, query string, limit int) (SearchOutput, error) {
	limit = searchLimit(limit)
	output := SearchOutput{Mode: "keyword"}

	if m.config != nil && m.config.HasEmbeddingProvider() {
		vec, err := config.GenerateEmbedding(ctx, m.config, query)
		if err == nil && len(vec) > 0 {
			nodes, searchErr := m.store.VectorSearch(ctx, vec, limit)
			if searchErr == nil && len(nodes) > 0 {
				output.Mode = "semantic"
				output.Matches = nodes
				return output, nil
			}
		}
	}

	nodes, err := m.store.KeywordSearch(ctx, query, limit)
	if err != nil {
		return output, err
	}
	output.Matches = nodes
	return output, nil
}

func (m *MCPServerWrapper) searchWorkspace(ctx context.Context, workspace string, query string, limit int) (string, []db.Node, error) {
	if m.config != nil && m.config.HasEmbeddingProvider() {
		vec, err := config.GenerateEmbedding(ctx, m.config, query)
		if err == nil && len(vec) > 0 {
			nodes, searchErr := m.store.VectorSearchWorkspace(ctx, workspace, vec, limit)
			if searchErr == nil && len(nodes) > 0 {
				return "semantic", nodes, nil
			}
		}
	}

	nodes, err := m.store.KeywordSearchWorkspace(ctx, workspace, query, limit)
	if err != nil {
		return "keyword", nil, err
	}
	return "keyword", nodes, nil
}

func compactMatches(nodes []db.Node, maxChars int) []CompactMatch {
	matches := make([]CompactMatch, 0, len(nodes))
	seen := make(map[string]struct{}, len(nodes))
	for _, node := range nodes {
		excerpt := compactExcerpt(node.Content, maxChars)
		key := node.URL + "\x00" + excerpt
		if node.URL == "" {
			key = node.ID + "\x00" + excerpt
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		matches = append(matches, CompactMatch{
			ID:      node.ID,
			Name:    node.Name,
			URL:     node.URL,
			Excerpt: excerpt,
		})
	}
	return matches
}

func compactExcerpt(content string, maxChars int) string {
	content = strings.Join(strings.Fields(content), " ")
	runes := []rune(content)
	if len(runes) <= maxChars {
		return content
	}
	return string(runes[:maxChars]) + "..."
}

func compactResultLimit(limit int) int {
	if limit <= 0 {
		return 3
	}
	if limit > 10 {
		return 10
	}
	return limit
}

func compactExcerptLimit(limit int) int {
	if limit <= 0 {
		return 600
	}
	if limit > 2_000 {
		return 2_000
	}
	return limit
}

func searchLimit(limit int) int {
	if limit <= 0 {
		return 5
	}
	if limit > 50 {
		return 50
	}
	return limit
}

func (m *MCPServerWrapper) Run(ctx context.Context) error {
	return m.server.Run(ctx, &mcpsdk.StdioTransport{})
}

func textResult(text string) *mcpsdk.CallToolResult {
	return &mcpsdk.CallToolResult{
		Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: text}},
	}
}

func renderJSON(v any) string {
	body, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("marshal error: %v", err)
	}
	return string(body)
}
