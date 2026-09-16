package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testEmbeddingProfile() EmbeddingProfile {
	return EmbeddingProfile{
		Name:       "openai-text-embedding-3-small",
		Provider:   "openai",
		Model:      "text-embedding-3-small",
		Dimensions: 3,
	}
}

func testGenerationProfile() GenerationProfile {
	return GenerationProfile{Name: "openai-gpt-4o-mini", Provider: "openai", Model: "gpt-4o-mini"}
}

func TestOpenAIEmbedSuccessAndRequestShape(t *testing.T) {
	var received struct {
		Input          []string `json:"input"`
		Model          string   `json:"model"`
		Dimensions     int      `json:"dimensions"`
		EncodingFormat string   `json:"encoding_format"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			t.Errorf("path = %s, want /v1/embeddings", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("authorization = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Errorf("decode request: %v", err)
		}
		vector := []float64{0.1, 0.2, 0.3}
		writeJSON(t, w, map[string]any{
			"model": "text-embedding-3-small",
			"data":  []any{map[string]any{"index": 0, "embedding": vector}},
			"usage": map[string]any{"prompt_tokens": 7},
		})
	}))
	defer server.Close()

	client := NewOpenAI("test-key")
	client.BaseURL = server.URL + "/v1"
	result, err := client.Embed(context.Background(), testEmbeddingProfile(), []string{"hello"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if received.Model != "text-embedding-3-small" || received.Dimensions != 3 || received.EncodingFormat != "float" || len(received.Input) != 1 || received.Input[0] != "hello" {
		t.Fatalf("request = %+v", received)
	}
	if len(result.Vectors) != 1 || len(result.Vectors[0]) != 3 || result.Vectors[0][2] != float32(0.3) {
		t.Fatalf("vectors = %+v", result.Vectors)
	}
	if result.PromptTokens == nil || *result.PromptTokens != 7 {
		t.Fatalf("prompt tokens = %v", result.PromptTokens)
	}
}

func TestOpenAIEmbedRejectsWrongShapeAsEmbeddingFailed(t *testing.T) {
	cases := []struct {
		name string
		body any
	}{
		{"wrong model", map[string]any{"model": "other", "data": []any{map[string]any{"index": 0, "embedding": []float64{0.1, 0.2, 0.3}}}}},
		{"wrong count", map[string]any{"model": "text-embedding-3-small", "data": []any{}}},
		{"wrong dimensions", map[string]any{"model": "text-embedding-3-small", "data": []any{map[string]any{"index": 0, "embedding": []float64{0.1}}}}},
		{"zero vector", map[string]any{"model": "text-embedding-3-small", "data": []any{map[string]any{"index": 0, "embedding": []float64{0, 0, 0}}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				writeJSON(t, w, tc.body)
			}))
			defer server.Close()
			client := NewOpenAI("key")
			client.BaseURL = server.URL
			_, err := client.Embed(context.Background(), testEmbeddingProfile(), []string{"x"})
			var perr *ProviderError
			if !errors.As(err, &perr) || perr.Code != ErrCodeEmbeddingFailed {
				t.Fatalf("want embedding_failed, got %v", err)
			}
		})
	}
}

func TestOpenAIEmbedMissingKeyClassified(t *testing.T) {
	client := NewOpenAI("")
	_, err := client.Embed(context.Background(), testEmbeddingProfile(), []string{"x"})
	var perr *ProviderError
	if !errors.As(err, &perr) || perr.Code != ErrCodeEmbeddingFailed {
		t.Fatalf("want embedding_failed, got %v", err)
	}
}

func TestOpenAIGenerateSuccessAndUsage(t *testing.T) {
	var received struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path = %s, want /chat/completions", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Errorf("decode request: %v", err)
		}
		writeJSON(t, w, map[string]any{
			"model":   "gpt-4o-mini-2024-07-18",
			"choices": []any{map[string]any{"message": map[string]any{"content": "Answer [1]."}}},
			"usage":   map[string]any{"prompt_tokens": 11, "completion_tokens": 4},
		})
	}))
	defer server.Close()

	client := NewOpenAI("key")
	client.BaseURL = server.URL
	result, err := client.Generate(context.Background(), testGenerationProfile(), "Question and evidence")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if received.Model != "gpt-4o-mini" || len(received.Messages) != 1 || received.Messages[0].Role != "user" || received.Messages[0].Content != "Question and evidence" {
		t.Fatalf("request = %+v", received)
	}
	if result.Text != "Answer [1]." {
		t.Fatalf("text = %q", result.Text)
	}
	if result.Model != "gpt-4o-mini-2024-07-18" {
		t.Fatalf("response model = %q", result.Model)
	}
	if result.InputTokens == nil || *result.InputTokens != 11 || result.OutputTokens == nil || *result.OutputTokens != 4 {
		t.Fatalf("usage = %v/%v", result.InputTokens, result.OutputTokens)
	}
}

func TestOpenAIGenerateStructuredRequestsJSONMode(t *testing.T) {
	var received struct {
		ResponseFormat struct {
			Type string `json:"type"`
		} `json:"response_format"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Errorf("decode request: %v", err)
		}
		writeJSON(t, w, map[string]any{
			"model":   "gpt-4o-mini",
			"choices": []any{map[string]any{"message": map[string]any{"content": `{"schema_version":"financial-v1","document_type":"unknown","line_items":[]}`}}},
		})
	}))
	defer server.Close()
	client := NewOpenAI("key")
	client.BaseURL = server.URL
	result, err := client.GenerateStructured(context.Background(), testGenerationProfile(), "return JSON", json.RawMessage(`{"type":"object"}`))
	if err != nil || result.Text == "" {
		t.Fatalf("GenerateStructured() = %+v, %v", result, err)
	}
	if received.ResponseFormat.Type != "json_object" {
		t.Fatalf("response_format = %+v", received.ResponseFormat)
	}
}

func TestOpenAICompatibleGenerationUsesConfiguredProviderAndEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.UserAgent() != "ragbench-my/1.0" {
			t.Errorf("user-agent = %q", r.UserAgent())
		}
		if r.Header.Get("X-OpenCode-Session") == "" {
			t.Error("x-opencode-session header is required")
		}
		writeJSON(t, w, map[string]any{
			"model": "glm-5.3-flash", "choices": []any{map[string]any{
				"message": map[string]any{"content": "Answer [1]."}}},
			"usage": map[string]any{"prompt_tokens": 5, "completion_tokens": 3},
		})
	}))
	defer server.Close()
	client := NewOpenAICompatible("opencode-go", server.URL+"/v1", "go-key")
	result, err := client.Generate(context.Background(), GenerationProfile{
		Name: "opencode-go-glm-5.3-flash", Provider: "opencode-go", Model: "glm-5.3-flash",
	}, "prompt")
	if err != nil || result.Text != "Answer [1]." {
		t.Fatalf("Generate() = %+v, %v", result, err)
	}
}

func TestOpenCodeZenGenerationUsesSessionHeader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.Header.Get("X-OpenCode-Session") == "" {
			t.Error("x-opencode-session header is required")
		}
		writeJSON(t, w, map[string]any{
			"model": "big-pickle", "choices": []any{map[string]any{
				"message": map[string]any{"content": "Answer [1]."}}},
		})
	}))
	defer server.Close()
	client := NewOpenAICompatible("opencode-zen", server.URL+"/v1", "zen-key")
	result, err := client.Generate(context.Background(), GenerationProfile{
		Name: "opencode-zen-big-pickle", Provider: "opencode-zen", Model: "big-pickle",
	}, "prompt")
	if err != nil || result.Text != "Answer [1]." {
		t.Fatalf("Generate() = %+v, %v", result, err)
	}
}

func TestHuggingFaceRouterAcceptsProviderSuffixResponseModel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %s", r.URL.Path)
		}
		writeJSON(t, w, map[string]any{
			"model": "google/gemma-3-4b-it", "choices": []any{map[string]any{
				"message": map[string]any{"content": "OK"}}},
		})
	}))
	defer server.Close()
	client := NewOpenAICompatible("huggingface-chat", server.URL+"/v1", "hf-key")
	result, err := client.Generate(context.Background(), GenerationProfile{
		Name: "huggingface-gemma-3-4b-it-free", Provider: "huggingface-chat", Model: "google/gemma-3-4b-it:featherless-ai",
	}, "prompt")
	if err != nil || result.Text != "OK" {
		t.Fatalf("Generate() = %+v, %v", result, err)
	}
}

func TestOpenAIGenerateClassifiesHTTPAndMalformedFailures(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		body     string
		wantCode string
	}{
		{name: "rate limited", status: http.StatusTooManyRequests, body: `{"error":"rate"}`, wantCode: ErrCodeModelRateLimited},
		{name: "upstream failure", status: http.StatusBadGateway, body: `{"error":"bad"}`, wantCode: ErrCodeModelFailed},
		{name: "malformed json", status: http.StatusOK, body: `{`, wantCode: ErrCodeMalformedResponse},
		{name: "empty choices", status: http.StatusOK, body: `{"model":"gpt-4o-mini","choices":[]}`, wantCode: ErrCodeMalformedResponse},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			client := NewOpenAI("key")
			client.BaseURL = server.URL
			_, err := client.Generate(context.Background(), testGenerationProfile(), "prompt")
			var perr *ProviderError
			if !errors.As(err, &perr) || perr.Code != tc.wantCode {
				t.Fatalf("want %s, got %v", tc.wantCode, err)
			}
		})
	}
}

func TestOpenAIGenerateTimeoutClassified(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(200 * time.Millisecond):
			writeJSON(t, w, map[string]any{"model": "gpt-4o-mini", "choices": []any{map[string]any{"message": map[string]any{"content": "late"}}}})
		case <-r.Context().Done():
		}
	}))
	defer server.Close()

	client := NewOpenAI("key")
	client.BaseURL = server.URL
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err := client.Generate(ctx, testGenerationProfile(), "prompt")
	var perr *ProviderError
	if !errors.As(err, &perr) || perr.Code != ErrCodeModelTimeout {
		t.Fatalf("want model_timeout, got %v", err)
	}
}

func TestOpenAIGenerateMissingKeyClassified(t *testing.T) {
	client := NewOpenAI("")
	_, err := client.Generate(context.Background(), testGenerationProfile(), "prompt")
	var perr *ProviderError
	if !errors.As(err, &perr) || perr.Code != ErrCodeModelFailed {
		t.Fatalf("want model_failed, got %v", err)
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}

func TestProviderErrorDoesNotExposeBody(t *testing.T) {
	secret := "provider secret response body"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(secret))
	}))
	defer server.Close()

	client := NewOpenAI("key")
	client.BaseURL = server.URL
	_, err := client.Generate(context.Background(), testGenerationProfile(), "prompt")
	if strings.Contains(fmt.Sprint(err), secret) {
		t.Fatal("provider response body must not be exposed in the error")
	}
}

var _ = time.Second
