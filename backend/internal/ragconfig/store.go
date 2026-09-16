package ragconfig

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// uniqueViolation is the PostgreSQL SQLSTATE for unique constraint violations
// (here: rag_configs_name_key).
const uniqueViolation = "23505"

// Store persists rag configurations in PostgreSQL.
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

const configColumns = `id, name, chunk_size, chunk_overlap, retrieval_mode, top_k,
	rerank_enabled, fusion_method, rrf_rank_constant, fts_candidate_limit,
	vector_candidate_limit,
	prompt_version, model_profile, embedding_profile,
	embedding_provider, embedding_model, embedding_dimensions, created_at`

// Create validates the request and inserts one immutable configuration.
func (s *Store) Create(ctx context.Context, req CreateRequest) (Config, error) {
	resolved, verrs := Resolve(req)
	if len(verrs) > 0 {
		return Config{}, verrs
	}

	row := s.pool.QueryRow(ctx, `
		INSERT INTO rag_configs (
			name, chunk_size, chunk_overlap, retrieval_mode, top_k, rerank_enabled,
			prompt_version, model_profile, embedding_profile,
			embedding_provider, embedding_model, embedding_dimensions
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		RETURNING id, created_at`,
		resolved.Name, resolved.ChunkSize, resolved.ChunkOverlap, resolved.RetrievalMode,
		resolved.TopK, resolved.RerankEnabled, resolved.PromptVersion, resolved.ModelProfile,
		resolved.EmbeddingProfile, resolved.EmbeddingProvider, resolved.EmbeddingModel,
		resolved.EmbeddingDimensions,
	)

	cfg := Config{
		Name:                resolved.Name,
		ChunkSize:           resolved.ChunkSize,
		ChunkOverlap:        resolved.ChunkOverlap,
		RetrievalMode:       resolved.RetrievalMode,
		TopK:                resolved.TopK,
		RerankEnabled:       resolved.RerankEnabled,
		PromptVersion:       resolved.PromptVersion,
		ModelProfile:        resolved.ModelProfile,
		EmbeddingProfile:    resolved.EmbeddingProfile,
		EmbeddingProvider:   resolved.EmbeddingProvider,
		EmbeddingModel:      resolved.EmbeddingModel,
		EmbeddingDimensions: resolved.EmbeddingDimensions,
	}
	if err := row.Scan(&cfg.ID, &cfg.CreatedAt); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			return Config{}, ErrNameConflict
		}
		return Config{}, fmt.Errorf("insert rag config: %w", err)
	}
	cfg.UnavailableCapabilities = unavailableCapabilitiesFor(cfg.RetrievalMode, cfg.RerankEnabled)
	return cfg, nil
}

// List returns every saved configuration ordered by name.
func (s *Store) List(ctx context.Context) ([]Config, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+configColumns+` FROM rag_configs ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list rag configs: %w", err)
	}
	defer rows.Close()

	configs := []Config{}
	for rows.Next() {
		cfg, err := scanConfig(rows)
		if err != nil {
			return nil, fmt.Errorf("list rag configs: %w", err)
		}
		configs = append(configs, cfg)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list rag configs: %w", err)
	}
	return configs, nil
}

// Get returns one configuration by id. Unknown or malformed ids return
// ErrNotFound.
func (s *Store) Get(ctx context.Context, id string) (Config, error) {
	parsed, err := uuid.Parse(strings.TrimSpace(id))
	if err != nil {
		return Config{}, ErrNotFound
	}
	row := s.pool.QueryRow(ctx,
		`SELECT `+configColumns+` FROM rag_configs WHERE id = $1`, parsed)
	cfg, err := scanConfig(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Config{}, ErrNotFound
		}
		return Config{}, fmt.Errorf("get rag config: %w", err)
	}
	return cfg, nil
}

// GetByName resolves one configuration by its unique name, used by retry
// paths that must reuse an existing immutable identity instead of creating
// twins.
func (s *Store) GetByName(ctx context.Context, name string) (Config, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+configColumns+` FROM rag_configs WHERE name = $1`, strings.TrimSpace(name))
	cfg, err := scanConfig(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Config{}, ErrNotFound
		}
		return Config{}, fmt.Errorf("get rag config by name: %w", err)
	}
	return cfg, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanConfig(row scanner) (Config, error) {
	var cfg Config
	if err := row.Scan(
		&cfg.ID, &cfg.Name, &cfg.ChunkSize, &cfg.ChunkOverlap, &cfg.RetrievalMode,
		&cfg.TopK, &cfg.RerankEnabled, &cfg.FusionMethod, &cfg.RRFConstant,
		&cfg.FTSCandidateLimit, &cfg.VectorCandidateLimit,
		&cfg.PromptVersion, &cfg.ModelProfile,
		&cfg.EmbeddingProfile, &cfg.EmbeddingProvider, &cfg.EmbeddingModel,
		&cfg.EmbeddingDimensions, &cfg.CreatedAt,
	); err != nil {
		return Config{}, err
	}
	if cfg.FusionMethod == "" {
		cfg.FusionMethod = FusionMethodRRF
	}
	if cfg.RRFConstant == 0 {
		cfg.RRFConstant = DefaultRRFConstant
	}
	if cfg.FTSCandidateLimit == 0 {
		cfg.FTSCandidateLimit = DefaultCandidateLimit
	}
	if cfg.VectorCandidateLimit == 0 {
		cfg.VectorCandidateLimit = DefaultCandidateLimit
	}
	cfg.UnavailableCapabilities = unavailableCapabilitiesFor(cfg.RetrievalMode, cfg.RerankEnabled)
	return cfg, nil
}
