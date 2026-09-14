-- RB-02: document registry and index revisions.
--
-- documents is the durable record of an uploaded file. index_revisions pins the
-- exact chunk settings and embedding profile used to build a searchable version
-- of a document: chunk-size or embedding changes create a new revision instead
-- of overwriting evidence referenced by historical traces and evaluations.

CREATE TABLE documents (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    filename TEXT NOT NULL,
    mime_type TEXT NOT NULL,
    storage_path TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'queued'
        CONSTRAINT documents_status_check
        CHECK (status IN ('queued', 'processing', 'processed', 'failed')),
    -- SHA-256 of the uploaded bytes. The same bytes are the same document;
    -- duplicate uploads are rejected instead of silently duplicated.
    checksum TEXT NOT NULL,
    -- Published chunk count; NULL until a revision is successfully indexed.
    chunk_count INTEGER
        CONSTRAINT documents_chunk_count_check CHECK (chunk_count >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX documents_checksum_key ON documents (checksum);
CREATE INDEX documents_status_idx ON documents (status);

CREATE TABLE index_revisions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    document_id UUID NOT NULL REFERENCES documents (id) ON DELETE CASCADE,
    -- Per-document monotonic revision number; reprocessing appends, never overwrites.
    revision_number INTEGER NOT NULL
        CONSTRAINT index_revisions_revision_number_check CHECK (revision_number >= 1),
    -- Checksum of the source bytes this revision was built from.
    source_checksum TEXT NOT NULL,
    -- Chunk settings that produced this revision's chunks.
    chunk_size INTEGER NOT NULL
        CONSTRAINT index_revisions_chunk_size_check CHECK (chunk_size > 0),
    chunk_overlap INTEGER NOT NULL
        CONSTRAINT index_revisions_chunk_overlap_check CHECK (chunk_overlap >= 0),
    -- Embedding compatibility identity; retrieval must never mix revisions
    -- with incompatible embedding profiles in one query.
    embedding_provider TEXT NOT NULL,
    embedding_model TEXT NOT NULL,
    embedding_dimensions INTEGER NOT NULL
        CONSTRAINT index_revisions_embedding_dimensions_check CHECK (embedding_dimensions > 0),
    status TEXT NOT NULL DEFAULT 'pending'
        CONSTRAINT index_revisions_status_check
        CHECK (status IN ('pending', 'ready', 'failed')),
    error_code TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at TIMESTAMPTZ,
    CONSTRAINT index_revisions_document_revision_number_key
        UNIQUE (document_id, revision_number),
    -- Target for the composite foreign key from doc_chunks, which pins each
    -- chunk to the document its revision belongs to.
    CONSTRAINT index_revisions_id_document_id_key UNIQUE (id, document_id),
    -- Ready revisions record when they were published and carry no error;
    -- failed revisions must say why; pending revisions are neither.
    CONSTRAINT index_revisions_state_check CHECK (
        (status = 'ready' AND published_at IS NOT NULL AND error_code IS NULL)
        OR (status = 'failed' AND published_at IS NULL AND error_code IS NOT NULL)
        OR (status = 'pending' AND published_at IS NULL AND error_code IS NULL)
    )
);

CREATE INDEX index_revisions_document_id_idx ON index_revisions (document_id);
-- Retrieval selects the compatible ready revision of a document.
CREATE INDEX index_revisions_ready_idx
    ON index_revisions (document_id, embedding_dimensions)
    WHERE status = 'ready';
