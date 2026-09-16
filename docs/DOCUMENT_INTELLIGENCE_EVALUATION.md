# Document-intelligence evaluation

Sprint 7 includes a small provider-independent labeled set at
[`db/fixtures/financial/financial_dataset_v1.json`](../db/fixtures/financial/financial_dataset_v1.json).
It contains three synthetic cases with different layouts and deliberate
failure conditions:

| Case | Extraction label | Validation label |
|---|---|---|
| `valid-invoice` | one invoice, one line item, all key values | `pass` |
| `receipt-missing-total` | receipt with an explicitly absent total | `failure`, `required.total` |
| `inconsistent-invoice` | invoice values are extracted as stated | `failure`, `line_item.extension`, `subtotal.line_items`, `total.reconciliation` |

The labels are data, not hidden test constants. The Go evaluator in
`backend/internal/documentintelligence/evaluation.go` validates the set and
scores each persisted analysis on separate dimensions:

- `schema_valid` and normalized field accuracy cover model/schema behavior;
- validation status plus rule precision/recall cover deterministic guardrail
  detection;
- total/stage latency, tokens, and cost are copied into an operational section
  and are never merged into the quality score.

Malformed structured output is counted as `schema_valid: false` with no
fabricated field or validation score. Missing provider usage or pricing stays
`null`; it is excluded from the corresponding operational population.

The evaluator can score a real API result without replaying the workflow:

```go
set, _ := documentintelligence.DecodeFinancialEvaluationSet(rawFixture)
score := documentintelligence.ScorePersistedAnalysis(set.Cases[0], analysis)
aggregate := documentintelligence.AggregateFinancialEvaluation(set.Version, scores)
```

The normal unit/fixture check is:

```sh
cd backend
GOCACHE=/tmp/ragbench-go-cache go test ./internal/documentintelligence
```

Provider-backed evaluation remains a deliberate reviewer run because it
requires credentials and can incur model cost. The primary extraction result
is not overwritten by this scorer or by the optional historical-document RAG
comparison.
