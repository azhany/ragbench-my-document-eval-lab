package health

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

const checkTimeout = 2 * time.Second

type NamedCheck struct {
	Name  string
	Check func(ctx context.Context) error
}

type CheckStatus struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

type livenessResponse struct {
	Service string `json:"service"`
	Status  string `json:"status"`
}

type readinessResponse struct {
	Status string                  `json:"status"`
	Checks map[string]*CheckStatus `json:"checks"`
}

func LivenessHandler(service string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, livenessResponse{Service: service, Status: "ok"})
	}
}

func ReadinessHandler(logger *slog.Logger, checks ...NamedCheck) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		results := make(map[string]*CheckStatus, len(checks))
		ready := true

		for _, check := range checks {
			ctx, cancel := context.WithTimeout(r.Context(), checkTimeout)
			err := check.Check(ctx)
			cancel()

			if err == nil {
				results[check.Name] = &CheckStatus{Status: "ok"}
				continue
			}
			ready = false
			results[check.Name] = &CheckStatus{Status: "error", Error: err.Error()}
			logger.Error("readiness check failed",
				slog.String("check", check.Name),
				slog.String("error", err.Error()),
			)
		}

		resp := readinessResponse{Status: "ready", Checks: results}
		status := http.StatusOK
		if !ready {
			resp.Status = "unavailable"
			status = http.StatusServiceUnavailable
		}
		writeJSON(w, status, resp)
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		return
	}
}
