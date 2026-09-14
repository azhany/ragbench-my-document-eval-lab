package httpapi

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
	"time"

	"ragbench-my/backend/internal/ragconfig"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type fakeStore struct {
	createErr error
	create    func(context.Context, ragconfig.CreateRequest) (ragconfig.Config, error)
	list      func(context.Context) ([]ragconfig.Config, error)
	get       func(context.Context, string) (ragconfig.Config, error)
}

func (f *fakeStore) Create(ctx context.Context, req ragconfig.CreateRequest) (ragconfig.Config, error) {
	if f.create != nil {
		return f.create(ctx, req)
	}
	return ragconfig.Config{}, f.createErr
}

func (f *fakeStore) List(ctx context.Context) ([]ragconfig.Config, error) {
	return f.list(ctx)
}

func (f *fakeStore) Get(ctx context.Context, id string) (ragconfig.Config, error) {
	return f.get(ctx, id)
}

func sampleConfig() ragconfig.Config {
	return ragconfig.Config{
		ID:                      "0b6c8cb1-6a7d-4d3f-9f6a-52f0a1b2c3d4",
		Name:                    "baseline-v1",
		ChunkSize:               500,
		ChunkOverlap:            80,
		RetrievalMode:           "vector",
		TopK:                    5,
		RerankEnabled:           false,
		PromptVersion:           "v1",
		ModelProfile:            "openai-gpt-4o-mini",
		EmbeddingProfile:        "openai-text-embedding-3-small",
		EmbeddingProvider:       "openai",
		EmbeddingModel:          "text-embedding-3-small",
		EmbeddingDimensions:     1536,
		CreatedAt:               time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC),
		UnavailableCapabilities: []string{},
	}
}

func newTestServer(store ConfigStore) http.Handler {
	return New(testLogger(), store)
}

func postJSON(t *testing.T, handler http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response body is not JSON: %v (%s)", err, rec.Body.String())
	}
	return body
}

func TestCreateConfigReturns201WithLocation(t *testing.T) {
	var created ragconfig.CreateRequest
	store := &fakeStore{
		create: func(_ context.Context, req ragconfig.CreateRequest) (ragconfig.Config, error) {
			created = req
			return sampleConfig(), nil
		},
	}
	rec := postJSON(t, newTestServer(store), "/api/v1/rag-configs", `{
		"name": "baseline-v1",
		"chunk_size": 500,
		"chunk_overlap": 80,
		"retrieval_mode": "vector",
		"top_k": 5,
		"rerank_enabled": false,
		"prompt_version": "v1",
		"model_profile": "openai-gpt-4o-mini",
		"embedding_profile": "openai-text-embedding-3-small"
	}`)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/api/v1/rag-configs/"+sampleConfig().ID {
		t.Fatalf("Location = %q, want the new config path", loc)
	}
	if created.Name != "baseline-v1" || created.TopK != 5 {
		t.Fatalf("store did not receive decoded request: %+v", created)
	}
	body := decodeBody(t, rec)
	if body["name"] != "baseline-v1" {
		t.Fatalf("response body missing config fields: %v", body)
	}
}

func TestCreateConfigRejectsUnknownFields(t *testing.T) {
	store := &fakeStore{
		create: func(context.Context, ragconfig.CreateRequest) (ragconfig.Config, error) {
			t.Fatal("store must not be called for an invalid body")
			return ragconfig.Config{}, nil
		},
	}
	rec := postJSON(t, newTestServer(store), "/api/v1/rag-configs",
		`{"name":"x","api_key":"secret"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	body := decodeBody(t, rec)
	if body["error"].(map[string]any)["code"] != "invalid_body" {
		t.Fatalf("error code = %v, want invalid_body", body)
	}
}

func TestCreateConfigMapsValidationErrorsTo400WithFields(t *testing.T) {
	store := &fakeStore{
		createErr: ragconfig.ValidationErrors{
			{Field: "chunk_overlap", Message: "chunk_overlap must be smaller than chunk_size"},
			{Field: "top_k", Message: "top_k must be between 1 and 100"},
		},
	}
	rec := postJSON(t, newTestServer(store), "/api/v1/rag-configs", `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	body := decodeBody(t, rec)
	errObj := body["error"].(map[string]any)
	if errObj["code"] != "validation_failed" {
		t.Fatalf("error code = %v, want validation_failed", errObj)
	}
	fields := errObj["fields"].([]any)
	if len(fields) != 2 {
		t.Fatalf("expected two field errors, got %v", fields)
	}
}

func TestCreateConfigMapsNameConflictTo409(t *testing.T) {
	store := &fakeStore{createErr: ragconfig.ErrNameConflict}
	rec := postJSON(t, newTestServer(store), "/api/v1/rag-configs", `{}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	body := decodeBody(t, rec)
	if body["error"].(map[string]any)["code"] != "name_conflict" {
		t.Fatalf("error code = %v, want name_conflict", body)
	}
}

func TestGetConfigMapsNotFoundTo404(t *testing.T) {
	store := &fakeStore{
		get: func(context.Context, string) (ragconfig.Config, error) {
			return ragconfig.Config{}, ragconfig.ErrNotFound
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/rag-configs/0b6c8cb1-6a7d-4d3f-9f6a-52f0a1b2c3d4", nil)
	rec := httptest.NewRecorder()
	newTestServer(store).ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestListConfigsReturns200(t *testing.T) {
	store := &fakeStore{
		list: func(context.Context) ([]ragconfig.Config, error) {
			return []ragconfig.Config{sampleConfig()}, nil
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/rag-configs", nil)
	rec := httptest.NewRecorder()
	newTestServer(store).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := decodeBody(t, rec)
	configs := body["configs"].([]any)
	if len(configs) != 1 {
		t.Fatalf("expected one config, got %v", body)
	}
}

func TestStoreFailureMapsTo500WithoutLeakingError(t *testing.T) {
	store := &fakeStore{
		list: func(context.Context) ([]ragconfig.Config, error) {
			return nil, errors.New("connection refused")
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/rag-configs", nil)
	rec := httptest.NewRecorder()
	newTestServer(store).ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	body := decodeBody(t, rec)
	errObj := body["error"].(map[string]any)
	if errObj["message"] == "connection refused" {
		t.Fatal("internal error details must not leak to the client")
	}
}
