package documents

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"ragbench-my/backend/internal/ragconfig"
)

func integrationStore(t *testing.T) (*Store, string) {
	t.Helper()
	dsn := os.Getenv("RAGBENCH_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("RAGBENCH_TEST_DATABASE_URL not set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := ragconfig.NewStore(pool).Create(context.Background(), ragconfig.CreateRequest{Name: "documents-test-" + uuid.NewString(), ChunkSize: 20, ChunkOverlap: 4, RetrievalMode: "vector", TopK: 5, PromptVersion: "v1", ModelProfile: "openai-gpt-4o-mini", EmbeddingProfile: "openai-text-embedding-3-small"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM rag_configs WHERE id=$1`, cfg.ID); pool.Close() })
	return NewStore(pool), cfg.ID
}
func createTestDocument(t *testing.T, s *Store, configID string) Document {
	t.Helper()
	ctx := context.Background()
	id := uuid.NewString()
	d, err := s.Create(ctx, Upload{ID: id, Filename: "source.txt", MIMEType: "text/plain", Path: "/test/" + id, Checksum: uuid.NewString(), Size: 10, ConfigID: configID})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		s.pool.Exec(ctx, `DELETE FROM ingestion_jobs WHERE index_revision_id IN(SELECT id FROM index_revisions WHERE document_id=$1)`, id)
		s.pool.Exec(ctx, `UPDATE documents SET active_revision_id=NULL,latest_revision_id=NULL WHERE id=$1`, id)
		s.pool.Exec(ctx, `DELETE FROM documents WHERE id=$1`, id)
	})
	return d
}

func TestDurableDispatchFailureAndDuplicateUpload(t *testing.T) {
	s, cfg := integrationStore(t)
	d := createTestDocument(t, s, cfg)
	ctx := context.Background()
	if d.Job == nil || d.Job.RunID != "rb_"+*d.LatestRevisionID || d.Status != "queued" {
		t.Fatalf("missing queued identity: %+v", d)
	}
	if err := s.RecordDispatch(ctx, d.Job.ID, errors.New("Airflow unavailable")); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "failed" || got.Job.State != "dispatch_failed" || *got.Job.ErrorMessage != "Airflow unavailable" {
		t.Fatalf("failure not visible: %+v", got)
	}
	if err = s.RecordDispatch(ctx, d.Job.ID, nil); err != nil {
		t.Fatal(err)
	}
	got, _ = NewStore(s.pool).Get(ctx, d.ID)
	if got.Job.RunID != d.Job.RunID || got.Job.DispatchAttempts != 2 || got.Status != "queued" {
		t.Fatalf("retry lost correlation: %+v", got)
	}
	_, err = s.Create(ctx, Upload{ID: uuid.NewString(), Filename: "copy.txt", MIMEType: "text/plain", Path: "/test/copy", Checksum: d.Checksum, ConfigID: cfg})
	if !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate = %v", err)
	}
}

func TestConcurrentReprocessAndDelete(t *testing.T) {
	s, cfg := integrationStore(t)
	d := createTestDocument(t, s, cfg)
	ctx := context.Background()
	if _, err := s.Reprocess(ctx, d.ID, cfg); !errors.Is(err, ErrActive) {
		t.Fatalf("active reprocess = %v", err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE index_revisions SET status='failed',error_code='extraction_failed' WHERE id=$1`, d.LatestRevisionID); err != nil {
		t.Fatal(err)
	}
	s.pool.Exec(ctx, `UPDATE ingestion_jobs SET state='failed' WHERE id=$1`, d.Job.ID)
	outcomes := make(chan error, 8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := s.Reprocess(ctx, d.ID, cfg); outcomes <- err }()
	}
	wg.Wait()
	close(outcomes)
	accepted := 0
	for err := range outcomes {
		if err == nil {
			accepted++
		} else if !errors.Is(err, ErrActive) {
			t.Fatal(err)
		}
	}
	if accepted != 1 {
		t.Fatalf("accepted %d simultaneous replacements", accepted)
	}
	latest, err := s.Get(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if latest.Job.DAGID != "document_reindex" || len(latest.Revisions) != 2 {
		t.Fatalf("replacement missing: %+v", latest)
	}
	for i := 0; i < 2; i++ {
		if err = s.Delete(ctx, d.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.Get(ctx, d.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted Get = %v", err)
	}
	if _, err = s.Reprocess(ctx, d.ID, cfg); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted reprocess = %v", err)
	}
	var state string
	s.pool.QueryRow(ctx, `SELECT state FROM ingestion_jobs WHERE id=$1`, latest.Job.ID).Scan(&state)
	if state != "cancelled" {
		t.Fatalf("inflight job = %s", state)
	}
	if err = s.RecordDispatch(ctx, latest.Job.ID, nil); err != nil {
		t.Fatal(err)
	}
	s.pool.QueryRow(ctx, `SELECT state FROM ingestion_jobs WHERE id=$1`, latest.Job.ID).Scan(&state)
	if state != "cancelled" {
		t.Fatal("late dispatch resurrected cancelled job")
	}
	if err = s.Delete(ctx, "bad-id"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}
