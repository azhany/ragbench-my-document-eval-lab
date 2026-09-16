RAGbench-MY golden dataset v1 — reviewed demonstration cases.

Scope: a stable, versioned benchmark for Sprint 4 evaluation. The expected
evidence references the four demonstration source files under db/fixtures/
corpus/; these are original fictional demonstration documents written for
this PoC (no copyrighted material). The relevance unit is the document:
questions in this version are answerable from one named source document, so
document-level Recall@K/MRR stays valid across chunk-size changes.

Version: 1 (20 cases). Edit through the versioned case-edit API, never in
place; eval runs pin the version they scored.
