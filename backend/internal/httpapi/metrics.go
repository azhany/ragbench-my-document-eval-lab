package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"ragbench-my/backend/internal/metrics"
)

type MetricsStore interface {
	Summary(context.Context, metrics.Filter) (metrics.Summary, error)
}

func (s *server) metricsSummary(w http.ResponseWriter, r *http.Request) {
	f, err := parseMetricsFilter(r)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_metrics_filter", err.Error(), nil)
		return
	}
	summary, err := s.metrics.Summary(r.Context(), f)
	if err != nil {
		s.logger.Error("metrics summary failed", "error", err.Error())
		writeError(w, http.StatusInternalServerError, "metrics_unavailable", "metrics summary could not be read", nil)
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

func parseMetricsFilter(r *http.Request) (metrics.Filter, error) {
	f := metrics.Filter{Timezone: r.URL.Query().Get("timezone"), Traffic: r.URL.Query().Get("traffic")}
	if f.Timezone != "" {
		if _, err := time.LoadLocation(f.Timezone); err != nil {
			return f, fmt.Errorf("timezone must be an IANA timezone: %w", err)
		}
	}
	parse := func(key string) (time.Time, error) {
		raw := r.URL.Query().Get(key)
		if raw == "" {
			return time.Time{}, nil
		}
		value, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return time.Time{}, fmt.Errorf("%s must be RFC3339", key)
		}
		return value.UTC(), nil
	}
	var err error
	if f.From, err = parse("from"); err != nil {
		return f, err
	}
	if f.To, err = parse("to"); err != nil {
		return f, err
	}
	if !f.From.IsZero() && !f.To.IsZero() && !f.From.Before(f.To) {
		return f, errors.New("from must be before to")
	}
	return f, nil
}
