package rag

import (
	"math"

	"ragbench-my/backend/internal/providers"
)

// CostUnavailableReason values explain a NULL estimated cost. Unavailable
// cost is never reported as zero.
const (
	reasonUsageUnavailable   = "usage_unavailable"
	reasonPricingUnavailable = "pricing_unavailable"
)

// Component names in the persisted cost breakdown. The query total includes
// the documented embedding and generation components only; ingestion and
// evaluator costs are tracked separately by their owning features.
const (
	ComponentEmbeddingQuery   = "embedding_query"
	ComponentGenerationInput  = "generation_input"
	ComponentGenerationOutput = "generation_output"
)

// CostComponents is the persisted cost breakdown for one query.
type CostComponents struct {
	Available bool `json:"available"`
	// Reason is set when Available is false.
	Reason string `json:"reason,omitempty"`
	// Currency and PricingVersion label the explicit rate table used.
	Currency       string `json:"currency,omitempty"`
	PricingVersion string `json:"pricing_version,omitempty"`
	// Per-component estimates in Currency, rounded to 6 decimals.
	EmbeddingQuery   *float64 `json:"embedding_query,omitempty"`
	GenerationInput  *float64 `json:"generation_input,omitempty"`
	GenerationOutput *float64 `json:"generation_output,omitempty"`
	Total            *float64 `json:"total"`
}

// costResult bundles the numeric cost with the persisted identity.
type costResult struct {
	Cost           *float64
	Currency       string
	PricingVersion string
	Components     CostComponents
}

// computeCost prices one query from explicit rates in the provider registry.
// A missing usage value or an unpriced provider/model makes the cost
// unavailable (nil, not zero); the reason is recorded so the UI can explain
// the gap.
func computeCost(embeddingProvider, embeddingModel string, embeddingTokens *int,
	generationProvider, generationModel string, inputTokens, outputTokens *int) costResult {

	unavailable := func(reason string) costResult {
		components := CostComponents{
			Available:      false,
			Reason:         reason,
			Currency:       providers.PricingCurrency,
			PricingVersion: providers.PricingVersion,
			Total:          nil,
		}
		return costResult{
			Cost:           nil,
			Currency:       "",
			PricingVersion: providers.PricingVersion,
			Components:     components,
		}
	}
	if embeddingTokens == nil || inputTokens == nil || outputTokens == nil {
		return unavailable(reasonUsageUnavailable)
	}
	if *embeddingTokens < 0 || *inputTokens < 0 || *outputTokens < 0 {
		return unavailable(reasonUsageUnavailable)
	}

	embedRate, ok := providers.RateFor(embeddingProvider, embeddingModel)
	if !ok {
		return unavailable(reasonPricingUnavailable)
	}
	genRate, ok := providers.RateFor(generationProvider, generationModel)
	if !ok {
		return unavailable(reasonPricingUnavailable)
	}

	embedCost := perMillion(*embeddingTokens, embedRate.InputPerMillion)
	inputCost := perMillion(*inputTokens, genRate.InputPerMillion)
	outputCost := perMillion(*outputTokens, genRate.OutputPerMillion)
	total := round6(embedCost + inputCost + outputCost)

	cost := total
	components := CostComponents{
		Available:        true,
		Currency:         providers.PricingCurrency,
		PricingVersion:   providers.PricingVersion,
		EmbeddingQuery:   &embedCost,
		GenerationInput:  &inputCost,
		GenerationOutput: &outputCost,
		Total:            &cost,
	}
	return costResult{Cost: &total, Currency: providers.PricingCurrency, PricingVersion: providers.PricingVersion, Components: components}
}

// perMillion prices tokens at an explicit per-million rate.
func perMillion(tokens int, rate float64) float64 {
	return round6(float64(tokens) * rate / 1_000_000)
}

func round6(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return math.Round(v*1e6) / 1e6
}
