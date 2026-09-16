package evalrun

import (
	"context"
	"errors"

	"ragbench-my/backend/internal/evaluation"
	"ragbench-my/backend/internal/rag"
)

// stubPipeline answers the executor with a deterministic successful trace.
type stubPipeline struct {
	out ragResponseAlias
}

func (s stubPipeline) AskEvaluation(ctx context.Context, req rag.ChatRequest) (rag.ChatResponse, error) {
	return rag.ChatResponse{
		Answer:    s.out.answer,
		Citations: []rag.Citation{{DocumentID: s.out.expectedDoc, ChunkID: "chunk-1"}},
		Trace: rag.ChatTrace{
			TraceID: s.out.traceID, LatencyMS: 42,
			InputTokens:          intPtr(11),
			OutputTokens:         intPtr(7),
			EmbeddingInputTokens: intPtr(3),
		},
		Retrieved: []rag.RetrievedIdentity{{DocumentID: s.out.expectedDoc, ChunkID: "chunk-1", Rank: 1}},
	}, nil
}

type ragResponseAlias struct {
	answer      string
	traceID     string
	expectedDoc string
}

type stubJudger struct{}

func (stubJudger) RunJudge(ctx context.Context, rubric string, in evaluation.JudgeInput) (evaluation.JudgeScores, error) {
	return evaluation.JudgeScores{
		AnswerRelevance: &evaluation.JudgeScore{Score: 4, Rationale: "addresses the question"},
		Groundedness:    &evaluation.JudgeScore{Score: 5, Rationale: "claims match evidence"},
		InputTokens:     intPtr(50), OutputTokens: intPtr(9),
	}, nil
}

type failingJudger struct{}

func (failingJudger) RunJudge(ctx context.Context, rubric string, in evaluation.JudgeInput) (evaluation.JudgeScores, error) {
	return evaluation.JudgeScores{}, errors.New("judge model call failed: 503")
}

func intPtr(v int) *int { return &v }
