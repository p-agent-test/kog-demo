// Package memory — Embedder interface and HTTP-based implementation.
// The Embedder converts text into float32 vectors for semantic search.
package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Embedder converts text into a float32 embedding vector.
type Embedder interface {
	// Embed returns the embedding for the given text.
	Embed(ctx context.Context, text string) ([]float32, error)

	// Dimensions returns the output vector length.
	Dimensions() int
}

// NoopEmbedder returns a zero-length vector — useful when no embedding
// backend is configured and semantic search is not needed.
type NoopEmbedder struct{}

func (NoopEmbedder) Embed(_ context.Context, _ string) ([]float32, error) { return nil, nil }
func (NoopEmbedder) Dimensions() int                                       { return 0 }

// HTTPEmbedder calls an OpenAI-compatible embedding endpoint.
// Supports: OpenAI /v1/embeddings, Ollama /api/embeddings, any compatible API.
type HTTPEmbedder struct {
	endpoint   string
	apiKey     string
	model      string
	dimensions int
	client     *http.Client
}

// HTTPEmbedderConfig holds configuration for HTTPEmbedder.
type HTTPEmbedderConfig struct {
	// Endpoint is the full URL, e.g. "https://api.openai.com/v1/embeddings"
	// or "http://localhost:11434/api/embeddings" for Ollama.
	Endpoint string

	// APIKey is the Bearer token. May be empty for local models.
	APIKey string

	// Model name, e.g. "text-embedding-3-small" or "nomic-embed-text".
	Model string

	// Dimensions is the expected output size. 0 = auto-detect from first call.
	Dimensions int

	// Timeout for each HTTP request. Default: 30s.
	Timeout time.Duration
}

// NewHTTPEmbedder creates an HTTPEmbedder from the given config.
func NewHTTPEmbedder(cfg HTTPEmbedderConfig) *HTTPEmbedder {
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	return &HTTPEmbedder{
		endpoint:   cfg.Endpoint,
		apiKey:     cfg.APIKey,
		model:      cfg.Model,
		dimensions: cfg.Dimensions,
		client:     &http.Client{Timeout: cfg.Timeout},
	}
}

// Dimensions returns the configured output vector size.
func (e *HTTPEmbedder) Dimensions() int { return e.dimensions }

// embeddingRequest is the JSON body sent to the endpoint.
// Compatible with OpenAI and Ollama (Ollama uses "prompt" not "input" —
// we send both fields for maximum compatibility).
type embeddingRequest struct {
	Model  string `json:"model"`
	Input  string `json:"input"`  // OpenAI
	Prompt string `json:"prompt"` // Ollama
}

// openAIEmbeddingResponse parses the OpenAI /v1/embeddings response.
type openAIEmbeddingResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	// Ollama returns embedding directly at top level.
	Embedding []float32 `json:"embedding"`
}

// Embed calls the HTTP endpoint and returns the embedding vector.
func (e *HTTPEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	body, err := json.Marshal(embeddingRequest{
		Model:  e.model,
		Input:  text,
		Prompt: text,
	})
	if err != nil {
		return nil, fmt.Errorf("embedder: marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("embedder: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if e.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+e.apiKey)
	}

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embedder: http: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("embedder: http %d: %s", resp.StatusCode, string(raw))
	}

	var parsed openAIEmbeddingResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("embedder: decode: %w", err)
	}

	var vec []float32
	switch {
	case len(parsed.Data) > 0:
		vec = parsed.Data[0].Embedding
	case len(parsed.Embedding) > 0:
		vec = parsed.Embedding
	default:
		return nil, fmt.Errorf("embedder: no embedding in response")
	}

	// Auto-detect dimensions on first call.
	if e.dimensions == 0 {
		e.dimensions = len(vec)
	}

	return vec, nil
}
