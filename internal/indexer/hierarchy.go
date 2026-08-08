package indexer

import (
	"context"
	"path/filepath"
	"strings"

	"raph/internal/db"
	"raph/internal/verbose"
)

// The structural spine of the graph: project → workspace → directory → file.
//
// Files already carry their workspace and relative path, so this hierarchy is
// not how a file is *found* — it is how a path is *navigated*. An agent holding
// a project can walk down to the directory it cares about with graph_neighbors,
// and a file can be walked back up to the project that owns it, which is what
// makes "which project does this belong to" answerable from the graph rather
// than by re-deriving hashes.
//
// Types are excluded from repository statistics (see db.ListWorkspaces): they
// describe the shape of a repo rather than its indexed content.
const (
	TypeProject   = "project"
	TypeWorkspace = "workspace"
	TypeDirectory = "directory"

	// DomainStructure marks nodes that exist to hold the hierarchy together.
	DomainStructure = "structure"

	// RelContains links a container to what it holds.
	RelContains = "CONTAINS"
)

// StructuralTypes are the node types that form the hierarchy rather than
// indexed content.
var StructuralTypes = []string{TypeProject, TypeWorkspace, TypeDirectory}

// ensureProjectAndWorkspace writes the two nodes at the top of the spine and
// the edge between them.
//
// The project node is deliberately NOT tagged with a workspace: one project can
// hold several indexed roots (a monorepo's packages), and a full re-index of any
// one of them clears its workspace wholesale. A workspace-tagged project node
// would be deleted by whichever root reindexed first, silently detaching the
// others. An untagged node also stays out of the repository listing, which
// counts only workspace-tagged rows.
func (i *Indexer) ensureProjectAndWorkspace(ctx context.Context) error {
	projectNode := db.Node{
		ID:        i.projectID,
		Workspace: "",
		Domain:    DomainStructure,
		Type:      TypeProject,
		Name:      filepath.Base(i.projectRoot),
		Content:   i.projectRoot,
		Path:      i.projectRoot,
	}
	if err := i.store.SaveNode(ctx, projectNode); err != nil {
		return err
	}

	workspaceNode := db.Node{
		ID:        i.workspaceID,
		Workspace: i.workspaceID,
		Domain:    DomainStructure,
		Type:      TypeWorkspace,
		Name:      filepath.Base(i.root),
		Content:   i.root,
		Path:      i.root,
	}
	if err := i.store.SaveNode(ctx, workspaceNode); err != nil {
		return err
	}

	if err := i.store.SaveEdge(ctx, db.Edge{
		SourceID: i.projectID,
		TargetID: i.workspaceID,
		Type:     RelContains,
	}); err != nil {
		return err
	}

	// The workspace root is the parent of everything at depth 0.
	i.dirNodes[""] = i.workspaceID
	return nil
}

// ensureDirChain creates the directory nodes leading to relPath and returns the
// id of its immediate parent, which is the workspace node for a top-level file.
//
// Directories are materialized from the files that live in them rather than
// from the directory walk, so empty and fully-ignored directories never become
// nodes. Each chain is written once per run; the memo also makes the
// single-file sync path cheap, since it re-walks the same ancestors on
// every save.
func (i *Indexer) ensureDirChain(ctx context.Context, relPath string) string {
	if i.dirNodes == nil {
		i.dirNodes = map[string]string{}
	}
	// The incremental sync path indexes a file without going through Run, so the
	// spine is written on first use. Both node saves are upserts, making this
	// safe to reach after Run has already built it.
	if _, ok := i.dirNodes[""]; !ok {
		if err := i.ensureProjectAndWorkspace(ctx); err != nil {
			verbose.Printf("project hierarchy unavailable: %v", err)
			// Memoize the fallback so a failing store isn't retried per file.
			i.dirNodes[""] = i.workspaceID
		}
	}

	dir := filepath.ToSlash(filepath.Dir(relPath))
	if dir == "." || dir == "/" {
		dir = ""
	}
	if id, ok := i.dirNodes[dir]; ok {
		return id
	}

	segments := strings.Split(dir, "/")
	parentID := i.workspaceID
	current := ""
	for _, segment := range segments {
		if segment == "" {
			continue
		}
		if current == "" {
			current = segment
		} else {
			current += "/" + segment
		}

		if existing, ok := i.dirNodes[current]; ok {
			parentID = existing
			continue
		}

		node := db.Node{
			ID:        i.nodeID("dir", current),
			Workspace: i.workspaceID,
			Domain:    DomainStructure,
			Type:      TypeDirectory,
			Name:      current,
			Content:   current,
			URL:       current,
			Path:      i.root,
		}
		if err := i.store.SaveNode(ctx, node); err != nil {
			// A missing directory node costs navigability, not correctness: the
			// file itself is still indexed and searchable.
			verbose.Printf("directory node save failed dir=%s: %v", current, err)
			return parentID
		}
		if err := i.store.SaveEdge(ctx, db.Edge{
			SourceID: parentID,
			TargetID: node.ID,
			Type:     RelContains,
		}); err != nil {
			verbose.Printf("directory edge save failed dir=%s: %v", current, err)
		}
		i.dirNodes[current] = node.ID
		parentID = node.ID
	}
	return parentID
}
