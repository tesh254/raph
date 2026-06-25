package indexer

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"raph/internal/db"
)

// TestImportAwareCrossFileFallback proves the pure-Go fallback links a
// reference to a declaration in another file via import resolution, with NO
// external tool. Python (no scip-python here) and TypeScript with SCIP forced
// off both exercise it.
func TestImportAwareCrossFileFallback(t *testing.T) {
	t.Setenv("RAPH_NO_SCIP", "1") // force the fallback even if scip-typescript is installed
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
	// Python relative import across files.
	write("pkg/config.py", "MAX = 10\n")
	write("pkg/client.py", "from .config import MAX\n\ndef use():\n    return MAX\n")
	// TypeScript relative import across files.
	write("config.ts", "export const LIMIT = 5;\n")
	write("client.ts", "import { LIMIT } from \"./config\";\nexport function ratio(): number {\n  return LIMIT * 2;\n}\n")

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
	for _, n := range nodes {
		byID[n.ID] = n
	}
	usage := map[string]bool{}
	for _, e := range edges {
		if e.Type == "USES" {
			usage[byID[e.SourceID].Name+"->"+byID[e.TargetID].Name] = true
		}
	}
	if !usage["use->MAX"] {
		t.Fatalf("expected cross-file USES use->MAX (python fallback); usage=%v nodes=%v", usage, nodeNames(nodes))
	}
	if !usage["ratio->LIMIT"] {
		t.Fatalf("expected cross-file USES ratio->LIMIT (ts fallback); usage=%v nodes=%v", usage, nodeNames(nodes))
	}
}
