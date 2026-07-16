//go:build !windows

package main_test

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// The e2e tests build the real raph binary and drive `raph start` over stdio
// exactly like an MCP client (opencode, claude code, cursor) does: spawn,
// initialize, tools/list, tools/call. Each test runs against an isolated
// temporary HOME so ~/.raph never touches the developer's real data.

var (
	buildOnce sync.Once
	buildBin  string
	buildErr  error
)

func raphBinary(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "raph-e2e-bin-")
		if err != nil {
			buildErr = err
			return
		}
		buildBin = filepath.Join(dir, "raph")
		cmd := exec.Command("go", "build", "-o", buildBin, ".")
		out, err := cmd.CombinedOutput()
		if err != nil {
			buildErr = fmt.Errorf("go build: %v\n%s", err, out)
		}
	})
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	return buildBin
}

type mcpProcess struct {
	t      *testing.T
	cmd    *exec.Cmd
	stdin  *json.Encoder
	lines  chan string
	nextID int
}

// startServer spawns `raph start` with HOME pointed at home and returns a
// handle that speaks line-delimited JSON-RPC. The sync worker the server
// spawns is killed on cleanup via its pid file.
func startServer(t *testing.T, home string) *mcpProcess {
	t.Helper()
	cmd := exec.Command(raphBinary(t), "start")
	cmd.Env = append(os.Environ(), "HOME="+home, "USERPROFILE="+home)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	lines := make(chan string, 16)
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 1024*1024), 16*1024*1024)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		close(lines)
	}()

	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		killSyncWorker(home)
		// Surface server-side diagnostics only when something went wrong;
		// stderr is safe to read here because Wait has returned.
		if t.Failed() && stderr.Len() > 0 {
			t.Logf("raph start stderr:\n%s", stderr.String())
		}
	})

	return &mcpProcess{t: t, cmd: cmd, stdin: json.NewEncoder(stdin), lines: lines}
}

func killSyncWorker(home string) {
	data, err := os.ReadFile(filepath.Join(home, ".raph", "data", "sync.pid"))
	if err != nil {
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
}

func (p *mcpProcess) send(obj map[string]any) {
	p.t.Helper()
	if err := p.stdin.Encode(obj); err != nil {
		p.t.Fatalf("write to raph start stdin: %v", err)
	}
}

// call sends a request and waits for the response with the matching id,
// skipping any notifications the server emits in between. The deadline is the
// test's assertion: an MCP client would have given up after its own timeout.
func (p *mcpProcess) call(method string, params map[string]any, deadline time.Duration) map[string]any {
	p.t.Helper()
	p.nextID++
	id := p.nextID
	req := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		req["params"] = params
	}
	p.send(req)

	timeout := time.After(deadline)
	for {
		select {
		case line, ok := <-p.lines:
			if !ok {
				p.t.Fatalf("raph start closed stdout while waiting for %s response", method)
			}
			var msg map[string]any
			if err := json.Unmarshal([]byte(line), &msg); err != nil {
				p.t.Fatalf("non-JSON line on stdout (breaks MCP framing): %q", line)
			}
			if got, ok := msg["id"].(float64); ok && int(got) == id {
				if errObj, ok := msg["error"]; ok {
					p.t.Fatalf("%s returned JSON-RPC error: %v", method, errObj)
				}
				result, _ := msg["result"].(map[string]any)
				if result == nil {
					p.t.Fatalf("%s response missing result: %v", method, msg)
				}
				return result
			}
		case <-timeout:
			p.t.Fatalf("no %s response within %s", method, deadline)
		}
	}
}

func (p *mcpProcess) handshake(deadline time.Duration) map[string]any {
	p.t.Helper()
	result := p.call("initialize", map[string]any{
		"protocolVersion": "2025-03-26",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "raph-e2e", "version": "1.0.0"},
	}, deadline)
	p.send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	return result
}

func (p *mcpProcess) callTool(name string, args map[string]any, deadline time.Duration) map[string]any {
	p.t.Helper()
	return p.call("tools/call", map[string]any{"name": name, "arguments": args}, deadline)
}

func toolNames(result map[string]any) map[string]bool {
	names := map[string]bool{}
	tools, _ := result["tools"].([]any)
	for _, raw := range tools {
		tool, _ := raw.(map[string]any)
		if name, _ := tool["name"].(string); name != "" {
			names[name] = true
		}
	}
	return names
}

func TestE2EHandshakeToolCallAndPersistence(t *testing.T) {
	home := t.TempDir()

	server := startServer(t, home)
	init := server.handshake(10 * time.Second)
	serverInfo, _ := init["serverInfo"].(map[string]any)
	if serverInfo == nil || serverInfo["name"] != "raph" {
		t.Fatalf("unexpected serverInfo: %v", init)
	}

	names := toolNames(server.call("tools/list", nil, 10*time.Second))
	for _, name := range []string{"store_memory", "search_shared_knowledge", "hybrid_semantic_search", "list_rules"} {
		if !names[name] {
			t.Fatalf("expected tool %q in listing, got %v", name, names)
		}
	}

	// First tool call opens ~/.raph/data/brain.db lazily and runs migrations.
	result := server.callTool("store_memory", map[string]any{
		"scope_type":     "shared",
		"scope_id":       "e2e-team",
		"knowledge_type": "decision",
		"memory_key":     "opencode-timeout",
		"title":          "opencode needs a 30s MCP timeout",
		"content":        "raph start migrations can exceed opencode's 5s default handshake window.",
		"source":         "user",
		"writer_id":      "agent:e2e",
	}, 30*time.Second)
	if isError, _ := result["isError"].(bool); isError {
		t.Fatalf("store_memory failed: %v", result)
	}
	if _, err := os.Stat(filepath.Join(home, ".raph", "data", "brain.db")); err != nil {
		t.Fatalf("expected brain.db created under temp home: %v", err)
	}

	// A fresh process must see the stored memory: proves it was persisted, not
	// held in process state.
	restarted := startServer(t, home)
	restarted.handshake(10 * time.Second)
	// The knowledge query is a literal substring match over title/content.
	result = restarted.callTool("search_shared_knowledge", map[string]any{
		"query":    "handshake window",
		"scope_id": "e2e-team",
	}, 30*time.Second)
	if isError, _ := result["isError"].(bool); isError {
		t.Fatalf("search_shared_knowledge failed: %v", result)
	}
	body, _ := json.Marshal(result)
	if !strings.Contains(string(body), "opencode-timeout") {
		t.Fatalf("expected stored memory to survive a restart, got %s", body)
	}
}

// TestE2EHandshakeSucceedsWhileDatabaseLocked reproduces the opencode failure:
// the sync worker held brain.db's write lock while opencode booted, and the
// old eager-migration startup blocked past the client's timeout. The
// handshake and tool discovery must now succeed while another connection
// holds BEGIN IMMEDIATE.
func TestE2EHandshakeSucceedsWhileDatabaseLocked(t *testing.T) {
	home := t.TempDir()

	// Prime the database (create + migrate + stamp) with one throwaway server.
	primer := startServer(t, home)
	primer.handshake(10 * time.Second)
	result := primer.callTool("list_rules", map[string]any{"scope": "global", "limit": 1}, 30*time.Second)
	if isError, _ := result["isError"].(bool); isError {
		t.Fatalf("priming tool call failed: %v", result)
	}
	_ = primer.cmd.Process.Kill()
	_, _ = primer.cmd.Process.Wait()

	// Hold the write lock the way a mid-index sync worker does.
	dbFile := filepath.Join(home, ".raph", "data", "brain.db")
	locker, err := sql.Open("sqlite", dbFile)
	if err != nil {
		t.Fatal(err)
	}
	defer locker.Close()
	ctx := context.Background()
	conn, err := locker.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	unlocked := false
	unlock := func() {
		if !unlocked {
			unlocked = true
			_, _ = conn.ExecContext(ctx, "ROLLBACK")
		}
	}
	defer unlock()

	// The 5s deadline mirrors opencode's default handshake window; the old
	// startup path blocked here until the lock holder finished.
	server := startServer(t, home)
	server.handshake(5 * time.Second)
	names := toolNames(server.call("tools/list", nil, 5*time.Second))
	if !names["store_memory"] {
		t.Fatalf("expected full tool listing while the database is locked, got %v", names)
	}

	// Once the writer finishes, the same session must serve tool calls.
	unlock()
	result = server.callTool("list_rules", map[string]any{"scope": "global", "limit": 1}, 30*time.Second)
	if isError, _ := result["isError"].(bool); isError {
		t.Fatalf("tool call after lock release failed: %v", result)
	}
}
