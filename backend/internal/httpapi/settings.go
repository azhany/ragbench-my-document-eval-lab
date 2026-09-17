package httpapi

import (
	"context"
	"errors"
	"net/http"

	"ragbench-my/backend/internal/modelprofile"
	"ragbench-my/backend/internal/ragconfig"
)

// ModelProfileStore is deliberately narrower than the settings domain. The
// HTTP layer can list and add identities, while credentials remain outside the
// browser and the database.
type ModelProfileStore interface {
	List(ctx context.Context) ([]modelprofile.Profile, error)
	Get(ctx context.Context, id string) (modelprofile.Profile, error)
	Create(ctx context.Context, req modelprofile.CreateRequest) (modelprofile.Profile, error)
}

type providerDescriptor struct {
	ID             string   `json:"id"`
	Label          string   `json:"label"`
	Protocol       string   `json:"protocol"`
	Roles          []string `json:"roles"`
	CredentialNote string   `json:"credential_note"`
}

var providerCatalog = []providerDescriptor{
	{ID: "openai", Label: "OpenAI", Protocol: "OpenAI-compatible", Roles: []string{"generation", "embedding"}, CredentialNote: "Server environment"},
	{ID: "opencode-go", Label: "OpenCode Go", Protocol: "OpenAI-compatible", Roles: []string{"generation"}, CredentialNote: "Server environment"},
	{ID: "opencode-zen", Label: "OpenCode Zen", Protocol: "OpenAI-compatible", Roles: []string{"generation"}, CredentialNote: "Server environment"},
	{ID: "huggingface-chat", Label: "Hugging Face Chat", Protocol: "OpenAI-compatible", Roles: []string{"generation"}, CredentialNote: "Server environment"},
	{ID: "huggingface", Label: "Hugging Face Embeddings", Protocol: "Native feature extraction", Roles: []string{"embedding"}, CredentialNote: "Server environment"},
}

type settingsResponse struct {
	Providers        []providerDescriptor   `json:"providers"`
	ModelProfiles    []modelprofile.Profile `json:"model_profiles"`
	PromptVersions   []string               `json:"prompt_versions"`
	RerankerProfiles []string               `json:"reranker_profiles"`
	Limits           map[string]int         `json:"limits"`
}

func (s *server) getSettings(w http.ResponseWriter, r *http.Request) {
	if s.profiles == nil {
		writeError(w, http.StatusServiceUnavailable, "settings_unavailable", "model profile storage is not configured", nil)
		return
	}
	profiles, err := s.profiles.List(r.Context())
	if err != nil {
		s.logger.Error("settings_profiles_list_failed", "error", err.Error())
		writeError(w, http.StatusInternalServerError, "settings_unavailable", "settings could not be loaded", nil)
		return
	}
	writeJSON(w, http.StatusOK, settingsResponse{
		Providers: providerCatalog, ModelProfiles: profiles,
		PromptVersions: []string{"v1"}, RerankerProfiles: []string{ragconfig.DefaultRerankerProfile},
		Limits: map[string]int{
			"min_chunk_size": ragconfig.MinChunkSize, "max_chunk_size": ragconfig.MaxChunkSize,
			"min_top_k": ragconfig.MinTopK, "max_top_k": ragconfig.MaxTopK,
			"min_rrf_rank_constant": ragconfig.MinRRFConstant, "max_rrf_rank_constant": ragconfig.MaxRRFConstant,
			"min_candidate_limit": ragconfig.MinRerankCandidateLimit, "max_candidate_limit": ragconfig.MaxRerankCandidateLimit,
		},
	})
}

func (s *server) getModelProfile(w http.ResponseWriter, r *http.Request) {
	if s.profiles == nil {
		writeError(w, http.StatusServiceUnavailable, "settings_unavailable", "model profile storage is not configured", nil)
		return
	}
	profile, err := s.profiles.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, modelprofile.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "no model profile with the given id", nil)
			return
		}
		s.logger.Error("settings_profile_get_failed", "error", err.Error())
		writeError(w, http.StatusInternalServerError, "settings_unavailable", "model profile could not be loaded", nil)
		return
	}
	writeJSON(w, http.StatusOK, profile)
}

func (s *server) createModelProfile(w http.ResponseWriter, r *http.Request) {
	if s.profiles == nil {
		writeError(w, http.StatusServiceUnavailable, "settings_unavailable", "model profile storage is not configured", nil)
		return
	}
	var req modelprofile.CreateRequest
	if !decodeStrict(w, r, &req, maxRequestBodyBytes) {
		return
	}
	profile, err := s.profiles.Create(r.Context(), req)
	if err != nil {
		var validation modelprofile.ValidationErrors
		switch {
		case errors.As(err, &validation):
			fields := make([]fieldErrorPayload, len(validation))
			for i, field := range validation {
				fields[i] = fieldErrorPayload{Field: field.Field, Message: field.Message}
			}
			writeError(w, http.StatusBadRequest, "validation_failed", "model profile failed validation", fields)
		case errors.Is(err, modelprofile.ErrNameConflict):
			writeError(w, http.StatusConflict, "name_conflict", err.Error(), nil)
		default:
			s.logger.Error("settings_profile_create_failed", "error", err.Error())
			writeError(w, http.StatusInternalServerError, "settings_unavailable", "model profile could not be saved", nil)
		}
		return
	}
	w.Header().Set("Location", "/api/v1/settings/model-profiles/"+profile.ID)
	writeJSON(w, http.StatusCreated, profile)
}
