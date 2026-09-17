package rag

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"ragbench-my/backend/internal/providers"
	"ragbench-my/backend/internal/ragconfig"
)

// GenerationTimeout bounds one generation call so a hung provider surfaces
// as model_timeout instead of an endless request. chatTimeout covers the
// sequential embedding and generation calls plus pipeline overhead; the API
// write deadline is longer than this bound.
const (
	GenerationTimeout       = 60 * time.Second
	chatTimeout             = 2 * GenerationTimeout
	tracePersistenceTimeout = 5 * time.Second
)

// RequestTypeChat labels normal chat traces; Sprint 4 evaluation traces get
// their own request type.
const (
	RequestTypeChat       = "chat"
	RequestTypeEvaluation = "evaluation"
)

// Span names, matching the migration's rag_spans_name_check.
const (
	SpanRequest         = "request"
	SpanQueryEmbedding  = "query_embedding"
	SpanRetrieval       = "retrieval"
	SpanRerank          = "rerank"
	SpanPromptBuild     = "prompt_build"
	SpanLLMGeneration   = "llm_generation"
	SpanCitationMapping = "citation_mapping"
)

// Configs is the configuration surface the pipeline needs. *ragconfig.Store
// satisfies it.
type Configs interface {
	Get(ctx context.Context, id string) (ragconfig.Config, error)
}

// TraceRecord is the complete persisted record of one request.
type TraceRecord struct {
	TraceID              string
	RequestType          string
	Question             string
	ConfigID             string
	Success              bool
	ErrorCode            string
	ErrorMessage         string
	TotalLatencyMS       int64
	InputTokens          *int
	OutputTokens         *int
	EmbeddingInputTokens *int
	Cost                 *float64
	CostCurrency         string
	PricingVersion       string
	CostComponents       CostComponents
	Answer               *string
	Citations            json.RawMessage
	PromptSnapshot       json.RawMessage
	ContextSnapshot      json.RawMessage
	Spans                []SpanRecord
}

// EvidenceSource is the retrieval surface of the pipeline. *Retriever (the
// pgvector implementation) satisfies it; tests substitute deterministic
// doubles.
type EvidenceSource interface {
	Retrieve(ctx context.Context, cfg ragconfig.Config, question string, vector []float32) ([]Evidence, int64, *Error)
}

// Pipeline executes the shared RAG query pipeline. Both chat and evaluation
// call this exact path.
type Pipeline struct {
	Configs   Configs
	Retriever EvidenceSource
	Embedder  providers.Embedder
	Generator providers.Generator
	Reranker  providers.Reranker
	Traces    TraceStore
	// Now stubs the clock in tests; nil means time.Now.
	Now func() time.Time
}

// ChatRequest is the raw chat payload.
type ChatRequest struct {
	Question string `json:"question"`
	ConfigID string `json:"config_id"`
}

// ChatTrace is the trace summary returned with a chat response. Token
// fields and the cost are null when the provider or the pricing table did
// not support them — never zero substitutes.
type ChatTrace struct {
	TraceID               string   `json:"trace_id"`
	LatencyMS             int64    `json:"latency_ms"`
	InputTokens           *int     `json:"input_tokens"`
	OutputTokens          *int     `json:"output_tokens"`
	EmbeddingInputTokens  *int     `json:"embedding_input_tokens"`
	EstimatedCost         *float64 `json:"estimated_cost"`
	CostCurrency          *string  `json:"cost_currency"`
	CostUnavailableReason *string  `json:"cost_unavailable_reason"`
	PricingVersion        *string  `json:"pricing_version"`
}

// ChatResponse is the documented successful chat shape.
type ChatResponse struct {
	Answer    string     `json:"answer"`
	Citations []Citation `json:"citations"`
	Trace     ChatTrace  `json:"trace"`
	// Retrieved lists every ranked evidence chunk actually sent as context
	// (document-level identities in rank order). Chat clients may ignore it;
	// evaluation scoring uses it as the retrieval observation.
	Retrieved []RetrievedIdentity `json:"retrieved"`
}

// RetrievedIdentity is one context chunk's relevance-unit identity.
type RetrievedIdentity struct {
	DocumentID string `json:"document_id"`
	ChunkID    string `json:"chunk_id"`
	Rank       int    `json:"rank"`
}

// ValidateChatRequest checks the raw payload bounds.
func ValidateChatRequest(req ChatRequest) error {
	n := len([]rune(req.Question))
	var fields []FieldError
	if n < MinQuestionLen || n > MaxQuestionLen {
		fields = append(fields, FieldError{Field: "question",
			Message: "question is required and must be between 1 and 2000 characters"})
	}
	if req.ConfigID == "" {
		fields = append(fields, FieldError{Field: "config_id", Message: "config_id is required"})
	}
	if len(fields) > 0 {
		return &ValidationErrors{Fields: fields}
	}
	return nil
}

// Ask runs the full pipeline for normal chat.
func (p *Pipeline) Ask(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	return p.run(ctx, req, RequestTypeChat)
}

// AskEvaluation runs the exact same pipeline with evaluation request
// attribution (RB-14): identical stages, identical code, but traces record
// request_type=evaluation so evaluation cases are attributable.
func (p *Pipeline) AskEvaluation(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	return p.run(ctx, req, RequestTypeEvaluation)
}

// run is the single pipeline implementation: validate, load config, embed,
// retrieve, build the prompt, generate, map citations, persist the trace.
// Every executed outcome — including classified failures — produces a
// correlated trace. Validation, unknown-config, and capability rejections
// happen before any provider call and produce none.
func (p *Pipeline) run(ctx context.Context, req ChatRequest, requestType string) (ChatResponse, error) {
	// The question is normalized once: trimmed whitespace is not a question,
	// and traces/prompts store the normalized form.
	req.Question = strings.TrimSpace(req.Question)
	ctx, cancel := context.WithTimeout(ctx, chatTimeout)
	defer cancel()
	if err := ValidateChatRequest(req); err != nil {
		return ChatResponse{}, err
	}

	cfg, err := p.Configs.Get(ctx, req.ConfigID)
	if err != nil {
		if errors.Is(err, ragconfig.ErrNotFound) {
			return ChatResponse{}, newError(ErrCodeNotFound, "no rag config with the given id")
		}
		return ChatResponse{}, newError(ErrCodePersistenceFailed, "load configuration: %v", err)
	}
	if blocker := cfg.ExecutionBlocker(); blocker != nil {
		return ChatResponse{}, newError(ErrCodeCapabilityUnavailable, "%v", blocker)
	}
	if cfg.RerankEnabled && p.Reranker == nil {
		return ChatResponse{}, newError(ErrCodeCapabilityUnavailable, "reranker integration is not configured")
	}

	now := p.Now
	if now == nil {
		now = time.Now
	}
	startedAt := now()
	traceID := uuid.NewString()
	var (
		embedTokens            *int
		generationInputTokens  *int
		generationOutputTokens *int
		generationProfile      providers.GenerationProfile
		promptSnapshotJSON     = json.RawMessage(`{}`)
		contextSnapshotJSON    = json.RawMessage(`[]`)
	)

	// fail persists a classified failure trace and returns the structured
	// error carrying the trace ID for correlation.
	fail := func(serr *Error, spans ...SpanRecord) (ChatResponse, error) {
		total := now().Sub(startedAt).Milliseconds()
		spans = append(spans, SpanRecord{Name: SpanRequest, StartedAt: startedAt, DurationMS: total})
		failureCost := computeCost(cfg.EmbeddingProvider, cfg.EmbeddingModel, embedTokens,
			generationProfile.Provider, generationProfile.Model,
			generationInputTokens, generationOutputTokens)
		if perr := p.persistTrace(ctx, TraceRecord{
			TraceID: traceID, RequestType: requestType,
			Question: req.Question, ConfigID: cfg.ID,
			Success:              false,
			ErrorCode:            serr.Code,
			ErrorMessage:         serr.Message,
			TotalLatencyMS:       total,
			InputTokens:          generationInputTokens,
			OutputTokens:         generationOutputTokens,
			EmbeddingInputTokens: embedTokens,
			Cost:                 failureCost.Cost,
			CostCurrency:         failureCost.Currency,
			PricingVersion:       failureCost.PricingVersion,
			CostComponents:       failureCost.Components,
			PromptSnapshot:       promptSnapshotJSON,
			ContextSnapshot:      contextSnapshotJSON,
			Spans:                spans,
		}); perr != nil {
			return ChatResponse{}, perr
		}
		return ChatResponse{}, &Error{Code: serr.Code, Message: serr.Message, TraceID: traceID}
	}

	embedStart := now()
	vector, embedTokens, embedErr := EmbedQuestion(ctx, cfg, p.Embedder, req.Question)
	embedSpan := SpanRecord{Name: SpanQueryEmbedding, StartedAt: embedStart,
		DurationMS: now().Sub(embedStart).Milliseconds(), Metadata: map[string]any{
			"provider": cfg.EmbeddingProvider, "model": cfg.EmbeddingModel,
			"profile": cfg.EmbeddingProfile, "dimensions": cfg.EmbeddingDimensions,
		}}
	if embedErr != nil {
		return fail(embedErr, embedSpan)
	}
	if embedTokens != nil {
		embedSpan.Metadata["prompt_tokens"] = *embedTokens
	}

	// Retrieval stage.
	retrievalStart := now()
	retrievalCfg := cfg
	if cfg.RerankEnabled && cfg.RerankCandidateLimit > retrievalCfg.TopK {
		retrievalCfg.TopK = cfg.RerankCandidateLimit
	}
	evidence, retrievalMS, retrievalErr := p.Retriever.Retrieve(ctx, retrievalCfg, req.Question, vector)
	retrievalSpan := SpanRecord{Name: SpanRetrieval,
		StartedAt: retrievalStart, DurationMS: retrievalMS, Metadata: map[string]any{"top_k": retrievalCfg.TopK, "final_top_k": cfg.TopK, "returned": len(evidence)}}
	if cfg.RetrievalMode == ragconfig.RetrievalModeHybrid {
		retrievalSpan.Metadata["retrieval_mode"] = "hybrid"
		retrievalSpan.Metadata["fusion_method"] = cfg.FusionMethod
		retrievalSpan.Metadata["rrf_rank_constant"] = cfg.RRFConstant
		retrievalSpan.Metadata["fts_candidate_limit"] = cfg.FTSCandidateLimit
		retrievalSpan.Metadata["vector_candidate_limit"] = cfg.VectorCandidateLimit
	}
	if retrievalErr != nil {
		return fail(retrievalErr, embedSpan, retrievalSpan)
	}

	var rerankSpan *SpanRecord
	if cfg.RerankEnabled {
		rerankStart := now()
		profile, profileErr := providers.RerankProfileByName(cfg.RerankerProfile)
		if profileErr != nil {
			span := SpanRecord{Name: SpanRerank, StartedAt: rerankStart,
				DurationMS: now().Sub(rerankStart).Milliseconds(),
				Metadata:   map[string]any{"profile": cfg.RerankerProfile, "candidate_limit": cfg.RerankCandidateLimit}}
			return fail(newError(ErrCodeRerankFailed, "%v", profileErr), embedSpan, retrievalSpan, span)
		}
		if p.Reranker == nil {
			span := SpanRecord{Name: SpanRerank, StartedAt: rerankStart,
				DurationMS: now().Sub(rerankStart).Milliseconds(),
				Metadata:   map[string]any{"profile": profile.Name, "candidate_limit": cfg.RerankCandidateLimit}}
			// A binary built without the optional integration still rejects the
			// request explicitly; it never falls back to the un-reranked list.
			return fail(newError(ErrCodeCapabilityUnavailable, "reranker provider %q is not configured", profile.Provider), embedSpan, retrievalSpan, span)
		}
		candidates := make([]providers.RerankCandidate, len(evidence))
		for i, e := range evidence {
			candidates[i] = providers.RerankCandidate{ChunkID: e.ChunkID, DocumentID: e.DocumentID,
				RevisionID: e.RevisionID, ChunkIndex: e.ChunkIndex, Content: e.Content,
				Distance: e.Distance, Rank: e.Rank, VectorRank: e.VectorRank,
				FTSRank: e.FTSRank, Score: e.Score}
		}
		result, rerankErr := p.Reranker.Rerank(ctx, profile, req.Question, candidates)
		span := SpanRecord{Name: SpanRerank, StartedAt: rerankStart,
			DurationMS: now().Sub(rerankStart).Milliseconds(),
			Metadata: map[string]any{"profile": profile.Name, "provider": profile.Provider,
				"candidate_limit": cfg.RerankCandidateLimit, "candidates": len(candidates)}}
		if rerankErr != nil {
			span.Metadata["error_code"] = ErrCodeRerankFailed
			return fail(newError(ErrCodeRerankFailed, "%v", rerankErr), embedSpan, retrievalSpan, span)
		}
		if len(result.Candidates) != len(candidates) {
			return fail(newError(ErrCodeRerankFailed, "reranker returned %d candidates for %d inputs", len(result.Candidates), len(candidates)), embedSpan, retrievalSpan, span)
		}
		seen := map[string]bool{}
		known := make(map[string]bool, len(candidates))
		for _, candidate := range candidates {
			known[candidate.ChunkID] = true
		}
		ranked := make([]Evidence, 0, len(result.Candidates))
		finalOrder := make([]string, 0, len(result.Candidates))
		for i, c := range result.Candidates {
			if seen[c.ChunkID] || c.ChunkID == "" || !known[c.ChunkID] {
				return fail(newError(ErrCodeRerankFailed, "reranker returned duplicate or empty chunk identity"), embedSpan, retrievalSpan, span)
			}
			seen[c.ChunkID] = true
			ranked = append(ranked, Evidence{ChunkID: c.ChunkID, DocumentID: c.DocumentID,
				RevisionID: c.RevisionID, ChunkIndex: c.ChunkIndex, Content: c.Content,
				Distance: c.Distance, Rank: i + 1, VectorRank: c.VectorRank,
				FTSRank: c.FTSRank, Score: c.Score})
			finalOrder = append(finalOrder, c.ChunkID)
		}
		if len(ranked) > cfg.TopK {
			ranked = ranked[:cfg.TopK]
			finalOrder = finalOrder[:cfg.TopK]
		}
		evidence = ranked
		span.Metadata["final_order"] = finalOrder
		if result.InputTokens != nil {
			span.Metadata["input_tokens"] = *result.InputTokens
		}
		if result.OutputTokens != nil {
			span.Metadata["output_tokens"] = *result.OutputTokens
		}
		rerankSpan = &span
	}

	// Prompt build stage with context budgeting.
	promptStart := now()
	selected := SelectContext(evidence, ContextBudgetChars)
	contextSnapshotJSON, _ = json.Marshal(selected)
	promptText, promptErr := BuildPrompt(cfg, req.Question, selected)
	promptSpan := SpanRecord{Name: SpanPromptBuild, StartedAt: promptStart,
		DurationMS: now().Sub(promptStart).Milliseconds(), Metadata: map[string]any{
			"prompt_version": cfg.PromptVersion, "context_chunks": len(selected)}}
	if promptErr != nil {
		return fail(promptErr, stageSpans(rerankSpan, embedSpan, retrievalSpan, promptSpan)...)
	}
	promptSnapshotJSON, _ = json.Marshal(snapshotPrompt(cfg, promptText, selected))

	// Generation stage under its own deadline.
	genProfile := providers.GenerationProfile{
		Name: cfg.ModelProfile, Provider: cfg.ModelProvider, Model: cfg.ModelName,
	}
	var genProfileErr error
	// Configs created before the Settings catalog migration (and lightweight
	// in-memory test configs) do not have the concrete columns. Keep their
	// explicit legacy registry identity working while making persisted settings
	// the normal runtime path.
	if genProfile.Provider == "" || genProfile.Model == "" {
		genProfile, genProfileErr = providers.GenerationProfileByName(cfg.ModelProfile)
	}
	generationProfile = genProfile
	if genProfileErr != nil {
		return fail(newError(ErrCodeCapabilityUnavailable, "%v", genProfileErr),
			stageSpans(rerankSpan, embedSpan, retrievalSpan, promptSpan)...)
	}
	genStart := now()
	genCtx, cancelGen := context.WithTimeout(ctx, GenerationTimeout)
	genRes, genErr := p.Generator.Generate(genCtx, genProfile, promptText)
	generationInputTokens = genRes.InputTokens
	generationOutputTokens = genRes.OutputTokens
	cancelGen()
	genSpan := SpanRecord{
		Name:       SpanLLMGeneration,
		StartedAt:  genStart,
		DurationMS: now().Sub(genStart).Milliseconds(),
		Metadata: map[string]any{
			"model_profile": cfg.ModelProfile,
			"model":         genProfile.Model,
		},
	}
	if genRes.Model != "" {
		genSpan.Metadata["response_model"] = genRes.Model
	}
	if genErr != nil {
		return fail(wrapProviderError(genErr), stageSpans(rerankSpan, embedSpan, retrievalSpan, promptSpan, genSpan)...)
	}
	if genRes.InputTokens != nil {
		genSpan.Metadata["input_tokens"] = *genRes.InputTokens
	}
	if genRes.OutputTokens != nil {
		genSpan.Metadata["output_tokens"] = *genRes.OutputTokens
	}

	// Citation mapping stage.
	citeStart := now()
	citations, insufficient, citeErr := mapCitations(genRes.Text, selected)
	citeSpan := SpanRecord{Name: SpanCitationMapping, StartedAt: citeStart,
		DurationMS: now().Sub(citeStart).Milliseconds(), Metadata: map[string]any{
			"citations": len(citations), "insufficient_evidence": insufficient,
			"answer_chars": len([]rune(genRes.Text))}}
	if citeErr != nil {
		// The raw output stays diagnosable in the span without being
		// returned as an answer: it carries invalid or missing citations.
		citeSpan.Metadata["raw_answer"] = genRes.Text
		return fail(citeErr, stageSpans(rerankSpan, embedSpan, retrievalSpan, promptSpan, genSpan, citeSpan)...)
	}

	// Cost from explicit rates; unavailable stays null, never zero.
	cost := computeCost(cfg.EmbeddingProvider, cfg.EmbeddingModel, embedTokens,
		genProfile.Provider, genProfile.Model, genRes.InputTokens, genRes.OutputTokens)

	if citations == nil {
		citations = []Citation{}
	}
	retrieved := make([]RetrievedIdentity, len(selected))
	for i, e := range selected {
		retrieved[i] = RetrievedIdentity{DocumentID: e.DocumentID, ChunkID: e.ChunkID, Rank: i + 1}
	}
	resp := ChatResponse{
		Answer:    genRes.Text,
		Citations: citations,
		Retrieved: retrieved,
		Trace: ChatTrace{
			TraceID:              traceID,
			LatencyMS:            now().Sub(startedAt).Milliseconds(),
			InputTokens:          genRes.InputTokens,
			OutputTokens:         genRes.OutputTokens,
			EmbeddingInputTokens: embedTokens,
			PricingVersion:       &cost.PricingVersion,
		},
	}
	if cost.Cost != nil {
		resp.Trace.EstimatedCost = cost.Cost
		currency := cost.Currency
		resp.Trace.CostCurrency = &currency
	} else {
		reason := cost.Components.Reason
		resp.Trace.CostUnavailableReason = &reason
	}

	// Persist trace + spans atomically. A write failure must surface as a
	// structured persistence error, never a traceable success.
	total := now().Sub(startedAt).Milliseconds()
	answerText := genRes.Text
	citationsJSON, _ := json.Marshal(citations)
	spans := stageSpans(rerankSpan, embedSpan, retrievalSpan, promptSpan, genSpan, citeSpan,
		SpanRecord{Name: SpanRequest, StartedAt: startedAt, DurationMS: total})
	if perr := p.persistTrace(ctx, TraceRecord{
		TraceID: traceID, RequestType: requestType,
		Question: req.Question, ConfigID: cfg.ID,
		Success:              true,
		TotalLatencyMS:       total,
		InputTokens:          genRes.InputTokens,
		OutputTokens:         genRes.OutputTokens,
		EmbeddingInputTokens: embedTokens,
		Cost:                 cost.Cost,
		CostCurrency:         cost.Currency,
		PricingVersion:       cost.PricingVersion,
		CostComponents:       cost.Components,
		Answer:               &answerText,
		Citations:            citationsJSON,
		PromptSnapshot:       promptSnapshotJSON,
		ContextSnapshot:      contextSnapshotJSON,
		Spans:                spans,
	}); perr != nil {
		return ChatResponse{}, perr
	}
	return resp, nil
}

// stageSpans inserts the optional rerank stage after retrieval. Keeping this
// in one place makes successful and classified-failure traces agree on the
// actual stage order.
func stageSpans(rerank *SpanRecord, spans ...SpanRecord) []SpanRecord {
	if rerank == nil || len(spans) < 2 {
		return spans
	}
	out := make([]SpanRecord, 0, len(spans)+1)
	out = append(out, spans[:2]...)
	out = append(out, *rerank)
	out = append(out, spans[2:]...)
	return out
}

// persistTrace wraps TraceStore.Insert so a store failure becomes the
// structured persistence error. The trace ID is retained for log correlation.
func (p *Pipeline) persistTrace(ctx context.Context, record TraceRecord) error {
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), tracePersistenceTimeout)
	defer cancel()
	if err := p.Traces.Insert(persistCtx, record); err != nil {
		return &Error{
			Code:    ErrCodePersistenceFailed,
			Message: "trace persistence failed; the answer is not traceable and is not returned: " + err.Error(),
			TraceID: record.TraceID,
		}
	}
	return nil
}
