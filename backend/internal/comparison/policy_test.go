package comparison

import (
	"encoding/json"
	"testing"
)

func f(v float64) *float64 { return &v }

func ms(v int64) *int64 { return &v }

// The adopted policy values from EVALUATION.md, persisted via migration
// 0011; every threshold is an input, never a code constant.
func defaultRules() map[string]Rule {
	return map[string]Rule{
		"recall_k":    {Direction: DirectionHigher, Delta: DeltaRelative, Threshold: 0.05},
		"mrr":         {Direction: DirectionHigher, Delta: DeltaRelative, Threshold: 0.05},
		"latency_p95": {Direction: DirectionLower, Delta: DeltaRelative, Threshold: 0.25},
		"cost_total":  {Direction: DirectionLower, Delta: DeltaRelative, Threshold: 0.30},
	}
}

func region(recall, mrr *float64, latency *int64, cost *float64, complete bool) RegionAggregates {
	return RegionAggregates{
		RecallMean: recall, MRRMean: mrr,
		LatencyP95MS: latency, CostTotal: cost,
		Complete: complete,
	}
}

func TestValidatePolicyAcceptsPersistedShape(t *testing.T) {
	raw, _ := json.Marshal(Policy{Version: 1, Rules: defaultRules()})
	p, err := ValidatePolicy(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Rules) != 4 {
		t.Fatalf("rules = %d", len(p.Rules))
	}
}

func TestValidatePolicyRejectsBadRules(t *testing.T) {
	zero := 0.0
	nan := zero / zero
	cases := map[string]Policy{
		"unknown direction":  {Rules: map[string]Rule{"recall_k": {Direction: "up", Delta: DeltaRelative, Threshold: 0.05}}},
		"unknown delta":      {Rules: map[string]Rule{"recall_k": {Direction: DirectionHigher, Delta: "percentage", Threshold: 0.05}}},
		"negative threshold": {Rules: map[string]Rule{"recall_k": {Direction: DirectionHigher, Delta: DeltaAbsolute, Threshold: -1}}},
		"nan threshold":      {Rules: map[string]Rule{"recall_k": {Direction: DirectionHigher, Delta: DeltaAbsolute, Threshold: nan}}},
		"no rules":           {Rules: map[string]Rule{}},
	}
	for name, p := range cases {
		raw, _ := json.Marshal(p)
		if _, err := ValidatePolicy(raw); err == nil {
			t.Fatalf("%s must be rejected", name)
		}
	}
	if _, err := ValidatePolicy(json.RawMessage(`{`)); err == nil {
		t.Fatal("malformed JSON must be rejected")
	}
}

func compatOK() Compatibility {
	return Compatibility{Comparable: true, DatasetVersion: true, CorpusMatches: true,
		PolicyMatches: true, ScoringKMatches: true, Reasons: []string{}}
}

func TestQualityRegressionDetected(t *testing.T) {
	rules := defaultRules()
	cand := region(f(0.45), f(0.6), ms(1000), f(0.001), true)
	base := region(f(0.50), f(0.6), ms(400), f(0.001), true)

	// A complete compatible pair: every persisted rule is evaluated on its
	// own dimension, and each verdict explains itself.
	v := Compare(cand, base, Policy{Rules: rules}, compatOK(), false)
	if !v.Complete {
		t.Fatal("a complete compatible pair cannot be reported incomplete")
	}
	if got := outcome(v, "recall_k"); got != "regression" {
		t.Fatalf("recall drop must be a regression, got %s", got)
	}
	if v.RecallDelta == nil || abs(*v.RecallDelta+0.05) > 1e-9 {
		t.Fatalf("delta = %v, want -0.05", v.RecallDelta)
	}
	// Latency grows well beyond 25% with no quality gain: efficiency keeps
	// its own regression verdict — never folded into recall.
	if got := outcome(v, "latency_p95"); got != "regression" {
		t.Fatalf("latency must keep its own regression verdict, got %s", got)
	}
	if got := outcome(v, "mrr"); got != "passed" {
		t.Fatalf("mrr is unchanged, got %s", got)
	}
}

// outcome with a complete pair: strictly greater-than semantics, zero
// baseline, missing metrics, and the quality-gain exception。
func TestThresholdBoundarySemantics(t *testing.T) {
	rules := defaultRules()
	// Recall drop exactly at threshold (relative: (0.475-0.5)/0.5 = -0.05)
	// passes: strictly greater-than regression semantics.
	cand := region(f(0.475), f(0.6), ms(100), f(1.0), true)
	base := region(f(0.50), f(0.6), ms(100), f(1.0), true)
	v := Compare(cand, base, Policy{Rules: rules}, compatOK(), false)
	if got := outcome(v, "recall_k"); got != "passed" {
		t.Fatalf("exactly-threshold drop must pass, got %s", got)
	}

	// Recall drop slightly beyond threshold regresses.
	cand2 := region(f(0.44), f(0.6), ms(100), f(1.0), true) // relative drop 0.12
	v2 := Compare(cand2, base, Policy{Rules: rules}, compatOK(), false)
	if got := outcome(v2, "recall_k"); got != "regression" {
		t.Fatalf("drop beyond threshold must regress, got %s", got)
	}
}

func TestZeroBaselineRelativeIsNotEvaluable(t *testing.T) {
	zero := 0.0
	rules := map[string]Rule{"recall_k": {Direction: DirectionHigher, Delta: DeltaRelative, Threshold: 0.05}}
	cand := region(f(0.5), nil, nil, nil, true)
	base := region(&zero, nil, nil, nil, true)
	v := Compare(cand, base, Policy{Rules: rules}, compatOK(), false)
	if got := outcome(v, "recall_k"); got != "not_evaluable" {
		t.Fatalf("relative delta at a zero baseline is undefined by the persisted rule, got %s", got)
	}
	// A relative drop at a zero baseline keeps its undefined relative delta
	// but still regresses through the absolute existence of the increase.
}

func ptrFloat(v float64) *float64 { return &v }

func TestMissingMetricsStayNotEvaluable(t *testing.T) {
	rules := defaultRules()
	cand := region(f(0.5), nil, ms(100), nil, true)
	base := region(f(0.5), f(0.5), ms(100), f(1.0), true)
	v := Compare(cand, base, Policy{Rules: rules}, compatOK(), false)
	if got := outcome(v, "mrr"); got != "not_evaluable" {
		t.Fatalf("missing metric must be not_evaluable, got %s", got)
	}
	if got := outcome(v, "cost_total"); got != "not_evaluable" {
		t.Fatalf("missing cost must be not_evaluable, got %s", got)
	}
	if got := outcome(v, "recall_k"); got != "passed" {
		t.Fatalf("complete pair with no change passes, got %s", got)
	}
}

// The quality-gain exception: efficiency regression with quality gain is an
// explicit stored exception, not a silent pass or hidden through-zero.
func TestQualityGainExceptionForEfficiency(t *testing.T) {
	rules := map[string]Rule{
		"latency_p95": {Direction: DirectionLower, Delta: DeltaRelative, Threshold: 0.25},
		"cost_total":  {Direction: DirectionLower, Delta: DeltaRelative, Threshold: 0.30},
	}
	// Candidate slower/more expensive but recall improved.
	cand := region(f(0.55), nil, ms(200), f(2.0), true)
	base := region(f(0.50), nil, ms(100), f(1.0), true)
	v := Compare(cand, base, Policy{Rules: rules}, compatOK(), true)
	if got := outcome(v, "latency_p95"); got != "gain_exception" {
		t.Fatalf("latency with quality gain must be gain_exception, got %s", got)
	}
	if got := outcome(v, "cost_total"); got != "gain_exception" {
		t.Fatalf("cost with quality gain must be gain_exception, got %s", got)
	}

	// Same regression without a quality gain clearly regresses.
	v2 := Compare(cand, base, Policy{Rules: rules}, compatOK(), false)
	if got := outcome(v2, "latency_p95"); got != "regression" {
		t.Fatalf("efficiency regression without quality gain is a plain regression, got %s", got)
	}
}

// Non-comparable pair: no rule is passable, the whole verdict is gated.
func TestNonComparablePairNeverPasses(t *testing.T) {
	rules := defaultRules()
	compat := CompareIdentity(
		FactorIdentity{DatasetID: "ds1", DatasetVersion: 2, ScoringK: 5},
		FactorIdentity{DatasetID: "ds1", DatasetVersion: 1, ScoringK: 5},
	)
	if compat.Comparable {
		t.Fatal("different dataset versions are not comparable")
	}
	cand := region(f(1.0), f(1.0), ms(1), f(0.01), true)
	base := region(f(0.1), f(0.1), ms(1), f(0.01), true)
	v := Compare(cand, base, Policy{Rules: rules}, compat, false)
	if v.Complete {
		t.Fatal("non-comparable verdict must not claim completeness")
	}
	for i := range v.Rules {
		if v.Rules[i].State == "passed" {
			t.Fatalf("rule %s must not pass when incompatible", v.Rules[i].Metric)
		}
	}
}

// Compatibility gates in hand-calculated detail.
func TestCompareIdentityCompatibility(t *testing.T) {
	corpus := json.RawMessage(`[{"document_id":"d1","checksum":"c1"}]`)
	other := json.RawMessage(`[{"document_id":"d1","checksum":"c2"}]`)
	cases := []struct {
		name        string
		cand, base  FactorIdentity
		comparable_ bool
	}{
		{"same everything is comparable",
			FactorIdentity{DatasetID: "ds", DatasetVersion: 2, Corpus: corpus, ScoringK: 5, EvaluatorPolicy: json.RawMessage(`{"p":1}`)},
			FactorIdentity{DatasetID: "ds", DatasetVersion: 2, Corpus: corpus, ScoringK: 5, EvaluatorPolicy: json.RawMessage(`{"p":1}`)},
			true},
		{"different dataset version is not",
			FactorIdentity{DatasetID: "ds", DatasetVersion: 2, Corpus: corpus, ScoringK: 5, EvaluatorPolicy: json.RawMessage(`{"p":1}`)},
			FactorIdentity{DatasetID: "ds", DatasetVersion: 1, Corpus: corpus, ScoringK: 5, EvaluatorPolicy: json.RawMessage(`{"p":1}`)},
			false},
		{"different corpus checksums are not",
			FactorIdentity{DatasetID: "ds", DatasetVersion: 2, Corpus: other, ScoringK: 5, EvaluatorPolicy: json.RawMessage(`{"p":1}`)},
			FactorIdentity{DatasetID: "ds", DatasetVersion: 2, Corpus: corpus, ScoringK: 5, EvaluatorPolicy: json.RawMessage(`{"p":1}`)},
			false},
		{"different scoring K is not",
			FactorIdentity{DatasetID: "ds", DatasetVersion: 2, Corpus: corpus, ScoringK: 8, EvaluatorPolicy: json.RawMessage(`{"p":1}`)},
			FactorIdentity{DatasetID: "ds", DatasetVersion: 2, Corpus: corpus, ScoringK: 5, EvaluatorPolicy: json.RawMessage(`{"p":1}`)},
			false},
		{"different rubric policy is not",
			FactorIdentity{DatasetID: "ds", DatasetVersion: 2, Corpus: corpus, ScoringK: 5, EvaluatorPolicy: json.RawMessage(`{"p":2}`)},
			FactorIdentity{DatasetID: "ds", DatasetVersion: 2, Corpus: corpus, ScoringK: 5, EvaluatorPolicy: json.RawMessage(`{"p":1}`)},
			false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := CompareIdentity(tc.cand, tc.base)
			if c.Comparable != tc.comparable_ {
				t.Fatalf("comparable = %v, reasons = %v", c.Comparable, c.Reasons)
			}
		})
	}
}

// Chunk-size-only differences with the same source checksums stay
// comparable (document-level relevance unit dims the mapping).
func TestChunkSizeChangeKeepsCompatibility(t *testing.T) {
	a := json.RawMessage(`[{"document_id":"d1","checksum":"c1","chunk_size":800}]`)
	b := json.RawMessage(`[{"document_id":"d1","checksum":"c1","chunk_size":500}]`)
	if c := CompareIdentity(
		FactorIdentity{DatasetID: "ds", DatasetVersion: 1, Corpus: a, ScoringK: 5, EvaluatorPolicy: json.RawMessage(`p`)},
		FactorIdentity{DatasetID: "ds", DatasetVersion: 1, Corpus: b, ScoringK: 5, EvaluatorPolicy: json.RawMessage(`p`)}); !c.Comparable {
		t.Fatalf("same corpus, different chunk settings must stay comparable: %v", c.Reasons)
	}
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

func outcome(v Verdict, metric string) string {
	for _, r := range v.Rules {
		if r.Metric == metric {
			return r.State
		}
	}
	return "MISSING"
}
