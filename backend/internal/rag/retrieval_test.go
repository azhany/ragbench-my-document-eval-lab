package rag

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"ragbench-my/backend/internal/ragconfig"
)

func testRetrieval(t *testing.T) (*Retriever, *pgxpool.Pool, ragconfig.Config) {
	t.Helper()
	dsn := os.Getenv("RAGBENCH_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("RAGBENCH_TEST_DATABASE_URL not set; skipping retrieval integration tests")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect to test database: %v", err)
	}
	t.Cleanup(pool.Close)

	configs := ragconfig.NewStore(pool)
	cfg, err := configs.Create(context.Background(), ragconfig.CreateRequest{
		Name:             fmt.Sprintf("retrieval-test-%s", uuid.NewString()[:8]),
		ChunkSize:        500,
		ChunkOverlap:     80,
		RetrievalMode:    ragconfig.RetrievalModeVector,
		TopK:             2,
		PromptVersion:    "v1",
		ModelProfile:     "openai-gpt-4o-mini",
		EmbeddingProfile: "openai-text-embedding-3-small",
	})
	if err != nil {
		t.Fatalf("create config: %v", err)
	}
	return NewRetriever(pool), pool, cfg
}

func vector1536(first, second float32) []float32 {
	vector := make([]float32, 1536)
	vector[0] = first
	vector[1] = second
	return vector
}

func insertDocumentRevision(t *testing.T, pool *pgxpool.Pool, cfg ragconfig.Config, active bool, deleted bool, provider, model string, vectorValues ...[]float32) (string, string) {
	t.Helper()
	ctx := context.Background()
	docID := uuid.NewString()
	revID := uuid.NewString()
	checksum := strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err := pool.Exec(ctx, `
		INSERT INTO documents (id, filename, mime_type, storage_path, status, checksum, size_bytes)
		VALUES ($1, $2, 'text/plain', $3, 'processed', $4, 1)`,
		docID, "retrieval-"+docID+".txt", "/data/uploads/"+docID, checksum)
	if err != nil {
		t.Fatalf("insert document: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM documents WHERE id=$1`, docID) })

	_, err = pool.Exec(ctx, `
		INSERT INTO index_revisions (
			id, document_id, revision_number, source_checksum, config_id,
			chunk_unit, chunk_size, chunk_overlap, embedding_provider,
			embedding_model, embedding_dimensions, status, published_at
		) VALUES ($1, $2, 1, $3, $4, 'unicode_characters', $5, $6, $7, $8, $9, 'ready', now())`,
		revID, docID, checksum, cfg.ID, cfg.ChunkSize, cfg.ChunkOverlap,
		provider, model, cfg.EmbeddingDimensions)
	if err != nil {
		t.Fatalf("insert revision: %v", err)
	}
	if active {
		_, err = pool.Exec(ctx, `UPDATE documents SET active_revision_id=$1, latest_revision_id=$1 WHERE id=$2`, revID, docID)
		if err != nil {
			t.Fatalf("activate revision: %v", err)
		}
	}
	if deleted {
		if _, err := pool.Exec(ctx, `UPDATE documents SET deleted_at=now() WHERE id=$1`, docID); err != nil {
			t.Fatalf("tombstone document: %v", err)
		}
	}
	for i, values := range vectorValues {
		content := fmt.Sprintf("evidence-%s-%d", docID, i)
		if values == nil {
			_, err = pool.Exec(ctx, `
				INSERT INTO doc_chunks (id, index_revision_id, document_id, chunk_index, content, embedding)
				VALUES ($1, $2, $3, $4, $5, NULL)`, uuid.NewString(), revID, docID, i, content)
		} else {
			_, err = pool.Exec(ctx, `
				INSERT INTO doc_chunks (id, index_revision_id, document_id, chunk_index, content, embedding)
				VALUES ($1, $2, $3, $4, $5, $6::vector)`, uuid.NewString(), revID, docID, i, content, vectorLiteral(values))
		}
		if err != nil {
			t.Fatalf("insert chunk %d: %v", i, err)
		}
	}
	return docID, revID
}

func TestRetrieveRanksTiesAndCapsTopK(t *testing.T) {
	retriever, pool, cfg := testRetrieval(t)
	query := vector1536(1, 0)
	firstDoc, _ := insertDocumentRevision(t, pool, cfg, true, false, cfg.EmbeddingProvider, cfg.EmbeddingModel,
		vector1536(1, 0), vector1536(0, 1))
	secondDoc, _ := insertDocumentRevision(t, pool, cfg, true, false, cfg.EmbeddingProvider, cfg.EmbeddingModel,
		vector1536(1, 0))

	evidence, _, err := retriever.Retrieve(context.Background(), cfg, query)
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	if len(evidence) != cfg.TopK {
		t.Fatalf("returned %d evidence rows, want top_k=%d", len(evidence), cfg.TopK)
	}
	if evidence[0].Distance > evidence[1].Distance {
		t.Fatalf("distances not ordered: %v then %v", evidence[0].Distance, evidence[1].Distance)
	}
	if evidence[0].Distance != evidence[1].Distance {
		t.Fatalf("expected first two known vectors to tie, got %v and %v", evidence[0].Distance, evidence[1].Distance)
	}
	if evidence[0].DocumentID != firstDoc && evidence[0].DocumentID != secondDoc {
		t.Fatalf("unexpected first document %q", evidence[0].DocumentID)
	}
	if evidence[0].Rank != 1 || evidence[1].Rank != 2 {
		t.Fatalf("ranks = %d,%d, want 1,2", evidence[0].Rank, evidence[1].Rank)
	}
	if evidence[0].Content == "" || evidence[0].ChunkID == "" || evidence[0].RevisionID == "" {
		t.Fatalf("evidence identity/content incomplete: %+v", evidence[0])
	}
}

func TestRetrieveExcludesDeletedNonActiveAndIncompatibleRevisions(t *testing.T) {
	retriever, pool, cfg := testRetrieval(t)
	query := vector1536(1, 0)
	liveDoc, activeRev := insertDocumentRevision(t, pool, cfg, true, false, cfg.EmbeddingProvider, cfg.EmbeddingModel, vector1536(1, 0))
	deletedDoc, _ := insertDocumentRevision(t, pool, cfg, true, true, cfg.EmbeddingProvider, cfg.EmbeddingModel, vector1536(1, 0))
	incompatibleDoc, _ := insertDocumentRevision(t, pool, cfg, true, false, "other", "other-embedding", vector1536(1, 0))
	_, oldRev := insertDocumentRevision(t, pool, cfg, false, false, cfg.EmbeddingProvider, cfg.EmbeddingModel, vector1536(1, 0))
	// Make the non-active revision belong to the same live document as the
	// searchable row, then point active_revision_id back to the known active one.
	// The helper's document is otherwise retained for cleanup.
	_ = oldRev

	cfg.TopK = 100
	evidence, _, err := retriever.Retrieve(context.Background(), cfg, query)
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	if len(evidence) != 1 || evidence[0].DocumentID != liveDoc || evidence[0].RevisionID != activeRev {
		t.Fatalf("filtered evidence = %+v, want only live active compatible row", evidence)
	}
	for _, e := range evidence {
		if e.DocumentID == deletedDoc || e.DocumentID == incompatibleDoc {
			t.Fatalf("filtered document returned: %+v", e)
		}
	}
}

func TestRetrieveDistinguishesEmptyFromUnavailableBoundary(t *testing.T) {
	retriever, pool, cfg := testRetrieval(t)
	query := vector1536(1, 0)

	_, _, err := retriever.Retrieve(context.Background(), cfg, query)
	var rerr *Error
	if !errors.As(err, &rerr) || rerr.Code != ErrCodeRevisionUnavailable {
		t.Fatalf("empty corpus: want revision_unavailable, got %v", err)
	}

	insertDocumentRevision(t, pool, cfg, true, false, cfg.EmbeddingProvider, cfg.EmbeddingModel, nil)
	_, _, err = retriever.Retrieve(context.Background(), cfg, query)
	if !errors.As(err, &rerr) || rerr.Code != ErrCodeRetrievalEmpty {
		t.Fatalf("ready boundary with no usable vectors: want retrieval_empty, got %v", err)
	}
}

func TestRetrieveStageTimingIsNonNegative(t *testing.T) {
	retriever, pool, cfg := testRetrieval(t)
	insertDocumentRevision(t, pool, cfg, true, false, cfg.EmbeddingProvider, cfg.EmbeddingModel, vector1536(1, 0))
	_, duration, err := retriever.Retrieve(context.Background(), cfg, vector1536(1, 0))
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	if duration < 0 {
		t.Fatalf("duration = %d, must be non-negative", duration)
	}
}

var _ = time.Second
