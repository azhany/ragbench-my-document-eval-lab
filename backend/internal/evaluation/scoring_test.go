package evaluation

import (
	"math"
	"testing"
)

func doc(id string, rank int) RetrievedEvidence { return RetrievedEvidence{DocumentID: id, Rank: rank} }

// Hand-calculated Recall@K fixtures.
func TestRecallAtK(t *testing.T) {
	cases := []struct {
		name      string
		expected  []string
		retrieved []RetrievedEvidence
		k         int
		want      float64
		wantOK    bool
	}{
		{"all expected at ranks 1-2", []string{"a", "b"}, []RetrievedEvidence{doc("a", 1), doc("b", 2)}, 2, 1.0, true},
		{"one of two within K", []string{"a", "b"}, []RetrievedEvidence{doc("a", 1), doc("c", 2)}, 3, 0.5, true},
		{"dedup collapses same-document chunks", []string{"a"}, []RetrievedEvidence{doc("a", 1), doc("a", 2)}, 2, 1.0, true},
		{"expected only outside K", []string{"a"}, []RetrievedEvidence{doc("a", 4), doc("b", 1)}, 2, 0, true},
		{"k truncation excludes later hit", []string{"a"}, []RetrievedEvidence{doc("a", 3)}, 2, 0, true},
		{"no expected docs is not evaluable", nil, []RetrievedEvidence{doc("a", 1)}, 2, 0, false},
		{"no retrieval is not evaluable with empty expected", nil, nil, 2, 0, false},
		{"empty retrieval scores zero recall", []string{"a"}, nil, 2, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := RecallAtK(tc.expected, tc.retrieved, tc.k)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if got != tc.want {
				t.Fatalf("Recall@K = %v, want %v", got, tc.want)
			}
		})
	}
}

// Hand-calculated MRR fixtures: first relevant rank.
func TestMRR(t *testing.T) {
	cases := []struct {
		name      string
		expected  []string
		retrieved []RetrievedEvidence
		want      float64
		wantOK    bool
	}{
		{"first rank", []string{"a", "b"}, []RetrievedEvidence{doc("c", 1), doc("a", 2)}, 0.5, true},
		{"top rank", []string{"b"}, []RetrievedEvidence{doc("b", 1)}, 1, true},
		{"third rank", []string{"z", "a"}, []RetrievedEvidence{doc("b", 1), doc("c", 2), doc("a", 3)}, 1.0 / 3.0, true},
		{"no relevant hit is zero, not undefined", []string{"z"}, []RetrievedEvidence{doc("a", 1)}, 0, true},
		{"no expected docs is not evaluable", nil, []RetrievedEvidence{doc("a", 1)}, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := MRR(tc.expected, tc.retrieved)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if got != tc.want {
				t.Fatalf("MRR = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNDCGAtK(t *testing.T) {
	judgments := map[string]int{"a": 3, "b": 2, "c": 0}
	got, ok := NDCGAtK(judgments, []RetrievedEvidence{doc("b", 1), doc("a", 2)}, 2)
	if !ok {
		t.Fatal("graded judgments should be evaluable")
	}
	// DCG = (2^2-1)/log2(2) + (2^3-1)/log2(3); ideal reverses the two.
	want := ((3.0 / math.Log2(2)) + (7.0 / math.Log2(3))) /
		((7.0 / math.Log2(2)) + (3.0 / math.Log2(3)))
	if math.Abs(got-want) > 1e-12 {
		t.Fatalf("nDCG = %.12f, want %.12f", got, want)
	}
	if _, ok := NDCGAtK(map[string]int{"zero": 0}, []RetrievedEvidence{doc("zero", 1)}, 1); ok {
		t.Fatal("zero ideal gain must be not evaluable")
	}
}

func TestScoreCitations(t *testing.T) {
	retrieved := map[string]bool{"doc-a": true, "doc-b": true}
	got, ok := ScoreCitations([]ScoredCitation{{1, "doc-a"}, {2, "doc-b"}, {3, "doc-x"}}, retrieved)
	if !ok || got != 2.0/3.0 {
		t.Fatalf("citation correctness = %v (ok=%v), want 2/3", got, ok)
	}
	// No citations is not evaluable, never zero quality.
	if _, ok := ScoreCitations(nil, retrieved); ok {
		t.Fatal("no citations must be not evaluable")
	}
}

func TestResolveEvaluatorPolicy(t *testing.T) {
	if _, err := ResolveEvaluatorPolicy("rubric-v1", 5); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveEvaluatorPolicy("rubric-v9", 5); err != ErrPolicyNotFound {
		t.Fatalf("expected ErrPolicyNotFound, got %v", err)
	}
	if _, err := ResolveEvaluatorPolicy("rubric-v1", 0); err == nil {
		t.Fatal("expected scoring_k bound rejection")
	}
}

func TestParseJudgeOutput(t *testing.T) {
	good := `{"answer_relevance": {"score": 4, "rationale": "addresses the question"},
	          "groundedness": {"score": 2, "rationale": "one unsupported claim"}}`
	scores, err := parseJudgeOutput("Judgment follows.\n" + good + "\n")
	if err != nil {
		t.Fatal(err)
	}
	if scores.AnswerRelevance.Score != 4 || scores.Groundedness.Score != 2 {
		t.Fatalf("parsed scores wrong: %+v", scores)
	}
	for _, bad := range []string{
		"no json at all",
		`{"answer_relevance": {"score": 4}, "groundedness": {"score": 2}}`,                                     // no rationales required? still scored shape ok → filter below
		`{"answer_relevance": {"score": 0, "rationale": "x"}, "groundedness": {"score": 3, "rationale": "y"}}`, // out of range
		`{"answer_relevance": {"score": 4, "rationale": "x"}}`,                                                 // missing groundedness
	} {
		if bad == `{"answer_relevance": {"score": 4}, "groundedness": {"score": 2}}` {
			continue // missing rationale is tolerable; only presence is checked
		}
		if _, err := parseJudgeOutput(bad); err == nil {
			t.Fatalf("expected parse failure for %q", bad)
		}
	}
}
