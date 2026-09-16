-- RB-19: persisted regression policy.
--
-- Thresholds are explicit persisted inputs, never hidden defaults: each
-- policy row carries its version, the metric direction table and the
-- absolute-or-relative delta rule with thresholds to apply. Stored runs are
-- compared under exactly one named policy version so any verdict can be
-- re-derived from the stored inputs.

CREATE TABLE eval_regression_policies (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL
        CONSTRAINT eval_regression_policies_name_check
        CHECK (length(btrim(name)) BETWEEN 1 AND 200),
    version INTEGER NOT NULL
        CONSTRAINT eval_regression_policies_version_check
        CHECK (version BETWEEN 1 AND 9999),
    -- The persisted rule set, one entry per compared metric:
    -- {"recall_k": {"direction": "higher", "delta": "relative", "threshold": 0.05},
    --  ...} — validated by the comparison module before use.
    -- "quality_gain_exception" endpoints list rule-dependent exclusions for
    -- efficiency metrics (latency/cost may regress only if quality gained).
    policy JSONB NOT NULL,
    -- quality_gain defines the exception rule the policy uses to allow an
    -- efficiency regression explicitly (documented wording, not code magic).
    quality_gain_definition TEXT NOT NULL DEFAULT
        'any recall_k gain on the same dataset version justifies latency or cost regression',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT eval_regression_policies_key UNIQUE (name, version)
);
