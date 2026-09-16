package httpapi

import (
	"net/http"
	"strconv"

	"ragbench-my/backend/internal/comparison"
	"ragbench-my/backend/internal/evalrun"
)

// DefaultRegressionPolicy names the seeded persisted policy used when the
// caller does not choose another one; its thresholds are stored policy
// values (eval_regression_policies), never hidden defaults inside code.
const DefaultRegressionPolicy = "default-v1"

// compareResponse is the documented comparison contract.
type compareResponse struct {
	CandidateID   string                   `json:"candidate_id"`
	BaselineID    string                   `json:"baseline_id"`
	Policy        evalrun.PolicyRow        `json:"policy"`
	Compatibility comparison.Compatibility `json:"compatibility"`
	Verdict       comparison.Verdict       `json:"verdict"`
}

// compareEvalRun is GET /api/v1/eval-runs/{id}/compare/{baselineId}?policy=.
//
// Gates: only runs with compatible dataset version, source corpus, scoring
// K and evaluator policy are compared; a non-comparable or incomplete pair
// is never a clean pass; a quality regression stays distinct from a
// pipeline/evaluator failure.
func (s *server) compareEvalRun(w http.ResponseWriter, r *http.Request) {
	candidateID := r.PathValue("id")
	baselineID := r.PathValue("baselineId")

	policyName := r.URL.Query().Get("policy")
	if policyName == "" {
		policyName = DefaultRegressionPolicy
	}
	policyVersion := 0
	if raw := r.URL.Query().Get("version"); raw != "" {
		parsed, parseErr := strconv.Atoi(raw)
		policyVersion = parsed
		if parseErr != nil || policyVersion < 1 {
			writeError(w, http.StatusUnprocessableEntity, "invalid_policy_version", "policy version must be a positive integer", nil)
			return
		}
	}
	policyRow, err := s.runs.Policy(r.Context(), policyName, policyVersion)
	if err != nil {
		s.writeRunError(w, err)
		return
	}
	policy, err := comparison.ValidatePolicy(policyRow.Policy)
	if err != nil {
		s.writeRunError(w, err)
		return
	}

	candidateIdentity, err := s.runs.Identity(r.Context(), candidateID)
	if err != nil {
		s.writeRunError(w, err)
		return
	}
	baselineIdentity, err := s.runs.Identity(r.Context(), baselineID)
	if err != nil {
		s.writeRunError(w, err)
		return
	}
	compat := comparison.CompareIdentity(candidateIdentity, baselineIdentity)

	candidateRegion, ok := s.compareRegion(w, r, candidateID)
	if !ok {
		return
	}
	baselineRegion, ok := s.compareRegion(w, r, baselineID)
	if !ok {
		return
	}

	// The stored quality-gain definition: recall gaining can tolerate an
	// efficiency regression exactly as the persisted policy defines.
	qualityGain := baselineRegion.RecallMean != nil && candidateRegion.RecallMean != nil &&
		*candidateRegion.RecallMean > *baselineRegion.RecallMean

	verdict := comparison.Compare(candidateRegion, baselineRegion, policy, compat, qualityGain)
	if recorder, ok := s.runs.(evalrun.ComparisonRecorder); ok {
		if err := recorder.RecordComparison(r.Context(), candidateID, baselineID, policyRow.Name, policyRow.Version, verdict); err != nil {
			s.logger.Error("comparison persistence failed", "error", err.Error())
			writeError(w, http.StatusInternalServerError, "persistence_failed", "comparison outcome could not be persisted", nil)
			return
		}
	}

	writeJSON(w, http.StatusOK, compareResponse{
		CandidateID: candidateID, BaselineID: baselineID,
		Policy: policyRow, Compatibility: compat, Verdict: verdict,
	})
}

// compareRegion folds one run's persisted results into the explicit
// completeness semantics: failed cases, evaluator failures or missing
// aggregates keep rules not_evaluable — incomplete pairs cannot pass.
func (s *server) compareRegion(w http.ResponseWriter, r *http.Request, runID string) (comparison.RegionAggregates, bool) {
	results, err := s.runs.GetResults(r.Context(), runID)
	if err != nil {
		s.writeRunError(w, err)
		return comparison.RegionAggregates{}, false
	}
	agg := evalrun.ToRegionAggregates(results)
	return agg, true
}
