package agentsetup

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"

	"raph/internal/verbose"
)

type Options struct {
	Root   string
	DryRun bool
}

type Outcome struct {
	Name       string
	Binary     string
	Installed  bool
	ConfigPath string
	Changed    bool
	Message    string
}

type Result struct {
	Root     string
	Outcomes []Outcome
}

type agentSpec struct {
	Name       string
	Binary     string
	ConfigPath func(root string) string
	Write      func(path string, dryRun bool) (bool, error)
}

func Setup(opts Options) (Result, error) {
	root := strings.TrimSpace(opts.Root)
	if root == "" {
		root = "."
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		return Result{}, fmt.Errorf("resolve project root: %w", err)
	}
	verbose.Printf("agents mcp setup root=%s dryRun=%t", absRoot, opts.DryRun)

	specs := []agentSpec{
		{
			Name:   "opencode",
			Binary: "opencode",
			ConfigPath: func(root string) string {
				return filepath.Join(root, "opencode.json")
			},
			Write: func(path string, dryRun bool) (bool, error) {
				return upsertJSONServer(path, "$schema", "https://opencode.ai/config.json", "mcp", "raph", map[string]any{
					"type":    "local",
					"command": []string{"raph", "start"},
					"enabled": true,
					// opencode gives local servers 5000ms (its default) to
					// finish the MCP handshake and tool discovery; a cold
					// raph start can exceed that when brain.db is contended,
					// so give it room.
					"timeout": 30000,
				}, dryRun)
			},
		},
		{
			Name:   "claude code",
			Binary: "claude",
			ConfigPath: func(root string) string {
				return filepath.Join(root, ".mcp.json")
			},
			Write: func(path string, dryRun bool) (bool, error) {
				return upsertJSONServer(path, "", "", "mcpServers", "raph", map[string]any{
					"type":    "stdio",
					"command": "raph",
					"args":    []string{"start"},
					"env":     map[string]any{},
				}, dryRun)
			},
		},
		{
			Name:   "codex",
			Binary: "codex",
			ConfigPath: func(root string) string {
				return filepath.Join(root, ".codex", "config.toml")
			},
			Write: func(path string, dryRun bool) (bool, error) {
				return upsertCodexServer(path, dryRun)
			},
		},
		{
			Name:   "cursor",
			Binary: "cursor",
			ConfigPath: func(root string) string {
				return filepath.Join(root, ".cursor", "mcp.json")
			},
			Write: func(path string, dryRun bool) (bool, error) {
				return upsertJSONServer(path, "", "", "mcpServers", "raph", map[string]any{
					"type":    "stdio",
					"command": "raph",
					"args":    []string{"start"},
					"env":     map[string]any{},
				}, dryRun)
			},
		},
		{
			Name:   "pi",
			Binary: "pi",
			ConfigPath: func(root string) string {
				return filepath.Join(root, ".pi", "mcp.json")
			},
			Write: func(path string, dryRun bool) (bool, error) {
				return upsertJSONServer(path, "", "", "mcpServers", "raph", map[string]any{
					"type":    "stdio",
					"command": "raph",
					"args":    []string{"start"},
					"env":     map[string]any{},
				}, dryRun)
			},
		},
	}

	result := Result{Root: absRoot}
	for _, spec := range specs {
		binaryPath, err := exec.LookPath(spec.Binary)
		installed := err == nil
		configPath := spec.ConfigPath(absRoot)

		verbose.Printf("agent=%s installed=%t configPath=%s", spec.Name, installed, configPath)
		changed, writeErr := spec.Write(configPath, opts.DryRun)
		if writeErr != nil {
			return Result{}, fmt.Errorf("%s config: %w", spec.Name, writeErr)
		}

		message := "updated"
		if opts.DryRun {
			message = "previewed"
		}
		if !changed {
			message = "already current"
		}
		if !installed {
			message += "; binary missing"
		} else {
			message += "; binary at " + binaryPath
		}

		result.Outcomes = append(result.Outcomes, Outcome{
			Name:       spec.Name,
			Binary:     binaryPath,
			Installed:  installed,
			ConfigPath: configPath,
			Changed:    changed,
			Message:    message,
		})
	}

	return result, nil
}

func upsertJSONServer(path string, schemaKey string, schemaValue string, containerKey string, serverName string, serverValue map[string]any, dryRun bool) (bool, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, fmt.Errorf("create config directory: %w", err)
	}

	// Round-trip the desired value through JSON so the DeepEqual below compares
	// like with like: values parsed from disk are map[string]any/[]any/float64,
	// while literals here carry Go types ([]string, int). Without this the
	// comparison never matches and every run reports "updated".
	normalized, err := normalizeJSONValue(serverValue)
	if err != nil {
		return false, fmt.Errorf("normalize %s server entry: %w", serverName, err)
	}
	serverValue, _ = normalized.(map[string]any)

	root := map[string]any{}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &root); err != nil {
			return false, fmt.Errorf("parse %s: %w", path, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("read %s: %w", path, err)
	}

	changed := false
	if schemaKey != "" {
		if existing, ok := root[schemaKey]; !ok || existing != schemaValue {
			root[schemaKey] = schemaValue
			changed = true
		}
	}

	container, _ := root[containerKey].(map[string]any)
	if container == nil {
		container = map[string]any{}
		root[containerKey] = container
		changed = true
	}

	if existing, ok := container[serverName]; !ok || !reflect.DeepEqual(existing, serverValue) {
		container[serverName] = serverValue
		changed = true
	}

	if !changed {
		return false, nil
	}
	if dryRun {
		return true, nil
	}

	data, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return false, fmt.Errorf("encode %s: %w", path, err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return false, fmt.Errorf("write %s: %w", path, err)
	}
	return true, nil
}

func normalizeJSONValue(value any) (any, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func upsertCodexServer(path string, dryRun bool) (bool, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, fmt.Errorf("create config directory: %w", err)
	}

	section := strings.Join([]string{
		"[mcp_servers.raph]",
		`command = "raph"`,
		`args = ["start"]`,
		"",
	}, "\n")

	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	if err == nil && (strings.Contains(string(data), "[mcp_servers.raph]") || strings.Contains(string(data), "[mcp_servers.\"raph\"]")) {
		return false, nil
	}

	if dryRun {
		return true, nil
	}

	var next []byte
	if len(data) == 0 {
		next = []byte(section)
	} else {
		next = append(append(append([]byte(strings.TrimRight(string(data), "\n")), '\n'), []byte(section)...), '\n')
	}
	if err := os.WriteFile(path, next, 0o644); err != nil {
		return false, fmt.Errorf("write %s: %w", path, err)
	}
	return true, nil
}
