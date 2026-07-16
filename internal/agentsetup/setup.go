package agentsetup

import (
	"bytes"
	_ "embed"
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

// opencodePluginSource is the raph opencode plugin, installed alongside the
// MCP entry. opencode auto-loads .ts files from .opencode/plugins/ (project)
// and ~/.config/opencode/plugins/ (global), so no config entry is needed.
//
//go:embed opencode_plugin.ts
var opencodePluginSource []byte

const (
	// ScopeGlobal installs the raph MCP entry into each agent's user-level
	// config, so every project picks it up. This is the default.
	ScopeGlobal = "global"
	// ScopeLocal installs into project files under the chosen root.
	ScopeLocal = "local"
)

// ParseScope normalizes a user-supplied scope answer. Empty input means the
// default (global); a leading "g" or "l" is enough, so prompt answers like
// "G" or "l" work.
func ParseScope(input string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(input)) {
	case "", "g", ScopeGlobal:
		return ScopeGlobal, nil
	case "l", ScopeLocal:
		return ScopeLocal, nil
	default:
		return "", fmt.Errorf("invalid scope %q: use %q or %q", input, ScopeGlobal, ScopeLocal)
	}
}

type Options struct {
	Root   string
	DryRun bool
	Scope  string
}

type Outcome struct {
	Name       string
	Binary     string
	Installed  bool
	ConfigPath string
	PluginPath string
	Changed    bool
	Message    string
}

type Result struct {
	Root     string
	Scope    string
	Outcomes []Outcome
}

type agentSpec struct {
	Name             string
	Binary           string
	LocalConfigPath  func(root string) string
	GlobalConfigPath func() (string, error)
	Write            func(path string, dryRun bool) (bool, error)
	// Plugin paths are optional; agents that support a raph plugin get the
	// embedded source installed next to their MCP entry.
	PluginLocalPath  func(root string) string
	PluginGlobalPath func() (string, error)
	PluginSource     []byte
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
	scope, err := ParseScope(opts.Scope)
	if err != nil {
		return Result{}, err
	}
	verbose.Printf("agents mcp setup root=%s scope=%s dryRun=%t", absRoot, scope, opts.DryRun)

	specs := []agentSpec{
		{
			Name:   "opencode",
			Binary: "opencode",
			LocalConfigPath: func(root string) string {
				return filepath.Join(root, "opencode.json")
			},
			GlobalConfigPath: func() (string, error) {
				dir, err := opencodeGlobalDir()
				if err != nil {
					return "", err
				}
				return filepath.Join(dir, "opencode.json"), nil
			},
			PluginLocalPath: func(root string) string {
				return filepath.Join(root, ".opencode", "plugins", "raph.ts")
			},
			PluginGlobalPath: func() (string, error) {
				dir, err := opencodeGlobalDir()
				if err != nil {
					return "", err
				}
				return filepath.Join(dir, "plugins", "raph.ts"), nil
			},
			PluginSource: opencodePluginSource,
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
			LocalConfigPath: func(root string) string {
				return filepath.Join(root, ".mcp.json")
			},
			// User-scope MCP servers live in ~/.claude.json under the same
			// mcpServers key the project .mcp.json uses.
			GlobalConfigPath: homeConfigPath(".claude.json"),
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
			LocalConfigPath: func(root string) string {
				return filepath.Join(root, ".codex", "config.toml")
			},
			GlobalConfigPath: homeConfigPath(".codex", "config.toml"),
			Write: func(path string, dryRun bool) (bool, error) {
				return upsertCodexServer(path, dryRun)
			},
		},
		{
			Name:   "cursor",
			Binary: "cursor",
			LocalConfigPath: func(root string) string {
				return filepath.Join(root, ".cursor", "mcp.json")
			},
			GlobalConfigPath: homeConfigPath(".cursor", "mcp.json"),
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
			LocalConfigPath: func(root string) string {
				return filepath.Join(root, ".pi", "mcp.json")
			},
			GlobalConfigPath: homeConfigPath(".pi", "mcp.json"),
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

	result := Result{Root: absRoot, Scope: scope}
	for _, spec := range specs {
		binaryPath, err := exec.LookPath(spec.Binary)
		installed := err == nil
		configPath := spec.LocalConfigPath(absRoot)
		if scope == ScopeGlobal {
			configPath, err = spec.GlobalConfigPath()
			if err != nil {
				return Result{}, fmt.Errorf("%s global config path: %w", spec.Name, err)
			}
		}

		verbose.Printf("agent=%s installed=%t scope=%s configPath=%s", spec.Name, installed, scope, configPath)
		changed, writeErr := spec.Write(configPath, opts.DryRun)
		if writeErr != nil {
			return Result{}, fmt.Errorf("%s config: %w", spec.Name, writeErr)
		}

		pluginPath := ""
		if spec.PluginLocalPath != nil {
			pluginPath = spec.PluginLocalPath(absRoot)
			if scope == ScopeGlobal {
				if spec.PluginGlobalPath == nil {
					return Result{}, fmt.Errorf("%s global plugin path: not configured", spec.Name)
				}
				pluginPath, err = spec.PluginGlobalPath()
				if err != nil {
					return Result{}, fmt.Errorf("%s global plugin path: %w", spec.Name, err)
				}
			}
			verbose.Printf("agent=%s pluginPath=%s", spec.Name, pluginPath)
			pluginChanged, pluginErr := writeFileIfChanged(pluginPath, spec.PluginSource, opts.DryRun)
			if pluginErr != nil {
				return Result{}, fmt.Errorf("%s plugin: %w", spec.Name, pluginErr)
			}
			changed = changed || pluginChanged
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
			PluginPath: pluginPath,
			Changed:    changed,
			Message:    message,
		})
	}

	return result, nil
}

// opencodeGlobalDir returns opencode's user-level config directory. opencode
// reads from the XDG config directory on every platform, not the OS-native
// one.
func opencodeGlobalDir() (string, error) {
	if xdg := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); xdg != "" {
		return filepath.Join(xdg, "opencode"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".config", "opencode"), nil
}

// writeFileIfChanged writes content to path unless the file already matches.
// Directories are created only when a write is certain, so dry runs and
// no-op runs leave the filesystem untouched.
func writeFileIfChanged(path string, content []byte, dryRun bool) (bool, error) {
	existing, err := os.ReadFile(path)
	if err == nil && bytes.Equal(existing, content) {
		return false, nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	if dryRun {
		return true, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, fmt.Errorf("create plugin directory: %w", err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return false, fmt.Errorf("write %s: %w", path, err)
	}
	return true, nil
}

func upsertJSONServer(path string, schemaKey string, schemaValue string, containerKey string, serverName string, serverValue map[string]any, dryRun bool) (bool, error) {
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
	// Create the directory only once a write is certain, so dry runs and
	// no-op runs leave the filesystem untouched.
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, fmt.Errorf("create config directory: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return false, fmt.Errorf("write %s: %w", path, err)
	}
	return true, nil
}

// homeConfigPath returns a resolver for a config file rooted at the user's
// home directory, e.g. homeConfigPath(".cursor", "mcp.json").
func homeConfigPath(elem ...string) func() (string, error) {
	return func() (string, error) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		return filepath.Join(append([]string{home}, elem...)...), nil
	}
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
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, fmt.Errorf("create config directory: %w", err)
	}
	if err := os.WriteFile(path, next, 0o644); err != nil {
		return false, fmt.Errorf("write %s: %w", path, err)
	}
	return true, nil
}
