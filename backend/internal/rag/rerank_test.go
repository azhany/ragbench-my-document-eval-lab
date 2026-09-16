package rag

import (
	"context"
	"errors"
	"testing"

	"ragbench-my/backend/internal/providers"
)

type testReranker struct {
	err error
}

func (r testReranker) Rerank(_ context.Context, _ providers.RerankProfile, _ string, in []providers.RerankCandidate) (providers.RerankResult, error) {
	if r.err != nil {
		return providers.RerankResult{}, r.err
	}
	for i, j := 0, len(in)-1; i < j; i, j = i+1, j-1 {
		in[i], in[j] = in[j], in[i]
	}
	return providers.RerankResult{Candidates: in}, nil
}

func TestRerankEnabledReordersAndPersistsSpan(t *testing.T) {
	config := testConfig
	config.RerankEnabled = true
	config.RerankerProfile = "lexical-v1"
	config.RerankCandidateLimit = 3
	traces := &fakeTraces{}
	p := newPipeline(okEmbedder(1), &fakeRetriever{evidence: defaultEvidence(), ms: 5},
		okGenerator("Retention is indefinite [1]."), traces)
	p.Configs = &fakeConfigs{config: config}
	p.Reranker = testReranker{}

	resp, err := p.Ask(context.Background(), ChatRequest{Question: "retention", ConfigID: config.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Retrieved) != 3 || resp.Retrieved[0].ChunkID != "chunk-3" {
		t.Fatalf("reranked evidence = %+v", resp.Retrieved)
	}
	if len(traces.records) != 1 || len(traces.records[0].Spans) != 7 || traces.records[0].Spans[2].Name != SpanRerank {
		t.Fatalf("spans = %+v", traces.records)
	}
}

func TestRerankFailureDoesNotFallback(t *testing.T) {
	config := testConfig
	config.RerankEnabled = true
	config.RerankerProfile = "lexical-v1"
	config.RerankCandidateLimit = 3
	traces := &fakeTraces{}
	p := newPipeline(okEmbedder(1), &fakeRetriever{evidence: defaultEvidence()}, okGenerator("x [1]"), traces)
	p.Configs = &fakeConfigs{config: config}
	p.Reranker = testReranker{err: errors.New("controlled reranker failure")}

	_, err := p.Ask(context.Background(), ChatRequest{Question: "q", ConfigID: config.ID})
	var structured *Error
	if !errors.As(err, &structured) || structured.Code != ErrCodeRerankFailed {
		t.Fatalf("error = %v, want rerank_failed", err)
	}
	if len(traces.records) != 1 || traces.records[0].Success {
		t.Fatal("rerank failure must persist a failed trace")
	}
}
