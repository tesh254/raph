// Package exporter serializes graph knowledge to portable files and publishes
// them to transferable destinations (GitHub gist/repo, S3, or Cloudflare R2) so
// knowledge can move between machines. Uploads shell out to gh/aws, keeping the
// binary dependency-light and the workflow local-first.
package exporter

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"raph/internal/db"
	"raph/internal/knowledge"
)

type Format string

const (
	FormatMarkdown Format = "md"
	FormatJSON     Format = "json"
)

// ExportVersion is the schema version of the JSON envelope. Bump it only on a
// breaking change to the envelope shape; Import tolerates this version and any
// lower one.
const ExportVersion = 1

// Envelope is the portable, plain-JSON "brain" export: the knowledge an agent
// accumulates that is NOT derivable from the codebase — scoped memory, rules,
// and handoffs. Indexed files, code symbols, and document chunks are
// deliberately excluded (they regenerate on the next index). It carries no
// binary blobs and no embedding vectors (db.Node tags Embedding `json:"-"`), so
// a file drops straight onto any raw URL or disk and reads back via `raph
// import`; embeddings regenerate locally on import.
type Envelope struct {
	Version    int               `json:"raph_export_version"`
	Kind       string            `json:"kind"` // "brain"
	ExportedAt string            `json:"exported_at,omitempty"`
	Memory     []db.MemoryRecord `json:"memory,omitempty"`   // scoped memory + rules (knowledge_type=rule)
	Handoffs   []db.Node         `json:"handoffs,omitempty"` // knowledge docs, doc_type=handoff
}

// Artifact is the in-memory result of an export before it is written/uploaded.
type Artifact struct {
	Filename string `json:"filename"`
	Content  string `json:"-"`
	Bytes    int    `json:"bytes"`
}

// marshalEnvelope renders an envelope as indented JSON.
func marshalEnvelope(env Envelope) (string, error) {
	env.Version = ExportVersion
	body, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// Document exports one document (with its chunks reconstructed) as Markdown or
// JSON.
func Document(ctx context.Context, store db.GraphStore, id string, format Format) (Artifact, error) {
	doc, err := knowledge.Read(ctx, store, id, false, "")
	if err != nil {
		return Artifact{}, err
	}
	base := slugify(doc.Node.Name)
	if format == FormatJSON {
		// Single-document export is for ad-hoc sharing/reading, not the importable
		// brain bundle; emit the document as-is.
		body, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			return Artifact{}, err
		}
		return artifact(base+".json", string(body)), nil
	}

	var sb strings.Builder
	writeFrontmatter(&sb, doc.Node)
	sb.WriteString("# ")
	sb.WriteString(doc.Node.Name)
	sb.WriteString("\n\n")
	sb.WriteString(strings.TrimSpace(doc.Node.Content))
	sb.WriteString("\n")
	if len(doc.Related) > 0 {
		sb.WriteString("\n## Related\n\n")
		for _, r := range doc.Related {
			fmt.Fprintf(&sb, "- %s (`%s`) — %s\n", r.Name, r.Type, r.ID)
		}
	}
	return artifact(base+".md", sb.String()), nil
}

// brainLimit bounds how many records/handoffs a single brain export gathers.
const brainLimit = 100_000

// Brain exports the portable agent brain — scoped memory + rules + handoffs —
// for the given memory scope types (e.g. "global", "shared"). Indexed files,
// code symbols, and document chunks are never included. Handoffs (knowledge
// docs of doc_type=handoff) are always gathered regardless of scope, since they
// are the explicit "what happened / what's next" carryover.
func Brain(ctx context.Context, store db.GraphStore, scopeTypes []string, format Format) (Artifact, error) {
	var records []db.MemoryRecord
	seen := map[string]bool{}
	for _, st := range scopeTypes {
		recs, err := store.SearchMemoryRecords(ctx, db.MemorySearchFilter{
			ScopeType:       st,
			LifecycleStates: []string{"active"},
			Limit:           brainLimit,
		})
		if err != nil {
			return Artifact{}, fmt.Errorf("list %s memory: %w", st, err)
		}
		for _, r := range recs {
			if seen[r.Node.ID] {
				continue
			}
			seen[r.Node.ID] = true
			records = append(records, r)
		}
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Node.ID < records[j].Node.ID })

	handoffs, err := store.ListNodes(ctx, db.NodeFilter{
		Domain:         knowledge.DomainKnowledge,
		Types:          []string{knowledge.TypeDoc},
		PropertyEquals: map[string]string{"doc_type": knowledge.DocHandoff},
		Limit:          brainLimit,
	})
	if err != nil {
		return Artifact{}, fmt.Errorf("list handoffs: %w", err)
	}
	sort.Slice(handoffs, func(i, j int) bool { return handoffs[i].Name < handoffs[j].Name })

	if format == FormatJSON {
		body, err := marshalEnvelope(Envelope{Kind: "brain", Memory: records, Handoffs: handoffs})
		if err != nil {
			return Artifact{}, err
		}
		return artifact("raph-brain.json", body), nil
	}

	// Markdown is a human-readable digest only (not importable).
	var sb strings.Builder
	sb.WriteString("# raph brain export\n\n")
	fmt.Fprintf(&sb, "%d memory/rule record(s)  ·  %d handoff(s)\n\n", len(records), len(handoffs))
	if len(records) > 0 {
		sb.WriteString("## Memory & rules\n\n")
		for _, r := range records {
			fmt.Fprintf(&sb, "### %s\n\n", firstNonEmpty(r.Node.Name, r.MemoryKey))
			fmt.Fprintf(&sb, "- scope: %s  ·  type: %s  ·  key: `%s`\n\n", r.ScopeType, r.KnowledgeType, r.MemoryKey)
			sb.WriteString(strings.TrimSpace(r.Node.Content))
			sb.WriteString("\n\n---\n\n")
		}
	}
	if len(handoffs) > 0 {
		sb.WriteString("## Handoffs\n\n")
		for _, h := range handoffs {
			fmt.Fprintf(&sb, "### %s\n\n", h.Name)
			sb.WriteString(strings.TrimSpace(h.Content))
			sb.WriteString("\n\n---\n\n")
		}
	}
	return artifact("raph-brain.md", sb.String()), nil
}

// Write persists an artifact to disk at outPath (or the artifact's default
// filename when outPath is empty/a directory).
func Write(a Artifact, outPath string) (string, error) {
	target := outPath
	if target == "" {
		target = a.Filename
	} else if info, err := os.Stat(outPath); err == nil && info.IsDir() {
		target = filepath.Join(outPath, a.Filename)
	}
	if err := os.WriteFile(target, []byte(a.Content), 0o644); err != nil {
		return "", fmt.Errorf("write export: %w", err)
	}
	return target, nil
}

// UploadGist publishes a file as a GitHub gist via the gh CLI and returns its
// URL. Works for files of any size that gh accepts.
func UploadGist(ctx context.Context, path string, public bool, description string) (string, error) {
	if _, err := exec.LookPath("gh"); err != nil {
		return "", fmt.Errorf("gh CLI not found; install GitHub CLI to upload gists")
	}
	args := []string{"gist", "create", path}
	if public {
		args = append(args, "--public")
	}
	if strings.TrimSpace(description) != "" {
		args = append(args, "--desc", description)
	}
	out, err := exec.CommandContext(ctx, "gh", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("gh gist create: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return lastURL(string(out)), nil
}

// UploadRepoFile commits a file into a GitHub repo path via the contents API.
func UploadRepoFile(ctx context.Context, repo, repoPath, localPath, message string) error {
	if _, err := exec.LookPath("gh"); err != nil {
		return fmt.Errorf("gh CLI not found; install GitHub CLI to upload to a repo")
	}
	content, err := os.ReadFile(localPath)
	if err != nil {
		return err
	}
	if strings.TrimSpace(message) == "" {
		message = "Add " + repoPath + " via raph export"
	}
	// gh api auto-base64s the file field via -F when prefixed appropriately; use
	// stdin JSON to stay explicit and avoid shell quoting issues.
	payload := map[string]string{
		"message": message,
		"content": base64Encode(content),
	}
	body, _ := json.Marshal(payload)
	cmd := exec.CommandContext(ctx, "gh", "api", "--method", "PUT",
		fmt.Sprintf("repos/%s/contents/%s", repo, repoPath), "--input", "-")
	cmd.Stdin = strings.NewReader(string(body))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("gh api contents: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// UploadS3 copies a file to an S3-compatible destination (s3://bucket/key). For
// Cloudflare R2 pass the account endpoint; aws CLI handles the rest.
func UploadS3(ctx context.Context, localPath, dest, endpoint string) error {
	if _, err := exec.LookPath("aws"); err != nil {
		return fmt.Errorf("aws CLI not found; install it to upload to S3/R2")
	}
	args := []string{"s3", "cp", localPath, dest}
	if strings.TrimSpace(endpoint) != "" {
		args = append(args, "--endpoint-url", endpoint)
	}
	if out, err := exec.CommandContext(ctx, "aws", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("aws s3 cp: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func base64Encode(b []byte) string {
	return base64.StdEncoding.EncodeToString(b)
}

func artifact(name, content string) Artifact {
	return Artifact{Filename: name, Content: content, Bytes: len(content)}
}

func writeFrontmatter(sb *strings.Builder, node db.Node) {
	sb.WriteString("---\n")
	fmt.Fprintf(sb, "id: %s\n", node.ID)
	keys := make([]string, 0, len(node.Properties))
	for k := range node.Properties {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(sb, "%s: %s\n", k, node.Properties[k])
	}
	sb.WriteString("---\n\n")
}

func lastURL(output string) string {
	fields := strings.Fields(output)
	for i := len(fields) - 1; i >= 0; i-- {
		if strings.HasPrefix(fields[i], "http") {
			return fields[i]
		}
	}
	return strings.TrimSpace(output)
}

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
		return "raph-export"
	}
	return out
}
