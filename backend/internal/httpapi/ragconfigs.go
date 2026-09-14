package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"ragbench-my/backend/internal/ragconfig"
)

// ConfigStore is the persistence surface the config API needs. *ragconfig.Store
// satisfies it; tests can substitute a fake.
type ConfigStore interface {
	Create(ctx context.Context, req ragconfig.CreateRequest) (ragconfig.Config, error)
	List(ctx context.Context) ([]ragconfig.Config, error)
	Get(ctx context.Context, id string) (ragconfig.Config, error)
}

// maxRequestBodyBytes bounds config creation payloads; configurations are a
// dozen small fields, so anything larger is a client error.
const maxRequestBodyBytes = 64 << 10 // 64 KiB

func (s *server) createConfig(w http.ResponseWriter, r *http.Request) {
	var req ragconfig.CreateRequest
	body := http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	dec := json.NewDecoder(body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body",
			fmt.Sprintf("request body is not a valid configuration: %v", err), nil)
		return
	}

	cfg, err := s.configs.Create(r.Context(), req)
	if err != nil {
		s.writeConfigError(w, err)
		return
	}

	w.Header().Set("Location", "/api/v1/rag-configs/"+cfg.ID)
	writeJSON(w, http.StatusCreated, cfg)
}

func (s *server) listConfigs(w http.ResponseWriter, r *http.Request) {
	configs, err := s.configs.List(r.Context())
	if err != nil {
		s.writeConfigError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"configs": configs})
}

func (s *server) getConfig(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.configs.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeConfigError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

func (s *server) writeConfigError(w http.ResponseWriter, err error) {
	var verrs ragconfig.ValidationErrors
	switch {
	case errors.As(err, &verrs):
		fields := make([]fieldErrorPayload, len(verrs))
		for i, fe := range verrs {
			fields[i] = fieldErrorPayload{Field: fe.Field, Message: fe.Message}
		}
		writeError(w, http.StatusBadRequest, "validation_failed",
			"configuration failed validation", fields)
	case errors.Is(err, ragconfig.ErrNameConflict):
		writeError(w, http.StatusConflict, "name_conflict", err.Error(), nil)
	case errors.Is(err, ragconfig.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found",
			"no rag config with the given id", nil)
	default:
		s.logger.Error("rag config request failed", slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "internal_error",
			"unexpected server error", nil)
	}
}
