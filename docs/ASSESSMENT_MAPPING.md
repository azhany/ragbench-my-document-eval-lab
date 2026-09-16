# AI/ML Take-Home Assessment Mapping

Status: living reviewer map. Code, automated tests, Compose startup, and
classified runtime failure evidence are recorded; provider-backed success,
live image happy-path coverage, and release/commit evidence remain pending.

## Purpose

Map every take-home requirement to the existing RAGbench-MY platform and the Sprint 7 stories that add assessment-specific behavior. This prevents duplicate architecture and gives reviewers one place to verify coverage.

## Mandatory requirement mapping

| Assessment area | Existing RAGbench-MY capability reused | Sprint 7 owner | Code / automated evidence | Runtime / documentation |
|---|---|---|---|---|
| Engineering notes | Architecture, API, evaluation, monitoring, test and story documentation already exist | RB-32 | [`ENGINEERING_NOTES.md`](../ENGINEERING_NOTES.md); source index and decision ledger | Reviewer entry points in [`README.md`](../README.md); runtime record in [`docs/SPRINT_7_VERIFICATION.md`](SPRINT_7_VERIFICATION.md) |
| Upload API | durable document identity, storage, status, Airflow dispatch | RB-27 | `backend/internal/httpapi/documents.go`, `analyses.go`; `documents_test.go`, `analyses_test.go` | [`docs/API.md`](API.md); live TXT/corrupt-PDF multipart evidence; live image happy path pending |
| Text extraction | existing PDF/DOCX/TXT parser and ingestion state model | RB-27 | `airflow/dags/ragbench/content.py`; `airflow/tests/test_content.py` (PDF, image, corrupt paths) | Airflow container tests and live corrupt-PDF/receipt runs; restart persistence pending |
| LLM structured extraction | provider abstraction, prompt/model identity, trace conventions | RB-28 | `documentintelligence/schema.go`, `prompt.go`, `runner.go`; schema/workflow tests; `providers/openai_test.go` | Live `model_rate_limited` failure is recorded; provider-backed success pending |
| Schema validation | structured errors, migration/config/test conventions | RB-28 | `ParseFinancialData`, `SchemaError`, `schema_test.go`; persisted `schema_errors` in `0020` | Full persisted result awaits provider-backed success |
| Validation tool | deterministic Go business logic pattern | RB-29 | `validation.go`; exact-decimal, tolerance, date, missing-field, reconciliation tests | Valid/inconsistent/missing-field labeled fixtures and deterministic tests; live API path awaits provider success |
| Simple agent workflow | Airflow batch orchestration + small explicit Go interfaces | RB-30 | `DocumentIntelligenceAgent`, named tool interfaces, `workflow_test.go`, fixed Airflow DAG | Live ordered prefix and short-circuit failure recorded; full five-stage trace awaits provider success |
| Summary report | generation provider abstraction | RB-30 | `BuildSummaryPrompt`, `appendValidationNotice`, runner/workflow tests | Summary-provider failure path is unit-tested; provider-backed summary pending |
| Production-style engineering | PostgreSQL durability, migrations, structured errors, tests, traces, Compose | RB-27–RB-32 | migration `0020`, stage fencing, spans/events, cost state, full Go compile | Compose services healthy, API ready, migrations 1–20 applied; clean-checkout/restart evidence pending |
| Public repo + setup README | existing local Compose flow | RB-32 | README reviewer flow, API/data-model/evaluation docs, synthetic fixtures | Final commit/tag intentionally not created by this change |

## Optional/bonus mapping

| Bonus | Approach / status |
|---|---|
| Computer vision enhancement | bounded Tesseract image OCR in RB-27; layout/table extraction remains intentionally optional |
| Multi-agent | intentionally not required; the default design is a single bounded orchestrator |
| RAG | not coupled to primary extraction; existing RAG remains available for a future historical-invoice comparison |
| Guardrails | schema enforcement, evidence-aware prompting, deterministic validation, explicit unknown/null handling, bounded retries, and no manufactured confidence score |
| Observability | analysis spans/events, latency, provider usage/token, explicit cost state, structured logging, and failure taxonomy |

## Reviewer acceptance matrix

Before submission, every mandatory row should contain:

- code location
- automated test location
- one runtime verification reference
- failure-path evidence where applicable
- documentation/API link

No requirement should be marked complete based only on scaffolding or planned
design. Current runtime gaps are listed rather than presented as passes.
