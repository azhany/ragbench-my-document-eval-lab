package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"ragbench-my/backend/internal/evaldata"
)

// DatasetStore is the eval dataset persistence surface the dataset API
// needs. *evaldata.Store satisfies it.
type DatasetStore interface {
	CreateDataset(ctx context.Context, name, description string, cases []evaldata.CaseInput) (evaldata.Dataset, error)
	NewVersion(ctx context.Context, id string, cases []evaldata.CaseInput) (evaldata.Dataset, error)
	List(ctx context.Context) ([]evaldata.Dataset, error)
	GetDataset(ctx context.Context, id string) (evaldata.Dataset, error)
	GetVersion(ctx context.Context, id string, version int) (evaldata.VersionedCases, error)
}

// maxDatasetBodyBytes bounds dataset import payloads: dozens of cases with
// short answers fit comfortably; anything larger is a client error.
const maxDatasetBodyBytes = 1 << 20 // 1 MiB

type datasetPayload struct {
	Name        string               `json:"name"`
	Description string               `json:"description"`
	Cases       []evaldata.CaseInput `json:"cases"`
}

func decodeDatasetBody(w http.ResponseWriter, r *http.Request) (datasetPayload, bool) {
	var payload datasetPayload
	body := http.MaxBytesReader(w, r.Body, maxDatasetBodyBytes)
	dec := json.NewDecoder(body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&payload); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeError(w, http.StatusRequestEntityTooLarge, "body_too_large",
				"dataset payload exceeds the documented size limit", nil)
			return payload, false
		}
		writeError(w, http.StatusBadRequest, "invalid_body",
			fmt.Sprintf("request body is not a valid dataset payload: %v", err), nil)
		return payload, false
	}
	return payload, true
}

// createDataset imports a dataset: immutable version 1 with its reviewed cases.
func (s *server) createDataset(w http.ResponseWriter, r *http.Request) {
	payload, ok := decodeDatasetBody(w, r)
	if !ok {
		return
	}
	ds, err := s.datasets.CreateDataset(r.Context(), payload.Name, payload.Description, payload.Cases)
	if err != nil {
		s.writeDatasetError(w, err)
		return
	}
	w.Header().Set("Location", "/api/v1/eval-datasets/"+ds.ID)
	writeJSON(w, http.StatusCreated, ds)
}

func (s *server) listDatasets(w http.ResponseWriter, r *http.Request) {
	datasets, err := s.datasets.List(r.Context())
	if err != nil {
		s.writeDatasetError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"datasets": datasets})
}

func (s *server) getDataset(w http.ResponseWriter, r *http.Request) {
	version, ok := versionParam(w, r)
	if !ok {
		return
	}
	vc, err := s.datasets.GetVersion(r.Context(), r.PathValue("id"), version)
	if err != nil {
		s.writeDatasetError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, vc)
}

// newDatasetVersion replaces the case set, creating immutable version N+1.
func (s *server) newDatasetVersion(w http.ResponseWriter, r *http.Request) {
	payload, ok := decodeDatasetBody(w, r)
	if !ok {
		return
	}
	ds, err := s.datasets.NewVersion(r.Context(), r.PathValue("id"), payload.Cases)
	if err != nil {
		s.writeDatasetError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, ds)
}

func versionParam(w http.ResponseWriter, r *http.Request) (int, bool) {
	raw := r.URL.Query().Get("version")
	if raw == "" {
		return 0, true
	}
	version, err := strconv.Atoi(raw)
	if err != nil || version < 0 {
		writeError(w, http.StatusUnprocessableEntity, "invalid_version",
			"version must be a non-negative integer (0 = latest)", nil)
		return 0, false
	}
	return version, true
}

func (s *server) writeDatasetError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, evaldata.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "no eval dataset with the given id", nil)
	case errors.Is(err, evaldata.ErrDocumentUnknown):
		writeError(w, http.StatusUnprocessableEntity, "invalid_reference", err.Error(), nil)
	case errors.Is(err, evaldata.ErrNameConflict):
		writeError(w, http.StatusConflict, "name_conflict", err.Error(), nil)
	case errors.Is(err, evaldata.ErrCaseKeyDuplicate):
		writeError(w, http.StatusBadRequest, "duplicate_case_key", err.Error(), nil)
	default:
		// Validation-shaped errors (bounds, empty versions) are client errors;
		// everything else is treated as a server fault.
		s.logger.Warn("eval dataset request failed", slog.String("error", err.Error()))
		writeError(w, http.StatusUnprocessableEntity, "invalid_dataset",
			err.Error(), nil)
	}
}
