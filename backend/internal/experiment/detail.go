package experiment

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Get returns one experiment with all persisted combinations.
func (s *Store) Get(ctx context.Context, id string) (Experiment, error) {
	if _, err := uuid.Parse(id); err != nil {
		return Experiment{}, ErrNotFound
	}
	var e Experiment
	var matrix json.RawMessage
	var description *string
	err := s.pool.QueryRow(ctx, `
		SELECT id, name, description, dataset_id, dataset_version,
		       rubric_version, scoring_k, requested_matrix, combination_limit, status, created_at
		FROM experiments WHERE id=$1`, id).
		Scan(&e.ID, &e.Name, &description, &e.DatasetID, &e.DatasetVersion,
			&e.RubricVersion, &e.ScoringK, &matrix, &e.CombinationLimit, &e.Status, &e.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Experiment{}, ErrNotFound
	}
	if err != nil {
		return e, err
	}
	if description != nil {
		e.Description = *description
	}
	e.RequestedMatrix = matrix

	rows, err := s.pool.Query(ctx, `
		SELECT combination_index, settings, COALESCE(rag_config_id::text,''),
		       index_dispatched, index_ready, COALESCE(index_error,''),
		       COALESCE(eval_run_id::text,''), COALESCE(eval_run_error,''),
		       COALESCE(eval_run_status,'')
		FROM experiment_combinations WHERE experiment_id=$1 ORDER BY combination_index`, id)
	if err != nil {
		return e, err
	}
	defer rows.Close()
	for rows.Next() {
		var settings json.RawMessage
		c := Combination{}
		if err := rows.Scan(&c.Index, &settings, &c.RagConfigID, &c.IndexDispatched,
			&c.IndexReady, &c.IndexError, &c.EvalRunID, &c.EvalRunError,
			&c.EvalRunStatus); err != nil {
			return e, err
		}
		c.Settings = settings
		e.Combinations = append(e.Combinations, c)
	}
	if rows.Err() != nil {
		return e, rows.Err()
	}
	if e.Combinations == nil {
		e.Combinations = []Combination{}
	}
	return e, nil
}

// List returns experiments newest first.
func (s *Store) List(ctx context.Context) ([]Experiment, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, COALESCE(description,''), dataset_id, dataset_version,
		       rubric_version, scoring_k, requested_matrix, combination_limit, status, created_at
		FROM experiments ORDER BY created_at DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Experiment{}
	for rows.Next() {
		var e Experiment
		var matrix json.RawMessage
		if err := rows.Scan(&e.ID, &e.Name, &e.Description, &e.DatasetID, &e.DatasetVersion,
			&e.RubricVersion, &e.ScoringK, &matrix, &e.CombinationLimit, &e.Status, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.RequestedMatrix = matrix
		out = append(out, e)
	}
	return out, rows.Err()
}
