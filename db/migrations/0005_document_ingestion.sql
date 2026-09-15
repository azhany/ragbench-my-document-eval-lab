-- RB-05: durable dispatch and revision configuration. Correct RB-02's accidental
-- one-chunk-per-revision constraint; the ordered chunk key remains unique.
ALTER TABLE doc_chunks DROP CONSTRAINT doc_chunks_id_document_id_key;
ALTER TABLE index_revisions ADD COLUMN config_id UUID REFERENCES rag_configs(id);
ALTER TABLE index_revisions ADD COLUMN chunk_unit TEXT NOT NULL DEFAULT 'unicode_characters'
    CHECK (chunk_unit = 'unicode_characters');
ALTER TABLE index_revisions ADD CONSTRAINT index_revisions_overlap_check CHECK (chunk_overlap < chunk_size);
ALTER TABLE documents ADD COLUMN size_bytes BIGINT CHECK (size_bytes >= 0);
ALTER TABLE documents ADD COLUMN deleted_at TIMESTAMPTZ;
ALTER TABLE documents ADD COLUMN active_revision_id UUID;
ALTER TABLE documents ADD COLUMN latest_revision_id UUID;
ALTER TABLE documents ADD CONSTRAINT documents_active_revision_fkey
    FOREIGN KEY (active_revision_id, id) REFERENCES index_revisions(id, document_id);
ALTER TABLE documents ADD CONSTRAINT documents_latest_revision_fkey
    FOREIGN KEY (latest_revision_id, id) REFERENCES index_revisions(id, document_id);
-- A deleted upload can be uploaded anew, while historical evidence is retained.
DROP INDEX documents_checksum_key;
CREATE UNIQUE INDEX documents_live_checksum_key ON documents(checksum) WHERE deleted_at IS NULL;

CREATE TABLE ingestion_jobs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    index_revision_id UUID NOT NULL UNIQUE REFERENCES index_revisions(id),
    dag_id TEXT NOT NULL CHECK (dag_id IN ('document_ingestion', 'document_reindex')),
    run_id TEXT NOT NULL UNIQUE,
    state TEXT NOT NULL DEFAULT 'dispatch_pending' CHECK (state IN
        ('dispatch_pending','dispatch_failed','queued','processing','succeeded','failed','cancelled')),
    stage TEXT NOT NULL DEFAULT 'dispatch',
    error_code TEXT,
    error_message TEXT,
    dispatch_attempts INTEGER NOT NULL DEFAULT 0,
    dispatched_at TIMESTAMPTZ,
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    stage_metrics JSONB NOT NULL DEFAULT '{}'::jsonb,
    -- Internal stage handoff, never exposed by the API or placed in XCom.
    artifacts JSONB NOT NULL DEFAULT '{}'::jsonb
);
CREATE INDEX ingestion_jobs_state_idx ON ingestion_jobs(state);
CREATE UNIQUE INDEX index_revisions_one_pending_document ON index_revisions(document_id) WHERE status = 'pending';
