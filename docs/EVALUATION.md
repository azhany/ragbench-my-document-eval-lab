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

### Recall@K

Whether expected evidence appears in the first K retrieved chunks.

### MRR

Rewards retrieving the first relevant chunk near the top of the ranking.

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
