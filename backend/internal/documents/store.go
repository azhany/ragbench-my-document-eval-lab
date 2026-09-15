// Package documents owns durable document and dispatch state. Batch processing
// is exclusively performed by Airflow against these persisted revisions.
package documents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound     = errors.New("document not found")
	ErrConfig       = errors.New("saved config_id is required")
	ErrDuplicate    = errors.New("these bytes already belong to a live document")
	ErrActive       = errors.New("document already has active ingestion; retry dispatch if it failed")
	ErrNotRetryable = errors.New("only pending or failed dispatch can be retried; reprocess a completed job")
)

type Upload struct {
	ID, Filename, MIMEType, Path, Checksum, ConfigID string
	Size                                             int64
}
type Job struct {
	ID               string          `json:"id"`
	RevisionID       string          `json:"index_revision_id"`
	DAGID            string          `json:"dag_id"`
	RunID            string          `json:"run_id"`
	State            string          `json:"state"`
	Stage            string          `json:"stage"`
	ErrorCode        *string         `json:"error_code"`
	ErrorMessage     *string         `json:"error_message"`
	DispatchAttempts int             `json:"dispatch_attempts"`
	DispatchedAt     *string         `json:"dispatched_at"`
	StartedAt        *string         `json:"started_at"`
	FinishedAt       *string         `json:"finished_at"`
	StageMetrics     json.RawMessage `json:"stage_metrics"`
}
type Document struct {
	ID               string            `json:"id"`
	Filename         string            `json:"filename"`
	MIMEType         string            `json:"mime_type"`
	Status           string            `json:"status"`
	Checksum         string            `json:"checksum"`
	Size             int64             `json:"size_bytes"`
	ChunkCount       *int              `json:"chunk_count"`
	ActiveRevisionID *string           `json:"active_revision_id"`
	LatestRevisionID *string           `json:"latest_revision_id"`
	ConfigID         *string           `json:"config_id"`
	CreatedAt        string            `json:"created_at"`
	UpdatedAt        string            `json:"updated_at"`
	Job              *Job              `json:"job"`
	Revisions        []json.RawMessage `json:"revisions,omitempty"`
}
type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Build the public JSON in SQL so optional job state is consistent in one snapshot.
const documentSelect = `SELECT jsonb_build_object(
 'id',d.id,'filename',d.filename,'mime_type',d.mime_type,'status',d.status,
 'checksum',d.checksum,'size_bytes',COALESCE(d.size_bytes,0),'chunk_count',d.chunk_count,
 'active_revision_id',d.active_revision_id,'latest_revision_id',d.latest_revision_id,
 'config_id',r.config_id,'created_at',d.created_at,'updated_at',d.updated_at,
 'job',CASE WHEN j.id IS NULL THEN NULL ELSE to_jsonb(j)-'artifacts' END)
 FROM documents d LEFT JOIN index_revisions r ON r.id=d.latest_revision_id
 LEFT JOIN ingestion_jobs j ON j.index_revision_id=r.id`

func (s *Store) Get(ctx context.Context, id string) (Document, error) {
	if _, err := uuid.Parse(id); err != nil {
		return Document{}, ErrNotFound
	}
	var raw []byte
	err := s.pool.QueryRow(ctx, documentSelect+` WHERE d.id=$1 AND d.deleted_at IS NULL`, id).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return Document{}, ErrNotFound
	}
	if err != nil {
		return Document{}, err
	}
	var d Document
	err = json.Unmarshal(raw, &d)
	if err != nil {
		return d, err
	}
	rows, err := s.pool.Query(ctx, `SELECT to_jsonb(r) || jsonb_build_object('job',to_jsonb(j)-'artifacts')
	 FROM index_revisions r LEFT JOIN ingestion_jobs j ON j.index_revision_id=r.id
	 WHERE r.document_id=$1 ORDER BY r.revision_number DESC`, id)
	if err != nil {
		return d, err
	}
	defer rows.Close()
	for rows.Next() {
		var revision json.RawMessage
		if err = rows.Scan(&revision); err != nil {
			return d, err
		}
		d.Revisions = append(d.Revisions, revision)
	}
	return d, rows.Err()
}
func (s *Store) List(ctx context.Context) ([]Document, error) {
	rows, err := s.pool.Query(ctx, documentSelect+` WHERE d.deleted_at IS NULL ORDER BY d.created_at DESC,d.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	docs := []Document{}
	for rows.Next() {
		var raw []byte
		var d Document
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &d); err != nil {
			return nil, err
		}
		docs = append(docs, d)
	}
	return docs, rows.Err()
}
func createRevision(ctx context.Context, tx pgx.Tx, id, configID, dagID string) (Job, error) {
	if _, err := uuid.Parse(configID); err != nil {
		return Job{}, ErrConfig
	}
	rev := uuid.NewString()
	tag, err := tx.Exec(ctx, `INSERT INTO index_revisions(id,document_id,revision_number,source_checksum,
 chunk_size,chunk_overlap,embedding_provider,embedding_model,embedding_dimensions,config_id)
 SELECT $1,d.id,COALESCE((SELECT MAX(revision_number) FROM index_revisions WHERE document_id=d.id),0)+1,
 d.checksum,c.chunk_size,c.chunk_overlap,c.embedding_provider,c.embedding_model,c.embedding_dimensions,c.id
 FROM documents d CROSS JOIN rag_configs c WHERE d.id=$2 AND c.id=$3`, rev, id, configID)
	if err != nil {
		return Job{}, err
	}
	if tag.RowsAffected() == 0 {
		return Job{}, ErrConfig
	}
	j := Job{ID: uuid.NewString(), RevisionID: rev, DAGID: dagID, RunID: "rb_" + rev, State: "dispatch_pending", Stage: "dispatch"}
	_, err = tx.Exec(ctx, `INSERT INTO ingestion_jobs(id,index_revision_id,dag_id,run_id) VALUES($1,$2,$3,$4)`, j.ID, rev, dagID, j.RunID)
	if err != nil {
		return Job{}, err
	}
	_, err = tx.Exec(ctx, `UPDATE documents SET latest_revision_id=$2,status='queued',updated_at=now() WHERE id=$1`, id, rev)
	return j, err
}
func (s *Store) Create(ctx context.Context, u Upload) (Document, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Document{}, err
	}
	defer tx.Rollback(context.Background())
	_, err = tx.Exec(ctx, `INSERT INTO documents(id,filename,mime_type,storage_path,checksum,size_bytes) VALUES($1,$2,$3,$4,$5,$6)`, u.ID, u.Filename, u.MIMEType, u.Path, u.Checksum, u.Size)
	if err != nil {
		var pe *pgconn.PgError
		if errors.As(err, &pe) && pe.Code == "23505" {
			return Document{}, ErrDuplicate
		}
		return Document{}, err
	}
	if _, err = createRevision(ctx, tx, u.ID, u.ConfigID, "document_ingestion"); err != nil {
		return Document{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Document{}, fmt.Errorf("commit upload: %w", err)
	}
	return s.Get(ctx, u.ID)
}

// RecordDispatch only updates dispatch states: an exceptionally fast task may
// already be processing or complete by the time the HTTP trigger returns.
func (s *Store) RecordDispatch(ctx context.Context, id string, dispatchErr error) error {
	state := "queued"
	var code, message *string
	if dispatchErr != nil {
		state = "dispatch_failed"
		c, m := "dispatch_failed", dispatchErr.Error()
		code, message = &c, &m
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	// Use the worker/delete lock order (document, then job) to avoid deadlocks.
	var documentID string
	if err = tx.QueryRow(ctx, `SELECT d.id FROM documents d JOIN index_revisions r ON r.document_id=d.id
	 JOIN ingestion_jobs j ON j.index_revision_id=r.id WHERE j.id=$1 FOR UPDATE OF d`, id).Scan(&documentID); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `WITH changed AS (
 UPDATE ingestion_jobs SET state=$2,error_code=$3,error_message=$4,
 dispatch_attempts=dispatch_attempts+1,updated_at=now(),
 dispatched_at=CASE WHEN $2='queued' THEN now() ELSE dispatched_at END
	 WHERE id=$1 AND (state IN ('dispatch_pending','dispatch_failed') OR (state='queued' AND $2='queued')) RETURNING index_revision_id)
 UPDATE documents SET status=CASE WHEN $2='queued' THEN 'queued' ELSE 'failed' END,updated_at=now()
 WHERE latest_revision_id IN (SELECT index_revision_id FROM changed) AND deleted_at IS NULL`, id, state, code, message)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) Reprocess(ctx context.Context, id, configID string) (Document, error) {
	if _, err := uuid.Parse(id); err != nil {
		return Document{}, ErrNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Document{}, err
	}
	defer tx.Rollback(context.Background())
	var found string
	if err = tx.QueryRow(ctx, `SELECT id FROM documents WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, id).Scan(&found); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Document{}, ErrNotFound
		}
		return Document{}, err
	}
	var active bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM index_revisions WHERE document_id=$1 AND status='pending')`, id).Scan(&active)
	if err != nil {
		return Document{}, err
	}
	if active {
		return Document{}, ErrActive
	}
	if _, err = createRevision(ctx, tx, id, configID, "document_reindex"); err != nil {
		return Document{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Document{}, err
	}
	return s.Get(ctx, id)
}

// Tombstones and cancellation commit together. Bytes, revisions, chunks and
// config identities are retained for historical citations; workers recheck the
// tombstone under the same document row lock before every durable write.
func (s *Store) Delete(ctx context.Context, id string) error {
	if _, err := uuid.Parse(id); err != nil {
		return ErrNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	var found string
	if err = tx.QueryRow(ctx, `SELECT id FROM documents WHERE id=$1 FOR UPDATE`, id).Scan(&found); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE documents SET deleted_at=COALESCE(deleted_at,now()),updated_at=now() WHERE id=$1`, id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE ingestion_jobs SET state='cancelled',finished_at=now(),updated_at=now(),
	 error_code='document_deleted',error_message='Document deleted; ingestion output is discarded'
	 WHERE index_revision_id IN (SELECT id FROM index_revisions WHERE document_id=$1)
	 AND state NOT IN ('succeeded','cancelled','failed')`, id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE index_revisions SET status='failed',error_code='document_deleted' WHERE document_id=$1 AND status='pending'`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
