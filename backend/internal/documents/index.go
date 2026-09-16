// Index preparation for parameter experiments (RB-18): chunk/embedding
// changes must build a compatible revision through document_reindex before
// an evaluation runs; a failed reindex keeps index_ready false so a
// combination never evaluates against the wrong corpus.
package documents

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"ragbench-my/backend/internal/ragconfig"
)

// IndexPlanner answers reindex needs per configuration: a configuration is
// ready when every live document with an active revision shares its chunk
// settings and embedding identity.
type IndexPlanner struct {
	pool *pgxpool.Pool
}

func NewIndexPlanner(pool *pgxpool.Pool) *IndexPlanner { return &IndexPlanner{pool: pool} }

// ReindexNeeded returns true when at least one live document has no active
// ready revision matching the configuration's chunk_size, chunk_overlap and
// embedding identity. Live documents with no revision at all also count:
// the corpus is not searchable under this configuration.
func (p *IndexPlanner) ReindexNeeded(ctx context.Context, cfg ragconfig.Config) (bool, error) {
	var missing int
	err := p.pool.QueryRow(ctx, `
		SELECT count(*)
		FROM documents d
		WHERE d.deleted_at IS NULL
		  AND NOT EXISTS (
			SELECT 1 FROM index_revisions r
			WHERE r.id = d.active_revision_id
			  AND r.status = 'ready'
			  AND r.chunk_size = $1 AND r.chunk_overlap = $2
			  AND r.embedding_provider = $3
			  AND r.embedding_model = $4
			  AND r.embedding_dimensions = $5
		  )`,
		cfg.ChunkSize, cfg.ChunkOverlap, cfg.EmbeddingProvider, cfg.EmbeddingModel, cfg.EmbeddingDimensions,
	).Scan(&missing)
	if err != nil {
		return false, err
	}
	return missing > 0, nil
}

// IndexDispatcher resubmits every live document under the configuration so
// document_reindex builds the compatible revisions. It is synchronous at
// dispatch level and reuses the existing per-document reprocess path (an
// in-flight revision fails cleanly instead of being hidden).
type IndexDispatcher struct {
	store                       *Store
	baseURL, username, password string
}

func NewIndexDispatcher(baseURL, user, password string, store *Store) *IndexDispatcher {
	return &IndexDispatcher{store: store, baseURL: baseURL, username: user, password: password}
}

// DispatchIndex triggers document_reindex for every live document under the
// config. Reuses the persisted reprocess+dispatch path; a failed dispatch
// surfaces to the caller (experiment combination records the failure) —
// never silently skipped.
func (d *IndexDispatcher) DispatchIndex(ctx context.Context, cfg ragconfig.Config) error {
	if d == nil || d.store == nil {
		return errors.New("index dispatcher is not wired")
	}
	docs, err := d.store.List(ctx)
	if err != nil {
		return fmt.Errorf("list documents for reindex: %w", err)
	}
	if len(docs) == 0 {
		return nil // nothing to prepare; evaluation will surface the empty corpus
	}
	airflow := NewAirflow(d.baseURL, d.username, d.password)
	for _, doc := range docs {
		updated, err := d.store.Reprocess(ctx, doc.ID, cfg.ID)
		if err != nil {
			// An active ingestion for another config may be in flight; the
			// experiment combination must not pretend readiness.
			return fmt.Errorf("reindex document %s: %w", doc.ID, err)
		}
		if updated.Job == nil || updated.Job.ID == "" {
			return fmt.Errorf("reindex document %s produced no job identity", doc.ID)
		}
		if err := airflow.Dispatch(ctx, *updated.Job); err != nil {
			_ = d.store.RecordDispatch(ctx, updated.Job.ID, err)
			return fmt.Errorf("dispatch reindex for document %s: %w", doc.ID, err)
		}
		_ = d.store.RecordDispatch(ctx, updated.Job.ID, nil)
	}
	return nil
}
