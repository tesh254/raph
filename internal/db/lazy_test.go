package db

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

func TestLazyStoreDefersOpenUntilFirstCall(t *testing.T) {
	opens := 0
	lazy := NewLazyStore(func() (GraphStore, error) {
		opens++
		return newTestStore(t), nil
	})

	if opens != 0 {
		t.Fatalf("expected no open before first use, got %d", opens)
	}

	ctx := context.Background()
	if err := lazy.SaveNode(ctx, Node{ID: "n", Workspace: "ws", Domain: "code", Type: "file", Name: "n.go"}); err != nil {
		t.Fatal(err)
	}
	if _, err := lazy.GetNodeByID(ctx, "n"); err != nil {
		t.Fatal(err)
	}
	if opens != 1 {
		t.Fatalf("expected exactly one open across calls, got %d", opens)
	}
}

func TestLazyStoreCloseWithoutUseDoesNotOpen(t *testing.T) {
	opens := 0
	lazy := NewLazyStore(func() (GraphStore, error) {
		opens++
		return newTestStore(t), nil
	})
	if err := lazy.Close(); err != nil {
		t.Fatal(err)
	}
	if opens != 0 {
		t.Fatalf("expected Close to not open the store, got %d opens", opens)
	}
}

func TestLazyStoreRetriesAfterOpenFailure(t *testing.T) {
	opens := 0
	lazy := NewLazyStore(func() (GraphStore, error) {
		opens++
		if opens == 1 {
			return nil, errors.New("transient: database is locked")
		}
		return newTestStore(t), nil
	})

	ctx := context.Background()
	if err := lazy.SaveNode(ctx, Node{ID: "n", Workspace: "ws", Domain: "code", Type: "file", Name: "n.go"}); err == nil {
		t.Fatal("expected first call to surface the open error")
	}
	if err := lazy.SaveNode(ctx, Node{ID: "n", Workspace: "ws", Domain: "code", Type: "file", Name: "n.go"}); err != nil {
		t.Fatalf("expected retry after failed open to succeed: %v", err)
	}
	if opens != 2 {
		t.Fatalf("expected two open attempts, got %d", opens)
	}
}

func TestLazyStoreCloseClosesUnderlyingStore(t *testing.T) {
	rawDB, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	store := &LibSQLStore{db: rawDB}
	if err := store.migrateIfNeeded(); err != nil {
		t.Fatal(err)
	}

	lazy := NewLazyStore(func() (GraphStore, error) { return store, nil })
	ctx := context.Background()
	if err := lazy.SaveNode(ctx, Node{ID: "n", Workspace: "ws", Domain: "code", Type: "file", Name: "n.go"}); err != nil {
		t.Fatal(err)
	}
	if err := lazy.Close(); err != nil {
		t.Fatal(err)
	}
	if err := rawDB.Ping(); err == nil {
		t.Fatal("expected underlying database handle to be closed")
	}
}
