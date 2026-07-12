// Package query provides a single search entry point shared by the CLI and MCP
// so agents and humans use identical ranking and filtering. It deliberately
// uses familiar CLI search ergonomics (literal/regex toggles and type/path
// filters) so agents can use raph effectively even without MCP access.
package query

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"raph/internal/config"
	"raph/internal/db"
	"raph/internal/verbose"
)

type Mode string

const (
	// ModeAuto: keyword (bm25) ranking, falling back to substring.
	ModeAuto Mode = "auto"
	// ModeLiteral: exact substring (trigram) match.
	ModeLiteral Mode = "literal"
	// ModeRegex: Go regular-expression match over candidate nodes.
	ModeRegex Mode = "regex"
	// ModeVector: semantic embedding match (requires a configured provider).
	ModeVector Mode = "vector"
)

type Options struct {
	Query     string
	Workspace string // empty = search every workspace
	Types     []string
	Limit     int
	Mode      Mode
}

type Match struct {
	ID      string            `json:"id"`
	Type    string            `json:"type"`
	Domain  string            `json:"domain"`
	Name    string            `json:"name"`
	URL     string            `json:"url,omitempty"`
	Path    string            `json:"path,omitempty"`
	Excerpt string            `json:"excerpt"`
	Props   map[string]string `json:"properties,omitempty"`
}

type Result struct {
	Query     string  `json:"query"`
	Mode      string  `json:"mode"`
	Workspace string  `json:"workspace,omitempty"`
	Count     int     `json:"count"`
	Matches   []Match `json:"matches"`
}

// Search runs the requested search mode and returns ranked, type-filtered
// matches as compact, agent-friendly records.
func Search(ctx context.Context, store db.GraphStore, cfg *config.Config, opts Options) (Result, error) {
	q := strings.TrimSpace(opts.Query)
	if q == "" {
		return Result{}, fmt.Errorf("query is required")
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 10
	}
	mode := opts.Mode
	if mode == "" {
		mode = ModeAuto
	}

	var nodes []db.Node
	var err error
	switch mode {
	case ModeVector:
		if cfg == nil || !cfg.HasEmbeddingProvider() {
			return Result{}, fmt.Errorf("vector mode requires a configured embedding provider")
		}
		vec, embedErr := config.GenerateEmbedding(ctx, cfg, q)
		if embedErr != nil {
			return Result{}, fmt.Errorf("embed query: %w", embedErr)
		}
		if opts.Workspace != "" {
			nodes, err = store.VectorSearchWorkspace(ctx, opts.Workspace, vec, candidateLimit(limit, opts.Types))
		} else {
			nodes, err = store.VectorSearch(ctx, vec, candidateLimit(limit, opts.Types))
		}
	case ModeLiteral:
		nodes, err = store.LexicalSearch(ctx, opts.Workspace, q, candidateLimit(limit, opts.Types))
	case ModeRegex:
		nodes, err = regexSearch(ctx, store, opts, q, limit)
	default: // ModeAuto
		if opts.Workspace != "" {
			nodes, err = store.KeywordSearchWorkspace(ctx, opts.Workspace, q, candidateLimit(limit, opts.Types))
		} else {
			nodes, err = store.KeywordSearch(ctx, q, candidateLimit(limit, opts.Types))
		}
	}
	if err != nil {
		return Result{}, err
	}

	nodes = filterTypes(nodes, opts.Types)
	if len(nodes) > limit {
		nodes = nodes[:limit]
	}

	result := Result{Query: q, Mode: string(mode), Workspace: opts.Workspace, Count: len(nodes)}
	for _, n := range nodes {
		result.Matches = append(result.Matches, Match{
			ID: n.ID, Type: n.Type, Domain: n.Domain, Name: n.Name, URL: n.URL, Path: n.Path,
			Excerpt: excerpt(n.Content, 240), Props: n.Properties,
		})
	}
	return result, nil
}

// regexCandidateCap bounds how many nodes regex search pulls into memory to
// scan (regex can't be pushed into SQLite/FTS). A warning is logged when it's
// hit so an incomplete result on a very large workspace isn't silent.
const regexCandidateCap = 5000

// regexSearch fetches a broad candidate set and applies a Go regexp, since
// SQLite/FTS cannot evaluate arbitrary regular expressions.
func regexSearch(ctx context.Context, store db.GraphStore, opts Options, pattern string, limit int) ([]db.Node, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid regex: %w", err)
	}
	candidates, err := store.ListNodes(ctx, db.NodeFilter{
		Workspace: opts.Workspace,
		Types:     opts.Types,
		Limit:     regexCandidateCap,
	})
	if err != nil {
		return nil, err
	}
	if len(candidates) == regexCandidateCap {
		// The candidate set was capped, so matches beyond it are not considered.
		// Surface it rather than returning a silently-incomplete result.
		verbose.Printf("regex search scanned the first %d nodes only; results may be incomplete on this workspace", regexCandidateCap)
	}
	var matched []db.Node
	for _, n := range candidates {
		if re.MatchString(n.Name) || re.MatchString(n.Content) || re.MatchString(n.URL) {
			matched = append(matched, n)
			if len(matched) >= limit {
				break
			}
		}
	}
	return matched, nil
}

func filterTypes(nodes []db.Node, types []string) []db.Node {
	set := map[string]struct{}{}
	for _, t := range types {
		t = strings.TrimSpace(t)
		if t != "" {
			set[t] = struct{}{}
		}
	}
	if len(set) == 0 {
		return nodes
	}
	out := nodes[:0]
	for _, n := range nodes {
		if _, ok := set[n.Type]; ok {
			out = append(out, n)
		}
	}
	return out
}

// candidateLimit over-fetches when type filters are present so post-filtering
// still has enough rows to satisfy the requested limit.
func candidateLimit(limit int, types []string) int {
	if len(types) == 0 {
		return limit
	}
	return limit * 5
}

// NewExcerpt returns a whitespace-collapsed, length-capped preview of content.
func NewExcerpt(content string, max int) string { return excerpt(content, max) }

func excerpt(content string, max int) string {
	content = strings.Join(strings.Fields(content), " ")
	runes := []rune(content)
	if len(runes) <= max {
		return content
	}
	return string(runes[:max]) + "..."
}

// RenderText writes compact terminal output: one match per block, location line
// then an indented excerpt. JSON remains the stable machine-readable format.
func (r Result) RenderText(sb *strings.Builder) {
	if len(r.Matches) == 0 {
		sb.WriteString(fmt.Sprintf("No matches for %q (mode=%s)\n", r.Query, r.Mode))
		return
	}
	// Group by file/url for a familiar grep-like layout.
	sort.SliceStable(r.Matches, func(i, j int) bool {
		return r.Matches[i].locator() < r.Matches[j].locator()
	})
	for _, m := range r.Matches {
		sb.WriteString(m.locator())
		sb.WriteString("  [")
		sb.WriteString(m.Type)
		sb.WriteString("]\n")
		if m.Excerpt != "" {
			sb.WriteString("    ")
			sb.WriteString(m.Excerpt)
			sb.WriteString("\n")
		}
	}
	sb.WriteString(fmt.Sprintf("\n%d match(es), mode=%s\n", r.Count, r.Mode))
}

func (m Match) locator() string {
	if strings.TrimSpace(m.URL) != "" {
		return m.URL
	}
	return m.Name
}
