-- RB-11: durable query traces and stage spans.
--
-- Every chat (and, from Sprint 4, evaluation) request persists one trace with
-- the exact evidence it used and the usage/cost the provider reported, so an
-- answer can always be explained — including when it failed.
--
-- Snapshot policy:
--   - The effective RAG configuration is NOT duplicated here: rag_configs is
--     an immutable identity with no update or delete, so rag_traces.rag_config_id
--     always resolves to the exact settings that produced the run.
--   - The rendered prompt text and the ranked context actually sent ARE
--     snapshotted, because they are built per request and must stay
--     reconstructable even after prompt versions evolve.
--   - Unknown pricing or missing provider usage makes estimated_cost NULL —
--     never a fabricated zero — and the reason is recorded in cost_components.
--   - Source bytes, chunks and revisions referenced here are retained
--     indefinitely by the documented tombstone policy, so historical traces
--     stay inspectable after reprocess/delete.

CREATE TABLE rag_traces (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    -- Public trace identifier (the UUID for chat; evaluation traces get a
    -- deterministic run-case id later). One durable row per request.
    trace_id TEXT NOT NULL UNIQUE,
    request_type TEXT NOT NULL
        CONSTRAINT rag_traces_request_type_check CHECK (request_type IN ('chat')),
    question TEXT NOT NULL,
    rag_config_id UUID NOT NULL REFERENCES rag_configs(id),
    success BOOLEAN NOT NULL,
    -- Documented failure taxonomy (MONITORING.md); NULL exactly when success.
    error_code TEXT,
    error_message TEXT,
    total_latency_ms INTEGER NOT NULL
        CONSTRAINT rag_traces_total_latency_check CHECK (total_latency_ms >= 0),
    -- Generation usage as reported by the provider; NULL = not reported.
    input_tokens INTEGER,
    output_tokens INTEGER,
    -- Query-embedding usage component; NULL = provider did not report it.
    embedding_input_tokens INTEGER,
    -- NULL = cost unavailable (unknown pricing or missing usage), never zero.
    estimated_cost NUMERIC(12,6),
    -- Native currency of the pricing table; NULL while cost is unavailable.
    cost_currency TEXT,
    pricing_version TEXT,
    -- Per-component breakdown on success, or {"available":false,"reason":...}.
    cost_components JSONB NOT NULL DEFAULT '{}'::jsonb,
    -- Grounded answer text; NULL whenever no answer was produced (failures
    -- must not carry a fabricated fallback answer).
    answer TEXT,
    citations JSONB,
    prompt_snapshot JSONB NOT NULL,   -- {prompt_version, prompt_identifier, prompt_text}
    context_snapshot JSONB NOT NULL,  -- ranked evidence actually sent (empty on retrieval_empty)
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT rag_traces_error_state_check CHECK (
        (success AND error_code IS NULL) OR (NOT success AND error_code IS NOT NULL))
);

CREATE INDEX rag_traces_created_at_idx ON rag_traces (created_at DESC);
CREATE INDEX rag_traces_config_idx ON rag_traces (rag_config_id);

CREATE TABLE rag_spans (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    trace_id UUID NOT NULL REFERENCES rag_traces(id) ON DELETE CASCADE,
    span_name TEXT NOT NULL CONSTRAINT rag_spans_name_check CHECK (span_name IN
        ('request', 'query_embedding', 'retrieval', 'prompt_build',
         'llm_generation', 'citation_mapping')),
    started_at TIMESTAMPTZ NOT NULL,
    duration_ms INTEGER NOT NULL CONSTRAINT rag_spans_duration_check CHECK (duration_ms >= 0),
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb
);

CREATE INDEX rag_spans_trace_idx ON rag_spans (trace_id, started_at);
