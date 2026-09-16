package httpapi

import (
	"net/http/httptest"
	"testing"
)

func TestParseMetricsFilterAcceptsLocalIANATimezone(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/v1/metrics/summary?timezone=Asia%2FKuala_Lumpur&traffic=query", nil)
	filter, err := parseMetricsFilter(req)
	if err != nil {
		t.Fatalf("parseMetricsFilter() error = %v", err)
	}
	if filter.Timezone != "Asia/Kuala_Lumpur" {
		t.Fatalf("timezone = %q, want Asia/Kuala_Lumpur", filter.Timezone)
	}
	if filter.Traffic != "query" {
		t.Fatalf("traffic = %q, want query", filter.Traffic)
	}
}

func TestParseMetricsFilterRejectsInvalidTimezone(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/v1/metrics/summary?timezone=not-a-timezone", nil)
	if _, err := parseMetricsFilter(req); err == nil {
		t.Fatal("parseMetricsFilter() accepted an invalid timezone")
	}
}
