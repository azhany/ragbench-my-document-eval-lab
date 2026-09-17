# Frontend

Vue 3 + Vite single-page shell for the RAGbench-MY UI destinations:
Overview, Library, Chat, Evaluations, Experiments, Monitor, Settings.

## Structure

- `src/router.js` — vue-router routes for the current workspace plus the
  Settings destination.
- `src/api/client.js` — the single API client shared by the whole shell. It
  normalizes every failure to `{ kind, status, code, message, fields, payload }`
  (`kind: 'unreachable'` for network failures, `kind: 'http'` for non-2xx
  responses) so views can render distinct, actionable error states.
- `src/views/OverviewView.vue` — stack status (health/readiness) and the saved
  RAG configuration list.
- `src/components/ConfigList.vue` — config list with distinct loading, empty,
  error (retryable), and ready states.
- `src/views/SettingsView.vue` — settings-managed model profiles and the
  immutable RAG configuration builder. It sends metadata only; credentials
  remain in the backend environment.
- State is component-local; no Pinia store — add one only when state is
  genuinely shared across views.

## Commands

```sh
npm install
npm test        # vitest: client, views, Settings, config list, router
npm run dev     # dev server; /api, /healthz, /readyz proxy to localhost:8080
npm run build   # production bundle into dist/
```

## Secrets

Provider API keys must never reach browser configuration: the bundle contains
no key material and the backend holds credentials server-side. The UI displays
model/profile identifiers only (e.g. `openai-gpt-4o-mini`), which are names,
not credentials.
