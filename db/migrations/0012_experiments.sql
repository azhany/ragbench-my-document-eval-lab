-- RB-18: bounded parameter experiments.
--
-- An experiment persists its requested matrix dimensions and the expanded
-- combination list BEFORE execution, with an explicit configured
-- combination limit. Each combination creates its own immutable rag_config
-- identity (RB-03) and its own eval run (RB-14), so two-config sweeps are
-- separately traceable. Retries reuse created identities (no duplicate
-- logical experiments/runs); partial matrix failures retain successful
-- run links instead of hiding them.

CREATE TABLE experiments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL
        CONSTRAINT experiments_name_check
        CHECK (length(btrim(name)) BETWEEN 1 AND 200),
    description TEXT,
    dataset_id UUID NOT NULL REFERENCES eval_datasets(id),
    dataset_version INTEGER NOT NULL,
    -- Evaluator policy pinned for every run of this experiment (RB-14).
    rubric_version TEXT NOT NULL,
    scoring_k INTEGER NOT NULL,
    -- Requested matrix dimensions exactly as submitted:
    -- {"chunk_sizes": [...], "chunk_overlaps": [...], "top_ks": [...],
    --  "retrieval_modes": [...], "prompt_versions": [...],
    --  "model_profiles": [...], "rerank_enabled": [...],
    --  "reranker_profiles": [...], "rerank_candidate_limits": [...]}.
    requested_matrix JSONB NOT NULL,
    -- Explicit expansion cap; submissions above it are rejected.
    combination_limit INTEGER NOT NULL
        CONSTRAINT experiments_limit_check
        CHECK (combination_limit BETWEEN 1 AND 64),
    status TEXT NOT NULL
        CONSTRAINT experiments_status_check
        CHECK (status IN ('created', 'running', 'completed', 'partial', 'failed', 'dispatch_failed')),
    dag_run_id TEXT,
    dispatch_attempts INTEGER NOT NULL DEFAULT 0,
    dispatch_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX experiments_name_key ON experiments (name);

CREATE TABLE experiment_combinations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    experiment_id UUID NOT NULL REFERENCES experiments(id),
    combination_index INTEGER NOT NULL
        CONSTRAINT experiment_combinations_index_check
        CHECK (combination_index >= 1),
    -- The expanded settings for ONE matrix cell:
    -- {"chunk_size": n, "chunk_overlap": n, "top_k": n,
    --  "retrieval_mode": "...", "prompt_version": "...", "model_profile": "..."}
    settings JSONB NOT NULL,
    -- The immutable config identity created for this combination; NULL
    -- until the first successful advance step creates it.
    rag_config_id UUID REFERENCES rag_configs(id),
    -- Index preparation state per combination; a failed reindex keeps
    -- index_ready false and blocks evaluation against the wrong corpus.
    index_dispatched BOOLEAN NOT NULL DEFAULT false,
    index_ready BOOLEAN NOT NULL DEFAULT false,
    index_error TEXT,
    eval_run_id UUID REFERENCES eval_runs(id),
    eval_run_error TEXT,
    -- Cached orchestration view of the linked eval run's status.
    eval_run_status TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT experiment_combinations_key UNIQUE (experiment_id, combination_index)
);

CREATE INDEX experiment_combinations_experiment_idx ON experiment_combinations (experiment_id);
