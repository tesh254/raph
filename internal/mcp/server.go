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
	ScopeType     string   `json:"scope_type" jsonschema:"Memory scope type such as project, shared, or global"`
	ScopeID       string   `json:"scope_id,omitempty" jsonschema:"Stable scope identifier. Project scope can infer this from the current workspace when omitted"`
	KnowledgeType string   `json:"knowledge_type" jsonschema:"Knowledge category such as decision, workflow, preference, or incident"`
	Title         string   `json:"title" jsonschema:"Short descriptive title"`
	Content       string   `json:"content" jsonschema:"Durable information to remember"`
	Source        string   `json:"source" jsonschema:"Origin of the memory, such as conversation, docs, or user"`
	WriterID      string   `json:"writer_id" jsonschema:"Stable identifier for the writer updating this memory"`
	Tags          []string `json:"tags,omitempty" jsonschema:"Optional tags for retrieval and display"`
	MemoryKey     string   `json:"memory_key" jsonschema:"Stable key within the immutable scope and knowledge type"`
}

type UpdateMemoryArgs struct {
	ScopeType     string   `json:"scope_type" jsonschema:"Immutable memory scope type"`
	ScopeID       string   `json:"scope_id,omitempty" jsonschema:"Immutable scope identifier. Project scope can infer this from the current workspace when omitted"`
	KnowledgeType string   `json:"knowledge_type" jsonschema:"Immutable knowledge category"`
	Title         string   `json:"title" jsonschema:"Updated short descriptive title"`
	Content       string   `json:"content" jsonschema:"Updated durable information"`
	Source        string   `json:"source" jsonschema:"Origin of the update"`
	WriterID      string   `json:"writer_id" jsonschema:"Stable identifier for the writer updating this memory"`
	Tags          []string `json:"tags,omitempty" jsonschema:"Optional replacement tag set"`
	MemoryKey     string   `json:"memory_key" jsonschema:"Stable immutable key used to locate the memory"`
}

type DeprecateMemoryArgs struct {
	NodeID            string `json:"node_id" jsonschema:"The memory node ID to deprecate"`
	ReplacementNodeID string `json:"replacement_node_id,omitempty" jsonschema:"Optional replacement memory node ID"`
	WriterID          string `json:"writer_id,omitempty" jsonschema:"Stable identifier for the writer performing the deprecation"`
	Reason            string `json:"reason,omitempty" jsonschema:"Why this memory was deprecated or replaced"`
}

type SearchKnowledgeArgs struct {
	Query         string `json:"query" jsonschema:"The search query"`
	KnowledgeType string `json:"knowledge_type,omitempty" jsonschema:"Optional knowledge type filter"`
	ScopeID       string `json:"scope_id,omitempty" jsonschema:"Required for shared knowledge lookups"`
	Limit         int    `json:"limit,omitempty" jsonschema:"Maximum result count"`
}

type GetMemoryHistoryArgs struct {
	NodeID string `json:"node_id" jsonschema:"The memory node ID whose revision history should be returned"`
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

type SearchCodebaseArgs struct {
	Query        string   `json:"query" jsonschema:"The code search query"`
	Path         string   `json:"path,omitempty" jsonschema:"Indexed codebase directory. Defaults to the MCP server working directory."`
	IncludePaths []string `json:"include_paths,omitempty" jsonschema:"Only include matches whose file path contains one of these substrings"`
	ExcludePaths []string `json:"exclude_paths,omitempty" jsonschema:"Exclude matches whose file path contains one of these substrings"`
	NodeTypes    []string `json:"node_types,omitempty" jsonschema:"Filter to specific node types such as file, func, type, markdown_chunk, or file_chunk"`
	Limit        int      `json:"limit,omitempty" jsonschema:"Maximum result count"`
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

type ScopedMemorySearchOutput struct {
	ScopeType string            `json:"scope_type"`
	ScopeID   string            `json:"scope_id"`
	Matches   []db.MemoryRecord `json:"matches"`
}

type MemoryHistoryOutput struct {
	NodeID    string              `json:"node_id"`
	Revisions []db.MemoryRevision `json:"revisions"`
}

type SearchCodebaseOutput struct {
	Path       string    `json:"path"`
	Workspace  string    `json:"workspace_id"`
	Mode       string    `json:"mode"`
	Symbols    []db.Node `json:"symbols"`
	Files      []db.Node `json:"files"`
	Chunks     []db.Node `json:"chunks"`
	Unassigned []db.Node `json:"unassigned,omitempty"`
}

type CrossCorpusNeighborOutput struct {
	QueryNodeID string    `json:"query_node_id"`
	Mode        string    `json:"mode"`
	Matches     []db.Node `json:"matches"`
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
		Description: "Returns structural neighboring nodes and edges for a given graph node.",
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
		Name:        "graph_neighbors_cross_corpus",
		Description: "Performs explicit semantic expansion from one node into similar nodes from other corpora or workspaces.",
	}, func(ctx context.Context, req *mcpsdk.CallToolRequest, args struct {
		NodeID string `json:"node_id"`
		Limit  int    `json:"limit,omitempty"`
	}) (*mcpsdk.CallToolResult, CrossCorpusNeighborOutput, error) {
		output, err := m.crossCorpusNeighbors(ctx, args.NodeID, args.Limit)
		if err != nil {
			return nil, CrossCorpusNeighborOutput{}, err
		}
		return textResult(renderJSON(output)), output, nil
	})

	mcpsdk.AddTool(m.server, &mcpsdk.Tool{
		Name:        "store_memory",
		Description: "Stores a new scoped memory record. Project scope can infer scope_id from the current workspace.",
	}, func(ctx context.Context, req *mcpsdk.CallToolRequest, args StoreMemoryArgs) (*mcpsdk.CallToolResult, memory.StoreOutput, error) {
		scopeID, err := m.resolveScopeID(args.ScopeType, args.ScopeID)
		if err != nil {
			return nil, memory.StoreOutput{}, err
		}
		output, err := memory.Store(ctx, m.store, m.config, memory.StoreInput{
			ScopeType:     args.ScopeType,
			ScopeID:       scopeID,
			KnowledgeType: args.KnowledgeType,
			Title:         args.Title,
			Content:       args.Content,
			Source:        args.Source,
			WriterID:      args.WriterID,
			Tags:          args.Tags,
			MemoryKey:     args.MemoryKey,
		})
		if err != nil {
			return nil, memory.StoreOutput{}, err
		}
		return textResult(renderJSON(output)), output, nil
	})

	mcpsdk.AddTool(m.server, &mcpsdk.Tool{
		Name:        "update_memory",
		Description: "Updates the current contents of an existing scoped memory record and saves the previous version to revision history.",
	}, func(ctx context.Context, req *mcpsdk.CallToolRequest, args UpdateMemoryArgs) (*mcpsdk.CallToolResult, memory.StoreOutput, error) {
		scopeID, err := m.resolveScopeID(args.ScopeType, args.ScopeID)
		if err != nil {
			return nil, memory.StoreOutput{}, err
		}
		output, err := memory.Update(ctx, m.store, m.config, memory.UpdateInput{
			ScopeType:     args.ScopeType,
			ScopeID:       scopeID,
			KnowledgeType: args.KnowledgeType,
			Title:         args.Title,
			Content:       args.Content,
			Source:        args.Source,
			WriterID:      args.WriterID,
			Tags:          args.Tags,
			MemoryKey:     args.MemoryKey,
		})
		if err != nil {
			return nil, memory.StoreOutput{}, err
		}
		return textResult(renderJSON(output)), output, nil
	})

	mcpsdk.AddTool(m.server, &mcpsdk.Tool{
		Name:        "deprecate_memory",
		Description: "Marks a memory as deprecated or replaced while preserving its revision history.",
	}, func(ctx context.Context, req *mcpsdk.CallToolRequest, args DeprecateMemoryArgs) (*mcpsdk.CallToolResult, db.MemoryRecord, error) {
		output, err := memory.Deprecate(ctx, m.store, memory.DeprecateInput{
			NodeID:            args.NodeID,
			ReplacementNodeID: args.ReplacementNodeID,
			WriterID:          args.WriterID,
			Reason:            args.Reason,
		})
		if err != nil {
			return nil, db.MemoryRecord{}, err
		}
		return textResult(renderJSON(output)), output, nil
	})

	mcpsdk.AddTool(m.server, &mcpsdk.Tool{
		Name:        "search_project_knowledge",
		Description: "Searches active project-scoped knowledge for the current workspace's project identity.",
	}, func(ctx context.Context, req *mcpsdk.CallToolRequest, args SearchKnowledgeArgs) (*mcpsdk.CallToolResult, ScopedMemorySearchOutput, error) {
		scopeID, err := m.resolveScopeID("project", "")
		if err != nil {
			return nil, ScopedMemorySearchOutput{}, err
		}
		output, err := m.searchKnowledge(ctx, "project", scopeID, args.KnowledgeType, args.Query, args.Limit)
		if err != nil {
			return nil, ScopedMemorySearchOutput{}, err
		}
		return textResult(renderJSON(output)), output, nil
	})

	mcpsdk.AddTool(m.server, &mcpsdk.Tool{
		Name:        "search_shared_knowledge",
		Description: "Searches active shared knowledge for an explicit shared scope.",
	}, func(ctx context.Context, req *mcpsdk.CallToolRequest, args SearchKnowledgeArgs) (*mcpsdk.CallToolResult, ScopedMemorySearchOutput, error) {
		scopeID := strings.TrimSpace(args.ScopeID)
		if scopeID == "" {
			return nil, ScopedMemorySearchOutput{}, fmt.Errorf("scope_id is required for shared knowledge")
		}
		output, err := m.searchKnowledge(ctx, "shared", scopeID, args.KnowledgeType, args.Query, args.Limit)
		if err != nil {
			return nil, ScopedMemorySearchOutput{}, err
		}
		return textResult(renderJSON(output)), output, nil
	})

	mcpsdk.AddTool(m.server, &mcpsdk.Tool{
		Name:        "search_global_preferences",
		Description: "Searches active global preference memories.",
	}, func(ctx context.Context, req *mcpsdk.CallToolRequest, args SearchKnowledgeArgs) (*mcpsdk.CallToolResult, ScopedMemorySearchOutput, error) {
		output, err := m.searchKnowledge(ctx, "global", "preferences", "preference", args.Query, args.Limit)
		if err != nil {
			return nil, ScopedMemorySearchOutput{}, err
		}
		return textResult(renderJSON(output)), output, nil
	})

	mcpsdk.AddTool(m.server, &mcpsdk.Tool{
		Name:        "get_memory_history",
		Description: "Returns revision history for a memory record.",
	}, func(ctx context.Context, req *mcpsdk.CallToolRequest, args GetMemoryHistoryArgs) (*mcpsdk.CallToolResult, MemoryHistoryOutput, error) {
		revisions, err := memory.History(ctx, m.store, args.NodeID)
		if err != nil {
			return nil, MemoryHistoryOutput{}, err
		}
		output := MemoryHistoryOutput{NodeID: strings.TrimSpace(args.NodeID), Revisions: revisions}
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

	mcpsdk.AddTool(m.server, &mcpsdk.Tool{
		Name:        "search_codebase",
		Description: "Searches indexed codebase nodes with explicit file, symbol, and chunk result groups.",
	}, func(ctx context.Context, req *mcpsdk.CallToolRequest, args SearchCodebaseArgs) (*mcpsdk.CallToolResult, SearchCodebaseOutput, error) {
		output, err := m.searchCodebase(ctx, args)
		if err != nil {
			return nil, SearchCodebaseOutput{}, err
		}
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

func (m *MCPServerWrapper) resolveScopeID(scopeType string, provided string) (string, error) {
	scopeType = strings.TrimSpace(scopeType)
	provided = strings.TrimSpace(provided)
	if scopeType == "" {
		return "", fmt.Errorf("scope_type is required")
	}
	if provided != "" {
		return provided, nil
	}
	if scopeType != "project" {
		return "", fmt.Errorf("scope_id is required for %s scope", scopeType)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve current workspace: %w", err)
	}
	projectID, err := indexer.ResolveProjectIdentity(m.config, cwd)
	if err != nil {
		return "", err
	}
	return projectID, nil
}

func (m *MCPServerWrapper) searchKnowledge(ctx context.Context, scopeType string, scopeID string, knowledgeType string, query string, limit int) (ScopedMemorySearchOutput, error) {
	output, err := memory.Search(ctx, m.store, memory.SearchInput{
		Query:         query,
		ScopeType:     scopeType,
		ScopeID:       scopeID,
		KnowledgeType: knowledgeType,
		Limit:         searchLimit(limit),
	})
	if err != nil {
		return ScopedMemorySearchOutput{}, err
	}
	return ScopedMemorySearchOutput{
		ScopeType: scopeType,
		ScopeID:   scopeID,
		Matches:   output.Matches,
	}, nil
}

func (m *MCPServerWrapper) crossCorpusNeighbors(ctx context.Context, nodeID string, limit int) (CrossCorpusNeighborOutput, error) {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return CrossCorpusNeighborOutput{}, fmt.Errorf("node_id is required")
	}
	node, err := m.store.GetNodeByID(ctx, nodeID)
	if err != nil {
		return CrossCorpusNeighborOutput{}, err
	}
	output := CrossCorpusNeighborOutput{QueryNodeID: nodeID, Mode: "keyword"}
	if m.config == nil || !m.config.HasEmbeddingProvider() {
		return output, nil
	}
	vec, err := config.GenerateEmbedding(ctx, m.config, node.Name+"\n\n"+node.Content)
	if err != nil {
		return CrossCorpusNeighborOutput{}, err
	}
	matches, err := m.store.VectorSearch(ctx, vec, searchLimit(limit)*4)
	if err != nil {
		return CrossCorpusNeighborOutput{}, err
	}
	for _, match := range matches {
		if match.ID == node.ID || match.Workspace == node.Workspace {
			continue
		}
		output.Matches = append(output.Matches, match)
		if len(output.Matches) == searchLimit(limit) {
			break
		}
	}
	if len(output.Matches) > 0 {
		output.Mode = "semantic"
	}
	return output, nil
}

func (m *MCPServerWrapper) searchCodebase(ctx context.Context, args SearchCodebaseArgs) (SearchCodebaseOutput, error) {
	query := strings.TrimSpace(args.Query)
	if query == "" {
		return SearchCodebaseOutput{}, fmt.Errorf("query is required")
	}
	path := strings.TrimSpace(args.Path)
	if path == "" {
		path = "."
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return SearchCodebaseOutput{}, fmt.Errorf("resolve codebase path: %w", err)
	}
	idx, err := indexer.New(m.store, m.config, absPath, true)
	if err != nil {
		return SearchCodebaseOutput{}, err
	}
	mode, nodes, err := m.searchWorkspace(ctx, idx.WorkspaceID(), query, searchLimit(args.Limit)*5)
	if err != nil {
		return SearchCodebaseOutput{}, err
	}
	filtered := filterCodeResults(nodes, args.IncludePaths, args.ExcludePaths, args.NodeTypes)
	output := SearchCodebaseOutput{
		Path:      absPath,
		Workspace: idx.WorkspaceID(),
		Mode:      mode,
	}
	for _, node := range filtered {
		switch node.Type {
		case "func", "type":
			output.Symbols = append(output.Symbols, node)
		case "file":
			output.Files = append(output.Files, node)
		case "markdown_chunk", "file_chunk":
			output.Chunks = append(output.Chunks, node)
		default:
			output.Unassigned = append(output.Unassigned, node)
		}
	}
	trimNodeGroups(&output, searchLimit(args.Limit))
	return output, nil
}

func filterCodeResults(nodes []db.Node, includePaths []string, excludePaths []string, nodeTypes []string) []db.Node {
	typeSet := make(map[string]struct{}, len(nodeTypes))
	for _, nodeType := range nodeTypes {
		nodeType = strings.TrimSpace(nodeType)
		if nodeType != "" {
			typeSet[nodeType] = struct{}{}
		}
	}
	out := make([]db.Node, 0, len(nodes))
	for _, node := range nodes {
		if len(typeSet) > 0 {
			if _, ok := typeSet[node.Type]; !ok {
				continue
			}
		}
		nodePath := firstNonEmpty(node.URL, node.Name)
		if !matchesPathFilters(nodePath, includePaths, excludePaths) {
			continue
		}
		out = append(out, node)
	}
	return out
}

func matchesPathFilters(path string, includePaths []string, excludePaths []string) bool {
	path = strings.ToLower(path)
	if len(includePaths) > 0 {
		matched := false
		for _, include := range includePaths {
			include = strings.ToLower(strings.TrimSpace(include))
			if include != "" && strings.Contains(path, include) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	for _, exclude := range excludePaths {
		exclude = strings.ToLower(strings.TrimSpace(exclude))
		if exclude != "" && strings.Contains(path, exclude) {
			return false
		}
	}
	return true
}

func trimNodeGroups(output *SearchCodebaseOutput, limit int) {
	if limit <= 0 {
		limit = 5
	}
	if len(output.Symbols) > limit {
		output.Symbols = output.Symbols[:limit]
	}
	if len(output.Files) > limit {
		output.Files = output.Files[:limit]
	}
	if len(output.Chunks) > limit {
		output.Chunks = output.Chunks[:limit]
	}
	if len(output.Unassigned) > limit {
		output.Unassigned = output.Unassigned[:limit]
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
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
