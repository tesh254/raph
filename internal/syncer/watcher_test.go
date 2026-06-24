package syncer

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRepoWatcherTriggersOnFileWrite(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	trigger := make(chan struct{}, 1)
	w, err := newRepoWatcher(ctx, trigger)
	if err != nil {
		t.Skipf("filesystem watcher unavailable on this platform: %v", err)
	}
	defer w.Close()
	w.ensureDirs([]string{root})

	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	select {
	case <-trigger:
		// Reacted within the debounce window — good.
	case <-time.After(2 * time.Second):
		t.Fatal("watcher did not trigger within 2s of a file write")
	}
}

func TestRepoWatcherIgnoresSkippedDirs(t *testing.T) {
	root := t.TempDir()
	gitDir := filepath.Join(root, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	trigger := make(chan struct{}, 1)
	w, err := newRepoWatcher(ctx, trigger)
	if err != nil {
		t.Skipf("filesystem watcher unavailable: %v", err)
	}
	defer w.Close()
	w.ensureDirs([]string{root})

	// .git must not be watched, so churn inside it should not wake the worker.
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case <-trigger:
		t.Fatal("watcher woke on a change inside a skipped directory")
	case <-time.After(500 * time.Millisecond):
		// Correctly ignored.
	}
}
