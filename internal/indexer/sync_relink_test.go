package indexer

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"raph/internal/db"
)

// usageEdges returns the set of "src->tgt" USES edges by node name.
func usageEdges(t *testing.T, store *db.LibSQLStore) map[string]bool {
	t.Helper()
	nodes, edges, err := store.GetAllGraphElements(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]db.Node{}
	for _, n := range nodes {
		byID[n.ID] = n
	}
	usage := map[string]bool{}
	for _, e := range edges {
		if e.Type == "USES" {
			usage[byID[e.SourceID].Name+"->"+byID[e.TargetID].Name] = true
		}
	}
	return usage
}

// TestSyncFilePreservesAndRestoresReferenceEdges guards H1: a single-file sync
// must not strip the cross-file reference graph.
//   - Editing the *target* file (config.ts) must preserve the incoming edge from
//     the unchanged importer (client.ts) — the symbol id is stable, so the edge
//     is snapshotted and restored.
//   - Editing the *importer* (client.ts) must restore its own outgoing edge via
//     the per-cycle RelinkImportAware pass.
func TestSyncFilePreservesAndRestoresReferenceEdges(t *testing.T) {
	t.Setenv("RAPH_NO_SCIP", "1") // force the pure-Go import-aware fallback
	t.Setenv("HOME", t.TempDir())
	store, err := db.InitStorage()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	root := t.TempDir()
	write := func(name, content string) {
		full := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("config.ts", "export const LIMIT = 5;\n")
	write("client.ts", "import { LIMIT } from \"./config\";\nexport function ratio(): number {\n  return LIMIT * 2;\n}\n")

	idx, err := New(store, nil, root, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := idx.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !usageEdges(t, store)["ratio->LIMIT"] {
		t.Fatalf("baseline full index missing ratio->LIMIT: %v", usageEdges(t, store))
	}

	// Edit the TARGET file (keep LIMIT) and sync only it. The incoming edge from
	// the untouched client.ts must survive.
	write("config.ts", "// tweaked\nexport const LIMIT = 7;\n")
	if _, err := idx.SyncFile(context.Background(), filepath.Join(root, "config.ts")); err != nil {
		t.Fatal(err)
	}
	if !usageEdges(t, store)["ratio->LIMIT"] {
		t.Fatalf("incoming edge lost after syncing the target file: %v", usageEdges(t, store))
	}

	// Edit the IMPORTER and sync it — its outgoing edge is deleted with its old
	// nodes; the per-cycle relink must restore it.
	write("client.ts", "import { LIMIT } from \"./config\";\nexport function ratio(): number {\n  return LIMIT * 3;\n}\n")
	if _, err := idx.SyncFile(context.Background(), filepath.Join(root, "client.ts")); err != nil {
		t.Fatal(err)
	}
	if usageEdges(t, store)["ratio->LIMIT"] {
		// SyncFile alone should NOT recreate the outgoing edge (documents why the
		// relink pass is needed); if this ever changes the assertion below still
		// holds and this branch simply won't fire.
		t.Logf("note: outgoing edge already present before relink")
	}
	idx.RelinkImportAware(context.Background(), []string{"client.ts"})
	if !usageEdges(t, store)["ratio->LIMIT"] {
		t.Fatalf("outgoing edge not restored after relink: %v", usageEdges(t, store))
	}
}

// TestSyncFileDropsEdgeToRemovedSymbol confirms the preservation is correct, not
// blind: if the edited target file removes the referenced symbol, the incoming
// edge must NOT be resurrected.
func TestSyncFileDropsEdgeToRemovedSymbol(t *testing.T) {
	t.Setenv("RAPH_NO_SCIP", "1")
	t.Setenv("HOME", t.TempDir())
	store, err := db.InitStorage()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	root := t.TempDir()
	write := func(name, content string) {
		full := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("config.ts", "export const LIMIT = 5;\n")
	write("client.ts", "import { LIMIT } from \"./config\";\nexport function ratio(): number {\n  return LIMIT * 2;\n}\n")

	idx, err := New(store, nil, root, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := idx.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Remove LIMIT from config.ts and sync it: the edge's target no longer exists.
	write("config.ts", "export const OTHER = 5;\n")
	if _, err := idx.SyncFile(context.Background(), filepath.Join(root, "config.ts")); err != nil {
		t.Fatal(err)
	}
	if usageEdges(t, store)["ratio->LIMIT"] {
		t.Fatalf("edge to removed symbol LIMIT should not survive: %v", usageEdges(t, store))
	}
}
