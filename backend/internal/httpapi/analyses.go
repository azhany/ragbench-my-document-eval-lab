package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"ragbench-my/backend/internal/documentintelligence"
)

const maxAnalysisRequestBytes = 8 << 20

type AnalysisStore interface {
	Create(context.Context, documentintelligence.CreateRequest) (documentintelligence.Analysis, error)
	List(context.Context, string) ([]documentintelligence.Analysis, error)
	Get(context.Context, string) (documentintelligence.Analysis, error)
	RecordDispatch(context.Context, string, error) error
}

type AnalysisDispatcher interface {
	Dispatch(context.Context, documentintelligence.Job) error
}

type AnalysisRunner interface {
	RunStage(context.Context, documentintelligence.StageRequest) (documentintelligence.Analysis, error)
}

type AnalysisOptions struct {
	Store      AnalysisStore
	Dispatcher AnalysisDispatcher
	Runner     AnalysisRunner
}

func (s *server) createAnalysis(w http.ResponseWriter, r *http.Request) {
	if s.analysis.Store == nil {
		writeError(w, http.StatusServiceUnavailable, "capability_unavailable", "document intelligence is not configured", nil)
		return
	}
	var body struct {
		ModelProfile string `json:"model_profile"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAnalysisRequestBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid_body", "expected an optional model_profile JSON object", nil)
		return
	}
	if body.ModelProfile != "" {
		body.ModelProfile = strings.TrimSpace(body.ModelProfile)
	}
	analysis, err := s.analysis.Store.Create(r.Context(), documentintelligence.CreateRequest{
		DocumentID: r.PathValue("id"), ModelProfile: body.ModelProfile,
	})
	if err != nil {
		s.writeAnalysisError(w, err)
		return
	}
	w.Header().Set("Location", "/api/v1/document-analyses/"+analysis.ID)
	s.dispatchAnalysis(w, analysis)
}

func (s *server) dispatchAnalysis(w http.ResponseWriter, analysis documentintelligence.Analysis) {
	if analysis.Job == nil {
		writeError(w, http.StatusServiceUnavailable, "dispatch_unavailable", "document intelligence dispatcher is not configured", nil)
		return
	}
	if s.analysis.Dispatcher == nil {
		err := errors.New("document intelligence dispatcher is not configured")
		saveCtx, saveCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer saveCancel()
		if saveErr := s.analysis.Store.RecordDispatch(saveCtx, analysis.Job.ID, err); saveErr != nil {
			s.logger.Error("analysis_dispatch_persistence_failed", "analysis_id", analysis.ID, "job_id", analysis.Job.ID, "error", saveErr)
			writeError(w, http.StatusServiceUnavailable, "dispatch_state_unknown", "dispatch outcome could not be saved; inspect the analysis and retry dispatch", nil)
			return
		}
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error":    errorPayload{Code: "dispatch_unavailable", Message: err.Error()},
			"analysis": analysis,
		})
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	err := s.analysis.Dispatcher.Dispatch(ctx, *analysis.Job)
	saveCtx, saveCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer saveCancel()
	if saveErr := s.analysis.Store.RecordDispatch(saveCtx, analysis.Job.ID, err); saveErr != nil {
		s.logger.Error("analysis_dispatch_persistence_failed", "analysis_id", analysis.ID, "job_id", analysis.Job.ID, "error", saveErr)
		writeError(w, http.StatusServiceUnavailable, "dispatch_state_unknown", "dispatch outcome could not be saved; inspect the analysis and retry dispatch", nil)
		return
	}
	if err != nil {
		latest, getErr := s.analysis.Store.Get(saveCtx, analysis.ID)
		if getErr == nil {
			analysis = latest
		}
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error":    errorPayload{Code: "dispatch_failed", Message: err.Error()},
			"analysis": analysis,
		})
		return
	}
	latest, getErr := s.analysis.Store.Get(saveCtx, analysis.ID)
	if getErr == nil {
		analysis = latest
	}
	s.logger.Info("document_analysis_dispatched", "analysis_id", analysis.ID, "job_id", analysis.Job.ID, "run_id", analysis.Job.RunID)
	writeJSON(w, http.StatusAccepted, analysis)
}

func (s *server) listAnalysesForDocument(w http.ResponseWriter, r *http.Request) {
	analyses, err := s.analysis.Store.List(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeAnalysisError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"analyses": analyses})
}

func (s *server) getAnalysis(w http.ResponseWriter, r *http.Request) {
	analysis, err := s.analysis.Store.Get(r.Context(), r.PathValue("analysisId"))
	if err != nil {
		s.writeAnalysisError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, analysis)
}

func (s *server) retryAnalysisDispatch(w http.ResponseWriter, r *http.Request) {
	analysis, err := s.analysis.Store.Get(r.Context(), r.PathValue("analysisId"))
	if err != nil {
		s.writeAnalysisError(w, err)
		return
	}
	if analysis.Job == nil || (analysis.Job.State != "dispatch_pending" && analysis.Job.State != "dispatch_failed") {
		writeError(w, http.StatusConflict, "dispatch_not_retryable", "only a pending or failed dispatch can be retried", nil)
		return
	}
	s.dispatchAnalysis(w, analysis)
}

func (s *server) runAnalysisStage(w http.ResponseWriter, r *http.Request) {
	if s.analysis.Runner == nil {
		writeError(w, http.StatusServiceUnavailable, "capability_unavailable", "document intelligence runner is not configured", nil)
		return
	}
	var request documentintelligence.StageRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAnalysisRequestBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "stage requests require job_id, dag_id, run_id and optional evidence", nil)
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid_body", "stage request must contain exactly one JSON object", nil)
		return
	}
	request.AnalysisID = r.PathValue("analysisId")
	request.Stage = r.PathValue("stage")
	if request.JobID == "" || request.DAGID == "" || request.RunID == "" {
		writeError(w, http.StatusBadRequest, "invalid_body", "job_id, dag_id and run_id are required", nil)
		return
	}
	analysis, err := s.analysis.Runner.RunStage(r.Context(), request)
	if err != nil {
		s.writeAnalysisStageError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, analysis)
}

func (s *server) writeAnalysisError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, documentintelligence.ErrNotFound), errors.Is(err, documentintelligence.ErrNoRevision):
		writeError(w, http.StatusNotFound, "not_found", err.Error(), nil)
	case errors.Is(err, documentintelligence.ErrAnalysisActive):
		writeError(w, http.StatusConflict, "analysis_active", err.Error(), nil)
	case errors.Is(err, documentintelligence.ErrInvalidModel):
		writeError(w, http.StatusBadRequest, "validation_failed", err.Error(), nil)
	default:
		s.logger.Error("document_analysis_request_failed", "error", err)
		writeError(w, http.StatusInternalServerError, "persistence_failed", "document analysis state could not be read or persisted", nil)
	}
}

func (s *server) writeAnalysisStageError(w http.ResponseWriter, err error) {
	var stageErr *documentintelligence.StageError
	if errors.As(err, &stageErr) {
		status := http.StatusBadGateway
		switch stageErr.Code {
		case "invalid_stage", "extraction_evidence_missing", "extraction_evidence_invalid", "source_changed", "empty_extraction", "extraction_method_missing":
			status = http.StatusBadRequest
		case "schema_invalid", "malformed_model_output", "validation_missing", "validation_policy_unavailable":
			status = http.StatusUnprocessableEntity
		}
		writeError(w, status, stageErr.Code, safeStageMessage(stageErr), nil)
		return
	}
	switch {
	case errors.Is(err, documentintelligence.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", err.Error(), nil)
	case errors.Is(err, documentintelligence.ErrJobMismatch):
		writeError(w, http.StatusConflict, "job_mismatch", err.Error(), nil)
	case errors.Is(err, documentintelligence.ErrDeleted):
		writeError(w, http.StatusNotFound, "not_found", err.Error(), nil)
	case errors.Is(err, documentintelligence.ErrStageOrder), errors.Is(err, documentintelligence.ErrStageNotRetryable):
		writeError(w, http.StatusConflict, "stage_order", err.Error(), nil)
	default:
		s.logger.Error("document_analysis_stage_failed", "error", err)
		writeError(w, http.StatusInternalServerError, "persistence_failed", "document analysis stage state could not be persisted", nil)
	}
}

func safeStageMessage(err error) string {
	if err == nil {
		return "document analysis stage failed"
	}
	message := err.Error()
	if len(message) > 1000 {
		return message[:1000]
	}
	return message
}
