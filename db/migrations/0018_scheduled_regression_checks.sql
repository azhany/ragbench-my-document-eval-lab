-- RB-23: an opt-in, durable scheduled regression definition and its latest
-- execution state. Airflow owns timing; PostgreSQL owns the pinned inputs and
-- outcome so restart/retry cannot turn a missing baseline into a pass.

CREATE TABLE scheduled_regression_checks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 200),
    dataset_id UUID NOT NULL REFERENCES eval_datasets(id),
    dataset_version INTEGER NOT NULL CHECK (dataset_version >= 1),
    candidate_config_id UUID NOT NULL REFERENCES rag_configs(id),
    baseline_run_id UUID NOT NULL REFERENCES eval_runs(id),
    policy_name TEXT NOT NULL,
    policy_version INTEGER NOT NULL DEFAULT 0 CHECK (policy_version >= 0),
    enabled BOOLEAN NOT NULL DEFAULT false,
    last_run_id UUID REFERENCES eval_runs(id),
    last_status TEXT NOT NULL DEFAULT 'not_run'
        CHECK (last_status IN ('not_run', 'running', 'passed', 'regression',
                               'not_evaluable', 'failed')),
    last_reason TEXT,
    last_verdict JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX scheduled_regression_checks_name_key
    ON scheduled_regression_checks (name);
CREATE INDEX scheduled_regression_checks_enabled_idx
    ON scheduled_regression_checks (enabled);
