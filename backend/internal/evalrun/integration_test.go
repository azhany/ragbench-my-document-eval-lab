package evalrun

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"ragbench-my/backend/internal/evaldata"
	"ragbench-my/backend/internal/ragconfig"
)

func integrationStore(t *testing.T) (*Store, *evaldata.Store, *pgxpool.Pool) {
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
	return NewStore(pool), evaldata.NewStore(pool), pool
}

func TestPinInputsAndPersistVersionedScoring(t *testing.T) {
	runs, datasets, pool := integrationStore(t)
	cfgStore := ragconfig.NewStore(pool)
	ctx := context.Background()

	cfg, err := cfgStore.Create(ctx, ragconfig.CreateRequest{
		Name:      "evalrun-test-" + uuid.NewString()[:8],
		ChunkSize: 500, ChunkOverlap: 80, RetrievalMode: "vector", TopK: 5,
		PromptVersion: "v1", ModelProfile: "openai-gpt-4o-mini",
		EmbeddingProfile: "openai-text-embedding-3-small",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM rag_configs WHERE id=$1`, cfg.ID) })

	doc := seedTestDocument(t, pool)
	cases := []evaldata.CaseInput{validReference(doc)}
	ds, err := datasets.CreateDataset(ctx, "evalrun-ds-"+uuid.NewString()[:8], "", cases)
	if err != nil {
		t.Fatal(err)
	}
	cleanupDataset(t, pool, ds.ID)

	run, err := runs.CreateRun(ctx, datasets, cfgStore, CreateRequest{
		DatasetID: ds.ID, RagConfigID: cfg.ID, RubricVersion: "rubric-v1", ScoringK: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != StatusCreated || run.DatasetVersion != 1 {
		t.Fatalf("pinned inputs wrong: %+v", run)
	}
	var corpus []struct {
		RevisionID string `json:"revision_id"`
	}
	if err := json.Unmarshal(run.CorpusRevisions, &corpus); err != nil {
		t.Fatal(err)
	}
	// A failed/missing reindex still pins (null revision) rather than hiding.
	if len(corpus) != 0 || run.CorpusRevisions == nil {
		t.Fatalf("corpus snapshot must be explicit: %s", run.CorpusRevisions)
	}

	// Unknown versions rejected before dispatch.
	if _, err := runs.CreateRun(ctx, datasets, cfgStore, CreateRequest{
		DatasetID: ds.ID, DatasetVersion: 9, RagConfigID: cfg.ID,
		RubricVersion: "rubric-v1", ScoringK: 5}); err == nil {
		t.Fatal("unknown dataset version must be rejected")
	}
	if _, err := runs.CreateRun(ctx, datasets, cfgStore, CreateRequest{
		DatasetID: ds.ID, RagConfigID: cfg.ID, RubricVersion: "rubric-v9", ScoringK: 5}); err == nil {
		t.Fatal("unknown rubric version must be rejected")
	}
}

func validReference(doc string) evaldata.CaseInput {
	evidence, _ := json.Marshal([]map[string]string{{"document_id": doc, "label": "test-doc.txt"}})
	return evaldata.CaseInput{
		CaseKey: "qa-001", Question: "What does the contract cover?",
		ReferenceAnswer:  "It covers three approval stages.",
		ExpectedEvidence: evidence,
	}
}

func seedTestDocument(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	id := uuid.NewString()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO documents (id, filename, mime_type, storage_path, checksum, size_bytes)
		 VALUES ($1, 'test-doc.txt', 'text/plain', 'storage/' || $2, $3, 10)`,
		id, id, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM documents WHERE id=$1`, id) })
	return id
}

func cleanupDataset(t *testing.T, pool *pgxpool.Pool, id string) {
	t.Helper()
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM eval_cases WHERE dataset_id=$1`, id)
		pool.Exec(context.Background(), `DELETE FROM eval_runs WHERE dataset_id=$1`, id)
		pool.Exec(context.Background(), `DELETE FROM eval_datasets WHERE id=$1`, id)
	})
}
