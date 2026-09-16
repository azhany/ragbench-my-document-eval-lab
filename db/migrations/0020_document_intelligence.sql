-- Sprint 7 (RB-27--RB-31): durable financial-document analysis workflow.
--
-- An analysis references the existing immutable source revision and document
-- identity. Airflow owns task scheduling, but all reviewer-visible state and
-- evidence lives here so restart/retry cannot fabricate a completed result.

CREATE TABLE document_analyses (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    document_id UUID NOT NULL,
    source_revision_id UUID NOT NULL,
    source_checksum TEXT NOT NULL,
    config_id UUID NOT NULL REFERENCES rag_configs(id),
    trace_id TEXT NOT NULL UNIQUE,
    job_id UUID NOT NULL UNIQUE,
    dag_id TEXT NOT NULL DEFAULT 'document_intelligence'
        CHECK (dag_id = 'document_intelligence'),
    run_id TEXT NOT NULL UNIQUE,
    status TEXT NOT NULL DEFAULT 'queued'
        CHECK (status IN ('queued', 'processing', 'completed', 'partial', 'failed', 'cancelled')),
    job_state TEXT NOT NULL DEFAULT 'dispatch_pending'
        CHECK (job_state IN ('dispatch_pending', 'dispatch_failed', 'queued', 'processing', 'succeeded', 'failed', 'cancelled')),
    stage TEXT NOT NULL DEFAULT 'dispatch'
        CHECK (stage IN ('dispatch', 'extract_text', 'structured_extract', 'schema_validate', 'financial_validate', 'summarize')),
    dispatch_attempts INTEGER NOT NULL DEFAULT 0 CHECK (dispatch_attempts >= 0),
    dispatched_at TIMESTAMPTZ,
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,

    extraction_method TEXT,
    extraction_profile TEXT,
    schema_version TEXT NOT NULL,
    prompt_version TEXT NOT NULL,
    summary_prompt_version TEXT NOT NULL,
    model_profile TEXT NOT NULL,
    validation_policy_version TEXT NOT NULL,

    -- Evidence is normalized text plus bounded source locations. Raw model
    -- output is retained separately for malformed-output diagnosis and is
    -- never returned in the public analysis response.
    extraction_evidence JSONB NOT NULL DEFAULT '{}'::jsonb,
    raw_model_output TEXT,
    structured_data JSONB,
    schema_errors JSONB NOT NULL DEFAULT '[]'::jsonb,
    validation_status TEXT CHECK (validation_status IN ('pass', 'warning', 'failure')),
    validation_findings JSONB NOT NULL DEFAULT '[]'::jsonb,
    validation_policy JSONB NOT NULL DEFAULT '{}'::jsonb,
    summary TEXT,
    prompt_snapshot JSONB NOT NULL DEFAULT '{}'::jsonb,

    stage_metrics JSONB NOT NULL DEFAULT '{}'::jsonb,
    tool_events JSONB NOT NULL DEFAULT '[]'::jsonb,
    input_tokens INTEGER CHECK (input_tokens IS NULL OR input_tokens >= 0),
    output_tokens INTEGER CHECK (output_tokens IS NULL OR output_tokens >= 0),
    estimated_cost NUMERIC(12,6),
    cost_currency TEXT,
    pricing_version TEXT,
    cost_components JSONB NOT NULL DEFAULT '{}'::jsonb,
    retry_count INTEGER NOT NULL DEFAULT 0 CHECK (retry_count >= 0),
    last_retry_reason TEXT,
    error_code TEXT,
    error_message TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT document_analyses_source_owner_fkey
        FOREIGN KEY (source_revision_id, document_id)
        REFERENCES index_revisions(id, document_id),
    CONSTRAINT document_analyses_source_checksum_check CHECK (length(source_checksum) = 64),
    CONSTRAINT document_analyses_terminal_state_check CHECK (
        (status = 'completed' AND job_state = 'succeeded' AND stage = 'summarize' AND summary IS NOT NULL AND error_code IS NULL)
        OR (status IN ('queued', 'processing', 'partial', 'failed', 'cancelled'))
    )
);

CREATE INDEX document_analyses_document_idx ON document_analyses(document_id, created_at DESC);
CREATE INDEX document_analyses_status_idx ON document_analyses(status, updated_at DESC);

CREATE TABLE document_analysis_spans (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    analysis_id UUID NOT NULL REFERENCES document_analyses(id) ON DELETE CASCADE,
    trace_id TEXT NOT NULL,
    span_name TEXT NOT NULL CHECK (span_name IN
        ('request', 'extract_text', 'structured_extract', 'schema_validate', 'financial_validate', 'summarize')),
    started_at TIMESTAMPTZ NOT NULL,
    duration_ms INTEGER NOT NULL CHECK (duration_ms >= 0),
    status TEXT NOT NULL CHECK (status IN ('accepted', 'processing', 'succeeded', 'failed', 'completed')),
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb
);

CREATE INDEX document_analysis_spans_trace_idx
    ON document_analysis_spans(analysis_id, started_at, id);
