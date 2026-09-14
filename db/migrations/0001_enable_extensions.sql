-- RB-02: enable pgvector for embedding storage and retrieval.
--
-- UUID generation does not need an extension: gen_random_uuid() is built into
-- PostgreSQL core since version 13, and this stack runs PostgreSQL 16.

CREATE EXTENSION IF NOT EXISTS vector;
