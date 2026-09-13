# Monitoring Design

## What to monitor

### Quality
- Recall@K trend
- MRR trend
- faithfulness trend
- answer relevance trend
- regression count

### Reliability
- query error rate
- ingestion failure rate
- evaluation job failure rate
- model/provider errors

### Performance
- p50/p95 total latency
- retrieval latency
- generation latency

### Cost
- input tokens
- output tokens
- estimated cost/query
- daily experiment cost

## Trace example

```text
TRACE #run_042_case_017

query_received       12ms
query_embedding      71ms
retrieval           320ms
prompt_build        120ms
llm_generation       2.4s
citation_mapping      84ms

total               3.01s
input_tokens        1102
output_tokens        284
estimated_cost      RM0.041
```

## Failure classification

Use a small taxonomy:
- ingestion_extract_failed
- embedding_failed
- retrieval_empty
- model_timeout
- model_rate_limited
- malformed_response
- citation_missing
- evaluator_failed

The goal is to distinguish pipeline failure from quality failure.
