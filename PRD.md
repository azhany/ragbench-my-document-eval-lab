# Product Requirements Document

## Product

**RAGbench-MY — Document Library Eval Lab**

## Problem

A typical RAG PoC can answer questions, but teams often cannot clearly answer:

- Did retrieval improve after changing chunk size?
- Did a new prompt reduce hallucination?
- Is better quality worth the additional token cost?
- Which document or pipeline stage caused a bad answer?
- Did the newest configuration regress compared with the baseline?

## Goal

Build a small Document Library PoC where every important RAG configuration can be evaluated and monitored using repeatable datasets and stored experiment runs.

## Primary users

### Developer / evaluator
Uploads documents, configures experiments, runs evaluations, compares results.

### Reviewer
Uses document chat and inspects citations, quality scores, latency, and cost.

## Core requirements

### Document Library
- Upload PDF, DOCX, TXT.
- List documents and ingestion status.
- Show chunk count and processing failure.
- Delete/reprocess document.

### RAG Chat
- Ask questions across processed documents.
- Return answer with source citations.
- Show retrieved chunks when requested.
- Record latency, model, token usage, estimated cost, retrieval config, and prompt version.

### Evaluation
- Maintain a small golden dataset of questions and expected evidence/answers.
- Run evaluation against a named configuration.
- Store per-case and aggregate metrics.
- Compare current run with baseline.
- Flag regressions.

### Tuning / Experiments
Support a deliberately small parameter matrix:
- chunk size
- chunk overlap
- top-k
- retrieval mode: vector / hybrid
- optional reranking on/off
- prompt version
- model profile

### Monitoring
Track:
- request success/error
- ingestion failure
- p50/p95 latency
- retrieval latency
- generation latency
- input/output tokens
- estimated cost
- answer quality trend
- retrieval quality trend

## MVP evaluation metrics

### Retrieval
- Recall@K
- MRR
- optional nDCG@K

### Generation
- answer relevance
- faithfulness / groundedness
- citation correctness or evidence match

### Operational
- p50/p95 latency
- token usage
- estimated cost/query
- error rate

## Success criteria

The PoC is successful when:

1. The same evaluation dataset can be run against two configurations.
2. The UI can show which configuration is better and by how much.
3. Each chat/evaluation case has enough trace data to diagnose retrieval vs generation failure.
4. A regression can be detected using explicit thresholds.
5. The whole stack starts locally through Docker Compose.
