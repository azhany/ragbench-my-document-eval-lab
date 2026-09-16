package providers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func testHuggingFaceProfile() EmbeddingProfile {
	return EmbeddingProfile{Name: "huggingface-test", Provider: "huggingface", Model: "org/model", Dimensions: 3}
}

func TestHuggingFaceEmbedNativeRequestAndResponse(t *testing.T) {
	var body struct {
		Inputs    []string `json:"inputs"`
		Normalize bool     `json:"normalize"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/org/model/pipeline/feature-extraction" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer hf-test" {
			t.Errorf("authorization header was not set")
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		writeJSON(t, w, [][]float64{{0.1, 0.2, 0.3}, {0.4, 0.5, 0.6}})
	}))
	defer server.Close()

	client := NewHuggingFace(server.URL, "hf-test")
	result, err := client.Embed(context.Background(), testHuggingFaceProfile(), []string{"one", "two"})
	if err != nil {
		t.Fatalf("Embed() error = %v", err)
	}
	if len(body.Inputs) != 2 || !body.Normalize {
		t.Fatalf("request = %+v", body)
	}
	if len(result.Vectors) != 2 || len(result.Vectors[0]) != 3 {
		t.Fatalf("vectors = %+v", result.Vectors)
	}
	if result.PromptTokens != nil {
		t.Fatalf("native Hugging Face usage must be unavailable, got %v", result.PromptTokens)
	}
}

func TestHuggingFaceEmbedClassifiesFailures(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   any
	}{
		{"http", http.StatusUnauthorized, map[string]any{"error": "secret body"}},
		{"wrong dimensions", http.StatusOK, [][]float64{{0.1}}},
		{"wrong count", http.StatusOK, [][]float64{}},
		{"zero vector", http.StatusOK, [][]float64{{0, 0, 0}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_ = json.NewEncoder(w).Encode(tt.body)
			}))
			defer server.Close()
			_, err := NewHuggingFace(server.URL, "key").Embed(
				context.Background(), testHuggingFaceProfile(), []string{"one"})
			var providerErr *ProviderError
			if !errors.As(err, &providerErr) || providerErr.Code != ErrCodeEmbeddingFailed {
				t.Fatalf("want embedding_failed, got %v", err)
			}
		})
	}
}

func TestRouterUsesPersistedProviderIdentity(t *testing.T) {
	router := &Router{Embedders: map[string]Embedder{
		"huggingface": NewHuggingFace("unused", ""),
	}}
	_, err := router.Embed(context.Background(), EmbeddingProfile{Provider: "missing"}, []string{"one"})
	var providerErr *ProviderError
	if !errors.As(err, &providerErr) || providerErr.Code != ErrCodeEmbeddingFailed {
		t.Fatalf("want classified missing-provider error, got %v", err)
	}
}
