package evalrun

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"

	"ragbench-my/backend/internal/comparison"
)

// ComparisonRecorder is implemented by the PostgreSQL run store. It is kept
// optional at the HTTP seam so focused API tests need not model monitoring
// persistence.
type ComparisonRecorder interface {
	RecordComparison(ctx context.Context, candidateID, baselineID, policyName string, policyVersion int, verdict any) error
}

// ErrPolicyReport records that a queried regression policy doesn't exist.
var ErrPolicyNotFound = errors.New("regression policy not found")

// PolicyRow is one persisted regression policy (compare uses exactly one
// named persisted version, never a hidden threshold).
type PolicyRow struct {
	Name                  string          `json:"name"`
	Version               int             `json:"version"`
	Policy                json.RawMessage `json:"policy"`
	QualityGainDefinition string          `json:"quality_gain_definition"`
}

// Policy reads one persisted policy by name+version; version 0 = latest.
// Unknown names are ErrPolicyNotFound.
func (s *Store) Policy(ctx context.Context, name string, version int) (PolicyRow, error) {
	const byNameVersion = `SELECT name, version, policy, quality_gain_definition FROM eval_regression_policies WHERE name=$1 AND version=$2`
	const latestByName = `SELECT name, version, policy, quality_gain_definition FROM eval_regression_policies WHERE name=$1 ORDER BY version DESC LIMIT 1`
	var row PolicyRow
	var err error
	if version > 0 {
		err = s.pool.QueryRow(ctx, byNameVersion, name, version).
			Scan(&row.Name, &row.Version, &row.Policy, &row.QualityGainDefinition)
	} else {
		err = s.pool.QueryRow(ctx, latestByName, name).
			Scan(&row.Name, &row.Version, &row.Policy, &row.QualityGainDefinition)
	}
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, pgx.ErrNoRows) {
		return PolicyRow{}, ErrPolicyNotFound
	}
	return row, err
}

// Identity returns a run's pinned comparison identity for RB-19 gates.
func (s *Store) Identity(ctx context.Context, id string) (comparison.FactorIdentity, error) {
	run, err := s.GetRun(ctx, id)
	if err != nil {
		return comparison.FactorIdentity{}, err
	}
	var policy struct {
		ScoringK int `json:"scoring_k"`
	}
	if err := json.Unmarshal(run.EvaluatorPolicy, &policy); err != nil {
		return comparison.FactorIdentity{}, err
	}
	return comparison.FactorIdentity{
		DatasetID: run.DatasetID, DatasetVersion: run.DatasetVersion,
		Corpus: run.CorpusRevisions, ScoringK: policy.ScoringK, EvaluatorPolicy: run.EvaluatorPolicy,
	}, nil
}

func (s *Store) RecordComparison(ctx context.Context, candidateID, baselineID, policyName string, policyVersion int, verdict any) error {
	raw, err := json.Marshal(verdict)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO eval_comparisons (candidate_run_id, baseline_run_id, policy_name, policy_version, verdict)
		VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (candidate_run_id, baseline_run_id, policy_name, policy_version)
		DO UPDATE SET verdict=EXCLUDED.verdict, created_at=now()`, candidateID, baselineID, policyName, policyVersion, raw)
	return err
}
