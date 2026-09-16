# AI/ML Take-Home Assessment Mapping

Status: living reviewer map. Update code/test/evidence links as Sprint 7 is implemented.

## Purpose

Map every take-home requirement to the existing RAGbench-MY platform and the Sprint 7 stories that add assessment-specific behavior. This prevents duplicate architecture and gives reviewers one place to verify coverage.

## Mandatory requirement mapping

| Assessment area | Existing RAGbench-MY capability reused | Sprint 7 owner | Expected evidence |
|---|---|---|---|
| Engineering notes | Architecture, API, evaluation, monitoring, test and story documentation already exist | RB-32 | root `ENGINEERING_NOTES.md` with architecture, prompt design, limitations, production improvements, and source-document index |
| Upload API | durable document identity, storage, status, Airflow dispatch | RB-27 | PDF/JPG/PNG upload smoke + API contract + failure tests |
| Text extraction | PDF extraction pipeline and ingestion state model | RB-27 | PDF parser plus image OCR/vision fixture results |
| LLM structured extraction | provider abstraction, prompt/model identity, trace conventions | RB-28 | schema-valid financial JSON, malformed/provider/error-path tests |
| Schema validation | structured errors, migration/config/test conventions | RB-28 | schema validation tests and persisted extraction status |
| Validation tool | deterministic Go business logic pattern | RB-29 | missing-field and amount/date/total validation tests |
| Simple agent workflow | Airflow batch orchestration + small explicit Go interfaces | RB-30 | ordered tool-call trace and final result |
| Summary report | generation provider abstraction | RB-30 | summary only after validation result exists |
| Production-style engineering | PostgreSQL durability, migrations, structured errors, tests, traces, Compose | RB-27–RB-32 | clean-start verification and failure evidence |
| Public repo + setup README | existing public repository and local Compose flow | RB-32 | reviewer section in README |

## Optional/bonus mapping

| Bonus | Approach |
|---|---|
| Computer vision enhancement | image OCR/vision path in RB-27; layout/table extraction can remain explicitly optional |
| Multi-agent | intentionally not required; the default design stays a single bounded orchestrator to avoid unnecessary complexity |
| RAG | reuse existing indexed-document/RAG path to compare an extracted invoice with historical documents; keep separate from mandatory extraction correctness |
| Guardrails | schema enforcement, evidence-aware prompting, deterministic validation, explicit unknown/null handling, bounded retries, confidence metadata where meaningful |
| Observability | reuse trace/span, latency, provider usage/token, cost, structured logging, and failure taxonomy conventions |

## Reviewer acceptance matrix

Before submission, every mandatory row should contain:

- code location
- automated test location
- one runtime verification reference
- failure-path evidence where applicable
- documentation/API link

No requirement should be marked complete based only on scaffolding or planned design.
