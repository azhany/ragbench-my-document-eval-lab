package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ragbench-my/backend/internal/documentintelligence"
)

type fakeAnalysisStore struct {
	analysis    documentintelligence.Analysis
	created     documentintelligence.CreateRequest
	dispatchErr error
	dispatches  int
}

func (f *fakeAnalysisStore) Create(_ context.Context, req documentintelligence.CreateRequest) (documentintelligence.Analysis, error) {
	f.created = req
	return f.analysis, nil
}
func (f *fakeAnalysisStore) List(context.Context, string) ([]documentintelligence.Analysis, error) {
	return []documentintelligence.Analysis{f.analysis}, nil
}
func (f *fakeAnalysisStore) Get(context.Context, string) (documentintelligence.Analysis, error) {
	return f.analysis, nil
}
func (f *fakeAnalysisStore) RecordDispatch(_ context.Context, _ string, err error) error {
	f.dispatches++
	f.dispatchErr = err
	return nil
}

type fakeAnalysisDispatcher struct{ calls int }

func (f *fakeAnalysisDispatcher) Dispatch(context.Context, documentintelligence.Job) error {
	f.calls++
	return nil
}

type fakeAnalysisRunner struct{ calls int }

func (f *fakeAnalysisRunner) RunStage(_ context.Context, req documentintelligence.StageRequest) (documentintelligence.Analysis, error) {
	f.calls++
	return documentintelligence.Analysis{ID: req.AnalysisID, Stage: req.Stage, Status: "processing"}, nil
}

func testAnalysis() documentintelligence.Analysis {
	return documentintelligence.Analysis{
		ID: "analysis-1", DocumentID: "document-1", TraceID: "trace-1", Status: "queued", Stage: "dispatch",
		Job: &documentintelligence.Job{ID: "job-1", AnalysisID: "analysis-1", DAGID: "document_intelligence", RunID: "di-analysis-1", State: "dispatch_pending"},
	}
}

func TestAnalysisAPIStartsAndDispatchesDurableAnalysis(t *testing.T) {
	store := &fakeAnalysisStore{analysis: testAnalysis()}
	dispatcher := &fakeAnalysisDispatcher{}
	handler := NewDocumentAPI(slog.New(slog.NewTextHandler(io.Discard, nil)), nil,
		DocumentOptions{Store: &uploadStore{}, AnalysisStore: store, AnalysisDispatcher: dispatcher})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/documents/document-1/analyses", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", res.Code, res.Body.String())
	}
	if store.created.DocumentID != "document-1" || dispatcher.calls != 1 || store.dispatches != 1 {
		t.Fatalf("create/dispatch = %+v/%d/%d", store.created, dispatcher.calls, store.dispatches)
	}
	var body documentintelligence.Analysis
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil || body.ID != "analysis-1" {
		t.Fatalf("body = %s, err = %v", res.Body.String(), err)
	}
}

func TestAnalysisStageAPIRequiresAirflowCorrelationAndRoutesStage(t *testing.T) {
	store := &fakeAnalysisStore{analysis: testAnalysis()}
	runner := &fakeAnalysisRunner{}
	handler := NewDocumentAPI(slog.New(slog.NewTextHandler(io.Discard, nil)), nil,
		DocumentOptions{Store: &uploadStore{}, AnalysisStore: store, AnalysisRunner: runner})
	bad := httptest.NewRequest(http.MethodPost, "/api/v1/document-analyses/analysis-1/stages/extract_text", strings.NewReader(`{}`))
	bad.Header.Set("Content-Type", "application/json")
	badResult := httptest.NewRecorder()
	handler.ServeHTTP(badResult, bad)
	if badResult.Code != http.StatusBadRequest {
		t.Fatalf("missing correlation status = %d", badResult.Code)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/document-analyses/analysis-1/stages/extract_text", strings.NewReader(`{"job_id":"job-1","dag_id":"document_intelligence","run_id":"di-analysis-1"}`))
	req.Header.Set("Content-Type", "application/json")
	result := httptest.NewRecorder()
	handler.ServeHTTP(result, req)
	if result.Code != http.StatusOK || runner.calls != 1 {
		t.Fatalf("stage status = %d, calls = %d, body = %s", result.Code, runner.calls, result.Body.String())
	}
}
