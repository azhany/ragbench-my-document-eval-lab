# Architecture patch — Document Intelligence path

Add a new section to `ARCHITECTURE.md`.

## Document intelligence path

Sprint 7 extends the existing document platform with a bounded financial-document workflow. PostgreSQL remains authoritative; Go owns domain contracts, validation rules, provider/tool interfaces, result APIs, and trace semantics; Airflow remains the asynchronous workflow orchestrator.

```text
financial PDF/JPG/PNG upload
  ↓
persist document + analysis run
  ↓
Airflow document_intelligence workflow
  ↓
text extraction / OCR tool
  ↓
LLM structured extraction tool
  ↓
schema validation
  ↓
deterministic financial validation tool
  ↓
summary generation tool
  ↓
persist result + validation findings + trace/usage/latency
  ↓
reviewer-facing API/UI result
```

### Boundary with the RAG path

The mandatory assessment path does not require retrieval. Structured financial extraction is performed from the uploaded document evidence. Existing RAG infrastructure may optionally retrieve previous invoices/receipts for comparison after the primary extraction result is persisted.

### Agent semantics

`DocumentIntelligenceAgent` is a small explicit orchestrator over named tools. It does not autonomously discover arbitrary tools, mutate infrastructure, or create an additional service boundary. Tool order is constrained so validation always occurs before final summary generation.

### Evidence and reproducibility

Persist enough immutable context to explain a result later: document/revision identity, extraction method, raw extracted text or evidence reference, prompt/version, model profile, schema version, validation policy/version, tool outcomes, usage, latency, retry/error state, and final summary.
