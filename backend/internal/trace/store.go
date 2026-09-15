// Package trace persists RAG query traces and stage spans in PostgreSQL and
// serves the trace list/detail data contract. A trace write is atomic: the
// trace row and every executed span commit together, or the caller sees a
// persistence failure instead of a half-traceable success.
package trace

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"

	"github.com/jackc/pgx/v5/pgxpool"

	"ragbench-my/backend/internal/rag"
)

// List limit bounds. The API documents these; anything else is a client
// error, not a silently clamped window.
const (
	DefaultListLimit = 50
	MaxListLimit     = 200
)

// ErrNotFound reports an unknown or malformed trace id.
var ErrNotFound = errors.New("trace not found")

// Store persists traces in the application PostgreSQL database.
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) Insert(ctx context.Context, r rag.TraceRecord) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	costComponents, err := json.Marshal(r.CostComponents)
	if err != nil {
		return err
	}

	var traceRowID string
	err = tx.QueryRow(ctx, `
		INSERT INTO rag_traces (
			trace_id, request_type, question, rag_config_id, success,
			error_code, error_message, total_latency_ms,
			input_tokens, output_tokens, embedding_input_tokens,
			estimated_cost, cost_currency, pricing_version, cost_components,
			answer, citations, prompt_snapshot, context_snapshot
		) VALUES (
			$1, $2, $3, $4, $5,
			nullIf($6, ''), nullIf($7, ''), $8,
			$9, $10, $11,
			$12, nullIf($13, ''), nullIf($14, ''), $15,
			$16, $17, $18, $19
		)
		RETURNING id`,
		r.TraceID, r.RequestType, r.Question, r.ConfigID, r.Success,
		r.ErrorCode, r.ErrorMessage, r.TotalLatencyMS,
		r.InputTokens, r.OutputTokens, r.EmbeddingInputTokens,
		r.Cost, r.CostCurrency, r.PricingVersion, costComponents,
		r.Answer, nullableJSON(r.Citations), r.PromptSnapshot, r.ContextSnapshot,
	).Scan(&traceRowID)
	if err != nil {
		return err
	}
	for _, span := range r.Spans {
		metadataValue := span.Metadata
		if metadataValue == nil {
			metadataValue = map[string]any{}
		}
		metadata, err := json.Marshal(metadataValue)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO rag_spans (trace_id, span_name, started_at, duration_ms, metadata)
			VALUES ($1, $2, $3, $4, $5)`,
			traceRowID, span.Name, span.StartedAt, span.DurationMS, metadata,
		); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// nullableJSON keeps missing citations (validation/classification failures)
// as SQL NULL rather than an empty JSON array.
func nullableJSON(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	return raw
}

// TraceSummary is one row of the trace list.
type TraceSummary struct {
	TraceID               string   `json:"trace_id"`
	RequestType           string   `json:"request_type"`
	Question              string   `json:"question"`
	RagConfigID           string   `json:"rag_config_id"`
	Success               bool     `json:"success"`
	ErrorCode             *string  `json:"error_code"`
	TotalLatencyMS        int64    `json:"total_latency_ms"`
	InputTokens           *int     `json:"input_tokens"`
	OutputTokens          *int     `json:"output_tokens"`
	EmbeddingInputTokens  *int     `json:"embedding_input_tokens"`
	EstimatedCost         *float64 `json:"estimated_cost"`
	CostUnavailableReason *string  `json:"cost_unavailable_reason"`
	CreatedAt             string   `json:"created_at"`
}

// listSelect builds one consistent JSON snapshot per trace, newest first.
const listSelect = `
	SELECT jsonb_build_object(
		'trace_id', t.trace_id, 'request_type', t.request_type,
		'question', t.question, 'rag_config_id', t.rag_config_id,
		'success', t.success, 'error_code', t.error_code,
		'total_latency_ms', t.total_latency_ms,
		'input_tokens', t.input_tokens, 'output_tokens', t.output_tokens,
		'embedding_input_tokens', t.embedding_input_tokens,
		'estimated_cost', t.estimated_cost,
		'cost_unavailable_reason', t.cost_components->>'reason',
		'created_at', t.created_at
	)
	FROM rag_traces t
	ORDER BY t.created_at DESC
	LIMIT $1`

// List returns the most recent traces newest first, bounded by limit.
func (s *Store) List(ctx context.Context, limit int) ([]TraceSummary, error) {
	rows, err := s.pool.Query(ctx, listSelect, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	traces := []TraceSummary{}
	for rows.Next() {
		var raw json.RawMessage
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var t TraceSummary
		if err := json.Unmarshal(raw, &t); err != nil {
			return nil, err
		}
		traces = append(traces, t)
	}
	return traces, rows.Err()
}

// Span is one persisted stage of a trace detail.
type Span struct {
	SpanName   string          `json:"span_name"`
	StartedAt  string          `json:"started_at"`
	DurationMS int64           `json:"duration_ms"`
	Metadata   json.RawMessage `json:"metadata"`
}

// TraceDetail is the full trace detail contract: the trace row, the effective
// configuration, the rendered prompt, the ranked context actually sent, and
// every executed span. Reopening a trace therefore shows the evidence and
// config used at that time, never current state.
type TraceDetail struct {
	TraceID              string          `json:"trace_id"`
	RequestType          string          `json:"request_type"`
	Question             string          `json:"question"`
	RagConfigID          string          `json:"rag_config_id"`
	Success              bool            `json:"success"`
	ErrorCode            *string         `json:"error_code"`
	ErrorMessage         *string         `json:"error_message"`
	TotalLatencyMS       int64           `json:"total_latency_ms"`
	InputTokens          *int            `json:"input_tokens"`
	OutputTokens         *int            `json:"output_tokens"`
	EmbeddingInputTokens *int            `json:"embedding_input_tokens"`
	EstimatedCost        *float64        `json:"estimated_cost"`
	CostCurrency         *string         `json:"cost_currency"`
	PricingVersion       *string         `json:"pricing_version"`
	CostComponents       json.RawMessage `json:"cost_components"`
	Answer               *string         `json:"answer"`
	Citations            json.RawMessage `json:"citations"`
	Config               json.RawMessage `json:"config"`
	Prompt               json.RawMessage `json:"prompt_snapshot"`
	Context              json.RawMessage `json:"context_snapshot"`
	CreatedAt            string          `json:"created_at"`
	Spans                []Span          `json:"spans"`
}

const detailSelect = `
	SELECT jsonb_build_object(
		'trace_id', t.trace_id, 'request_type', t.request_type,
		'question', t.question, 'rag_config_id', t.rag_config_id,
		'success', t.success, 'error_code', t.error_code,
		'error_message', t.error_message, 'total_latency_ms', t.total_latency_ms,
		'input_tokens', t.input_tokens, 'output_tokens', t.output_tokens,
		'embedding_input_tokens', t.embedding_input_tokens,
		'estimated_cost', t.estimated_cost, 'cost_currency', t.cost_currency,
		'pricing_version', t.pricing_version, 'cost_components', t.cost_components,
		'answer', t.answer, 'citations', t.citations,
		'config', to_jsonb(c), 'prompt_snapshot', t.prompt_snapshot,
		'context_snapshot', t.context_snapshot, 'created_at', t.created_at,
		'spans', (
			SELECT COALESCE(json_agg(s ORDER BY s.started_at), '[]'::json)
			FROM rag_spans s WHERE s.trace_id = t.id
		)
	)
	FROM rag_traces t
	JOIN rag_configs c ON c.id = t.rag_config_id
	WHERE t.trace_id = $1`

// Get returns one trace detail by public trace ID. Unknown or malformed IDs
// return ErrNotFound.
func (s *Store) Get(ctx context.Context, traceID string) (TraceDetail, error) {
	if _, err := uuid.Parse(traceID); err != nil {
		return TraceDetail{}, ErrNotFound
	}

	rows, err := s.pool.Query(ctx, detailSelect, traceID)
	if err != nil {
		return TraceDetail{}, err
	}
	defer rows.Close()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return TraceDetail{}, err
		}
		return TraceDetail{}, ErrNotFound
	}
	var raw json.RawMessage
	if err := rows.Scan(&raw); err != nil {
		return TraceDetail{}, err
	}
	if err := rows.Err(); err != nil {
		return TraceDetail{}, err
	}

	var detail TraceDetail
	if err := json.Unmarshal(raw, &detail); err != nil {
		return TraceDetail{}, err
	}
	return detail, nil
}

// guard: the store satisfies the pipeline's TraceStore contract.
var _ rag.TraceStore = (*Store)(nil)
