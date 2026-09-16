-- RB-14: durable evaluation runs; RB-15 adds the separate scoring columns.
--
-- A run pins its inputs at creation: the dataset version, the immutable
-- rag_config identity, the exact corpus/index revisions that are readable
-- under that configuration's embedding identity, and the evaluator policy
-- (rubric versions, scoring K) — so a later reindex never silently changes
-- what an old run measured.
--
-- eval_results carries one row per attempted case with CASE-level status:
--   completed        query succeeded, scoring recorded
--   failed           query pipeline returned a classified error
--   evaluator_failed generation succeeded but scoring could not produce
--                    a judgment (e.g. judge model failure) — NOT low quality
-- Idempotency: UNIQUE (run_id, eval_case_id) makes Airflow retries safe; a
-- retried execute of an already-final case returns the stored row untouched.

CREATE TABLE eval_runs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    dataset_id UUID NOT NULL REFERENCES eval_datasets(id),
    dataset_version INTEGER NOT NULL,
    rag_config_id UUID NOT NULL REFERENCES rag_configs(id),
    -- Snapshot of the corpus at creation: per-document source filename,
    -- checksum, active ready revision id and chunk settings readable with
    -- the pinned config. NULL when no compatible revision existed then.
    corpus_revisions JSONB,
    -- Evaluator policy pinned at creation: rubric versions, scoring K.
    evaluator_policy JSONB NOT NULL,
    status TEXT NOT NULL
        CONSTRAINT eval_runs_status_check
        CHECK (status IN ('created', 'running', 'completed', 'partial', 'failed', 'dispatch_failed')),
    -- Airflow dispatch identity; dispatch_failed runs carry the reason in
    -- dispatch_error. A failed dispatch is a visible failure, never success.
    dag_run_id TEXT,
    dispatch_attempts INTEGER NOT NULL DEFAULT 0,
    dispatch_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Correlation shorthand for the public run id used in evaluation traces.
CREATE UNIQUE INDEX eval_runs_dag_run_key ON eval_runs (dag_run_id) WHERE dag_run_id IS NOT NULL;

CREATE TABLE eval_results (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id UUID NOT NULL REFERENCES eval_runs(id),
    eval_case_id UUID NOT NULL, -- dataset_version-scoped; no FK: versioned data
    case_key TEXT NOT NULL,
    status TEXT NOT NULL
        CONSTRAINT eval_results_status_check
        CHECK (status IN ('completed', 'failed', 'evaluator_failed')),
    -- The immutable dataset version this row was scored against.
    dataset_version INTEGER NOT NULL,
    -- Query pipeline outcome; links every generated answer to its trace.
    trace_id TEXT UNIQUE, -- public rag_traces.trace_id, NULL on pre-query failure
    query_error_code TEXT,
    query_error_message TEXT,
    -- Retrieval scoring (RB-15); NULL = not evaluable, never zero.
    recall_k REAL,
    mrr REAL,
    -- Judge outputs; NULL = not judged, never zero. rationale is verbatim.
    answer_relevance INTEGER,
    answer_relevance_rationale TEXT,
    groundedness INTEGER,
    groundedness_rationale TEXT,
    citation_correct REAL,
    -- Scoring bookkeeping
    scores JSONB NOT NULL DEFAULT '{}'::jsonb,
    evaluator_error TEXT,
    evaluator_input_tokens INTEGER,
    evaluator_output_tokens INTEGER,
    evaluator_cost NUMERIC(12,6),
    evaluator_cost_currency TEXT,
    evaluator_pricing_version TEXT,
    -- Operational aggregates copied from the trace so run inspection never
    -- needs to re-join rag_traces to compute latency/tokens/cost.
    total_latency_ms INTEGER,
    retrieval_latency_ms INTEGER,
    generation_latency_ms INTEGER,
    input_tokens INTEGER,
    output_tokens INTEGER,
    embedding_input_tokens INTEGER,
    estimated_cost NUMERIC(12,6),
    cost_currency TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT eval_results_one_attempt UNIQUE (run_id, eval_case_id),
    CONSTRAINT eval_results_outcome_state_check CHECK (
        (status = 'completed' AND trace_id IS NOT NULL AND query_error_code IS NULL)
        OR (status = 'failed' AND query_error_code IS NOT NULL)
        OR (status = 'evaluator_failed' AND trace_id IS NOT NULL))
);

CREATE INDEX eval_results_run_idx ON eval_results (run_id);

-- Evaluation cases run through the same pipeline as chat with request
-- attribution, so traces carry the new evaluation request type.
ALTER TABLE rag_traces
    DROP CONSTRAINT rag_traces_request_type_check,
    ADD CONSTRAINT rag_traces_request_type_check
        CHECK (request_type IN ('chat', 'evaluation'));
