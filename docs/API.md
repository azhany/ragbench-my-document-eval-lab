# API Sketch

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

Service information: `{"service":"ragbench-api","status":"ok","resources":["/healthz","/readyz","/api/v1/rag-configs","/api/v1/documents"]}`.

## RAG configurations (implemented)

Base path: `/api/v1/rag-configs`. A saved configuration is an **immutable
identity**: there is no update or delete. Changing settings means saving a new
configuration under a new name, so runs and traces always reference the exact
settings that produced them.

The structured error envelope for this resource group is
`{"error":{"code":"...","message":"...","fields":[...]}}`; `fields` appears
only on validation failures with one entry per offending field.

### `POST /api/v1/rag-configs`

Creates one configuration. Request body (all fields required except
`rerank_enabled`, which defaults to `false`):

```json
{
  "name": "baseline-v1",
  "chunk_size": 500,
  "chunk_overlap": 80,
  "retrieval_mode": "vector",
  "top_k": 5,
  "rerank_enabled": false,
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
| `model_profile` | must exist in the model profile registry (currently `openai-gpt-4o-mini`) |
| `embedding_profile` | must exist in the embedding profile registry (currently `openai-text-embedding-3-small`, 1536 dimensions) |

The registry lives in `backend/internal/providers`; unknown profiles and
prompt versions are rejected, never silently accepted.

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
  "prompt_version": "v1",
  "model_profile": "openai-gpt-4o-mini",
  "embedding_profile": "openai-text-embedding-3-small",
  "embedding_provider": "openai",
  "embedding_model": "text-embedding-3-small",
  "embedding_dimensions": 1536,
  "created_at": "2026-09-14T10:00:00Z",
  "unavailable_capabilities": []
}
```

`embedding_provider`/`embedding_model`/`embedding_dimensions` are resolved
server-side from the registry and persisted with the profile key, so the exact
embedding identity used survives registry changes.

`unavailable_capabilities` lists requested features the stack cannot execute
yet: `"hybrid_retrieval"` until RB-17, `"rerank"` until RB-25. Such
configurations can be saved, but every execution path (chat, evaluation) must
reject them with `422 capability_unavailable` — never silently degrade to
vector/no-rerank.

Errors:

| Status | Code | Cause |
|---|---|---|
| 400 | `invalid_body` | malformed JSON, unknown fields, empty body, or body larger than 64 KiB |
| 400 | `validation_failed` | one or more field violations, detailed in `error.fields` |
| 409 | `name_conflict` | a configuration with the same name already exists |
| 405 | — | method not allowed for the path |

### `GET /api/v1/rag-configs`

Response `200 OK`: `{"configs": [ …config objects as above… ]}`, ordered by
name. Returns `{"configs": []}` when none are saved.

### `GET /api/v1/rag-configs/{id}`

Response `200 OK` with one config object as above. Errors: `404 not_found`
when the id is unknown or not a UUID; `405` for other methods.

## Documents (RB-05–RB-08)

`POST /api/v1/documents` accepts exactly two multipart fields: `file` and
`config_id` (an existing saved RAG configuration UUID), in either order.
PDF, DOCX and UTF-8 TXT extensions are accepted case-insensitively; the batch
extractor validates actual content. Client MIME claims are ignored. Names must
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
| 415 | `unsupported_format` | Use PDF/DOCX/TXT |
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
`image_only_or_empty`, `empty_content`, `extraction_limit`, `revision_conflict`,
`embedding_failed`, `indexing_failed`, and `task_failed`. Embedding errors name
the batch and starting chunk without exposing provider bodies or credentials.
Airflow retries a stage twice, ten seconds apart; a failed retry becomes visible
immediately. Creating a new revision fences off subsequent retries of older jobs.

Integration references: [Airflow 3.0.6 public API authentication](https://airflow.apache.org/docs/apache-airflow/3.0.6/security/api.html),
[Airflow 3.0.6 REST API](https://airflow.apache.org/docs/apache-airflow/3.0.6/stable-rest-api-ref.html),
[OpenAI create embeddings](https://developers.openai.com/api/reference/resources/embeddings/methods/create).

*The following resource groups remain a sketch; they are not yet implemented.*

## Chat

`POST /api/v1/chat`

Request:

```json
{
  "question": "How does approval work?",
  "config_id": "uuid"
}
```

Response:

```json
{
  "answer": "...",
  "citations": [
    {
      "document_id": "uuid",
      "chunk_id": "uuid",
      "snippet": "..."
    }
  ],
  "trace": {
    "trace_id": "uuid",
    "latency_ms": 2940,
    "input_tokens": 1102,
    "output_tokens": 284,
    "estimated_cost": 0.041
  }
}
```

## Evaluation

`POST /api/v1/eval-runs`

`GET /api/v1/eval-runs`

`GET /api/v1/eval-runs/{id}`

`GET /api/v1/eval-runs/{id}/results`

`GET /api/v1/eval-runs/{id}/compare/{baselineId}`

## Monitoring

`GET /api/v1/metrics/summary`

`GET /api/v1/traces`

`GET /api/v1/traces/{traceId}`
