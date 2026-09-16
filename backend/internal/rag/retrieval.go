// Package rag owns the shared RAG query pipeline used by both normal chat
// and (from Sprint 4) evaluation: question validation, query embedding,
// pgvector retrieval with top-k truncation, versioned prompt construction,
// grounded generation, citation mapping, and trace persistence.
//
// Retrieval never mixes incompatible revisions: the searchable_chunks SQL
// function restricts candidates to live documents' active ready revisions
// whose embedding identity matches the saved configuration.
package rag

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"ragbench-my/backend/internal/providers"
	"ragbench-my/backend/internal/ragconfig"
)

// Structured error codes for the query pipeline. HTTP status mapping lives
// in internal/httpapi; here they classify pipeline failures so traces and
// logs use the same vocabulary as MONITORING.md.
const (
	ErrCodeNotFound              = "not_found"
	ErrCodeValidationFailed      = "validation_failed"
	ErrCodeInvalidBody           = "invalid_body"
	ErrCodeCapabilityUnavailable = "capability_unavailable"
	ErrCodeRevisionUnavailable   = "revision_unavailable"
	ErrCodeRetrievalEmpty        = "retrieval_empty"
	ErrCodeRetrievalFailed       = "retrieval_failed"
	ErrCodeCitationMissing       = "citation_missing"
	ErrCodeCitationInvalid       = "citation_invalid"
	ErrCodePersistenceFailed     = "persistence_failed"
	ErrCodeEmbeddingFailed       = providers.ErrCodeEmbeddingFailed
	ErrCodeModelTimeout          = providers.ErrCodeModelTimeout
	ErrCodeModelRateLimited      = providers.ErrCodeModelRateLimited
	ErrCodeMalformedResponse     = providers.ErrCodeMalformedResponse
	ErrCodeModelFailed           = providers.ErrCodeModelFailed
)

// Question bounds. Declared here so no validation rule is a hidden magic
// number; the API documentation mirrors them.
const (
	MinQuestionLen = 1
	MaxQuestionLen = 2000
)

// Error is a structured pipeline failure with a documented code and, when
// the request produced one, the trace ID it was persisted under so HTTP
// errors stay correlated.
type Error struct {
	Code    string
	Message string
	// TraceID is empty for pre-execution rejections (validation, unknown
	// config, unavailable capability) that produce no trace.
	TraceID string
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func newError(code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// ValidationErrors carries one field-level violation for the chat request.
type FieldError struct {
	Field   string
	Message string
}

// ValidationErrors is a field-keyed validation failure.
type ValidationErrors struct {
	Fields []FieldError
}

func (e *ValidationErrors) Error() string {
	parts := make([]string, len(e.Fields))
	for i, fe := range e.Fields {
		parts[i] = fe.Field + ": " + fe.Message
	}
	return "invalid request: " + strings.Join(parts, "; ")
}

// Evidence is one retrieved chunk with full evidence identity. Rank is
// 1-based; Distance is pgvector cosine distance (lower is closer).
type Evidence struct {
	ChunkID    string  `json:"chunk_id"`
	DocumentID string  `json:"document_id"`
	RevisionID string  `json:"revision_id"`
	ChunkIndex int     `json:"chunk_index"`
	Content    string  `json:"content"`
	Distance   float64 `json:"distance"`
	Rank       int     `json:"rank"`
}

// Retriever executes vector retrieval against the searchable_chunks boundary.
type Retriever struct {
	pool *pgxpool.Pool
}

func NewRetriever(pool *pgxpool.Pool) *Retriever { return &Retriever{pool: pool} }

// ordered top-k over the retrieval boundary; cosine distance with chunk and
// immutable identity tiebreaks keeps equal-distance results deterministic.
const retrievalQuery = `
	SELECT c.id, c.document_id, c.index_revision_id, c.chunk_index, c.content,
	       c.embedding <=> $1::vector AS distance
	FROM searchable_chunks($2::uuid) c
	ORDER BY c.embedding <=> $1::vector, c.chunk_index ASC,
	         c.document_id ASC, c.index_revision_id ASC, c.id ASC
	LIMIT $3`

const retrievalQuery1536 = `
	SELECT c.id, c.document_id, c.index_revision_id, c.chunk_index, c.content,
	       (c.embedding::vector(1536)) <=> $1::vector(1536) AS distance
	FROM searchable_chunks($2::uuid) c
	WHERE vector_dims(c.embedding) = 1536
	ORDER BY (c.embedding::vector(1536)) <=> $1::vector(1536), c.chunk_index ASC,
	         c.document_id ASC, c.index_revision_id ASC, c.id ASC
	LIMIT $3`

const retrievalQuery384 = `
	SELECT c.id, c.document_id, c.index_revision_id, c.chunk_index, c.content,
	       (c.embedding::vector(384)) <=> $1::vector(384) AS distance
	FROM searchable_chunks($2::uuid) c
	WHERE vector_dims(c.embedding) = 384
	ORDER BY (c.embedding::vector(384)) <=> $1::vector(384), c.chunk_index ASC,
	         c.document_id ASC, c.index_revision_id ASC, c.id ASC
	LIMIT $3`

// boundaryExists reports whether any live document currently has an active
// ready revision compatible with the configuration's embedding identity.
const boundaryExistsQuery = `
	SELECT EXISTS (
		SELECT 1
		FROM documents d
		JOIN index_revisions r ON r.id = d.active_revision_id
		JOIN rag_configs cfg ON cfg.id = $1
		WHERE d.deleted_at IS NULL
		  AND r.status = 'ready'
		  AND r.embedding_provider = cfg.embedding_provider
		  AND r.embedding_model = cfg.embedding_model
		  AND r.embedding_dimensions = cfg.embedding_dimensions
	)`

// Retrieve returns the top-k most relevant chunks for the query vector,
// ordered by cosine distance. An empty result is classified: a missing
// retrieval boundary (nothing indexed, everything deleted, or an embedding
// mismatch) is revision_unavailable; a boundary with chunks but no usable
// evidence stays retrieval_empty. Stage duration is returned so callers can
// persist it as a span.
func (r *Retriever) Retrieve(ctx context.Context, cfg ragconfig.Config, vector []float32) ([]Evidence, int64, *Error) {
	start := time.Now()
	query := retrievalQuery
	switch cfg.EmbeddingDimensions {
	case 1536:
		query = retrievalQuery1536
	case 384:
		query = retrievalQuery384
	}
	rows, err := r.pool.Query(ctx, query, vectorLiteral(vector), cfg.ID, cfg.TopK)
	if err != nil {
		return nil, 0, classifyRetrievalQuery(err)
	}
	defer rows.Close()

	evidence := []Evidence{}
	for rows.Next() {
		var e Evidence
		if err := rows.Scan(&e.ChunkID, &e.DocumentID, &e.RevisionID, &e.ChunkIndex, &e.Content, &e.Distance); err != nil {
			return nil, 0, newError(ErrCodeRetrievalFailed, "read retrieved evidence: %v", err)
		}
		e.Rank = len(evidence) + 1
		evidence = append(evidence, e)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, newError(ErrCodeRetrievalFailed, "read retrieved evidence: %v", err)
	}

	if len(evidence) == 0 {
		var exists bool
		if err := r.pool.QueryRow(ctx, boundaryExistsQuery, cfg.ID).Scan(&exists); err != nil {
			return nil, 0, newError(ErrCodeRetrievalFailed, "check retrieval boundary: %v", err)
		}
		if !exists {
			return nil, elapsedMS(start), newError(ErrCodeRevisionUnavailable,
				"no live document has an active ready revision compatible with this configuration's embedding identity (%s/%s/%d dimensions); process documents or reindex them with this configuration",
				cfg.EmbeddingProvider, cfg.EmbeddingModel, cfg.EmbeddingDimensions)
		}
		return nil, elapsedMS(start), newError(ErrCodeRetrievalEmpty,
			"retrieval returned no usable evidence for this question; refusing to generate an unsupported answer")
	}
	return evidence, elapsedMS(start), nil
}

func classifyRetrievalQuery(err error) *Error {
	if errors.Is(err, context.DeadlineExceeded) {
		return newError(ErrCodeRetrievalFailed, "retrieval timed out")
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return newError(ErrCodeRetrievalFailed, "retrieval query returned no rows")
	}
	return newError(ErrCodeRetrievalFailed, "retrieval query failed: %v", err)
}

// vectorLiteral formats a float32 vector as a pgvector string literal so the
// query can cast it explicitly without a dependency on the pgvector Go type.
func vectorLiteral(vector []float32) string {
	parts := make([]string, len(vector))
	for i, v := range vector {
		parts[i] = strconv.FormatFloat(float64(v), 'g', -1, 32)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func elapsedMS(start time.Time) int64 { return time.Since(start).Milliseconds() }

// EmbedQuery embeds one question under the configuration's persisted
// embedding identity and validates the provider response shape.
func EmbedQuestion(ctx context.Context, cfg ragconfig.Config, embedder providers.Embedder, question string) ([]float32, *int, *Error) {
	profile := providers.EmbeddingProfile{
		Name:       cfg.EmbeddingProfile,
		Provider:   cfg.EmbeddingProvider,
		Model:      cfg.EmbeddingModel,
		Dimensions: cfg.EmbeddingDimensions,
	}
	result, err := embedder.Embed(ctx, profile, []string{question})
	if err != nil {
		return nil, nil, wrapProviderError(err)
	}
	if len(result.Vectors) != 1 {
		return nil, nil, &Error{Code: providers.ErrCodeEmbeddingFailed,
			Message: fmt.Sprintf("query embedding returned %d vectors for 1 question", len(result.Vectors))}
	}
	vector := result.Vectors[0]
	if len(vector) != cfg.EmbeddingDimensions {
		return nil, nil, &Error{Code: providers.ErrCodeEmbeddingFailed,
			Message: fmt.Sprintf("query embedding dimensions must equal %d", cfg.EmbeddingDimensions)}
	}
	nonzero := false
	for _, value := range vector {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return nil, nil, &Error{Code: providers.ErrCodeEmbeddingFailed,
				Message: "query embedding returned invalid vector values"}
		}
		if value != 0 {
			nonzero = true
		}
	}
	if !nonzero {
		return nil, nil, &Error{Code: providers.ErrCodeEmbeddingFailed,
			Message: "query embedding returned a zero vector, unusable for cosine search"}
	}
	return vector, result.PromptTokens, nil
}

// wrapProviderError converts a providers.ProviderError into the pipeline's
// *Error, preserving the classified code.
func wrapProviderError(err error) *Error {
	var perr *providers.ProviderError
	if errors.As(err, &perr) {
		return &Error{Code: perr.Code, Message: perr.Message}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &Error{Code: ErrCodeModelTimeout, Message: "generation did not finish within the configured deadline"}
	}
	return &Error{Code: ErrCodeModelFailed, Message: err.Error()}
}
