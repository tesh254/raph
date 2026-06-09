package config

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGenerateEmbeddingRejectsEmptyVector(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"embedding":[]}]}`))
	}))
	defer server.Close()

	cfg := &Config{Vector: VectorSettings{
		CurrentProvider: "openrouter",
		Providers: ProviderContainer{OpenRouter: OpenRouterConfig{
			APIKey:  "test",
			Model:   "test-model",
			BaseURL: server.URL,
		}},
	}}
	if _, err := GenerateEmbedding(context.Background(), cfg, "hello"); err == nil {
		t.Fatal("expected empty embedding to fail")
	}
}
