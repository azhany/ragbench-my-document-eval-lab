package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"ragbench-my/backend/internal/evalrun"
	"ragbench-my/backend/internal/experiment"
	"ragbench-my/backend/internal/health"
	"ragbench-my/backend/internal/rag"
)

const ServiceName = "ragbench-api"

// server carries the dependencies shared by API handlers.
type server struct {
	logger       *slog.Logger
	configs      ConfigStore
	documents    DocumentOptions
	chatPipeline *rag.Pipeline
	traces       TracesStore
	datasets     DatasetStore
	runs         EvalRunStore
	experiments  ExperimentStore
	orchestrator *experiment.Orchestrator
	dispatcher   evalrun.Dispatcher
	executor     *evalrun.Executor
	metrics      MetricsStore
	regressions  RegressionStore
	analysis     AnalysisOptions
}

func NewDocumentAPI(logger *slog.Logger, configs ConfigStore, opts DocumentOptions, readinessChecks ...health.NamedCheck) http.Handler {
	return newAPI(logger, configs, opts, nil, nil, EvalOptions{}, readinessChecks...)
}

// New wires configs and health only: no chat pipeline, traces, or document store.
func New(logger *slog.Logger, configs ConfigStore, readinessChecks ...health.NamedCheck) http.Handler {
	return newAPI(logger, configs, DocumentOptions{}, nil, nil, EvalOptions{}, readinessChecks...)
}

// NewFullAPI wires every implemented resource, including chat and traces.
func NewFullAPI(logger *slog.Logger, configs ConfigStore, opts DocumentOptions, chatPipeline *rag.Pipeline, traces TracesStore, eval EvalOptions, readinessChecks ...health.NamedCheck) http.Handler {
	return newAPI(logger, configs, opts, chatPipeline, traces, eval, readinessChecks...)
}

// EvalOptions carries the Sprint 4/5 evaluation dependencies. All fields are
// optional; a route is only registered when its store is wired.
type EvalOptions struct {
	Datasets    DatasetStore
	Runs        EvalRunStore
	Experiments ExperimentStore

	// Orchestrator executes RB-18's persisted experiment state machine.
	Orchestrator *experiment.Orchestrator

	// Dispatcher triggers the evaluation DAG; Executor runs cases through
	// the shared pipeline and persists scores.
	Dispatcher  evalrun.Dispatcher
	Executor    *evalrun.Executor
	Metrics     MetricsStore
	Regressions RegressionStore
}

func newAPI(logger *slog.Logger, configs ConfigStore, opts DocumentOptions, chatPipeline *rag.Pipeline, traces TracesStore, eval EvalOptions, readinessChecks ...health.NamedCheck) http.Handler {
	s := newServer(logger, configs, opts, chatPipeline, traces, eval)

	mux := http.NewServeMux()

	mux.HandleFunc("GET /{$}", rootHandler)
	mux.HandleFunc("GET /healthz", health.LivenessHandler(ServiceName))
	mux.Handle("GET /readyz", health.ReadinessHandler(logger, readinessChecks...))
	mux.HandleFunc("POST /api/v1/rag-configs", s.createConfig)
	mux.HandleFunc("GET /api/v1/rag-configs", s.listConfigs)
	mux.HandleFunc("GET /api/v1/rag-configs/{id}", s.getConfig)
	if chatPipeline != nil {
		mux.HandleFunc("POST /api/v1/chat", s.chat)
		if traces != nil {
			mux.HandleFunc("GET /api/v1/traces", s.listTraces)
			mux.HandleFunc("GET /api/v1/traces/{traceId}", s.getTrace)
		}
	}
	if opts.Store != nil {
		mux.HandleFunc("POST /api/v1/documents", s.uploadDocument)
		mux.HandleFunc("GET /api/v1/documents", s.listDocuments)
		mux.HandleFunc("GET /api/v1/documents/{id}", s.getDocument)
		mux.HandleFunc("POST /api/v1/documents/{id}/retry-dispatch", s.retryDispatch)
		mux.HandleFunc("POST /api/v1/documents/{id}/reprocess", s.reprocessDocument)
		mux.HandleFunc("DELETE /api/v1/documents/{id}", s.deleteDocument)
		if opts.AnalysisStore != nil {
			mux.HandleFunc("POST /api/v1/documents/{id}/analyses", s.createAnalysis)
			mux.HandleFunc("GET /api/v1/documents/{id}/analyses", s.listAnalysesForDocument)
		}
	}
	if opts.AnalysisStore != nil {
		mux.HandleFunc("GET /api/v1/document-analyses/{analysisId}", s.getAnalysis)
		mux.HandleFunc("POST /api/v1/document-analyses/{analysisId}/retry-dispatch", s.retryAnalysisDispatch)
		if opts.AnalysisRunner != nil {
			mux.HandleFunc("POST /api/v1/document-analyses/{analysisId}/stages/{stage}", s.runAnalysisStage)
		}
	}
	if eval.Datasets != nil {
		mux.HandleFunc("POST /api/v1/eval-datasets", s.createDataset)
		mux.HandleFunc("GET /api/v1/eval-datasets", s.listDatasets)
		mux.HandleFunc("GET /api/v1/eval-datasets/{id}", s.getDataset)
		mux.HandleFunc("POST /api/v1/eval-datasets/{id}/cases", s.newDatasetVersion)
	}
	if eval.Runs != nil {
		mux.HandleFunc("POST /api/v1/eval-runs", s.createEvalRun)
		mux.HandleFunc("GET /api/v1/eval-runs", s.listEvalRuns)
		mux.HandleFunc("GET /api/v1/eval-runs/{id}", s.getEvalRun)
		mux.HandleFunc("GET /api/v1/eval-runs/{id}/results", s.listEvalResults)
		mux.HandleFunc("POST /api/v1/eval-runs/{id}/cases/{caseId}/execute", s.executeEvalCase)
		mux.HandleFunc("POST /api/v1/eval-runs/{id}/finalize", s.finalizeEvalRun)
		mux.HandleFunc("GET /api/v1/eval-runs/{id}/compare/{baselineId}", s.compareEvalRun)
	}
	if eval.Experiments != nil {
		mux.HandleFunc("POST /api/v1/experiments", s.createExperiment)
		mux.HandleFunc("GET /api/v1/experiments", s.listExperiments)
		mux.HandleFunc("GET /api/v1/experiments/{id}", s.getExperiment)
		mux.HandleFunc("POST /api/v1/experiments/{id}/advance", s.advanceExperiment)
	}
	if eval.Metrics != nil {
		mux.HandleFunc("GET /api/v1/metrics/summary", s.metricsSummary)
	}
	if eval.Regressions != nil {
		mux.HandleFunc("POST /api/v1/regression-checks", s.createRegressionCheck)
		mux.HandleFunc("GET /api/v1/regression-checks", s.listRegressionChecks)
		mux.HandleFunc("GET /api/v1/regression-checks/{id}", s.getRegressionCheck)
		mux.HandleFunc("POST /api/v1/regression-checks/{id}/start", s.startRegressionCheck)
		mux.HandleFunc("POST /api/v1/regression-checks/{id}/finish", s.finishRegressionCheck)
	}

	return logMiddleware(logger, mux)
}

func newServer(logger *slog.Logger, configs ConfigStore, opts DocumentOptions, chatPipeline *rag.Pipeline, traces TracesStore, eval EvalOptions) *server {
	return &server{
		logger: logger, configs: configs, documents: opts, chatPipeline: chatPipeline, traces: traces,
		datasets: eval.Datasets, runs: eval.Runs, experiments: eval.Experiments,
		orchestrator: eval.Orchestrator, dispatcher: eval.Dispatcher, executor: eval.Executor, metrics: eval.Metrics, regressions: eval.Regressions,
		analysis: AnalysisOptions{Store: opts.AnalysisStore, Dispatcher: opts.AnalysisDispatcher, Runner: opts.AnalysisRunner},
	}
}

type serviceInfo struct {
	Service   string   `json:"service"`
	Status    string   `json:"status"`
	Resources []string `json:"resources"`
}

func rootHandler(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, serviceInfo{
		Service:   ServiceName,
		Status:    "ok",
		Resources: []string{"/healthz", "/readyz", "/api/v1/rag-configs", "/api/v1/documents", "/api/v1/document-analyses", "/api/v1/chat", "/api/v1/traces", "/api/v1/eval-datasets", "/api/v1/eval-runs", "/api/v1/experiments", "/api/v1/metrics/summary", "/api/v1/regression-checks"},
	})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		return
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func logMiddleware(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		logger.Info("http_request",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", rec.status),
			slog.Int64("duration_ms", time.Since(start).Milliseconds()),
		)
	})
}
