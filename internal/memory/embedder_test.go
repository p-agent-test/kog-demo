package memory_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/p-blackswan/platform-agent/internal/memory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNoopEmbedder(t *testing.T) {
	e := memory.NoopEmbedder{}
	vec, err := e.Embed(context.Background(), "hello")
	require.NoError(t, err)
	assert.Nil(t, vec)
	assert.Equal(t, 0, e.Dimensions())
}

func TestHTTPEmbedder_OpenAIFormat(t *testing.T) {
	// Build a fake OpenAI-compatible embedding server.
	wantVec := []float32{0.1, 0.2, 0.3, 0.4}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))

		var body map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "test-model", body["model"])
		assert.Equal(t, "hello world", body["input"])

		resp := map[string]interface{}{
			"data": []map[string]interface{}{
				{"embedding": wantVec, "index": 0},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	e := memory.NewHTTPEmbedder(memory.HTTPEmbedderConfig{
		Endpoint: srv.URL,
		APIKey:   "test-key",
		Model:    "test-model",
	})

	vec, err := e.Embed(context.Background(), "hello world")
	require.NoError(t, err)
	assert.Equal(t, wantVec, vec)
	assert.Equal(t, 4, e.Dimensions()) // auto-detected
}

func TestHTTPEmbedder_OllamaFormat(t *testing.T) {
	wantVec := []float32{0.5, 0.6, 0.7}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"embedding": wantVec,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	e := memory.NewHTTPEmbedder(memory.HTTPEmbedderConfig{
		Endpoint: srv.URL,
		Model:    "nomic-embed-text",
	})

	vec, err := e.Embed(context.Background(), "test")
	require.NoError(t, err)
	assert.Equal(t, wantVec, vec)
}

func TestHTTPEmbedder_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	e := memory.NewHTTPEmbedder(memory.HTTPEmbedderConfig{
		Endpoint: srv.URL,
		Model:    "any",
	})

	_, err := e.Embed(context.Background(), "test")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "500")
}

func TestHTTPEmbedder_EmptyResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{})
	}))
	defer srv.Close()

	e := memory.NewHTTPEmbedder(memory.HTTPEmbedderConfig{
		Endpoint: srv.URL,
		Model:    "any",
	})

	_, err := e.Embed(context.Background(), "test")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no embedding")
}
