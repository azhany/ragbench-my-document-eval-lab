# Sprint 7 Verification — Document Intelligence Assessment

Use this file as the runtime evidence record. Do not mark a story complete only because code exists.

## Environment

- Git commit: `<commit>`
- Date: `<date>`
- Docker/Compose versions: `<versions>`
- Model profile: `<profile>`
- Extraction/OCR profile: `<profile>`
- Schema version: `<version>`
- Validation policy version: `<version>`

## 1. Clean startup

- [ ] `sh scripts/dev.sh` starts the required services from a clean checkout.
- [ ] Database migrations are current.
- [ ] API readiness passes.

Evidence:

```text
<paste concise command/output references>
```

## 2. Upload and extraction

Fixtures:

- [ ] text-bearing invoice PDF
- [ ] JPG or PNG receipt/invoice
- [ ] unsupported/corrupt/unreadable fixture

Verify:

- [ ] durable `document_id`
- [ ] processing status is visible
- [ ] raw extraction/evidence is persisted or durably referenced
- [ ] failed extraction is visible and classified

## 3. Structured extraction

- [ ] output satisfies the financial schema
- [ ] dates/currency/amounts use normalized types
- [ ] absent evidence becomes `null`/unknown rather than fabricated content
- [ ] malformed LLM output is rejected/retried according to documented policy
- [ ] prompt/model/schema version is traceable

## 4. Deterministic validation

Exercise at least:

- [ ] valid totals
- [ ] inconsistent line-item/subtotal/total relationship
- [ ] missing important field
- [ ] date inconsistency when applicable

Verify findings are persisted and not hidden by summary generation.

## 5. Agent workflow

Expected ordered stages:

```text
extract_text -> structured_extract -> schema_validate -> financial_validate -> summarize
```

- [ ] successful workflow contains each stage
- [ ] validation runs before summary
- [ ] failed tool/stage creates visible failed state
- [ ] retry behavior is bounded and traceable

## 6. Observability

- [ ] document/analysis correlation ID
- [ ] per-stage processing time
- [ ] total processing time
- [ ] model/provider identity
- [ ] token/usage data when provider reports it
- [ ] cost state follows existing explicit known/unknown semantics
- [ ] structured failure code/message

## 7. Optional RAG comparison

If implemented:

- [ ] persisted structured result can be compared with relevant historical financial documents
- [ ] retrieved evidence is cited/traceable
- [ ] comparison failure does not invalidate the primary extraction result

## 8. Reviewer reproduction

From README only:

- [ ] reviewer can start the stack
- [ ] reviewer can submit a sample document
- [ ] reviewer can retrieve/inspect the final result
- [ ] reviewer can trigger/observe one controlled failure
- [ ] reviewer can find architecture/prompt/limitations/production notes in `ENGINEERING_NOTES.md`

## Sprint exit statement

`<Complete / blocked + concise evidence summary>`
