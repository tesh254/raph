package memory

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"strings"

	"raph/internal/config"
	"raph/internal/db"
)

const workspaceID = "memory:agent"

type StoreInput struct {
	Key     string
	Title   string
	Content string
}

type StoreOutput struct {
	Node     db.Node `json:"node"`
	Embedded bool    `json:"embedded"`
}

func Store(ctx context.Context, store db.GraphStore, cfg *config.Config, input StoreInput) (StoreOutput, error) {
	title := strings.TrimSpace(input.Title)
	content := strings.TrimSpace(input.Content)
	if content == "" {
		return StoreOutput{}, fmt.Errorf("content is required")
	}
	if title == "" {
		title = preview(content, 80)
	}

	key := strings.TrimSpace(input.Key)
	if key == "" {
		key = title + "\n" + content
	}
	sum := sha1.Sum([]byte(key))
	id := "memory:" + hex.EncodeToString(sum[:])

	node := db.Node{
		ID:        id,
		Workspace: workspaceID,
		Domain:    "memory",
		Type:      "memory",
		Name:      title,
		Content:   content,
		URL:       "memory://" + hex.EncodeToString(sum[:]),
	}

	if cfg != nil && cfg.HasEmbeddingProvider() {
		embedding, err := config.GenerateEmbedding(ctx, cfg, title+"\n\n"+content)
		if err != nil {
			return StoreOutput{}, fmt.Errorf("generate memory embedding: %w", err)
		}
		node.Embedding = embedding
		node.EmbeddingLength = len(embedding)
	}

	if err := store.SaveNode(ctx, node); err != nil {
		return StoreOutput{}, fmt.Errorf("save memory: %w", err)
	}
	node.Embedding = nil
	return StoreOutput{Node: node, Embedded: node.EmbeddingLength > 0}, nil
}

func preview(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "..."
}
