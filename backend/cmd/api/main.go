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
	"ragbench-my/backend/internal/documents"
	"ragbench-my/backend/internal/health"
	"ragbench-my/backend/internal/httpapi"
	"ragbench-my/backend/internal/providers"
	"ragbench-my/backend/internal/rag"
	"ragbench-my/backend/internal/ragconfig"
	"ragbench-my/backend/internal/schema"
	"ragbench-my/backend/internal/trace"

	"github.com/jackc/pgx/v5/pgxpool"
)

const shutdownTimeout = 10 * time.Second

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
			"openai":      providers.NewOpenAICompatible("openai", cfg.OpenAIBaseURL, cfg.OpenAIGenerationAPIKey),
			"opencode-go": providers.NewOpenAICompatible("opencode-go", cfg.OpenAICompatibleBaseURL, cfg.OpenAICompatibleAPIKey),
		},
	}
	chatPipeline := &rag.Pipeline{
		Configs:   configStore,
		Retriever: rag.NewRetriever(pool),
		Embedder:  providerRouter,
		Generator: providerRouter,
		Traces:    traceStore,
	}
	handler := httpapi.NewFullAPI(logger,
		configStore,
		httpapi.DocumentOptions{Store: documents.NewStore(pool), Dispatcher: documents.NewAirflow(cfg.AirflowURL, cfg.AirflowUsername, cfg.AirflowPassword), UploadDir: cfg.UploadDir, MaxBytes: cfg.MaxUploadBytes},
		chatPipeline,
		traceStore,
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
