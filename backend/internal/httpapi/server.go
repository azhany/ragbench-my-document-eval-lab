package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"ragbench-my/backend/internal/health"
)

const ServiceName = "ragbench-api"

func New(logger *slog.Logger, readinessChecks ...health.NamedCheck) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /{$}", rootHandler)
	mux.HandleFunc("GET /healthz", health.LivenessHandler(ServiceName))
	mux.Handle("GET /readyz", health.ReadinessHandler(logger, readinessChecks...))

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
		Status:     "ok",
		Resources: []string{"/healthz", "/readyz"},
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
