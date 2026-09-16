// Package experiment owns bounded parameter experiments (RB-18): persisted
// matrix expansion, per-combination immutable config identities, index
// revision resolution through document_reindex (a failed reindex never lets
// evaluation run against the wrong corpus), and idempotent retries.
package experiment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"ragbench-my/backend/internal/evalrun"
	"ragbench-my/backend/internal/evaluation"
	"ragbench-my/backend/internal/ragconfig"
)

const uniqueViolation = "23505"

// Store persists experiments and their combinations.
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Experiment is one persisted experiment with its combinations.
type Experiment struct {
	ID               string          `json:"id"`
	Name             string          `json:"name"`
	Description      string          `json:"description"`
	DatasetID        string          `json:"dataset_id"`
	DatasetVersion   int             `json:"dataset_version"`
	RubricVersion    string          `json:"rubric_version"`
	ScoringK         int             `json:"scoring_k"`
	RequestedMatrix  json.RawMessage `json:"requested_matrix"`
	CombinationLimit int             `json:"combination_limit"`
	Status           string          `json:"status"`
	Combinations     []Combination   `json:"combinations"`
	CreatedAt        time.Time       `json:"created_at"`
}

// Combination is one persisted expanded matrix cell.
type Combination struct {
	Index           int             `json:"combination_index"`
	Settings        json.RawMessage `json:"settings"`
	RagConfigID     string          `json:"rag_config_id"`
	IndexDispatched bool            `json:"index_dispatched"`
	IndexReady      bool            `json:"index_ready"`
	IndexError      string          `json:"index_error"`
	EvalRunID       string          `json:"eval_run_id"`
	EvalRunError    string          `json:"eval_run_error"`
	EvalRunStatus   string          `json:"eval_run_status"`
}

// Setting decodes the cell's concrete persisted settings.
func (c Combination) Setting() (CombinationSetting, error) {
	var s CombinationSetting
	if err := json.Unmarshal(c.Settings, &s); err != nil {
		return s, fmt.Errorf("stored combination settings are unreadable: %v", err)
	}
	return s, nil
}

// Failed reports whether the cell recorded an orchestration failure.
func (c Combination) Failed() bool { return c.IndexError != "" || c.EvalRunError != "" }

// Done reports whether the cell's linked run reached a terminal state (the
// experiment still derives its own aggregate from run statuses).
func (c Combination) Done() bool {
	switch c.EvalRunStatus {
	case evalrun.StatusCompleted, evalrun.StatusPartial, evalrun.StatusFailed,
		evalrun.StatusDispatchFailed:
		return true
	}
	return false
}

// ConfigReader reads immutable config identities.
type ConfigReader interface {
	Get(ctx context.Context, id string) (ragconfig.Config, error)
	GetByName(ctx context.Context, name string) (ragconfig.Config, error)
}

// ConfigCreator creates immutable rag config identities.
type ConfigCreator interface {
	Create(ctx context.Context, req ragconfig.CreateRequest) (ragconfig.Config, error)
}

// CreateRequest is the experiment payload.
type CreateRequest struct {
	Name             string `json:"name"`
	Description      string `json:"description"`
	DatasetID        string `json:"dataset_id"`
	DatasetVersion   int    `json:"dataset_version"`
	RubricVersion    string `json:"rubric_version"`
	ScoringK         int    `json:"scoring_k"`
	BaseConfigID     string `json:"base_config_id"`
	Matrix           Matrix `json:"matrix"`
	CombinationLimit int    `json:"combination_limit"`
}

// expConfigName builds the deterministic immutable-config name of one cell
// so Airflow retries reuse identities instead of creating twins.
func expConfigName(experimentName string, index int) string {
	return fmt.Sprintf("%s-c%d", experimentName, index)
}

// Create expands and persists the matrix (with the explicit combination
// limit) BEFORE any execution; each combination's immutable config identity
// is created lazily by Advance so a partially failed orchestration stays
// recoverable.
func (s *Store) Create(ctx context.Context, datasets evalrun.DatasetReader, configs ConfigReader, req CreateRequest) (Experiment, error) {
	if n := len([]rune(req.Name)); n < 1 || n > 200 {
		return Experiment{}, fmt.Errorf("name must be between 1 and 200 characters")
	}
	ds, err := datasets.GetDataset(ctx, req.DatasetID)
	if err != nil {
		return Experiment{}, fmt.Errorf("resolve dataset: %w", err)
	}
	version := req.DatasetVersion
	if version == 0 {
		version = ds.LatestVersion
	} else if version < 1 || version > ds.LatestVersion {
		return Experiment{}, fmt.Errorf("dataset version %d does not exist (1–%d)", version, ds.LatestVersion)
	}
	base, err := configs.Get(ctx, req.BaseConfigID)
	if err != nil {
		return Experiment{}, fmt.Errorf("resolve base config: %w", err)
	}
	combos, err := Expand(base, req.Matrix)
	if err != nil {
		return Experiment{}, err
	}
	limit := req.CombinationLimit
	if limit == 0 {
		limit = DefaultCombinationLimit
	}
	if limit < 1 || limit > MaxCombinationLimit {
		return Experiment{}, fmt.Errorf("combination_limit must be between 1 and %d", MaxCombinationLimit)
	}
	if len(combos) > limit {
		return Experiment{}, fmt.Errorf(
			"matrix expands to %d combinations, above the configured limit of %d; raise combination_limit (max %d) or reduce dimensions",
			len(combos), limit, MaxCombinationLimit)
	}
	// Every combination must resolve as a valid immutable configuration
	// identity before persistence: invalid or unsupported combinations are
	// rejected up front, never silently degrading.
	for i, c := range combos {
		if _, verrs := ragconfig.Resolve(c.configRequest(expConfigName(req.Name, i+1))); len(verrs) > 0 {
			return Experiment{}, fmt.Errorf("combination %d is invalid: %v", i+1, verrs)
		}
	}
	if _, err := evaluation.ResolveEvaluatorPolicy(req.RubricVersion, req.ScoringK); err != nil {
		return Experiment{}, fmt.Errorf("evaluator policy invalid: %v", err)
	}

	matrixJSON, err := json.Marshal(req.Matrix)
	if err != nil {
		return Experiment{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Experiment{}, err
	}
	defer tx.Rollback(context.Background())

	var id string
	err = tx.QueryRow(ctx, `
		INSERT INTO experiments (name, description, dataset_id, dataset_version,
		                         rubric_version, scoring_k, requested_matrix, combination_limit, status)
		VALUES ($1, nullIf($2,''), $3, $4, $5, $6, $7, $8, $9)
		RETURNING id`,
		req.Name, req.Description, req.DatasetID, version,
		req.RubricVersion, req.ScoringK, matrixJSON, limit, StatusCreated,
	).Scan(&id)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			return Experiment{}, ErrNameConflict
		}
		return Experiment{}, err
	}
	for i, c := range combos {
		settings, _ := json.Marshal(c)
		if _, err := tx.Exec(ctx, `
			INSERT INTO experiment_combinations (experiment_id, combination_index, settings)
			VALUES ($1, $2, $3)`, id, i+1, settings); err != nil {
			return Experiment{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Experiment{}, err
	}
	return s.Get(ctx, id)
}

// setRagConfig persists the created immutable config identity for one cell.
func (s *Store) setRagConfig(ctx context.Context, experimentID string, index int, configID string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE experiment_combinations SET rag_config_id=$3
		WHERE experiment_id=$1 AND combination_index=$2`, experimentID, index, configID)
	return err
}

// markIndexDispatched persists the reindex/remap decision for a cell.
func (s *Store) markIndexDispatched(ctx context.Context, experimentID string, index int) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE experiment_combinations SET index_dispatched=TRUE
		WHERE experiment_id=$1 AND combination_index=$2`, experimentID, index)
	return err
}

// setIndexOutcome persists the index readiness outcome for a cell.
func (s *Store) setIndexOutcome(ctx context.Context, experimentID string, index int, ready bool, errText string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE experiment_combinations SET index_ready=$3, index_error=nullIf($4,'')
		WHERE experiment_id=$1 AND combination_index=$2`, experimentID, index, ready, errText)
	return err
}

// setEvalRun persists the eval-run link for one cell; a second run id for
// the same cell is a conflict (no duplicate logical runs).
func (s *Store) setEvalRun(ctx context.Context, experimentID string, index int, runID string) error {
	var existing string
	if err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(eval_run_id::text,'') FROM experiment_combinations
		WHERE experiment_id=$1 AND combination_index=$2`,
		experimentID, index).Scan(&existing); err != nil {
		return err
	}
	if existing != "" && existing != runID {
		return errors.New("experiment link conflict: this combination already has a durable eval run")
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE experiment_combinations SET eval_run_id=$3, eval_run_error=NULL
		WHERE experiment_id=$1 AND combination_index=$2`, experimentID, index, runID)
	return err
}

// setEvalRunOutcome records eval run status transitions on a cell (kept for
// experiment detail without re-reading runs) — empty string clears.
func (s *Store) setEvalRunOutcome(ctx context.Context, experimentID string, index int, runStatus string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE experiment_combinations SET eval_run_status=nullIf($3,'')
		WHERE experiment_id=$1 AND combination_index=$2`, experimentID, index, runStatus)
	return err
}

// setFailure records any orchestration failure for a cell, visible in the
// experiment detail and in aggregate status.
func (s *Store) setFailure(ctx context.Context, experimentID string, index int, kind, message string) error {
	column := "index_error"
	if kind == "eval_run_error" {
		column = "eval_run_error"
	}
	q := `UPDATE experiment_combinations SET ` + column + `=nullIf($1,'') WHERE experiment_id=$2 AND combination_index=$3`
	_, err := s.pool.Exec(ctx, q, message, experimentID, index)
	return err
}

// setExperimentStatus persists a terminal or running experiment status.
func (s *Store) setExperimentStatus(ctx context.Context, experimentID string, status string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE experiments SET status=CASE WHEN status='dispatch_failed' THEN status ELSE $2 END,
		updated_at=now()
		WHERE id=$1`, experimentID, status)
	return err
}
