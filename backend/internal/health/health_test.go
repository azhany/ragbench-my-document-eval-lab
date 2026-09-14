package health

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestLivenessReportsOK(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)

	LivenessHandler("ragbench-api")(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d", rec.Code, http.StatusOK)
	}
	var body livenessResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Service != "ragbench-api" || body.Status != "ok" {
		t.Fatalf("body = %+v, want service=ragbench-api status=ok", body)
	}
}

func TestReadinessReportsReadyWhenAllChecksPass(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)

	ReadinessHandler(quietLogger(),
		NamedCheck{Name: "database", Check: func(context.Context) error { return nil }},
	)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d", rec.Code, http.StatusOK)
	}
	var body readinessResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Status != "ready" {
		t.Fatalf("status = %q, want %q", body.Status, "ready")
	}
	if got := body.Checks["database"].Status; got != "ok" {
		t.Fatalf("database check = %q, want %q", got, "ok")
	}
}

func TestReadinessReportsUnavailableWhenDatabaseDown(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)

	wantErr := "connection refused"
	ReadinessHandler(quietLogger(),
		NamedCheck{Name: "database", Check: func(context.Context) error {
			return errors.New(wantErr)
		}},
	)(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status code = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	var body readinessResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Status != "unavailable" {
		t.Fatalf("status = %q, want %q", body.Status, "unavailable")
	}
	dbCheck, ok := body.Checks["database"]
	if !ok {
		t.Fatalf("database check missing from response: %+v", body)
	}
	if dbCheck.Status != "error" {
		t.Fatalf("database check status = %q, want %q", dbCheck.Status, "error")
	}
	if !strings.Contains(dbCheck.Error, wantErr) {
		t.Fatalf("database check error = %q, want it to contain %q", dbCheck.Error, wantErr)
	}
}

func TestReadinessReportsEachCheckIndependently(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)

	ReadinessHandler(quietLogger(),
		NamedCheck{Name: "database", Check: func(context.Context) error { return nil }},
		NamedCheck{Name: "schema", Check: func(context.Context) error {
			return errors.New("relation \"documents\" does not exist")
		}},
	)(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status code = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	var body readinessResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got := body.Checks["database"].Status; got != "ok" {
		t.Fatalf("database check = %q, want %q (a passing check must still report ok)", got, "ok")
	}
	if got := body.Checks["schema"].Status; got != "error" {
		t.Fatalf("schema check = %q, want %q", got, "error")
	}
}
