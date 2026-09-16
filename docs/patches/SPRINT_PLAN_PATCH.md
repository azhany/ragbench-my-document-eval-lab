# Patch for `docs/SPRINT_PLAN.md`

## A. Update the delivery target language

After the existing six required sprint gates, add:

> **Assessment extension:** Sprint 7 is an additional portfolio/take-home delivery increment. It does not redefine the original six-sprint RAGBench-MY core acceptance gate; it reuses the completed platform to demonstrate document intelligence, structured extraction, validation, and a simple tool-oriented agent workflow.

## B. Add to the Sprint roadmap table

| Sprint | Goal | Required stories | Exit demonstration |
|---|---|---|---|
| 7 — Document intelligence assessment | Process a financial PDF/image into validated structured data plus a concise summary, using the existing production-minded platform | RB-27–RB-32 | Upload a PDF/JPG/PNG invoice or receipt; persist raw extraction; produce schema-valid structured data; execute deterministic validation before summary generation; inspect trace/usage/latency/error evidence; reviewer can reproduce from README |

Add after the existing dependency-chain paragraph:

> Sprint 7 depends on the durable document lifecycle/provider/trace foundations established earlier, but its financial-document result is independent from RAG chat. Retrieval over historical financial documents is an optional extension, not a prerequisite for mandatory structured extraction.

## C. Add to Detailed stories

### Sprint 7 — Document intelligence assessment

- [RB-27 — Accept financial PDFs and images for document intelligence](stories/RB-27.md)
- [RB-28 — Extract schema-valid financial data with the LLM](stories/RB-28.md)
- [RB-29 — Validate financial document data deterministically](stories/RB-29.md)
- [RB-30 — Orchestrate extraction, validation, and reporting as an agent workflow](stories/RB-30.md)
- [RB-31 — Trace, evaluate, and guard the document-intelligence workflow](stories/RB-31.md)
- [RB-32 — Package reproducible assessment evidence and engineering notes](stories/RB-32.md)

## D. Add to Coverage and scope control

| Documented requirement | Owning stories |
|---|---|
| Financial PDF/image upload, durable identity, status | RB-27 |
| PDF parsing and JPG/PNG OCR/vision extraction | RB-27 |
| LLM structured financial extraction, JSON/schema validation, extraction errors | RB-28 |
| Missing-field, date, amount, line-item/total business validation | RB-29 |
| Explicit tool calls and ordered agent orchestration | RB-30 |
| Summary generated only after validation result exists | RB-30 |
| Trace, processing time, token usage, confidence/retry guardrails, evaluation | RB-31 |
| Assessment mapping, README, verification evidence, root ENGINEERING_NOTES.md | RB-32 |
| Optional comparison with prior invoices through existing RAG capability | RB-31 (optional extension) |

## E. Add Sprint 7 final acceptance walkthrough

9. **Financial document upload:** upload at least one text-bearing PDF and one JPG/PNG financial document; verify durable `document_id`, status transitions, and visible extraction failures.
10. **Structured extraction:** inspect the persisted structured result and verify schema validity, normalized types, provenance/raw text linkage, and explicit null/unknown behavior rather than invented values.
11. **Validation before reporting:** verify deterministic validation runs before summary generation and records pass/warning/failure findings; intentionally exercise at least one inconsistent-total or missing-field fixture.
12. **Agent trace:** inspect ordered spans/tool calls for extraction, structured extraction, validation, and summary, including processing time and provider usage when reported.
13. **Reviewer reproduction:** from a clean checkout, follow README instructions and reproduce one successful document and one controlled failure; verify `ENGINEERING_NOTES.md` describes architecture decisions, prompt strategy, limitations, and production improvements.
