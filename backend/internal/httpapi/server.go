package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

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
}

func NewDocumentAPI(logger *slog.Logger, configs ConfigStore, opts DocumentOptions, readinessChecks ...health.NamedCheck) http.Handler {
	return newAPI(logger, configs, opts, nil, nil, readinessChecks...)
}

// New wires configs and health only: no chat pipeline, traces, or document store.
func New(logger *slog.Logger, configs ConfigStore, readinessChecks ...health.NamedCheck) http.Handler {
	return newAPI(logger, configs, DocumentOptions{}, nil, nil, readinessChecks...)
}

// NewFullAPI wires every implemented resource, including chat and traces.
func NewFullAPI(logger *slog.Logger, configs ConfigStore, opts DocumentOptions, chatPipeline *rag.Pipeline, traces TracesStore, readinessChecks ...health.NamedCheck) http.Handler {
	return newAPI(logger, configs, opts, chatPipeline, traces, readinessChecks...)
}

func newAPI(logger *slog.Logger, configs ConfigStore, opts DocumentOptions, chatPipeline *rag.Pipeline, traces TracesStore, readinessChecks ...health.NamedCheck) http.Handler {
	s := &server{logger: logger, configs: configs, documents: opts, chatPipeline: chatPipeline, traces: traces}
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
	}

	return logMiddleware(logger, mux)
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
		Resources: []string{"/healthz", "/readyz", "/api/v1/rag-configs", "/api/v1/documents", "/api/v1/chat", "/api/v1/traces"},
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
