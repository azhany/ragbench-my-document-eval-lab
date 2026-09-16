# README section — Assessment Review

Add near the top of the root README after the portfolio story / MVP overview.

## AI/ML Take-Home Assessment — Document Intelligence Agent

This repository also contains a bounded financial-document intelligence workflow built as Sprint 7 on top of the existing RAGbench-MY platform.

### Assessment flow

```text
PDF/JPG/PNG
  -> text extraction / OCR
  -> LLM structured financial extraction
  -> schema validation
  -> deterministic financial validation
  -> concise summary
  -> persisted trace / latency / usage / validation evidence
```

The implementation intentionally reuses the repository's production-minded foundations instead of creating a separate demo service: durable PostgreSQL state, explicit migrations, Go domain logic/provider interfaces, Airflow batch orchestration, structured errors, trace/cost conventions, Docker Compose, and repeatable tests.

### Reviewer guide

1. Start the stack using the normal local-development instructions.
2. Apply pending database migrations.
3. Configure the documented model/OCR provider credentials in the uncommitted `.env` file.
4. Submit one of the public sample financial documents using the documented assessment endpoint.
5. Poll/read the result until the workflow completes.
6. Inspect structured data, validation findings, summary, and trace/usage metadata.
7. See `docs/ASSESSMENT_MAPPING.md` for requirement-to-evidence coverage.
8. See `docs/SPRINT_7_VERIFICATION.md` for the verified runtime/test evidence.
9. See `ENGINEERING_NOTES.md` for architecture decisions, prompt strategy, limitations, guardrails, and production improvements.

> Replace this guide with exact endpoint/curl commands and committed public fixture paths after implementation. Do not publish placeholder commands as verified behavior.
