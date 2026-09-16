package comparison

import (
	"fmt"
	"math"
)

// Verdict is the persisted comparison response shape (RB-19 contract): every
// dimension stays separate; quality and efficiency never collapse into one
// score. Pipeline/evaluator failures surface as distinct blockers from any
// quality regression.
type Verdict struct {
	// Comparable = compatible dataset version, source corpus, relevance
	// unit / scoring K, evaluator policy (see Compatibility). A
	// non-comparable pair is never a pass.
	Comparable bool     `json:"comparable"`
	Reasons    []string `json:"reasons"`
	// Completeness emerges from the values: an incomplete pair (a run with
	// a failed case or a missing aggregate metric the rules compare) can
	// never be a clean pass — the failing rule's state is not_evaluable.
	Complete bool          `json:"complete"`
	Rules    []RuleOutcome `json:"rules"`
	// Aggregates carries raw candidate/baseline headline values.
	RecallDelta     *float64 `json:"recall_delta"`
	MRRDelta        *float64 `json:"mrr_delta"`
	LatencyP95Delta *float64 `json:"latency_p95_delta"`
	CostDelta       *float64 `json:"cost_delta"`
	NDCGDelta       *float64 `json:"ndcg_delta"`
}

// RegionAggregates is one run's headline aggregates for comparison.
type RegionAggregates struct {
	RecallMean   *float64
	MRRMean      *float64
	LatencyP95MS *int64
	CostTotal    *float64
	NDCGMean     *float64
	// Flags the pair's completeness: any run with failed or
	// evaluator-failed cases or missing aggregate pieces is incomplete for
	// the affected rules.
	Complete       bool
	FailureReasons []string
}

// Compare applies the persisted rule set to two runs' aggregates under the
// persisted threshold policy, computing absolute and relative deltas, per
// rule verdicts and reason strings, honoring:
//   - strictly greater-than threshold semantics (a delta exactly equal to
//     the threshold passes; floating comparison uses a 1e-9 epsilon so the
//     documented boundary is exact);
//   - zero baseline deltas are absolute-only (relative delta is undefined:
//     the rule is not_evaluable rather than treating 0/0 as 0 or Infinity);
//   - missing metrics keep their own not_evaluable state;
//   - the quality-gain exception: an efficiency regression does not
//     register when recall_k gained, per the stored definition.
func Compare(candidate, baseline RegionAggregates, policy Policy, compat Compatibility, qualityGain bool) Verdict {
	v := Verdict{Comparable: compat.Comparable, Reasons: compat.Reasons, Rules: []RuleOutcome{}}
	if !compat.Comparable {
		v.Complete = false
		return v
	}

	values := map[string]MetricValues{
		"recall_k":    {candidate.RecallMean, baseline.RecallMean},
		"mrr":         {candidate.MRRMean, baseline.MRRMean},
		"ndcg_k":      {candidate.NDCGMean, baseline.NDCGMean},
		"latency_p95": {fromIntms(candidate.LatencyP95MS), fromIntms(baseline.LatencyP95MS)},
		"cost_total":  {candidate.CostTotal, baseline.CostTotal},
	}
	v.RecallDelta = deltaOf(candidate.RecallMean, baseline.RecallMean)
	v.MRRDelta = deltaOf(candidate.MRRMean, baseline.MRRMean)
	v.NDCGDelta = deltaOf(candidate.NDCGMean, baseline.NDCGMean)
	v.LatencyP95Delta = deltaOf(fromIntms(candidate.LatencyP95MS), fromIntms(baseline.LatencyP95MS))
	v.CostDelta = deltaOf(candidate.CostTotal, baseline.CostTotal)

	comparableRuns := candidate.Complete && baseline.Complete
	v.Complete = comparableRuns
	for _, metric := range orderedMetrics(policy) {
		rule := policy.Rules[metric]
		v.Rules = append(v.Rules, evaluateRule(metric, rule, values[metric], comparableRuns, qualityGain))
	}
	return v
}

func orderedMetrics(policy Policy) []string {
	// Stable ordering for reproducible response shapes.
	metrics := []string{"recall_k", "mrr", "ndcg_k", "latency_p95", "cost_total"}
	out := make([]string, 0, len(metrics))
	for _, m := range metrics {
		if _, ok := policy.Rules[m]; ok {
			out = append(out, m)
		}
	}
	return out
}

func fromIntms(v *int64) *float64 {
	if v == nil {
		return nil
	}
	f := float64(*v)
	return &f
}

// deltaDifference returns (candidate - baseline, true) when both present.
func deltaDifference(cand, base *float64) (*float64, bool) {
	if cand == nil || base == nil {
		return nil, false
	}
	d := *cand - *base
	if math.IsNaN(d) {
		return nil, false
	}
	return &d, true
}

func deltaOf(cand, base *float64) *float64 {
	if d, ok := deltaDifference(cand, base); ok {
		return d
	}
	return nil
}

func deltaValue(rule Rule, cand, base float64) (float64, bool) {
	switch rule.Delta {
	case DeltaAbsolute:
		return cand - base, true
	case DeltaRelative:
		if base == 0 {
			return 0, false // 0/0 is undefined; larger-than-zero baseline relative rule needs a value
		}
		return (cand - base) / math.Abs(base), true
	default:
		return 0, false
	}
}

func evaluateRule(metric string, rule Rule, v MetricValues, comparableRuns, qualityGain bool) RuleOutcome {
	oc := RuleOutcome{
		Metric: metric, Threshold: rule.Threshold,
		Direction: rule.Direction, DeltaKind: rule.Delta,
		Candidate: v.Candidate, Baseline: v.Baseline,
	}
	if v.Candidate == nil || v.Baseline == nil {
		oc.State = "not_evaluable"
		oc.Reason = "aggregate value missing for one side; a missing metric is never scored as zero"
		return oc
	}
	if !comparableRuns {
		oc.State = "not_evaluable"
		oc.Reason = "one or both runs are incomplete (failed cases or missing scores); incomplete pairs cannot produce a clean verdict"
		return oc
	}
	delta, ok := deltaValue(rule, *v.Candidate, *v.Baseline)
	if !ok {
		oc.State = "not_evaluable"
		oc.Reason = fmt.Sprintf("relative delta undefined at zero baseline for %s; compare this rule with an absolute threshold instead", metric)
		return oc
	}
	oc.Delta = &delta

	// Regression amount measured in the worse direction (positive when the
	// candidate is worse than the baseline). Strictly greater-than
	// semantics: a delta exactly equal to the threshold passes.
	degradation := delta
	if rule.Direction == DirectionHigher {
		degradation = -delta // positive when the candidate got worse
	}
	regressed := degradation > rule.Threshold+1e-9
	if regressed && qualityGain && rule.Direction == DirectionLower {
		oc.State = "gain_exception"
		oc.Reason = "efficiency regressed beyond threshold but quality gained (stored quality-gain exception)"
		return oc
	}
	if regressed {
		oc.State = "regression"
		param := fmt.Sprintf("%s %s delta %.6g vs threshold %.6g", metric, rule.Delta, delta, rule.Threshold)
		oc.Reason = "regression: " + param + " (direction " + rule.Direction + ")"
		return oc
	}
	oc.State = "passed"
	oc.Reason = "no regression beyond threshold (strictly greater-than semantics)"
	return oc
}
