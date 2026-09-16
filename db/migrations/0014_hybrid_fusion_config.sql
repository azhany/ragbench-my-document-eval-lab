-- RB-17: configurable hybrid retrieval.
--
-- Retrieval mode 'hybrid' was reserved at RB-03 without execution support.
-- This migration adds the persisted fusion constants so vector/hybrid
-- selection changes actual execution and experiments (RB-18 comparisons,
-- traces) can reproduce the exact merge: reciprocal rank fusion with the
-- recorded rank constant and per-branch candidate limits.
--
-- Vector-mode configurations are unaffected: these columns exist for
-- identity completeness and only hybrid execution applies them.

ALTER TABLE rag_configs
    ADD COLUMN fusion_method TEXT NOT NULL DEFAULT 'rrf'
        CONSTRAINT rag_configs_fusion_method_check CHECK (fusion_method IN ('rrf')),
    ADD COLUMN rrf_rank_constant REAL NOT NULL DEFAULT 60
        CONSTRAINT rag_configs_rrf_k_check CHECK (rrf_rank_constant BETWEEN 1 AND 1000),
    ADD COLUMN fts_candidate_limit INTEGER NOT NULL DEFAULT 20
        CONSTRAINT rag_configs_fts_limit_check CHECK (fts_candidate_limit BETWEEN 1 AND 100),
    ADD COLUMN vector_candidate_limit INTEGER NOT NULL DEFAULT 20
        CONSTRAINT rag_configs_vector_limit_check CHECK (vector_candidate_limit BETWEEN 1 AND 100);
