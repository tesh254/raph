package config

import (
	"os"
	"testing"
)

func TestValidateAppliesDefaultModel(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Vector: VectorSettings{
			CurrentProvider: "openrouter",
			Providers: ProviderContainer{
				OpenRouter: OpenRouterConfig{},
			},
		},
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected config to validate, got %v", err)
	}
	if cfg.Vector.Providers.OpenRouter.Model != defaultEmbeddingModel {
		t.Fatalf("expected default model %q, got %q", defaultEmbeddingModel, cfg.Vector.Providers.OpenRouter.Model)
	}
}

func TestResolveEnv(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "secret")

	cfg := &Config{
		Vector: VectorSettings{
			CurrentProvider: "openrouter",
			Providers: ProviderContainer{
				OpenRouter: OpenRouterConfig{APIKey: "${OPENROUTER_API_KEY}"},
			},
		},
	}
	cfg.resolveEnv()
	if cfg.Vector.Providers.OpenRouter.APIKey != "secret" {
		t.Fatalf("expected env variable to resolve, got %q", cfg.Vector.Providers.OpenRouter.APIKey)
	}

	_ = os.Unsetenv("OPENROUTER_API_KEY")
}
