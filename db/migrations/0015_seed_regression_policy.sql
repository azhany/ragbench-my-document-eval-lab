-- RB-19: seed the documented example regression policy as an explicit,
-- persisted, named policy row (EVALUATION.md example values). These are
-- project policy values under one name, not hidden defaults in code: any
-- comparison can select a different policy by name.

INSERT INTO eval_regression_policies (name, version, policy, quality_gain_definition)
VALUES (
  'default-v1',
  1,
  '{
    "recall_k":    {"direction": "higher", "delta": "relative", "threshold": 0.05},
    "mrr":         {"direction": "higher", "delta": "relative", "threshold": 0.05},
    "latency_p95": {"direction": "lower",  "delta": "relative", "threshold": 0.25},
    "cost_total":  {"direction": "lower",  "delta": "relative", "threshold": 0.30}
  }'::jsonb,
  'any recall_k gain on the same dataset version justifies latency or cost regression'
)
ON CONFLICT (name, version) DO NOTHING;
