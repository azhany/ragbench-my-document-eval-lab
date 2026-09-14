package ragconfig

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The store tests exercise real PostgreSQL behavior (constraints, unique
// index, persistence). They are skipped unless RAGBENCH_TEST_DATABASE_URL
// points at a migrated database, e.g. a disposable container after running
// sh scripts/migrate.sh --container <name>.
func testStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("RAGBENCH_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("RAGBENCH_TEST_DATABASE_URL not set; skipping store integration tests")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect to test database: %v", err)
	}
	t.Cleanup(pool.Close)
	return NewStore(pool)
}

func uniqueName(prefix string) string {
	return fmt.Sprintf("%s-%s", prefix, uuid.NewString()[:8])
}

func TestCreateGetListRoundtrip(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	created, err := store.Create(ctx, CreateRequest{
		Name:             uniqueName("roundtrip"),
		ChunkSize:        500,
		ChunkOverlap:     80,
		RetrievalMode:    RetrievalModeVector,
		TopK:             5,
		RerankEnabled:    false,
		PromptVersion:    "v1",
		ModelProfile:     "openai-gpt-4o-mini",
		EmbeddingProfile: "openai-text-embedding-3-small",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == "" || created.CreatedAt.IsZero() {
		t.Fatalf("Create did not populate identity: %+v", created)
	}

	got, err := store.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != created.Name || got.ChunkSize != created.ChunkSize ||
		got.EmbeddingDimensions != 1536 || got.EmbeddingProvider != "openai" {
		t.Fatalf("Get mismatch: created %+v, got %+v", created, got)
	}

	list, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	found := false
	for _, cfg := range list {
		if cfg.ID == created.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("created config missing from List")
	}
}

func TestCreateRejectsDuplicateName(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	name := uniqueName("duplicate")
	req := CreateRequest{
		Name:             name,
		ChunkSize:        500,
		ChunkOverlap:     80,
		RetrievalMode:    RetrievalModeVector,
		TopK:             5,
		PromptVersion:    "v1",
		ModelProfile:     "openai-gpt-4o-mini",
		EmbeddingProfile: "openai-text-embedding-3-small",
	}
	if _, err := store.Create(ctx, req); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	_, err := store.Create(ctx, req)
	if !errors.Is(err, ErrNameConflict) {
		t.Fatalf("second Create = %v, want ErrNameConflict", err)
	}
}

func TestGetUnknownIDReturnsNotFound(t *testing.T) {
	store := testStore(t)
	if _, err := store.Get(context.Background(), uuid.NewString()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get(unknown uuid) = %v, want ErrNotFound", err)
	}
	if _, err := store.Get(context.Background(), "not-a-uuid"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get(malformed id) = %v, want ErrNotFound", err)
	}
}

// TestConfigurationsAreImmutableIdentities covers the RB-03 core rule:
// changing settings creates a new identity; the previous configuration keeps
// its exact settings, and a fresh store instance (as after an application
// restart) sees both unchanged.
func TestConfigurationsAreImmutableIdentities(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	first, err := store.Create(ctx, CreateRequest{
		Name:             uniqueName("immutable-first"),
		ChunkSize:        500,
		ChunkOverlap:     80,
		RetrievalMode:    RetrievalModeVector,
		TopK:             5,
		PromptVersion:    "v1",
		ModelProfile:     "openai-gpt-4o-mini",
		EmbeddingProfile: "openai-text-embedding-3-small",
	})
	if err != nil {
		t.Fatalf("Create first: %v", err)
	}

	second, err := store.Create(ctx, CreateRequest{
		Name:             uniqueName("immutable-second"),
		ChunkSize:        800,
		ChunkOverlap:     120,
		RetrievalMode:    RetrievalModeVector,
		TopK:             8,
		RerankEnabled:    true,
		PromptVersion:    "v1",
		ModelProfile:     "openai-gpt-4o-mini",
		EmbeddingProfile: "openai-text-embedding-3-small",
	})
	if err != nil {
		t.Fatalf("Create second: %v", err)
	}

	// A fresh store instance over the same database simulates an
	// application restart: both identities must survive unchanged.
	restarted := NewStore(store.pool)
	gotFirst, err := restarted.Get(ctx, first.ID)
	if err != nil {
		t.Fatalf("Get first after restart: %v", err)
	}
	gotSecond, err := restarted.Get(ctx, second.ID)
	if err != nil {
		t.Fatalf("Get second after restart: %v", err)
	}

	if gotFirst.ChunkSize != 500 || gotFirst.ChunkOverlap != 80 || gotFirst.TopK != 5 || gotFirst.RerankEnabled {
		t.Fatalf("first config changed: %+v", gotFirst)
	}
	if gotSecond.ChunkSize != 800 || gotSecond.ChunkOverlap != 120 || gotSecond.TopK != 8 || !gotSecond.RerankEnabled {
		t.Fatalf("second config changed: %+v", gotSecond)
	}
	if gotFirst.ID == gotSecond.ID {
		t.Fatal("different settings must produce different identities")
	}
	if len(gotSecond.UnavailableCapabilities) != 1 || gotSecond.UnavailableCapabilities[0] != "rerank" {
		t.Fatalf("second config should expose rerank as unavailable: %+v", gotSecond.UnavailableCapabilities)
	}
}
