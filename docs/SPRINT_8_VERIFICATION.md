# Sprint 8 Verification — Settings-managed RAG workspace

This file records the evidence for RB-33–RB-35. Runtime checks require a
migrated PostgreSQL/Compose stack; no provider calls are made by the automated
tests.

## Automated evidence

- [x] Frontend unit tests: `npm test` — 34 tests passed
- [x] Frontend production bundle: `npm run build` — passed
- [x] Backend model-profile/API/RAG/pipeline/experiment packages compile and
  unit tests pass with `GOCACHE=/tmp/ragbench-go-cache`
- [x] Settings API rejects unknown fields and maps validation/name conflicts
- [x] Settings UI payload tests prove credentials are not sent
- [x] Airflow embedding tests cover arbitrary persisted model IDs behind the
  supported provider boundary
- [x] `go vet ./...` passes; the full Go test command compiles all packages,
  while this sandbox blocks two pre-existing `httptest` cases from binding
  IPv6 loopback (`documents` and `providers`); the Sprint 8 target packages
  pass independently
- [ ] Apply migration `0021_settings_model_profiles.sql` to a disposable
  PostgreSQL database and verify a profile/config round trip
- [ ] Browser-check desktop and narrow layouts against the supplied dashboard
  mockup

## Contract evidence

- `GET /api/v1/settings` returns the persisted model catalog and provider
  adapter metadata without keys or environment values.
- `POST /api/v1/settings/model-profiles` creates generation/embedding entries;
  embedding dimensions are required and provider-kind combinations are
  explicit.
- `POST /api/v1/rag-configs` resolves enabled Settings profiles and persists
  `model_provider`/`model_name` plus embedding provider/model/dimensions.
- Library, Chat, Evaluations, and Experiments continue to consume the same
  immutable `/api/v1/rag-configs` identities.

## Sprint exit statement

The code and automated verification gates for the single Sprint 8 increment
are complete. Database migration and visual browser inspection remain the
environment-dependent release checks; provider credentials are intentionally
not required for those checks.
