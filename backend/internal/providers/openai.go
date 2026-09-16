package providers

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// Classified provider failure codes. The embedding path classifies every
// provider problem as embedding_failed (matching the Airflow pipeline); the
// generation path uses the documented MONITORING.md taxonomy, so a pipeline
// failure is never reported as a quality score.
const (
	ErrCodeEmbeddingFailed   = "embedding_failed"
	ErrCodeModelTimeout      = "model_timeout"
	ErrCodeModelRateLimited  = "model_rate_limited"
	ErrCodeMalformedResponse = "malformed_response"
	ErrCodeModelFailed       = "model_failed"
)

// ProviderError is a classified provider failure. Code is one of the taxonomy
// constants above; Message never contains provider response bodies or
// credentials, only the classified reason.
type ProviderError struct {
	Code    string
	Message string
}

func (e *ProviderError) Error() string { return e.Code + ": " + e.Message }

// EmbeddingResult carries one embedding batch plus the usage the provider
// reported for it. PromptTokens is nil when usage was not reported; callers
// must treat that as unavailable cost, not zero cost.
type EmbeddingResult struct {
	Vectors      [][]float32
	PromptTokens *int
}

// GenerationResult carries one completion plus the reported token usage.
// Both token fields are nil when the provider did not report usage. Model is
// the provider's resolved response model, which may be a pinned snapshot of
// the requested profile alias.
type GenerationResult struct {
	Model        string
	Text         string
	InputTokens  *int
	OutputTokens *int
}

// Embedder produces embeddings for a batch of texts under one embedding
// profile. Real implementations perform network calls; tests substitute
// deterministic doubles.
type Embedder interface {
	Embed(ctx context.Context, profile EmbeddingProfile, texts []string) (EmbeddingResult, error)
}

// Generator produces one completion for a prompt under one generation
// profile.
type Generator interface {
	Generate(ctx context.Context, profile GenerationProfile, prompt string) (GenerationResult, error)
}

// OpenAI is an OpenAI Chat Completions/Embeddings protocol client. The
// provider identity and base URL are explicit so the same wire contract can
// be used with OpenAI-compatible services without mislabelling their traces.
type OpenAI struct {
	Provider string
	BaseURL  string
	APIKey   string
	Client   *http.Client
	Headers  map[string]string
}

func NewOpenAI(apiKey string) *OpenAI {
	return NewOpenAICompatible("openai", "https://api.openai.com/v1", apiKey)
}

// NewOpenAICompatible creates a client for a named provider implementing the
// OpenAI v1 embeddings and/or Chat Completions wire contract.
func NewOpenAICompatible(provider, baseURL, apiKey string) *OpenAI {
	return &OpenAI{
		Provider: provider,
		BaseURL:  baseURL,
		APIKey:   apiKey,
		Client:   &http.Client{Timeout: 60 * time.Second},
	}
}

type httpStatusError struct {
	status int
}

func (e *httpStatusError) Error() string { return fmt.Sprintf("HTTP %d", e.status) }

// Embed implements Embedder for the configured embedding profile. Every
// transport/validation problem is classified as embedding_failed, mirroring
// the Airflow embedding stage.
func (o *OpenAI) Embed(ctx context.Context, profile EmbeddingProfile, texts []string) (EmbeddingResult, error) {
	if profile.Provider != o.provider() {
		return EmbeddingResult{}, &ProviderError{Code: ErrCodeEmbeddingFailed,
			Message: fmt.Sprintf("embedding provider %q is not configured", profile.Provider)}
	}
	if o.APIKey == "" {
		return EmbeddingResult{}, &ProviderError{Code: ErrCodeEmbeddingFailed,
			Message: "embedding API key is not configured in the API service"}
	}
	body, err := json.Marshal(map[string]any{
		"input":           texts,
		"model":           profile.Model,
		"dimensions":      profile.Dimensions,
		"encoding_format": "float",
	})
	if err != nil {
		return EmbeddingResult{}, &ProviderError{Code: ErrCodeEmbeddingFailed,
			Message: "encode embedding request"}
	}

	raw, err := o.post(ctx, "/embeddings", body)
	if err != nil {
		var herr *httpStatusError
		switch {
		case errors.As(err, &herr):
			return EmbeddingResult{}, &ProviderError{Code: ErrCodeEmbeddingFailed,
				Message: fmt.Sprintf("%s embeddings returned HTTP %d", o.label(), herr.status)}
		default:
			return EmbeddingResult{}, &ProviderError{Code: ErrCodeEmbeddingFailed,
				Message: classifyTransport(err, o.label()+" embeddings")}
		}
	}

	var out struct {
		Model string `json:"model"`
		Data  []struct {
			Index     int       `json:"index"`
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
		Usage struct {
			PromptTokens *int `json:"prompt_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return EmbeddingResult{}, &ProviderError{Code: ErrCodeEmbeddingFailed,
			Message: o.label() + " embeddings response is not valid JSON"}
	}
	if out.Model != profile.Model {
		return EmbeddingResult{}, &ProviderError{Code: ErrCodeEmbeddingFailed,
			Message: o.label() + " embeddings response model differs from the persisted profile"}
	}
	if out.Usage.PromptTokens != nil && *out.Usage.PromptTokens < 0 {
		return EmbeddingResult{}, &ProviderError{Code: ErrCodeEmbeddingFailed,
			Message: o.label() + " embeddings returned a negative token count"}
	}
	if len(out.Data) != len(texts) {
		return EmbeddingResult{}, &ProviderError{Code: ErrCodeEmbeddingFailed,
			Message: o.label() + " embeddings returned the wrong number of vectors"}
	}
	vectors := make([][]float32, len(out.Data))
	for i, item := range out.Data {
		if item.Index != i {
			return EmbeddingResult{}, &ProviderError{Code: ErrCodeEmbeddingFailed,
				Message: o.label() + " embeddings response indexes are missing or duplicated"}
		}
		if len(item.Embedding) != profile.Dimensions {
			return EmbeddingResult{}, &ProviderError{Code: ErrCodeEmbeddingFailed,
				Message: fmt.Sprintf("%s embeddings vector dimensions must equal %d", o.label(), profile.Dimensions)}
		}
		vector := make([]float32, len(item.Embedding))
		for j, v := range item.Embedding {
			if math.IsNaN(v) || math.IsInf(v, 0) || v > math.MaxFloat32 || v < -math.MaxFloat32 {
				return EmbeddingResult{}, &ProviderError{Code: ErrCodeEmbeddingFailed,
					Message: o.label() + " embeddings returned invalid vector values"}
			}
			vector[j] = float32(v)
		}
		nonzero := false
		for _, v := range vector {
			if v != 0 {
				nonzero = true
				break
			}
		}
		if !nonzero {
			return EmbeddingResult{}, &ProviderError{Code: ErrCodeEmbeddingFailed,
				Message: o.label() + " embeddings returned a zero vector, unusable for cosine search"}
		}
		vectors[i] = vector
	}
	return EmbeddingResult{Vectors: vectors, PromptTokens: out.Usage.PromptTokens}, nil
}

// Generate implements Generator for the configured generation profile. The
// failure taxonomy distinguishes timeout, rate limiting, malformed responses
// and other provider failures so traces can classify them.
func (o *OpenAI) Generate(ctx context.Context, profile GenerationProfile, prompt string) (GenerationResult, error) {
	if profile.Provider != o.provider() {
		return GenerationResult{}, &ProviderError{Code: ErrCodeModelFailed,
			Message: fmt.Sprintf("generation provider %q is not configured", profile.Provider)}
	}
	if o.APIKey == "" {
		return GenerationResult{}, &ProviderError{Code: ErrCodeModelFailed,
			Message: "generation API key is not configured in the API service"}
	}
	body, err := json.Marshal(map[string]any{
		"model": profile.Model,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
	})
	if err != nil {
		return GenerationResult{}, &ProviderError{Code: ErrCodeMalformedResponse,
			Message: "encode generation request"}
	}

	raw, err := o.post(ctx, "/chat/completions", body)
	if err != nil {
		var herr *httpStatusError
		switch {
		case errors.Is(err, context.DeadlineExceeded), isTimeout(err):
			return GenerationResult{}, &ProviderError{Code: ErrCodeModelTimeout,
				Message: "generation did not finish within the configured deadline"}
		case errors.As(err, &herr) && herr.status == http.StatusTooManyRequests:
			return GenerationResult{}, &ProviderError{Code: ErrCodeModelRateLimited,
				Message: "generation provider returned HTTP 429 rate limiting"}
		case errors.As(err, &herr):
			return GenerationResult{}, &ProviderError{Code: ErrCodeModelFailed,
				Message: fmt.Sprintf("%s chat completions returned HTTP %d", o.label(), herr.status)}
		default:
			return GenerationResult{}, &ProviderError{Code: ErrCodeModelFailed,
				Message: classifyTransport(err, o.label()+" chat completions")}
		}
	}

	var out struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content *string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     *int `json:"prompt_tokens"`
			CompletionTokens *int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return GenerationResult{}, &ProviderError{Code: ErrCodeMalformedResponse,
			Message: "generation response is not valid JSON"}
	}
	if !responseModelMatches(profile.Model, out.Model) {
		return GenerationResult{}, &ProviderError{Code: ErrCodeMalformedResponse,
			Message: "generation response model differs from the persisted profile"}
	}
	if (out.Usage.PromptTokens != nil && *out.Usage.PromptTokens < 0) ||
		(out.Usage.CompletionTokens != nil && *out.Usage.CompletionTokens < 0) {
		return GenerationResult{}, &ProviderError{Code: ErrCodeMalformedResponse,
			Message: "generation response contains a negative token count"}
	}
	if len(out.Choices) != 1 || out.Choices[0].Message.Content == nil || *out.Choices[0].Message.Content == "" {
		return GenerationResult{}, &ProviderError{Code: ErrCodeMalformedResponse,
			Message: "generation response has no completion content"}
	}
	return GenerationResult{
		Model:        out.Model,
		Text:         *out.Choices[0].Message.Content,
		InputTokens:  out.Usage.PromptTokens,
		OutputTokens: out.Usage.CompletionTokens,
	}, nil
}

// responseModelMatches accepts the requested model identifier and the
// documented resolved snapshot returned by OpenAI for the current alias.
// Unknown snapshots remain malformed so the trace cannot silently claim a
// different model.
func responseModelMatches(requested, returned string) bool {
	if returned == requested {
		return true
	}
	// HF's router accepts a provider suffix (model:provider) but returns the
	// canonical repository id in the completion envelope.
	if suffix := strings.IndexByte(requested, ':'); suffix > 0 && returned == requested[:suffix] {
		return true
	}
	return requested == "gpt-4o-mini" && returned == "gpt-4o-mini-2024-07-18"
}

// post performs one authenticated POST and returns the raw body. HTTP errors
// are returned as *httpStatusError so callers can classify by status; the
// body is never included in errors.
func (o *OpenAI) post(ctx context.Context, path string, body []byte) ([]byte, error) {
	client := o.Client
	if client == nil {
		client = http.DefaultClient
	}
	baseURL := strings.TrimRight(o.BaseURL, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+o.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "ragbench-my/1.0")
	if o.provider() == "opencode-go" || o.provider() == "opencode-zen" {
		req.Header.Set("X-OpenCode-Session", newSessionID())
	}
	for name, value := range o.Headers {
		req.Header.Set(name, value)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &httpStatusError{status: resp.StatusCode}
	}
	return raw, nil
}

func newSessionID() string {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return fmt.Sprintf("ragbench-%d", time.Now().UnixNano())
	}
	return "ragbench-" + hex.EncodeToString(id[:])
}

func (o *OpenAI) provider() string {
	if o.Provider == "" {
		return "openai"
	}
	return o.Provider
}

func (o *OpenAI) label() string {
	if o.provider() == "openai" {
		return "OpenAI"
	}
	return o.provider()
}

func isTimeout(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func classifyTransport(err error, what string) string {
	if isTimeout(err) || errors.Is(err, context.DeadlineExceeded) {
		return what + " timed out"
	}
	return what + " unavailable or response invalid"
}

// EmbeddingKey resolves the configured embedding API key using the same
// precedence as the Airflow pipeline: the embedding-specific key wins over
// OPENAI_API_KEY.
func EmbeddingKey() string {
	if key := os.Getenv("EMBEDDING_PROVIDER_API_KEY"); key != "" {
		return key
	}
	return os.Getenv("OPENAI_API_KEY")
}

// GenerationKey resolves the configured generation API key.
func GenerationKey() string {
	return os.Getenv("OPENAI_API_KEY")
}
