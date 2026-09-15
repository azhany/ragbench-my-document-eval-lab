package trace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"ragbench-my/backend/internal/rag"
	"ragbench-my/backend/internal/ragconfig"
)

func testTraceStore(t *testing.T) (*Store, ragconfig.Config) {
	t.Helper()
	dsn := os.Getenv("RAGBENCH_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("RAGBENCH_TEST_DATABASE_URL not set; skipping trace store integration tests")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect to test database: %v", err)
	}
	t.Cleanup(pool.Close)

	configs := ragconfig.NewStore(pool)
	cfg, err := configs.Create(context.Background(), ragconfig.CreateRequest{
		Name:             fmt.Sprintf("trace-test-%s", uuid.NewString()[:8]),
		ChunkSize:        500,
		ChunkOverlap:     80,
		RetrievalMode:    ragconfig.RetrievalModeVector,
		TopK:             3,
		PromptVersion:    "v1",
		ModelProfile:     "openai-gpt-4o-mini",
		EmbeddingProfile: "openai-text-embedding-3-small",
	})
	if err != nil {
		t.Fatalf("create config: %v", err)
	}
	return NewStore(pool), cfg
}

func traceRecord(configID string) rag.TraceRecord {
	answer := "Answer [1]."
	input, output, embedding := 11, 4, 7
	cost := 0.000001
	started := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	return rag.TraceRecord{
		TraceID:              uuid.NewString(),
		RequestType:          rag.RequestTypeChat,
		Question:             "question",
		ConfigID:             configID,
		Success:              true,
		TotalLatencyMS:       123,
		InputTokens:          &input,
		OutputTokens:         &output,
		EmbeddingInputTokens: &embedding,
		Cost:                 &cost,
		CostCurrency:         "USD",
		PricingVersion:       "2026-01-openai",
		CostComponents: rag.CostComponents{
			Available: true, Currency: "USD", PricingVersion: "2026-01-openai", Total: &cost,
		},
		Answer:          &answer,
		Citations:       json.RawMessage(`[{"document_id":"doc","chunk_id":"chunk","snippet":"evidence"}]`),
		PromptSnapshot:  json.RawMessage(`{"prompt_version":"v1","prompt_text":"Question: question"}`),
		ContextSnapshot: json.RawMessage(`[{"chunk_id":"chunk","document_id":"doc","revision_id":"rev","rank":1,"content":"evidence"}]`),
		Spans: []rag.SpanRecord{
			{Name: rag.SpanQueryEmbedding, StartedAt: started, DurationMS: 7, Metadata: map[string]any{"prompt_tokens": 7}},
			{Name: rag.SpanRetrieval, StartedAt: started.Add(7 * time.Millisecond), DurationMS: 21, Metadata: map[string]any{"returned": 1}},
		},
	}
}

func TestInsertListGetRoundtrip(t *testing.T) {
	store, cfg := testTraceStore(t)
	ctx := context.Background()
	record := traceRecord(cfg.ID)
	if err := store.Insert(ctx, record); err != nil {
		t.Fatalf("insert trace: %v", err)
	}

	list, err := store.List(ctx, 10)
	if err != nil {
		t.Fatalf("list traces: %v", err)
	}
	var found *TraceSummary
	for i := range list {
		if list[i].TraceID == record.TraceID {
			found = &list[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("trace %s missing from list", record.TraceID)
	}
	if !found.Success || found.Question != "question" || found.RagConfigID != cfg.ID {
		t.Fatalf("summary = %+v", *found)
	}
	if found.InputTokens == nil || *found.InputTokens != 11 || found.EstimatedCost == nil {
		t.Fatalf("summary usage/cost = %+v", *found)
	}

	detail, err := store.Get(ctx, record.TraceID)
	if err != nil {
		t.Fatalf("get trace: %v", err)
	}
	if detail.TraceID != record.TraceID || !detail.Success || detail.Answer == nil || *detail.Answer != *record.Answer {
		t.Fatalf("detail identity = %+v", detail)
	}
	if len(detail.Spans) != 2 || detail.Spans[0].SpanName != rag.SpanQueryEmbedding || detail.Spans[1].SpanName != rag.SpanRetrieval {
		t.Fatalf("detail spans = %+v", detail.Spans)
	}
	if len(detail.Config) == 0 || string(detail.Config) == "null" {
		t.Fatal("detail must include effective config")
	}
	if len(detail.Prompt) == 0 || len(detail.Context) == 0 {
		t.Fatal("detail must include prompt and historical context snapshots")
	}
	var contextSnapshot []map[string]any
	if err := json.Unmarshal(detail.Context, &contextSnapshot); err != nil {
		t.Fatalf("context snapshot JSON: %v", err)
	}
	if contextSnapshot[0]["chunk_id"] != "chunk" {
		t.Fatalf("context snapshot = %+v", contextSnapshot)
	}
}

func TestGetUnknownOrMalformedReturnsNotFound(t *testing.T) {
	store, _ := testTraceStore(t)
	ctx := context.Background()
	for _, id := range []string{"not-a-uuid", uuid.NewString()} {
		_, err := store.Get(ctx, id)
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("Get(%q) = %v, want ErrNotFound", id, err)
		}
	}
}

func TestInsertRollsBackTraceWhenSpanFails(t *testing.T) {
	store, cfg := testTraceStore(t)
	ctx := context.Background()
	record := traceRecord(cfg.ID)
	record.Spans = append(record.Spans, rag.SpanRecord{Name: "not-a-real-span", StartedAt: time.Now(), DurationMS: 1})
	if err := store.Insert(ctx, record); err == nil {
		t.Fatal("expected invalid span insertion to fail")
	}
	if _, err := store.Get(ctx, record.TraceID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("failed insert left trace behind: %v", err)
	}
}

func TestListLimitRejectsNoRowsIndependently(t *testing.T) {
	store, cfg := testTraceStore(t)
	ctx := context.Background()
	for range 2 {
		record := traceRecord(cfg.ID)
		record.TraceID = uuid.NewString()
		if err := store.Insert(ctx, record); err != nil {
			t.Fatalf("insert trace: %v", err)
		}
	}
	list, err := store.List(ctx, 1)
	if err != nil {
		t.Fatalf("list with limit 1: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("list length = %d, want 1", len(list))
	}
}
