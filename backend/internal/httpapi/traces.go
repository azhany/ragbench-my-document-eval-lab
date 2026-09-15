package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"ragbench-my/backend/internal/trace"
)

// TracesStore is the trace read surface the trace API needs. *trace.Store
// satisfies it; tests can substitute a fake.
type TracesStore interface {
	List(ctx context.Context, limit int) ([]trace.TraceSummary, error)
	Get(ctx context.Context, traceID string) (trace.TraceDetail, error)
}

func (s *server) listTraces(w http.ResponseWriter, r *http.Request) {
	limit := trace.DefaultListLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > trace.MaxListLimit {
			writeError(w, http.StatusUnprocessableEntity, "invalid_limit",
				"limit must be an integer between 1 and "+strconv.Itoa(trace.MaxListLimit), nil)
			return
		}
		limit = parsed
	}

	traces, err := s.traces.List(r.Context(), limit)
	if err != nil {
		s.logger.Error("trace list failed", slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "internal_error", "unexpected server error", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"traces": traces})
}

func (s *server) getTrace(w http.ResponseWriter, r *http.Request) {
	detail, err := s.traces.Get(r.Context(), r.PathValue("traceId"))
	if err != nil {
		if errors.Is(err, trace.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "no trace with the given id", nil)
			return
		}
		s.logger.Error("trace detail failed", slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "internal_error", "unexpected server error", nil)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}
