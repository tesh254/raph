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
	"raph/internal/knowledge"
	"raph/internal/memory"
	"raph/internal/query"
	"raph/internal/verbose"

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

type StoreRuleArgs struct {
	Scope    string   `json:"scope" jsonschema:"Rule scope: global (affects all work) or project (this codebase)"`
	Content  string   `json:"content" jsonschema:"The rule the agent must follow"`
	Title    string   `json:"title,omitempty" jsonschema:"Short rule title"`
	Tags     []string `json:"tags,omitempty" jsonschema:"Optional tags"`
	Key      string   `json:"key,omitempty" jsonschema:"Stable rule key; defaults to a slug of title/content"`
	WriterID string   `json:"writer_id,omitempty" jsonschema:"Stable identifier for the writer"`
}

type ListRulesArgs struct {
	Scope string `json:"scope" jsonschema:"Rule scope: global or project"`
	Query string `json:"query,omitempty" jsonschema:"Optional text filter"`
	Limit int    `json:"limit,omitempty" jsonschema:"Maximum rules to return"`
}

type AddDocumentArgs struct {
	Scope    string   `json:"scope,omitempty" jsonschema:"Scope: project (this codebase) or global. Defaults to project"`
	Title    string   `json:"title,omitempty" jsonschema:"Document title"`
	Content  string   `json:"content" jsonschema:"Full document text"`
	DocType  string   `json:"doc_type,omitempty" jsonschema:"architecture, handoff, reference, or note"`
	Source   string   `json:"source,omitempty" jsonschema:"Origin such as user, web, or agent"`
	Tags     []string `json:"tags,omitempty" jsonschema:"Optional tags"`
	Links    []string `json:"links,omitempty" jsonschema:"Node ids to relate this document to"`
	Key      string   `json:"key,omitempty" jsonschema:"Stable key; defaults to a slug of the title"`
	WriterID string   `json:"writer_id,omitempty" jsonschema:"Stable identifier for the writer"`
}

type ListDocumentsArgs struct {
	Scope   string `json:"scope,omitempty" jsonschema:"Scope: project or global"`
	DocType string `json:"doc_type,omitempty" jsonschema:"Filter by doc type"`
	Status  string `json:"status,omitempty" jsonschema:"Filter by status: fresh, stale, used"`
	Query   string `json:"query,omitempty" jsonschema:"Optional text filter"`
	Limit   int    `json:"limit,omitempty" jsonschema:"Maximum documents"`
}

type ListDocumentsOutput struct {
	Documents []db.Node `json:"documents"`
}

type ReadDocumentArgs struct {
	ID       string `json:"id,omitempty" jsonschema:"Document node id. If omitted, provide query to resolve the document by search."`
	Query    string `json:"query,omitempty" jsonschema:"Resolve the document by text search when the id is unknown. A single match is read (as a peek); multiple matches return candidates to choose from."`
	DocType  string `json:"doc_type,omitempty" jsonschema:"Optional doc type filter for the query path, e.g. handoff"`
	Scope    string `json:"scope,omitempty" jsonschema:"Optional scope for the query path: project or global"`
	MarkUsed *bool  `json:"mark_used,omitempty" jsonschema:"Mark a handoff as used on read. Defaults to true for an id read and false for a query-resolved read (a peek). Set explicitly to override."`
	ReaderID string `json:"reader_id,omitempty" jsonschema:"Stable identifier for the reading agent"`
}

// DocCandidate is a lightweight match returned when a read_document query is
// ambiguous, so the caller can pick an exact id.
type DocCandidate struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	DocType string `json:"doc_type,omitempty"`
	Status  string `json:"status,omitempty"`
}

// ReadDocumentOutput carries either the resolved document or, when a query
// matched more than one, the candidates to disambiguate.
type ReadDocumentOutput struct {
	Document   *knowledge.Document `json:"document,omitempty"`
	Candidates []DocCandidate      `json:"candidates,omitempty"`
	Resolved   string              `json:"resolved,omitempty"` // "id" or "query"
}

type LinkNodesArgs struct {
	From string `json:"from" jsonschema:"Source node id"`
	To   string `json:"to" jsonschema:"Target node id"`
	Rel  string `json:"rel,omitempty" jsonschema:"Relation type (default RELATES_TO)"`
}

type LinkNodesOutput struct {
	From string `json:"from"`
	To   string `json:"to"`
	Rel  string `json:"rel"`
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

type SearchArgs struct {
	Query  string   `json:"query" jsonschema:"The search query or pattern"`
	Mode   string   `json:"mode,omitempty" jsonschema:"Search mode: auto (ranked keyword, default), literal (exact substring), regex, or vector (semantic)"`
	Types  []string `json:"types,omitempty" jsonschema:"Filter to node types such as func, type, file, markdown_chunk, file_chunk, doc, doc_chunk"`
	Path   string   `json:"path,omitempty" jsonschema:"Workspace path to scope the search. Defaults to the server working directory"`
	Global bool     `json:"global,omitempty" jsonschema:"Search across all indexed workspaces instead of one"`
	Limit  int      `json:"limit,omitempty" jsonschema:"Maximum number of matches"`
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
	verbose.Printf("creating MCP server")
	s := mcpsdk.NewServer(&mcpsdk.Implementation{
		Name:    "raph",
		Version: "1.0.0",
	}, nil)

	wrapper := &MCPServerWrapper{server: s, store: store, config: cfg}
	wrapper.registerTools()
	verbose.Printf("MCP server ready with all tools registered")
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
		m.recordSearchHits(ctx, query, nodeIDs(output.Matches))
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
			m.recordSearchHits(ctx, query, nodeIDs(result.Matches))
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
		m.recordSearchHits(ctx, query, nodeIDs(nodes))
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
		m.recordAccess(ctx, strings.TrimSpace(args.NodeID), "neighbors", "")
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
		m.recordAccess(ctx, strings.TrimSpace(args.NodeID), "neighbors", "")
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
		m.recordAccess(ctx, strings.TrimSpace(args.NodeID), "read", "")
		output := MemoryHistoryOutput{NodeID: strings.TrimSpace(args.NodeID), Revisions: revisions}
		return textResult(renderJSON(output)), output, nil
	})

	mcpsdk.AddTool(m.server, &mcpsdk.Tool{
		Name:        "store_rule",
		Description: "Stores or updates a rule the agent must follow, scoped globally (all work) or to the current project (this codebase).",
	}, func(ctx context.Context, req *mcpsdk.CallToolRequest, args StoreRuleArgs) (*mcpsdk.CallToolResult, memory.StoreOutput, error) {
		scopeType, scopeID, err := m.resolveRuleScope(args.Scope)
		if err != nil {
			return nil, memory.StoreOutput{}, err
		}
		key := strings.TrimSpace(args.Key)
		if key == "" {
			key = slugify(firstNonEmpty(args.Title, args.Content))
		}
		writer := strings.TrimSpace(args.WriterID)
		if writer == "" {
			writer = "agent"
		}
		out, err := memory.Put(ctx, m.store, m.config, memory.StoreInput{
			ScopeType: scopeType, ScopeID: scopeID, KnowledgeType: "rule",
			Title: args.Title, Content: args.Content, Source: "agent", WriterID: writer, Tags: args.Tags, MemoryKey: key,
		})
		if err != nil {
			return nil, memory.StoreOutput{}, err
		}
		return textResult(renderJSON(out)), out, nil
	})

	mcpsdk.AddTool(m.server, &mcpsdk.Tool{
		Name:        "list_rules",
		Description: "Lists active rules for a scope. Use scope=global for rules affecting all work and scope=project for rules specific to the current codebase.",
	}, func(ctx context.Context, req *mcpsdk.CallToolRequest, args ListRulesArgs) (*mcpsdk.CallToolResult, ScopedMemorySearchOutput, error) {
		scopeType, scopeID, err := m.resolveRuleScope(args.Scope)
		if err != nil {
			return nil, ScopedMemorySearchOutput{}, err
		}
		out, err := m.searchKnowledge(ctx, scopeType, scopeID, "rule", args.Query, args.Limit)
		if err != nil {
			return nil, ScopedMemorySearchOutput{}, err
		}
		return textResult(renderJSON(out)), out, nil
	})

	mcpsdk.AddTool(m.server, &mcpsdk.Tool{
		Name:        "add_document",
		Description: "Attaches a local document to the graph. Set doc_type to architecture (durable design), handoff (work transfer), reference (a fact to confirm against), or note. Chunked and linked so related material is one hop away.",
	}, func(ctx context.Context, req *mcpsdk.CallToolRequest, args AddDocumentArgs) (*mcpsdk.CallToolResult, knowledge.Document, error) {
		workspace, err := m.resolveDocWorkspace(args.Scope)
		if err != nil {
			return nil, knowledge.Document{}, err
		}
		writer := strings.TrimSpace(args.WriterID)
		if writer == "" {
			writer = "agent"
		}
		doc, err := knowledge.Add(ctx, m.store, m.config, knowledge.AddInput{
			Workspace: workspace, Key: args.Key, Title: args.Title, Content: args.Content,
			DocType: args.DocType, Source: firstNonEmpty(args.Source, "agent"), WriterID: writer, Tags: args.Tags, Links: args.Links,
		})
		if err != nil {
			return nil, knowledge.Document{}, err
		}
		return textResult(renderJSON(doc)), doc, nil
	})

	mcpsdk.AddTool(m.server, &mcpsdk.Tool{
		Name:        "list_documents",
		Description: "Lists local documents in a scope, optionally filtered by doc_type (architecture, handoff, reference, note) or status (fresh, stale, used).",
	}, func(ctx context.Context, req *mcpsdk.CallToolRequest, args ListDocumentsArgs) (*mcpsdk.CallToolResult, ListDocumentsOutput, error) {
		workspace, err := m.resolveDocWorkspace(args.Scope)
		if err != nil {
			return nil, ListDocumentsOutput{}, err
		}
		docs, err := knowledge.List(ctx, m.store, knowledge.ListFilter{
			Workspace: workspace, DocType: args.DocType, Status: args.Status, Query: args.Query, Limit: args.Limit,
		})
		if err != nil {
			return nil, ListDocumentsOutput{}, err
		}
		out := ListDocumentsOutput{Documents: docs}
		return textResult(renderJSON(out)), out, nil
	})

	mcpsdk.AddTool(m.server, &mcpsdk.Tool{
		Name:        "read_document",
		Description: "Reads a document with its chunks and related nodes. Provide id for a direct read, or query to resolve by search (a single match is read; multiple matches return candidates to pick from). Reading a handoff by id marks it used so the next agent knows the work is taken; a query-resolved read is a peek by default. Pass mark_used to override.",
	}, func(ctx context.Context, req *mcpsdk.CallToolRequest, args ReadDocumentArgs) (*mcpsdk.CallToolResult, ReadDocumentOutput, error) {
		reader := strings.TrimSpace(args.ReaderID)
		if reader == "" {
			reader = "agent"
		}

		id := strings.TrimSpace(args.ID)
		resolvedByQuery := false

		// No id: resolve by search. A single unambiguous match is read; multiple
		// matches return candidates without reading or claiming any (so a fuzzy
		// query can't silently mark the wrong handoff used).
		if id == "" {
			query := strings.TrimSpace(args.Query)
			if query == "" {
				return nil, ReadDocumentOutput{}, fmt.Errorf("either id or query is required")
			}
			workspace := ""
			if scope := strings.TrimSpace(args.Scope); scope != "" {
				ws, err := m.resolveDocWorkspace(scope)
				if err != nil {
					return nil, ReadDocumentOutput{}, err
				}
				workspace = ws
			}
			matches, err := knowledge.List(ctx, m.store, knowledge.ListFilter{
				Workspace: workspace,
				DocType:   strings.TrimSpace(args.DocType),
				Query:     query,
				Limit:     10,
			})
			if err != nil {
				return nil, ReadDocumentOutput{}, err
			}
			if len(matches) == 0 {
				return nil, ReadDocumentOutput{}, fmt.Errorf("no documents match query %q", query)
			}
			if len(matches) > 1 {
				out := ReadDocumentOutput{Resolved: "query"}
				for _, n := range matches {
					out.Candidates = append(out.Candidates, DocCandidate{
						ID: n.ID, Title: n.Name, DocType: n.Prop("doc_type"), Status: n.Prop("status"),
					})
				}
				return textResult(renderJSON(out)), out, nil
			}
			id = matches[0].ID
			resolvedByQuery = true
		}

		// Claim on an explicit id read; peek when resolved via query. An explicit
		// mark_used always wins.
		markUsed := true
		if args.MarkUsed != nil {
			markUsed = *args.MarkUsed
		} else if resolvedByQuery {
			markUsed = false
		}

		doc, err := knowledge.Read(ctx, m.store, id, markUsed, reader)
		if err != nil {
			return nil, ReadDocumentOutput{}, err
		}
		m.recordAccess(ctx, id, "read", "")
		resolved := "id"
		if resolvedByQuery {
			resolved = "query"
		}
		out := ReadDocumentOutput{Document: &doc, Resolved: resolved}
		return textResult(renderJSON(out)), out, nil
	})

	mcpsdk.AddTool(m.server, &mcpsdk.Tool{
		Name:        "link_nodes",
		Description: "Creates a relation edge between two graph nodes so related material can be reached via graph_neighbors instead of another search.",
	}, func(ctx context.Context, req *mcpsdk.CallToolRequest, args LinkNodesArgs) (*mcpsdk.CallToolResult, LinkNodesOutput, error) {
		if err := knowledge.Link(ctx, m.store, args.From, args.To, args.Rel); err != nil {
			return nil, LinkNodesOutput{}, err
		}
		rel := strings.TrimSpace(args.Rel)
		if rel == "" {
			rel = knowledge.RelRelatesTo
		}
		out := LinkNodesOutput{From: args.From, To: args.To, Rel: rel}
		return textResult(renderJSON(out)), out, nil
	})

	mcpsdk.AddTool(m.server, &mcpsdk.Tool{
		Name:        "crawl_url",
		Description: "Fetches exactly one user-provided HTTP or HTTPS page, extracts readable content, creates chunks, and generates embeddings.",
	}, func(ctx context.Context, req *mcpsdk.CallToolRequest, args CrawlURLArgs) (*mcpsdk.CallToolResult, CrawlURLOutput, error) {
		verbose.Printf("crawl_url tool called url=%s", args.URL)
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
		stats := docCrawler.Stats()
		verbose.Printf("crawl_url complete pages=%d chunks=%d embeddings=%d", stats.PagesIndexed, stats.ChunksIndexed, stats.EmbeddingsCreated)
		output := CrawlURLOutput{URL: strings.TrimSpace(args.URL), Stats: stats}
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
		verbose.Printf("crawl_website tool called url=%s query=%q", args.URL, query)
		args.Limit = compactResultLimit(args.Limit)
		args.MaxChars = compactExcerptLimit(args.MaxChars)

		verbose.Printf("crawl_website creating crawler url=%s", args.URL)
		docCrawler, err := crawler.NewDocumentationCrawler(m.store, m.config, args.URL)
		if err != nil {
			return nil, CrawlWebsiteOutput{}, err
		}
		verbose.Printf("crawl_website running crawler")
		if err := docCrawler.Run(ctx); err != nil {
			return nil, CrawlWebsiteOutput{}, err
		}

		verbose.Printf("crawl_website searching crawled workspace query=%q", query)
		mode, nodes, err := m.searchWorkspace(ctx, docCrawler.WorkspaceID(), query, args.Limit)
		if err != nil {
			return nil, CrawlWebsiteOutput{}, err
		}
		verbose.Printf("crawl_website search complete mode=%s results=%d", mode, len(nodes))
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
		Description: "Indexes a local codebase into the graph. Defaults to the MCP server working directory and replaces that workspace's existing indexed nodes. The result's stats.scip_active lists languages resolved compiler-grade; stats.scip_suggestions lists languages that would gain go/types-level cross-file accuracy via an installable indexer — each entry has an agent_action command (e.g. `raph code-intel install python`). PROTOCOL: always ask the user for permission before running an install command; if they decline, tell them the command to run themselves and continue with the bundled resolver. Never install without explicit approval. After an approved install, re-run index_codebase to upgrade.",
	}, func(ctx context.Context, req *mcpsdk.CallToolRequest, args IndexCodebaseArgs) (*mcpsdk.CallToolResult, IndexCodebaseOutput, error) {
		path := strings.TrimSpace(args.Path)
		if path == "" {
			path = "."
		}
		verbose.Printf("index_codebase tool called path=%s noEmbeddings=%t", path, args.NoEmbeddings)
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

		verbose.Printf("index_codebase creating indexer path=%s", absPath)
		idx, err := indexer.New(m.store, m.config, absPath, args.NoEmbeddings)
		if err != nil {
			return nil, IndexCodebaseOutput{}, err
		}
		verbose.Printf("index_codebase running indexer workspace=%s", idx.WorkspaceID())
		stats, err := idx.Run(ctx)
		if err != nil {
			return nil, IndexCodebaseOutput{}, err
		}
		verbose.Printf("index_codebase complete files=%d nodes=%d edges=%d embeddings=%d", stats.FilesIndexed, stats.NodesSaved, stats.EdgesSaved, stats.EmbeddingsCreated)
		output := IndexCodebaseOutput{Path: absPath, WorkspaceID: idx.WorkspaceID(), Stats: stats}
		return textResult(renderJSON(output)), output, nil
	})

	mcpsdk.AddTool(m.server, &mcpsdk.Tool{
		Name:        "search",
		Description: "Search over the indexed graph (code, docs, knowledge) with familiar CLI ergonomics for agents that do not have this MCP server connected. Modes: auto (ranked keyword), literal (exact substring), regex (Go regexp), vector (semantic graph search). Filter by node type; scope to the current workspace or all.",
	}, func(ctx context.Context, req *mcpsdk.CallToolRequest, args SearchArgs) (*mcpsdk.CallToolResult, query.Result, error) {
		workspace := ""
		if !args.Global {
			ws, err := m.resolveWorkspace(args.Path)
			if err != nil {
				return nil, query.Result{}, err
			}
			workspace = ws
		}
		mode := query.Mode(strings.TrimSpace(args.Mode))
		result, err := query.Search(ctx, m.store, m.config, query.Options{
			Query:     args.Query,
			Workspace: workspace,
			Types:     args.Types,
			Limit:     args.Limit,
			Mode:      mode,
		})
		if err != nil {
			return nil, query.Result{}, err
		}
		hits := make([]string, 0, len(result.Matches))
		for _, match := range result.Matches {
			hits = append(hits, match.ID)
		}
		m.recordSearchHits(ctx, args.Query, hits)
		return textResult(renderJSON(result)), result, nil
	})

	mcpsdk.AddTool(m.server, &mcpsdk.Tool{
		Name:        "search_codebase",
		Description: "Searches indexed codebase nodes with explicit file, symbol, and chunk result groups.",
	}, func(ctx context.Context, req *mcpsdk.CallToolRequest, args SearchCodebaseArgs) (*mcpsdk.CallToolResult, SearchCodebaseOutput, error) {
		output, err := m.searchCodebase(ctx, args)
		if err != nil {
			return nil, SearchCodebaseOutput{}, err
		}
		hits := nodeIDs(output.Symbols)
		hits = append(hits, nodeIDs(output.Files)...)
		hits = append(hits, nodeIDs(output.Chunks)...)
		m.recordSearchHits(ctx, args.Query, hits)
		return textResult(renderJSON(output)), output, nil
	})
}

func (m *MCPServerWrapper) hybridSearch(ctx context.Context, query string, limit int) (SearchOutput, error) {
	limit = searchLimit(limit)
	output := SearchOutput{Mode: "keyword"}
	verbose.Printf("hybrid search query=%q limit=%d", query, limit)

	if m.config != nil && m.config.HasEmbeddingProvider() {
		verbose.Printf("generating query embedding for semantic search")
		vec, err := config.GenerateEmbedding(ctx, m.config, query)
		if err == nil && len(vec) > 0 {
			nodes, searchErr := m.store.VectorSearch(ctx, vec, limit)
			if searchErr == nil && len(nodes) > 0 {
				output.Mode = "semantic"
				output.Matches = nodes
				verbose.Printf("semantic search returned %d results", len(nodes))
				return output, nil
			}
		}
		verbose.Printf("semantic search unavailable, falling back to keyword search")
	}

	nodes, err := m.store.KeywordSearch(ctx, query, limit)
	if err != nil {
		return output, err
	}
	output.Matches = nodes
	verbose.Printf("keyword search returned %d results", len(nodes))
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

// resolveDocWorkspace maps a document scope to a workspace id: the current
// project's workspace, or the shared global-knowledge bucket.
func (m *MCPServerWrapper) resolveDocWorkspace(scope string) (string, error) {
	switch strings.TrimSpace(scope) {
	case "", "project":
		return m.resolveWorkspace(".")
	case "global":
		return knowledge.GlobalWorkspace, nil
	default:
		return "", fmt.Errorf("unknown scope %q (use project or global)", scope)
	}
}

// resolveRuleScope maps a rule scope keyword to a (scopeType, scopeID) pair.
// global rules share a fixed bucket; project rules use the workspace identity.
func (m *MCPServerWrapper) resolveRuleScope(scope string) (string, string, error) {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		scope = "project"
	}
	switch scope {
	case "global":
		return "global", "global", nil
	case "project":
		id, err := m.resolveScopeID("project", "")
		if err != nil {
			return "", "", err
		}
		return "project", id, nil
	default:
		return "", "", fmt.Errorf("unknown rule scope %q (use global or project)", scope)
	}
}

// slugify produces a stable key from free text.
func slugify(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash && b.Len() > 0 {
				b.WriteRune('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "entry"
	}
	if len(out) > 60 {
		out = strings.Trim(out[:60], "-")
	}
	return out
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
	m.recordSearchHits(ctx, query, memoryNodeIDs(output.Matches))
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

// recordAccess logs an access event when the store supports it (the local
// SQLite store does). Best-effort telemetry for the studio analytics view.
func (m *MCPServerWrapper) recordAccess(ctx context.Context, nodeID, kind, query string) {
	if rec, ok := m.store.(interface {
		RecordAccess(context.Context, string, string, string) error
	}); ok {
		_ = rec.RecordAccess(ctx, nodeID, kind, query)
	}
}

// maxRecordedHits caps per-search node attribution so one broad query doesn't
// swamp the access log.
const maxRecordedHits = 10

// recordSearchHits attributes a search and the nodes it surfaced. Every node
// returned to an agent counts as touched — that is what the studio's
// "most accessed" view measures.
func (m *MCPServerWrapper) recordSearchHits(ctx context.Context, query string, nodeIDs []string) {
	if q := strings.TrimSpace(query); q != "" {
		m.recordAccess(ctx, "", "search", q)
	}
	recorded := 0
	for _, id := range nodeIDs {
		if strings.TrimSpace(id) == "" {
			continue
		}
		m.recordAccess(ctx, id, "hit", "")
		recorded++
		if recorded == maxRecordedHits {
			return
		}
	}
}

func nodeIDs(nodes []db.Node) []string {
	ids := make([]string, 0, len(nodes))
	for _, n := range nodes {
		ids = append(ids, n.ID)
	}
	return ids
}

func memoryNodeIDs(records []db.MemoryRecord) []string {
	ids := make([]string, 0, len(records))
	for _, r := range records {
		ids = append(ids, r.Node.ID)
	}
	return ids
}

// resolveWorkspace maps a path (default: server working directory) to a graph
// workspace id for scoped searches.
func (m *MCPServerWrapper) resolveWorkspace(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		path = "."
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve workspace path: %w", err)
	}
	idx, err := indexer.New(m.store, m.config, absPath, true)
	if err != nil {
		return "", err
	}
	return idx.WorkspaceID(), nil
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
