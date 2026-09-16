package config

import "testing"

func TestFirstEnvUsesProviderSpecificPrecedence(t *testing.T) {
	t.Setenv("TEST_GENERIC_KEY", "generic")
	t.Setenv("TEST_SPECIFIC_KEY", "specific")
	if got := firstEnv("TEST_SPECIFIC_KEY", "TEST_GENERIC_KEY"); got != "specific" {
		t.Fatalf("firstEnv() = %q, want provider-specific key", got)
	}
	t.Setenv("TEST_SPECIFIC_KEY", "")
	if got := firstEnv("TEST_SPECIFIC_KEY", "TEST_GENERIC_KEY"); got != "generic" {
		t.Fatalf("firstEnv() fallback = %q, want generic key", got)
	}
}
