# API Sketch

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
