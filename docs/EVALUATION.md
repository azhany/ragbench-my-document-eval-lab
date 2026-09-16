# Evaluation Design

## Evaluation philosophy

Separate the system into three questions:

1. **Did we retrieve the right evidence?**
2. **Did the model answer from that evidence?**
3. **How much did the answer cost in time and money?**

Do not collapse everything into one score.

## Golden dataset

Start with 20–40 questions.

Example row:

```json
{
  "id": "qa-001",
  "question": "What is the document approval workflow?",
  "expected_document_ids": ["doc-123"],
  "expected_chunk_ids": ["chunk-123-08", "chunk-123-09"],
  "reference_answer": "..."
}
```

## Metrics

### Recall@K (implemented, `backend/internal/evaluation`)

Relevant evidence retrieved within K divided by total expected evidence, at
the documented relevance unit: the document identity ("which stored source
contains the expected evidence"). Deliberate conventions:
- K semantics: the first K retrieved chunks AFTER retrieval truncation
  (scoring truncates defensively to the configured K).
- Deduplication: multiple chunks of the same expected document collapse to
  one hit; a document counts once.
- Missing relevance: an absent expected document stays in the denominator
  (it lowers recall; it never silently improves MRR either).
- Undefined values: zero expected documents leaves Recall@K not evaluable —
  callers must persist a missing score, never a zero.

### MRR (implemented)

1 / (first rank at which any expected document appears after deduplication),
0 when no expected document was retrieved (documented convention: no
relevant hit is a failed ranking). Empty expected sets are not evaluable.

A different granularity (e.g. chunk level) requires an explicit relevance
remapping recorded with the run; stale chunk UUIDs are never treated as
ground truth for another index revision.

### nDCG@K

Useful when relevance is graded rather than binary. Keep optional for MVP.

### Answer relevance

Does the answer actually address the question?

### Faithfulness / groundedness

Are claims supported by retrieved context?

### Citation correctness

Do cited chunks contain evidence for the associated answer?

### Cost and latency

Treat efficiency as a first-class evaluation dimension:
- total latency
- retrieval latency
- generation latency
- input tokens
- output tokens
- estimated cost

## Experiment comparison

Example configurations:

```yaml
baseline:
  chunk_size: 500
  chunk_overlap: 80
  retrieval: hybrid
  top_k: 5
  rerank: false
  prompt_version: v1

candidate:
  chunk_size: 800
  chunk_overlap: 120
  retrieval: hybrid
  top_k: 8
  rerank: true
  prompt_version: v2
```

## Regression rule

Example:

```text
FAIL if:
Recall@5 decreases > 5%
OR faithfulness decreases > 3%
OR p95 latency increases > 25% without a quality gain
OR estimated cost/query increases > 30% without a quality gain
```

These values are project policy, not universal truth.

### Implemented comparison mechanics (RB-19)

Policies live in `eval_regression_policies` (`default-v1` seeded with the
example thresholds above, under a persisted name). Each rule carries:
metric direction (`higher`/`lower`), delta kind (`absolute`/`relative`),
non-negative threshold, and the policy's stored quality-gain definition
(efficiency regressions can be tolerated only while recall gained).
Boundary semantics implemented and unit-tested:
- strictly greater-than threshold regression (equal passes; 1e-9 epsilon);
- relative delta at a zero baseline is undefined → rule `not_evaluable`;
- missing aggregates keep their own `not_evaluable` state (never zero);
- incomplete runs (query/evaluator failures) cannot produce a clean pass;
- a quality regression is distinct from a pipeline/evaluator failure. The
  compare endpoint only evaluates compatible pairs (dataset version, source
  corpus checksums, scoring K, evaluator policy).
