// Package evalrun owns durable evaluation runs (RB-14): run creation that
// pins dataset version, config identity, corpus revisions and evaluator
// policy; per-case execution through the same query pipeline as chat with
// evaluation attribution; idempotent case retries; and aggregate run status
// that distinguishes completed, partial, and failed work.
package evalrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"ragbench-my/backend/internal/evaldata"
	"ragbench-my/backend/internal/evaluation"
	"ragbench-my/backend/internal/ragconfig"
)

// Run statuses (mirrored by the migration CHECK):
//
//	created         — pinned inputs, not yet dispatched/executed
//	running         — dispatched or at least one case executing
//	completed       — every case has a terminal result with zero query failures
//	partial         — executed with some failed or evaluator-failed cases
//	failed          — every attempted case failed
//	dispatch_failed — Airflow never accepted dispatch; a visible failure
const (
	StatusCreated        = "created"
	StatusRunning        = "running"
	StatusCompleted      = "completed"
	StatusPartial        = "partial"
	StatusFailed         = "failed"
	StatusDispatchFailed = "dispatch_failed"
)

var ErrNotFound = errors.New("eval run not found")

// Run is one persisted evaluation run with its pinned inputs.
type Run struct {
	ID               string          `json:"id"`
	DatasetID        string          `json:"dataset_id"`
	DatasetVersion   int             `json:"dataset_version"`
	RagConfigID      string          `json:"rag_config_id"`
	CorpusRevisions  json.RawMessage `json:"corpus_revisions"`
	EvaluatorPolicy  json.RawMessage `json:"evaluator_policy"`
	Status           string          `json:"status"`
	DagRunID         string          `json:"dag_run_id"`
	DispatchAttempts int             `json:"dispatch_attempts"`
	DispatchError    string          `json:"dispatch_error"`
	CreatedAt        time.Time       `json:"created_at"`
	// Aggregate is computed on read from persisted results; never fabricated.
	Aggregate json.RawMessage `json:"aggregate,omitempty"`
}

// CreateRequest is the API payload to launch an evaluation run.
type CreateRequest struct {
	DatasetID      string `json:"dataset_id"`
	DatasetVersion int    `json:"dataset_version"` // 0 = latest
	RagConfigID    string `json:"rag_config_id"`
	RubricVersion  string `json:"rubric_version"`
	ScoringK       int    `json:"scoring_k"`
}

// Store persists evaluation runs and results in PostgreSQL.
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// corpusRevisionSnapshot records, per live document, its active ready
// revision readable under the config's embedding identity. Documents with
// no compatible revision make the aggregate `null`, which execute-time
// retrieval will surface as revision_unavailable — the corpus is pinned
// either way.
const corpusSnapshotQuery = `
	SELECT COALESCE(jsonb_agg(
		jsonb_build_object(
			'document_id', d.id,
			'filename', d.filename,
			'checksum', d.checksum,
			'revision_id', r.id,
			'revision_number', r.revision_number,
			'chunk_size', r.chunk_size,
			'chunk_overlap', r.chunk_overlap
		)
		ORDER BY d.created_at, d.id), 'null'::jsonb)
	FROM documents d
	JOIN index_revisions r ON r.id = d.active_revision_id
	WHERE d.deleted_at IS NULL
	  AND r.status = 'ready'
	  AND r.embedding_provider = $1 AND r.embedding_model = $2 AND r.embedding_dimensions = $3`

// CreateRun pins every input and persists the run in status created.
// Dispatch (DoDispatch) is the caller's job, so the HTTP state is accurate.
type DatasetReader interface {
	GetDataset(ctx context.Context, id string) (evaldata.Dataset, error)
}

type ConfigReader interface {
	Get(ctx context.Context, id string) (ragconfig.Config, error)
}

func (s *Store) CreateRun(ctx context.Context, datasets DatasetReader, configs ConfigReader, req CreateRequest) (Run, error) {
	policy, err := evaluation.ResolveEvaluatorPolicy(req.RubricVersion, req.ScoringK)
	if err != nil {
		return Run{}, err
	}
	ds, err := datasets.GetDataset(ctx, req.DatasetID)
	if err != nil {
		return Run{}, fmt.Errorf("resolve dataset: %w", err)
	}
	version := req.DatasetVersion
	if version == 0 {
		version = ds.LatestVersion
	} else if version < 1 || version > ds.LatestVersion {
		return Run{}, fmt.Errorf("dataset version %d does not exist (1–%d)", version, ds.LatestVersion)
	}
	cfg, err := configs.Get(ctx, req.RagConfigID)
	if err != nil {
		return Run{}, fmt.Errorf("resolve rag config: %w", err)
	}
	if blocker := cfg.ExecutionBlocker(); blocker != nil {
		return Run{}, fmt.Errorf("rag config cannot execute: %v", blocker)
	}

	policyJSON, err := json.Marshal(policy)
	if err != nil {
		return Run{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Run{}, err
	}
	defer tx.Rollback(context.Background())

	var corpus json.RawMessage
	if err := tx.QueryRow(ctx, corpusSnapshotQuery, cfg.EmbeddingProvider, cfg.EmbeddingModel, cfg.EmbeddingDimensions).Scan(&corpus); err != nil {
		return Run{}, fmt.Errorf("snapshot corpus revisions: %w", err)
	}

	var id, status string
	var createdAt time.Time
	err = tx.QueryRow(ctx, `
		INSERT INTO eval_runs (dataset_id, dataset_version, rag_config_id, corpus_revisions, evaluator_policy, status)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, status, created_at`,
		req.DatasetID, version, req.RagConfigID, corpus, policyJSON, StatusCreated,
	).Scan(&id, &status, &createdAt)
	if err != nil {
		return Run{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Run{}, err
	}
	return Run{
		ID: id, DatasetID: req.DatasetID, DatasetVersion: version,
		RagConfigID: req.RagConfigID, CorpusRevisions: corpus,
		EvaluatorPolicy: policyJSON, Status: status, CreatedAt: createdAt,
	}, nil
}

// MarkRunning transitions created → running exactly once (idempotent); only
// created and running runs accept executions.
func (s *Store) MarkRunning(ctx context.Context, runID string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE eval_runs SET status='running', updated_at=now()
		WHERE id=$1 AND status='created'`, runID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() > 0 {
		return nil
	}
	var status string
	if err := s.pool.QueryRow(ctx, `SELECT status FROM eval_runs WHERE id=$1`, runID).Scan(&status); err != nil {
		return err
	}
	if status != StatusRunning {
		return fmt.Errorf("eval run is %s and no longer accepts execution", status)
	}
	return nil
}

// RecordDispatch persists the Airflow dag_run_id; a dispatch failure is
// stored as dispatch_failed with the reason — never successful processing.
func (s *Store) RecordDispatch(ctx context.Context, runID, dagRunID string, dispatchErr error) error {
	status := StatusRunning
	errMsg := ""
	dagRun := dagRunID
	if dispatchErr != nil {
		status = StatusDispatchFailed
		errMsg = dispatchErr.Error()
		dagRun = dagRunID // the durable run ID survives ambiguous timeouts
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE eval_runs SET dag_run_id=$2, status=$3, dispatch_error=nullIf($4,''),
		dispatch_attempts=dispatch_attempts+1, updated_at=now()
		WHERE id=$1 AND dispatch_attempts=0 AND status IN ('created', 'running')`,
		runID, dagRun, status, errMsg)
	return err
}

func scanRun(row pgx.Row) (Run, error) {
	var r Run
	err := row.Scan(&r.ID, &r.DatasetID, &r.DatasetVersion, &r.RagConfigID,
		&r.CorpusRevisions, &r.EvaluatorPolicy, &r.Status, &r.DagRunID,
		&r.DispatchAttempts, &r.DispatchError, &r.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, ErrNotFound
	}
	return r, err
}

const runSelect = `
	SELECT id, dataset_id, dataset_version, rag_config_id,
	       corpus_revisions, evaluator_policy, status,
	       COALESCE(dag_run_id, ''), dispatch_attempts, COALESCE(dispatch_error, ''), created_at
	FROM eval_runs`

// GetRun loads one run.
func (s *Store) GetRun(ctx context.Context, id string) (Run, error) {
	if _, err := uuid.Parse(id); err != nil {
		return Run{}, ErrNotFound
	}
	return scanRun(s.pool.QueryRow(ctx, runSelect+` WHERE id=$1`, id))
}

// ListRuns returns runs newest first.
func (s *Store) ListRuns(ctx context.Context) ([]Run, error) {
	rows, err := s.pool.Query(ctx, runSelect+` ORDER BY created_at DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	runs := []Run{}
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, r)
	}
	return runs, rows.Err()
}

// Result is one attempted case with its scores and operational copy of the
// trace metrics.
type Result struct {
	ID                    string    `json:"id"`
	RunID                 string    `json:"run_id"`
	EvalCaseID            string    `json:"eval_case_id"`
	CaseKey               string    `json:"case_key"`
	Status                string    `json:"status"`
	DatasetVersion        int       `json:"dataset_version"`
	TraceID               string    `json:"trace_id"`
	QueryErrorCode        string    `json:"query_error_code"`
	QueryErrorMessage     string    `json:"query_error_message"`
	RecallK               *float64  `json:"recall_k"`
	MRR                   *float64  `json:"mrr"`
	AnswerRelevance       *int      `json:"answer_relevance"`
	RelevanceRationale    string    `json:"answer_relevance_rationale"`
	Groundedness          *int      `json:"groundedness"`
	GroundednessRationale string    `json:"groundedness_rationale"`
	CitationCorrect       *float64  `json:"citation_correct"`
	EvaluatorError        string    `json:"evaluator_error"`
	EvaluatorCost         *float64  `json:"evaluator_cost"`
	TotalLatencyMS        *int64    `json:"total_latency_ms"`
	RetrievalLatencyMS    *int64    `json:"retrieval_latency_ms"`
	GenerationLatencyMS   *int64    `json:"generation_latency_ms"`
	InputTokens           *int      `json:"input_tokens"`
	OutputTokens          *int      `json:"output_tokens"`
	EmbeddingTokens       *int      `json:"embedding_input_tokens"`
	Cost                  *float64  `json:"estimated_cost"`
	CostCurrency          string    `json:"cost_currency"`
	CreatedAt             time.Time `json:"created_at"`
}

// GetResults returns every persisted result of one run.
func (s *Store) GetResults(ctx context.Context, runID string) ([]Result, error) {
	if _, err := uuid.Parse(runID); err != nil {
		return nil, ErrNotFound
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, eval_case_id, case_key, status, dataset_version,
		       COALESCE(trace_id, ''), COALESCE(query_error_code, ''), COALESCE(query_error_message, ''),
		       recall_k, mrr,
		       answer_relevance, COALESCE(answer_relevance_rationale, ''),
		       groundedness, COALESCE(groundedness_rationale, ''),
		       citation_correct, COALESCE(evaluator_error, ''),
		       evaluator_cost, total_latency_ms, retrieval_latency_ms, generation_latency_ms,
		       input_tokens, output_tokens, embedding_input_tokens,
		       estimated_cost, COALESCE(cost_currency, ''), created_at
		FROM eval_results WHERE run_id=$1 ORDER BY case_key`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Result{}
	for rows.Next() {
		var r Result
		if err := rows.Scan(&r.ID, &r.EvalCaseID, &r.CaseKey, &r.Status, &r.DatasetVersion,
			&r.TraceID, &r.QueryErrorCode, &r.QueryErrorMessage,
			&r.RecallK, &r.MRR,
			&r.AnswerRelevance, &r.RelevanceRationale,
			&r.Groundedness, &r.GroundednessRationale,
			&r.CitationCorrect, &r.EvaluatorError,
			&r.EvaluatorCost, &r.TotalLatencyMS, &r.RetrievalLatencyMS, &r.GenerationLatencyMS,
			&r.InputTokens, &r.OutputTokens, &r.EmbeddingTokens,
			&r.Cost, &r.CostCurrency, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ResultStatuses is one case's terminal state summary for finalize.
type resultStatuses struct {
	TotalCases      int
	Completed       int
	QueryFailed     int
	EvaluatorFailed int
}

// recomputeStatus derives aggregate run status from persisted terminal
// results plus the expected case total. attemptZero = cases never executed.
func (s *Store) statusCounts(ctx context.Context, runID string, expectedTotal int) (Run, resultStatuses, error) {
	run, err := s.GetRun(ctx, runID)
	if err != nil {
		return Run{}, resultStatuses{}, err
	}
	results, err := s.GetResults(ctx, runID)
	if err != nil {
		return Run{}, resultStatuses{}, err
	}
	var st resultStatuses
	st.TotalCases = expectedTotal
	for _, r := range results {
		switch r.Status {
		case "completed":
			st.Completed++
		case "failed":
			st.QueryFailed++
		case "evaluator_failed":
			st.EvaluatorFailed++
		}
	}
	return run, st, nil
}

// FinalizeStatus computes and persists one accurate terminal status:
// completed / partial / failed / dispatch_failed — never a fabricated
// success. Idempotent via CASE update.
func (s *Store) FinalizeStatus(ctx context.Context, runID string, expectedTotal int) (Run, error) {
	return s.finalizeStatus(ctx, runID, expectedTotal)
}

// finalizeStatus computes and persists one accurate terminal status:
// completed / partial / failed / dispatch_failed — never a fabricated
// success. Idempotent via CASE update.
func (s *Store) finalizeStatus(ctx context.Context, runID string, expectedTotal int) (Run, error) {
	run, st, err := s.statusCounts(ctx, runID, expectedTotal)
	if err != nil {
		return Run{}, err
	}
	status := run.Status
	if run.Status == StatusDispatchFailed {
		return run, nil // dispatch failure stays visible
	}
	switch {
	case st.Completed+st.QueryFailed+st.EvaluatorFailed == 0:
		status = StatusFailed // nothing executed at all
	case st.Completed == 0 && st.EvaluatorFailed == 0:
		status = StatusFailed // every query failed
	case st.QueryFailed+st.EvaluatorFailed == 0:
		status = StatusCompleted
	default:
		// Some queries failed or some scoring failed: a partial outcome,
		// visible as such — evaluator failures are not zero quality.
		status = StatusPartial
	}
	_, err = s.pool.Exec(ctx, `
		UPDATE eval_runs SET status=CASE
			WHEN status='dispatch_failed' THEN status
			ELSE $2 END, updated_at=now() WHERE id=$1`, runID, status)
	if err != nil {
		return Run{}, err
	}
	return s.GetRun(ctx, runID)
}

// ErrDispatchFailed wraps a run whose Airflow dispatch failed; experiment
// orchestration surfaces this distinctly rather than silent success.
var ErrDispatchFailed = errors.New("eval run dispatch failed")
