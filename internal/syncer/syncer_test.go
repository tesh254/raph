package syncer

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"raph/internal/db"
	"raph/internal/indexer"
)

func TestSyncOnceUpdatesAndRemovesChangedFiles(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repoPath := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatal(err)
	}
	filePath := filepath.Join(repoPath, "notes.md")
	if err := os.WriteFile(filePath, []byte("# Notes\nold-only-token"), 0o644); err != nil {
		t.Fatal(err)
	}

	store, err := db.InitStorage()
	if err != nil {
		t.Fatal(err)
	}
	idx, err := indexer.New(store, nil, repoPath, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := idx.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Register(repoPath, true); err != nil {
		t.Fatal(err)
	}

	nextModTime := time.Now().Add(2 * time.Second)
	if err := os.WriteFile(filePath, []byte("# Notes\nnew-only-token"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filePath, nextModTime, nextModTime); err != nil {
		t.Fatal(err)
	}
	snapshots := make(map[string]map[string]indexer.FileState)
	if _, _, err := syncOnce(context.Background(), snapshots); err != nil {
		t.Fatal(err)
	}

	store, err = db.InitStorage()
	if err != nil {
		t.Fatal(err)
	}
	results, err := store.KeywordSearch(context.Background(), "new-only-token", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("expected changed file content to be indexed")
	}
	oldResults, err := store.KeywordSearch(context.Background(), "old-only-token", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(oldResults) != 0 {
		t.Fatalf("expected old file nodes to be removed, got %d", len(oldResults))
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	if err := os.Remove(filePath); err != nil {
		t.Fatal(err)
	}
	if _, _, err := syncOnce(context.Background(), snapshots); err != nil {
		t.Fatal(err)
	}
	store, err = db.InitStorage()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	nodes, _, err := store.GetAllGraphElements(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 0 {
		t.Fatalf("expected deleted file nodes to be cleaned, got %d", len(nodes))
	}
}
