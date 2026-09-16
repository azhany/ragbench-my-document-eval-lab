package evalrun

import (
	"context"

	"ragbench-my/backend/internal/evaluation"
	"ragbench-my/backend/internal/providers"
)

// RubricJudge implements Judger with the real generation provider surface.
type RubricJudge struct {
	Generator providers.Generator
}

// RunJudge executes the versioned judge rubric for one case. All failures —
// provider errors, malformed JSON, out-of-range scores — are returned as
// errors so callers mark the case evaluator_failed instead of scoring low.
func (j RubricJudge) RunJudge(ctx context.Context, rubricVersion string, in evaluation.JudgeInput) (evaluation.JudgeScores, error) {
	return evaluation.RunJudge(ctx, j.Generator, rubricVersion, in)
}
