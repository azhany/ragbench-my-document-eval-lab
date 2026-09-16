// Package evaluation holds the importable evaluation modules: retrieval
// scoring (Recall@K, MRR), versioned judge rubrics, citation scoring, and
// run aggregation. Scoring is separated from persistence so every rule here
// is unit-testable against hand-calculated fixtures (TEST_PLAN.md).
//
// Relevance unit: the dataset's documented stable unit — the document. A
// retrieved chunk is "relevant" when its document identity is expected.
// This unit is stable across chunk-size changes; stale chunk UUIDs are never
// treated as ground truth for a different index revision.
package evaluation

import (
	"math"
	"sort"
)

// RetrievedEvidence is one retrieved chunk reduced to its relevance-unit
// identity: document and 1-based rank.
type RetrievedEvidence struct {
	DocumentID string
	Rank       int
}

// ScoringSemantics documents the deliberate conventions, so no behavior in
// this file is an accident:
//
//   - K semantics: Recall@K considers the first K retrieved chunks after
//     truncation to K (retrieval already applied top-k; scoring truncates
//     again defensively to the configured K).
//   - Deduplication: multiple retrieved chunks of the same expected document
//     collapse to one hit (a document counts once).
//   - Missing relevance: an expected document absent from the top K lowers
//     Recall@K (it stays in the denominator); it never silently improves MRR.
//   - Undefined scores: zero expected documents makes Recall@K/MRR not
//     evaluable; callers must record a missing score, not a zero.

// RecallAtK returns (# distinct expected documents found within the first K
// retrieved chunks) / (# expected documents). Empty expected set or empty
// retrieval is NotEvaluable (callers must not treat it as zero quality).
func RecallAtK(expected []string, retrieved []RetrievedEvidence, k int) (float64, bool) {
	if len(expected) == 0 {
		return 0, false
	}
	if k <= 0 {
		return 0, false
	}
	expectedSet := make(map[string]bool, len(expected))
	for _, d := range expected {
		expectedSet[d] = true
	}
	hits := 0
	seen := map[string]bool{}
	for _, ev := range retrieved {
		if ev.Rank > k {
			continue
		}
		if seen[ev.DocumentID] {
			continue // deduplicate: a document counts once
		}
		seen[ev.DocumentID] = true
		if expectedSet[ev.DocumentID] {
			hits++
		}
	}
	return float64(hits) / float64(len(expected)), true
}

// MRR returns 1 / (first rank at which any expected document appears in the
// deduplicated retrieval), or 0 when no expected document was retrieved —
// the documented convention: no relevant hit is a failed ranking, not an
// undefined value.
func MRR(expected []string, retrieved []RetrievedEvidence) (float64, bool) {
	if len(expected) == 0 {
		return 0, false
	}
	expectedSet := make(map[string]bool, len(expected))
	for _, d := range expected {
		expectedSet[d] = true
	}
	hits := make([]int, 0, len(retrieved))
	seen := map[string]bool{}
	for _, ev := range retrieved {
		if seen[ev.DocumentID] {
			continue
		}
		seen[ev.DocumentID] = true
		if expectedSet[ev.DocumentID] {
			hits = append(hits, ev.Rank)
			break // first relevant rank only
		}
	}
	if len(hits) == 0 {
		return 0, true
	}
	sort.Ints(hits)
	return 1 / float64(hits[0]), true
}

// NDCGPolicyVersion identifies the persisted graded-judgment semantics.
const NDCGPolicyVersion = "ndcg-v1"

// NDCGAtK computes graded nDCG using gain 2^grade-1 and log2(rank+1)
// discount. Grades are keyed by the stable document relevance unit. Unknown
// retrieved documents have gain zero; an empty judgment set or an ideal list
// with zero gain is not evaluable, never a manufactured zero.
func NDCGAtK(judgments map[string]int, retrieved []RetrievedEvidence, k int) (float64, bool) {
	if len(judgments) == 0 || k <= 0 {
		return 0, false
	}
	grades := make([]int, 0, len(judgments))
	for _, grade := range judgments {
		if grade < 0 {
			return 0, false
		}
		grades = append(grades, grade)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(grades)))
	ideal := 0.0
	for rank, grade := range grades {
		if rank >= k {
			break
		}
		ideal += gain(grade) / math.Log2(float64(rank+2))
	}
	if ideal == 0 {
		return 0, false
	}
	actual := 0.0
	seen := map[string]bool{}
	for _, evidence := range retrieved {
		if evidence.Rank > k || seen[evidence.DocumentID] {
			continue
		}
		seen[evidence.DocumentID] = true
		actual += gain(judgments[evidence.DocumentID]) / math.Log2(float64(evidence.Rank+1))
	}
	return actual / ideal, true
}

func gain(grade int) float64 { return math.Pow(2, float64(grade)) - 1 }
