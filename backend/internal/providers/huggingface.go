package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// HuggingFace implements the native Hugging Face feature-extraction API. Its
// response is a bare array of vectors and normally has no token-usage field.
type HuggingFace struct {
	BaseURL string
	APIKey  string
	Client  *http.Client
}

func NewHuggingFace(baseURL, apiKey string) *HuggingFace {
	return &HuggingFace{BaseURL: baseURL, APIKey: apiKey, Client: &http.Client{Timeout: 60 * time.Second}}
}

func (h *HuggingFace) Embed(ctx context.Context, profile EmbeddingProfile, texts []string) (EmbeddingResult, error) {
	if profile.Provider != "huggingface" {
		return EmbeddingResult{}, &ProviderError{Code: ErrCodeEmbeddingFailed,
			Message: fmt.Sprintf("embedding provider %q is not configured", profile.Provider)}
	}
	if h.APIKey == "" {
		return EmbeddingResult{}, &ProviderError{Code: ErrCodeEmbeddingFailed,
			Message: "Hugging Face embedding API key is not configured in the API service"}
	}
	body, err := json.Marshal(map[string]any{"inputs": texts, "normalize": true})
	if err != nil {
		return EmbeddingResult{}, &ProviderError{Code: ErrCodeEmbeddingFailed, Message: "encode embedding request"}
	}

	client := h.Client
	if client == nil {
		client = http.DefaultClient
	}
	endpoint := strings.TrimRight(h.BaseURL, "/") + "/" + escapeModelPath(profile.Model) + "/pipeline/feature-extraction"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return EmbeddingResult{}, &ProviderError{Code: ErrCodeEmbeddingFailed, Message: "create Hugging Face embedding request"}
	}
	req.Header.Set("Authorization", "Bearer "+h.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "ragbench-my/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return EmbeddingResult{}, &ProviderError{Code: ErrCodeEmbeddingFailed,
			Message: classifyTransport(err, "Hugging Face embeddings")}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return EmbeddingResult{}, &ProviderError{Code: ErrCodeEmbeddingFailed, Message: "Hugging Face embeddings response is unreadable"}
	}
	if resp.StatusCode != http.StatusOK {
		return EmbeddingResult{}, &ProviderError{Code: ErrCodeEmbeddingFailed,
			Message: fmt.Sprintf("Hugging Face embeddings returned HTTP %d", resp.StatusCode)}
	}
	var decoded [][]float64
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return EmbeddingResult{}, &ProviderError{Code: ErrCodeEmbeddingFailed,
			Message: "Hugging Face embeddings response is not a vector array"}
	}
	if len(decoded) != len(texts) {
		return EmbeddingResult{}, &ProviderError{Code: ErrCodeEmbeddingFailed,
			Message: "Hugging Face embeddings returned the wrong number of vectors"}
	}
	vectors := make([][]float32, len(decoded))
	for i, values := range decoded {
		if len(values) != profile.Dimensions {
			return EmbeddingResult{}, &ProviderError{Code: ErrCodeEmbeddingFailed,
				Message: fmt.Sprintf("Hugging Face embeddings vector dimensions must equal %d", profile.Dimensions)}
		}
		vectors[i] = make([]float32, len(values))
		nonzero := false
		for j, value := range values {
			if math.IsNaN(value) || math.IsInf(value, 0) || value > math.MaxFloat32 || value < -math.MaxFloat32 {
				return EmbeddingResult{}, &ProviderError{Code: ErrCodeEmbeddingFailed,
					Message: "Hugging Face embeddings returned invalid vector values"}
			}
			vectors[i][j] = float32(value)
			nonzero = nonzero || value != 0
		}
		if !nonzero {
			return EmbeddingResult{}, &ProviderError{Code: ErrCodeEmbeddingFailed,
				Message: "Hugging Face embeddings returned a zero vector, unusable for cosine search"}
		}
	}
	return EmbeddingResult{Vectors: vectors, PromptTokens: nil}, nil
}

func escapeModelPath(model string) string {
	parts := strings.Split(model, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return strings.Join(parts, "/")
}
