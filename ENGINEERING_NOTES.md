# ENGINEERING_NOTES.md

## Purpose

This is the assessment-facing engineering compendium for **RAGbench-MY — Document Library Eval Lab** and its **Sprint 7 Document Intelligence Agent** extension.

It summarizes the decisions that materially affect implementation and review. Detailed API/schema/test contracts remain in their canonical repository documents; this file links those decisions together so a reviewer can understand why the system is shaped this way.

Update this file whenever a completed story changes architecture, prompts, validation semantics, guardrails, limitations, or production recommendations.

---

## 1. System context

RAGbench-MY began as a small production-minded Document Library / RAG evaluation lab. The core platform uses:

- Go for the synchronous application/API path and deterministic domain logic
- Vue.js for the reviewer/developer UI
- Apache Airflow for asynchronous ingestion/evaluation workflows
- PostgreSQL + pgvector as durable application state and vector storage
- Docker Compose for local reproducibility
- Python only where Airflow/evaluation tooling benefits from the ecosystem, not as a second application backend

Sprint 7 adds financial-document intelligence without creating a second application. Existing document identity/storage, provider abstractions, migrations, traces, cost semantics, tests, and observability are reused.

Canonical sources:

- `README.md`
- `PRD.md`
- `ARCHITECTURE.md`
- `AGENTS.md`
- `docs/API.md`
- `docs/SPRINT_PLAN.md`
- `docs/TEST_PLAN.md`
- `docs/EVALUATION.md`
- `docs/MONITORING.md`
- `docs/stories/RB-27.md` … `RB-32.md`

---

## 2. Architecture decisions

### 2.1 Keep one repository and one platform

The assessment workflow is implemented inside the existing repository. This avoids a throwaway assessment service and demonstrates how a new AI workload is added to an established production-shaped platform.

### 2.2 PostgreSQL remains authoritative

Document identity, analysis state, structured extraction, validation findings, trace identities, version metadata, and final result state should be durable. Workflow memory or Airflow task state is not the source of truth for user-visible results.

### 2.3 Go retains domain and validation semantics

Financial schemas, deterministic validation, API contracts, provider/tool interfaces, and structured error semantics remain in the Go application boundary. This keeps business rules strongly typed and directly testable.

### 2.4 Airflow owns asynchronous workflow orchestration

Document intelligence is a batch-style pipeline and follows the existing architecture boundary. Airflow coordinates long-running stages and retries, while durable result/state is persisted through the application data model.

### 2.5 The “agent” is deliberately bounded

The assessment asks for a simple agent workflow. Here, `DocumentIntelligenceAgent` means an explicit orchestrator over named tools/functions:

```text
extract_text
  -> structured_extract
  -> schema_validate
  -> financial_validate
  -> summarize
```

The tool set is fixed for this workflow. A general-purpose agent framework, autonomous tool discovery, or multi-agent hierarchy would add complexity without improving the mandatory use case.

### 2.6 Validation is separated from extraction

The model extracts candidate facts. Deterministic code checks business rules and arithmetic. The same LLM is not trusted to both produce and self-certify financial totals.

### 2.7 RAG remains optional for the assessment core

Historical invoice comparison can reuse the existing RAG path, but retrieval is not necessary to extract and validate the current document. This keeps extraction correctness independent from retrieval quality.

---

## 3. Document intelligence data contract

The exact schema is versioned in code. At minimum the structured result should support the assessment's financial-document fields while keeping absent evidence explicit.

Representative fields:

- document type
- vendor / merchant
- invoice or receipt number when present
- invoice/document date
- due date when applicable
- currency
- subtotal/tax/discount/total when evidenced
- line items with description and available quantity/unit-price/amount information
- extraction/schema version metadata

### Null/unknown behavior

A missing field is not equivalent to an empty string or zero. If the document does not support a value, the result should represent it as absent/unknown according to the schema. Prompts and validation must not invent values merely to satisfy a field list.

---

## 4. Text extraction and OCR design

### PDFs

Reuse the existing text-bearing PDF extraction path where practical so assessment behavior benefits from the document library's established storage, status, and failure handling.

### Images

JPG/PNG inputs require a bounded OCR or vision-language extraction path. The workflow records which extraction method/profile produced the evidence.

### Extraction evidence

Structured extraction remains traceable to raw extracted text or a durable evidence reference. A reviewer should be able to distinguish:

- unreadable/corrupt source
- extraction/OCR failure
- successful extraction with weak/partial text
- downstream LLM/schema failure

These are different failure classes and should not collapse into one generic “AI failed” state.

---

## 5. Prompt design

### Goal

The extraction prompt converts document evidence into the versioned financial schema. It is not asked to provide business validation or a persuasive explanation.

### Prompt principles

1. **Evidence-only extraction** — use only information present in the supplied document evidence.
2. **No forced guessing** — use explicit null/unknown behavior when a value is not evidenced.
3. **Typed output** — follow the declared schema and normalized date/amount/currency conventions.
4. **No arithmetic self-approval** — extract stated line-item/totals; deterministic code performs reconciliation.
5. **Stable versioning** — persist prompt version and model profile so results can be reproduced and compared.
6. **Minimize irrelevant prose** — prefer structured output mode/JSON schema when the provider supports it.

### Summary prompt

Summary generation happens only after structured extraction and deterministic validation. The summary receives normalized structured data plus validation findings and must not hide material warnings/failures.

### Prompt changes

Any prompt change that can alter extraction semantics receives a new prompt version and should be evaluated on the same labeled financial-document set before replacing the baseline.

---

## 6. Schema and error handling

Model output is untrusted input.

The pipeline distinguishes at least:

- provider/network failure
- malformed/unparseable model output
- schema-invalid structured output
- successful schema parse with missing optional/important fields
- deterministic validation warning/failure
- summary-generation failure after a valid extraction

Retries are bounded and based on failure class. Deterministic validation failures are not “fixed” by repeatedly asking the model until the numbers happen to agree.

---

## 7. Deterministic financial validation

Validation rules use stable identifiers and a versioned policy.

Representative checks:

- required/important field presence by document type
- recognized currency/date/amount representation
- invoice date and due-date ordering when both exist
- non-negative/valid monetary values where the business rule requires it
- line-item extension reconciliation when quantity/unit price are available
- line-item sum versus subtotal
- subtotal/tax/discount versus total

### Decimal handling

Use decimal arithmetic rather than binary floating point for financial reconciliation. Any tolerance for rounding differences must be explicit, tested at boundaries, and recorded in the validation policy rather than hidden as a magic constant.

### Validation result

The final result preserves validation findings. A fluent summary cannot overwrite or suppress an inconsistency discovered by deterministic rules.

---

## 8. Agent/tool workflow

The tool contract is intentionally small:

1. `extract_text(document)`
2. `extract_financial_data(evidence, schema_version, prompt_version)`
3. `validate_financial_data(structured_data, policy_version)`
4. `generate_summary(structured_data, validation_findings)`

The orchestrator records stage state and correlation metadata. If a required stage fails, later stages cannot silently produce a fully successful final result.

---

## 9. Guardrails and reliability

The primary reliability mechanisms are structural rather than conversational:

- file/type/size limits
- immutable source identity/checksum where supported by existing document lifecycle
- evidence-preserving extraction
- structured-output/schema enforcement
- explicit unknown/null semantics
- deterministic financial validation
- bounded retries by failure class
- persisted prompt/model/schema/validation-policy identity
- structured errors and visible failed/partial state
- trace correlation across stages

Confidence scoring is included only if its semantics are defensible. A model saying “90% confident” is not automatically treated as a calibrated probability.

---

## 10. Observability

The document-intelligence workflow follows the existing trace-first philosophy.

Useful spans/events include:

- request/upload acceptance
- text extraction / OCR
- structured LLM extraction
- schema validation
- deterministic financial validation
- summary generation
- persistence/finalization

Record where available:

- document/analysis/trace identity
- stage and total latency
- provider/model/profile
- token/usage information reported by the provider
- explicit known/unknown estimated cost state
- retry count/reason
- classified error code/message
- validation findings

Missing provider usage is unknown, not zero.

---

## 11. Evaluation strategy

A single successful invoice is insufficient evidence.

Use a small labeled financial-document set covering multiple layouts and failure conditions. Keep dimensions separate:

### Extraction quality

- schema-valid response rate
- exact/normalized match for key scalar fields
- line-item extraction accuracy where labeled
- unsupported/hallucinated field rate or evidence mismatch checks where practical

### Validation quality

- detection of known inconsistent totals
- detection of missing required/important fields
- false-positive validation findings on clean fixtures

### Operational quality

- success/error rate
- p50/p95 total and stage latency when sample size makes percentile reporting meaningful
- token/usage and estimated cost where available
- retry/failure distribution

RAG metrics remain separate from extraction metrics when the optional historical-comparison feature is exercised.

---

## 12. Testing strategy

Tests should cover:

- upload/type/size/path safety
- PDF and image extraction fixtures
- schema parsing and unexpected/malformed model output
- deterministic decimal/tolerance/date rules
- ordered agent workflow and failure short-circuiting
- provider failure classification and bounded retry behavior
- persistence/restart behavior
- trace/usage/cost known/unknown semantics
- optional historical RAG comparison without corrupting the primary result

Normal automated tests should not require paid provider calls. Real-provider smoke/evaluation runs are deliberate and recorded separately.

---

## 13. Security and data handling

- Provider credentials remain in uncommitted environment configuration.
- Do not commit real private invoices, receipts, personal identifiers, or customer financial documents as fixtures.
- Public fixtures should be synthetic or redistributable test documents.
- Uploaded document bytes and extracted evidence should follow the existing local-development storage/retention model; production retention/deletion policy requires explicit design.
- Prompts treat document content as data, not as trusted instructions. Production hardening should include prompt-injection/document-instruction defenses if documents can be adversarial.

---

## 14. Known limitations

Keep this section synchronized with verified behavior. Expected PoC limitations include:

1. OCR/layout quality varies with scans, image resolution, rotation, handwriting, complex tables, and provider capability.
2. Financial schemas cannot model every invoice/receipt convention without becoming domain-specific and large.
3. Line-item reconciliation can be ambiguous when documents contain bundled discounts, inclusive taxes, rounding, credits, or multiple totals.
4. LLM structured extraction can still omit or misread evidence even when its JSON is schema-valid.
5. Confidence metadata is not necessarily calibrated unless explicitly evaluated.
6. The PoC is not a multi-tenant financial processing system and should not be treated as an accounting source of truth.
7. Historical-document RAG comparison inherits retrieval/index quality constraints from the RAG path.
8. Local Docker Compose/provider setup is optimized for reviewability, not high-throughput production serving.

Replace or refine these statements with observed limitations from Sprint 7 verification.

---

## 15. What I would improve for production

Priority production improvements:

1. **Document security and retention** — encryption at rest, explicit retention/erasure policy, access control, audit policy, malware/file scanning where appropriate.
2. **OCR/layout robustness** — evaluated fallback strategy across parser/OCR/VLM/table extraction based on document class and quality.
3. **Human review path** — route low-confidence/material validation failures to review rather than auto-accepting financial data.
4. **Calibrated quality controls** — larger representative labeled corpus, field-level thresholds, drift/regression gates, and calibration before using confidence operationally.
5. **Idempotency and concurrency hardening** — duplicate submission semantics, worker crash recovery, stage idempotency, queue/backpressure behavior.
6. **Observability export** — OpenTelemetry/central logs/metrics if operating beyond the local PoC, while retaining current trace identities.
7. **Cost governance** — quotas/budgets, provider/model fallback policy, cached/reused extraction where safe, explicit pricing-version updates.
8. **Schema/version migration strategy** — backward-compatible result reading and reprocessing policy as extraction schemas/prompts/validators evolve.
9. **Adversarial document defenses** — prompt-injection testing, content sanitization/boundaries, strict tool permissions, and evaluator cases for malicious document instructions.
10. **Operational SLOs** — define expected throughput/latency/error targets based on real workload rather than demo assumptions.

---

## 16. Engineering decision / notes ledger

Use this table for future implementation discoveries. Keep concise; link to the detailed story/ADR/test where applicable.

| Date | Story | Decision / observation | Why | Evidence / follow-up |
|---|---|---|---|---|
| `<date>` | RB-27 | `<decision>` | `<reason>` | `<link/path>` |
| `<date>` | RB-28 | `<decision>` | `<reason>` | `<link/path>` |
| `<date>` | RB-29 | `<decision>` | `<reason>` | `<link/path>` |
| `<date>` | RB-30 | `<decision>` | `<reason>` | `<link/path>` |
| `<date>` | RB-31 | `<decision>` | `<reason>` | `<link/path>` |
| `<date>` | RB-32 | `<decision>` | `<reason>` | `<link/path>` |

---

## 17. Reviewer navigation

For assessment review, start with:

1. `README.md` — setup and reviewer flow
2. `docs/ASSESSMENT_MAPPING.md` — requirement coverage
3. `docs/SPRINT_7_VERIFICATION.md` — runtime/test evidence
4. `ENGINEERING_NOTES.md` — engineering rationale and limitations
5. `docs/stories/RB-27.md` … `RB-32.md` — implementation stories/acceptance criteria
6. `docs/API.md` / `ARCHITECTURE.md` / `docs/TEST_PLAN.md` — detailed contracts
