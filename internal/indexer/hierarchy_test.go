package indexer

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"raph/internal/db"
	"raph/internal/knowledge"
	"raph/internal/memory"
	"raph/internal/project"
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

// The backfill exists so an existing graph does not have to be re-indexed (and
// re-embedded) to gain the hierarchy. It must reconstruct the same spine a full
// index would, using only what the stored file nodes already carry.
func TestBackfillHierarchyRebuildsSpineWithoutReindexing(t *testing.T) {
	store := hierarchyStore(t)
	root := t.TempDir()
	writeFile(t, root, "top.md", "# top")
	writeFile(t, root, "internal/deep/nested.md", "# nested")
	ctx := context.Background()

	idx, err := New(store, nil, root, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := idx.Run(ctx); err != nil {
		t.Fatal(err)
	}

	// Simulate a graph indexed before the hierarchy existed: drop every
	// structural node, leaving the file nodes untouched.
	for _, nodeType := range StructuralTypes {
		nodes, err := store.ListNodes(ctx, db.NodeFilter{Types: []string{nodeType}})
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range nodes {
			if err := store.DeleteNodeByID(ctx, n.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	if children := childrenOf(t, store, idx.WorkspaceID()); len(children) != 0 {
		t.Fatalf("expected the spine removed before backfill, got %+v", children)
	}

	stats, err := BackfillHierarchy(ctx, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Projects != 1 || stats.Workspaces != 1 {
		t.Fatalf("expected 1 project and 1 workspace, got %+v", stats)
	}
	if stats.Files != 2 {
		t.Fatalf("expected both files linked to a directory, got %+v", stats)
	}
	if stats.Directories != 2 {
		t.Fatalf("expected internal and internal/deep rebuilt, got %+v", stats)
	}

	// The rebuilt spine must be walkable exactly like a freshly indexed one.
	projectChildren := childrenOf(t, store, idx.ProjectID())
	if len(projectChildren[TypeWorkspace]) != 1 {
		t.Fatalf("project does not contain its workspace after backfill: %+v", projectChildren)
	}
	wsChildren := childrenOf(t, store, idx.WorkspaceID())
	if len(wsChildren["file"]) != 1 || wsChildren["file"][0].Name != "top.md" {
		t.Fatalf("expected top.md under the workspace, got %+v", wsChildren["file"])
	}
	if len(wsChildren[TypeDirectory]) != 1 || wsChildren[TypeDirectory][0].Name != "internal" {
		t.Fatalf("expected internal/ under the workspace, got %+v", wsChildren[TypeDirectory])
	}
	deep := childrenOf(t, store, wsChildren[TypeDirectory][0].ID)
	if len(deep[TypeDirectory]) != 1 || deep[TypeDirectory][0].Name != "internal/deep" {
		t.Fatalf("expected internal/deep rebuilt, got %+v", deep)
	}
}

func TestBackfillHierarchyIsIdempotent(t *testing.T) {
	store := hierarchyStore(t)
	root := t.TempDir()
	writeFile(t, root, "pkg/a.md", "# a")
	ctx := context.Background()

	idx, err := New(store, nil, root, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := idx.Run(ctx); err != nil {
		t.Fatal(err)
	}

	first, err := BackfillHierarchy(ctx, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	nodesBefore, edgesBefore, err := store.GetAllGraphElements(ctx)
	if err != nil {
		t.Fatal(err)
	}

	second, err := BackfillHierarchy(ctx, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("expected a repeat backfill to report the same result: %+v then %+v", first, second)
	}
	nodesAfter, edgesAfter, err := store.GetAllGraphElements(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodesBefore) != len(nodesAfter) || len(edgesBefore) != len(edgesAfter) {
		t.Fatalf("repeat backfill changed the graph: nodes %d->%d, edges %d->%d",
			len(nodesBefore), len(nodesAfter), len(edgesBefore), len(edgesAfter))
	}
}

// A graph with no indexed repositories (memories only) must be a clean no-op.
func TestBackfillHierarchyWithoutIndexedRepositories(t *testing.T) {
	store := hierarchyStore(t)
	stats, err := BackfillHierarchy(context.Background(), store, nil)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Workspaces != 0 || stats.Projects != 0 {
		t.Fatalf("expected nothing to backfill, got %+v", stats)
	}
}

// A repository with more files than one listing page must be fully linked.
// ListNodes caps an unspecified limit at a small default, so an unpaged
// backfill silently linked only the first page and still reported success.
func TestBackfillHierarchyLinksEveryFileAcrossPages(t *testing.T) {
	store := hierarchyStore(t)
	root := t.TempDir()
	const files = backfillPageSize + 37
	for i := 0; i < files; i++ {
		writeFile(t, root, fmt.Sprintf("pkg%02d/file%04d.md", i%7, i), fmt.Sprintf("# doc %d", i))
	}
	ctx := context.Background()

	idx, err := New(store, nil, root, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := idx.Run(ctx); err != nil {
		t.Fatal(err)
	}
	for _, nodeType := range StructuralTypes {
		nodes, err := store.ListNodes(ctx, db.NodeFilter{Types: []string{nodeType}, Limit: 10_000})
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range nodes {
			if err := store.DeleteNodeByID(ctx, n.ID); err != nil {
				t.Fatal(err)
			}
		}
	}

	stats, err := BackfillHierarchy(ctx, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Files != files {
		t.Fatalf("expected all %d files linked, got %d", files, stats.Files)
	}
	if stats.Directories != 7 {
		t.Fatalf("expected 7 package directories, got %d", stats.Directories)
	}
}

func migrateMemoriesFn(ctx context.Context, store db.GraphStore, oldID, newID string) (int, int, error) {
	stats, err := memory.MigrateProjectScope(ctx, store, oldID, newID)
	return stats.Moved, stats.Conflict, err
}

func migrateDocsFn(ctx context.Context, store db.GraphStore, oldID, newID string) (int, error) {
	return knowledge.MigrateWorkspace(ctx, store,
		knowledge.ProjectWorkspace(oldID), knowledge.ProjectWorkspace(newID))
}

func gitRepoWithRemote(t *testing.T, remote string) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{{"init"}, {"remote", "add", "origin", remote}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git unavailable: %v (%s)", err, out)
		}
	}
	return dir
}

func seedProjectMemory(t *testing.T, store db.GraphStore, scopeID, key string) {
	t.Helper()
	if _, err := memory.Store(context.Background(), store, nil, memory.StoreInput{
		ScopeType: "project", ScopeID: scopeID, KnowledgeType: "decision",
		Title: key, Content: "content for " + key, Source: "user",
		WriterID: "agent:test", MemoryKey: key,
	}); err != nil {
		t.Fatal(err)
	}
}

// Memories and documents written under the old path-derived identity must move
// onto the remote-derived one, or re-anchoring would orphan exactly the
// knowledge it was meant to protect.
func TestMigrateProjectIdentitiesMovesKnowledgeOntoRemoteIdentity(t *testing.T) {
	store := hierarchyStore(t)
	ctx := context.Background()
	repo := gitRepoWithRemote(t, "git@github.com:acme/widget.git")

	identity, err := project.Resolve(nil, repo)
	if err != nil {
		t.Fatal(err)
	}
	legacyID := project.LegacyPathID(identity.Root)
	if legacyID == identity.ID {
		t.Fatal("fixture error: expected the remote to change the identity")
	}

	seedProjectMemory(t, store, legacyID, "deploy/process")
	if _, err := knowledge.Add(ctx, store, nil, knowledge.AddInput{
		Workspace: knowledge.ProjectWorkspace(legacyID), Key: "handoff/one",
		Title: "Handoff", Content: "work in progress", DocType: knowledge.DocHandoff,
		Source: "user", NoEmbed: true,
	}); err != nil {
		t.Fatal(err)
	}

	stats, err := MigrateProjectIdentities(ctx, store, nil, []string{repo}, migrateMemoriesFn, migrateDocsFn)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Projects != 1 || stats.Memories != 1 || stats.Documents != 1 {
		t.Fatalf("expected one project with its memory and document moved, got %+v", stats)
	}

	moved, err := store.SearchMemoryRecords(ctx, db.MemorySearchFilter{
		ScopeType: "project", ScopeID: identity.ID, Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(moved) != 1 {
		t.Fatalf("expected the memory under the remote identity, found %d", len(moved))
	}
	stale, err := store.SearchMemoryRecords(ctx, db.MemorySearchFilter{
		ScopeType: "project", ScopeID: legacyID, Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) != 0 {
		t.Fatalf("memory left behind under the legacy identity: %d", len(stale))
	}
	docs, err := knowledge.List(ctx, store, knowledge.ListFilter{Workspace: knowledge.ProjectWorkspace(identity.ID)})
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected the document under the remote identity, found %d", len(docs))
	}
	// Present in the new bucket is only half the claim; a copy left behind would
	// be resurrected by any later lookup against the legacy identity.
	legacyDocs, err := knowledge.List(ctx, store, knowledge.ListFilter{Workspace: knowledge.ProjectWorkspace(legacyID)})
	if err != nil {
		t.Fatal(err)
	}
	if len(legacyDocs) != 0 {
		t.Fatalf("document left behind under the legacy identity: %d", len(legacyDocs))
	}
}

// A repository with no remote keeps its path identity, so there is nothing to
// migrate and nothing should be touched.
func TestMigrateProjectIdentitiesLeavesRemotelessReposAlone(t *testing.T) {
	store := hierarchyStore(t)
	ctx := context.Background()
	repo := t.TempDir()
	cmd := exec.Command("git", "init")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("git unavailable: %v (%s)", err, out)
	}

	identity, err := project.Resolve(nil, repo)
	if err != nil {
		t.Fatal(err)
	}
	seedProjectMemory(t, store, identity.ID, "kept")

	stats, err := MigrateProjectIdentities(ctx, store, nil, []string{repo}, migrateMemoriesFn, migrateDocsFn)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Projects != 0 || stats.Memories != 0 {
		t.Fatalf("expected nothing to move for a remoteless repo, got %+v", stats)
	}
	kept, err := store.SearchMemoryRecords(ctx, db.MemorySearchFilter{
		ScopeType: "project", ScopeID: identity.ID, Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != 1 {
		t.Fatalf("expected the memory untouched, found %d", len(kept))
	}
}

// Regression: a project that was just re-anchored, but never indexed, has no
// project node. Reporting it as "still scoped to a path" would send the operator
// chasing a migration that already happened.
func TestMigrateProjectIdentitiesDoesNotReportJustMigratedProjects(t *testing.T) {
	store := hierarchyStore(t)
	ctx := context.Background()
	repo := gitRepoWithRemote(t, "git@github.com:acme/never-indexed.git")

	identity, err := project.Resolve(nil, repo)
	if err != nil {
		t.Fatal(err)
	}
	seedProjectMemory(t, store, project.LegacyPathID(identity.Root), "only/memory")

	stats, err := MigrateProjectIdentities(ctx, store, nil, []string{repo}, migrateMemoriesFn, migrateDocsFn)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Memories != 1 {
		t.Fatalf("expected the memory migrated, got %+v", stats)
	}
	for _, scope := range stats.Unresolved {
		if scope == identity.ID {
			t.Fatalf("a project migrated in this run was reported as unresolved: %v", stats.Unresolved)
		}
	}
}

// A scope whose path nobody supplied must be reported, not silently ignored —
// it is the only signal that knowledge is still parked under an old identity.
func TestMigrateProjectIdentitiesReportsUnknownScopes(t *testing.T) {
	store := hierarchyStore(t)
	ctx := context.Background()
	seedProjectMemory(t, store, "project:unknownpathhash", "stranded")

	stats, err := MigrateProjectIdentities(ctx, store, nil, nil, migrateMemoriesFn, migrateDocsFn)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(stats.Unresolved, "project:unknownpathhash") {
		t.Fatalf("expected the stranded scope reported, got %+v", stats.Unresolved)
	}
}

// Removing the last indexed file in a directory must take the directory node
// with it, or the project hierarchy keeps showing folders that hold nothing.
func TestRemoveFilePrunesEmptyDirectories(t *testing.T) {
	store := hierarchyStore(t)
	root := t.TempDir()
	writeFile(t, root, "keep/stays.md", "# stays")
	writeFile(t, root, "gone/deep/only.md", "# only")
	ctx := context.Background()

	idx, err := New(store, nil, root, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := idx.Run(ctx); err != nil {
		t.Fatal(err)
	}

	if err := os.Remove(filepath.Join(root, "gone", "deep", "only.md")); err != nil {
		t.Fatal(err)
	}
	if err := idx.RemoveFile(ctx, "gone/deep/only.md"); err != nil {
		t.Fatal(err)
	}

	dirs, err := store.ListNodes(ctx, db.NodeFilter{
		Workspace: idx.WorkspaceID(), Types: []string{TypeDirectory}, Limit: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	remaining := map[string]bool{}
	for _, d := range dirs {
		remaining[d.Name] = true
	}
	if remaining["gone/deep"] || remaining["gone"] {
		t.Fatalf("emptied directories were not pruned: %v", remaining)
	}
	if !remaining["keep"] {
		t.Fatalf("a directory that still holds a file was pruned: %v", remaining)
	}
}
