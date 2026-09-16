package rag

import (
	"testing"
)

// Cost math receives focused boundary tests per the shared definition of
// done: exact token-cost calculations, and unknown-profile/missing-usage
// behavior that must be unavailable, never falsely zero.

func TestComputeCostExactRates(t *testing.T) {
	// gpt-4o-mini: $0.15/1M input, $0.60/1M output.
	// text-embedding-3-small: $0.02/1M.
	// 1102 input, 284 output, 34 embedding tokens:
	//   embedding_query   = 34   * 0.02 / 1e6 = 0.00000068
	//   generation_input  = 1102 * 0.15 / 1e6 = 0.00016530
	//   generation_output = 284  * 0.60 / 1e6 = 0.00017040
	//   total             = 0.00033638 (stored/returned rounded to 0.000336)
	embedTokens, inputTokens, outputTokens := 34, 1102, 284
	result := computeCost("openai", "text-embedding-3-small", &embedTokens,
		"openai", "gpt-4o-mini", &inputTokens, &outputTokens)

	if !result.Components.Available {
		t.Fatalf("expected cost available, got unavailable (%s)", result.Components.Reason)
	}
	if result.Cost == nil || *result.Cost != 0.000336 {
		t.Fatalf("estimated cost = %v, want 0.000336", result.Cost)
	}
	if result.Components.Currency != "USD" {
		t.Fatalf("currency = %q, want USD", result.Components.Currency)
	}
	if result.Components.EmbeddingQuery == nil || *result.Components.EmbeddingQuery != 0.000001 {
		t.Fatalf("embedding component = %v, want 0.000001", result.Components.EmbeddingQuery)
	}
	if result.Components.GenerationInput == nil || *result.Components.GenerationInput != 0.000165 {
		t.Fatalf("generation input component = %v, want 0.000165", result.Components.GenerationInput)
	}
	if result.Components.GenerationOutput == nil || *result.Components.GenerationOutput != 0.00017 {
		t.Fatalf("generation output component = %v, want 0.00017", result.Components.GenerationOutput)
	}
	if result.Components.Total == nil || *result.Components.Total != 0.000336 {
		t.Fatalf("component total = %v, want 0.000336", result.Components.Total)
	}
}

func TestComputeCostZeroTokensIsZeroCost(t *testing.T) {
	// Zero reported tokens is a real zero, not unavailability.
	embedTokens, inputTokens, outputTokens := 0, 0, 0
	result := computeCost("openai", "text-embedding-3-small", &embedTokens,
		"openai", "gpt-4o-mini", &inputTokens, &outputTokens)
	if !result.Components.Available {
		t.Fatal("expected available cost for zero tokens")
	}
	if result.Cost == nil || *result.Cost != 0 {
		t.Fatalf("cost = %v, want exactly 0", result.Cost)
	}
}

func TestComputeCostMissingUsageUnavailable(t *testing.T) {
	inputTokens, outputTokens := 1102, 284
	result := computeCost("openai", "text-embedding-3-small", nil,
		"openai", "gpt-4o-mini", &inputTokens, &outputTokens)
	if result.Components.Available {
		t.Fatal("expected unavailable cost when embedding usage is missing")
	}
	if result.Cost != nil {
		t.Fatalf("cost must be nil when unavailable, got %v", *result.Cost)
	}
	if result.Components.Reason != "usage_unavailable" {
		t.Fatalf("reason = %q, want usage_unavailable", result.Components.Reason)
	}
	if result.Components.Total != nil {
		t.Fatal("component total must be nil when unavailable")
	}
}

func TestComputeCostUnknownGenerationModelUnavailable(t *testing.T) {
	embedTokens, inputTokens, outputTokens := 34, 1102, 284
	result := computeCost("openai", "text-embedding-3-small", &embedTokens,
		"openai", "not-in-pricing-table", &inputTokens, &outputTokens)
	if result.Components.Available {
		t.Fatal("expected unavailable cost for unpriced model")
	}
	if result.Cost != nil {
		t.Fatalf("cost must be nil, got %v", *result.Cost)
	}
	if result.Components.Reason != "pricing_unavailable" {
		t.Fatalf("reason = %q, want pricing_unavailable", result.Components.Reason)
	}
}

func TestComputeCostOpenCodeGoGenerationRate(t *testing.T) {
	embedTokens, inputTokens, outputTokens := 10, 1000, 200
	result := computeCost("openai", "text-embedding-3-small", &embedTokens,
		"opencode-go", "glm-5.3-flash", &inputTokens, &outputTokens)
	// 10*0.02/1M + 1000*0.15/1M + 200*0.50/1M = 0.0002502,
	// rounded to the persisted six decimal places.
	if result.Cost == nil || *result.Cost != 0.00025 {
		t.Fatalf("cost = %v, want 0.00025", result.Cost)
	}
}

func TestComputeCostUnknownProviderUnavailable(t *testing.T) {
	embedTokens, inputTokens, outputTokens := 34, 1102, 284
	result := computeCost("other", "some-embedder", &embedTokens,
		"openai", "gpt-4o-mini", &inputTokens, &outputTokens)
	if result.Components.Available || result.Cost != nil {
		t.Fatal("expected unavailable cost for unknown provider")
	}
}

func TestComputeCostLargeTokensDoNotOverflowRounding(t *testing.T) {
	embedTokens := 2_000_000  // $0.02/1M → 0.04
	inputTokens := 1_000_000  // $0.15/1M → 0.15
	outputTokens := 1_000_000 // $0.60/1M → 0.60
	result := computeCost("openai", "text-embedding-3-small", &embedTokens,
		"openai", "gpt-4o-mini", &inputTokens, &outputTokens)
	if result.Cost == nil || *result.Cost != 0.79 {
		t.Fatalf("cost = %v, want 0.79", result.Cost)
	}
}
