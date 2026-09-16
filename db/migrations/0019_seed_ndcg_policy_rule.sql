-- RB-26: include the optional graded-retrieval metric in the named default
-- policy. Cases without graded judgments remain NULL/not_evaluable.

UPDATE eval_regression_policies
SET policy = policy || '{"ndcg_k": {"direction": "higher", "delta": "relative", "threshold": 0.05}}'::jsonb
WHERE name = 'default-v1' AND version = 1
  AND NOT (policy ? 'ndcg_k');
