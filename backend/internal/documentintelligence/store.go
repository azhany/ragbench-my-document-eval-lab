package documentintelligence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"ragbench-my/backend/internal/providers"
)

var (
	ErrNotFound          = errors.New("document analysis not found")
	ErrNoRevision        = errors.New("document has no latest source revision")
	ErrStageOrder        = errors.New("document analysis stage is out of order")
	ErrStageNotRetryable = errors.New("document analysis stage is not retryable")
	ErrAnalysisActive    = errors.New("document analysis already has active processing")
	ErrJobMismatch       = errors.New("Airflow run does not own this document analysis")
	ErrDeleted           = errors.New("document is deleted")
	ErrInvalidModel      = errors.New("invalid analysis model profile")
)

type Job struct {
	ID               string          `json:"id"`
	AnalysisID       string          `json:"analysis_id"`
	DAGID            string          `json:"dag_id"`
	RunID            string          `json:"run_id"`
	State            string          `json:"state"`
	Stage            string          `json:"stage"`
	ErrorCode        *string         `json:"error_code"`
	ErrorMessage     *string         `json:"error_message"`
	DispatchAttempts int             `json:"dispatch_attempts"`
	DispatchedAt     *string         `json:"dispatched_at"`
	StartedAt        *string         `json:"started_at"`
	FinishedAt       *string         `json:"finished_at"`
	StageMetrics     json.RawMessage `json:"stage_metrics"`
}

type Analysis struct {
	ID                      string          `json:"id"`
	DocumentID              string          `json:"document_id"`
	SourceRevisionID        string          `json:"source_revision_id"`
	SourceChecksum          string          `json:"source_checksum"`
	ConfigID                string          `json:"config_id"`
	TraceID                 string          `json:"trace_id"`
	Status                  string          `json:"status"`
	Stage                   string          `json:"stage"`
	ExtractionMethod        *string         `json:"extraction_method"`
	ExtractionProfile       *string         `json:"extraction_profile"`
	SchemaVersion           string          `json:"schema_version"`
	PromptVersion           string          `json:"prompt_version"`
	SummaryPromptVersion    string          `json:"summary_prompt_version"`
	ModelProfile            string          `json:"model_profile"`
	ValidationPolicyVersion string          `json:"validation_policy_version"`
	ExtractionEvidence      json.RawMessage `json:"extraction_evidence"`
	StructuredData          json.RawMessage `json:"structured_data"`
	SchemaErrors            json.RawMessage `json:"schema_errors"`
	ValidationStatus        *string         `json:"validation_status"`
	ValidationFindings      json.RawMessage `json:"validation_findings"`
	ValidationPolicy        json.RawMessage `json:"validation_policy"`
	Summary                 *string         `json:"summary"`
	PromptSnapshot          json.RawMessage `json:"prompt_snapshot"`
	StageMetrics            json.RawMessage `json:"stage_metrics"`
	ToolEvents              json.RawMessage `json:"tool_events"`
	InputTokens             *int            `json:"input_tokens"`
	OutputTokens            *int            `json:"output_tokens"`
	EstimatedCost           *float64        `json:"estimated_cost"`
	CostCurrency            *string         `json:"cost_currency"`
	PricingVersion          *string         `json:"pricing_version"`
	CostComponents          json.RawMessage `json:"cost_components"`
	RetryCount              int             `json:"retry_count"`
	LastRetryReason         *string         `json:"last_retry_reason"`
	ErrorCode               *string         `json:"error_code"`
	ErrorMessage            *string         `json:"error_message"`
	CreatedAt               string          `json:"created_at"`
	UpdatedAt               string          `json:"updated_at"`
	StartedAt               *string         `json:"started_at"`
	FinishedAt              *string         `json:"finished_at"`
	Job                     *Job            `json:"job"`
	Spans                   []Span          `json:"spans"`
}

type Span struct {
	SpanName   string          `json:"span_name"`
	StartedAt  string          `json:"started_at"`
	DurationMS int64           `json:"duration_ms"`
	Status     string          `json:"status"`
	Metadata   json.RawMessage `json:"metadata"`
}

type CreateRequest struct {
	DocumentID   string
	ModelProfile string
}

type StageRequest struct {
	AnalysisID        string          `json:"analysis_id"`
	JobID             string          `json:"job_id"`
	DAGID             string          `json:"dag_id"`
	RunID             string          `json:"run_id"`
	Stage             string          `json:"stage"`
	SourceChecksum    string          `json:"source_checksum,omitempty"`
	ExtractionMethod  string          `json:"extraction_method,omitempty"`
	ExtractionProfile string          `json:"extraction_profile,omitempty"`
	Evidence          json.RawMessage `json:"evidence,omitempty"`
}

// StageUpdate contains the durable output of one named tool. The store writes
// it atomically with the stage event/span and state transition.
type StageUpdate struct {
	DurationMS         int64
	Metadata           map[string]any
	ToolEvent          ToolEvent
	ExtractionMethod   string
	ExtractionProfile  string
	Evidence           json.RawMessage
	RawModelOutput     *string
	StructuredData     json.RawMessage
	SchemaErrors       json.RawMessage
	ValidationStatus   string
	ValidationFindings json.RawMessage
	ValidationPolicy   json.RawMessage
	Summary            *string
	PromptSnapshot     json.RawMessage
	InputTokens        *int
	OutputTokens       *int
	EstimatedCost      *float64
	CostCurrency       string
	PricingVersion     string
	CostComponents     json.RawMessage
}

// stageInput is private database state read after the stage claim. Provider
// calls happen after the transaction releases its row lock.
type stageInput struct {
	ID                      string
	DocumentID              string
	SourceRevisionID        string
	SourceChecksum          string
	TraceID                 string
	Status                  string
	Stage                   string
	ExtractionMethod        *string
	ExtractionProfile       *string
	SchemaVersion           string
	PromptVersion           string
	SummaryPromptVersion    string
	ModelProfile            string
	ValidationPolicyVersion string
	ExtractionEvidence      json.RawMessage
	RawModelOutput          *string
	StructuredData          json.RawMessage
	SchemaErrors            json.RawMessage
	ValidationStatus        *string
	ValidationFindings      json.RawMessage
	ValidationPolicy        json.RawMessage
	Summary                 *string
	PromptSnapshot          json.RawMessage
	StageMetrics            json.RawMessage
	ToolEvents              json.RawMessage
	InputTokens             *int
	OutputTokens            *int
	EstimatedCost           *float64
	CostCurrency            *string
	PricingVersion          *string
	CostComponents          json.RawMessage
	RetryCount              int
	LastRetryReason         *string
	ErrorCode               *string
	ErrorMessage            *string
	JobID                   string
	DAGID                   string
	RunID                   string
	JobState                string
	DispatchAttempts        int
	DispatchedAt            *string
	StartedAt               *string
	FinishedAt              *string
	MimeType                string
	StoragePath             string
	Deleted                 bool
}

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func methodForMIME(mimeType string) (string, string) {
	switch mimeType {
	case "application/pdf":
		return "pdf_text", "pypdf-text-v1"
	case "image/jpeg":
		return "image_ocr", "tesseract-ocr-v1"
	case "image/png":
		return "image_ocr", "tesseract-ocr-v1"
	default:
		return "document_text", "document-parser-v1"
	}
}

func (s *Store) Create(ctx context.Context, req CreateRequest) (Analysis, error) {
	if _, err := uuid.Parse(req.DocumentID); err != nil {
		return Analysis{}, ErrNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Analysis{}, err
	}
	defer tx.Rollback(context.Background())

	var documentID, revisionID, checksum, configID, modelProfile, mimeType string
	err = tx.QueryRow(ctx, `
		SELECT d.id, r.id, r.source_checksum, r.config_id, c.model_profile, d.mime_type
		FROM documents d
		JOIN index_revisions r ON r.id=d.latest_revision_id AND r.document_id=d.id
		JOIN rag_configs c ON c.id=r.config_id
		WHERE d.id=$1 AND d.deleted_at IS NULL
		FOR UPDATE OF d`, req.DocumentID).Scan(&documentID, &revisionID, &checksum, &configID, &modelProfile, &mimeType)
	if errors.Is(err, pgx.ErrNoRows) {
		return Analysis{}, ErrNoRevision
	}
	if err != nil {
		return Analysis{}, err
	}
	if req.ModelProfile != "" {
		modelProfile = strings.TrimSpace(req.ModelProfile)
	}
	if _, err := providers.GenerationProfileByName(modelProfile); err != nil {
		return Analysis{}, fmt.Errorf("%w: %v", ErrInvalidModel, err)
	}
	extractionMethod, extractionProfile := methodForMIME(mimeType)
	analysisID := uuid.NewString()
	traceID := uuid.NewString()
	jobID := uuid.NewString()
	runID := "di_" + analysisID
	_, err = tx.Exec(ctx, `
		INSERT INTO document_analyses (
			id, document_id, source_revision_id, source_checksum, config_id,
			trace_id, job_id, dag_id, run_id, status, job_state, stage,
			extraction_method, extraction_profile, schema_version, prompt_version,
			summary_prompt_version, model_profile, validation_policy_version,
			created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,'document_intelligence',$8,'queued',
			'dispatch_pending','dispatch',$9,$10,$11,$12,$13,$14,$15,now(),now())`,
		analysisID, documentID, revisionID, checksum, configID, traceID, jobID, runID,
		extractionMethod, extractionProfile, SchemaVersionV1, ExtractionPromptV1,
		SummaryPromptV1, modelProfile, ValidationPolicyVersionV1)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return Analysis{}, ErrAnalysisActive
		}
		return Analysis{}, err
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO document_analysis_spans (analysis_id, trace_id, span_name, started_at, duration_ms, status, metadata)
		VALUES ($1,$2,$3,now(),0,'accepted','{}'::jsonb)`, analysisID, traceID, StageRequestSpan); err != nil {
		return Analysis{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Analysis{}, err
	}
	return s.Get(ctx, analysisID)
}

const analysisSelect = `
SELECT jsonb_build_object(
 'id',a.id,'document_id',a.document_id,'source_revision_id',a.source_revision_id,
 'source_checksum',a.source_checksum,'config_id',a.config_id,'trace_id',a.trace_id,
 'status',a.status,'stage',a.stage,'extraction_method',a.extraction_method,
 'extraction_profile',a.extraction_profile,'schema_version',a.schema_version,
 'prompt_version',a.prompt_version,'summary_prompt_version',a.summary_prompt_version,
 'model_profile',a.model_profile,'validation_policy_version',a.validation_policy_version,
 'extraction_evidence',a.extraction_evidence,'structured_data',a.structured_data,
 'schema_errors',a.schema_errors,'validation_status',a.validation_status,
 'validation_findings',a.validation_findings,'validation_policy',a.validation_policy,
 'summary',a.summary,'prompt_snapshot',a.prompt_snapshot,
 'stage_metrics',a.stage_metrics,'tool_events',a.tool_events,
 'input_tokens',a.input_tokens,'output_tokens',a.output_tokens,
 'estimated_cost',a.estimated_cost,'cost_currency',a.cost_currency,
 'pricing_version',a.pricing_version,'cost_components',a.cost_components,
 'retry_count',a.retry_count,'last_retry_reason',a.last_retry_reason,
 'error_code',a.error_code,'error_message',a.error_message,
 'created_at',a.created_at,'updated_at',a.updated_at,
 'started_at',a.started_at,'finished_at',a.finished_at,
 'job',jsonb_build_object('id',a.job_id,'analysis_id',a.id,'dag_id',a.dag_id,
    'run_id',a.run_id,'state',a.job_state,'stage',a.stage,
    'error_code',a.error_code,'error_message',a.error_message,
    'dispatch_attempts',a.dispatch_attempts,'dispatched_at',a.dispatched_at,
    'started_at',a.started_at,'finished_at',a.finished_at,'stage_metrics',a.stage_metrics),
 'spans',(SELECT COALESCE(jsonb_agg(jsonb_build_object(
    'span_name',sp.span_name,'started_at',sp.started_at,'duration_ms',sp.duration_ms,
    'status',sp.status,'metadata',sp.metadata) ORDER BY sp.started_at,sp.id),'[]'::jsonb)
    FROM document_analysis_spans sp WHERE sp.analysis_id=a.id)
) FROM document_analyses a`

func (s *Store) Get(ctx context.Context, id string) (Analysis, error) {
	if _, err := uuid.Parse(id); err != nil {
		return Analysis{}, ErrNotFound
	}
	var raw []byte
	err := s.pool.QueryRow(ctx, analysisSelect+` WHERE a.id=$1`, id).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return Analysis{}, ErrNotFound
	}
	if err != nil {
		return Analysis{}, err
	}
	var analysis Analysis
	if err := json.Unmarshal(raw, &analysis); err != nil {
		return Analysis{}, err
	}
	return analysis, nil
}

func (s *Store) List(ctx context.Context, documentID string) ([]Analysis, error) {
	if _, err := uuid.Parse(documentID); err != nil {
		return nil, ErrNotFound
	}
	rows, err := s.pool.Query(ctx, analysisSelect+`
		JOIN documents d ON d.id=a.document_id
		WHERE a.document_id=$1 AND d.deleted_at IS NULL
		ORDER BY a.created_at DESC,a.id`, documentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	analyses := []Analysis{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var analysis Analysis
		if err := json.Unmarshal(raw, &analysis); err != nil {
			return nil, err
		}
		analyses = append(analyses, analysis)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(analyses) == 0 {
		var exists bool
		if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM documents WHERE id=$1 AND deleted_at IS NULL)`, documentID).Scan(&exists); err != nil {
			return nil, err
		}
		if !exists {
			return nil, ErrNotFound
		}
	}
	return analyses, nil
}

// RecordDispatch makes Airflow dispatch durable and idempotent. A late
// successful response cannot resurrect a cancelled or already-running job.
func (s *Store) RecordDispatch(ctx context.Context, jobID string, dispatchErr error) error {
	if _, err := uuid.Parse(jobID); err != nil {
		return ErrNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	var state string
	if err = tx.QueryRow(ctx, `SELECT job_state FROM document_analyses WHERE job_id=$1 FOR UPDATE`, jobID).Scan(&state); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if state == "cancelled" || state == "succeeded" || state == "processing" || state == "queued" {
		return tx.Commit(ctx)
	}
	if dispatchErr == nil {
		_, err = tx.Exec(ctx, `UPDATE document_analyses SET job_state='queued',status='queued',error_code=NULL,error_message=NULL,
			dispatch_attempts=dispatch_attempts+1,dispatched_at=now(),updated_at=now()
			WHERE job_id=$1 AND job_state IN ('dispatch_pending','dispatch_failed')`, jobID)
	} else {
		_, err = tx.Exec(ctx, `UPDATE document_analyses SET job_state='dispatch_failed',status='failed',stage='dispatch',
			error_code='dispatch_failed',error_message=$2,dispatch_attempts=dispatch_attempts+1,updated_at=now()
			WHERE job_id=$1 AND job_state IN ('dispatch_pending','dispatch_failed')`, jobID, safeErrorMessage(dispatchErr))
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) claimStage(ctx context.Context, request StageRequest) (stageInput, bool, error) {
	if _, err := uuid.Parse(request.AnalysisID); err != nil {
		return stageInput{}, false, ErrNotFound
	}
	if !knownStage(request.Stage) {
		return stageInput{}, false, ErrStageOrder
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return stageInput{}, false, err
	}
	defer tx.Rollback(context.Background())
	var input stageInput
	var configID string
	err = tx.QueryRow(ctx, `SELECT a.id,a.document_id,a.source_revision_id,a.source_checksum,a.config_id,a.trace_id,
		a.status,a.stage,a.extraction_method,a.extraction_profile,a.schema_version,a.prompt_version,a.summary_prompt_version,
		a.model_profile,a.validation_policy_version,a.extraction_evidence,a.raw_model_output,a.structured_data,a.schema_errors,
		a.validation_status,a.validation_findings,a.validation_policy,a.summary,a.prompt_snapshot,a.stage_metrics,a.tool_events,
		a.input_tokens,a.output_tokens,a.estimated_cost,a.cost_currency,a.pricing_version,a.cost_components,a.retry_count,
		a.last_retry_reason,a.error_code,a.error_message,
		a.job_id,a.dag_id,a.run_id,a.job_state,a.dispatch_attempts,
		d.mime_type,d.storage_path,d.deleted_at IS NOT NULL
		FROM document_analyses a JOIN documents d ON d.id=a.document_id
		JOIN index_revisions r ON r.id=a.source_revision_id AND r.document_id=a.document_id
		WHERE a.id=$1 FOR UPDATE`, request.AnalysisID).Scan(
		&input.ID, &input.DocumentID, &input.SourceRevisionID, &input.SourceChecksum, &configID, &input.TraceID,
		&input.Status, &input.Stage, &input.ExtractionMethod, &input.ExtractionProfile, &input.SchemaVersion, &input.PromptVersion,
		&input.SummaryPromptVersion, &input.ModelProfile, &input.ValidationPolicyVersion, &input.ExtractionEvidence, &input.RawModelOutput, &input.StructuredData, &input.SchemaErrors,
		&input.ValidationStatus, &input.ValidationFindings, &input.ValidationPolicy, &input.Summary, &input.PromptSnapshot, &input.StageMetrics, &input.ToolEvents, &input.InputTokens, &input.OutputTokens,
		&input.EstimatedCost, &input.CostCurrency, &input.PricingVersion, &input.CostComponents, &input.RetryCount, &input.LastRetryReason,
		&input.ErrorCode, &input.ErrorMessage,
		&input.JobID, &input.DAGID, &input.RunID, &input.JobState, &input.DispatchAttempts,
		&input.MimeType, &input.StoragePath, &input.Deleted)
	if errors.Is(err, pgx.ErrNoRows) {
		return stageInput{}, false, ErrNotFound
	}
	if err != nil {
		return stageInput{}, false, err
	}
	_ = configID // the immutable config identity is returned by Get and not needed by a stage tool
	if input.JobID != request.JobID || input.DAGID != request.DAGID || input.RunID != request.RunID {
		return stageInput{}, false, ErrJobMismatch
	}
	if input.Deleted {
		return stageInput{}, false, ErrDeleted
	}
	if input.JobState == "cancelled" {
		return input, true, tx.Commit(ctx)
	}
	if input.StageMetrics == nil {
		input.StageMetrics = json.RawMessage(`{}`)
	}
	var stageMetric stageMetricRecord
	if raw := json.RawMessage(input.StageMetrics); len(raw) > 0 {
		var all map[string]stageMetricRecord
		if json.Unmarshal(raw, &all) == nil {
			stageMetric = all[request.Stage]
		}
	}
	if stageMetric.Status == "succeeded" {
		return input, true, tx.Commit(ctx)
	}
	current := stageNumber(input.Stage)
	requested := stageNumber(request.Stage)
	if requested != current+1 && !(requested == current && stageMetric.Status == "failed") {
		if requested <= current {
			return stageInput{}, false, ErrStageNotRetryable
		}
		return stageInput{}, false, ErrStageOrder
	}
	if input.JobState == "succeeded" {
		return stageInput{}, false, ErrStageNotRetryable
	}
	if _, err := tx.Exec(ctx, `UPDATE document_analyses SET status='processing',job_state='processing',stage=$2,
		error_code=NULL,error_message=NULL,started_at=COALESCE(started_at,now()),finished_at=NULL,updated_at=now()
		WHERE id=$1`, request.AnalysisID, request.Stage); err != nil {
		return stageInput{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return stageInput{}, false, err
	}
	return input, false, nil
}

type stageMetricRecord struct {
	Status string `json:"status"`
}

func (s *Store) CompleteStage(ctx context.Context, analysisID, stage string, update StageUpdate) error {
	if _, err := uuid.Parse(analysisID); err != nil {
		return ErrNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	var traceID, currentStage, jobState string
	if err := tx.QueryRow(ctx, `SELECT trace_id,stage,job_state FROM document_analyses WHERE id=$1 FOR UPDATE`, analysisID).
		Scan(&traceID, &currentStage, &jobState); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if jobState == "cancelled" {
		return tx.Commit(ctx)
	}
	if currentStage != stage {
		return ErrStageOrder
	}
	if update.ToolEvent.Name == "" {
		update.ToolEvent.Name = stage
	}
	if update.ToolEvent.Status == "" {
		update.ToolEvent.Status = "succeeded"
	}
	if update.ToolEvent.StartedAt == "" {
		update.ToolEvent.StartedAt = "now"
	}
	metrics := update.Metadata
	if metrics == nil {
		metrics = map[string]any{}
	}
	metrics["duration_ms"] = update.DurationMS
	metrics["status"] = "succeeded"
	metricJSON, err := json.Marshal(map[string]any{stage: metrics})
	if err != nil {
		return err
	}
	eventJSON, err := json.Marshal([]ToolEvent{update.ToolEvent})
	if err != nil {
		return err
	}
	promptJSON := update.PromptSnapshot
	if len(promptJSON) == 0 {
		promptJSON = json.RawMessage(`{}`)
	}
	structuredJSON := nullableJSON(update.StructuredData)
	schemaErrorsJSON := nullableJSON(update.SchemaErrors)
	findingsJSON := nullableJSON(update.ValidationFindings)
	policyJSON := nullableJSON(update.ValidationPolicy)
	evidenceJSON := nullableJSON(update.Evidence)
	costComponentsJSON := nullableJSON(update.CostComponents)
	final := stage == StageSummarize
	jobStateValue, statusValue := "processing", "processing"
	if final {
		jobStateValue, statusValue = "succeeded", "completed"
	}
	_, err = tx.Exec(ctx, `UPDATE document_analyses SET
		extraction_method=CASE WHEN $2<>'' THEN $2 ELSE extraction_method END,
		extraction_profile=CASE WHEN $3<>'' THEN $3 ELSE extraction_profile END,
		extraction_evidence=CASE WHEN $4::jsonb IS NULL THEN extraction_evidence ELSE $4::jsonb END,
		raw_model_output=COALESCE($5,raw_model_output),
		structured_data=CASE WHEN $6::jsonb IS NULL THEN structured_data ELSE $6::jsonb END,
		schema_errors=CASE WHEN $7::jsonb IS NULL THEN schema_errors ELSE $7::jsonb END,
		validation_status=CASE WHEN $8<>'' THEN $8 ELSE validation_status END,
		validation_findings=CASE WHEN $9::jsonb IS NULL THEN validation_findings ELSE $9::jsonb END,
		validation_policy=CASE WHEN $10::jsonb IS NULL THEN validation_policy ELSE $10::jsonb END,
		summary=COALESCE($11,summary),
		prompt_snapshot=prompt_snapshot || $12::jsonb,
		stage_metrics=stage_metrics || $13::jsonb,
		tool_events=tool_events || $14::jsonb,
		input_tokens=COALESCE($15,input_tokens),output_tokens=COALESCE($16,output_tokens),
		estimated_cost=COALESCE($17,estimated_cost),cost_currency=CASE WHEN $18<>'' THEN $18 ELSE cost_currency END,
		pricing_version=CASE WHEN $19<>'' THEN $19 ELSE pricing_version END,
		cost_components=CASE WHEN $20::jsonb IS NULL THEN cost_components ELSE cost_components || $20::jsonb END,
		job_state=$21,status=$22,error_code=NULL,error_message=NULL,
		finished_at=CASE WHEN $23 THEN now() ELSE finished_at END,updated_at=now()
		WHERE id=$1`, analysisID, update.ExtractionMethod, update.ExtractionProfile, evidenceJSON,
		update.RawModelOutput, structuredJSON, schemaErrorsJSON, update.ValidationStatus, findingsJSON, policyJSON,
		update.Summary, promptJSON, metricJSON, eventJSON, update.InputTokens, update.OutputTokens,
		update.EstimatedCost, update.CostCurrency, update.PricingVersion, costComponentsJSON,
		jobStateValue, statusValue, final)
	if err != nil {
		return err
	}
	metadata := update.Metadata
	if metadata == nil {
		metadata = map[string]any{}
	}
	metadata["stage"] = stage
	if _, err := tx.Exec(ctx, `INSERT INTO document_analysis_spans
		(analysis_id,trace_id,span_name,started_at,duration_ms,status,metadata)
		VALUES($1,$2,$3,COALESCE(NULLIF($4,'now')::timestamptz,now()),$5,'succeeded',$6)`,
		analysisID, traceID, stage, update.ToolEvent.StartedAt, update.DurationMS, mustJSON(metadata)); err != nil {
		return err
	}
	if final {
		if _, err := tx.Exec(ctx, `UPDATE document_analysis_spans sp SET duration_ms=GREATEST(0,EXTRACT(EPOCH FROM (now()-a.created_at))*1000)::integer,
			status='completed',metadata=metadata || '{"status":"completed"}'::jsonb
			FROM document_analyses a WHERE sp.analysis_id=a.id AND sp.analysis_id=$1 AND sp.span_name='request'`, analysisID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) FailStage(ctx context.Context, analysisID, stage, code, message string, update StageUpdate) error {
	if _, err := uuid.Parse(analysisID); err != nil {
		return ErrNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	var traceID, currentStage, jobState string
	var stageMetrics json.RawMessage
	var hasStructured bool
	if err := tx.QueryRow(ctx, `SELECT trace_id,stage,job_state,structured_data IS NOT NULL,stage_metrics FROM document_analyses WHERE id=$1 FOR UPDATE`, analysisID).
		Scan(&traceID, &currentStage, &jobState, &hasStructured, &stageMetrics); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if jobState == "cancelled" {
		return tx.Commit(ctx)
	}
	if currentStage != stage {
		return ErrStageOrder
	}
	// A provider/task process can receive an ambiguous error after the
	// completion transaction committed. Never turn that durable success into
	// a failure while handling the late error or Airflow callback.
	var stageRecords map[string]stageMetricRecord
	if json.Unmarshal(stageMetrics, &stageRecords) == nil && stageRecords[stage].Status == "succeeded" {
		return tx.Commit(ctx)
	}
	if code == "" {
		code = "stage_failed"
	}
	message = safeErrorMessage(errors.New(message))
	if update.ToolEvent.Name == "" {
		update.ToolEvent.Name = stage
	}
	update.ToolEvent.Status = "failed"
	update.ToolEvent.ErrorCode = code
	if update.ToolEvent.StartedAt == "" {
		update.ToolEvent.StartedAt = "now"
	}
	metrics := update.Metadata
	if metrics == nil {
		metrics = map[string]any{}
	}
	metrics["duration_ms"] = update.DurationMS
	metrics["status"] = "failed"
	metrics["error_code"] = code
	metricJSON, err := json.Marshal(map[string]any{stage: metrics})
	if err != nil {
		return err
	}
	eventJSON, err := json.Marshal([]ToolEvent{update.ToolEvent})
	if err != nil {
		return err
	}
	status := "failed"
	if stage == StageSummarize && hasStructured {
		status = "partial"
	}
	evidenceJSON := nullableJSON(update.Evidence)
	structuredJSON := nullableJSON(update.StructuredData)
	schemaErrorsJSON := nullableJSON(update.SchemaErrors)
	findingsJSON := nullableJSON(update.ValidationFindings)
	policyJSON := nullableJSON(update.ValidationPolicy)
	costComponentsJSON := nullableJSON(update.CostComponents)
	promptJSON := update.PromptSnapshot
	if len(promptJSON) == 0 {
		promptJSON = json.RawMessage(`{}`)
	}
	_, err = tx.Exec(ctx, `UPDATE document_analyses SET
		extraction_evidence=CASE WHEN $2::jsonb IS NULL THEN extraction_evidence ELSE $2::jsonb END,
		raw_model_output=COALESCE($3,raw_model_output),
		structured_data=CASE WHEN $4::jsonb IS NULL THEN structured_data ELSE $4::jsonb END,
		schema_errors=CASE WHEN $5::jsonb IS NULL THEN schema_errors ELSE $5::jsonb END,
		validation_status=CASE WHEN $6<>'' THEN $6 ELSE validation_status END,
		validation_findings=CASE WHEN $7::jsonb IS NULL THEN validation_findings ELSE $7::jsonb END,
		validation_policy=CASE WHEN $8::jsonb IS NULL THEN validation_policy ELSE $8::jsonb END,
		summary=COALESCE($9,summary),prompt_snapshot=prompt_snapshot || $10::jsonb,
		stage_metrics=stage_metrics || $11::jsonb,tool_events=tool_events || $12::jsonb,
		input_tokens=COALESCE($13,input_tokens),output_tokens=COALESCE($14,output_tokens),
		estimated_cost=COALESCE($15,estimated_cost),cost_currency=CASE WHEN $16<>'' THEN $16 ELSE cost_currency END,
		pricing_version=CASE WHEN $17<>'' THEN $17 ELSE pricing_version END,
		cost_components=CASE WHEN $18::jsonb IS NULL THEN cost_components ELSE cost_components || $18::jsonb END,
		job_state='failed',status=$19,error_code=$20,error_message=$21,
		retry_count=retry_count+1,last_retry_reason=$20,finished_at=now(),updated_at=now()
		WHERE id=$1`, analysisID, evidenceJSON, update.RawModelOutput, structuredJSON, schemaErrorsJSON,
		update.ValidationStatus, findingsJSON, policyJSON, update.Summary, promptJSON, metricJSON, eventJSON,
		update.InputTokens, update.OutputTokens, update.EstimatedCost, update.CostCurrency, update.PricingVersion,
		costComponentsJSON, status, code, message)
	if err != nil {
		return err
	}
	metadata := update.Metadata
	if metadata == nil {
		metadata = map[string]any{}
	}
	metadata["stage"] = stage
	metadata["error_code"] = code
	metadata["error_message"] = message
	if _, err := tx.Exec(ctx, `INSERT INTO document_analysis_spans
		(analysis_id,trace_id,span_name,started_at,duration_ms,status,metadata)
		VALUES($1,$2,$3,COALESCE(NULLIF($4,'now')::timestamptz,now()),$5,'failed',$6)`,
		analysisID, traceID, stage, update.ToolEvent.StartedAt, update.DurationMS, mustJSON(metadata)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func nullableJSON(raw json.RawMessage) any {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return raw
}

func mustJSON(value any) []byte {
	raw, err := json.Marshal(value)
	if err != nil {
		return []byte(`{}`)
	}
	return raw
}

// knownStage and stageNumber are kept in the store package so all state
// transitions enforce the same documented order.
func knownStage(stage string) bool {
	for _, candidate := range OrderedStages {
		if candidate == stage {
			return true
		}
	}
	return false
}

func stageNumber(stage string) int {
	for i, candidate := range OrderedStages {
		if candidate == stage {
			return i
		}
	}
	return -1
}

func safeErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	message := strings.TrimSpace(err.Error())
	if len(message) > 1000 {
		return message[:1000]
	}
	return message
}
