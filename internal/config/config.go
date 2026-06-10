package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var ErrConfigNotFound = errors.New("raph config file not found")

type Config struct {
	Vector  VectorSettings  `json:"vector"`
	Project ProjectSettings `json:"project,omitempty"`
}

type ProjectSettings struct {
	IdentityOverride string `json:"identity_override,omitempty"`
}

type VectorSettings struct {
	CurrentProvider string            `json:"current_provider"`
	Providers       ProviderContainer `json:"providers"`
}

type ProviderContainer struct {
	OpenRouter OpenRouterConfig `json:"openrouter"`
}

type OpenRouterConfig struct {
	APIKey  string `json:"api_key"`
	Model   string `json:"model"`
	BaseURL string `json:"base_url,omitempty"`
}

type Paths struct {
	BaseDir    string
	SchemaFile string
	ConfigFile string
	DataDir    string
}

const defaultEmbeddingModel = "openai/text-embedding-3-small"

const DefaultSchemaJSON = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "title": "RaphConfig",
  "type": "object",
  "properties": {
    "vector": {
      "type": "object",
      "properties": {
        "current_provider": { "type": "string", "enum": ["openrouter"] },
        "providers": {
          "type": "object",
          "properties": {
            "openrouter": {
              "type": "object",
              "properties": {
                "api_key": {
                  "type": "string",
                  "description": "Supports literal strings or environment variables like ${OPENROUTER_API_KEY}"
                },
                "model": {
                  "type": "string",
                  "default": "openai/text-embedding-3-small"
                }
              },
              "required": ["api_key"]
            }
          },
          "required": ["openrouter"]
        }
      },
      "required": ["current_provider", "providers"]
    },
    "project": {
      "type": "object",
      "properties": {
        "identity_override": {
          "type": "string",
          "description": "Optional stable project identity used to share project-scoped memory across different checkouts."
        }
      }
    }
  },
  "required": ["vector"]
}
`

const DefaultConfigJSON = `{
  "$schema": "./schema.json",
  "vector": {
    "current_provider": "openrouter",
    "providers": {
      "openrouter": {
        "api_key": "${OPENROUTER_API_KEY}",
        "model": "openai/text-embedding-3-small"
      }
    }
  },
  "project": {
    "identity_override": ""
  }
}
`

func GetConfigPaths() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, fmt.Errorf("unable to resolve user home directory: %w", err)
	}

	baseDir := filepath.Join(home, ".raph")
	return Paths{
		BaseDir:    baseDir,
		SchemaFile: filepath.Join(baseDir, "schema.json"),
		ConfigFile: filepath.Join(baseDir, "raph.json"),
		DataDir:    filepath.Join(baseDir, "data"),
	}, nil
}

func EnsureBaseLayout() (Paths, error) {
	paths, err := GetConfigPaths()
	if err != nil {
		return Paths{}, err
	}

	for _, dir := range []string{paths.BaseDir, paths.DataDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return Paths{}, fmt.Errorf("failed creating %s: %w", dir, err)
		}
	}

	return paths, nil
}

func WriteDefaultFiles(overwrite bool) (Paths, error) {
	paths, err := EnsureBaseLayout()
	if err != nil {
		return Paths{}, err
	}

	if err := writeIfNeeded(paths.SchemaFile, DefaultSchemaJSON, overwrite); err != nil {
		return Paths{}, err
	}
	if err := writeIfNeeded(paths.ConfigFile, DefaultConfigJSON, overwrite); err != nil {
		return Paths{}, err
	}

	return paths, nil
}

func writeIfNeeded(path string, content string, overwrite bool) error {
	if !overwrite {
		if _, err := os.Stat(path); err == nil {
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("stat %s: %w", path, err)
		}
	}

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func LoadConfig() (*Config, error) {
	paths, err := EnsureBaseLayout()
	if err != nil {
		return nil, err
	}

	fileBytes, err := os.ReadFile(paths.ConfigFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: run `raph config init` to create %s", ErrConfigNotFound, paths.ConfigFile)
		}
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(fileBytes, &cfg); err != nil {
		return nil, fmt.Errorf("malformed JSON config: %w", err)
	}

	cfg.resolveEnv()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func LoadConfigIfPresent() (*Config, error) {
	cfg, err := LoadConfig()
	if err == nil {
		return cfg, nil
	}
	if errors.Is(err, ErrConfigNotFound) {
		return nil, nil
	}
	return nil, err
}

func (c *Config) resolveEnv() {
	c.Vector.Providers.OpenRouter.APIKey = strings.TrimSpace(os.ExpandEnv(c.Vector.Providers.OpenRouter.APIKey))
	c.Vector.Providers.OpenRouter.Model = strings.TrimSpace(c.Vector.Providers.OpenRouter.Model)
	c.Vector.Providers.OpenRouter.BaseURL = strings.TrimSpace(os.ExpandEnv(c.Vector.Providers.OpenRouter.BaseURL))
	c.Project.IdentityOverride = strings.TrimSpace(os.ExpandEnv(c.Project.IdentityOverride))
}

func (c *Config) Validate() error {
	if c == nil {
		return errors.New("config is nil")
	}

	if strings.TrimSpace(c.Vector.CurrentProvider) == "" {
		return errors.New("vector.current_provider is required")
	}
	if c.Vector.CurrentProvider != "openrouter" {
		return fmt.Errorf("unsupported vector.current_provider %q", c.Vector.CurrentProvider)
	}
	if strings.TrimSpace(c.Vector.Providers.OpenRouter.Model) == "" {
		c.Vector.Providers.OpenRouter.Model = defaultEmbeddingModel
	}
	return nil
}

func (c *Config) HasEmbeddingProvider() bool {
	if c == nil {
		return false
	}
	return strings.TrimSpace(c.Vector.Providers.OpenRouter.APIKey) != ""
}
