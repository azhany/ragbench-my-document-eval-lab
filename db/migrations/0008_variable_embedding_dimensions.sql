-- RB-07/RB-09: support multiple persisted embedding profiles.
--
-- The original vector(1536) column made the provider abstraction nominal:
-- every non-OpenAI embedding model with a different native width failed at
-- PostgreSQL before profile isolation could do its job. A typmod-free vector
-- keeps dimensions on each row, while publication and searchable_chunks still
-- enforce the revision/config provider, model and dimension identity.

DROP INDEX doc_chunks_embedding_hnsw_idx;

ALTER TABLE doc_chunks
    ALTER COLUMN embedding TYPE vector
    USING embedding::vector;

-- Keep ANN acceleration for each executable profile. New profile dimensions
-- require an explicit companion index migration; correctness still comes from
-- the persisted identity and publication checks.
CREATE INDEX doc_chunks_embedding_1536_hnsw_idx
    ON doc_chunks USING hnsw ((embedding::vector(1536)) vector_cosine_ops)
    WHERE embedding IS NOT NULL AND vector_dims(embedding) = 1536;

CREATE INDEX doc_chunks_embedding_384_hnsw_idx
    ON doc_chunks USING hnsw ((embedding::vector(384)) vector_cosine_ops)
    WHERE embedding IS NOT NULL AND vector_dims(embedding) = 384;
