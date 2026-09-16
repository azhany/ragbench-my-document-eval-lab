// Package ragconfig holds the immutable RAG configuration domain: validation
// against explicit bounds and the provider registry, and PostgreSQL storage.
// Configurations are immutable identities — there is intentionally no update
// or delete; changing settings means saving a new configuration under a new
// name so historical runs keep pointing at the settings that produced them.
package ragconfig

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"ragbench-my/backend/internal/providers"
)

// Validation bounds, mirrored by the CHECK constraints in
// db/migrations/0004_rag_configs.sql. They are declared here so no validation
// rule is a hidden magic number; keep the two in sync when changing them.
const (
	MinChunkSize = 1
	MaxChunkSize = 8192
	MinTopK      = 1
	MaxTopK      = 100
	MinNameLen   = 1
	MaxNameLen   = 200
)

const (
	RetrievalModeVector = "vector"
	RetrievalModeHybrid = "hybrid"
)

var validRetrievalModes = map[string]bool{
	RetrievalModeVector: true,
	RetrievalModeHybrid: true,
}

// Hybrid fusion validation bounds, mirrored by the CHECK constraints in
// db/migrations/0014_hybrid_fusion_config.sql. Declared so no fusion
// constant is a hidden magic number; keep the two in sync.
const (
	DefaultRRFConstant    = 60
	MinRRFConstant        = 1
	MaxRRFConstant        = 1000
	DefaultCandidateLimit = 20
	FusionMethodRRF       = "rrf"
)

// Config is one immutable saved configuration identity.
type Config struct {
	ID                   string    `json:"id"`
	Name                 string    `json:"name"`
	ChunkSize            int       `json:"chunk_size"`
	ChunkOverlap         int       `json:"chunk_overlap"`
	RetrievalMode        string    `json:"retrieval_mode"`
	TopK                 int       `json:"top_k"`
	RerankEnabled        bool      `json:"rerank_enabled"`
	FusionMethod         string    `json:"fusion_method"`
	RRFConstant          float64   `json:"rrf_rank_constant"`
	FTSCandidateLimit    int       `json:"fts_candidate_limit"`
	VectorCandidateLimit int       `json:"vector_candidate_limit"`
	PromptVersion        string    `json:"prompt_version"`
	ModelProfile         string    `json:"model_profile"`
	EmbeddingProfile     string    `json:"embedding_profile"`
	EmbeddingProvider    string    `json:"embedding_provider"`
	EmbeddingModel       string    `json:"embedding_model"`
	EmbeddingDimensions  int       `json:"embedding_dimensions"`
	CreatedAt            time.Time `json:"created_at"`
	// UnavailableCapabilities lists requested features the stack cannot
	// execute yet; execution paths must reject them explicitly instead of
	// silently degrading.
	UnavailableCapabilities []string `json:"unavailable_capabilities"`
}

// CreateRequest is the API payload for saving a new configuration.
type CreateRequest struct {
	Name             string `json:"name"`
	ChunkSize        int    `json:"chunk_size"`
	ChunkOverlap     int    `json:"chunk_overlap"`
	RetrievalMode    string `json:"retrieval_mode"`
	TopK             int    `json:"top_k"`
	RerankEnabled    bool   `json:"rerank_enabled"`
	PromptVersion    string `json:"prompt_version"`
	ModelProfile     string `json:"model_profile"`
	EmbeddingProfile string `json:"embedding_profile"`
}

// FieldError is one field-level validation violation.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// ValidationErrors carries every field violation from one request so callers
// see all problems at once.
type ValidationErrors []FieldError

func (e ValidationErrors) Error() string {
	messages := make([]string, len(e))
	for i, fe := range e {
		messages[i] = fe.Field + ": " + fe.Message
	}
	return "invalid configuration: " + strings.Join(messages, "; ")
}

var (
	// ErrNotFound reports that no configuration with the given id exists.
	ErrNotFound = errors.New("rag config not found")

	// ErrNameConflict reports that a configuration with the same name already
	// exists; names identify immutable identities, so duplicates are rejected.
	ErrNameConflict = errors.New("a configuration with this name already exists; save changed settings under a new name")
)

// CapabilityUnavailableError reports a configuration that requests a
// capability the stack cannot execute yet. Execution paths must return this
// instead of silently degrading (hybrid → vector, rerank → no-rerank).
type CapabilityUnavailableError struct {
	Capability string
}

func (e *CapabilityUnavailableError) Error() string {
	return fmt.Sprintf(
		"capability %q is not available in this stack version; refusing to execute instead of silently degrading",
		e.Capability)
}

// Resolved is a validated creation request with the embedding identity
// resolved from the registry.
type Resolved struct {
	Name                string
	ChunkSize           int
	ChunkOverlap        int
	RetrievalMode       string
	TopK                int
	RerankEnabled       bool
	PromptVersion       string
	ModelProfile        string
	EmbeddingProfile    string
	EmbeddingProvider   string
	EmbeddingModel      string
	EmbeddingDimensions int
}

// Resolve validates the request against explicit bounds and the provider
// registry, returning every violation at once. The embedding identity is
// resolved server-side and persisted alongside the profile key so the exact
// provider/model/dimensions used survive registry changes.
func Resolve(req CreateRequest) (Resolved, ValidationErrors) {
	var errs ValidationErrors

	name := strings.TrimSpace(req.Name)
	if utf8.RuneCountInString(name) < MinNameLen {
		errs = append(errs, FieldError{Field: "name",
			Message: fmt.Sprintf("name is required (%d–%d characters)", MinNameLen, MaxNameLen)})
	} else if utf8.RuneCountInString(name) > MaxNameLen {
		errs = append(errs, FieldError{Field: "name",
			Message: fmt.Sprintf("name must be at most %d characters", MaxNameLen)})
	}

	if req.ChunkSize < MinChunkSize || req.ChunkSize > MaxChunkSize {
		errs = append(errs, FieldError{Field: "chunk_size",
			Message: fmt.Sprintf("chunk_size must be between %d and %d", MinChunkSize, MaxChunkSize)})
	}
	if req.ChunkOverlap < 0 {
		errs = append(errs, FieldError{Field: "chunk_overlap", Message: "chunk_overlap must be >= 0"})
	} else if req.ChunkSize >= MinChunkSize && req.ChunkSize <= MaxChunkSize && req.ChunkOverlap >= req.ChunkSize {
		errs = append(errs, FieldError{Field: "chunk_overlap",
			Message: "chunk_overlap must be smaller than chunk_size"})
	}

	if req.TopK < MinTopK || req.TopK > MaxTopK {
		errs = append(errs, FieldError{Field: "top_k",
			Message: fmt.Sprintf("top_k must be between %d and %d", MinTopK, MaxTopK)})
	}

	if !validRetrievalModes[req.RetrievalMode] {
		errs = append(errs, FieldError{Field: "retrieval_mode",
			Message: fmt.Sprintf("retrieval_mode must be %q or %q", RetrievalModeVector, RetrievalModeHybrid)})
	}

	if _, err := providers.PromptByVersion(strings.TrimSpace(req.PromptVersion)); err != nil {
		errs = append(errs, FieldError{Field: "prompt_version", Message: err.Error()})
	}
	if _, err := providers.GenerationProfileByName(strings.TrimSpace(req.ModelProfile)); err != nil {
		errs = append(errs, FieldError{Field: "model_profile", Message: err.Error()})
	}
	if _, err := providers.EmbeddingProfileByName(strings.TrimSpace(req.EmbeddingProfile)); err != nil {
		errs = append(errs, FieldError{Field: "embedding_profile", Message: err.Error()})
	}

	if len(errs) > 0 {
		return Resolved{}, errs
	}

	embedding, _ := providers.EmbeddingProfileByName(strings.TrimSpace(req.EmbeddingProfile))
	return Resolved{
		Name:                name,
		ChunkSize:           req.ChunkSize,
		ChunkOverlap:        req.ChunkOverlap,
		RetrievalMode:       req.RetrievalMode,
		TopK:                req.TopK,
		RerankEnabled:       req.RerankEnabled,
		PromptVersion:       strings.TrimSpace(req.PromptVersion),
		ModelProfile:        strings.TrimSpace(req.ModelProfile),
		EmbeddingProfile:    embedding.Name,
		EmbeddingProvider:   embedding.Provider,
		EmbeddingModel:      embedding.Model,
		EmbeddingDimensions: embedding.Dimensions,
	}, nil
}

// unavailableCapabilitiesFor lists capabilities requested by these settings
// that the stack cannot execute yet. Hybrid retrieval has executed since
// RB-17 (deterministic RRF fusion); reranking waits for RB-25.
func unavailableCapabilitiesFor(mode string, rerank bool) []string {
	// Non-nil so the JSON field serializes as [] rather than null.
	caps := []string{}
	if rerank {
		caps = append(caps, "rerank")
	}
	return caps
}

// ExecutionBlocker returns a non-nil error when executing a query with this
// configuration would require a capability the stack does not have yet.
// Query and evaluation pipelines must call this and reject the request with
// a capability_unavailable error instead of degrading silently.
func (c Config) ExecutionBlocker() error {
	if c.RerankEnabled {
		return &CapabilityUnavailableError{Capability: "rerank"}
	}
	return nil
}
