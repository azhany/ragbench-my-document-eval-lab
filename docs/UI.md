# UI / UX

## Navigation

- Overview
- Library
- Chat
- Evaluations
- Experiments
- Monitor

## MVP screens

### Library
- upload area
- document table
- processing status
- chunk count
- reprocess/delete actions

### Chat (RB-12 implemented)

- RAG configuration selector populated from `GET /api/v1/rag-configs`
- question composer posting to `POST /api/v1/chat`
- answer rendered as text, never injected HTML
- citation cards showing document/chunk identity and bounded safe snippets
- persisted trace link and summary for latency, generation tokens, query
  embedding tokens, and native cost
- trace detail drawer/panel loaded from `GET /api/v1/traces/{traceId}`; it
  shows the historical configuration, prompt version, ranked context actually
  sent, and executed spans
- distinct loading, empty-config, empty-retrieval, provider-failure,
  unavailable-cost, and generic error states

The drawer uses `context_snapshot` and `config` from trace detail, not the
currently selected config or live document state. A trace can therefore be
reopened after reprocess/delete without changing the evidence displayed.

### Evaluations
- dataset selector
- config selector
- run evaluation
- aggregate score cards
- per-question results
- compare with baseline

### Monitor
- success/error
- p95 latency
- token/cost trend
- recent traces
- recent failures

## UX principle

The UI should visually connect:

**Question → Evidence → Answer → Metrics → Trace**

That relationship is the core value of the project.
