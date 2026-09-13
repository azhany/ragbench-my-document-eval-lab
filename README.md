# RAGbench-MY — Document Library Eval Lab

A small portfolio PoC based on a document-library/RAG workload, focused on **evaluation, tuning, cost, tracing, and monitoring** rather than building another large AI application.

## Purpose

Demonstrate a production-minded RAG workflow using the preferred stack:

- Go API/backend
- Vue.js frontend
- Apache Airflow for ingestion and evaluation jobs
- PostgreSQL + pgvector
- Docker Compose
- Python only where Airflow/evaluation tooling benefits from the ecosystem

The project should feel like a simplified internal Document Library system: upload documents, process/chunk/embed them, ask grounded questions, inspect citations, run repeatable evaluation suites, compare configurations, and monitor quality/cost/latency.

## Portfolio story

> I took a familiar enterprise Document Library / RAG workflow and added an evaluation and observability control layer so retrieval and generation changes can be measured instead of guessed.

## MVP flows

1. Upload a document.
2. Airflow processes it: extract → normalize → chunk → embed → index.
3. User asks a question in the chat UI.
4. Go retrieves context and calls the configured LLM.
5. The response returns citations plus trace/cost metadata.
6. User runs a fixed evaluation dataset.
7. Evaluation results are stored and compared against a baseline.
8. Monitor page shows latency, error rate, token usage, cost, and quality regressions.

## Keep it small

Do **not** build:
- a full multi-tenant SaaS
- complex agents
- Kubernetes
- a large auth/permissions system
- dozens of evaluation metrics

The PoC should prove one thing well: **RAG changes are measurable, comparable, and observable.**
