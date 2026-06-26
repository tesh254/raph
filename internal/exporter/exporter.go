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

// Envelope is the portable, plain-JSON export format. It is intentionally free
// of binary or embedded vectors so a file can be dropped into a gist and read
// back by `raph import` with no special handling. Embeddings are omitted (the
// db.Node struct tags Embedding `json:"-"`); Import regenerates them locally
// when an embedding provider is configured.
type Envelope struct {
	Version    int       `json:"raph_export_version"`
	Kind       string    `json:"kind"` // "document" | "bundle"
	Workspace  string    `json:"workspace,omitempty"`
	ExportedAt string    `json:"exported_at,omitempty"`
	Nodes      []db.Node `json:"nodes"`
	Edges      []db.Edge `json:"edges,omitempty"`
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
		env := Envelope{Kind: "document", Workspace: doc.Node.Workspace, Nodes: []db.Node{doc.Node}}
		for _, r := range doc.Related {
			env.Edges = append(env.Edges, db.Edge{SourceID: doc.Node.ID, TargetID: r.ID, Type: knowledge.RelRelatesTo})
		}
		body, err := marshalEnvelope(env)
		if err != nil {
			return Artifact{}, err
		}
		return artifact(base+".json", body), nil
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

// Bundle exports every document in a workspace plus an index, as a single file.
func Bundle(ctx context.Context, store db.GraphStore, workspace string, format Format) (Artifact, error) {
	docs, err := store.ListNodes(ctx, db.NodeFilter{
		Workspace: workspace,
		Types:     []string{knowledge.TypeDoc},
		Limit:     1000,
	})
	if err != nil {
		return Artifact{}, err
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].Name < docs[j].Name })

	if format == FormatJSON {
		body, err := marshalEnvelope(Envelope{Kind: "bundle", Workspace: workspace, Nodes: docs})
		if err != nil {
			return Artifact{}, err
		}
		return artifact("raph-knowledge-bundle.json", body), nil
	}

	var sb strings.Builder
	sb.WriteString("# raph knowledge bundle\n\n")
	fmt.Fprintf(&sb, "Workspace: `%s`  ·  %d documents\n\n", workspace, len(docs))
	for _, d := range docs {
		fmt.Fprintf(&sb, "## %s\n\n", d.Name)
		fmt.Fprintf(&sb, "- type: %s\n- status: %s\n- id: `%s`\n\n", d.Prop("doc_type"), d.Prop("status"), d.ID)
		sb.WriteString(strings.TrimSpace(d.Content))
		sb.WriteString("\n\n---\n\n")
	}
	return artifact("raph-knowledge-bundle.md", sb.String()), nil
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
