package indexer

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"raph/internal/db"
)

func TestTreeSitterMultiLanguageSymbolsAndUsage(t *testing.T) {
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
	write("mod.py", "GLOBAL = 1\n\ndef foo(x):\n    return x + GLOBAL\n\nclass Bar:\n    def m(self):\n        return foo(2)\n")
	write("app.ts", "export const MAX = 10;\nfunction add(a: number) { return a + MAX; }\n")
	write("lib.rs", "static MAX: i32 = 10;\nfn add(a: i32) -> i32 { a + MAX }\n")

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

	if _, ok := have[key{"func", "foo"}]; !ok {
		t.Fatalf("python func foo not indexed; nodes=%v", nodeNames(nodes))
	}
	if _, ok := have[key{"type", "Bar"}]; !ok {
		t.Fatalf("python class Bar not indexed; nodes=%v", nodeNames(nodes))
	}
	pyGlobal, ok := have[key{"var", "GLOBAL"}]
	if !ok || pyGlobal.Prop("global") != "true" {
		t.Fatalf("python GLOBAL not indexed as global var; nodes=%v", nodeNames(nodes))
	}
	tsMax, ok := have[key{"const", "MAX"}]
	if !ok || tsMax.Prop("global") != "true" {
		t.Fatalf("ts const MAX not indexed as global; nodes=%v", nodeNames(nodes))
	}

	usage := map[string]bool{}
	for _, e := range edges {
		if e.Type == "USES" {
			usage[byID[e.SourceID].Name+"->"+byID[e.TargetID].Name] = true
		}
	}
	if !usage["foo->GLOBAL"] {
		t.Fatalf("expected USES foo->GLOBAL; usage=%v", usage)
	}
	if !usage["add->MAX"] {
		t.Fatalf("expected USES add->MAX; usage=%v", usage)
	}
}
