package indexer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"raph/internal/config"
	"raph/internal/db"
	"raph/internal/project"
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

// backfillPageSize bounds how many file nodes are held in memory at once while
// paging through a repository.
const backfillPageSize = 500

// BackfillStats reports what a hierarchy backfill wrote.
type BackfillStats struct {
	Projects    int `json:"projects"`
	Workspaces  int `json:"workspaces"`
	Directories int `json:"directories"`
	Files       int `json:"files"`
}

// BackfillHierarchy rebuilds the structural spine for repositories that were
// indexed before it existed.
//
// It reads nothing from disk and embeds nothing: a file node already carries
// the workspace it belongs to, the absolute root it was indexed from, and its
// path relative to that root, which is the whole hierarchy. That makes this a
// cheap, resumable alternative to re-indexing — re-indexing a large graph would
// re-embed every file and spend real money to reproduce content it already has.
//
// Every write is an upsert, so running it twice changes nothing and running it
// on an already-current graph is a no-op.
func BackfillHierarchy(ctx context.Context, store db.GraphStore, cfg *config.Config) (BackfillStats, error) {
	var stats BackfillStats

	lister, ok := store.(interface {
		ListWorkspaces(context.Context) ([]db.Workspace, error)
	})
	if !ok {
		return stats, fmt.Errorf("store does not support listing indexed repositories")
	}
	workspaces, err := lister.ListWorkspaces(ctx)
	if err != nil {
		return stats, fmt.Errorf("list indexed repositories: %w", err)
	}

	projects := map[string]bool{}
	for _, ws := range workspaces {
		if strings.TrimSpace(ws.Root) == "" {
			// Without a root there is nothing to derive a project or a directory
			// tree from; the workspace holds memories or crawled docs, not code.
			verbose.Printf("backfill: skipping workspace without a root workspace=%s", ws.Workspace)
			continue
		}
		if err := ctx.Err(); err != nil {
			return stats, err
		}

		// A root that is no longer on disk cannot be resolved to the project it
		// belongs to: with the checkout gone there is no git remote to read, so
		// resolution falls back to hashing the stale path and would attach this
		// workspace to a project node that nothing else — not its memories, not
		// its documents — shares. Leaving it unlinked is honest; inventing a
		// second project is not.
		if _, statErr := os.Stat(ws.Root); statErr != nil {
			verbose.Printf("backfill: skipping %s, root is no longer present: %v", ws.Workspace, statErr)
			continue
		}

		idx, err := New(store, cfg, ws.Root, true)
		if err != nil {
			return stats, fmt.Errorf("resolve %s: %w", ws.Root, err)
		}
		// Attach to the workspace id already on the stored nodes rather than the
		// one recomputed from the root. They normally agree, but a root that has
		// since become a symlink (or moved) would otherwise build a second,
		// disconnected spine next to the real one.
		idx.workspaceID = ws.Workspace
		idx.dirNodes = map[string]string{}

		if err := idx.ensureProjectAndWorkspace(ctx); err != nil {
			return stats, fmt.Errorf("write hierarchy for %s: %w", ws.Root, err)
		}
		stats.Workspaces++
		if !projects[idx.projectID] {
			projects[idx.projectID] = true
			stats.Projects++
		}

		// Page explicitly rather than passing one big limit: ListNodes caps an
		// unspecified limit at a small default, and a repository larger than any
		// number picked here would be silently half-linked — the backfill would
		// report success having skipped most of the tree.
		linked := 0
		for offset := 0; ; offset += backfillPageSize {
			if err := ctx.Err(); err != nil {
				return stats, err
			}
			// Lean: only id/type/name/url are needed, so this never pulls file
			// contents or embeddings into memory.
			files, err := store.ListNodes(ctx, db.NodeFilter{
				Workspace: ws.Workspace,
				Types:     []string{"file"},
				Lean:      true,
				Limit:     backfillPageSize,
				Offset:    offset,
			})
			if err != nil {
				return stats, fmt.Errorf("list files for %s: %w", ws.Root, err)
			}
			for _, file := range files {
				relPath := strings.TrimSpace(file.URL)
				if relPath == "" {
					relPath = strings.TrimSpace(file.Name)
				}
				if relPath == "" {
					continue
				}
				parentID := idx.ensureDirChain(ctx, relPath)
				if parentID == "" {
					continue
				}
				if err := store.SaveEdge(ctx, db.Edge{
					SourceID: parentID,
					TargetID: file.ID,
					Type:     RelContains,
				}); err != nil {
					verbose.Printf("backfill: edge for %s failed: %v", relPath, err)
					continue
				}
				linked++
			}
			if len(files) < backfillPageSize {
				break
			}
		}
		stats.Files += linked
		// dirNodes holds the workspace root under "" as well, so discount it.
		stats.Directories += len(idx.dirNodes) - 1
		verbose.Printf("backfill: workspace=%s root=%s files=%d dirs=%d", ws.Workspace, ws.Root, linked, len(idx.dirNodes)-1)
	}

	return stats, nil
}

// IdentityMigration reports what re-anchoring project identities moved.
type IdentityMigration struct {
	Projects   int      `json:"projects"`
	Memories   int      `json:"memories"`
	Documents  int      `json:"documents"`
	Conflicts  int      `json:"conflicts"`
	Unresolved []string `json:"unresolved,omitempty"`
}

// scopeMigrator is the memory-side migration, injected so this package does not
// import internal/memory (which would be a cycle through db test helpers).
type scopeMigrator func(ctx context.Context, store db.GraphStore, oldScopeID, newScopeID string) (moved int, conflicts int, err error)

// docMigrator moves a project's documents between buckets.
type docMigrator func(ctx context.Context, store db.GraphStore, oldProjectID, newProjectID string) (int, error)

// MigrateProjectIdentities re-homes memories, documents, and the project node
// for every project whose identity changed when identities moved from being
// hashed over a checkout path to being anchored to the repository's remote.
//
// roots are the directories to consider. Only a project that still exists on
// disk can be migrated: the legacy id is a hash of its path, which cannot be
// inverted, so the path has to come from somewhere — the graph, the sync
// registry, or the user. Anything left behind is reported rather than dropped,
// and stays findable because recall never filters by scope; it simply loses the
// ranking boost until its path is supplied.
func MigrateProjectIdentities(
	ctx context.Context,
	store db.GraphStore,
	cfg *config.Config,
	roots []string,
	migrateMemories scopeMigrator,
	migrateDocs docMigrator,
) (IdentityMigration, error) {
	var stats IdentityMigration

	seen := map[string]bool{}
	// Identities this run resolved. A project that is correctly anchored but was
	// never indexed has no project node, so node presence alone cannot tell a
	// re-anchored scope apart from a stale one — without this set, a scope that
	// was just migrated successfully gets reported as still needing migration.
	resolved := map[string]bool{}
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" || seen[root] {
			continue
		}
		seen[root] = true
		if err := ctx.Err(); err != nil {
			return stats, err
		}

		identity, err := project.Resolve(cfg, root)
		if err != nil {
			verbose.Printf("identity migration: cannot resolve %s: %v", root, err)
			continue
		}
		resolved[identity.ID] = true
		legacyID := project.LegacyPathID(identity.Root)
		if legacyID == identity.ID {
			// Anchored to the path already (no remote, or an override): nothing
			// to move.
			continue
		}

		moved, conflicts, err := migrateMemories(ctx, store, legacyID, identity.ID)
		if err != nil {
			return stats, fmt.Errorf("migrate memories for %s: %w", identity.Root, err)
		}
		docs, err := migrateDocs(ctx, store, legacyID, identity.ID)
		if err != nil {
			return stats, fmt.Errorf("migrate documents for %s: %w", identity.Root, err)
		}
		if err := relocateProjectNode(ctx, store, legacyID, identity); err != nil {
			return stats, fmt.Errorf("relocate project node for %s: %w", identity.Root, err)
		}

		stats.Memories += moved
		stats.Documents += docs
		stats.Conflicts += conflicts
		if moved > 0 || docs > 0 || conflicts > 0 {
			stats.Projects++
		}
		verbose.Printf("identity migration: %s %s -> %s memories=%d docs=%d", identity.Root, legacyID, identity.ID, moved, docs)
	}

	// Anything still scoped to a path-hash that no supplied root explains.
	unresolved, err := unresolvedLegacyScopes(ctx, store, resolved)
	if err != nil {
		return stats, err
	}
	stats.Unresolved = unresolved
	return stats, nil
}

// relocateProjectNode moves the structural project node onto the new identity so
// the graph hierarchy and the memory scope keep sharing one id.
func relocateProjectNode(ctx context.Context, store db.GraphStore, legacyID string, identity project.Identity) error {
	if _, err := store.GetNodeByID(ctx, legacyID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil // never indexed under the old identity: nothing to move
		}
		// A database or cancellation error is not "absent". Swallowing it would
		// report a successful migration while the project node still carries the
		// old id, leaving the graph and the memory scope disagreeing.
		return fmt.Errorf("look up legacy project node %s: %w", legacyID, err)
	}
	relocator, ok := store.(interface {
		RelocateNode(ctx context.Context, oldID, newID, newWorkspace, newURL string) error
	})
	if !ok {
		return nil
	}
	return relocator.RelocateNode(ctx, legacyID, identity.ID, "", "")
}

// unresolvedLegacyScopes lists project scopes that still look like a path hash
// and no longer correspond to a project node, so the operator can see exactly
// what is left and re-run with the right --path.
func unresolvedLegacyScopes(ctx context.Context, store db.GraphStore, resolved map[string]bool) ([]string, error) {
	lister, ok := store.(interface {
		ListProjectScopes(ctx context.Context) ([]string, error)
	})
	if !ok {
		return nil, nil
	}
	scopes, err := lister.ListProjectScopes(ctx)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, scope := range scopes {
		if resolved[scope] {
			continue
		}
		if _, err := store.GetNodeByID(ctx, scope); err != nil {
			out = append(out, scope)
		}
	}
	sort.Strings(out)
	return out, nil
}
