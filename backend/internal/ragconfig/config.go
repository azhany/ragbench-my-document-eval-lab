// Package ragconfig holds the immutable RAG configuration domain: validation
// against explicit bounds and the persisted Settings catalog, and PostgreSQL
// storage.
// Configurations are immutable identities — there is intentionally no update
// or delete; changing settings means saving a new configuration under a new
// name so historical runs keep pointing at the settings that produced them.
package ragconfig

import (
	"context"
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
	DefaultRRFConstant      = 60
	MinRRFConstant          = 1
	MaxRRFConstant          = 1000
	DefaultCandidateLimit   = 20
	FusionMethodRRF         = "rrf"
	DefaultRerankerProfile  = "lexical-v1"
	MinRerankCandidateLimit = 1
	MaxRerankCandidateLimit = 100
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
	RerankerProfile      string    `json:"reranker_profile"`
	RerankCandidateLimit int       `json:"rerank_candidate_limit"`
	FusionMethod         string    `json:"fusion_method"`
	RRFConstant          float64   `json:"rrf_rank_constant"`
	FTSCandidateLimit    int       `json:"fts_candidate_limit"`
	VectorCandidateLimit int       `json:"vector_candidate_limit"`
	PromptVersion        string    `json:"prompt_version"`
	ModelProfile         string    `json:"model_profile"`
	ModelProvider        string    `json:"model_provider"`
	ModelName            string    `json:"model_name"`
	EmbeddingProfile     string    `json:"embedding_profile"`
	EmbeddingProvider    string    `json:"embedding_provider"`
	EmbeddingModel       string    `json:"embedding_model"`
	EmbeddingDimensions  int       `json:"embedding_dimensions"`
	CreatedAt            time.Time `json:"created_at"`
	// UnavailableCapabilities lists requested features the running binary
	// cannot execute; execution paths must reject them explicitly instead of
	// silently degrading. The shipped vector, hybrid, and lexical-v1 rerank
	// modes therefore serialize as an empty list.
	UnavailableCapabilities []string `json:"unavailable_capabilities"`
}

// CreateRequest is the API payload for saving a new configuration.
type CreateRequest struct {
	Name                 string  `json:"name"`
	ChunkSize            int     `json:"chunk_size"`
	ChunkOverlap         int     `json:"chunk_overlap"`
	RetrievalMode        string  `json:"retrieval_mode"`
	TopK                 int     `json:"top_k"`
	RerankEnabled        bool    `json:"rerank_enabled"`
	RerankerProfile      string  `json:"reranker_profile"`
	RerankCandidateLimit int     `json:"rerank_candidate_limit"`
	FusionMethod         string  `json:"fusion_method"`
	RRFConstant          float64 `json:"rrf_rank_constant"`
	FTSCandidateLimit    int     `json:"fts_candidate_limit"`
	VectorCandidateLimit int     `json:"vector_candidate_limit"`
	PromptVersion        string  `json:"prompt_version"`
	ModelProfile         string  `json:"model_profile"`
	EmbeddingProfile     string  `json:"embedding_profile"`
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
// capability the stack cannot execute. Execution paths must return this
// instead of silently degrading.
type CapabilityUnavailableError struct {
	Capability string
}

func (e *CapabilityUnavailableError) Error() string {
	return fmt.Sprintf(
		"capability %q is not available in this stack version; refusing to execute instead of silently degrading",
		e.Capability)
}

// Resolved is a validated creation request with model identities resolved from
// the Settings catalog (or the legacy built-in catalog for pure callers).
type Resolved struct {
	Name                 string
	ChunkSize            int
	ChunkOverlap         int
	RetrievalMode        string
	TopK                 int
	RerankEnabled        bool
	RerankerProfile      string
	RerankCandidateLimit int
	FusionMethod         string
	RRFConstant          float64
	FTSCandidateLimit    int
	VectorCandidateLimit int
	PromptVersion        string
	ModelProfile         string
	ModelProvider        string
	ModelName            string
	EmbeddingProfile     string
	EmbeddingProvider    string
	EmbeddingModel       string
	EmbeddingDimensions  int
}

// ProfileResolver is the small boundary between configuration validation and
// the persisted Settings catalog. Keeping it here means the RAG config domain
// does not know how model profiles are stored, while tests can still use the
// built-in registry through Resolve.
type ProfileResolver interface {
	GenerationProfile(context.Context, string) (providers.GenerationProfile, error)
	EmbeddingProfile(context.Context, string) (providers.EmbeddingProfile, error)
}

type registryResolver struct{}

func (registryResolver) GenerationProfile(_ context.Context, name string) (providers.GenerationProfile, error) {
	return providers.GenerationProfileByName(name)
}

func (registryResolver) EmbeddingProfile(_ context.Context, name string) (providers.EmbeddingProfile, error) {
	return providers.EmbeddingProfileByName(name)
}

// Resolve validates against the built-in catalog. It remains useful for pure
// domain tests and compatibility callers; the PostgreSQL Store uses
// ResolveWithProfiles so Settings-created profiles are accepted at runtime.
func Resolve(req CreateRequest) (Resolved, ValidationErrors) {
	return resolve(context.Background(), req, registryResolver{})
}

// ResolveWithProfiles validates against the persisted Settings catalog.
func ResolveWithProfiles(ctx context.Context, req CreateRequest, resolver ProfileResolver) (Resolved, ValidationErrors) {
	if resolver == nil {
		resolver = registryResolver{}
	}
	return resolve(ctx, req, resolver)
}

// resolve validates the request against explicit bounds and the selected
// profile catalog, returning every violation at once. Model identities are
// resolved server-side and persisted alongside their profile keys so the exact
// provider/model/dimensions used survive catalog changes.
func resolve(ctx context.Context, req CreateRequest, resolver ProfileResolver) (Resolved, ValidationErrors) {
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

	fusionMethod := strings.TrimSpace(req.FusionMethod)
	if fusionMethod == "" {
		fusionMethod = FusionMethodRRF
	}
	if fusionMethod != FusionMethodRRF {
		errs = append(errs, FieldError{Field: "fusion_method",
			Message: fmt.Sprintf("fusion_method must be %q", FusionMethodRRF)})
	}
	rrfConstant := req.RRFConstant
	if rrfConstant == 0 {
		rrfConstant = DefaultRRFConstant
	}
	if rrfConstant < MinRRFConstant || rrfConstant > MaxRRFConstant {
		errs = append(errs, FieldError{Field: "rrf_rank_constant",
			Message: fmt.Sprintf("rrf_rank_constant must be between %d and %d", MinRRFConstant, MaxRRFConstant)})
	}
	ftsCandidateLimit := req.FTSCandidateLimit
	if ftsCandidateLimit == 0 {
		ftsCandidateLimit = DefaultCandidateLimit
	}
	if ftsCandidateLimit < MinRerankCandidateLimit || ftsCandidateLimit > MaxRerankCandidateLimit {
		errs = append(errs, FieldError{Field: "fts_candidate_limit",
			Message: fmt.Sprintf("fts_candidate_limit must be between %d and %d", MinRerankCandidateLimit, MaxRerankCandidateLimit)})
	}
	vectorCandidateLimit := req.VectorCandidateLimit
	if vectorCandidateLimit == 0 {
		vectorCandidateLimit = DefaultCandidateLimit
	}
	if vectorCandidateLimit < MinRerankCandidateLimit || vectorCandidateLimit > MaxRerankCandidateLimit {
		errs = append(errs, FieldError{Field: "vector_candidate_limit",
			Message: fmt.Sprintf("vector_candidate_limit must be between %d and %d", MinRerankCandidateLimit, MaxRerankCandidateLimit)})
	}

	rerankerProfile := strings.TrimSpace(req.RerankerProfile)
	if rerankerProfile == "" {
		rerankerProfile = DefaultRerankerProfile
	}
	if req.RerankCandidateLimit == 0 {
		req.RerankCandidateLimit = DefaultCandidateLimit
	}
	if req.RerankCandidateLimit < MinRerankCandidateLimit || req.RerankCandidateLimit > MaxRerankCandidateLimit {
		errs = append(errs, FieldError{Field: "rerank_candidate_limit",
			Message: fmt.Sprintf("rerank_candidate_limit must be between %d and %d", MinRerankCandidateLimit, MaxRerankCandidateLimit)})
	}
	if _, err := providers.RerankProfileByName(rerankerProfile); err != nil {
		errs = append(errs, FieldError{Field: "reranker_profile", Message: err.Error()})
	}

	if !validRetrievalModes[req.RetrievalMode] {
		errs = append(errs, FieldError{Field: "retrieval_mode",
			Message: fmt.Sprintf("retrieval_mode must be %q or %q", RetrievalModeVector, RetrievalModeHybrid)})
	}

	if _, err := providers.PromptByVersion(strings.TrimSpace(req.PromptVersion)); err != nil {
		errs = append(errs, FieldError{Field: "prompt_version", Message: err.Error()})
	}
	generationProfile, generationErr := resolver.GenerationProfile(ctx, strings.TrimSpace(req.ModelProfile))
	if generationErr != nil {
		errs = append(errs, FieldError{Field: "model_profile", Message: generationErr.Error()})
	}
	embeddingProfile, embeddingErr := resolver.EmbeddingProfile(ctx, strings.TrimSpace(req.EmbeddingProfile))
	if embeddingErr != nil {
		errs = append(errs, FieldError{Field: "embedding_profile", Message: embeddingErr.Error()})
	}

	if len(errs) > 0 {
		return Resolved{}, errs
	}

	return Resolved{
		Name:                 name,
		ChunkSize:            req.ChunkSize,
		ChunkOverlap:         req.ChunkOverlap,
		RetrievalMode:        req.RetrievalMode,
		TopK:                 req.TopK,
		RerankEnabled:        req.RerankEnabled,
		RerankerProfile:      rerankerProfile,
		RerankCandidateLimit: req.RerankCandidateLimit,
		FusionMethod:         fusionMethod,
		RRFConstant:          rrfConstant,
		FTSCandidateLimit:    ftsCandidateLimit,
		VectorCandidateLimit: vectorCandidateLimit,
		PromptVersion:        strings.TrimSpace(req.PromptVersion),
		ModelProfile:         strings.TrimSpace(req.ModelProfile),
		ModelProvider:        generationProfile.Provider,
		ModelName:            generationProfile.Model,
		EmbeddingProfile:     embeddingProfile.Name,
		EmbeddingProvider:    embeddingProfile.Provider,
		EmbeddingModel:       embeddingProfile.Model,
		EmbeddingDimensions:  embeddingProfile.Dimensions,
	}, nil
}

// unavailableCapabilitiesFor lists capabilities requested by these settings
// that the current binary cannot execute. Hybrid retrieval and lexical-v1
// reranking are executable; an unknown reranker is rejected during creation.
func unavailableCapabilitiesFor(mode string, rerank bool) []string {
	// Non-nil so the JSON field serializes as [] rather than null.
	return []string{}
}

// ExecutionBlocker returns a non-nil error when executing a query with this
// configuration would require a capability the running binary does not have.
// Query and evaluation pipelines must call this and reject the request with
// a capability_unavailable error instead of degrading silently.
func (c Config) ExecutionBlocker() error {
	return nil
}
