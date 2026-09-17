# API Contract

## Health endpoints (implemented)

Base URL: `http://localhost:8080` (the Vue frontend at `http://localhost:5173` proxies these paths to the API).

### `GET /healthz`

Process (liveness) health. Never touches the database; succeeds whenever the API process is able to serve requests.

Response `200 OK`:

```json
{
  "service": "ragbench-api",
  "status": "ok"
}
```

Errors: none specific to the endpoint. Any other method returns `405 Method Not Allowed`.

### `GET /readyz`

Dependency (readiness) health. Runs named checks, each with a 2-second timeout, and reports every check individually.

Response `200 OK` when all checks pass:

```json
{
  "status": "ready",
  "checks": {
    "database": {
      "status": "ok"
    },
    "schema": {
      "status": "ok"
    }
  }
}
```

Response `503 Service Unavailable` when any required dependency or schema state is unavailable (example: application database unreachable):

```json
{
  "status": "unavailable",
  "checks": {
    "database": {
      "status": "error",
      "error": "connect: connection refused"
    }
  }
}
```

Any other method returns `405 Method Not Allowed`.

Current check set:

| Check | Meaning |
|---|---|
| `database` | Ping of the application PostgreSQL connection pool |
| `schema` | Application schema is migrated to the version this binary expects (`schema_migrations` present at `backend/internal/schema` `RequiredVersion`); fails against an unmigrated or mismatched database |

### `GET /`

Service information: `{"service":"ragbench-api","status":"ok"}` plus the implemented resource roots, including `/api/v1/settings`, `/api/v1/metrics/summary`, and `/api/v1/regression-checks`.

## RAG configurations (implemented)

Base path: `/api/v1/rag-configs`. A saved configuration is an **immutable
identity**: there is no update or delete. Changing settings means saving a new
configuration under a new name, so runs and traces always reference the exact
settings that produced them.

The structured error envelope for this resource group is
`{"error":{"code":"...","message":"...","fields":[...]}}`; `fields` appears
only on validation failures with one entry per offending field.

### `POST /api/v1/rag-configs`

Creates one configuration. Request body (all core fields required except
`rerank_enabled`, which defaults to `false`; fusion fields default to the
persisted RRF values when omitted):

```json
{
  "name": "baseline-v1",
  "chunk_size": 500,
  "chunk_overlap": 80,
  "retrieval_mode": "vector",
  "top_k": 5,
  "rerank_enabled": false,
  "reranker_profile": "lexical-v1",
  "rerank_candidate_limit": 20,
  "fusion_method": "rrf",
  "rrf_rank_constant": 60,
  "fts_candidate_limit": 20,
  "vector_candidate_limit": 20,
  "prompt_version": "v1",
  "model_profile": "openai-gpt-4o-mini",
  "embedding_profile": "openai-text-embedding-3-small"
}
```

Validation rules (explicit bounds, mirrored by database constraints in
`db/migrations/0004_rag_configs.sql`):

| Field | Rule |
|---|---|
| `name` | 1–200 characters after trimming; unique across configurations |
| `chunk_size` | integer 1–8192 |
| `chunk_overlap` | integer ≥ 0 and strictly smaller than `chunk_size` |
| `retrieval_mode` | `vector` or `hybrid` |
| `top_k` | integer 1–100 |
| `prompt_version` | must exist in the prompt registry (currently `v1`) |
| `fusion_method` | `rrf` (defaults to `rrf`) |
| `rrf_rank_constant` | number 1–1000 (defaults to 60) |
| `fts_candidate_limit` / `vector_candidate_limit` | integer 1–100 (defaults to 20) |
| `model_profile` | must be an enabled generation profile in the persisted Settings catalog |
| `embedding_profile` | must be an enabled embedding profile in the persisted Settings catalog; its provider/model/dimensions are resolved and stored |

The catalog lives in PostgreSQL (`model_profiles`) and is managed through the
Settings API. Provider adapters remain server-side code, so a profile may use
any model ID supported by an existing adapter; adding a new wire protocol still
requires backend work. Unknown profiles and prompt versions are rejected,
never silently accepted.

Response `201 Created` with `Location: /api/v1/rag-configs/{id}`:

```json
{
  "id": "0b6c8cb1-6a7d-4d3f-9f6a-52f0a1b2c3d4",
  "name": "baseline-v1",
  "chunk_size": 500,
  "chunk_overlap": 80,
  "retrieval_mode": "vector",
  "top_k": 5,
  "rerank_enabled": false,
  "reranker_profile": "lexical-v1",
  "rerank_candidate_limit": 20,
  "fusion_method": "rrf",
  "rrf_rank_constant": 60,
  "fts_candidate_limit": 20,
  "vector_candidate_limit": 20,
  "prompt_version": "v1",
  "model_profile": "openai-gpt-4o-mini",
  "model_provider": "openai",
  "model_name": "gpt-4o-mini",
  "embedding_profile": "openai-text-embedding-3-small",
  "embedding_provider": "openai",
  "embedding_model": "text-embedding-3-small",
  "embedding_dimensions": 1536,
  "created_at": "2026-09-14T10:00:00Z",
  "unavailable_capabilities": []
}
```

`embedding_provider`/`embedding_model`/`embedding_dimensions` are resolved
server-side from the Settings catalog and persisted with the profile key, so
the exact embedding identity used survives catalog changes. The concrete
`model_provider`/`model_name` generation identity is persisted for the same
reason.

`unavailable_capabilities` lists requested features the running stack cannot
execute. Hybrid retrieval is executable through persisted FTS+RRF settings;
the shipped `lexical-v1` reranker is executable when `rerank_enabled` is true.
Reranker failures are classified and persisted; there is no silent fallback.

Errors:

| Status | Code | Cause |
|---|---|---|
| 400 | `invalid_body` | malformed JSON, unknown fields, empty body, or body larger than 64 KiB |
| 400 | `validation_failed` | one or more field violations, detailed in `error.fields` |
| 409 | `name_conflict` | a configuration with the same name already exists |
| 405 | — | method not allowed for the path |

### `GET /api/v1/settings`

Returns the provider adapters, persisted model profiles, prompt versions,
reranker profiles, and UI validation limits. It never returns provider keys,
secret values, or environment contents.

Response `200 OK`:

```json
{
  "providers": [
    {"id":"openai","label":"OpenAI","protocol":"OpenAI-compatible","roles":["generation","embedding"],"credential_note":"Server environment"}
  ],
  "model_profiles": [
    {"id":"…","name":"openai-gpt-4o-mini","kind":"generation","provider":"openai","model":"gpt-4o-mini","dimensions":null,"enabled":true}
  ],
  "prompt_versions": ["v1"],
  "reranker_profiles": ["lexical-v1"],
  "limits": {"min_chunk_size":1,"max_chunk_size":8192,"min_top_k":1,"max_top_k":100}
}
```

### `GET /api/v1/settings/model-profiles/{id}`

Returns one persisted model profile, including disabled entries for Settings
administration. Errors are `404 not_found` for an unknown or malformed id.

### `POST /api/v1/settings/model-profiles`

Adds a generation or embedding model identity to the persisted catalog. This
endpoint accepts model metadata only; it deliberately rejects unknown fields,
including credentials. The `name` is a lowercase profile key (`a-z`, numbers,
`.`, `_`, `-`), and names are unique per kind.

Request:

```json
{"name":"support-model-v1","kind":"generation","provider":"opencode-go","model":"glm-5.3-flash"}
```

Embedding profiles additionally require a positive `dimensions` value. The
provider must have a matching existing adapter (`openai` or `huggingface` for
embeddings; `openai`, `opencode-go`, `opencode-zen`, or `huggingface-chat` for
generation). Response `201 Created` returns the persisted profile and its
`Location`. Errors are `400 invalid_body`/`validation_failed`, `409
name_conflict`, or `503 settings_unavailable` when the catalog is not wired.

### `GET /api/v1/rag-configs`

Response `200 OK`: `{"configs": [ …config objects as above… ]}`, ordered by
name. Returns `{"configs": []}` when none are saved.

### `GET /api/v1/rag-configs/{id}`

Response `200 OK` with one config object as above. Errors: `404 not_found`
when the id is unknown or not a UUID; `405` for other methods.

## Documents (RB-05–RB-08)

`POST /api/v1/documents` accepts exactly two multipart fields: `file` and
`config_id` (an existing saved RAG configuration UUID), in either order.
PDF, DOCX, UTF-8 TXT, JPG/JPEG and PNG extensions are accepted
case-insensitively; the batch extractor validates actual content. Client MIME
claims are ignored. Names must
be plain filenames, ≤255 bytes, without `/`, `\`, `:`, or control characters.
The default byte limit is 20 MiB (`MAX_UPLOAD_BYTES`); the complete multipart
request has an additional 64 KiB allowance for headers/fields. Empty files are
rejected. Byte-identical live uploads return `409 duplicate_document`.

```sh
curl -F 'config_id=<saved-config-uuid>' -F 'file=@policy.pdf' \
  http://localhost:8080/api/v1/documents
```

After source bytes are synced to the shared volume and document/revision/job
state commits, Go triggers Airflow's `document_ingestion` using its public
v2 API and JWT authentication. It performs no extraction or embedding.
`202 Accepted` and `Location: /api/v1/documents/{id}` return the accepted
queued document snapshot. A worker can have progressed further when the next
GET runs. The document shape is:

```json
{
  "id": "uuid", "filename": "policy.pdf", "mime_type": "application/pdf",
  "status": "queued", "checksum": "sha256", "size_bytes": 2400,
  "chunk_count": null, "active_revision_id": null,
  "latest_revision_id": "uuid", "config_id": "uuid",
  "created_at": "timestamp", "updated_at": "timestamp",
  "job": {
    "id": "uuid", "index_revision_id": "uuid", "dag_id": "document_ingestion",
    "run_id": "rb_<revision-uuid>", "state": "queued", "stage": "dispatch",
    "error_code": null, "error_message": null, "dispatch_attempts": 1,
    "dispatched_at": "timestamp-or-null", "started_at": null,
    "finished_at": null, "stage_metrics": {}
  }
}
```

`GET /api/v1/documents` returns `{"documents": [...]}`, newest first (ID breaks
ties), excluding deleted documents. It is unpaginated for this bounded PoC.
`GET /api/v1/documents/{id}` returns one document plus `revisions`, newest
revision first. Each revision includes its persisted config ID, chunk settings
and unit, source checksum, embedding identity, status, publication timestamp,
and job object. Internal source paths and stage artifacts are never exposed.

`POST /api/v1/documents/{id}/reprocess` requires JSON
`{"config_id":"<saved-config-uuid>"}`. It appends a new immutable revision,
triggers `document_reindex`, and returns the same `202` shape. Only one pending
revision per document is allowed (`409 ingestion_active`). A repeat while
active is rejected; a new call after completion intentionally creates another
revision, even with unchanged settings. Unsupported *query* capabilities such
as reranking do not prevent ingestion: only persisted chunk/embedding settings
are executed here.

`POST /api/v1/documents/{id}/retry-dispatch` needs no body. It retries a
`dispatch_pending` or `dispatch_failed` job using the original DAG/run/job IDs.
An Airflow `409` is accepted only after checking the existing run's job ID.
Other job states return `409 dispatch_not_retryable`. This also recovers an API
restart/timeout between persisting a job, triggering Airflow, and recording its
response. It does not clear a failed Airflow task; use reprocess for a new run.

`DELETE /api/v1/documents/{id}` returns `204 No Content`, including repeated
deletion of the same retained ID. It tombstones the document and cancels its
unfinished jobs transactionally. Subsequent list/detail/reprocess/retry calls
exclude it. It cannot be resurrected by late dispatch or task completion.
Unknown or malformed IDs return `404 not_found` (including DELETE).

The document's status reports the **latest attempt**: queued → processing →
processed/failed. `chunk_count` and `active_revision_id` always refer to the
last successfully published revision. They stay usable during a replacement
and after replacement failure. All vectors must be present before active
revision/count switching commits. Old source bytes, revisions, chunks and job
metadata are retained indefinitely for the PoC; delete is not an erasure API.
Historical trace rendering will be verified when RB-11 exists.

Errors use the shared `{"error":{"code":"...","message":"..."}}` envelope:

| HTTP | Code | Meaning / recovery |
|---|---|---|
| 400 | `invalid_upload`, `invalid_filename`, `empty_upload` | Correct multipart fields/name/content |
| 400 | `invalid_config`, `invalid_body` | Use an existing configuration and documented request shape |
| 413 | `upload_too_large` | Reduce the file or raise the configured bound |
| 415 | `unsupported_format` | Use PDF/DOCX/TXT/JPG/JPEG/PNG |
| 404 | `not_found` | ID malformed, unknown or deleted |
| 409 | `duplicate_document` | Inspect the existing live document; use reprocess if needed |
| 409 | `ingestion_active`, `dispatch_not_retryable` | Inspect latest job state before repeating the action |
| 500 | `storage_failed` | Repair shared-volume permissions/capacity, then upload again |
| 500 | `persistence_failed` | Inspect correlated server logs and reconcile uncertain source storage |
| 503 | `dispatch_failed` | Response also includes persisted `document`; fix Airflow and call retry-dispatch |
| 503 | `dispatch_state_unknown` | Use Location/document ID to inspect state and retry the existing dispatch |

Completed/rejected requests close and remove unpersisted upload files. If the
DB commit outcome is uncertain, bytes are preserved with a generated UUID
filename and an `upload_persistence_uncertain` log; avoid deleting a possibly
committed source. A process crash can also leave a source before metadata
commits. Run `sh scripts/reconcile-uploads.sh` after stopping uploads to report
unreferenced files and missing sources; it never deletes evidence automatically.

Batch failures are returned in `job.error_code` / `job.error_message`, including
`storage_failed`, `source_changed`, `corrupt_document`, `encrypted_document`,
`image_only_or_empty`, `image_unreadable`, `ocr_unavailable`, `ocr_timeout`,
`empty_content`, `extraction_limit`, `revision_conflict`, `embedding_failed`,
`indexing_failed`, and `task_failed`. Embedding errors name
the batch and starting chunk without exposing provider bodies or credentials.
Airflow retries a stage twice, ten seconds apart; a failed retry becomes visible
immediately. Creating a new revision fences off subsequent retries of older jobs.

Integration references: [Airflow 3.0.6 public API authentication](https://airflow.apache.org/docs/apache-airflow/3.0.6/security/api.html),
[Airflow 3.0.6 REST API](https://airflow.apache.org/docs/apache-airflow/3.0.6/stable-rest-api-ref.html),
[OpenAI create embeddings](https://developers.openai.com/api/reference/resources/embeddings/methods/create),
[Hugging Face feature extraction](https://huggingface.co/docs/inference-providers/tasks/feature-extraction),
and [OpenCode Go endpoints](https://dev.opencode.ai/docs/go/#endpoints), plus
[Hugging Face Inference Providers](https://huggingface.co/docs/inference-providers/en/index). The
testing-only `opencode-zen-big-pickle` profile uses OpenCode Zen's
OpenAI-compatible endpoint (`OPENCODE_ZEN_BASE_URL`, default
`https://opencode.ai/zen/v1`) and its free `big-pickle` model; its evaluator
counterparts are `rubric-v1-big-pickle`, `rubric-v1-mimo-free`, and
`rubric-v1-huggingface-gemma`. The Hugging Face fallback uses the
OpenAI-compatible router at `https://router.huggingface.co/v1` and the
configured `HF_TOKEN`; its cost remains unavailable unless the router exposes
an explicit rate for the selected provider/model.

## Document intelligence analyses (RB-27–RB-31)

An analysis reuses an uploaded document and its immutable latest source
revision. It does not create a second file store. The analysis is dispatched
asynchronously to the `document_intelligence` Airflow DAG and can be inspected
through its durable analysis ID and trace ID.

### `POST /api/v1/documents/{id}/analyses`

The request body is optional. The empty object `{}` inherits the model profile
from the document's saved RAG configuration. A reviewer may select another
registered model profile explicitly:

```json
{"model_profile":"opencode-go-glm-5.3-flash"}
```

The `202 Accepted` response contains the analysis snapshot. Its important
fields are `id`, `document_id`, `source_revision_id`, `trace_id`, `status`,
`stage`, `job`, `prompt_version`, `schema_version`, and
`validation_policy_version`. The initial status is `queued`; Airflow advances
it through `processing` to `completed`, `partial`, or `failed`.

Example:

```sh
curl -X POST http://localhost:8080/api/v1/documents/<document-id>/analyses \
  -H 'Content-Type: application/json' -d '{}'
curl http://localhost:8080/api/v1/document-analyses/<analysis-id>
```

### `GET /api/v1/document-analyses/{analysisId}`

Returns the durable result, including bounded `extraction_evidence`,
schema-valid `structured_data` when available, `schema_errors`,
`validation_status`, ordered `validation_findings`, `summary`,
`stage_metrics`, `tool_events`, usage/cost metadata, and `spans`. Raw model
output is retained for failure diagnosis but is not returned by this API.
`summary` is only populated after deterministic validation has completed; a
summary-provider failure is returned as `status: "partial"` with the existing
structured data and findings intact.

### `GET /api/v1/documents/{id}/analyses`

Returns `{"analyses":[...]}` newest first. `POST
/api/v1/document-analyses/{analysisId}/retry-dispatch` retries only a persisted
`dispatch_pending` or `dispatch_failed` job with the same Airflow run identity.

The Airflow-coordinated stage contract is internal to the local Compose stack:
`extract_text` (pypdf or bounded Tesseract OCR) → `structured_extract` →
`schema_validate` → `financial_validate` → `summarize`. The stage endpoint
requires the persisted `job_id`, `dag_id`, and `run_id`; an out-of-order or
foreign request returns a visible conflict. Provider failures, malformed model
JSON, schema violations, OCR failures, and summary failures retain their
classified error code and correlation IDs. Extraction has one Airflow retry;
structured extraction and summary each have one bounded Go/provider retry;
schema and financial validation are deterministic and are not Airflow-retried.

The financial schema is `financial-v1`: amounts/quantities are decimal
strings, dates are `YYYY-MM-DD`, currency is a three-letter uppercase code,
and absent nullable values are normalized to JSON `null`. The deterministic
validation policy is `financial-validation-v1` with a persisted `0.01` amount
tolerance; it uses exact decimal arithmetic and emits stable rule IDs.
The synthetic labeled set and separate field/validation/operational scoring
dimensions are documented in
[`DOCUMENT_INTELLIGENCE_EVALUATION.md`](DOCUMENT_INTELLIGENCE_EVALUATION.md).

## Chat and traces (RB-09–RB-12 implemented)

The chat pipeline is synchronous in Go. It embeds the question with the
configuration's persisted embedding identity, retrieves compatible active
ready revisions through `searchable_chunks`, builds the versioned prompt,
calls the configured generation provider, maps citations, and writes one
trace plus its spans transactionally. Evaluation will call this same pipeline
when RB-14 lands.

### `POST /api/v1/chat`

Request bodies are JSON, limited to 64 KiB, and reject unknown fields:

```json
{
  "question": "How does approval work?",
  "config_id": "uuid"
}
```

`question` is trimmed and must contain 1–2,000 Unicode characters.
`config_id` must identify an existing immutable RAG configuration. The
selected configuration must not request a capability absent from the running
stack. Hybrid retrieval and the shipped lexical reranker are executable.

Response `200 OK`:

```json
{
  "answer": "Approval needs two signatures [1].",
  "citations": [
    {
      "document_id": "uuid",
      "chunk_id": "uuid",
      "snippet": "Approval needs two signatures."
    }
  ],
  "trace": {
    "trace_id": "uuid",
    "latency_ms": 2940,
    "input_tokens": 1102,
    "output_tokens": 284,
    "embedding_input_tokens": 12,
    "estimated_cost": 0.000336,
    "cost_currency": "USD",
    "cost_unavailable_reason": null,
    "pricing_version": "2026-09-16-provider-rates"
  }
}
```

The query embedding, generation input, and generation output are the three
cost components. Known OpenAI, OpenCode Go, and the explicit zero-rate
OpenCode Zen Big Pickle test profile are recorded in
`backend/internal/providers/providers.go`, and the query total is rounded to
six decimal places in native USD. Provider-reported token fields and
`estimated_cost` are `null` when usage is missing or pricing is unknown;
`cost_unavailable_reason` is then `usage_unavailable` or
`pricing_unavailable`, never a fabricated zero.

Provider selection comes from the immutable saved profiles, never from a
request-supplied URL. The Go query path routes OpenAI profiles to the OpenAI v1
wire contract, OpenCode Go and OpenCode Zen to their OpenAI-compatible
`/chat/completions` contracts, and Hugging Face embeddings to the native feature-extraction
contract (`inputs` in; a bare vector array out). Native Hugging Face responses
do not report token usage, so their query cost is explicitly unavailable.

Prompt `v1` includes the question and whole retrieved chunks in rank order
until the 24,000-Unicode-character context budget. The template labels
document text as untrusted evidence, not instructions. The trace detail
stores the exact rendered prompt and the context actually sent.
The pipeline bounds the full sequential request at 120 seconds and keeps a
longer server response deadline so timeout and persistence errors still return
as structured responses.

Answers must contain numbered markers such as `[1]`. Markers are one-based
indices into the context sent to the model; duplicates are returned once in
first-seen order. An outside, zero, negative, or otherwise invalid index is a
`citation_invalid` failure. An answer with no marker is a
`citation_missing` failure, and no uncited answer is returned. The exact
`INSUFFICIENT_EVIDENCE` answer is a successful empty-citation response.

Errors use the shared `{"error":{"code":"...","message":"...","trace_id":"..."}}`
envelope. `trace_id` is included after a trace has been created and persisted;
pre-execution validation/config errors have no trace:

| HTTP | Code | Meaning |
|---|---|---|
| 400 | `invalid_body` | malformed JSON, unknown field, empty body, or body over 64 KiB |
| 400 | `validation_failed` | question/config field validation failed |
| 404 | `not_found` | unknown or malformed configuration ID |
| 409 | `revision_unavailable` | no active ready revision compatible with the saved embedding identity |
| 422 | `capability_unavailable` | requested capability is not executable in this deployment |
| 422 | `retrieval_empty` | a compatible retrieval boundary exists but returned no evidence |
| 502 | `retrieval_failed` | pgvector retrieval or retrieval-boundary query failed |
| 502 | `embedding_failed` | query embedding provider failure or invalid vector response |
| 502 | `model_failed` | non-timeout/non-rate-limited generation provider failure |
| 502 | `model_rate_limited` | generation provider returned HTTP 429 |
| 502 | `malformed_response` | generation provider response was invalid or empty |
| 502 | `citation_missing` / `citation_invalid` | generated answer is not safely grounded |
| 504 | `model_timeout` | generation exceeded its 60-second deadline |
| 500 | `persistence_failed` | trace write failed; answer is not returned as traceable |

Provider response bodies and credentials are never returned. Provider,
retrieval, citation, and persistence failures are logged with the trace ID
when one exists.

### `GET /api/v1/traces`

Lists successful and failed chat traces newest first. `limit` is optional
(default `50`, maximum `200`); invalid values return `422 invalid_limit`.
Each summary contains `trace_id`, `request_type`, `question`, `rag_config_id`,
`success`, nullable `error_code`, `total_latency_ms`, nullable
`input_tokens`, `output_tokens`, `embedding_input_tokens`,
`estimated_cost`, `cost_unavailable_reason`, and `created_at`.

### `GET /api/v1/traces/{traceId}`

Returns the complete persisted trace, or `404 not_found` for an unknown or
malformed ID. In addition to the list fields it returns nullable
`error_message`, `cost_currency`, `pricing_version`, `cost_components`,
`answer`, `citations`, the immutable `config` object, `prompt_snapshot`,
`context_snapshot`, and ordered `spans`. Spans contain `span_name`,
`started_at`, `duration_ms`, and provider/stage metadata.

The detail contract is the historical source for the Chat context drawer:
reopening a trace uses its saved configuration, rendered prompt, and ranked
evidence snapshot rather than current configuration or document state. Failed
traces retain the executed spans plus prompt/context snapshots available
before the failing stage; early embedding/retrieval failures have empty
snapshots. A trace write failure is returned as `persistence_failed` and
cannot look like a successful trace.

## Evaluation (RB-13–RB-16, RB-18–RB-20 implemented)

### `POST /api/v1/eval-datasets` (RB-13)
Import a dataset: immutable version 1 with reviewed cases.

```json
{
  "name": "golden-dataset-v1",
  "description": "…",
  "cases": [
    {
      "case_key": "qa-001",
      "question": "…",
      "reference_answer": "…",
      "expected_evidence": [{"document_id": "<uuid>", "label": "<source name>"}],
      "judgment_version": "binary-v1",
      "graded_judgments": {},
      "notes": ""
    }
  ]
}
```

`201 Created` → dataset JSON (`id`, `name`, `description`, `latest_version`,
`created_at`). Errors:
- `409 name_conflict` — the dataset name is unique.
- `422 invalid_dataset` / `422 invalid_reference` — empty versions, invalid
  references (unknown/deleted/malformed document ids) are rejected, never
  stored; duplicate case_key is `400 duplicate_case_key`. For graded
  retrieval, `graded_judgments` maps expected document UUIDs to integer grades
  `0`–`5` and pins `judgment_version` to the stored nDCG semantics.

### `GET /api/v1/eval-datasets`
`{"datasets": [...], "fields": same as 201}`.

### `GET /api/v1/eval-datasets/{id}?version=N`
`{"dataset_id", "version", "cases": [...]}` — one immutable version's
reviewed cases (version omitted = latest; out-of-range = `422`).

### `POST /api/v1/eval-datasets/{id}/cases` (RB-13 versioned edit)
Replace the case set; creates immutable version N+1 (`201`). Old runs keep
their original cases; the same validation errors apply.

### `POST /api/v1/eval-runs` (RB-14)
Launch a dataset version against a named config through the shared Go query
pipeline with evaluation attribution.

```json
{"dataset_id": "<uuid>", "dataset_version": 0, "rag_config_id": "<uuid>",
 "rubric_version": "rubric-v1", "scoring_k": 5}
```

`201 Created` (dispatched) or `202 Accepted` with `status
dispatch_failed` + `dispatch_error` — Airflow dispatch failure is a visible
run failure, never successful processing. Inputs are pinned at creation:
dataset version, immutable config identity, per-document corpus revision
snapshot, evaluator policy. Unknown rubric/dataset versions and blocked
configs are `422 invalid_run`.

### `GET /api/v1/eval-runs`, `GET /api/v1/eval-runs/{id}`
List (newest first) / detail. Detail includes the pinned inputs and a
computed `aggregate` with explicit denominators: `recall_count`,
`recall_mean`, `mrr_*`, `ndcg_count`, `ndcg_mean`, rubric means with counts, `latency_population`,
`latency_p50_ms`, `latency_p95_ms`, `cost_population`, `cost_total`,
`completed`, `query_failed`, `evaluator_failed`, `missing_score_cases`.
Missing values are null — never zero — and status distinguishes
completed/partial/failed work.

### `GET /api/v1/eval-runs/{id}/results`
Per-case rows: `case_key`, `status` (completed|failed|evaluator_failed),
`trace_id`, `query_error_code/message`, `recall_k`, `mrr`, `ndcg_k`, rubric scores
with verbatim rationales, `citation_correct`, evaluator tokens/cost, latency
and cost copies. `NULL` scores mean not evaluable, never zero quality.

### `POST /api/v1/eval-runs/{id}/cases/{caseId}/execute`
Runs one case through the same pipeline as chat (`request_type=evaluation`).
Idempotent: a stored terminal result is returned unchanged.

### `POST /api/v1/eval-runs/{id}/finalize`
Recomputes aggregate run status from persisted results (DAG + recovery).

### `GET /api/v1/eval-runs/{id}/compare/{baselineId}?policy=default-v1` (RB-19)
Compares only compatible pairs (same dataset version, source corpus, scoring
K, evaluator policy; differing chunk settings allowed via the document
relevance unit). Response: `policy` (persisted rules + stored quality-gain
definition), `compatibility` with reasons, and a `verdict` — per-rule
baseline/candidate values, absolute/relative deltas, direction, threshold
and `passed|regression|not_evaluable|gain_exception` with reasons. Strictly
greater-than threshold semantics; zero-baseline relative deltas are
undefined (`not_evaluable`); incomplete runs are never a clean pass; a
quality regression stays distinct from a pipeline or evaluator failure.
When a persisted policy includes `ndcg_k`, its values are compared under the
same pinned `ndcg_policy_version`; different judgment/scoring policies are
incompatible rather than silently compared.

### Experiments (RB-18)
- `POST /api/v1/experiments` — persisted matrix expansion before execution
  with an explicit `combination_limit` (1–64); every combination resolves as
  a valid immutable config identity up front; rerank settings use the same
  persisted config identity and candidate-limit validation.
- `GET /api/v1/experiments`, `GET /api/v1/experiments/{id}`
- `POST /api/v1/experiments/{id}/advance` — one idempotent orchestration
  step (the `rag_parameter_sweep` DAG drives it until terminal); chunk/
  embedding changes resolve through `document_reindex` first, and a failed
  reindex keeps that combination unevaluated. Retries reuse created
  identities; partial matrix failures retain successful run links.

## Monitoring

`GET /api/v1/metrics/summary`

The summary accepts optional `from`/`to` RFC3339 timestamps, an IANA
`timezone` (default `UTC`), and `traffic=all|query|evaluation` (default
`all`). The default window is the previous 30×24 hours. Query traffic means
`rag_traces.request_type=chat`; evaluation traffic means `evaluation`. Query
tokens/cost and evaluation judge cost are kept separate. Percentile fields
state their population; empty populations and unknown usage/pricing are null,
never manufactured zeroes. Quality trends are grouped by dataset/version,
evaluator/nDCG policy, day and currency, and failure rows contain a
`detail_path` to the persisted trace, document or evaluation run.

`GET /api/v1/traces`

`GET /api/v1/traces/{traceId}`

### Scheduled regression checks (RB-23)

`POST /api/v1/regression-checks` persists an opt-in definition containing
dataset/version, candidate configuration, baseline run, policy name/version
and `enabled`. `GET /api/v1/regression-checks` and `GET .../{id}` expose the
last execution state. Airflow calls `POST .../{id}/start`, which creates a
normal `eval_run` through the existing dispatch workflow, then calls
`POST .../{id}/finish` with the existing comparison verdict. Missing or
incomplete baselines are `not_evaluable`, never pass. Local Compose sets
`RAGBENCH_SCHEDULE_ENABLED=false` and no cron by default; explicitly set
`RAGBENCH_SCHEDULE_ENABLED=true`, `RAGBENCH_SCHEDULE_CRON`, and
`RAGBENCH_SCHEDULE_CHECK_ID` only in an authorized environment. Manual DAG
triggers can provide `dag_run.conf.check_id` instead.
