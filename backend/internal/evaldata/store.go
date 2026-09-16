// Package evaldata owns the versioned golden evaluation dataset domain:
// immutable numbered versions of reviewed question cases whose expected
// evidence references live document identities. Editing cases always creates
// a new version; the previous version becomes immutable history that eval
// runs (RB-14) keep pointing at.
//
// Relevance unit: expected evidence references document identities only. See
// db/migrations/0009_eval_datasets.sql for the documented policy — a stable
// unit across chunk configurations, never stale chunk UUIDs.
package evaldata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Validation bounds, mirrored conceptually by the CHECK constraints in
// db/migrations/0009_eval_datasets.sql. Declared here so no rule is a hidden
// magic number; keep the two in sync when changing them.
const (
	MinNameLen     = 1
	MaxNameLen     = 200
	MinCaseKeyLen  = 1
	MaxCaseKeyLen  = 100
	MinQuestionLen = 1
	MaxQuestionLen = 2000
	MaxAnswerLen   = 8000
	// A version with zero usable cases can never drive an evaluation run,
	// so it is rejected at write time instead of producing a dead run.
	MinCasesPerVersion = 1
)

// Dataset is one named golden dataset with its version pointer.
type Dataset struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Description   string    `json:"description"`
	LatestVersion int       `json:"latest_version"`
	CreatedAt     time.Time `json:"created_at"`
}

// Case is one reviewed golden question with its expected evidence.
type Case struct {
	ID               string         `json:"id"`
	CaseKey          string         `json:"case_key"`
	Question         string         `json:"question"`
	ReferenceAnswer  string         `json:"reference_answer"`
	ExpectedEvidence []string       `json:"expected_evidence"` // document ids
	ExpectedLabels   []Label        `json:"expected_labels"`   // document id → source label
	JudgmentVersion  string         `json:"judgment_version"`
	GradedJudgments  map[string]int `json:"graded_judgments"`
	Notes            string         `json:"notes"`
}

// Label keeps the human-readable source name next to each expected document
// so the dataset stays reviewable without joining documents at read time.
type Label struct {
	DocumentID string `json:"document_id"`
	Label      string `json:"label"`
}

// CaseInput is the raw case payload accepted by create/import and versioned
// edit endpoints.
type CaseInput struct {
	CaseKey          string          `json:"case_key"`
	Question         string          `json:"question"`
	ReferenceAnswer  string          `json:"reference_answer"`
	ExpectedEvidence json.RawMessage `json:"expected_evidence"`
	JudgmentVersion  string          `json:"judgment_version"`
	GradedJudgments  json.RawMessage `json:"graded_judgments"`
	// relevance_judgments is accepted as a descriptive alias for imports; it
	// is normalized to graded_judgments in the immutable stored case.
	RelevanceJudgments json.RawMessage `json:"relevance_judgments"`
	Notes              string          `json:"notes"`
}

func resolveGradedJudgments(raw, alias json.RawMessage, expected []string) (string, map[string]int, error) {
	if len(raw) == 0 {
		raw = alias
	}
	version := "binary-v1"
	if len(raw) == 0 || string(raw) == "null" {
		return version, map[string]int{}, nil
	}
	var grades map[string]int
	if err := json.Unmarshal(raw, &grades); err != nil {
		return "", nil, fmt.Errorf("graded_judgments must be an object of document id to integer grade: %v", err)
	}
	expectedSet := map[string]bool{}
	for _, id := range expected {
		expectedSet[id] = true
	}
	for id, grade := range grades {
		if _, err := uuid.Parse(id); err != nil {
			return "", nil, fmt.Errorf("graded_judgments contains malformed document id %q", id)
		}
		if !expectedSet[id] {
			return "", nil, fmt.Errorf("graded_judgments document %q is not in expected_evidence", id)
		}
		if grade < 0 || grade > 5 {
			return "", nil, fmt.Errorf("graded_judgments grade for %q must be between 0 and 5", id)
		}
	}
	if len(grades) > 0 {
		version = "ndcg-v1"
	}
	return version, grades, nil
}

// evidenceEntry is the accepted expected_evidence element shape.
type evidenceRef struct {
	DocumentID string `json:"document_id"`
	Label      string `json:"label"`
}

// ErrNotFound reports an unknown dataset, version, or case.
var ErrNotFound = errors.New("eval dataset not found")

// ErrNameConflict reports that a dataset with the same name already exists.
var ErrNameConflict = errors.New("an eval dataset with this name already exists")

// ErrDocumentUnknown reports an expected_evidence reference that is not a
// live document; invalid references are rejected, never stored.
var ErrDocumentUnknown = errors.New("expected evidence references an unknown or deleted document")

const uniqueViolation = "23505"

// Store persists golden datasets in PostgreSQL.
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// resolveEvidence normalizes and validates one case's expected_evidence: a
// non-empty array of {"document_id", "label"} where each document_id is a
// UUID that must reference a live (not deleted) document.
func (s *Store) resolveEvidence(ctx context.Context, raw json.RawMessage) ([]string, []Label, error) {
	if len(raw) == 0 {
		return nil, nil, fmt.Errorf("expected_evidence is required")
	}
	var refs []evidenceRef
	if err := json.Unmarshal(raw, &refs); err != nil {
		return nil, nil, fmt.Errorf("expected_evidence must be an array of {document_id, label} objects: %v", err)
	}
	if len(refs) == 0 {
		return nil, nil, fmt.Errorf("expected_evidence must list at least one document")
	}
	ids := make([]string, 0, len(refs))
	labels := make([]Label, 0, len(refs))
	seen := map[string]bool{}
	for _, ref := range refs {
		id := strings.TrimSpace(ref.DocumentID)
		if _, err := uuid.Parse(id); err != nil {
			return nil, nil, fmt.Errorf("expected_evidence contains a malformed document id %q", ref.DocumentID)
		}
		if seen[id] {
			return nil, nil, fmt.Errorf("expected_evidence lists document %s more than once", id)
		}
		seen[id] = true
		live, err := s.documentExists(ctx, id)
		if err != nil {
			return nil, nil, err
		}
		if !live {
			return nil, nil, fmt.Errorf("%w: %s", ErrDocumentUnknown, id)
		}
		ids = append(ids, id)
		labels = append(labels, Label{DocumentID: id, Label: strings.TrimSpace(ref.Label)})
	}
	return ids, labels, nil
}

func (s *Store) documentExists(ctx context.Context, id string) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM documents WHERE id=$1 AND deleted_at IS NULL)`, id).Scan(&exists)
	return exists, err
}

// validateCaseInput checks one raw case; reference errors (unknown documents)
// are returned as errors, not field violations, because they depend on
// database state.
func validateCaseInput(c CaseInput) error {
	key := strings.TrimSpace(c.CaseKey)
	if n := utf8.RuneCountInString(key); n < MinCaseKeyLen || n > MaxCaseKeyLen {
		return fmt.Errorf("case_key must be between %d and %d characters", MinCaseKeyLen, MaxCaseKeyLen)
	}
	q := strings.TrimSpace(c.Question)
	if n := utf8.RuneCountInString(q); n < MinQuestionLen || n > MaxQuestionLen {
		return fmt.Errorf("question must be between %d and %d characters", MinQuestionLen, MaxQuestionLen)
	}
	if utf8.RuneCountInString(strings.TrimSpace(c.ReferenceAnswer)) == 0 {
		return fmt.Errorf("reference_answer is required")
	}
	if utf8.RuneCountInString(c.ReferenceAnswer) > MaxAnswerLen {
		return fmt.Errorf("reference_answer must be at most %d characters", MaxAnswerLen)
	}
	return nil
}

// insertCases writes one full version of cases inside a transaction; the
// caller committed to the dataset row first.
func (s *Store) insertCases(ctx context.Context, tx pgx.Tx, datasetID string, version int, cases []CaseInput) error {
	if len(cases) < MinCasesPerVersion {
		return fmt.Errorf("a dataset version needs at least %d case(s); empty or unusable datasets are rejected", MinCasesPerVersion)
	}
	for i, c := range cases {
		if err := validateCaseInput(c); err != nil {
			return fmt.Errorf("case %d: %v", i+1, err)
		}
		ids, labels, err := s.resolveEvidence(ctx, c.ExpectedEvidence)
		if err != nil {
			return fmt.Errorf("case %d (%s): %w", i+1, strings.TrimSpace(c.CaseKey), err)
		}
		idsJSON, _ := json.Marshal(ids)
		labelsJSON, _ := json.Marshal(labels)
		judgmentVersion, grades, err := resolveGradedJudgments(c.GradedJudgments, c.RelevanceJudgments, ids)
		if err != nil {
			return fmt.Errorf("case %d (%s): %w", i+1, strings.TrimSpace(c.CaseKey), err)
		}
		if strings.TrimSpace(c.JudgmentVersion) != "" {
			judgmentVersion = strings.TrimSpace(c.JudgmentVersion)
			switch judgmentVersion {
			case "binary-v1":
				if len(grades) > 0 {
					return fmt.Errorf("judgment_version binary-v1 cannot be used with graded_judgments")
				}
			case "ndcg-v1":
			default:
				return fmt.Errorf("unknown judgment_version %q", judgmentVersion)
			}
		}
		gradesJSON, _ := json.Marshal(grades)
		tag, err := tx.Exec(ctx, `
			INSERT INTO eval_cases (dataset_id, version, case_key, question, reference_answer, expected_evidence, judgment_version, graded_judgments, notes)
			VALUES ($1, $2, $3, $4, $5,
			        jsonb_build_object('document_ids', $6::jsonb, 'labels', $7::jsonb), $8, $9,
			        nullIf($10, ''))
			ON CONFLICT (dataset_id, version, case_key) DO NOTHING`,
			datasetID, version, strings.TrimSpace(c.CaseKey),
			strings.TrimSpace(c.Question), strings.TrimSpace(c.ReferenceAnswer),
			idsJSON, labelsJSON, judgmentVersion, gradesJSON, strings.TrimSpace(c.Notes),
		)
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
				return fmt.Errorf("duplicate case_key %q within one version", strings.TrimSpace(c.CaseKey))
			}
			return fmt.Errorf("insert case: %w", err)
		}
		if tag.RowsAffected() != 1 {
			return fmt.Errorf("duplicate insert produced no row (case_key %q internal error)", strings.TrimSpace(c.CaseKey))
		}
	}
	return nil
}

// ErrCaseKeyDuplicate reports a repeated case_key inside one version payload.
var ErrCaseKeyDuplicate = errors.New("duplicate case_key in the submitted cases")

// CreateDataset creates an immutable version 1 with the submitted cases.
// Empty case lists and invalid reference documents are rejected.
func (s *Store) CreateDataset(ctx context.Context, name, description string, cases []CaseInput) (Dataset, error) {
	if n := utf8.RuneCountInString(strings.TrimSpace(name)); n < MinNameLen || n > MaxNameLen {
		return Dataset{}, fmt.Errorf("name must be between %d and %d characters", MinNameLen, MaxNameLen)
	}
	keys := map[string]bool{}
	for _, c := range cases {
		k := strings.TrimSpace(c.CaseKey)
		if keys[k] {
			return Dataset{}, fmt.Errorf("%w: %s", ErrCaseKeyDuplicate, k)
		}
		keys[k] = true
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Dataset{}, err
	}
	defer tx.Rollback(context.Background())

	row := tx.QueryRow(ctx, `
		INSERT INTO eval_datasets (name, description, latest_version)
		VALUES ($1, nullIf($2, ''), 1)
		RETURNING id, name, COALESCE(description, ''), latest_version, created_at`,
		strings.TrimSpace(name), strings.TrimSpace(description))
	var d Dataset
	if err := row.Scan(&d.ID, &d.Name, &d.Description, &d.LatestVersion, &d.CreatedAt); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			return Dataset{}, ErrNameConflict
		}
		return Dataset{}, fmt.Errorf("create eval dataset: %w", err)
	}
	if err := s.insertCases(ctx, tx, d.ID, 1, cases); err != nil {
		return Dataset{}, err
	}
	// Version 1 is empty/unusable only if every case was rejected above.
	if err := tx.Commit(ctx); err != nil {
		return Dataset{}, fmt.Errorf("commit eval dataset: %w", err)
	}
	return d, nil
}

// NewVersion replaces the case set with a new immutable version (latest+1).
// The previous versions remain untouched so historical runs keep reading them.
func (s *Store) NewVersion(ctx context.Context, datasetID string, cases []CaseInput) (Dataset, error) {
	if _, err := uuid.Parse(datasetID); err != nil {
		return Dataset{}, ErrNotFound
	}
	keys := map[string]bool{}
	for _, c := range cases {
		k := strings.TrimSpace(c.CaseKey)
		if keys[k] {
			return Dataset{}, fmt.Errorf("%w: %s", ErrCaseKeyDuplicate, k)
		}
		keys[k] = true
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Dataset{}, err
	}
	defer tx.Rollback(context.Background())

	var latest int
	err = tx.QueryRow(ctx,
		`SELECT latest_version FROM eval_datasets WHERE id=$1 FOR UPDATE`, datasetID).Scan(&latest)
	if errors.Is(err, pgx.ErrNoRows) {
		return Dataset{}, ErrNotFound
	}
	if err != nil {
		return Dataset{}, err
	}
	version := latest + 1
	if err := s.insertCases(ctx, tx, datasetID, version, cases); err != nil {
		return Dataset{}, err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE eval_datasets SET latest_version=$2 WHERE id=$1`, datasetID, version); err != nil {
		return Dataset{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Dataset{}, err
	}
	return s.GetDataset(ctx, datasetID)
}

// List returns every dataset ordered by name.
func (s *Store) List(ctx context.Context) ([]Dataset, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, COALESCE(description, ''), latest_version, created_at
		FROM eval_datasets ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Dataset{}
	for rows.Next() {
		var d Dataset
		if err := rows.Scan(&d.ID, &d.Name, &d.Description, &d.LatestVersion, &d.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// GetDataset returns one dataset; version 0 means the latest version.
func (s *Store) GetDataset(ctx context.Context, id string) (Dataset, error) {
	if _, err := uuid.Parse(id); err != nil {
		return Dataset{}, ErrNotFound
	}
	var d Dataset
	err := s.pool.QueryRow(ctx, `
		SELECT id, name, COALESCE(description, ''), latest_version, created_at
		FROM eval_datasets WHERE id=$1`, id).
		Scan(&d.ID, &d.Name, &d.Description, &d.LatestVersion, &d.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Dataset{}, ErrNotFound
	}
	return d, err
}

// VersionedCases bundles one immutable version's case list.
type VersionedCases struct {
	DatasetID string `json:"dataset_id"`
	Version   int    `json:"version"`
	Cases     []Case `json:"cases"`
}

// GetVersion returns the reviewable case list of one immutable version.
func (s *Store) GetVersion(ctx context.Context, datasetID string, version int) (VersionedCases, error) {
	d, err := s.GetDataset(ctx, datasetID)
	if err != nil {
		return VersionedCases{}, err
	}
	if version == 0 {
		version = d.LatestVersion
	}
	if version < 1 || version > d.LatestVersion {
		return VersionedCases{}, fmt.Errorf("version %d does not exist for this dataset (1–%d)", version, d.LatestVersion)
	}

	rows, err := s.pool.Query(ctx, `
		SELECT id, case_key, question, reference_answer, expected_evidence,
		       judgment_version, graded_judgments, COALESCE(notes, '')
		FROM eval_cases
		WHERE dataset_id=$1 AND version=$2
		ORDER BY case_key`, datasetID, version)
	if err != nil {
		return VersionedCases{}, err
	}
	defer rows.Close()
	vc := VersionedCases{DatasetID: datasetID, Version: version, Cases: []Case{}}
	for rows.Next() {
		var c Case
		var evidence struct {
			DocumentIDs []string `json:"document_ids"`
			Labels      []Label  `json:"labels"`
		}
		var gradesJSON json.RawMessage
		if err := rows.Scan(&c.ID, &c.CaseKey, &c.Question, &c.ReferenceAnswer, &evidence,
			&c.JudgmentVersion, &gradesJSON, &c.Notes); err != nil {
			return VersionedCases{}, err
		}
		c.GradedJudgments = map[string]int{}
		if len(gradesJSON) > 0 {
			_ = json.Unmarshal(gradesJSON, &c.GradedJudgments)
		}
		c.ExpectedEvidence = evidence.DocumentIDs
		c.ExpectedLabels = evidence.Labels
		vc.Cases = append(vc.Cases, c)
	}
	if err := rows.Err(); err != nil {
		return VersionedCases{}, err
	}
	return vc, nil
}

// GetCase returns one case from one version, for scoring and run execution.
func (s *Store) GetCase(ctx context.Context, datasetID string, version int, caseID string) (Case, error) {
	vc, err := s.GetVersion(ctx, datasetID, version)
	if err != nil {
		return Case{}, err
	}
	for _, c := range vc.Cases {
		if c.ID == caseID {
			return c, nil
		}
	}
	return Case{}, ErrNotFound
}
