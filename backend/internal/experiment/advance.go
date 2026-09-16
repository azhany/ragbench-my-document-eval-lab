package experiment

import (
	"context"
	"errors"

	"ragbench-my/backend/internal/evalrun"
	"ragbench-my/backend/internal/ragconfig"
)

// AdvanceResult reports one state-machine step to the caller (the DAG and
// the polling endpoint).
type AdvanceResult struct {
	State            string `json:"state"` // "running" | "waiting_reindex" | "waiting_eval" | "completed" | "partial" | "failed"
	CombinationIndex int    `json:"combination_index,omitempty"`
	Detail           string `json:"detail,omitempty"`
}

// IndexPlanner decides reindex needs: chunk/embedding changes must build
// compatible revisions through document_reindex before evaluation; query-only
// changes reuse compatible ready revisions.
type IndexPlanner interface {
	ReindexNeeded(ctx context.Context, cfg ragconfig.Config) (bool, error)
}

// IndexDispatcher dispatches document_reindex for one configuration and
// persists durable dispatch state (a failure stays visible).
type IndexDispatcher interface {
	DispatchIndex(ctx context.Context, cfg ragconfig.Config) error
}

// RunLauncher creates and dispatches one eval run through the RB-14 path so
// experiment runs share identity/idempotency with golden runs.
type RunLauncher interface {
	LaunchRun(ctx context.Context, cfg ragconfig.Config, datasetID string, datasetVersion int, rubricVersion string, scoringK int) (evalrun.Run, error)
}

// RunReader reads eval run status for experiment orchestration.
type RunReader interface {
	GetRun(ctx context.Context, id string) (evalrun.Run, error)
}

// Orchestrator drives the persisted state machine; every step is idempotent
// so Airflow retries never duplicate logical experiments or runs.
type Orchestrator struct {
	Store         *Store
	Configs       ConfigReader
	ConfigCreator ConfigCreator
	Datasets      evalrun.DatasetReader
	Runner        RunLauncher
	Runs          RunReader
	IndexPlanner  IndexPlanner
	IndexDispatch IndexDispatcher
}

func (o *Orchestrator) Advance(ctx context.Context, experimentID string) (AdvanceResult, error) {
	e, err := o.Store.Get(ctx, experimentID)
	if err != nil {
		return AdvanceResult{}, err
	}
	if terminal(e.Status) {
		return AdvanceResult{State: e.Status}, nil
	}

	for _, c := range e.Combinations {
		if c.Failed() || c.Done() {
			continue
		}
		result, err := o.progressCombination(ctx, e, c)
		if err != nil {
			return AdvanceResult{}, err
		}
		if result != nil {
			return *result, nil
		}
	}
	return o.finalize(ctx, e)
}

// progressCombination advances one cell one idempotent step. A nil result
// means the cell is fully handled this pass and advance should continue
// scanning or finalize.
func (o *Orchestrator) progressCombination(ctx context.Context, e Experiment, c Combination) (*AdvanceResult, error) {
	settings, serr := c.Setting()
	if serr != nil {
		_ = o.Store.setFailure(ctx, e.ID, c.Index, "index_error", serr.Error())
		return nil, nil
	}

	// Step 1: immutable config identity for this cell.
	if c.RagConfigID == "" {
		cfg, err := o.createConfigIdentity(ctx, e, c, settings)
		if err != nil {
			return nil, err
		}
		if err := o.Store.setRagConfig(ctx, e.ID, c.Index, cfg.ID); err != nil {
			return nil, err
		}
		return &AdvanceResult{State: "running", CombinationIndex: c.Index, Detail: "config identity created"}, nil
	}

	cfg, err := o.Configs.Get(ctx, c.RagConfigID)
	if err != nil {
		_ = o.Store.setFailure(ctx, e.ID, c.Index, "index_error", "load config identity: "+err.Error())
		return nil, nil
	}
	if blocker := cfg.ExecutionBlocker(); blocker != nil {
		_ = o.Store.setFailure(ctx, e.ID, c.Index, "index_error", blocker.Error())
		return nil, nil
	}

	// Step 2: compatible index revision (document_reindex when needed).
	if !c.IndexDispatched {
		needed, err := o.IndexPlanner.ReindexNeeded(ctx, cfg)
		if err != nil {
			_ = o.Store.setFailure(ctx, e.ID, c.Index, "index_error", err.Error())
			return nil, nil
		}
		if needed {
			if err := o.IndexDispatch.DispatchIndex(ctx, cfg); err != nil {
				// A failed reindex prevents evaluation against the wrong
				// corpus: index_ready stays false and the cell fails.
				_ = o.Store.setFailure(ctx, e.ID, c.Index, "index_error",
					"reindex dispatch failed; refusing to evaluate against the wrong corpus: "+err.Error())
				return nil, nil
			}
		}
		if err := o.Store.markIndexDispatched(ctx, e.ID, c.Index); err != nil {
			return nil, err
		}
		if !needed {
			if err := o.Store.setIndexOutcome(ctx, e.ID, c.Index, true, ""); err != nil {
				return nil, err
			}
		}
		return &AdvanceResult{State: "running", CombinationIndex: c.Index, Detail: "index readiness ensured"}, nil
	}

	// Step 3: index readiness (a failed reindex is never bypassed).
	if !c.IndexReady {
		needed, err := o.IndexPlanner.ReindexNeeded(ctx, cfg)
		if err != nil {
			_ = o.Store.setFailure(ctx, e.ID, c.Index, "index_error", err.Error())
			return nil, nil
		}
		if needed {
			return &AdvanceResult{State: "waiting_reindex", CombinationIndex: c.Index}, nil
		}
		if err := o.Store.setIndexOutcome(ctx, e.ID, c.Index, true, ""); err != nil {
			return nil, err
		}
	}

	// Step 4: launch the pinned eval run (once, durably linked).
	if c.EvalRunID == "" {
		run, err := o.Runner.LaunchRun(ctx, cfg, e.DatasetID, e.DatasetVersion, e.RubricVersion, e.ScoringK)
		if err != nil {
			if run.ID != "" {
				// Keep the durable link visible even when dispatch failed.
				_ = o.Store.setEvalRun(ctx, e.ID, c.Index, run.ID)
				_ = o.Store.setEvalRunOutcome(ctx, e.ID, c.Index, run.Status)
			}
			if errors.Is(err, evalrun.ErrDispatchFailed) || run.ID != "" {
				_ = o.Store.setFailure(ctx, e.ID, c.Index, "eval_run_error", err.Error())
				return nil, nil
			}
			_ = o.Store.setFailure(ctx, e.ID, c.Index, "eval_run_error", err.Error())
			return nil, nil
		}
		if err := o.Store.setEvalRun(ctx, e.ID, c.Index, run.ID); err != nil {
			return nil, err
		}
		_ = o.Store.setEvalRunOutcome(ctx, e.ID, c.Index, run.Status)
		return &AdvanceResult{State: "waiting_eval", CombinationIndex: c.Index}, nil
	}

	// Step 5: refresh the combination's run status.
	run, err := o.Runs.GetRun(ctx, c.EvalRunID)
	if err != nil {
		_ = o.Store.setFailure(ctx, e.ID, c.Index, "eval_run_error", err.Error())
		return nil, nil
	}
	_ = o.Store.setEvalRunOutcome(ctx, e.ID, c.Index, run.Status)
	if run.Status == evalrun.StatusDispatchFailed {
		_ = o.Store.setFailure(ctx, e.ID, c.Index, "eval_run_error", run.DispatchError)
		return &AdvanceResult{State: "failed", CombinationIndex: c.Index}, nil
	}
	return &AdvanceResult{State: combinationState(run.Status), CombinationIndex: c.Index}, nil
}

// createConfigIdentity creates (or, on Airflow retry, reuses) the immutable
// config identity of one cell. Names are deterministic so a partially
// completed orchestration never creates twin identities.
func (o *Orchestrator) createConfigIdentity(ctx context.Context, e Experiment, c Combination, settings CombinationSetting) (ragconfig.Config, error) {
	req := settings.configRequest(expConfigName(e.Name, c.Index))
	cfg, err := o.ConfigCreator.Create(ctx, req)
	if err == nil {
		return cfg, nil
	}
	if errors.Is(err, ragconfig.ErrNameConflict) {
		// Reuse the identity a previous attempt already created.
		return o.Configs.GetByName(ctx, req.Name)
	}
	return ragconfig.Config{}, err
}

func combinationState(runStatus string) string {
	switch runStatus {
	case evalrun.StatusCompleted, evalrun.StatusPartial, evalrun.StatusFailed:
		return "running" // the experiment level decides its aggregate
	default:
		return "waiting_eval"
	}
}

// finalize computes terminal status; partial matrix failures retain
// successful run links and report partial/failed visibly.
func (o *Orchestrator) finalize(ctx context.Context, e Experiment) (AdvanceResult, error) {
	var succeeded, failed int
	for _, c := range e.Combinations {
		if c.Failed() {
			failed++
			continue
		}
		if c.EvalRunID != "" {
			succeeded++
		}
	}
	status := e.Status
	switch {
	case failed > 0 && succeeded > 0:
		status = StatusPartial
	case failed > 0:
		status = StatusFailed
	case succeeded == len(e.Combinations) && len(e.Combinations) > 0:
		status = StatusCompleted
	default:
		status = e.Status
	}
	if err := o.Store.setExperimentStatus(ctx, e.ID, status); err != nil {
		return AdvanceResult{}, err
	}
	return AdvanceResult{State: status}, nil
}

func terminal(status string) bool {
	switch status {
	case StatusCompleted, StatusPartial, StatusFailed, StatusDispatchFailed:
		return true
	}
	return false
}
