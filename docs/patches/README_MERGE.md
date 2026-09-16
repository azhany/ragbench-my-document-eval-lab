# RAGbench-MY Sprint 7 Assessment Extension — Merge Guide

This bundle extends the existing RAGbench-MY planning/documentation conventions without creating a parallel project structure.

## Files to add

- `ENGINEERING_NOTES.md` — root-level living engineering compendium required by the assessment.
- `docs/ASSESSMENT_MAPPING.md` — requirement-to-story and evidence mapping for reviewers.
- `docs/SPRINT_7_VERIFICATION.md` — runtime/test evidence record for the sprint exit gate.
- `docs/stories/RB-27.md` … `RB-32.md` — Sprint 7 stories using the existing story format.

## Existing files to update

- `docs/SPRINT_PLAN.md` — apply the content in `SPRINT_PLAN_PATCH.md`.
- `ARCHITECTURE.md` — add the document-intelligence flow from `ARCHITECTURE_PATCH.md`.
- `README.md` — add the reviewer-facing section from `README_ASSESSMENT_SECTION.md`.

## Design principle

Sprint 7 is an extension of the existing six-sprint RAG evaluation lab, not a second application. It reuses document identity/storage, provider abstractions, trace/cost conventions, PostgreSQL durability, and existing evaluation/observability capabilities.

The assessment workflow is intentionally explicit:

`upload -> text extraction/OCR -> structured extraction -> deterministic validation -> summary -> persisted result/trace`

The word “agent” refers to this small orchestrator and its tool calls. Do not introduce a general-purpose agent framework or multi-agent topology unless it is deliberately implemented as an optional bonus.

## Completion workflow

1. Add Sprint 7 and RB-27–RB-32.
2. Implement each story and keep acceptance criteria unchecked until verified.
3. Record runtime evidence in `docs/SPRINT_7_VERIFICATION.md`.
4. Update `ENGINEERING_NOTES.md` whenever implementation changes architecture decisions, prompts, limitations, guardrails, or production recommendations.
5. Before submission, reconcile `docs/ASSESSMENT_MAPPING.md` so every mandatory requirement points to concrete code/tests/evidence.
6. Add the reviewer section to `README.md` and run the documented clean-start verification.
