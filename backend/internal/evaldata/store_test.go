package evaldata

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func integrationStore(t *testing.T) (*Store, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("RAGBENCH_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("RAGBENCH_TEST_DATABASE_URL not set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Close() })
	return NewStore(pool), pool
}

// seedDocument inserts one live document directly so expected-evidence
// references can be validated; the row is removed on cleanup.
func seedDocument(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	id := uuid.NewString()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO documents (id, filename, mime_type, storage_path, checksum, size_bytes)
		 VALUES ($1, 'golden-source.txt', 'text/plain', $2 || '/' || $3, $4, 10)`,
		id, "storage", id, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM documents WHERE id=$1`, id) })
	return id
}

func cleanupDataset(t *testing.T, pool *pgxpool.Pool, id string) {
	t.Helper()
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM eval_cases WHERE dataset_id=$1`, id)
		pool.Exec(context.Background(), `DELETE FROM eval_datasets WHERE id=$1`, id)
	})
}

func validCase(docID string) CaseInput {
	evidence, _ := json.Marshal([]map[string]string{
		{"document_id": docID, "label": "golden-source.txt"},
	})
	return CaseInput{
		CaseKey:          "qa-001",
		Question:         "What does the source document say?",
		ReferenceAnswer:  "A reviewed reference answer.",
		ExpectedEvidence: evidence,
	}
}

func TestCreateDatasetRejectsInvalidReferences(t *testing.T) {
	s, _ := integrationStore(t)
	ctx := context.Background()

	badEvidence, _ := json.Marshal([]map[string]string{{"document_id": uuid.NewString()}})
	_, err := s.CreateDataset(ctx, "ds-invalid-"+uuid.NewString()[:8], "", []CaseInput{{
		CaseKey: "qa-001", Question: "q", ReferenceAnswer: "a", ExpectedEvidence: badEvidence,
	}})
	if err == nil {
		t.Fatal("expected invalid reference rejection")
	}
	if !errors.Is(err, ErrDocumentUnknown) {
		t.Fatalf("expected ErrDocumentUnknown wrap, got: %v", err)
	}
}

func TestCreateDatasetRejectsEmptyAndDuplicateKeys(t *testing.T) {
	s, _ := integrationStore(t)
	ctx := context.Background()

	if _, err := s.CreateDataset(ctx, "ds-empty", "", nil); err == nil {
		t.Fatal("expected empty dataset rejection")
	}

	docID := seedDocument(t, s.pool)
	dup := []CaseInput{validCase(docID)}
	c1 := validCase(docID)
	dup = append(dup, c1)
	if _, err := s.CreateDataset(ctx, "ds-dup", "", dup); err == nil {
		t.Fatal("expected duplicate case_key rejection")
	}
}

func TestVersionedEditCreatesNewVersionKeepingOld(t *testing.T) {
	s, _ := integrationStore(t)
	ctx := context.Background()
	docID := seedDocument(t, s.pool)

	ds, err := s.CreateDataset(ctx, "ds-versioned-"+uuid.NewString()[:8], "demo", []CaseInput{validCase(docID)})
	if err != nil {
		t.Fatal(err)
	}
	cleanupDataset(t, s.pool, ds.ID)

	edited := validCase(docID)
	edited.Question = "Edited question"
	edited.CaseKey = "qa-002"
	ds2, err := s.NewVersion(ctx, ds.ID, []CaseInput{edited})
	if err != nil {
		t.Fatal(err)
	}
	if ds2.LatestVersion != 2 {
		t.Fatalf("expected version 2, got %d", ds2.LatestVersion)
	}

	v1, err := s.GetVersion(ctx, ds.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(v1.Cases) != 1 || v1.Cases[0].Question != "What does the source document say?" {
		t.Fatalf("version 1 must retain its original cases: %+v", v1.Cases)
	}
	v2, err := s.GetVersion(ctx, ds.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(v2.Cases) != 1 || v2.Cases[0].Question != "Edited question" {
		t.Fatalf("version 2 must contain the edited case: %+v", v2.Cases)
	}

	// Out-of-range versions are rejected, never silently clamped.
	if _, err := s.GetVersion(ctx, ds.ID, 3); err == nil {
		t.Fatal("expected unknown version rejection")
	}
}

func TestNameConflictRejected(t *testing.T) {
	s, _ := integrationStore(t)
	ctx := context.Background()
	docID := seedDocument(t, s.pool)
	name := "ds-conflict-" + uuid.NewString()[:8]
	if _, err := s.CreateDataset(ctx, name, "", []CaseInput{validCase(docID)}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateDataset(ctx, name, "", []CaseInput{validCase(docID)}); err != ErrNameConflict {
		t.Fatalf("expected ErrNameConflict, got %v", err)
	}
}
