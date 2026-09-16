package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"ragbench-my/backend/internal/comparison"
	"ragbench-my/backend/internal/evalrun"
	"ragbench-my/backend/internal/evaluation"
	"ragbench-my/backend/internal/regression"
)

type RegressionStore interface {
	Create(context.Context, regression.CreateRequest) (regression.Check, error)
	Get(context.Context, string) (regression.Check, error)
	List(context.Context) ([]regression.Check, error)
	SetRunning(context.Context, string, string) error
	SetOutcome(context.Context, string, string, string, any) error
}

type regressionPayload struct {
	Name              string `json:"name"`
	DatasetID         string `json:"dataset_id"`
	DatasetVersion    int    `json:"dataset_version"`
	CandidateConfigID string `json:"candidate_config_id"`
	BaselineRunID     string `json:"baseline_run_id"`
	PolicyName        string `json:"policy_name"`
	PolicyVersion     int    `json:"policy_version"`
	Enabled           bool   `json:"enabled"`
}

func (s *server) createRegressionCheck(w http.ResponseWriter, r *http.Request) {
	var p regressionPayload
	if !decodeStrict(w, r, &p, maxRequestBodyBytes) {
		return
	}
	if p.PolicyName == "" {
		p.PolicyName = DefaultRegressionPolicy
	}
	c, err := s.regressions.Create(r.Context(), regression.CreateRequest{
		Name: p.Name, DatasetID: p.DatasetID, DatasetVersion: p.DatasetVersion,
		CandidateConfigID: p.CandidateConfigID, BaselineRunID: p.BaselineRunID,
		PolicyName: p.PolicyName, PolicyVersion: p.PolicyVersion, Enabled: p.Enabled,
	})
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_regression_check", err.Error(), nil)
		return
	}
	w.Header().Set("Location", "/api/v1/regression-checks/"+c.ID)
	writeJSON(w, http.StatusCreated, c)
}

func (s *server) listRegressionChecks(w http.ResponseWriter, r *http.Request) {
	checks, err := s.regressions.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not list scheduled checks", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"checks": checks})
}

func (s *server) getRegressionCheck(w http.ResponseWriter, r *http.Request) {
	c, err := s.regressions.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, regression.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "no scheduled regression check", nil)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not read scheduled check", nil)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

// startRegressionCheck creates and dispatches a normal eval run with the
// pinned candidate/dataset identity. The scheduled DAG then uses the normal
// evaluation and comparison endpoints; it never implements a second scorer.
func (s *server) startRegressionCheck(w http.ResponseWriter, r *http.Request) {
	if s.dispatcher == nil {
		writeError(w, http.StatusInternalServerError, "dispatcher_unavailable", "scheduled evaluation dispatcher is not configured", nil)
		return
	}
	c, err := s.regressions.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "no scheduled regression check", nil)
		return
	}
	if !c.Enabled {
		writeError(w, http.StatusUnprocessableEntity, "schedule_disabled", "scheduled regression check is disabled", nil)
		return
	}
	baseline, err := s.runs.GetRun(r.Context(), c.BaselineRunID)
	if err != nil {
		_ = s.regressions.SetOutcome(r.Context(), c.ID, "not_evaluable", "baseline run is missing", map[string]any{})
		writeError(w, http.StatusUnprocessableEntity, "baseline_unavailable", "baseline run is missing; check is not evaluable", nil)
		return
	}
	if baseline.Status != evalrun.StatusCompleted {
		reason := fmt.Sprintf("baseline run is %s; a scheduled check cannot pass", baseline.Status)
		_ = s.regressions.SetOutcome(r.Context(), c.ID, "not_evaluable", reason, map[string]any{})
		writeError(w, http.StatusUnprocessableEntity, "baseline_unavailable", reason, nil)
		return
	}
	policy, err := evaluationPolicyFromRun(baseline)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_baseline_policy", err.Error(), nil)
		return
	}
	run, err := s.runs.CreateRun(r.Context(), s.datasets, s.configs, evalrun.CreateRequest{
		DatasetID: c.DatasetID, DatasetVersion: c.DatasetVersion, RagConfigID: c.CandidateConfigID,
		RubricVersion: policy.RubricVersion, ScoringK: policy.ScoringK,
	})
	if err != nil {
		_ = s.regressions.SetOutcome(r.Context(), c.ID, "failed", err.Error(), map[string]any{})
		writeError(w, http.StatusUnprocessableEntity, "scheduled_run_failed", err.Error(), nil)
		return
	}
	if err := s.runs.MarkRunning(r.Context(), run.ID); err != nil {
		_ = s.regressions.SetOutcome(r.Context(), c.ID, "failed", err.Error(), map[string]any{})
		writeError(w, http.StatusInternalServerError, "scheduled_run_failed", err.Error(), nil)
		return
	}
	dispatchErr := s.dispatcher.DispatchRun(r.Context(), run.ID)
	if err := s.runs.RecordDispatch(r.Context(), run.ID, evalrun.EvalDagRunID(run.ID), dispatchErr); err != nil {
		writeError(w, http.StatusInternalServerError, "persistence_failed", err.Error(), nil)
		return
	}
	if dispatchErr != nil {
		_ = s.regressions.SetOutcome(r.Context(), c.ID, "failed", dispatchErr.Error(), map[string]any{})
		writeError(w, http.StatusBadGateway, "dispatch_failed", dispatchErr.Error(), nil)
		return
	}
	if err := s.regressions.SetRunning(r.Context(), c.ID, run.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "persistence_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"check": c, "run": run})
}

type finishRegressionPayload struct {
	Verdict comparison.Verdict `json:"verdict"`
}

func (s *server) finishRegressionCheck(w http.ResponseWriter, r *http.Request) {
	var p finishRegressionPayload
	if !decodeStrict(w, r, &p, maxRequestBodyBytes) {
		return
	}
	status, reason := scheduledVerdict(p.Verdict)
	if err := s.regressions.SetOutcome(r.Context(), r.PathValue("id"), status, reason, p.Verdict); err != nil {
		if errors.Is(err, regression.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "no scheduled regression check", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "persistence_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": status, "reason": reason, "verdict": p.Verdict})
}

func evaluationPolicyFromRun(run evalrun.Run) (evaluation.EvaluatorPolicyVersioned, error) {
	var p evaluation.EvaluatorPolicyVersioned
	if err := json.Unmarshal(run.EvaluatorPolicy, &p); err != nil {
		return p, err
	}
	return p, nil
}

func scheduledVerdict(v comparison.Verdict) (string, string) {
	if !v.Comparable {
		return "not_evaluable", "runs are not compatible: " + joinReasons(v.Reasons)
	}
	if len(v.Rules) == 0 {
		return "not_evaluable", "comparison produced no persisted policy rules"
	}
	for _, rule := range v.Rules {
		if rule.State == "regression" {
			return "regression", rule.Reason
		}
		if rule.State == "not_evaluable" {
			return "not_evaluable", rule.Reason
		}
	}
	if !v.Complete {
		return "not_evaluable", "one or both scheduled runs are incomplete"
	}
	return "passed", "all persisted comparison rules passed"
}

func joinReasons(reasons []string) string {
	if len(reasons) == 0 {
		return "no compatibility details"
	}
	return fmt.Sprint(reasons)
}
