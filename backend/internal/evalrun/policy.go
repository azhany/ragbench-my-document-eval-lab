package evalrun

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"ragbench-my/backend/internal/comparison"
)

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
	if errors.Is(err, sql.ErrNoRows) {
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
