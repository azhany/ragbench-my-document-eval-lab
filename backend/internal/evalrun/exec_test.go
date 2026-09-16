package evalrun

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"

	"ragbench-my/backend/internal/evaldata"
	"ragbench-my/backend/internal/ragconfig"
)

// The executor runs the shared pipeline (with evaluation attribution) and
// persists idempotent, separately-scored results.
func TestExecutePersistsScoredResultsIdempotently(t *testing.T) {
	if os.Getenv("RAGBENCH_TEST_DATABASE_URL") == "" {
		t.Skip("RAGBENCH_TEST_DATABASE_URL not set")
	}
	runs, datasets, pool := integrationStore(t)
	cfgStore := ragconfig.NewStore(pool)
	ctx := context.Background()

	cfg, err := cfgStore.Create(ctx, ragconfig.CreateRequest{
		Name:      "exec-test-" + uuid.NewString()[:8],
		ChunkSize: 500, ChunkOverlap: 80, RetrievalMode: "vector", TopK: 5,
		PromptVersion: "v1", ModelProfile: "opencode-go-glm-5.3-flash",
		EmbeddingProfile: "huggingface-bge-small-en-v1.5",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM rag_configs WHERE id=$1`, cfg.ID) })

	doc := seedTestDocument(t, pool)
	ds, err := datasets.CreateDataset(ctx, "exec-ds-"+uuid.NewString()[:8], "",
		[]evaldata.CaseInput{validReference(doc)})
	if err != nil {
		t.Fatal(err)
	}
	cleanupDataset(t, pool, ds.ID)

	run, err := runs.CreateRun(ctx, datasets, cfgStore, CreateRequest{
		DatasetID: ds.ID, RagConfigID: cfg.ID, RubricVersion: "rubric-v1", ScoringK: 5})
	if err != nil {
		t.Fatal(err)
	}
	vc, err := datasets.GetVersion(ctx, ds.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	caseID := vc.Cases[0].ID

	exec := &Executor{Store: runs, Datasets: datasets,
		Pipeline: stubPipeline{out: ragResponseAlias{
			answer: "Three approval stages [1].", traceID: "t-" + uuid.NewString()[:8],
			expectedDoc: doc,
		}},
		Judger: stubJudger{}}
	result, err := exec.ExecuteCase(ctx, run.ID, caseID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "completed" {
		t.Fatalf("status = %s, want completed: %+v", result.Status, result)
	}
	if result.TraceID == "" {
		t.Fatal("result must link its trace")
	}
	if result.RecallK == nil || *result.RecallK != 1.0 {
		t.Fatalf("recall expected 1.0 against the expected document: %+v", result.RecallK)
	}
	if result.MRR == nil || *result.MRR != 1.0 {
		t.Fatalf("MRR expected 1.0, got %v", result.MRR)
	}
	if result.AnswerRelevance == nil || *result.AnswerRelevance != 4 {
		t.Fatalf("judge score must persist: %+v", result.AnswerRelevance)
	}

	// Idempotency: a retried case keeps stored state and doesn't duplicate.
	stored, err := exec.ExecuteCase(ctx, run.ID, caseID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ID != result.ID || stored.TraceID != result.TraceID {
		t.Fatalf("retry duplicated stored result: %+v vs %+v", stored, result)
	}
	after, _ := runs.GetResults(ctx, run.ID)
	if len(after) != 1 {
		t.Fatalf("results = %d rows, want exactly the one stored row", len(after))
	}

	// Aggregate run status refreshes to completed.
	final, err := runs.FinalizeStatus(ctx, run.ID, len(vc.Cases))
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != StatusCompleted {
		t.Fatalf("run status = %s, want completed", final.Status)
	}
}

func TestJudgeFailureIsEvaluatorFailed(t *testing.T) {
	if os.Getenv("RAGBENCH_TEST_DATABASE_URL") == "" {
		t.Skip("RAGBENCH_TEST_DATABASE_URL not set")
	}
	runs, datasets, pool := integrationStore(t)
	cfgStore := ragconfig.NewStore(pool)
	ctx := context.Background()

	total := 1
	_ = total
	cfg, cfgErr := cfgStore.Create(ctx, ragconfig.CreateRequest{
		Name:      "exec-test-" + uuid.NewString()[:8],
		ChunkSize: 500, ChunkOverlap: 80, RetrievalMode: "vector", TopK: 5,
		PromptVersion: "v1", ModelProfile: "opencode-go-glm-5.3-flash",
		EmbeddingProfile: "huggingface-bge-small-en-v1.5"})
	if cfgErr != nil {
		t.Fatal(cfgErr)
	}
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM rag_configs WHERE id=$1`, cfg.ID) })
	doc := seedTestDocument(t, pool)
	ds, dsErr := datasets.CreateDataset(ctx, "exec-ds-"+uuid.NewString()[:8], "",
		[]evaldata.CaseInput{validReference(doc)})
	if dsErr != nil {
		t.Fatal(dsErr)
	}
	cleanupDataset(t, pool, ds.ID)
	run, runErr := runs.CreateRun(ctx, datasets, cfgStore, CreateRequest{
		DatasetID: ds.ID, RagConfigID: cfg.ID, RubricVersion: "rubric-v1", ScoringK: 5})
	if runErr != nil {
		t.Fatal(runErr)
	}
	vc, _ := datasets.GetVersion(ctx, ds.ID, 1)
	exec := &Executor{Store: runs, Datasets: datasets,
		Pipeline: stubPipeline{out: ragResponseAlias{
			answer:      "covers three approvals [1]",
			traceID:     "t-" + uuid.NewString()[:8],
			expectedDoc: doc}},
		Judger: failingJudger{}}
	result, err := exec.ExecuteCase(ctx, run.ID, vc.Cases[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "evaluator_failed" {
		t.Fatalf("judge failures must be evaluator_failed, got %+v", result)
	}
	if result.RecallK == nil {
		t.Fatal("retrieval metrics still persist on evaluator failure")
	}
	if result.EvaluatorError == "" {
		t.Fatal("evaluator error reason must persist")
	}
	final, err := runs.FinalizeStatus(ctx, run.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != StatusPartial {
		t.Fatalf("a run with evaluator_failed cases is partial, got %s", final.Status)
	}
}
