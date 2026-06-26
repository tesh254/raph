package exporter

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"raph/internal/config"
	"raph/internal/db"
	"raph/internal/knowledge"
)

// ImportResult summarizes what an import loaded into the local graph.
type ImportResult struct {
	Kind      string `json:"kind"`
	Workspace string `json:"workspace"`
	Documents int    `json:"documents"`
	Nodes     int    `json:"nodes"`
	Edges     int    `json:"edges"`
	Skipped   int    `json:"skipped"`
}

// ParseEnvelope decodes export JSON. It is tolerant: any envelope whose version
// is not newer than this build is accepted, so older exports keep loading.
func ParseEnvelope(data []byte) (Envelope, error) {
	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return Envelope{}, fmt.Errorf("parse export json: %w", err)
	}
	if env.Version > ExportVersion {
		return Envelope{}, fmt.Errorf("export version %d is newer than this raph supports (%d); upgrade raph", env.Version, ExportVersion)
	}
	if len(env.Nodes) == 0 {
		return Envelope{}, fmt.Errorf("export contains no nodes")
	}
	return env, nil
}

// Import loads an export envelope into the local graph under the given
// workspace (empty -> the document's global scope). Documents are reconstructed
// through knowledge.Add so chunks and embeddings are regenerated locally rather
// than carried in the file; any other node types are saved verbatim. Edges are
// applied best-effort: only those whose endpoints both resolve in the new graph.
func Import(ctx context.Context, store db.GraphStore, cfg *config.Config, workspace string, data []byte, noEmbed bool) (ImportResult, error) {
	env, err := ParseEnvelope(data)
	if err != nil {
		return ImportResult{}, err
	}

	res := ImportResult{Kind: env.Kind, Workspace: workspace}
	// Map an exported node id to the id it lands on locally, so edges can be
	// remapped (a reconstructed document gets a new, workspace-derived id).
	idMap := map[string]string{}

	for _, n := range env.Nodes {
		switch n.Type {
		case knowledge.TypeDoc:
			doc, addErr := knowledge.Add(ctx, store, cfg, knowledge.AddInput{
				Workspace:  workspace,
				Key:        keyFromURL(n.URL),
				Title:      n.Name,
				Content:    n.Content,
				DocType:    n.Prop("doc_type"),
				Source:     firstNonEmpty(n.Prop("source"), "import"),
				WriterID:   n.Prop("writer_id"),
				Tags:       splitTags(n.Prop("tags")),
				Properties: n.Properties,
				NoEmbed:    noEmbed,
			})
			if addErr != nil {
				return res, fmt.Errorf("import document %q: %w", n.Name, addErr)
			}
			idMap[n.ID] = doc.Node.ID
			res.Documents++
		case knowledge.TypeDocChunk:
			// Chunks are regenerated from document content; never import them.
			res.Skipped++
		default:
			node := n
			if strings.TrimSpace(workspace) != "" {
				node.Workspace = workspace
			}
			node.Embedding = nil // exports never carry vectors
			if err := store.SaveNode(ctx, node); err != nil {
				return res, fmt.Errorf("import node %q: %w", n.ID, err)
			}
			idMap[n.ID] = node.ID
			res.Nodes++
		}
	}

	for _, e := range env.Edges {
		src, okS := idMap[e.SourceID]
		tgt, okT := idMap[e.TargetID]
		if !okS || !okT {
			res.Skipped++
			continue // endpoint not in this import; skip rather than dangle
		}
		if err := store.SaveEdge(ctx, db.Edge{SourceID: src, TargetID: tgt, Type: e.Type}); err != nil {
			return res, fmt.Errorf("import edge: %w", err)
		}
		res.Edges++
	}

	return res, nil
}

// keyFromURL recovers a document's stable key from its knowledge:// URL so a
// re-import updates the same document instead of duplicating it.
func keyFromURL(url string) string {
	const prefix = "knowledge://"
	if !strings.HasPrefix(url, prefix) {
		return ""
	}
	rest := strings.TrimPrefix(url, prefix)
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		return rest[i+1:]
	}
	return ""
}

func splitTags(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := parts[:0]
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
