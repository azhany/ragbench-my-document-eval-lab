-- RB-25/RB-26: executable reranking configuration and versioned graded
-- retrieval judgments. Existing rows remain valid: disabled reranking uses
-- the local lexical-v1 profile only when explicitly enabled, while existing
-- evaluation cases have no graded judgments and therefore return nDCG NULL.

ALTER TABLE rag_configs
    ADD COLUMN reranker_profile TEXT NOT NULL DEFAULT 'lexical-v1'
        CONSTRAINT rag_configs_reranker_profile_check
        CHECK (length(btrim(reranker_profile)) > 0),
    ADD COLUMN rerank_candidate_limit INTEGER NOT NULL DEFAULT 20
        CONSTRAINT rag_configs_rerank_limit_check
        CHECK (rerank_candidate_limit BETWEEN 1 AND 100);

ALTER TABLE eval_cases
    ADD COLUMN judgment_version TEXT NOT NULL DEFAULT 'binary-v1'
        CONSTRAINT eval_cases_judgment_version_check
        CHECK (length(btrim(judgment_version)) > 0),
    ADD COLUMN graded_judgments JSONB NOT NULL DEFAULT '{}'::jsonb
        CONSTRAINT eval_cases_graded_judgments_check
        CHECK (jsonb_typeof(graded_judgments) = 'object');

ALTER TABLE eval_results
    ADD COLUMN ndcg_k REAL;

ALTER TABLE rag_spans DROP CONSTRAINT rag_spans_name_check;
ALTER TABLE rag_spans ADD CONSTRAINT rag_spans_name_check CHECK (span_name IN
    ('request', 'query_embedding', 'retrieval', 'rerank', 'prompt_build',
     'llm_generation', 'citation_mapping'));
