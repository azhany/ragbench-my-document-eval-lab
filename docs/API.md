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

Service information: `{"service":"ragbench-api","status":"ok","resources":["/healthz","/readyz","/api/v1/rag-configs"]}`.

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

*The following resource groups remain a sketch; they are not yet implemented.*

## Documents

`POST /api/v1/documents`
- multipart upload
- returns document + queued status

`GET /api/v1/documents`

`GET /api/v1/documents/{id}`

`POST /api/v1/documents/{id}/reprocess`

`DELETE /api/v1/documents/{id}`

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
