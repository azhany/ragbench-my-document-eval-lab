-- RB-21: retain comparison outcomes so monitoring can count real regression
-- verdicts instead of inferring them from failed evaluation jobs.

CREATE TABLE eval_comparisons (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    candidate_run_id UUID NOT NULL REFERENCES eval_runs(id),
    baseline_run_id UUID NOT NULL REFERENCES eval_runs(id),
    policy_name TEXT NOT NULL,
    policy_version INTEGER NOT NULL,
    verdict JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT eval_comparisons_key UNIQUE
        (candidate_run_id, baseline_run_id, policy_name, policy_version)
);

CREATE INDEX eval_comparisons_created_at_idx ON eval_comparisons (created_at DESC);
