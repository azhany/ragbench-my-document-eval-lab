# AGENTS.md

## Project intent

This repository is a small, finishable RAG evaluation and observability PoC. Prefer clarity and measurable behavior over framework complexity.

## Architecture rules

1. PostgreSQL is the source of truth for durable state.
2. Go owns synchronous API/query behavior.
3. Airflow owns batch ingestion and evaluation orchestration.
4. Python must not become a second application backend.
5. Keep provider integrations behind small interfaces.
6. All RAG configuration used for a run must be persisted.
7. Every generated answer must be traceable to retrieved source chunks.
8. Evaluation code must call the same query pipeline used by normal chat where practical.
9. No hidden magic constants for scoring thresholds; store/configure them explicitly.
10. Prefer shell scripts over Makefiles.

## Coding principles

- Small packages with explicit dependencies.
- Avoid premature microservices.
- Return structured errors.
- Use migrations for schema changes.
- Add tests around retrieval scoring, experiment comparison, and cost calculation.
- Keep UI state simple; Pinia only where shared state is justified.
- Do not add infrastructure that is not exercised by the PoC.

## Definition of done for a feature

A feature is complete only when:
- happy path works
- important failure path is visible
- metrics/logging exist
- API contract is documented
- at least one test covers its core rule
