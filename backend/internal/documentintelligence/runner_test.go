package documentintelligence

import (
	"testing"

	"ragbench-my/backend/internal/providers"
)

func TestProviderCostUsesReportedUsageAndPinnedRates(t *testing.T) {
	input, output := 1_000_000, 2_000_000
	cost, currency, pricing, components := providerCost(providers.GenerationProfile{
		Provider: "opencode-go", Model: "glm-5.3-flash",
	}, &input, &output)
	if cost == nil || *cost != 1.15 {
		t.Fatalf("cost = %v, want 1.15", cost)
	}
	if currency != providers.PricingCurrency || pricing != providers.PricingVersion {
		t.Fatalf("pricing identity = %q/%q", currency, pricing)
	}
	if string(components) == "{}" {
		t.Fatal("expected cost components")
	}
}

func TestProviderCostRemainsUnavailableWithoutCompleteUsageOrPricing(t *testing.T) {
	input := 10
	if cost, currency, pricing, components := providerCost(providers.GenerationProfile{
		Provider: "opencode-go", Model: "glm-5.3-flash",
	}, &input, nil); cost != nil || currency != "" || pricing != "" || string(components) == "{}" {
		t.Fatalf("partial usage should be unavailable: %v/%q/%q/%s", cost, currency, pricing, components)
	}
	output := 2
	if cost, currency, pricing, components := providerCost(providers.GenerationProfile{
		Provider: "unknown", Model: "unknown",
	}, &input, &output); cost != nil || currency != "" || pricing != "" || string(components) == "{}" {
		t.Fatalf("unknown pricing should be unavailable: %v/%q/%q/%s", cost, currency, pricing, components)
	}
}
