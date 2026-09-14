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

Service information: `{"service":"ragbench-api","status":"ok","resources":["/healthz","/readyz"]}`.

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
