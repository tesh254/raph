package exporter

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"raph/internal/config"
	"raph/internal/db"
	"raph/internal/knowledge"
	"raph/internal/memory"
)

// ImportResult summarizes what a brain import loaded into the local graph.
type ImportResult struct {
	Kind     string `json:"kind"`
	Memory   int    `json:"memory"`   // memory + rule records restored
	Handoffs int    `json:"handoffs"` // handoff documents restored
	Skipped  int    `json:"skipped"`
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
	if len(env.Memory) == 0 && len(env.Handoffs) == 0 {
		return Envelope{}, fmt.Errorf("export contains no memory, rules, or handoffs")
	}
	return env, nil
}

// Import loads a brain envelope into the local graph: memory and rules are
// restored under their original scope via memory.Put (idempotent on the
// scope/type/key natural key), and handoffs are reconstructed through
// knowledge.Add (chunks/embeddings regenerate locally). Passing cfg == nil (or
// noEmbed) skips embedding regeneration.
func Import(ctx context.Context, store db.GraphStore, cfg *config.Config, data []byte, noEmbed bool) (ImportResult, error) {
	env, err := ParseEnvelope(data)
	if err != nil {
		return ImportResult{}, err
	}
	if noEmbed {
		cfg = nil // embedding paths short-circuit on a nil config
	}

	res := ImportResult{Kind: env.Kind}

	for _, r := range env.Memory {
		if strings.TrimSpace(r.MemoryKey) == "" || strings.TrimSpace(r.ScopeType) == "" {
			res.Skipped++
			continue
		}
		if _, err := memory.Put(ctx, store, cfg, memory.StoreInput{
			ScopeType:     r.ScopeType,
			ScopeID:       r.ScopeID,
			KnowledgeType: r.KnowledgeType,
			Title:         r.Node.Name,
			Content:       r.Node.Content,
			Source:        firstNonEmpty(r.Source, "import"),
			WriterID:      firstNonEmpty(r.WriterID, "import"),
			Tags:          chooseTags(r.DisplayTags, r.NormalizedTags),
			MemoryKey:     r.MemoryKey,
		}); err != nil {
			return res, fmt.Errorf("import memory %q: %w", r.MemoryKey, err)
		}
		res.Memory++
	}

	for _, h := range env.Handoffs {
		if _, err := knowledge.Add(ctx, store, cfg, knowledge.AddInput{
			Workspace:  h.Workspace,
			Key:        keyFromURL(h.URL),
			Title:      h.Name,
			Content:    h.Content,
			DocType:    knowledge.DocHandoff,
			Source:     firstNonEmpty(h.Prop("source"), "import"),
			WriterID:   h.Prop("writer_id"),
			Tags:       splitTags(h.Prop("tags")),
			Properties: h.Properties,
			NoEmbed:    noEmbed,
		}); err != nil {
			return res, fmt.Errorf("import handoff %q: %w", h.Name, err)
		}
		res.Handoffs++
	}

	return res, nil
}

// keyFromURL recovers a document's stable key from its knowledge:// URL so a
// re-import updates the same document instead of duplicating it.
func keyFromURL(url string) string {
	const prefix = "knowledge://"
	rest, ok := strings.CutPrefix(url, prefix)
	if !ok {
		return ""
	}
	if _, key, found := strings.Cut(rest, "/"); found {
		return key
	}
	return ""
}

func chooseTags(primary, fallback []string) []string {
	if len(primary) > 0 {
		return primary
	}
	return fallback
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
