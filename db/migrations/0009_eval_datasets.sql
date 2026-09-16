-- RB-13: versioned golden datasets.
--
-- A dataset is a named container whose cases live in immutable numbered
-- versions. Editing cases always creates a new version; eval runs pin one
-- version (RB-14), so old runs keep pointing at the exact rows they scored.
--
-- Relevance unit policy (documented, stable across chunk configurations):
-- expected evidence references document identities only. Chunk index or
-- chunk UUID references would silently break when a different chunk_size
-- re-chunks the same source bytes, so this PoC pins the document ("which
-- stored source contains the expected evidence") as the relevance unit.
-- Recall@K and MRR (RB-15) score at this unit; a different granularity
-- requires an explicit remapping, never treated stale chunk UUIDs as ground
-- truth for another index revision.

CREATE TABLE eval_datasets (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL
        CONSTRAINT eval_datasets_name_check
        CHECK (length(btrim(name)) BETWEEN 1 AND 200),
    description TEXT,
    -- Highest immutable version with cases; always >= 1 once created.
    latest_version INTEGER NOT NULL
        CONSTRAINT eval_datasets_version_check
        CHECK (latest_version BETWEEN 1 AND 9999),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX eval_datasets_name_key ON eval_datasets (name);

CREATE TABLE eval_cases (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    dataset_id UUID NOT NULL REFERENCES eval_datasets(id),
    version INTEGER NOT NULL
        CONSTRAINT eval_cases_version_check
        CHECK (version BETWEEN 1 AND 9999),
    -- Stable caller-provided identifier within one version (e.g. qa-001).
    case_key TEXT NOT NULL
        CONSTRAINT eval_cases_key_check
        CHECK (length(btrim(case_key)) BETWEEN 1 AND 100),
    question TEXT NOT NULL
        CONSTRAINT eval_cases_question_check
        CHECK (length(btrim(question)) BETWEEN 1 AND 2000),
    reference_answer TEXT NOT NULL
        CONSTRAINT eval_cases_reference_check
        CHECK (length(btrim(reference_answer)) <= 8000),
    -- [{"document_id": "<uuid>", "label": "<source filename>"}] — the stable
    -- relevance unit. document_id must reference a live document.
    expected_evidence JSONB NOT NULL DEFAULT '[]'::jsonb,
    notes TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT eval_cases_dataset_version_key UNIQUE (dataset_id, version, case_key),
    -- An immutable version is created once and never edited in place; no
    -- update statements exist for eval_cases.
    CONSTRAINT eval_cases_evidence_shape_check CHECK (
        jsonb_typeof(expected_evidence) = 'array')
);

CREATE INDEX eval_cases_dataset_version_idx ON eval_cases (dataset_id, version);
