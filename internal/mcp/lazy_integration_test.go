package mcp

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"raph/internal/db"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// connectLazyClient wires a wrapper around the given lazy store to an
// in-memory MCP client session, mirroring how raph start serves stdio.
func connectLazyClient(t *testing.T, store db.GraphStore) *mcpsdk.ClientSession {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	wrapper := NewMCPServerWrapper(store, nil)
	clientTransport, serverTransport := mcpsdk.NewInMemoryTransports()
	go func() { _ = wrapper.server.Run(ctx, serverTransport) }()

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "raph-test", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// TestHandshakeAndToolDiscoveryDoNotOpenStore is the regression test for the
// opencode "server unavailable" failure: MCP clients time out servers whose
// initialize/tools listing waits on the database, so neither may touch it.
func TestHandshakeAndToolDiscoveryDoNotOpenStore(t *testing.T) {
	var opens int32
	lazy := db.NewLazyStore(func() (db.GraphStore, error) {
		atomic.AddInt32(&opens, 1)
		return newProtocolStore(), nil
	})

	session := connectLazyClient(t, lazy)

	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) == 0 {
		t.Fatal("expected tools to be listed")
	}
	if got := atomic.LoadInt32(&opens); got != 0 {
		t.Fatalf("expected handshake and tool discovery to leave the store unopened, got %d opens", got)
	}

	result, err := session.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "hybrid_semantic_search",
		Arguments: map[string]any{"query": "anything"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("expected tool call to succeed through the lazy store: %+v", result.Content)
	}
	if got := atomic.LoadInt32(&opens); got != 1 {
		t.Fatalf("expected first tool call to open the store once, got %d opens", got)
	}
}

// TestToolCallSurfacesOpenErrorAndRecovers pins the no-latch contract: a
// transient open failure (e.g. SQLITE_BUSY while the sync worker holds the
// write lock) fails that one call and the next call retries.
func TestToolCallSurfacesOpenErrorAndRecovers(t *testing.T) {
	var opens int32
	lazy := db.NewLazyStore(func() (db.GraphStore, error) {
		if atomic.AddInt32(&opens, 1) == 1 {
			return nil, errors.New("database is locked")
		}
		return newProtocolStore(), nil
	})

	session := connectLazyClient(t, lazy)
	params := &mcpsdk.CallToolParams{
		Name:      "hybrid_semantic_search",
		Arguments: map[string]any{"query": "anything"},
	}

	result, err := session.CallTool(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("expected first tool call to surface the open failure as a tool error")
	}

	result, err = session.CallTool(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("expected retry after a failed open to succeed: %+v", result.Content)
	}
	if got := atomic.LoadInt32(&opens); got != 2 {
		t.Fatalf("expected two open attempts, got %d", got)
	}
}
