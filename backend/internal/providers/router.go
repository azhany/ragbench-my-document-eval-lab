package providers

import (
	"context"
	"fmt"
)

// Router selects a concrete integration from the provider identity persisted
// in the immutable RAG configuration. It prevents one provider's credential
// or endpoint from being used accidentally for another provider's profile.
type Router struct {
	Embedders  map[string]Embedder
	Generators map[string]Generator
	Rerankers  map[string]Reranker
}

func (r *Router) Rerank(ctx context.Context, profile RerankProfile, question string, candidates []RerankCandidate) (RerankResult, error) {
	reranker := r.Rerankers[profile.Provider]
	if reranker == nil {
		return RerankResult{}, fmt.Errorf("reranking provider %q is not configured", profile.Provider)
	}
	return reranker.Rerank(ctx, profile, question, candidates)
}

func (r *Router) Embed(ctx context.Context, profile EmbeddingProfile, texts []string) (EmbeddingResult, error) {
	embedder := r.Embedders[profile.Provider]
	if embedder == nil {
		return EmbeddingResult{}, &ProviderError{Code: ErrCodeEmbeddingFailed,
			Message: fmt.Sprintf("embedding provider %q is not configured", profile.Provider)}
	}
	return embedder.Embed(ctx, profile, texts)
}

func (r *Router) Generate(ctx context.Context, profile GenerationProfile, prompt string) (GenerationResult, error) {
	generator := r.Generators[profile.Provider]
	if generator == nil {
		return GenerationResult{}, &ProviderError{Code: ErrCodeModelFailed,
			Message: fmt.Sprintf("generation provider %q is not configured", profile.Provider)}
	}
	return generator.Generate(ctx, profile, prompt)
}
