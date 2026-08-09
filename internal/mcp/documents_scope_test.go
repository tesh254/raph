package mcp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"raph/internal/db"
	"raph/internal/indexer"
	"raph/internal/knowledge"
)

func docTestStore(t *testing.T) *db.LibSQLStore {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	store, err := db.InitStorage()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func newRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("# repo"), 0o600); err != nil {
		t.Fatal(err)
	}
	return repo
}

func addDoc(t *testing.T, store db.GraphStore, workspace, key, title string) knowledge.Document {
	t.Helper()
	doc, err := knowledge.Add(context.Background(), store, nil, knowledge.AddInput{
		Workspace: workspace,
		Key:       key,
		Title:     title,
		Content:   "Durable handoff content that must outlive an index run.",
		DocType:   knowledge.DocHandoff,
		Source:    "user",
		WriterID:  "agent:test",
		NoEmbed:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

// Documents and handoffs are written by a person or an agent; nothing
// regenerates them. Re-indexing a repository clears its workspace wholesale, so
// storing docs under the indexer's workspace id meant `raph init` silently
// destroyed them.
func TestReindexingKeepsProjectDocuments(t *testing.T) {
	store := docTestStore(t)
	repo := newRepo(t)
	ctx := context.Background()

	wrapper := NewMCPServerWrapper(store, nil)
	workspace, err := wrapper.resolveDocWorkspace("project", repo)
	if err != nil {
		t.Fatal(err)
	}
	doc := addDoc(t, store, workspace, "release/handoff", "Release handoff")

	idx, err := indexer.New(store, nil, repo, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := idx.Run(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := store.GetNodeByID(ctx, doc.Node.ID); err != nil {
		t.Fatalf("re-indexing destroyed a project document: %v", err)
	}
	docs, err := knowledge.List(ctx, store, knowledge.ListFilter{Workspace: workspace})
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected the document to survive re-indexing, found %d", len(docs))
	}
}

// A document belongs to a project, not to whichever directory the agent
// happened to be in. Memory already resolves the worktree root; documents must
// agree, or a handoff written from a subdirectory is invisible from the repo
// root that a later agent works in.
func TestProjectDocumentsShareOneScopeAcrossSubdirectories(t *testing.T) {
	store := docTestStore(t)
	repo := newRepo(t)
	// A worktree is what makes subdirectories one project; outside git, sibling
	// directories are legitimately separate projects.
	cmd := exec.Command("git", "init")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("git unavailable: %v (%s)", err, out)
	}
	nested := filepath.Join(repo, "internal", "deep")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	wrapper := NewMCPServerWrapper(store, nil)
	fromRoot, err := wrapper.resolveDocWorkspace("project", repo)
	if err != nil {
		t.Fatal(err)
	}
	fromNested, err := wrapper.resolveDocWorkspace("project", nested)
	if err != nil {
		t.Fatal(err)
	}
	if fromRoot != fromNested {
		t.Fatalf("a subdirectory resolved to a different document scope: %s vs %s", fromNested, fromRoot)
	}

	addDoc(t, store, fromNested, "nested/handoff", "Written from a subdirectory")
	docs, err := knowledge.List(context.Background(), store, knowledge.ListFilter{Workspace: fromRoot})
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected a document written from a subdirectory to be listed from the repo root, found %d", len(docs))
	}
}

// The document scope must never equal the indexer's workspace id — that
// collision is what let an index run delete user-authored knowledge.
func TestDocumentScopeIsDistinctFromIndexerWorkspace(t *testing.T) {
	store := docTestStore(t)
	repo := newRepo(t)

	wrapper := NewMCPServerWrapper(store, nil)
	docWorkspace, err := wrapper.resolveDocWorkspace("project", repo)
	if err != nil {
		t.Fatal(err)
	}
	idx, err := indexer.New(store, nil, repo, true)
	if err != nil {
		t.Fatal(err)
	}
	if docWorkspace == idx.WorkspaceID() {
		t.Fatalf("document scope shares the indexer workspace id %q", docWorkspace)
	}
}
