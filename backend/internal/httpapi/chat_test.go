package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ragbench-my/backend/internal/providers"
	"ragbench-my/backend/internal/rag"
	"ragbench-my/backend/internal/ragconfig"
	"ragbench-my/backend/internal/trace"
)

type httpChatEmbedder struct {
	err error
}

func (f httpChatEmbedder) Embed(context.Context, providers.EmbeddingProfile, []string) (providers.EmbeddingResult, error) {
	if f.err != nil {
		return providers.EmbeddingResult{}, f.err
	}
	tokens := 5
	vector := make([]float32, 1536)
	vector[0] = 1
	return providers.EmbeddingResult{Vectors: [][]float32{vector}, PromptTokens: &tokens}, nil
}

type httpChatRetriever struct {
	evidence []rag.Evidence
	err      *rag.Error
}

func (f httpChatRetriever) Retrieve(context.Context, ragconfig.Config, string, []float32) ([]rag.Evidence, int64, *rag.Error) {
	if f.err != nil {
		return nil, 1, f.err
	}
	return f.evidence, 1, nil
}

type httpChatGenerator struct {
	result providers.GenerationResult
	err    error
}

func (f httpChatGenerator) Generate(context.Context, providers.GenerationProfile, string) (providers.GenerationResult, error) {
	if f.err != nil {
		return providers.GenerationResult{}, f.err
	}
	return f.result, nil
}

type httpChatTraces struct {
	records []rag.TraceRecord
	err     error
}

func (f *httpChatTraces) Insert(_ context.Context, record rag.TraceRecord) error {
	if f.err != nil {
		return f.err
	}
	f.records = append(f.records, record)
	return nil
}

type httpTraceReads struct {
	list   []trace.TraceSummary
	detail trace.TraceDetail
	err    error
}

func (f httpTraceReads) List(context.Context, int) ([]trace.TraceSummary, error) {
	return f.list, f.err
}

func (f httpTraceReads) Get(context.Context, string) (trace.TraceDetail, error) {
	return f.detail, f.err
}

func makeHTTPChatPipeline(generator providers.Generator, retriever EvidenceSourceForHTTP, traces *httpChatTraces) *rag.Pipeline {
	return &rag.Pipeline{
		Configs: &fakeStore{get: func(context.Context, string) (ragconfig.Config, error) {
			return sampleConfig(), nil
		}},
		Retriever: retriever,
		Embedder:  httpChatEmbedder{},
		Generator: generator,
		Traces:    traces,
	}
}

// Alias keeps the helper signature explicit without exposing a test-only
// implementation from the rag package.
type EvidenceSourceForHTTP interface {
	Retrieve(context.Context, ragconfig.Config, string, []float32) ([]rag.Evidence, int64, *rag.Error)
}

func httpEvidence() []rag.Evidence {
	return []rag.Evidence{{
		ChunkID: "chunk-1", DocumentID: "doc-1", RevisionID: "rev-1", ChunkIndex: 0,
		Content: "approval requires two signatures", Distance: 0.1, Rank: 1,
	}}
}

func TestChatHTTPHappyPathShape(t *testing.T) {
	input, output := 10, 4
	traces := &httpChatTraces{}
	pipeline := makeHTTPChatPipeline(httpChatGenerator{result: providers.GenerationResult{
		Text: "Approval needs two signatures [1].", InputTokens: &input, OutputTokens: &output,
	}}, httpChatRetriever{evidence: httpEvidence()}, traces)
	handler := NewFullAPI(testLogger(), &fakeStore{get: func(context.Context, string) (ragconfig.Config, error) {
		return sampleConfig(), nil
	}}, DocumentOptions{}, pipeline, httpTraceReads{}, EvalOptions{})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/chat", strings.NewReader(`{"question":"How does approval work?","config_id":"`+sampleConfig().ID+`"}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body rag.ChatResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Answer != "Approval needs two signatures [1]." || len(body.Citations) != 1 {
		t.Fatalf("response = %+v", body)
	}
	if body.Trace.TraceID == "" || body.Trace.InputTokens == nil || *body.Trace.InputTokens != input {
		t.Fatalf("trace summary = %+v", body.Trace)
	}
	if len(traces.records) != 1 || !traces.records[0].Success {
		t.Fatal("happy-path trace was not persisted")
	}
}

func TestChatHTTPInvalidBodyAndValidation(t *testing.T) {
	pipeline := makeHTTPChatPipeline(httpChatGenerator{}, httpChatRetriever{evidence: httpEvidence()}, &httpChatTraces{})
	handler := NewFullAPI(testLogger(), &fakeStore{get: func(context.Context, string) (ragconfig.Config, error) {
		return sampleConfig(), nil
	}}, DocumentOptions{}, pipeline, httpTraceReads{}, EvalOptions{})

	cases := []struct {
		name string
		body string
		want int
	}{
		{"malformed JSON", `{`, http.StatusBadRequest},
		{"unknown field", `{"question":"q","config_id":"id","extra":true}`, http.StatusBadRequest},
		{"trailing JSON", `{"question":"q","config_id":"id"}{"question":"q","config_id":"id"}`, http.StatusBadRequest},
		{"blank question", `{"question":"   ","config_id":"` + sampleConfig().ID + `"}`, http.StatusBadRequest},
		{"missing config", `{"question":"q"}`, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/chat", strings.NewReader(tc.body))
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			var body errorEnvelope
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode envelope: %v", err)
			}
			if tc.name == "malformed JSON" || tc.name == "unknown field" || tc.name == "trailing JSON" {
				if body.Error.Code != "invalid_body" {
					t.Fatalf("code = %q, want invalid_body", body.Error.Code)
				}
			} else if body.Error.Code != rag.ErrCodeValidationFailed {
				t.Fatalf("code = %q, want validation_failed", body.Error.Code)
			}
		})
	}
}

func TestChatHTTPClassifiedFailuresCarryTraceID(t *testing.T) {
	cases := []struct {
		name       string
		generator  providers.Generator
		retriever  EvidenceSourceForHTTP
		wantCode   string
		wantStatus int
	}{
		{
			name: "empty retrieval", generator: httpChatGenerator{},
			retriever: httpChatRetriever{err: &rag.Error{Code: rag.ErrCodeRetrievalEmpty, Message: "no evidence"}},
			wantCode:  rag.ErrCodeRetrievalEmpty, wantStatus: http.StatusUnprocessableEntity,
		},
		{
			name: "model timeout", generator: httpChatGenerator{err: &providers.ProviderError{Code: providers.ErrCodeModelTimeout, Message: "timeout"}},
			retriever: httpChatRetriever{evidence: httpEvidence()},
			wantCode:  rag.ErrCodeModelTimeout, wantStatus: http.StatusGatewayTimeout,
		},
		{
			name: "rate limited", generator: httpChatGenerator{err: &providers.ProviderError{Code: providers.ErrCodeModelRateLimited, Message: "rate"}},
			retriever: httpChatRetriever{evidence: httpEvidence()},
			wantCode:  rag.ErrCodeModelRateLimited, wantStatus: http.StatusBadGateway,
		},
		{
			name: "malformed response", generator: httpChatGenerator{err: &providers.ProviderError{Code: providers.ErrCodeMalformedResponse, Message: "malformed"}},
			retriever: httpChatRetriever{evidence: httpEvidence()},
			wantCode:  rag.ErrCodeMalformedResponse, wantStatus: http.StatusBadGateway,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			traces := &httpChatTraces{}
			pipeline := makeHTTPChatPipeline(tc.generator, tc.retriever, traces)
			handler := NewFullAPI(testLogger(), &fakeStore{get: func(context.Context, string) (ragconfig.Config, error) {
				return sampleConfig(), nil
			}}, DocumentOptions{}, pipeline, httpTraceReads{}, EvalOptions{})
			req := httptest.NewRequest(http.MethodPost, "/api/v1/chat", strings.NewReader(`{"question":"q","config_id":"`+sampleConfig().ID+`"}`))
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			var body errorEnvelope
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode envelope: %v", err)
			}
			if body.Error.Code != tc.wantCode || body.Error.TraceID == "" {
				t.Fatalf("error = %+v, want %s with trace id", body.Error, tc.wantCode)
			}
			if len(traces.records) != 1 || traces.records[0].ErrorCode != tc.wantCode {
				t.Fatalf("persisted records = %+v", traces.records)
			}
		})
	}
}

func TestChatHTTPTracePersistenceFailureReturns500(t *testing.T) {
	input, output := 10, 4
	traces := &httpChatTraces{err: errors.New("database unavailable")}
	pipeline := makeHTTPChatPipeline(httpChatGenerator{result: providers.GenerationResult{
		Text: "Answer [1].", InputTokens: &input, OutputTokens: &output,
	}}, httpChatRetriever{evidence: httpEvidence()}, traces)
	handler := NewFullAPI(testLogger(), &fakeStore{get: func(context.Context, string) (ragconfig.Config, error) {
		return sampleConfig(), nil
	}}, DocumentOptions{}, pipeline, httpTraceReads{}, EvalOptions{})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/chat", strings.NewReader(`{"question":"q","config_id":"`+sampleConfig().ID+`"}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body errorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if body.Error.Code != rag.ErrCodePersistenceFailed {
		t.Fatalf("code = %q, want persistence_failed", body.Error.Code)
	}
	if strings.Contains(rec.Body.String(), "Answer [1].") {
		t.Fatal("trace persistence failure must not return the answer")
	}
}

func TestTraceReadRoutes(t *testing.T) {
	traceID := "trace-1"
	reads := httpTraceReads{
		list:   []trace.TraceSummary{{TraceID: traceID, Question: "q"}},
		detail: trace.TraceDetail{TraceID: traceID, Context: json.RawMessage(`[{"chunk_id":"historical"}]`)},
	}
	pipeline := makeHTTPChatPipeline(httpChatGenerator{}, httpChatRetriever{}, &httpChatTraces{})
	handler := NewFullAPI(testLogger(), &fakeStore{get: func(context.Context, string) (ragconfig.Config, error) {
		return sampleConfig(), nil
	}}, DocumentOptions{}, pipeline, reads, EvalOptions{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/traces?limit=1", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), traceID) {
		t.Fatalf("list response = %d %s", rec.Code, rec.Body.String())
	}
	req = httptest.NewRequest(http.MethodGet, "/api/v1/traces/"+traceID, nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "historical") {
		t.Fatalf("detail response = %d %s", rec.Code, rec.Body.String())
	}
}
