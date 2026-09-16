// Package regression persists opt-in scheduled comparison definitions and
// their latest outcome. Airflow owns the clock; this package owns identities
// and outcome state.
package regression

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("scheduled regression check not found")

type Check struct {
	ID                string          `json:"id"`
	Name              string          `json:"name"`
	DatasetID         string          `json:"dataset_id"`
	DatasetVersion    int             `json:"dataset_version"`
	CandidateConfigID string          `json:"candidate_config_id"`
	BaselineRunID     string          `json:"baseline_run_id"`
	PolicyName        string          `json:"policy_name"`
	PolicyVersion     int             `json:"policy_version"`
	Enabled           bool            `json:"enabled"`
	LastRunID         string          `json:"last_run_id"`
	LastStatus        string          `json:"last_status"`
	LastReason        string          `json:"last_reason"`
	LastVerdict       json.RawMessage `json:"last_verdict"`
	CreatedAt         time.Time       `json:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at"`
}

type CreateRequest struct {
	Name              string `json:"name"`
	DatasetID         string `json:"dataset_id"`
	DatasetVersion    int    `json:"dataset_version"`
	CandidateConfigID string `json:"candidate_config_id"`
	BaselineRunID     string `json:"baseline_run_id"`
	PolicyName        string `json:"policy_name"`
	PolicyVersion     int    `json:"policy_version"`
	Enabled           bool   `json:"enabled"`
}

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

const selectColumns = `id, name, dataset_id, dataset_version, candidate_config_id,
 baseline_run_id, policy_name, policy_version, enabled, COALESCE(last_run_id::text,''),
 last_status, COALESCE(last_reason,''), COALESCE(last_verdict,'{}'::jsonb), created_at, updated_at`

func scan(row pgx.Row) (Check, error) {
	var c Check
	err := row.Scan(&c.ID, &c.Name, &c.DatasetID, &c.DatasetVersion, &c.CandidateConfigID,
		&c.BaselineRunID, &c.PolicyName, &c.PolicyVersion, &c.Enabled, &c.LastRunID,
		&c.LastStatus, &c.LastReason, &c.LastVerdict, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Check{}, ErrNotFound
	}
	return c, err
}

func (s *Store) Create(ctx context.Context, req CreateRequest) (Check, error) {
	var id string
	err := s.pool.QueryRow(ctx, `INSERT INTO scheduled_regression_checks
		(name,dataset_id,dataset_version,candidate_config_id,baseline_run_id,policy_name,policy_version,enabled)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`, req.Name, req.DatasetID, req.DatasetVersion,
		req.CandidateConfigID, req.BaselineRunID, req.PolicyName, req.PolicyVersion, req.Enabled).Scan(&id)
	if err != nil {
		return Check{}, err
	}
	return s.Get(ctx, id)
}

func (s *Store) Get(ctx context.Context, id string) (Check, error) {
	return scan(s.pool.QueryRow(ctx, `SELECT `+selectColumns+` FROM scheduled_regression_checks WHERE id=$1`, id))
}

func (s *Store) List(ctx context.Context) ([]Check, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+selectColumns+` FROM scheduled_regression_checks ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Check{}
	for rows.Next() {
		var c Check
		if err := rows.Scan(&c.ID, &c.Name, &c.DatasetID, &c.DatasetVersion, &c.CandidateConfigID,
			&c.BaselineRunID, &c.PolicyName, &c.PolicyVersion, &c.Enabled, &c.LastRunID,
			&c.LastStatus, &c.LastReason, &c.LastVerdict, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) SetRunning(ctx context.Context, id, runID string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE scheduled_regression_checks SET last_run_id=$2,last_status='running',last_reason=NULL,last_verdict=NULL,updated_at=now() WHERE id=$1`, id, runID)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func (s *Store) SetOutcome(ctx context.Context, id, status, reason string, verdict any) error {
	raw, err := json.Marshal(verdict)
	if err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `UPDATE scheduled_regression_checks SET last_status=$2,last_reason=nullIf($3,''),last_verdict=$4,updated_at=now() WHERE id=$1`, id, status, reason, raw)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}
