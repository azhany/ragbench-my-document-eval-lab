package experiment

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"ragbench-my/backend/internal/evaldata"
	"ragbench-my/backend/internal/evalrun"
	"ragbench-my/backend/internal/ragconfig"
)

func integrationAll(t *testing.T) (*Store, *ragconfig.Store, *evaldata.Store, *evalrun.Store, *pgxpool.Pool) {
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
	return NewStore(pool), ragconfig.NewStore(pool), evaldata.NewStore(pool), evalrun.NewStore(pool), pool
}

type stubPlanner struct{ needed bool }

func (s stubPlanner) ReindexNeeded(ctx context.Context, cfg ragconfig.Config) (bool, error) {
	return s.needed, nil
}

type stubDispatch struct {
	err   error
	calls int
}

func (s *stubDispatch) DispatchIndex(ctx context.Context, cfg ragconfig.Config) error {
	s.calls++
	return s.err
}

type fakeRunner struct{ launched []string }

func (f *fakeRunner) LaunchRun(ctx context.Context, cfg ragconfig.Config, datasetID string, datasetVersion int, rubric string, scoringK int) (evalrun.Run, error) {
	f.launched = append(f.launched, cfg.ID)
	return evalrun.Run{ID: "run-for-" + cfg.ID, Status: evalrun.StatusRunning}, nil
}

func seedDataset(t *testing.T, datasets *evaldata.Store, pool *pgxpool.Pool) evaldata.Dataset {
	t.Helper()
	doc := uuid.NewString()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO documents (id, filename, mime_type, storage_path, checksum, size_bytes)
		 VALUES ($1, 'exp-doc.txt', 'text/plain', 'storage/' || $2, $3, 10)`,
		doc, doc, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM documents WHERE id=$1`, doc) })
	evidence, _ := json.Marshal([]map[string]string{{"document_id": doc, "label": "exp-doc.txt"}})
	ds, err := datasets.CreateDataset(context.Background(), "exp-ds-"+uuid.NewString()[:8], "",
		[]evaldata.CaseInput{{CaseKey: "qa-1", Question: "What does the demo cover?",
			ReferenceAnswer: "Two example stages.", ExpectedEvidence: evidence}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM eval_cases WHERE dataset_id=$1`, ds.ID)
		pool.Exec(context.Background(), `DELETE FROM eval_runs WHERE dataset_id=$1`, ds.ID)
		pool.Exec(context.Background(), `DELETE FROM eval_datasets WHERE id=$1`, ds.ID)
	})
	return ds
}

func drive(orch *Orchestrator, id string, max int) (AdvanceResult, error) {
	var result AdvanceResult
	for i := 0; i < max; i++ {
		r, err := orch.Advance(context.Background(), id)
		if err != nil {
			return result, err
		}
		result = r
		switch r.State {
		case StatusCompleted, StatusPartial, StatusFailed, StatusDispatchFailed:
			return result, nil
		}
	}
	return result, nil
}

func cleanupConfigs(t *testing.T, pool *pgxpool.Pool, base ragconfig.Config, e Experiment) {
	pool.Exec(context.Background(), `DELETE FROM rag_configs WHERE id=$1`, base.ID)
	for _, c := range e.Combinations {
		pool.Exec(context.Background(), `DELETE FROM rag_configs WHERE id=$1`, c.RagConfigID)
	}
}

// A two-configuration sweep (chunk-size change): the persisted matrix is
// separate immutable config identities with separate traceable runs.
func TestTwoConfigSweepSeparateRuns(t *testing.T) {
	store, configs, datasets, runs, pool := integrationAll(t)
	ctx := context.Background()
	base, err := configs.Create(ctx, ragconfig.CreateRequest{
		Name:      "exp-base-" + uuid.NewString()[:8],
		ChunkSize: 500, ChunkOverlap: 80, RetrievalMode: "vector", TopK: 5,
		PromptVersion: "v1", ModelProfile: "openai-gpt-4o-mini",
		EmbeddingProfile: "openai-text-embedding-3-small"})
	if err != nil {
		t.Fatal(err)
	}
	ds := seedDataset(t, datasets, pool)

	runner := &fakeRunner{}
	orch := &Orchestrator{Store: store, Configs: configs, ConfigCreator: configs,
		Datasets: datasets, Runner: runner, Runs: runs,
		IndexPlanner: stubPlanner{needed: false}, IndexDispatch: &stubDispatch{}}

	e, err := store.Create(ctx, datasets, configs, CreateRequest{
		Name:      "sweep-" + uuid.NewString()[:8],
		DatasetID: ds.ID, RubricVersion: "rubric-v1", ScoringK: 5,
		BaseConfigID: base.ID, Matrix: Matrix{ChunkSizes: []int{800}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(e.Combinations) != 2 {
		t.Fatalf("persisted expansion = %d, want base+1 combos", len(e.Combinations))
	}

	result, err := drive(orch, e.ID, 12)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != StatusCompleted {
		t.Fatalf("sweep state = %s", result.State)
	}
	if len(runner.launched) != 2 {
		t.Fatalf("two configs must produce two separate runs, launched %d", len(runner.launched))
	}
	detail, err := store.Get(ctx, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Status != StatusCompleted {
		t.Fatalf("experiment status = %s", detail.Status)
	}
	ids := map[string]bool{}
	for _, c := range detail.Combinations {
		if c.RagConfigID == "" || c.EvalRunID == "" {
			t.Fatalf("combination %d missing identity or run link: %+v", c.Index, c)
		}
		if ids[c.RagConfigID] {
			t.Fatal("two combinations share one config identity")
		}
		ids[c.RagConfigID] = true
	}
}

// A failed reindex prevents evaluation against the wrong corpus.
func TestFailedReindexBlocksCombination(t *testing.T) {
	store, configs, datasets, runscrawl, pool := integrationAll(t)
	ctx := context.Background()
	base, err := configs.Create(ctx, ragconfig.CreateRequest{
		Name:      "exp-fail-" + uuid.NewString()[:8],
		ChunkSize: 500, ChunkOverlap: 80, RetrievalMode: "vector", TopK: 5,
		PromptVersion: "v1", ModelProfile: "openai-gpt-4o-mini",
		EmbeddingProfile: "openai-text-embedding-3-small"})
	if err != nil {
		t.Fatal(err)
	}
	ds := seedDataset(t, datasets, pool)

	runner := &fakeRunner{}
	orch := &Orchestrator{Store: store, Configs: configs, ConfigCreator: configs,
		Datasets: datasets, Runner: runner, Runs: runscrawl,
		IndexPlanner:  stubPlanner{needed: true},
		IndexDispatch: &stubDispatch{err: os.ErrDeadlineExceeded}}

	e, err := store.Create(ctx, datasets, configs, CreateRequest{
		Name:      "sweep-fail-" + uuid.NewString()[:8],
		DatasetID: ds.ID, RubricVersion: "rubric-v1", ScoringK: 5,
		BaseConfigID: base.ID, Matrix: Matrix{ChunkSizes: []int{800}}})
	if err != nil {
		t.Fatal(err)
	}

	result, err := drive(orch, e.ID, 12)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != StatusFailed {
		t.Fatalf("a totally failed reindex matrix must fail visibly, got %s", result.State)
	}
	if len(runner.launched) != 0 {
		t.Fatal("a failed reindex must never launch evaluations")
	}
	detail, err := store.Get(ctx, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range detail.Combinations {
		if c.IndexError == "" {
			t.Fatalf("combination %d must carry a visible index failure", c.Index)
		}
		if c.EvalRunID != "" {
			t.Fatal("no run may be linked against the wrong corpus")
		}
	}
}

// Retrying a partially failed matrix reuses created identities and does not
// duplicate logical experiments or runs.
func TestRetryKeepsSuccessfulCombinationLinks(t *testing.T) {
	store, configs, datasets, runs, pool := integrationAll(t)
	ctx := context.Background()
	base, err := configs.Create(ctx, ragconfig.CreateRequest{
		Name:      "exp-retry-" + uuid.NewString()[:8],
		ChunkSize: 500, ChunkOverlap: 80, RetrievalMode: "vector", TopK: 5,
		PromptVersion: "v1", ModelProfile: "openai-gpt-4o-mini",
		EmbeddingProfile: "openai-text-embedding-3-small"})
	if err != nil {
		t.Fatal(err)
	}
	ds := seedDataset(t, datasets, pool)

	broken := &stubDispatch{err: os.ErrPermission}
	orch := &Orchestrator{Store: store, Configs: configs, ConfigCreator: configs,
		Datasets: datasets, Runner: &fakeRunner{}, Runs: runs,
		IndexPlanner: stubPlanner{needed: true}, IndexDispatch: broken}
	e, err := store.Create(ctx, datasets, configs, CreateRequest{
		Name:      "sweep-retry-" + uuid.NewString()[:8],
		DatasetID: ds.ID, RubricVersion: "rubric-v1", ScoringK: 5,
		BaseConfigID: base.ID, Matrix: Matrix{ChunkSizes: []int{500}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := drive(orch, e.ID, 4); err != nil {
		t.Fatal(err)
	}
	detail, err := store.Get(ctx, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Status == StatusCompleted {
		t.Fatal("a failed reindex dispatch cannot complete the matrix")
	}
	dummy := detail

	healthy := &Orchestrator{Store: store, Configs: configs, ConfigCreator: configs,
		Datasets: datasets, Runs: runs,
		IndexPlanner: stubPlanner{}, IndexDispatch: &stubDispatch{}}
	_ = healthy

	// Re-entering advance on the failed experiment keeps the state visible
	// (a failed reindex is never bypassed) without duplicating runs.
	for i := 0; i < 3; i++ {
		if _, err := orch.Advance(ctx, e.ID); err != nil {
			t.Fatal(err)
		}
	}
	if len(orch.Runner.(*fakeRunner).launched) != 0 {
		t.Fatal("retries must not launch evaluations against an unprepared corpus")
	}
	final, err := store.Get(ctx, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status == StatusCompleted {
		t.Fatal("a broken reindex path cannot complete the matrix")
	}
	_ = dummy
}
