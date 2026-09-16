package providers

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// RerankProfile is the immutable identity of a reranker. The first supported
// profile is deliberately local and deterministic: it is a real lexical
// reranker with no provider spend, which makes on/off comparisons reproducible
// in a local PoC while keeping the integration behind this interface.
type RerankProfile struct {
	Name     string
	Provider string
	Model    string
}

type RerankCandidate struct {
	ChunkID    string
	DocumentID string
	RevisionID string
	ChunkIndex int
	Content    string
	Distance   float64
	Rank       int
	VectorRank int
	FTSRank    int
	Score      float64
}

type RerankResult struct {
	Candidates []RerankCandidate
	// Local lexical ranking has no usage or price. Future remote profiles can
	// report these fields without changing the pipeline contract.
	InputTokens  *int
	OutputTokens *int
}

type Reranker interface {
	Rerank(context.Context, RerankProfile, string, []RerankCandidate) (RerankResult, error)
}

// LexicalReranker scores query-token overlap using a stable normalized score,
// then retains the original retrieval order and chunk identity as tie-breaks.
// It intentionally does not mutate or discard candidate evidence.
type LexicalReranker struct{}

var tokenPattern = regexp.MustCompile(`[[:alnum:]]+`)

func NewLexicalReranker() *LexicalReranker { return &LexicalReranker{} }

func (r *LexicalReranker) Rerank(_ context.Context, profile RerankProfile, question string, candidates []RerankCandidate) (RerankResult, error) {
	if profile.Name != "lexical-v1" {
		return RerankResult{}, fmt.Errorf("unsupported reranker profile %q", profile.Name)
	}
	query := tokenSet(question)
	ordered := append([]RerankCandidate(nil), candidates...)
	for i := range ordered {
		content := tokenSet(ordered[i].Content)
		if len(query) == 0 || len(content) == 0 {
			ordered[i].Score = 0
			continue
		}
		hits := 0
		for token := range query {
			if content[token] {
				hits++
			}
		}
		ordered[i].Score = float64(hits) / float64(len(query))
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Score != ordered[j].Score {
			return ordered[i].Score > ordered[j].Score
		}
		if ordered[i].Rank != ordered[j].Rank {
			return ordered[i].Rank < ordered[j].Rank
		}
		return ordered[i].ChunkID < ordered[j].ChunkID
	})
	return RerankResult{Candidates: ordered}, nil
}

func tokenSet(value string) map[string]bool {
	set := map[string]bool{}
	for _, token := range tokenPattern.FindAllString(strings.ToLower(value), -1) {
		set[token] = true
	}
	return set
}
