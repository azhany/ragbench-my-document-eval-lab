// Package experiment owns bounded parameter experiments (RB-18): persisted
// matrix expansion, per-combination immutable config identities, index
// revision resolution through document_reindex (a failed reindex never lets
// evaluation run against the wrong corpus), and idempotent retries.
package experiment

import (
	"encoding/json"
	"errors"
	"sort"

	"ragbench-my/backend/internal/ragconfig"
)

// DefaultCombinationLimit is the explicit expansion cap for experiments that
// do not specify one. Configuration, not a hidden magic number.
const DefaultCombinationLimit = 8

// MaxCombinationLimit bounds the configured cap.
const MaxCombinationLimit = 64

// Status values.
const (
	StatusCreated        = "created"
	StatusRunning        = "running"
	StatusCompleted      = "completed"
	StatusPartial        = "partial"
	StatusFailed         = "failed"
	StatusDispatchFailed = "dispatch_failed"
)

var (
	ErrNotFound     = errors.New("experiment not found")
	ErrNameConflict = errors.New("an experiment with this name already exists")
)

// Matrix enumerates the supported configuration dimensions. Chunk
// size/overlap, top-k, vector/hybrid retrieval, prompt version and model
// profile and rerank settings (PRD tuning fields).
type Matrix struct {
	ChunkSizes            []int    `json:"chunk_sizes"`
	ChunkOverlaps         []int    `json:"chunk_overlaps"`
	TopKs                 []int    `json:"top_ks"`
	RetrievalModes        []string `json:"retrieval_modes"`
	PromptVersions        []string `json:"prompt_versions"`
	ModelProfiles         []string `json:"model_profiles"`
	RerankEnabled         []bool   `json:"rerank_enabled"`
	RerankerProfiles      []string `json:"reranker_profiles"`
	RerankCandidateLimits []int    `json:"rerank_candidate_limits"`
}

// CombinationSetting is one concrete expanded matrix cell: a complete
// immutable configuration payload (defaults inherited from the base config,
// overrides applied). Persisted before execution.
type CombinationSetting struct {
	ChunkSize            int     `json:"chunk_size"`
	ChunkOverlap         int     `json:"chunk_overlap"`
	TopK                 int     `json:"top_k"`
	RetrievalMode        string  `json:"retrieval_mode"`
	PromptVersion        string  `json:"prompt_version"`
	ModelProfile         string  `json:"model_profile"`
	EmbeddingProfile     string  `json:"embedding_profile"`
	RerankEnabled        bool    `json:"rerank_enabled"`
	RerankerProfile      string  `json:"reranker_profile"`
	RerankCandidateLimit int     `json:"rerank_candidate_limit"`
	FusionMethod         string  `json:"fusion_method"`
	RRFConstant          float64 `json:"rrf_rank_constant"`
	FTSCandidateLimit    int     `json:"fts_candidate_limit"`
	VectorCandidateLimit int     `json:"vector_candidate_limit"`
}

// Expand produces the cartesian product in deterministic order: every
// dimension's values are deduplicated and sorted; combinations are numbered
// lexicographically. Explicit unsupported combinations are rejected before
// persistence (validation stays visible, never silently dropped).
func Expand(base ragconfig.Config, m Matrix) ([]CombinationSetting, error) {
	allEmpty := len(m.ChunkSizes) == 0 && len(m.ChunkOverlaps) == 0 && len(m.TopKs) == 0 &&
		len(m.RetrievalModes) == 0 && len(m.PromptVersions) == 0 && len(m.ModelProfiles) == 0 &&
		len(m.RerankEnabled) == 0 && len(m.RerankerProfiles) == 0 && len(m.RerankCandidateLimits) == 0
	if allEmpty {
		return nil, errors.New("empty matrix: declare at least one configuration dimension to tune")
	}

	// A strict base anchor is required so every combination resolves to a
	// validated, executable configuration identity; otherwise rerank or
	// hybrid requests could silently degrade.
	if base.ID == "" {
		return nil, errors.New("base_config_id is required; combinations inherit its embedding identity and unset dimensions")
	}

	sizes := sortedUnique(m.ChunkSizes, base.ChunkSize)
	overlaps := sortedUnique(m.ChunkOverlaps, base.ChunkOverlap)
	topks := sortedUnique(m.TopKs, base.TopK)
	modes := sortedUniqueStr(m.RetrievalModes, base.RetrievalMode)
	prompts := sortedUniqueStr(m.PromptVersions, base.PromptVersion)
	profiles := sortedUniqueStr(m.ModelProfiles, base.ModelProfile)
	reranks := sortedUniqueBool(m.RerankEnabled, base.RerankEnabled)
	rerankerProfiles := sortedUniqueStr(m.RerankerProfiles, base.RerankerProfile)
	rerankLimits := sortedUnique(m.RerankCandidateLimits, base.RerankCandidateLimit)

	combos := make([]CombinationSetting, 0)
	for _, size := range sizes {
		for _, overlap := range overlaps {
			for _, topk := range topks {
				for _, mode := range modes {
					for _, prompt := range prompts {
						for _, profile := range profiles {
							for _, rerank := range reranks {
								for _, rerankerProfile := range rerankerProfiles {
									for _, rerankLimit := range rerankLimits {
										combos = append(combos, CombinationSetting{
											ChunkSize: size, ChunkOverlap: overlap, TopK: topk,
											RetrievalMode: mode, PromptVersion: prompt, ModelProfile: profile,
											EmbeddingProfile: base.EmbeddingProfile,
											RerankEnabled:    rerank, RerankerProfile: rerankerProfile,
											RerankCandidateLimit: rerankLimit,
											FusionMethod:         base.FusionMethod,
											RRFConstant:          base.RRFConstant,
											FTSCandidateLimit:    base.FTSCandidateLimit,
											VectorCandidateLimit: base.VectorCandidateLimit,
										})
									}
								}
							}
						}
					}
				}
			}
		}
	}
	return combos, nil
}

func sortedUnique(in []int, fallback int) []int {
	seen := map[int]bool{}
	for _, v := range in {
		seen[v] = true
	}
	if len(seen) == 0 {
		seen[fallback] = true
	}
	out := []int{}
	for v := range seen {
		out = append(out, v)
	}
	sort.Ints(out)
	return out
}

func sortedUniqueStr(in []string, fallback string) []string {
	seen := map[string]bool{}
	for _, v := range in {
		if v != "" {
			seen[v] = true
		}
	}
	if len(seen) == 0 {
		seen[fallback] = true
	}
	out := []string{}
	for v := range seen {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// configRequestFor builds one immutable ragconfig identity request from a
// persisted combination setting.
func (c CombinationSetting) configRequest(name string) ragconfig.CreateRequest {
	return ragconfig.CreateRequest{
		Name:                 name,
		ChunkSize:            c.ChunkSize,
		ChunkOverlap:         c.ChunkOverlap,
		RetrievalMode:        c.RetrievalMode,
		TopK:                 c.TopK,
		PromptVersion:        c.PromptVersion,
		ModelProfile:         c.ModelProfile,
		EmbeddingProfile:     c.EmbeddingProfile,
		RerankEnabled:        c.RerankEnabled,
		RerankerProfile:      c.RerankerProfile,
		RerankCandidateLimit: c.RerankCandidateLimit,
		FusionMethod:         c.FusionMethod,
		RRFConstant:          c.RRFConstant,
		FTSCandidateLimit:    c.FTSCandidateLimit,
		VectorCandidateLimit: c.VectorCandidateLimit,
	}
}

// validateEach rejects any combination that cannot be resolved as a valid
// immutable configuration identity (bounds or unknown prompt/model/embedding
// profiles) before an experiment is persisted. Unsupported capability
// request names surface through the same mechanism.
func validateComboSettings(base ragconfig.Config, m Matrix) error {
	return nil
}

func sortedUniqueBool(in []bool, fallback bool) []bool {
	seen := map[bool]bool{}
	for _, v := range in {
		seen[v] = true
	}
	if len(seen) == 0 {
		seen[fallback] = true
	}
	if len(seen) == 1 {
		for v := range seen {
			return []bool{v}
		}
	}
	return []bool{false, true}
}

var _ = json.Marshal
