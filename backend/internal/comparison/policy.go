// Package comparison implements persisted regression-policy comparisons
// between evaluation runs (RB-19). Every threshold is a persisted policy
// input; a non-comparable or incomplete pair cannot be labelled a clean
// pass, and a quality regression stays distinct from a pipeline or
// evaluator failure.
package comparison

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
)

// Directions and comparison kinds.
const (
	DirectionHigher = "higher" // quality: increase is good
	DirectionLower  = "lower"  // efficiency: increase is bad without quality gain
	DeltaAbsolute   = "absolute"
	DeltaRelative   = "relative"
)

// Rule is one persisted threshold rule for one metric.
type Rule struct {
	Direction string  `json:"direction"`
	Delta     string  `json:"delta"`
	Threshold float64 `json:"threshold"`
}

// Policy is the persisted policy payload validated before any comparison.
type Policy struct {
	Version int             `json:"version"`
	Rules   map[string]Rule `json:"rules"`
}

var policyErr = errors.New("invalid regression policy")

// ValidatePolicy enforces the documented rule shape:
// every metric has a known direction and delta kind, non-negative
// threshold, relative thresholds are proportion-like (>= 0) and absolute
// deltas are plain numbers; NaN/Inf is rejected for all.
func ValidatePolicy(raw json.RawMessage) (Policy, error) {
	var p Policy
	if err := json.Unmarshal(raw, &p); err != nil {
		return Policy{}, fmt.Errorf("%w: %v", policyErr, err)
	}
	return p, p.validate()
}

func (p Policy) validate() error {
	truth := map[string]bool{DirectionHigher: true, DirectionLower: true}
	deltaTrue := map[string]bool{DeltaAbsolute: true, DeltaRelative: true}
	for metric, rule := range p.Rules {
		if !truth[rule.Direction] {
			return fmt.Errorf("%w: metric %q direction must be \"higher\" or \"lower\"", policyErr, metric)
		}
		if !deltaTrue[rule.Delta] {
			return fmt.Errorf("%w: metric %q delta must %q or %q", policyErr, metric, DeltaAbsolute, DeltaRelative)
		}
		if err := thresholdErr(rule, metric); err != nil {
			return err
		}
	}
	if err := ruleCount(p.Rules); err != nil {
		return err
	}
	return nil
}

func ruleCount(rules map[string]Rule) error {
	if len(rules) == 0 {
		return fmt.Errorf("%w: policy has no rules", policyErr)
	}
	return nil
}

func thresholdErr(rule Rule, metric string) error {
	if math.IsNaN(rule.Threshold) || math.IsInf(rule.Threshold, 0) || rule.Threshold < 0 {
		return fmt.Errorf("%w: metric %q threshold must be a non-negative number", policyErr, metric)
	}
	return nil
}

// MetricValues is one metric's two comparable numbers plus per-metric
// comparability flags.
type MetricValues struct {
	Candidate *float64
	Baseline  *float64
}

// RuleOutcome explains one rule's verdict: pass, regression, or the reason
// it could not be evaluated (missing values on either side).
type RuleOutcome struct {
	Metric    string   `json:"metric"`
	State     string   `json:"state"` // "passed" | "regression" | "not_evaluable" | "gain_exception"
	Reason    string   `json:"reason"`
	Candidate *float64 `json:"candidate"`
	Baseline  *float64 `json:"baseline"`
	Delta     *float64 `json:"delta"`
	Threshold float64  `json:"threshold"`
	Direction string   `json:"direction"`
	DeltaKind string   `json:"delta_kind"`
}

// Compatibility is one pair-comparison gate: dataset version identity,
// source corpus, relevance unit / scoring K, and evaluator policy must be
// equal before deltas are meaningful (differing index settings are allowed
// when the document-level relevance unit supports the mapping; that gate is
// the corpus comparison plus the stored relevance unit).
type Compatibility struct {
	Comparable      bool     `json:"comparable"`
	Reasons         []string `json:"reasons"`
	DatasetVersion  bool     `json:"dataset_version_matches"`
	CorpusMatches   bool     `json:"source_corpus_matches"`
	PolicyMatches   bool     `json:"evaluator_policy_matches"`
	ScoringKMatches bool     `json:"scoring_k_matches"`
}

// FactorIdentity carries the pinned inputs compared by compatibility.
type FactorIdentity struct {
	DatasetID       string
	DatasetVersion  int
	Corpus          json.RawMessage
	ScoringK        int
	EvaluatorPolicy json.RawMessage
}

// sameCorpus compares pinned corpus revision snapshots: same document set
// with the same checksums (chunk-size changes make different revisions of
// the same checksummed source — allowed by the document relevance unit).
func sameCorpus(a, b json.RawMessage) (bool, error) {
	var da, db []struct {
		DocumentID string `json:"document_id"`
		Checksum   string `json:"checksum"`
	}
	if err := json.Unmarshal(a, &da); err != nil {
		return false, nil
	}
	if err := json.Unmarshal(b, &db); err != nil {
		return false, nil
	}
	makeSet := func(in []struct {
		DocumentID string `json:"document_id"`
		Checksum   string `json:"checksum"`
	}) map[string]string {
		set := make(map[string]string, len(in))
		for _, d := range in {
			set[d.DocumentID] = d.Checksum
		}
		return set
	}
	sa, sb := makeSet(da), makeSet(db)
	if len(sa) != len(sb) {
		return false, nil
	}
	for id, checksum := range sa {
		if sb[id] != checksum {
			return false, nil
		}
	}
	return true, nil
}

// CompareIdentity checks one candidate against one baseline pair's pinned
// inputs; the returned verdict never collapses dimensions.
func CompareIdentity(candidate, baseline FactorIdentity) Compatibility {
	var reasons []string
	c := Compatibility{Reasons: []string{}, Comparable: true}
	if candidate.DatasetID != baseline.DatasetID {
		c.DatasetVersion = false
		c.Comparable = false
		reasons = append(reasons, "different dataset")
	} else if candidate.DatasetVersion != baseline.DatasetVersion {
		c.DatasetVersion = false
		c.Comparable = false
		reasons = append(reasons, fmt.Sprintf("different dataset version (candidate v%d vs baseline v%d)", candidate.DatasetVersion, baseline.DatasetVersion))
	} else {
		c.DatasetVersion = true
	}

	corpusOK, err := sameCorpus(candidate.Corpus, baseline.Corpus)
	if err != nil || !corpusOK {
		c.CorpusMatches = false
		c.Comparable = false
		reasons = append(reasons, "source corpus (document identities/checksums or revisions) differs; document-level evidence mapping is not the same")
	} else {
		c.CorpusMatches = true
	}
	if candidate.ScoringK != baseline.ScoringK {
		c.ScoringKMatches = false
		c.Comparable = false
		reasons = append(reasons, fmt.Sprintf("scoring K differs (%d vs %d)", candidate.ScoringK, baseline.ScoringK))
	} else {
		c.ScoringKMatches = true
	}
	if string(candidate.EvaluatorPolicy) != string(baseline.EvaluatorPolicy) {
		c.PolicyMatches = false
		c.Comparable = false
		reasons = append(reasons, "evaluator policy (rubric version) differs")
	} else {
		c.PolicyMatches = true
	}
	c.Reasons = reasons
	return c
}
