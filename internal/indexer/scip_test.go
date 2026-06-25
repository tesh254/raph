package indexer

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"raph/internal/db"
)

// TestSCIPCrossFileResolution proves the SCIP tier links a reference to a
// declaration in a DIFFERENT file — something the within-file tree-sitter pass
// cannot do. Requires scip-typescript on PATH; skipped otherwise so CI without
// the optional tool still passes.
func TestSCIPCrossFileResolution(t *testing.T) {
	if _, err := exec.LookPath("scip-typescript"); err != nil {
		t.Skip("scip-typescript not installed; skipping SCIP integration test")
	}
	t.Setenv("HOME", t.TempDir())
	store, err := db.InitStorage()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	root := t.TempDir()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("config.ts", "export const MAX_RETRIES = 5;\n")
	write("client.ts", "import { MAX_RETRIES } from \"./config\";\nexport function retry(): number {\n  return MAX_RETRIES;\n}\n")

	idx, err := New(store, nil, root, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := idx.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	nodes, edges, err := store.GetAllGraphElements(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]db.Node{}
	type key struct{ typ, name string }
	have := map[key]db.Node{}
	for _, n := range nodes {
		byID[n.ID] = n
		have[key{n.Type, n.Name}] = n
	}
	if _, ok := have[key{"const", "MAX_RETRIES"}]; !ok {
		t.Fatalf("const MAX_RETRIES not indexed; nodes=%v", nodeNames(nodes))
	}
	if _, ok := have[key{"func", "retry"}]; !ok {
		t.Fatalf("func retry not indexed; nodes=%v", nodeNames(nodes))
	}

	usage := map[string]bool{}
	for _, e := range edges {
		if e.Type == "USES" {
			usage[byID[e.SourceID].Name+"->"+byID[e.TargetID].Name] = true
		}
	}
	if !usage["retry->MAX_RETRIES"] {
		t.Fatalf("expected cross-file USES retry->MAX_RETRIES from SCIP; usage=%v", usage)
	}
}
