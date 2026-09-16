package httpapi

import (
	"testing"

	"ragbench-my/backend/internal/comparison"
)

func TestScheduledVerdictKeepsRegressionAndIncompleteStatesVisible(t *testing.T) {
	tests := []struct {
		name   string
		input  comparison.Verdict
		status string
	}{
		{
			name:   "incompatible",
			input:  comparison.Verdict{Comparable: false, Reasons: []string{"dataset differs"}},
			status: "not_evaluable",
		},
		{
			name:   "regression rule",
			input:  comparison.Verdict{Comparable: true, Complete: true, Rules: []comparison.RuleOutcome{{State: "regression", Reason: "recall dropped"}}},
			status: "regression",
		},
		{
			name:   "incomplete",
			input:  comparison.Verdict{Comparable: true, Complete: false},
			status: "not_evaluable",
		},
		{
			name:   "passed",
			input:  comparison.Verdict{Comparable: true, Complete: true, Rules: []comparison.RuleOutcome{{State: "passed"}}},
			status: "passed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, _ := scheduledVerdict(tt.input)
			if status != tt.status {
				t.Fatalf("status = %q, want %q", status, tt.status)
			}
		})
	}
}
