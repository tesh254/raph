package agentsetup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func opencodeServerValue() map[string]any {
	return map[string]any{
		"type":    "local",
		"command": []string{"raph", "start"},
		"enabled": true,
		"timeout": 30000,
	}
}

func TestUpsertJSONServerWritesOpencodeEntryWithTimeout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.json")

	changed, err := upsertJSONServer(path, "$schema", "https://opencode.ai/config.json", "mcp", "raph", opencodeServerValue(), false)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected first upsert to report a change")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	server, _ := root["mcp"].(map[string]any)["raph"].(map[string]any)
	if server == nil {
		t.Fatalf("expected mcp.raph entry, got %s", data)
	}
	if server["timeout"] != float64(30000) {
		t.Fatalf("expected timeout 30000 in opencode entry, got %v", server["timeout"])
	}
}

func TestUpsertJSONServerIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.json")

	if _, err := upsertJSONServer(path, "$schema", "https://opencode.ai/config.json", "mcp", "raph", opencodeServerValue(), false); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	changed, err := upsertJSONServer(path, "$schema", "https://opencode.ai/config.json", "mcp", "raph", opencodeServerValue(), false)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("expected second upsert with identical values to report no change")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("expected file contents to be untouched on a no-op upsert")
	}
}

func TestUpsertJSONServerPreservesExistingConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.json")
	existing := `{"$schema":"https://opencode.ai/config.json","plugin":["./x.ts"],"mcp":{"other":{"type":"remote","url":"https://example.com/mcp"}}}`
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := upsertJSONServer(path, "$schema", "https://opencode.ai/config.json", "mcp", "raph", opencodeServerValue(), false); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	if _, ok := root["plugin"]; !ok {
		t.Fatalf("expected existing plugin key preserved, got %s", data)
	}
	mcp, _ := root["mcp"].(map[string]any)
	if _, ok := mcp["other"]; !ok {
		t.Fatalf("expected existing mcp.other entry preserved, got %s", data)
	}
	if _, ok := mcp["raph"]; !ok {
		t.Fatalf("expected mcp.raph entry added, got %s", data)
	}
}
