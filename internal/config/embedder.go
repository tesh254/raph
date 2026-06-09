package config

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type OpenAIEmbeddingRequest struct {
	Model          string `json:"model"`
	Input          string `json:"input"`
	EncodingFormat string `json:"encoding_format"`
}

type OpenAIEmbeddingResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

func GenerateEmbedding(ctx context.Context, cfg *Config, text string) ([]float32, error) {
	if cfg == nil {
		return nil, fmt.Errorf("embedding config unavailable")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	switch cfg.Vector.CurrentProvider {
	case "openrouter":
		provider := cfg.Vector.Providers.OpenRouter
		if strings.TrimSpace(provider.APIKey) == "" {
			return nil, fmt.Errorf("resolved openrouter api key is empty")
		}
		return generateOpenRouterEmbedding(ctx, provider.APIKey, provider.Model, provider.BaseURL, text)
	default:
		return nil, fmt.Errorf("unsupported embedding provider %q", cfg.Vector.CurrentProvider)
	}
}

func GenerateOpenRouterEmbedding(ctx context.Context, apiKey string, model string, text string) ([]float32, error) {
	return generateOpenRouterEmbedding(ctx, apiKey, model, "", text)
}

func generateOpenRouterEmbedding(ctx context.Context, apiKey string, model string, baseURL string, text string) ([]float32, error) {
	apiURL := strings.TrimSpace(baseURL)
	if apiURL == "" {
		apiURL = "https://openrouter.ai/api/v1/embeddings"
	}
	if model == "" {
		model = defaultEmbeddingModel
	}

	reqBody, err := json.Marshal(OpenAIEmbeddingRequest{
		Model:          model,
		Input:          text,
		EncodingFormat: "float",
	})
	if err != nil {
		return nil, fmt.Errorf("marshal embedding request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, fmt.Errorf("create embedding request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", apiKey))
	req.Header.Set("HTTP-Referer", "https://github.com/tesh254/raph")
	req.Header.Set("X-OpenRouter-Title", "Raph Graph Context Daemon")

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send embedding request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8*1024))
		return nil, fmt.Errorf("openrouter embedding error status %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var openAIResp OpenAIEmbeddingResponse
	if err := json.NewDecoder(resp.Body).Decode(&openAIResp); err != nil {
		return nil, fmt.Errorf("decode embedding response: %w", err)
	}

	if len(openAIResp.Data) == 0 {
		return nil, fmt.Errorf("vector index returned empty array")
	}
	if len(openAIResp.Data[0].Embedding) == 0 {
		return nil, fmt.Errorf("vector index returned an empty embedding")
	}

	return openAIResp.Data[0].Embedding, nil
}
