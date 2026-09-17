// Package modelprofile persists the model identities that can be selected by
// a RAG configuration. Provider credentials and endpoint secrets deliberately
// do not belong here: they remain process/environment configuration.
package modelprofile

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"ragbench-my/backend/internal/providers"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Kind string

const (
	KindGeneration Kind = "generation"
	KindEmbedding  Kind = "embedding"
)

const (
	MinNameLen = 1
	MaxNameLen = 120
)

var profileNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// The provider adapters are intentionally small and explicit. A profile may
// select any model served by one of these adapters; adding a model no longer
// requires a Go registry change. Adding a new wire protocol still requires a
// provider adapter and a server-side credential mapping.
var providerKinds = map[string]map[Kind]bool{
	"openai":           {KindGeneration: true, KindEmbedding: true},
	"opencode-go":      {KindGeneration: true},
	"opencode-zen":     {KindGeneration: true},
	"huggingface-chat": {KindGeneration: true},
	"huggingface":      {KindEmbedding: true},
}

type Profile struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Kind       Kind      `json:"kind"`
	Provider   string    `json:"provider"`
	Model      string    `json:"model"`
	Dimensions *int      `json:"dimensions"`
	Enabled    bool      `json:"enabled"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type CreateRequest struct {
	Name       string `json:"name"`
	Kind       Kind   `json:"kind"`
	Provider   string `json:"provider"`
	Model      string `json:"model"`
	Dimensions int    `json:"dimensions"`
}

type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

type ValidationErrors []FieldError

func (e ValidationErrors) Error() string {
	parts := make([]string, len(e))
	for i, field := range e {
		parts[i] = field.Field + ": " + field.Message
	}
	return "invalid model profile: " + strings.Join(parts, "; ")
}

var (
	ErrNotFound     = errors.New("model profile not found")
	ErrNameConflict = errors.New("a model profile with this name and kind already exists")
)

// Validate checks only settings that are safe to validate without contacting
// a provider. The endpoint and credential are supplied by the API process.
func Validate(req CreateRequest) (CreateRequest, ValidationErrors) {
	var errs ValidationErrors
	req.Name = strings.ToLower(strings.TrimSpace(req.Name))
	req.Provider = strings.TrimSpace(req.Provider)
	req.Model = strings.TrimSpace(req.Model)

	nameLength := utf8.RuneCountInString(req.Name)
	if nameLength < MinNameLen || nameLength > MaxNameLen {
		errs = append(errs, FieldError{Field: "name", Message: fmt.Sprintf("name is required (%d–%d characters)", MinNameLen, MaxNameLen)})
	} else if !profileNamePattern.MatchString(req.Name) {
		errs = append(errs, FieldError{Field: "name", Message: "name may contain lowercase letters, numbers, dots, underscores, and hyphens"})
	}

	if req.Kind != KindGeneration && req.Kind != KindEmbedding {
		errs = append(errs, FieldError{Field: "kind", Message: "kind must be generation or embedding"})
	} else if !providerKinds[req.Provider][req.Kind] {
		errs = append(errs, FieldError{Field: "provider", Message: fmt.Sprintf("provider %q cannot serve %s profiles", req.Provider, req.Kind)})
	}
	if req.Model == "" {
		errs = append(errs, FieldError{Field: "model", Message: "model is required"})
	}
	if req.Kind == KindEmbedding {
		if req.Dimensions < 1 {
			errs = append(errs, FieldError{Field: "dimensions", Message: "embedding dimensions must be greater than zero"})
		}
	} else if req.Dimensions != 0 {
		errs = append(errs, FieldError{Field: "dimensions", Message: "generation profiles do not use dimensions"})
	}
	return req, errs
}

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) List(ctx context.Context) ([]Profile, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, kind, provider, model, dimensions, enabled, created_at, updated_at
		FROM model_profiles ORDER BY kind, name`)
	if err != nil {
		return nil, fmt.Errorf("list model profiles: %w", err)
	}
	defer rows.Close()
	profiles := []Profile{}
	for rows.Next() {
		profile, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("list model profiles: %w", err)
		}
		profiles = append(profiles, profile)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list model profiles: %w", err)
	}
	return profiles, nil
}

func (s *Store) Create(ctx context.Context, req CreateRequest) (Profile, error) {
	req, errs := Validate(req)
	if len(errs) > 0 {
		return Profile{}, errs
	}
	var profile Profile
	err := s.pool.QueryRow(ctx, `
		INSERT INTO model_profiles (name, kind, provider, model, dimensions)
		VALUES ($1, $2, $3, $4, NULLIF($5, 0))
		RETURNING id, name, kind, provider, model, dimensions, enabled, created_at, updated_at`,
		req.Name, req.Kind, req.Provider, req.Model, req.Dimensions).Scan(
		&profile.ID, &profile.Name, &profile.Kind, &profile.Provider, &profile.Model,
		&profile.Dimensions, &profile.Enabled, &profile.CreatedAt, &profile.UpdatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return Profile{}, ErrNameConflict
		}
		return Profile{}, fmt.Errorf("create model profile: %w", err)
	}
	return profile, nil
}

// Get returns one persisted profile by id. Disabled profiles remain readable
// for Settings administration, but only enabled profiles resolve for new RAG
// configurations.
func (s *Store) Get(ctx context.Context, id string) (Profile, error) {
	parsed, err := uuid.Parse(strings.TrimSpace(id))
	if err != nil {
		return Profile{}, ErrNotFound
	}
	var profile Profile
	err = s.pool.QueryRow(ctx, `
		SELECT id, name, kind, provider, model, dimensions, enabled, created_at, updated_at
		FROM model_profiles WHERE id=$1`, parsed).Scan(
		&profile.ID, &profile.Name, &profile.Kind, &profile.Provider, &profile.Model,
		&profile.Dimensions, &profile.Enabled, &profile.CreatedAt, &profile.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Profile{}, ErrNotFound
	}
	if err != nil {
		return Profile{}, fmt.Errorf("get model profile: %w", err)
	}
	return profile, nil
}

// GenerationProfile resolves an enabled persisted profile into the small
// provider integration identity used by the Go query and generation paths.
func (s *Store) GenerationProfile(ctx context.Context, name string) (providers.GenerationProfile, error) {
	profile, err := s.get(ctx, KindGeneration, name)
	if err != nil {
		return providers.GenerationProfile{}, err
	}
	return providers.GenerationProfile{Name: profile.Name, Provider: profile.Provider, Model: profile.Model}, nil
}

// EmbeddingProfile resolves an enabled persisted profile into the identity
// used by Go query embedding and Airflow batch embedding.
func (s *Store) EmbeddingProfile(ctx context.Context, name string) (providers.EmbeddingProfile, error) {
	profile, err := s.get(ctx, KindEmbedding, name)
	if err != nil {
		return providers.EmbeddingProfile{}, err
	}
	if profile.Dimensions == nil || *profile.Dimensions < 1 {
		return providers.EmbeddingProfile{}, fmt.Errorf("embedding profile %q has invalid dimensions", name)
	}
	return providers.EmbeddingProfile{Name: profile.Name, Provider: profile.Provider, Model: profile.Model, Dimensions: *profile.Dimensions}, nil
}

func (s *Store) get(ctx context.Context, kind Kind, name string) (Profile, error) {
	var profile Profile
	err := s.pool.QueryRow(ctx, `
		SELECT id, name, kind, provider, model, dimensions, enabled, created_at, updated_at
		FROM model_profiles WHERE kind=$1 AND name=$2 AND enabled=true`, kind, strings.TrimSpace(name)).Scan(
		&profile.ID, &profile.Name, &profile.Kind, &profile.Provider, &profile.Model,
		&profile.Dimensions, &profile.Enabled, &profile.CreatedAt, &profile.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Profile{}, fmt.Errorf("%w: %s/%s", ErrNotFound, kind, name)
	}
	if err != nil {
		return Profile{}, fmt.Errorf("resolve model profile: %w", err)
	}
	return profile, nil
}

type scanner interface{ Scan(dest ...any) error }

func scan(row scanner) (Profile, error) {
	var profile Profile
	err := row.Scan(&profile.ID, &profile.Name, &profile.Kind, &profile.Provider,
		&profile.Model, &profile.Dimensions, &profile.Enabled, &profile.CreatedAt, &profile.UpdatedAt)
	return profile, err
}
