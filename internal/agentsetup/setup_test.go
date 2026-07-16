package agentsetup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

func TestUpsertJSONServerDryRunDoesNotWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.json")

	changed, err := upsertJSONServer(path, "$schema", "https://opencode.ai/config.json", "mcp", "raph", opencodeServerValue(), true)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected dry run to report a pending change")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected dry run to leave no file, stat err = %v", err)
	}
}

func TestUpsertJSONServerUpdatesOutdatedEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.json")
	outdated := `{"$schema":"https://opencode.ai/config.json","mcp":{"raph":{"type":"local","command":["raph","start"],"enabled":true}}}`
	if err := os.WriteFile(path, []byte(outdated), 0o644); err != nil {
		t.Fatal(err)
	}

	changed, err := upsertJSONServer(path, "$schema", "https://opencode.ai/config.json", "mcp", "raph", opencodeServerValue(), false)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected an entry missing timeout to be rewritten")
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
	if server["timeout"] != float64(30000) {
		t.Fatalf("expected outdated entry upgraded with timeout, got %v", server)
	}
}

func TestUpsertJSONServerRejectsCorruptConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := upsertJSONServer(path, "$schema", "https://opencode.ai/config.json", "mcp", "raph", opencodeServerValue(), false); err == nil {
		t.Fatal("expected corrupt config to surface a parse error, not be overwritten")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "{not json" {
		t.Fatal("expected corrupt config left untouched")
	}
}

func TestUpsertCodexServerLifecycle(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".codex", "config.toml")

	changed, err := upsertCodexServer(path, true)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected dry run on missing file to report a pending change")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected dry run to leave no file, stat err = %v", err)
	}

	changed, err = upsertCodexServer(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected first write to report a change")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "[mcp_servers.raph]") {
		t.Fatalf("expected raph server section, got %s", data)
	}

	changed, err = upsertCodexServer(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("expected second write to be a no-op")
	}
}

func TestUpsertCodexServerAppendsToExistingConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	existing := "[model]\nname = \"gpt\"\n"
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	changed, err := upsertCodexServer(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected append to report a change")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "[model]") || !strings.Contains(string(data), "[mcp_servers.raph]") {
		t.Fatalf("expected existing content preserved and raph section appended, got %s", data)
	}
}

func TestSetupWritesEveryAgentConfigAndIsIdempotent(t *testing.T) {
	root := t.TempDir()

	result, err := Setup(Options{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Outcomes) != 5 {
		t.Fatalf("expected 5 agent outcomes, got %d", len(result.Outcomes))
	}
	for _, rel := range []string{
		"opencode.json",
		".mcp.json",
		filepath.Join(".codex", "config.toml"),
		filepath.Join(".cursor", "mcp.json"),
		filepath.Join(".pi", "mcp.json"),
	} {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			t.Fatalf("expected %s to be written: %v", rel, err)
		}
	}
	for _, outcome := range result.Outcomes {
		if !outcome.Changed {
			t.Fatalf("expected first run to change %s config", outcome.Name)
		}
	}

	again, err := Setup(Options{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	for _, outcome := range again.Outcomes {
		if outcome.Changed {
			t.Fatalf("expected second run to leave %s config untouched, got message %q", outcome.Name, outcome.Message)
		}
	}
}

func TestSetupDryRunWritesNothing(t *testing.T) {
	root := t.TempDir()

	result, err := Setup(Options{Root: root, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, outcome := range result.Outcomes {
		if !outcome.Changed {
			t.Fatalf("expected dry run to preview a change for %s", outcome.Name)
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, entry := range entries {
		if !entry.IsDir() {
			files = append(files, entry.Name())
		}
	}
	if len(files) != 0 {
		t.Fatalf("expected dry run to write no config files, found %v", files)
	}
}
