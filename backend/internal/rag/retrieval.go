// Package rag owns the shared RAG query pipeline used by both normal chat
// and (from Sprint 4) evaluation: question validation, query embedding,
// retrieval (vector or hybrid with persisted fusion constants), versioned
// prompt construction, grounded generation, citation mapping, and trace
// persistence.
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
	"sort"
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
	ErrCodeRerankFailed          = "rerank_failed"
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

// FieldError is one field-level violation for a validated pipeline request.
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
// 1-based; Distance is pgvector cosine distance (lower is closer). Hybrid
// results additionally carry their branch ranks and fused score so traces
// and experiments reproduce the exact execution.
type Evidence struct {
	ChunkID    string  `json:"chunk_id"`
	DocumentID string  `json:"document_id"`
	RevisionID string  `json:"revision_id"`
	ChunkIndex int     `json:"chunk_index"`
	Content    string  `json:"content"`
	Distance   float64 `json:"distance"`
	Rank       int     `json:"rank"`
	VectorRank int     `json:"vector_rank,omitempty"`
	FTSRank    int     `json:"fts_rank,omitempty"`
	Score      float64 `json:"fusion_score,omitempty"`
}

// Retriever executes retrieval against the searchable_chunks boundary: the
// pgvector path (RB-09) or the deterministic hybrid fusion (RB-17).
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

// FTSEmptyNote documents FTS empty-branch behavior: a question with no
// lexical match contributes an empty branch; fusion proceeds with the other
// branch's candidates only (deterministically), and both-empty stays
// retrieval_empty.
const ftsQuery = `
	SELECT c.id, c.document_id, c.index_revision_id, c.chunk_index, c.content
	FROM searchable_chunks($1::uuid) c
	WHERE websearch_to_tsquery('english', $2) @@ to_tsvector('english', c.content)
	ORDER BY ts_rank_cd(to_tsvector('english', c.content), websearch_to_tsquery('english', $2)) DESC,
	         c.chunk_index ASC, c.document_id ASC, c.index_revision_id ASC, c.id ASC
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

// Retrieve executes the pinned retrieval mode for the question: pure
// pgvector top-k for vector mode, or a deterministic hybrid fusion (RB-17)
// for hybrid mode. Empty results classify as in vector mode. Stage duration
// is returned so callers can persist it as a span.
func (r *Retriever) Retrieve(ctx context.Context, cfg ragconfig.Config, question string, vector []float32) ([]Evidence, int64, *Error) {
	if cfg.RetrievalMode == ragconfig.RetrievalModeHybrid {
		return r.hybrid(ctx, cfg, question, vector)
	}
	return r.vector(ctx, cfg, vector)
}

// vector executes the plain pgvector path (RB-09 semantics, unchanged).
func (r *Retriever) vector(ctx context.Context, cfg ragconfig.Config, vector []float32) ([]Evidence, int64, *Error) {
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

	return evidence, elapsedMS(start), classifiedBoundary(r, ctx, cfg, len(evidence), elapsedMS(start))
}

// classifiedBoundary turns an empty pgvector result into its classified
// outcome, or nil when evidence exists.
func classifiedBoundary(r *Retriever, ctx context.Context, cfg ragconfig.Config, count int, elapsed int64) *Error {
	if count > 0 {
		return nil
	}
	var exists bool
	if err := r.pool.QueryRow(ctx, boundaryExistsQuery, cfg.ID).Scan(&exists); err != nil {
		return newError(ErrCodeRetrievalFailed, "check retrieval boundary: %v", err)
	}
	if !exists {
		return newError(ErrCodeRevisionUnavailable,
			"no live document has an active ready revision compatible with this configuration's embedding identity (%s/%s/%d dimensions); process documents or reindex them with this configuration",
			cfg.EmbeddingProvider, cfg.EmbeddingModel, cfg.EmbeddingDimensions)
	}
	return newError(ErrCodeRetrievalEmpty,
		"retrieval returned no usable evidence for this question; refusing to generate an unsupported answer")
}

// hybrid retrieves both branches independently and fuses the two candidate
// lists with the persisted RRF constants. Callers record the fusion settings
// with the trace (retrieval span metadata) so an experiment reproduces the
// same execution. Top-k truncation applies AFTER the merge.
func (r *Retriever) hybrid(ctx context.Context, cfg ragconfig.Config, question string, vector []float32) ([]Evidence, int64, *Error) {
	start := time.Now()

	vectorEvidence, _, vErr := r.vectorBranch(ctx, cfg, vector)
	if vErr != nil {
		return nil, 0, vErr
	}
	ftsEvidence, _, fErr := r.ftsBranch(ctx, cfg, question)
	if fErr != nil {
		return nil, 0, fErr
	}

	fused := fuseHybrid(vectorEvidence, ftsEvidence, cfg.RRFConstant, cfg.TopK)
	return fused, elapsedMS(start), classifiedBoundary(r, ctx, cfg, len(fused), elapsedMS(start))
}

// vectorBranch fetches the vector branch candidates (top vector limit).
func (r *Retriever) vectorBranch(ctx context.Context, cfg ragconfig.Config, vector []float32) ([]Evidence, int64, *Error) {
	branch := cfg
	branch.TopK = cfg.VectorCandidateLimit
	return r.vector(ctx, branch, vector)
}

// ftsBranch fetches the full-text candidates ordered by ts_rank_cd with the
// same deterministic identity tiebreakers, top fts limit.
func (r *Retriever) ftsBranch(ctx context.Context, cfg ragconfig.Config, question string) ([]Evidence, int64, *Error) {
	start := time.Now()
	rows, err := r.pool.Query(ctx, ftsQuery, cfg.ID, question, cfg.FTSCandidateLimit)
	if err != nil {
		return nil, 0, classifyRetrievalQuery(err)
	}
	defer rows.Close()
	evidence := []Evidence{}
	for rows.Next() {
		var e Evidence
		if err := rows.Scan(&e.ChunkID, &e.DocumentID, &e.RevisionID, &e.ChunkIndex, &e.Content); err != nil {
			return nil, 0, newError(ErrCodeRetrievalFailed, "read fts candidates: %v", err)
		}
		evidence = append(evidence, e)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, newError(ErrCodeRetrievalFailed, "read fts candidates: %v", err)
	}
	return evidence, elapsedMS(start), nil
}

// fuseHybrid is the deterministic RRF merge documented for RB-17, pure and
// unit-testable:
//   - deduplication: a chunk appears once regardless of how many branches hit;
//   - score = Σ_branch 1/(k + rank_branch) (missing branch contributes 0);
//   - ordering: score DESC, then vector-present before FTS-only, then
//     distance ASC, then immutable chunk identity (document, revision,
//     chunk index, id);
//   - top-k truncation happens AFTER the merge.
func fuseHybrid(vectorBranch, ftsBranch []Evidence, rrfConstant float64, topK int) []Evidence {
	type state struct {
		e      Evidence
		vRank  int
		fRank  int
		score  float64
		hasVec bool
		hasFTS bool
	}
	byKey := map[string]*state{}
	keys := []string{}
	slot := func(chunkID string, e Evidence) *state {
		s, ok := byKey[chunkID]
		if !ok {
			s = &state{e: Evidence{
				ChunkID:    e.ChunkID,
				DocumentID: e.DocumentID,
				RevisionID: e.RevisionID,
				ChunkIndex: e.ChunkIndex,
				Content:    e.Content,
				Distance:   e.Distance,
			}}
			byKey[chunkID] = s
			keys = append(keys, chunkID)
		}
		return s
	}
	for i, e := range vectorBranch {
		s := slot(e.ChunkID, e)
		if !s.hasVec {
			s.hasVec = true
			s.vRank = i + 1
			s.score += 1.0 / (rrfConstant + float64(s.vRank))
		}
	}
	for i, e := range ftsBranch {
		s := slot(e.ChunkID, e)
		if !s.hasFTS {
			s.hasFTS = true
			s.fRank = i + 1
			s.score += 1.0 / (rrfConstant + float64(s.fRank))
		}
	}

	out := make([]Evidence, 0, len(keys))
	for _, key := range keys {
		s := byKey[key]
		e := s.e
		e.Score = s.score
		e.VectorRank = s.vRank
		e.FTSRank = s.fRank
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		aVec, bVec := a.VectorRank > 0, b.VectorRank > 0
		if aVec != bVec {
			return aVec // vector-scored evidence precedes FTS-only on ties
		}
		if a.Distance != b.Distance {
			return a.Distance < b.Distance
		}
		// Immutable identity tiebreaks: document, revision, chunk index, id.
		if a.DocumentID != b.DocumentID {
			return a.DocumentID < b.DocumentID
		}
		if a.RevisionID != b.RevisionID {
			return a.RevisionID < b.RevisionID
		}
		if a.ChunkIndex != b.ChunkIndex {
			return a.ChunkIndex < b.ChunkIndex
		}
		return a.ChunkID < b.ChunkID
	})
	if len(out) > topK {
		out = out[:topK]
	}
	for i := range out {
		out[i].Rank = i + 1
	}
	return out
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

// EmbedQuestion embeds one question under the configuration's persisted
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
	queryVector := result.Vectors[0]
	if len(queryVector) != cfg.EmbeddingDimensions {
		return nil, nil, &Error{Code: providers.ErrCodeEmbeddingFailed,
			Message: fmt.Sprintf("query embedding dimensions must equal %d", cfg.EmbeddingDimensions)}
	}
	nonzero := false
	for _, value := range queryVector {
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
	return queryVector, result.PromptTokens, nil
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
