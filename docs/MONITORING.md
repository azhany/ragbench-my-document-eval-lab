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
TRACE <public UUID>

request              2940ms
query_embedding       71ms  provider=openai model=text-embedding-3-small
retrieval            320ms  returned=5
prompt_build           1ms  prompt=v1 context_chunks=5
llm_generation      2440ms  profile=openai-gpt-4o-mini response=gpt-4o-mini-2024-07-18
citation_mapping       1ms  citations=2

total               2940ms
embedding_input_tokens  12
input_tokens        1102
output_tokens        284
estimated_cost    USD 0.000336
pricing_version  2026-09-16-provider-rates
```

`rag_traces` stores the exact prompt and ranked context snapshots in addition
to this summary. A missing usage field or unknown rate makes cost NULL with an
explicit reason, never zero. The query total includes the embedding,
generation-input, and generation-output components.

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

### Implemented query classification (Sprint 3)

The Go query path records a correlated PostgreSQL trace for every request
that reaches execution, including provider, retrieval, citation, and trace
write failures. Structured logs include the classification and trace ID while
excluding provider response bodies, source secrets, and credentials.

Current taxonomy:

- `embedding_failed`
- `retrieval_empty`
- `revision_unavailable`
- `retrieval_failed`
- `model_timeout`
- `model_rate_limited`
- `model_failed`
- `malformed_response`
- `citation_missing`
- `citation_invalid`
- `capability_unavailable`
- `persistence_failed`

Validation and unknown-configuration failures occur before a trace exists and
are still returned as structured HTTP errors. `persistence_failed` is never
reported as a successful answer. `INSUFFICIENT_EVIDENCE` is a successful model
outcome with zero citations, distinct from `retrieval_empty`.

Trace spans currently cover `request`, `query_embedding`, `retrieval`,
`prompt_build`, `llm_generation`, and `citation_mapping`. Reranking is
rejected as an unavailable capability until its owning story implements it.
