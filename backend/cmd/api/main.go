package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"ragbench-my/backend/internal/config"
	"ragbench-my/backend/internal/documentintelligence"
	"ragbench-my/backend/internal/documents"
	"ragbench-my/backend/internal/evaldata"
	"ragbench-my/backend/internal/evalrun"
	"ragbench-my/backend/internal/experiment"
	"ragbench-my/backend/internal/health"
	"ragbench-my/backend/internal/httpapi"
	"ragbench-my/backend/internal/metrics"
	"ragbench-my/backend/internal/providers"
	"ragbench-my/backend/internal/rag"
	"ragbench-my/backend/internal/ragconfig"
	"ragbench-my/backend/internal/regression"
	"ragbench-my/backend/internal/schema"
	"ragbench-my/backend/internal/trace"

	"github.com/jackc/pgx/v5/pgxpool"
)

const shutdownTimeout = 10 * time.Second

// dispatchCreateTimeout bounds one experiment combination's launch step in
// the orchestrator (run creation + Airflow dispatch).
const dispatchCreateTimeout = 20 * time.Second

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	if err := run(logger); err != nil {
		logger.Error("api server exited with failure", slog.String("error", err.Error()))
		os.Exit(1)
	}
	logger.Info("api server stopped")
}

func run(logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("set up database pool from DATABASE_URL (check the postgres service is healthy and credentials match): %w", err)
	}
	defer pool.Close()

	logDatabaseState(ctx, logger, pool)

	if err := os.MkdirAll(cfg.UploadDir, 0750); err != nil {
		return fmt.Errorf("prepare upload directory: %w", err)
	}
	configStore := ragconfig.NewStore(pool)
	traceStore := trace.NewStore(pool)
	providerRouter := &providers.Router{
		Embedders: map[string]providers.Embedder{
			"openai":      providers.NewOpenAICompatible("openai", cfg.OpenAIBaseURL, cfg.OpenAIEmbeddingAPIKey),
			"huggingface": providers.NewHuggingFace(cfg.HuggingFaceEmbeddingBaseURL, cfg.HuggingFaceAPIKey),
		},
		Generators: map[string]providers.Generator{
			"openai":           providers.NewOpenAICompatible("openai", cfg.OpenAIBaseURL, cfg.OpenAIGenerationAPIKey),
			"opencode-go":      providers.NewOpenAICompatible("opencode-go", cfg.OpenAICompatibleBaseURL, cfg.OpenAICompatibleAPIKey),
			"opencode-zen":     providers.NewOpenAICompatible("opencode-zen", cfg.OpenCodeZenBaseURL, cfg.OpenAICompatibleAPIKey),
			"huggingface-chat": providers.NewOpenAICompatible("huggingface-chat", cfg.HuggingFaceGenerationBaseURL, cfg.HuggingFaceAPIKey),
		},
		Rerankers: map[string]providers.Reranker{
			"local": providers.NewLexicalReranker(),
		},
	}
	chatPipeline := &rag.Pipeline{
		Configs:   configStore,
		Retriever: rag.NewRetriever(pool),
		Embedder:  providerRouter,
		Generator: providerRouter,
		Reranker:  providerRouter,
		Traces:    traceStore,
	}
	datasetStore := evaldata.NewStore(pool)
	runStore := evalrun.NewStore(pool)
	metricsStore := metrics.NewStore(pool)
	regressionStore := regression.NewStore(pool)
	experimentStore := experiment.NewStore(pool)
	analysisStore := documentintelligence.NewStore(pool)
	analysisRunner := &documentintelligence.Runner{Store: analysisStore, Generator: providerRouter}
	orchestrator := &experiment.Orchestrator{
		Store:         experimentStore,
		Configs:       configStore,
		ConfigCreator: configStore,
		Datasets:      datasetStore,
		Runner:        experimentRunner{runs: runStore, datasets: datasetStore, configs: configStore, dispatcher: newAirflowDispatch(cfg)},
		Runs:          runStore,
		IndexPlanner:  documents.NewIndexPlanner(pool),
		IndexDispatch: documents.NewIndexDispatcher(cfg.AirflowURL, cfg.AirflowUsername, cfg.AirflowPassword, documents.NewStore(pool)),
	}
	handler := httpapi.NewFullAPI(logger,
		configStore,
		httpapi.DocumentOptions{Store: documents.NewStore(pool), Dispatcher: documents.NewAirflow(cfg.AirflowURL, cfg.AirflowUsername, cfg.AirflowPassword), UploadDir: cfg.UploadDir, MaxBytes: cfg.MaxUploadBytes,
			AnalysisStore: analysisStore, AnalysisDispatcher: documentintelligence.NewAirflow(cfg.AirflowURL, cfg.AirflowUsername, cfg.AirflowPassword), AnalysisRunner: analysisRunner},
		chatPipeline,
		traceStore,
		httpapi.EvalOptions{
			Datasets:     datasetStore,
			Runs:         runStore,
			Experiments:  experimentStore,
			Orchestrator: orchestrator,
			Dispatcher:   evalrun.NewAirflowDispatcher(cfg.AirflowURL, cfg.AirflowUsername, cfg.AirflowPassword),
			Executor: &evalrun.Executor{
				Store: runStore, Datasets: datasetStore,
				Pipeline: chatPipeline, Judger: evalrun.RubricJudge{Generator: providerRouter},
			},
			Metrics:     metricsStore,
			Regressions: regressionStore,
		},
		health.NamedCheck{Name: "database", Check: pool.Ping},
		health.NamedCheck{Name: "schema", Check: func(ctx context.Context) error {
			return schema.Check(ctx, pool)
		}},
	)

	listener, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return fmt.Errorf("listen on %s (is the port already in use?): %w", cfg.Addr, err)
	}

	server := &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		// Leave room for the sequential embedding and generation deadlines,
		// trace persistence, and the structured response.
		WriteTimeout: 3 * rag.GenerationTimeout,
		IdleTimeout:  120 * time.Second,
	}

	serveErr := make(chan error, 1)
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
	}()

	logger.Info("api server listening",
		slog.String("addr", cfg.Addr),
		slog.String("service", httpapi.ServiceName),
	)

	select {
	case err := <-serveErr:
		return fmt.Errorf("http server failed: %w", err)
	case <-ctx.Done():
		logger.Info("shutdown signal received, draining in-flight requests")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown within %s: %w", shutdownTimeout, err)
	}
	return nil
}

func logDatabaseState(ctx context.Context, logger *slog.Logger, pool *pgxpool.Pool) {
	pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	if err := pool.Ping(pingCtx); err != nil {
		logger.Warn("application database not reachable at startup",
			slog.String("dependency", "postgres"),
			slog.String("error", err.Error()),
			slog.String("impact", "/readyz returns 503 until the database recovers"),
		)
		return
	}
	logger.Info("application database reachable", slog.String("dependency", "postgres"))

	// Surface schema state at startup without creating or altering anything:
	// migrations are applied out of band via scripts/migrate.sh.
	schemaCtx, schemaCancel := context.WithTimeout(ctx, 3*time.Second)
	defer schemaCancel()
	if err := schema.Check(schemaCtx, pool); err != nil {
		logger.Warn("application schema is not ready for this binary",
			slog.String("dependency", "postgres"),
			slog.String("error", err.Error()),
			slog.String("impact", "/readyz reports the schema check as failed until migrations are applied"),
			slog.String("remediation", "run sh scripts/migrate.sh"),
		)
		return
	}
	logger.Info("application schema at expected migration version",
		slog.Int("required_version", schema.RequiredVersion),
	)
}

// newExperimentRunner adapts evalrun create/dispatch into the experiment
// orchestrator's RunLauncher contract so a sweep's combination launches its
// pinned durable eval run (RB-14 semantics), visible on partial failures.
type experimentRunner struct {
	runs       *evalrun.Store
	datasets   *evaldata.Store
	configs    *ragconfig.Store
	dispatcher evalrun.Dispatcher
}

func (r experimentRunner) configReader() ragconfig.Store { return *r.configs }

func (r experimentRunner) LaunchRun(parent context.Context, cfg ragconfig.Config, datasetID string, datasetVersion int, rubricVersion string, scoringK int) (evalrun.Run, error) {
	ctx, cancel := context.WithTimeout(parent, dispatchCreateTimeout)
	defer cancel()

	run, err := r.runs.CreateRun(ctx, r.datasets, r.configs, evalrun.CreateRequest{
		DatasetID: datasetID, DatasetVersion: datasetVersion, RagConfigID: cfg.ID,
		RubricVersion: rubricVersion, ScoringK: scoringK,
	})
	if err != nil {
		return evalrun.Run{}, err
	}
	if err := r.runs.MarkRunning(ctx, run.ID); err != nil {
		return evalrun.Run{}, err
	}
	dagRun := evalrun.EvalDagRunID(run.ID)
	dispatchErr := r.dispatcher.DispatchRun(ctx, run.ID)
	if err := r.runs.RecordDispatch(ctx, run.ID, dagRun, dispatchErr); err != nil {
		return evalrun.Run{}, err
	}
	combined, getErr := r.runs.GetRun(ctx, run.ID)
	if getErr != nil {
		return evalrun.Run{}, getErr
	}
	if combined.Status == evalrun.StatusDispatchFailed {
		return combined, fmt.Errorf("%v: %s", evalrun.ErrDispatchFailed, combined.DispatchError)
	}
	return combined, nil
}

func newAirflowDispatch(cfg config.Config) evalrun.Dispatcher {
	return evalrun.NewAirflowDispatcher(cfg.AirflowURL, cfg.AirflowUsername, cfg.AirflowPassword)
}
