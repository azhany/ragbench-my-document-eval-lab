package rag

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"ragbench-my/backend/internal/providers"
	"ragbench-my/backend/internal/ragconfig"
)

// The pipeline tests use deterministic doubles for every external surface
// (config store, embedding, retrieval, generation, trace persistence). The
// pgvector boundary and the real provider clients receive their own
// integration tests.

var testConfig = ragconfig.Config{
	ID:                  "0b6c8cb1-6a7d-4d3f-9f6a-52f0a1b2c3d4",
	Name:                "baseline-v1",
	ChunkSize:           500,
	ChunkOverlap:        80,
	RetrievalMode:       "vector",
	TopK:                3,
	RerankEnabled:       false,
	PromptVersion:       "v1",
	ModelProfile:        "openai-gpt-4o-mini",
	EmbeddingProfile:    "openai-text-embedding-3-small",
	EmbeddingProvider:   "openai",
	EmbeddingModel:      "text-embedding-3-small",
	EmbeddingDimensions: 1536,
}

type fakeConfigs struct {
	config ragconfig.Config
	err    error
}

func (f *fakeConfigs) Get(ctx context.Context, id string) (ragconfig.Config, error) {
	if f.err != nil {
		return ragconfig.Config{}, f.err
	}
	return f.config, nil
}

type fakeEmbedder struct {
	result providers.EmbeddingResult
	err    error
	calls  int
}

func (f *fakeEmbedder) Embed(ctx context.Context, profile providers.EmbeddingProfile, texts []string) (providers.EmbeddingResult, error) {
	f.calls++
	if f.err != nil {
		return providers.EmbeddingResult{}, f.err
	}
	return f.result, nil
}

type fakeRetriever struct {
	evidence []Evidence
	ms       int64
	err      *Error
	calls    int
}

func (f *fakeRetriever) Retrieve(ctx context.Context, cfg ragconfig.Config, question string, vector []float32) ([]Evidence, int64, *Error) {
	f.calls++
	if f.err != nil {
		return nil, f.ms, f.err
	}
	return f.evidence, f.ms, nil
}

func defaultEvidence() []Evidence {
	return []Evidence{
		{ChunkID: "chunk-1", DocumentID: "doc-1", RevisionID: "rev-1", ChunkIndex: 0,
			Content: "approval requires two signatures", Distance: 0.1, Rank: 1},
		{ChunkID: "chunk-2", DocumentID: "doc-1", RevisionID: "rev-1", ChunkIndex: 1,
			Content: "storage is audited quarterly", Distance: 0.2, Rank: 2},
		{ChunkID: "chunk-3", DocumentID: "doc-2", RevisionID: "rev-2", ChunkIndex: 0,
			Content: "retention is indefinite", Distance: 0.3, Rank: 3},
	}
}

type fakeGenerator struct {
	result providers.GenerationResult
	err    error
	prompt string
	calls  int
}

func (f *fakeGenerator) Generate(ctx context.Context, profile providers.GenerationProfile, prompt string) (providers.GenerationResult, error) {
	f.calls++
	f.prompt = prompt
	if f.err != nil {
		return providers.GenerationResult{}, f.err
	}
	return f.result, nil
}

type fakeTraces struct {
	records []TraceRecord
	err     error
}

func (f *fakeTraces) Insert(ctx context.Context, record TraceRecord) error {
	if f.err != nil {
		return f.err
	}
	f.records = append(f.records, record)
	return nil
}

type contextCheckingTrace struct{}

func (contextCheckingTrace) Insert(ctx context.Context, record TraceRecord) error {
	return ctx.Err()
}

func newPipeline(embedder providers.Embedder, retriever EvidenceSource, generator providers.Generator, traces *fakeTraces) *Pipeline {
	return &Pipeline{
		Configs:   &fakeConfigs{config: testConfig},
		Retriever: retriever,
		Embedder:  embedder,
		Generator: generator,
		Traces:    traces,
		Now: func() func() time.Time {
			base := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
			i := 0
			return func() time.Time {
				i++
				return base.Add(time.Duration(i-1) * time.Millisecond)
			}
		}(),
	}
}

func okEmbedder(tokens int) *fakeEmbedder {
	vector := make([]float32, 1536)
	vector[0], vector[1], vector[2] = 0.1, 0.2, 0.3
	return &fakeEmbedder{result: providers.EmbeddingResult{
		Vectors:      [][]float32{vector},
		PromptTokens: &tokens,
	}}
}

func okGenerator(answer string) *fakeGenerator {
	in, out := 1102, 284
	return &fakeGenerator{result: providers.GenerationResult{
		Text: answer, InputTokens: &in, OutputTokens: &out,
	}}
}

func TestAskHappyPath(t *testing.T) {
	traces := &fakeTraces{}
	p := newPipeline(okEmbedder(34), &fakeRetriever{evidence: defaultEvidence(), ms: 5},
		okGenerator("Approval needs two signatures [1] and storage is audited [2]."), traces)

	resp, err := p.Ask(context.Background(), ChatRequest{Question: "How does approval work?", ConfigID: testConfig.ID})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.Answer != "Approval needs two signatures [1] and storage is audited [2]." {
		t.Fatalf("answer = %q", resp.Answer)
	}
	if len(resp.Citations) != 2 {
		t.Fatalf("citations = %d, want 2", len(resp.Citations))
	}
	if resp.Citations[0].ChunkID != "chunk-1" || resp.Citations[0].DocumentID != "doc-1" {
		t.Fatalf("citation[0] = %+v", resp.Citations[0])
	}
	if resp.Citations[1].ChunkID != "chunk-2" {
		t.Fatalf("citation[1] = %+v", resp.Citations[1])
	}
	if resp.Citations[0].Snippet != "approval requires two signatures" {
		t.Fatalf("snippet = %q", resp.Citations[0].Snippet)
	}

	// Trace summary: persisted trace id, truthful usage/cost.
	if resp.Trace.TraceID == "" {
		t.Fatal("missing trace id")
	}
	if resp.Trace.InputTokens == nil || *resp.Trace.InputTokens != 1102 {
		t.Fatalf("input tokens = %v", resp.Trace.InputTokens)
	}
	if resp.Trace.OutputTokens == nil || *resp.Trace.OutputTokens != 284 {
		t.Fatalf("output tokens = %v", resp.Trace.OutputTokens)
	}
	if resp.Trace.EmbeddingInputTokens == nil || *resp.Trace.EmbeddingInputTokens != 34 {
		t.Fatalf("embedding tokens = %v", resp.Trace.EmbeddingInputTokens)
	}
	if resp.Trace.EstimatedCost == nil || *resp.Trace.EstimatedCost != 0.000336 {
		t.Fatalf("estimated cost = %v, want 0.000336", resp.Trace.EstimatedCost)
	}
	if resp.Trace.CostCurrency == nil || *resp.Trace.CostCurrency != "USD" {
		t.Fatalf("cost currency = %v", resp.Trace.CostCurrency)
	}
	if resp.Trace.CostUnavailableReason != nil {
		t.Fatalf("unexpected unavailability reason %v", resp.Trace.CostUnavailableReason)
	}

	// Persisted record: six executed spans in order, snapshots recoverable.
	if len(traces.records) != 1 {
		t.Fatalf("persisted %d trace records, want 1", len(traces.records))
	}
	record := traces.records[0]
	if !record.Success || record.ErrorCode != "" {
		t.Fatalf("record success=%v code=%q", record.Success, record.ErrorCode)
	}
	if record.ConfigID != testConfig.ID {
		t.Fatalf("record config = %q, want %q", record.ConfigID, testConfig.ID)
	}
	if got := spanNames(record); len(got) != 6 {
		t.Fatalf("spans = %v, want 6 executed stages", got)
	}

	var prompt promptSnapshot
	if err := json.Unmarshal(record.PromptSnapshot, &prompt); err != nil {
		t.Fatalf("prompt snapshot: %v", err)
	}
	if prompt.PromptVersion != "v1" || prompt.PromptIdentifier != "grounded-answer" {
		t.Fatalf("prompt identity = %s/%s", prompt.PromptVersion, prompt.PromptIdentifier)
	}
	if !strings.Contains(prompt.PromptText, "How does approval work?") {
		t.Fatal("prompt text must contain the question")
	}
	if !strings.Contains(prompt.PromptText, "[1] approval requires two signatures") {
		t.Fatal("prompt text must contain the numbered evidence")
	}

	var context []Evidence
	if err := json.Unmarshal(record.ContextSnapshot, &context); err != nil {
		t.Fatalf("context snapshot: %v", err)
	}
	if len(context) != 3 || context[0].ChunkID != "chunk-1" || context[2].ChunkID != "chunk-3" {
		t.Fatalf("context snapshot = %+v", context)
	}

	var citations []Citation
	if err := json.Unmarshal(record.Citations, &citations); err != nil {
		t.Fatalf("citations snapshot: %v", err)
	}
	if len(citations) != 2 {
		t.Fatalf("persisted citations = %d", len(citations))
	}
	if record.Answer == nil || *record.Answer != resp.Answer {
		t.Fatal("persisted answer must match the response")
	}
}

func spanNames(r TraceRecord) []string {
	names := make([]string, 0, len(r.Spans))
	for _, s := range r.Spans {
		names = append(names, s.Name)
	}
	return names
}

func TestAskValidationRejectedBeforeExecution(t *testing.T) {
	traces := &fakeTraces{}
	p := newPipeline(okEmbedder(1), &fakeRetriever{}, okGenerator("x"), traces)

	_, err := p.Ask(context.Background(), ChatRequest{Question: "   ", ConfigID: testConfig.ID})
	if err == nil {
		t.Fatal("expected validation error for blank question")
	}
	var verrs *ValidationErrors
	if !errors.As(err, &verrs) {
		t.Fatalf("want ValidationErrors, got %T", err)
	}
	_, err = p.Ask(context.Background(), ChatRequest{Question: strings.Repeat("q", 2100), ConfigID: testConfig.ID})
	if !errors.As(err, &verrs) {
		t.Fatalf("want ValidationErrors for long question, got %v", err)
	}
	_, err = p.Ask(context.Background(), ChatRequest{Question: "ok", ConfigID: ""})
	if !errors.As(err, &verrs) {
		t.Fatalf("want ValidationErrors for missing config id, got %v", err)
	}
	if len(traces.records) != 0 {
		t.Fatalf("rejected requests must not produce traces, got %d", len(traces.records))
	}
}

func TestAskUnknownConfigDistinctError(t *testing.T) {
	traces := &fakeTraces{}
	p := newPipeline(okEmbedder(1), &fakeRetriever{}, okGenerator("x"), traces)
	p.Configs = &fakeConfigs{err: ragconfig.ErrNotFound}

	_, err := p.Ask(context.Background(), ChatRequest{Question: "q", ConfigID: "00000000-0000-0000-0000-000000000001"})
	var rerr *Error
	if !errors.As(err, &rerr) || rerr.Code != ErrCodeNotFound {
		t.Fatalf("want not_found, got %v", err)
	}
	if rerr.TraceID != "" {
		t.Fatal("pre-execution rejection must not carry a trace id")
	}
	if len(traces.records) != 0 {
		t.Fatal("no trace must be persisted for unknown config")
	}
}

func TestAskCapabilityUnavailable(t *testing.T) {
	traces := &fakeTraces{}
	p := newPipeline(okEmbedder(1), &fakeRetriever{}, okGenerator("x [1]"), traces)

	// Hybrid executes normally since RB-17 (same pipeline, retrieval mode
	// actually selects the hybrid branch inside the retriever).
	hybridConfig := testConfig
	hybridConfig.RetrievalMode = "hybrid"
	p.Configs = &fakeConfigs{config: hybridConfig}
	_, err := p.Ask(context.Background(), ChatRequest{Question: "q", ConfigID: hybridConfig.ID})
	var herr *Error
	if !errors.As(err, &herr) || herr.Code == ErrCodeCapabilityUnavailable {
		t.Fatalf("hybrid must not be a capability rejection; got %v", err)
	}

	// A test pipeline without the optional reranker integration rejects the
	// request before any provider call and produces no trace.
	rerankConfig := testConfig
	rerankConfig.RerankEnabled = true
	p.Configs = &fakeConfigs{config: rerankConfig}
	tracesBefore := len(traces.records)
	_, err = p.Ask(context.Background(), ChatRequest{Question: "q", ConfigID: rerankConfig.ID})
	var rerr *Error
	if !errors.As(err, &rerr) || rerr.Code != ErrCodeCapabilityUnavailable {
		t.Fatalf("want capability_unavailable, got %v", err)
	}
	if len(traces.records) != tracesBefore {
		t.Fatal("capability rejections must not produce traces")
	}
}

func TestAskEmbeddingFailureClassified(t *testing.T) {
	traces := &fakeTraces{}
	p := newPipeline(&fakeEmbedder{err: &providers.ProviderError{Code: providers.ErrCodeEmbeddingFailed,
		Message: "OpenAI embeddings returned HTTP 503"}},
		&fakeRetriever{}, okGenerator("x"), traces)

	_, err := p.Ask(context.Background(), ChatRequest{Question: "q", ConfigID: testConfig.ID})
	var rerr *Error
	if !errors.As(err, &rerr) || rerr.Code != ErrCodeEmbeddingFailed {
		t.Fatalf("want embedding_failed, got %v", err)
	}
	if rerr.TraceID == "" {
		t.Fatal("classified failure must carry its trace id")
	}
	if len(traces.records) != 1 {
		t.Fatalf("failed requests must have correlated traces, got %d", len(traces.records))
	}
	record := traces.records[0]
	if record.Success || record.ErrorCode != ErrCodeEmbeddingFailed {
		t.Fatalf("record success=%v code=%q", record.Success, record.ErrorCode)
	}
	if got := spanNames(record); len(got) != 2 || got[0] != SpanQueryEmbedding || got[1] != SpanRequest {
		t.Fatalf("spans = %v, want query_embedding + request", got)
	}
}

func TestAskRetrievalEmptyOutcome(t *testing.T) {
	traces := &fakeTraces{}
	generator := okGenerator("should never be called")
	p := newPipeline(okEmbedder(1),
		&fakeRetriever{err: &Error{Code: ErrCodeRetrievalEmpty, Message: "retrieval returned no usable evidence"}},
		generator, traces)

	_, err := p.Ask(context.Background(), ChatRequest{Question: "q", ConfigID: testConfig.ID})
	var rerr *Error
	if !errors.As(err, &rerr) || rerr.Code != ErrCodeRetrievalEmpty {
		t.Fatalf("want retrieval_empty, got %v", err)
	}
	if generator.calls != 0 {
		t.Fatal("generation must not run when retrieval is empty")
	}
	if len(traces.records) != 1 {
		t.Fatalf("retrieval_empty must be persisted as a trace, got %d", len(traces.records))
	}
	record := traces.records[0]
	if record.ErrorCode != ErrCodeRetrievalEmpty || record.Success {
		t.Fatalf("record = %v/%q", record.Success, record.ErrorCode)
	}
	if record.Answer != nil {
		t.Fatal("retrieval_empty must not invent an answer")
	}
}

func TestAskRevisionUnavailableDistinct(t *testing.T) {
	traces := &fakeTraces{}
	p := newPipeline(okEmbedder(1),
		&fakeRetriever{err: &Error{Code: ErrCodeRevisionUnavailable, Message: "no live document has an active ready revision compatible with this configuration's embedding identity"}},
		okGenerator("x"), traces)

	_, err := p.Ask(context.Background(), ChatRequest{Question: "q", ConfigID: testConfig.ID})
	var rerr *Error
	if !errors.As(err, &rerr) || rerr.Code != ErrCodeRevisionUnavailable {
		t.Fatalf("want revision_unavailable, got %v", err)
	}
}

func TestAskModelTimeoutClassified(t *testing.T) {
	traces := &fakeTraces{}
	p := newPipeline(okEmbedder(1), &fakeRetriever{evidence: defaultEvidence()},
		&fakeGenerator{err: context.DeadlineExceeded}, traces)

	_, err := p.Ask(context.Background(), ChatRequest{Question: "q", ConfigID: testConfig.ID})
	var rerr *Error
	if !errors.As(err, &rerr) || rerr.Code != ErrCodeModelTimeout {
		t.Fatalf("want model_timeout, got %v", err)
	}
	if len(traces.records) != 1 {
		t.Fatal("classified provider failure must be traced")
	}
	record := traces.records[0]
	if got := spanNames(record); len(got) != 5 || got[3] != SpanLLMGeneration {
		t.Fatalf("spans = %v, want llm_generation as the failing stage", got)
	}
	if string(record.PromptSnapshot) == "{}" {
		t.Fatal("generation failure must retain rendered prompt snapshot")
	}
	var context []Evidence
	if err := json.Unmarshal(record.ContextSnapshot, &context); err != nil {
		t.Fatalf("failure context snapshot: %v", err)
	}
	if len(context) != len(defaultEvidence()) {
		t.Fatalf("failure context snapshot = %d chunks, want %d", len(context), len(defaultEvidence()))
	}
	if record.EmbeddingInputTokens == nil || *record.EmbeddingInputTokens != 1 {
		t.Fatalf("failure embedding tokens = %v, want 1", record.EmbeddingInputTokens)
	}
	if record.CostComponents.Available || record.CostComponents.Reason != reasonUsageUnavailable {
		t.Fatalf("failure cost components = %+v", record.CostComponents)
	}
}

func TestAskModelRateLimitedClassified(t *testing.T) {
	traces := &fakeTraces{}
	p := newPipeline(okEmbedder(1), &fakeRetriever{evidence: defaultEvidence()},
		&fakeGenerator{err: &providers.ProviderError{Code: providers.ErrCodeModelRateLimited, Message: "generation provider returned HTTP 429 rate limiting"}},
		traces)

	_, err := p.Ask(context.Background(), ChatRequest{Question: "q", ConfigID: testConfig.ID})
	var rerr *Error
	if !errors.As(err, &rerr) || rerr.Code != ErrCodeModelRateLimited {
		t.Fatalf("want model_rate_limited, got %v", err)
	}
}

func TestAskMalformedResponseClassified(t *testing.T) {
	traces := &fakeTraces{}
	p := newPipeline(okEmbedder(1), &fakeRetriever{evidence: defaultEvidence()},
		&fakeGenerator{err: &providers.ProviderError{Code: providers.ErrCodeMalformedResponse, Message: "generation response has no completion content"}},
		traces)

	_, err := p.Ask(context.Background(), ChatRequest{Question: "q", ConfigID: testConfig.ID})
	var rerr *Error
	if !errors.As(err, &rerr) || rerr.Code != ErrCodeMalformedResponse {
		t.Fatalf("want malformed_response, got %v", err)
	}
}

func TestAskCitationMissingVisible(t *testing.T) {
	traces := &fakeTraces{}
	gen := okGenerator("An answer that cites nothing.")
	p := newPipeline(okEmbedder(1), &fakeRetriever{evidence: defaultEvidence()}, gen, traces)

	_, err := p.Ask(context.Background(), ChatRequest{Question: "q", ConfigID: testConfig.ID})
	var rerr *Error
	if !errors.As(err, &rerr) || rerr.Code != ErrCodeCitationMissing {
		t.Fatalf("want citation_missing, got %v", err)
	}
	if len(traces.records) != 1 {
		t.Fatal("citation failures must be traced")
	}
	record := traces.records[0]
	if record.ErrorCode != ErrCodeCitationMissing || record.Answer != nil {
		t.Fatalf("record success=%v answer=%v", record.Success, record.Answer)
	}
	if string(record.PromptSnapshot) == "{}" {
		t.Fatal("citation failure must retain the rendered prompt")
	}
	var context []Evidence
	if err := json.Unmarshal(record.ContextSnapshot, &context); err != nil {
		t.Fatalf("citation failure context snapshot: %v", err)
	}
	if len(context) != len(defaultEvidence()) {
		t.Fatalf("citation failure context chunks = %d, want %d", len(context), len(defaultEvidence()))
	}
	if record.Cost == nil || !record.CostComponents.Available {
		t.Fatalf("citation failure should preserve known usage cost: %+v", record.CostComponents)
	}
	// The raw uncited output stays diagnosable in the citation span.
	var meta string
	for _, span := range record.Spans {
		if span.Name == SpanCitationMapping {
			raw, _ := json.Marshal(span.Metadata["raw_answer"])
			meta = string(raw)
		}
	}
	if !strings.Contains(meta, "cites nothing") {
		t.Fatalf("raw answer not captured in span metadata: %s", meta)
	}
}

func TestAskCitationInvalidOutsideContext(t *testing.T) {
	traces := &fakeTraces{}
	p := newPipeline(okEmbedder(1), &fakeRetriever{evidence: defaultEvidence()},
		okGenerator("Answer citing outside evidence [4]."), traces)

	_, err := p.Ask(context.Background(), ChatRequest{Question: "q", ConfigID: testConfig.ID})
	var rerr *Error
	if !errors.As(err, &rerr) || rerr.Code != ErrCodeCitationInvalid {
		t.Fatalf("want citation_invalid, got %v", err)
	}
}

func TestAskInsufficientEvidencePassthrough(t *testing.T) {
	traces := &fakeTraces{}
	p := newPipeline(okEmbedder(1), &fakeRetriever{evidence: defaultEvidence()},
		okGenerator("INSUFFICIENT_EVIDENCE"), traces)

	resp, err := p.Ask(context.Background(), ChatRequest{Question: "q", ConfigID: testConfig.ID})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Answer != "INSUFFICIENT_EVIDENCE" {
		t.Fatalf("answer = %q", resp.Answer)
	}
	if len(resp.Citations) != 0 {
		t.Fatalf("citations = %v, want empty", resp.Citations)
	}
	if len(traces.records) != 1 || !traces.records[0].Success {
		t.Fatal("insufficient evidence is a successful, persisted outcome")
	}
}

func TestAskTracePersistenceFailureNotTraceableSuccess(t *testing.T) {
	traces := &fakeTraces{err: errors.New("connection refused")}
	p := newPipeline(okEmbedder(1), &fakeRetriever{evidence: defaultEvidence()},
		okGenerator("Answer [1]."), traces)

	_, err := p.Ask(context.Background(), ChatRequest{Question: "q", ConfigID: testConfig.ID})
	var rerr *Error
	if !errors.As(err, &rerr) || rerr.Code != ErrCodePersistenceFailed {
		t.Fatalf("want persistence_failed, got %v", err)
	}
	if rerr.TraceID == "" {
		t.Fatal("persistence failure must retain the trace id for log correlation")
	}
}

func TestPersistTraceSurvivesCanceledRequestContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := &Pipeline{Traces: contextCheckingTrace{}}
	if err := p.persistTrace(ctx, TraceRecord{}); err != nil {
		t.Fatalf("persistTrace returned canceled context: %v", err)
	}
}

func TestAskContextBudgetTruncates(t *testing.T) {
	traces := &fakeTraces{}
	big := defaultEvidence()
	for i := range big {
		big[i].Content = strings.Repeat(fmt.Sprintf("chunk%d-", i), 2000) // 14000 chars each
	}
	gen := okGenerator("Answer [1].")
	p := newPipeline(okEmbedder(1), &fakeRetriever{evidence: big, ms: 1}, gen, traces)

	_, err := p.Ask(context.Background(), ChatRequest{Question: "q", ConfigID: testConfig.ID})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Budget 24000 chars: six 5000-char chunks fit, the seventh would exceed it.
	selected := SelectContext(big, ContextBudgetChars)
	if len(selected) >= len(big) {
		t.Fatalf("budget did not truncate: %d of %d", len(selected), len(big))
	}
	if !strings.Contains(gen.prompt, "[1] chunk0-") {
		t.Fatal("prompt must start with rank-1 evidence")
	}
	if strings.Contains(gen.prompt, "[2] chunk1-") {
		t.Fatal("prompt must not contain chunks beyond the budget")
	}
}
