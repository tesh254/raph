package indexer

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"raph/internal/db"
)

func hierarchyStore(t *testing.T) *db.LibSQLStore {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	store, err := db.InitStorage()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// childrenOf returns the ids a node CONTAINS, by node type.
func childrenOf(t *testing.T, store *db.LibSQLStore, nodeID string) map[string][]db.Node {
	t.Helper()
	nodes, edges, err := store.GetNeighbors(context.Background(), nodeID)
	if err != nil {
		t.Fatal(err)
	}
	byID := make(map[string]db.Node, len(nodes))
	for _, n := range nodes {
		byID[n.ID] = n
	}
	out := map[string][]db.Node{}
	for _, e := range edges {
		if e.Type != RelContains || e.SourceID != nodeID {
			continue
		}
		if child, ok := byID[e.TargetID]; ok {
			out[child.Type] = append(out[child.Type], child)
		}
	}
	return out
}

func TestIndexBuildsProjectWorkspaceDirectoryFileSpine(t *testing.T) {
	store := hierarchyStore(t)
	root := t.TempDir()
	writeFile(t, root, "README.md", "# top level")
	writeFile(t, root, "internal/deep/nested.md", "# nested")

	idx, err := New(store, nil, root, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := idx.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	// project -> workspace
	projectChildren := childrenOf(t, store, idx.ProjectID())
	if len(projectChildren[TypeWorkspace]) != 1 || projectChildren[TypeWorkspace][0].ID != idx.WorkspaceID() {
		t.Fatalf("expected the project to contain its workspace, got %+v", projectChildren)
	}

	// workspace -> top-level file and first-level directory
	wsChildren := childrenOf(t, store, idx.WorkspaceID())
	if len(wsChildren["file"]) != 1 || wsChildren["file"][0].Name != "README.md" {
		t.Fatalf("expected README.md directly under the workspace, got %+v", wsChildren["file"])
	}
	if len(wsChildren[TypeDirectory]) != 1 || wsChildren[TypeDirectory][0].Name != "internal" {
		t.Fatalf("expected the internal/ directory under the workspace, got %+v", wsChildren[TypeDirectory])
	}

	// internal -> internal/deep -> nested.md
	internalID := wsChildren[TypeDirectory][0].ID
	internalChildren := childrenOf(t, store, internalID)
	if len(internalChildren[TypeDirectory]) != 1 || internalChildren[TypeDirectory][0].Name != "internal/deep" {
		t.Fatalf("expected internal/deep beneath internal, got %+v", internalChildren)
	}
	deepChildren := childrenOf(t, store, internalChildren[TypeDirectory][0].ID)
	if len(deepChildren["file"]) != 1 || deepChildren["file"][0].Name != "internal/deep/nested.md" {
		t.Fatalf("expected nested.md beneath internal/deep, got %+v", deepChildren["file"])
	}
}

// Directories are materialized from indexed files, so a directory holding
// nothing indexable never becomes a node.
func TestIndexSkipsDirectoriesWithoutIndexedFiles(t *testing.T) {
	store := hierarchyStore(t)
	root := t.TempDir()
	writeFile(t, root, "keep/notes.md", "# kept")
	if err := os.MkdirAll(filepath.Join(root, "empty", "deeper"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, root, "binaryonly/image.png", "not indexed")

	idx, err := New(store, nil, root, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := idx.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	nodes, err := store.ListNodes(context.Background(), db.NodeFilter{Workspace: idx.WorkspaceID()})
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range nodes {
		if node.Type != TypeDirectory {
			continue
		}
		if node.Name != "keep" {
			t.Fatalf("unexpected directory node %q — only directories with indexed files should exist", node.Name)
		}
	}
}

// A monorepo indexes several roots under one project. Re-indexing one root
// clears that workspace wholesale, which must not detach the project node or
// the sibling workspace hanging off it.
func TestReindexingOneWorkspaceKeepsProjectAndSiblings(t *testing.T) {
	store := hierarchyStore(t)
	repo := t.TempDir()
	writeFile(t, repo, "alpha/a.md", "# alpha")
	writeFile(t, repo, "beta/b.md", "# beta")

	alpha, err := New(store, nil, filepath.Join(repo, "alpha"), true)
	if err != nil {
		t.Fatal(err)
	}
	beta, err := New(store, nil, filepath.Join(repo, "beta"), true)
	if err != nil {
		t.Fatal(err)
	}
	// Both roots sit outside a git worktree here, so pin them to one project the
	// way an identity override or a shared worktree would.
	beta.projectID = alpha.ProjectID()
	beta.projectRoot = alpha.projectRoot

	ctx := context.Background()
	if _, err := alpha.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := beta.Run(ctx); err != nil {
		t.Fatal(err)
	}

	children := childrenOf(t, store, alpha.ProjectID())
	if len(children[TypeWorkspace]) != 2 {
		t.Fatalf("expected both workspaces under the project, got %+v", children[TypeWorkspace])
	}

	// Re-index alpha: beta's link must survive.
	if _, err := alpha.Run(ctx); err != nil {
		t.Fatal(err)
	}
	children = childrenOf(t, store, alpha.ProjectID())
	if len(children[TypeWorkspace]) != 2 {
		t.Fatalf("re-indexing one workspace detached the other: %+v", children[TypeWorkspace])
	}
}

// The incremental sync path indexes a file without going through Run, so it
// must still build (and reuse) the spine.
func TestSyncFileBuildsHierarchyWithoutFullRun(t *testing.T) {
	store := hierarchyStore(t)
	root := t.TempDir()
	writeFile(t, root, "docs/guide.md", "# guide")

	idx, err := New(store, nil, root, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := idx.SyncFile(context.Background(), filepath.Join(root, "docs", "guide.md")); err != nil {
		t.Fatal(err)
	}

	projectChildren := childrenOf(t, store, idx.ProjectID())
	if len(projectChildren[TypeWorkspace]) != 1 {
		t.Fatalf("expected the sync path to write the project spine, got %+v", projectChildren)
	}
	wsChildren := childrenOf(t, store, idx.WorkspaceID())
	if len(wsChildren[TypeDirectory]) != 1 || wsChildren[TypeDirectory][0].Name != "docs" {
		t.Fatalf("expected docs/ created by the sync path, got %+v", wsChildren[TypeDirectory])
	}
}

// Repository stats describe indexed content. Structural nodes would inflate
// every total and add a phantom domain, so they are excluded.
func TestListWorkspacesIgnoresStructuralNodes(t *testing.T) {
	store := hierarchyStore(t)
	root := t.TempDir()
	writeFile(t, root, "a.md", "# a")
	writeFile(t, root, "nested/b.md", "# b")

	idx, err := New(store, nil, root, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := idx.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	workspaces, err := store.ListWorkspaces(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var found *db.Workspace
	for i := range workspaces {
		if workspaces[i].Workspace == idx.WorkspaceID() {
			found = &workspaces[i]
		}
	}
	if found == nil {
		t.Fatalf("indexed repo missing from workspace listing: %+v", workspaces)
	}
	if found.Files != 2 {
		t.Fatalf("expected 2 files, got %d", found.Files)
	}
	if _, ok := found.ByDomain[DomainStructure]; ok {
		t.Fatalf("structural nodes leaked into the domain breakdown: %+v", found.ByDomain)
	}

	all, err := store.ListNodes(context.Background(), db.NodeFilter{Workspace: idx.WorkspaceID()})
	if err != nil {
		t.Fatal(err)
	}
	if found.Nodes >= len(all) {
		t.Fatalf("expected structural nodes excluded from the node count: reported %d of %d stored", found.Nodes, len(all))
	}
}
