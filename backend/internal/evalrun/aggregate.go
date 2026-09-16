package evalrun

import (
	"encoding/json"
	"ragbench-my/backend/internal/comparison"

	"ragbench-my/backend/internal/evaluation"
)

// Observations maps persisted results to aggregate inputs (RB-15's explicit
// populations: latency over completed/evaluator-failed queries, every
// scoring metric with its own denominator, distinct failure classes).
func Observations(results []Result) []evaluation.ResultObservation {
	obs := make([]evaluation.ResultObservation, 0, len(results))
	for _, r := range results {
		o := evaluation.ResultObservation{}
		switch r.Status {
		case "completed":
			o.Status = evaluation.StatusCompleted
		case "failed":
			o.Status = evaluation.StatusQueryFailed
		case "evaluator_failed":
			o.Status = evaluation.StatusEvaluatorFailed
		}
		if r.TotalLatencyMS != nil {
			o.TotalLatencyMS = *r.TotalLatencyMS
		}
		if r.RecallK != nil {
			o.HasRecall, o.Recall = true, *r.RecallK
		}
		if r.MRR != nil {
			o.HasMRR, o.MRR = true, *r.MRR
		}
		if r.AnswerRelevance != nil {
			o.HasRelevance, o.AnswerRelevance = true, *r.AnswerRelevance
		}
		if r.Groundedness != nil {
			o.HasGroundedness, o.Groundedness = true, *r.Groundedness
		}
		if r.Cost != nil {
			o.HasCost, o.Cost = true, *r.Cost
		}
		obs = append(obs, o)
	}
	return obs
}

// AggregateJSON serializes the run aggregate for the detail contract.
func AggregateJSON(results []Result) json.RawMessage {
	agg := evaluation.Aggregate(Observations(results))
	raw, err := json.Marshal(agg)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return raw
}

// ToRegionAggregates folds one run's persisted results into the comparison
// layer's explicit-completeness form: any failed case, evaluator failure or
// missing aggregate piece keeps the run incomplete (its comparison rules
// are therefore not_evaluable, never silently passed).
func ToRegionAggregates(results []Result) comparison.RegionAggregates {
	one := evaluation.Aggregate(Observations(results))
	complete := len(results) > 0
	var reasons []string
	for _, r := range results {
		if r.Status != "completed" {
			complete = false
			reasons = append(reasons, "case "+r.CaseKey+" status "+r.Status)
		}
	}
	if len(results) == 0 {
		reasons = append(reasons, "run has no executed cases; comparisons stay not_evaluable")
	}
	return comparison.RegionAggregates{
		RecallMean: one.RecallMean, MRRMean: one.MRRMean,
		LatencyP95MS: one.LatencyP95MS, CostTotal: one.CostTotal,
		Complete: complete, FailureReasons: reasons,
	}
}
