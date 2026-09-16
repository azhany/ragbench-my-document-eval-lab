package evalrun

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"ragbench-my/backend/internal/evaldata"
	"ragbench-my/backend/internal/evaluation"
	"ragbench-my/backend/internal/providers"
	"ragbench-my/backend/internal/rag"
)

// Pipeline is the shared query pipeline with evaluation attribution.
// *rag.Pipeline satisfies it; the HTTP layer wires the same *rag.Pipeline
// that serves chat so "same query path as chat" is structural, not hopeful.
type Pipeline interface {
	AskEvaluation(ctx context.Context, req rag.ChatRequest) (rag.ChatResponse, error)
}

// Judger runs the versioned rubric judge. Any failure means the case is
// evaluator_failed, never a low-quality score.
type Judger interface {
	RunJudge(ctx context.Context, rubricVersion string, in evaluation.JudgeInput) (evaluation.JudgeScores, error)
}

// Executor executes one golden case through the shared pipeline, scores it
// with the run's pinned evaluator policy, and persists one result row —
// UNIQUE (run_id, eval_case_id) plus ON CONFLICT DO NOTHING makes Airflow
// retries idempotent: a retried case whose row already exists returns the
// stored result and runs nothing again.
type Executor struct {
	Store    *Store
	Datasets *evaldata.Store
	Pipeline Pipeline
	Judger   Judger
}

// ExecuteCase runs and scores exactly one case. Unknown run or case id
// returns ErrNotFound.
func (e *Executor) ExecuteCase(ctx context.Context, runID, caseID string) (Result, error) {
	run, err := e.Store.GetRun(ctx, runID)
	if err != nil {
		return Result{}, err
	}
	vc, err := e.Datasets.GetVersion(ctx, run.DatasetID, run.DatasetVersion)
	if err != nil {
		return Result{}, err
	}
	var golden evaldata.Case
	found := false
	for _, c := range vc.Cases {
		if c.ID == caseID {
			golden, found = c, true
			break
		}
	}
	if !found {
		return Result{}, ErrNotFound
	}
	if stored, _ := e.storedResult(ctx, run.ID, caseID); stored.ID != "" {
		return stored, nil // idempotent: completed work is never duplicated
	}
	outcome := e.runPipeline(ctx, run, golden)
	result, perr := e.persist(ctx, run, golden, outcome, len(vc.Cases))
	if perr != nil {
		return Result{}, perr
	}
	return result, nil
}

// storedResult loads the persisted result for this case, if any.
func (e *Executor) storedResult(ctx context.Context, runID, caseID string) (Result, error) {
	rows, err := e.Store.GetResults(ctx, runID)
	if err != nil {
		return Result{}, err
	}
	for _, r := range rows {
		if r.EvalCaseID == caseID {
			return r, nil
		}
	}
	return Result{}, nil
}

// pipelineOutcome carries the successful response or the classified failure;
// pipeline failures persist a trace, so failed cases keep their trace link
// unless the failure happened before any trace existed (validation).
type pipelineOutcome struct {
	resp     rag.ChatResponse
	serr     *rag.Error // nil on success
	hasTrace bool
}

func (e *Executor) runPipeline(ctx context.Context, run Run, golden evaldata.Case) pipelineOutcome {
	resp, err := e.Pipeline.AskEvaluation(ctx, rag.ChatRequest{Question: golden.Question, ConfigID: run.RagConfigID})
	outcome := pipelineOutcome{resp: resp, hasTrace: err == nil}
	if err == nil {
		return outcome
	}
	var rerr *rag.Error
	if errors.As(err, &rerr) {
		outcome.serr = rerr
		outcome.hasTrace = rerr.TraceID != ""
		return outcome
	}
	outcome.serr = &rag.Error{Code: rag.ErrCodeValidationFailed, Message: err.Error()}
	return outcome
}

// persist writes the single result row for this case with separate
// retrieval, judge, citation, timing, token, and cost metrics.
func (e *Executor) persist(ctx context.Context, run Run, golden evaldata.Case, outcome pipelineOutcome, totalCases int) (Result, error) {
	scores := e.score(ctx, run, golden, outcome)
	judge := judgeCallCost(run, scores)

	var traceID any
	if outcome.hasTrace {
		traceID = outcomeTraceID(outcome)
	}
	var queryCode, queryMsg any
	if outcome.serr != nil {
		queryCode, queryMsg = outcome.serr.Code, outcome.serr.Message
	}

	status := statusFor(outcome, scores)

	var id string
	err := e.Store.pool.QueryRow(ctx, `
		INSERT INTO eval_results (
			run_id, eval_case_id, case_key, status, dataset_version,
			trace_id, query_error_code, query_error_message,
			recall_k, mrr,
			answer_relevance, answer_relevance_rationale,
			groundedness, groundedness_rationale,
			citation_correct,
			evaluator_error, evaluator_input_tokens, evaluator_output_tokens,
			evaluator_cost, evaluator_cost_currency, evaluator_pricing_version,
			total_latency_ms, retrieval_latency_ms, generation_latency_ms,
			input_tokens, output_tokens, embedding_input_tokens,
			estimated_cost, cost_currency
		) VALUES (
			$1,$2,$3,$4,$5,
			$6,$7,$8,
			$9,$10,
			$11,$12,
			$13,$14,
			$15,
			$16,$17,$18,
			$19,$20,$21,
			$22,$23,$24,
			$25,$26,$27,
			$28,$29
		)
		ON CONFLICT (run_id, eval_case_id) DO NOTHING
		RETURNING id`,
		run.ID, golden.ID, golden.CaseKey, status, run.DatasetVersion,
		traceID, queryCode, queryMsg,
		scores.Recall, scores.MRR,
		scores.AnswerRelevance, scores.RelevanceRationale,
		scores.Groundedness, scores.GroundedRationale,
		scores.CitationCorrect,
		scores.EvaluatorError, scores.EvaluatorInputTokens, scores.EvaluatorOutputTokens,
		judge.Cost, judge.Currency, judge.PricingVersion,
		outcome.resp.Trace.LatencyMS, scores.RetrievalMS, scores.GenerationMS,
		outcome.resp.Trace.InputTokens, outcome.resp.Trace.OutputTokens, outcome.resp.Trace.EmbeddingInputTokens,
		outcome.resp.Trace.EstimatedCost, outcome.resp.Trace.CostCurrency,
	).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		// A concurrent attempt stored the row first; return its stored state.
		return e.storedResult(ctx, run.ID, golden.ID)
	}
	if err != nil {
		return Result{}, err
	}
	// Aggregate status is refreshed after every case execution reaches a
	// terminal state, so run status is always accurate on read.
	if _, ferr := e.Store.finalizeStatus(ctx, run.ID, totalCases); ferr != nil {
		return Result{}, ferr
	}
	return e.storedResult(ctx, run.ID, golden.ID)
}

func outcomeTraceID(o pipelineOutcome) string {
	if o.serr != nil && o.serr.TraceID != "" {
		return o.serr.TraceID
	}
	return o.resp.Trace.TraceID
}

func statusFor(outcome pipelineOutcome, scores scoreInputs) string {
	switch {
	case outcome.serr != nil:
		// Pipeline failures — including classified failures that persisted a
		// trace — stay failed cases; the trace stays linked for diagnosis.
		return "failed"
	case scores.EvaluatorFailed:
		return "evaluator_failed"
	default:
		return "completed"
	}
}

// scoreInputs carries one case's scoring outputs; nil interface values mean
// "not evaluable" and are stored as SQL NULL, never as zero.
type scoreInputs struct {
	Recall, MRR, CitationCorrect                any
	AnswerRelevance, Groundedness               any
	RelevanceRationale, GroundedRationale       string
	EvaluatorError                              string
	EvaluatorFailed                             bool
	EvaluatorInputTokens, EvaluatorOutputTokens *int
	RetrievalMS, GenerationMS                   *int64
}

// judgeCallCost prices the judge usage from the explicit rate table under
// the rubric's pinned judge model: evaluator cost stays separate from query
// cost, and unreported/priced-out usage leaves NULL with its reason.
func judgeCallCost(run Run, s scoreInputs) rag.JudgeCostResult {
	var policy evaluation.EvaluatorPolicyVersioned
	if err := json.Unmarshal(run.EvaluatorPolicy, &policy); err != nil {
		return rag.EvalJudgeCost("", "", s.EvaluatorInputTokens, s.EvaluatorOutputTokens)
	}
	rubric, err := providers.RubricByVersion(policy.RubricVersion)
	if err != nil {
		return rag.EvalJudgeCost("", "", s.EvaluatorInputTokens, s.EvaluatorOutputTokens)
	}
	return rag.EvalJudgeCost(rubric.JudgeProvider, rubric.JudgeModel,
		s.EvaluatorInputTokens, s.EvaluatorOutputTokens)
}

// score applies the run's pinned evaluator policy.
func (e *Executor) score(ctx context.Context, run Run, golden evaldata.Case, outcome pipelineOutcome) scoreInputs {
	var s scoreInputs
	var policy evaluation.EvaluatorPolicyVersioned
	_ = json.Unmarshal(run.EvaluatorPolicy, &policy)
	if outcome.serr != nil {
		return s // nothing to score: the query did not succeed
	}

	retrieved := make([]evaluation.RetrievedEvidence, 0, len(outcome.resp.Retrieved))
	retrievedDocs := map[string]bool{}
	for _, r := range outcome.resp.Retrieved {
		retrieved = append(retrieved, evaluation.RetrievedEvidence{DocumentID: r.DocumentID, Rank: r.Rank})
		retrievedDocs[r.DocumentID] = true
	}
	if v, ok := evaluation.RecallAtK(golden.ExpectedEvidence, retrieved, policy.ScoringK); ok {
		s.Recall = v
	}
	if v, ok := evaluation.MRR(golden.ExpectedEvidence, retrieved); ok {
		s.MRR = v
	}

	if len(outcome.resp.Citations) > 0 {
		cited := make([]evaluation.ScoredCitation, 0, len(outcome.resp.Citations))
		for _, c := range outcome.resp.Citations {
			cited = append(cited, evaluation.ScoredCitation{DocumentID: c.DocumentID})
		}
		if v, ok := evaluation.ScoreCitations(cited, retrievedDocs); ok {
			s.CitationCorrect = v
		}
	}

	scores, err := e.Judger.RunJudge(ctx, policy.RubricVersion, evaluation.JudgeInput{
		Question:        golden.Question,
		ReferenceAnswer: golden.ReferenceAnswer,
		Answer:          outcome.resp.Answer,
		CitedEvidence:   evidenceText(outcome.resp.Retrieved),
	})
	if err != nil {
		s.EvaluatorFailed = true
		s.EvaluatorError = err.Error()
		return s
	}
	if scores.AnswerRelevance != nil {
		s.AnswerRelevance = scores.AnswerRelevance.Score
		s.RelevanceRationale = scores.AnswerRelevance.Rationale
	}
	if scores.Groundedness != nil {
		s.Groundedness = scores.Groundedness.Score
		s.GroundedRationale = scores.Groundedness.Rationale
	}
	if scores.InputTokens != nil {
		s.EvaluatorInputTokens = scores.InputTokens
	}
	if scores.OutputTokens != nil {
		s.EvaluatorOutputTokens = scores.OutputTokens
	}
	return s
}

// evidenceText renders the ranked retrieved evidence for the judge prompt.
func evidenceText(retrieved []rag.RetrievedIdentity) string {
	out := ""
	for _, r := range retrieved {
		out += fmt.Sprintf("[%d] document %s chunk %s\n", r.Rank, r.DocumentID, r.ChunkID)
	}
	return out
}
