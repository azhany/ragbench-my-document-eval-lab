package httpapi

import (
	"io"
	"log/slog"
	"testing"

	"ragbench-my/backend/internal/experiment"
)

func TestNewServerWiresExperimentOrchestrator(t *testing.T) {
	want := &experiment.Orchestrator{}
	got := newServer(slog.New(slog.NewTextHandler(io.Discard, nil)), nil, DocumentOptions{}, nil, nil,
		EvalOptions{Orchestrator: want})
	if got.orchestrator != want {
		t.Fatal("experiment orchestrator was not wired into the HTTP server")
	}
}
