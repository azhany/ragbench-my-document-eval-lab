package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"ragbench-my/backend/internal/comparison"
	"ragbench-my/backend/internal/evalrun"
)

// EvalRunStore is the eval run read/write surface the run API needs.
// *evalrun.Store satisfies it; tests substitute fakes.
type EvalRunStore interface {
	CreateRun(ctx context.Context, datasets evalrun.DatasetReader, configs evalrun.ConfigReader, req evalrun.CreateRequest) (evalrun.Run, error)
	ListRuns(ctx context.Context) ([]evalrun.Run, error)
	GetRun(ctx context.Context, id string) (evalrun.Run, error)
	GetResults(ctx context.Context, id string) ([]evalrun.Result, error)
	MarkRunning(ctx context.Context, id string) error
	RecordDispatch(ctx context.Context, id, dagRunID string, dispatchErr error) error
	FinalizeStatus(ctx context.Context, id string, totalCases int) (evalrun.Run, error)
	Policy(ctx context.Context, name string, version int) (evalrun.PolicyRow, error)
	Identity(ctx context.Context, id string) (comparison.FactorIdentity, error)
}

type createRunPayload struct {
	DatasetID      string `json:"dataset_id"`
	DatasetVersion int    `json:"dataset_version"`
	RagConfigID    string `json:"rag_config_id"`
	RubricVersion  string `json:"rubric_version"`
	ScoringK       int    `json:"scoring_k"`
}

// createEvalRun pins the dataset version, config, corpus revisions and
// evaluator policy in PostgreSQL, then dispatches rag_evaluation to Airflow.
// A dispatch failure is persisted as dispatch_failed with its reason and
// still returned as structured state — never as successful processing.
func (s *server) createEvalRun(w http.ResponseWriter, r *http.Request) {
	var payload createRunPayload
	if !decodeStrict(w, r, &payload, maxRequestBodyBytes) {
		return
	}
	run, err := s.runs.CreateRun(r.Context(), s.datasets, s.configs, evalrun.CreateRequest{
		DatasetID: payload.DatasetID, DatasetVersion: payload.DatasetVersion,
		RagConfigID: payload.RagConfigID, RubricVersion: payload.RubricVersion, ScoringK: payload.ScoringK,
	})
	if err != nil {
		s.writeRunError(w, err)
		return
	}

	if err := s.runs.MarkRunning(r.Context(), run.ID); err == nil {
		dagRun := evalrun.EvalDagRunID(run.ID)
		if dispatchErr := s.dispatcher.DispatchRun(r.Context(), run.ID); dispatchErr != nil {
			s.logger.Warn("eval run dispatch failed",
				slog.String("run_id", run.ID), slog.String("error", dispatchErr.Error()))
			if err := s.runs.RecordDispatch(r.Context(), run.ID, dagRun, dispatchErr); err != nil {
				s.logger.Error("recording dispatch failure failed", slog.String("error", err.Error()))
			}
			if run, _ = s.runs.GetRun(r.Context(), run.ID); run.ID != "" {
				writeJSON(w, http.StatusAccepted, run)
				return
			}
			writeError(w, http.StatusBadGateway, "dispatch_failed", dispatchErr.Error(), nil)
			return
		}
		if err := s.runs.RecordDispatch(r.Context(), run.ID, dagRun, nil); err != nil {
			s.logger.Error("recording dispatch failed", slog.String("error", err.Error()))
		}
	}
	run, _ = s.runs.GetRun(r.Context(), run.ID)
	w.Header().Set("Location", "/api/v1/eval-runs/"+run.ID)
	writeJSON(w, http.StatusCreated, run)
}

func (s *server) listEvalRuns(w http.ResponseWriter, r *http.Request) {
	runs, err := s.runs.ListRuns(r.Context())
	if err != nil {
		s.writeRunError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs})
}

// getEvalRun returns run detail with the aggregate computed from persisted
// results — missing scores keep their missing counts, never zeros.
func (s *server) getEvalRun(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("id")
	run, err := s.runs.GetRun(r.Context(), runID)
	if err != nil {
		s.writeRunError(w, err)
		return
	}
	results, err := s.runs.GetResults(r.Context(), runID)
	if err != nil {
		s.writeRunError(w, err)
		return
	}
	total := evalrun.AggregateJSON(results)
	run.Aggregate = total
	writeJSON(w, http.StatusOK, run)
}

func (s *server) listEvalResults(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("id")
	if _, err := s.runs.GetRun(r.Context(), runID); err != nil {
		s.writeRunError(w, err)
		return
	}
	results, err := s.runs.GetResults(r.Context(), runID)
	if err != nil {
		s.writeRunError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

// executeEvalCase runs one case through the shared pipeline with evaluation
// attribution and stores its idempotent result.
func (s *server) executeEvalCase(w http.ResponseWriter, r *http.Request) {
	result, err := s.executor.ExecuteCase(r.Context(), r.PathValue("id"), r.PathValue("caseId"))
	if err != nil {
		if errors.Is(err, evalrun.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "no such eval run or case", nil)
			return
		}
		s.logger.Error("eval case execution failed", slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "internal_error", "case execution failed: "+err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// finalizeEvalRun recomputes aggregate run status from persisted results
// (used by the DAG after its case loop and by manual recovery).
func (s *server) finalizeEvalRun(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("id")
	run, err := s.runs.GetRun(r.Context(), runID)
	if err != nil {
		s.writeRunError(w, err)
		return
	}
	vc, err := s.datasets.GetVersion(r.Context(), run.DatasetID, run.DatasetVersion)
	if err != nil {
		s.writeRunError(w, fmt.Errorf("resolve dataset version: %v", err))
		return
	}
	final, err := s.runs.FinalizeStatus(r.Context(), runID, len(vc.Cases))
	if err != nil {
		s.writeRunError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, final)
}

func (s *server) writeRunError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, evalrun.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "no eval run with the given id", nil)
	default:
		// Validation-shaped failures (unknown dataset version, unknown
		// rubric, blocked config) surface as structured 422 input errors.
		s.logger.Warn("eval run request failed", slog.String("error", err.Error()))
		writeError(w, http.StatusUnprocessableEntity, "invalid_run", err.Error(), nil)
	}
}
