-- RB-02: chunks belonging to an index revision, with retrieval indexes.
--
-- Every chunk belongs to exactly one index revision; chunks are identified by
-- (revision, chunk_index) so retries cannot duplicate or overwrite evidence.
-- The embedding dimension is pinned to 1536 for the initially selected
-- embedding profile. Changing dimensions requires a new migration that alters
-- the column and rebuilds the retrieval index; it must also create new index
-- revisions because old and new vectors are not comparable.

CREATE TABLE doc_chunks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    index_revision_id UUID NOT NULL,
    -- Denormalized document ownership, kept consistent with the revision via
    -- a composite foreign key: a chunk can never point at a different
    -- document than the revision it belongs to.
    document_id UUID NOT NULL,
    chunk_index INTEGER NOT NULL
        CONSTRAINT doc_chunks_chunk_index_check CHECK (chunk_index >= 0),
    content TEXT NOT NULL
        CONSTRAINT doc_chunks_content_check CHECK (length(content) > 0),
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    -- Full-text retrieval support. The 'simple' configuration keeps parsing
    -- language-neutral for mixed-language documents.
    content_tsv TSVECTOR GENERATED ALWAYS AS (to_tsvector('simple', content)) STORED,
    -- NULL until the embedding stage fills it; retrieval must exclude NULLs.
    embedding VECTOR(1536),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT doc_chunks_id_document_id_key UNIQUE (index_revision_id, document_id),
    CONSTRAINT doc_chunks_revision_chunk_index_key UNIQUE (index_revision_id, chunk_index),
    CONSTRAINT doc_chunks_index_revision_id_document_id_fkey
        FOREIGN KEY (index_revision_id, document_id)
        REFERENCES index_revisions (id, document_id)
        ON DELETE CASCADE
);

CREATE INDEX doc_chunks_document_id_idx ON doc_chunks (document_id);
-- Full-text candidate retrieval (RB-17).
CREATE INDEX doc_chunks_content_tsv_idx ON doc_chunks USING gin (content_tsv);
-- Vector retrieval with cosine distance over the pinned dimension.
CREATE INDEX doc_chunks_embedding_hnsw_idx
    ON doc_chunks USING hnsw (embedding vector_cosine_ops);
