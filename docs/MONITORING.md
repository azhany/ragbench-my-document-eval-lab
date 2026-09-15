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

### Implemented ingestion observability (Sprint 2)

Go logs `document_dispatched`, `document_dispatch_failed`, `document_deleted`,
and upload/storage/persistence errors, alongside HTTP status and duration.
Dispatch logs carry document/job/run IDs. Airflow stage logs carry document,
revision, job and stage identity plus duration and classified error. Join the
persisted job's DAG/run ID to Airflow logs; source contents and credentials are
not included in structured messages.

`ingestion_jobs` stores dispatch attempts, created/dispatched/started/finished
timestamps and per-stage duration/status. Successful embedding stages record
batch size, chunk count and provider-reported prompt tokens; missing usage stays
null. These tokens cover the successful embedding stage, not total billable
usage of failed/retried provider requests. No cost estimate is invented here.

```sql
SELECT state, count(*) FROM ingestion_jobs GROUP BY state;
SELECT id, dag_id, run_id, stage, error_code, error_message
FROM ingestion_jobs WHERE state IN ('failed', 'dispatch_failed');
SELECT id, finished_at-started_at AS elapsed, stage_metrics
FROM ingestion_jobs WHERE state='succeeded';
```

For ingestion failure rate, count `failed` jobs over `succeeded + failed` jobs
in the same time window; report dispatch failures separately, and exclude
cancelled and in-flight jobs. Library exposes the latest job's real state/error
and revision history. Aggregated Monitor UI belongs to later stories.

### Planned query/evaluation classification

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
