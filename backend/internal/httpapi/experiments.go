package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"ragbench-my/backend/internal/evalrun"
	"ragbench-my/backend/internal/experiment"
)

// ExperimentStore is the experiment surface the API needs (create/list/detail
// and the DAG-driven advance step). *experiment.Store plus the Orchestrator
// satisfy it; tests substitute fakes.
type ExperimentStore interface {
	Create(ctx context.Context, datasets evalrun.DatasetReader, configs experiment.ConfigReader, req experiment.CreateRequest) (experiment.Experiment, error)
	List(ctx context.Context) ([]experiment.Experiment, error)
	Get(ctx context.Context, id string) (experiment.Experiment, error)
}

type createExperimentPayload struct {
	Name             string            `json:"name"`
	Description      string            `json:"description"`
	DatasetID        string            `json:"dataset_id"`
	DatasetVersion   int               `json:"dataset_version"`
	RubricVersion    string            `json:"rubric_version"`
	ScoringK         int               `json:"scoring_k"`
	BaseConfigID     string            `json:"base_config_id"`
	Matrix           experiment.Matrix `json:"matrix"`
	CombinationLimit int               `json:"combination_limit"`
}

func (s *server) createExperiment(w http.ResponseWriter, r *http.Request) {
	var payload createExperimentPayload
	if !decodeStrict(w, r, &payload, maxRequestBodyBytes) {
		return
	}
	cfgReader, ok := s.configs.(experiment.ConfigReader)
	if !ok {
		writeError(w, http.StatusInternalServerError, "config_store_unsupported",
			"the wired configuration store cannot resolve names; experiment creation requires GetByName support", nil)
		return
	}
	e, err := s.experiments.Create(r.Context(), s.datasets, cfgReader, experiment.CreateRequest{
		Name: payload.Name, Description: payload.Description,
		DatasetID: payload.DatasetID, DatasetVersion: payload.DatasetVersion,
		RubricVersion: payload.RubricVersion, ScoringK: payload.ScoringK,
		BaseConfigID: payload.BaseConfigID, Matrix: payload.Matrix,
		CombinationLimit: payload.CombinationLimit,
	})
	if err != nil {
		s.writeExperimentError(w, err)
		return
	}
	w.Header().Set("Location", "/api/v1/experiments/"+e.ID)
	writeJSON(w, http.StatusCreated, e)
}

func (s *server) listExperiments(w http.ResponseWriter, r *http.Request) {
	experiments, err := s.experiments.List(r.Context())
	if err != nil {
		s.writeExperimentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"experiments": experiments})
}

func (s *server) getExperiment(w http.ResponseWriter, r *http.Request) {
	e, err := s.experiments.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeExperimentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, e)
}

// advanceExperiment performs one idempotent orchestration step; the
// rag_parameter_sweep DAG calls it until the returned state is terminal.
func (s *server) advanceExperiment(w http.ResponseWriter, r *http.Request) {
	result, err := s.orchestrator.Advance(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, experiment.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "no experiment with the given id", nil)
			return
		}
		s.writeExperimentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *server) writeExperimentError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, experiment.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "no experiment with the given id", nil)
	case errors.Is(err, experiment.ErrNameConflict):
		writeError(w, http.StatusConflict, "name_conflict", err.Error(), nil)
	default:
		s.logger.Warn("experiment request failed", slog.String("error", err.Error()))
		writeError(w, http.StatusUnprocessableEntity, "invalid_experiment", err.Error(), nil)
	}
}
