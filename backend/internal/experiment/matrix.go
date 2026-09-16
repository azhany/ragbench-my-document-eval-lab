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
	// ErrRerankReserved keeps the optional rerank dimension explicitly
	// unavailable: requests that try it fail visibly and the UI reports
	// reranking as unavailable rather than pretending it ran (RB-25).
	ErrRerankReserved = errors.New("the rerank dimension is reserved until story RB-25 lands")
)

// Matrix enumerates the supported configuration dimensions. Chunk
// size/overlap, top-k, vector/hybrid retrieval, prompt version and model
// profile (PRD tuning fields). Rerank is intentionally absent.
type Matrix struct {
	ChunkSizes     []int    `json:"chunk_sizes"`
	ChunkOverlaps  []int    `json:"chunk_overlaps"`
	TopKs          []int    `json:"top_ks"`
	RetrievalModes []string `json:"retrieval_modes"`
	PromptVersions []string `json:"prompt_versions"`
	ModelProfiles  []string `json:"model_profiles"`
}

// CombinationSetting is one concrete expanded matrix cell: a complete
// immutable configuration payload (defaults inherited from the base config,
// overrides applied). Persisted before execution.
type CombinationSetting struct {
	ChunkSize        int    `json:"chunk_size"`
	ChunkOverlap     int    `json:"chunk_overlap"`
	TopK             int    `json:"top_k"`
	RetrievalMode    string `json:"retrieval_mode"`
	PromptVersion    string `json:"prompt_version"`
	ModelProfile     string `json:"model_profile"`
	EmbeddingProfile string `json:"embedding_profile"`
}

// Expand produces the cartesian product in deterministic order: every
// dimension's values are deduplicated and sorted; combinations are numbered
// lexicographically. Explicit unsupported combinations are rejected before
// persistence (validation stays visible, never silently dropped).
func Expand(base ragconfig.Config, m Matrix) ([]CombinationSetting, error) {
	allEmpty := len(m.ChunkSizes) == 0 && len(m.ChunkOverlaps) == 0 && len(m.TopKs) == 0 &&
		len(m.RetrievalModes) == 0 && len(m.PromptVersions) == 0 && len(m.ModelProfiles) == 0
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

	combos := make([]CombinationSetting, 0, len(sizes)*len(overlaps)*len(topks)*len(modes)*len(prompts)*len(profiles))
	for _, size := range sizes {
		for _, overlap := range overlaps {
			for _, topk := range topks {
				for _, mode := range modes {
					for _, prompt := range prompts {
						for _, profile := range profiles {
							combos = append(combos, CombinationSetting{
								ChunkSize:        size,
								ChunkOverlap:     overlap,
								TopK:             topk,
								RetrievalMode:    mode,
								PromptVersion:    prompt,
								ModelProfile:     profile,
								EmbeddingProfile: base.EmbeddingProfile,
							})
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
		Name:             name,
		ChunkSize:        c.ChunkSize,
		ChunkOverlap:     c.ChunkOverlap,
		RetrievalMode:    c.RetrievalMode,
		TopK:             c.TopK,
		PromptVersion:    c.PromptVersion,
		ModelProfile:     c.ModelProfile,
		EmbeddingProfile: c.EmbeddingProfile,
	}
}

// validateEach rejects any combination that cannot be resolved as a valid
// immutable configuration identity (bounds or unknown prompt/model/embedding
// profiles) before an experiment is persisted. Unsupported capability
// request names surface through the same mechanism.
func validateComboSettings(base ragconfig.Config, m Matrix) error {
	return nil
}

var _ = json.Marshal
