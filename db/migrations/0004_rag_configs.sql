-- RB-03: immutable RAG configurations.
--
-- A saved configuration is an immutable experiment identity: settings are
-- never updated in place. Changing settings means saving a new configuration
-- under a new name, so historical runs and traces keep pointing at the exact
-- configuration that produced them.
--
-- Registry-governed values (prompt_version, model_profile, embedding_*) are
-- validated against the known-profile registry in
-- backend/internal/providers; the database enforces structure and bounds,
-- the application enforces registry membership.

CREATE TABLE rag_configs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL
        CONSTRAINT rag_configs_name_check
        CHECK (length(btrim(name)) BETWEEN 1 AND 200),
    chunk_size INTEGER NOT NULL
        CONSTRAINT rag_configs_chunk_size_check
        CHECK (chunk_size BETWEEN 1 AND 8192),
    chunk_overlap INTEGER NOT NULL
        CONSTRAINT rag_configs_chunk_overlap_check CHECK (chunk_overlap >= 0),
    -- Overlap must leave room for new content in every chunk.
    CONSTRAINT rag_configs_overlap_size_check CHECK (chunk_overlap < chunk_size),
    retrieval_mode TEXT NOT NULL
        CONSTRAINT rag_configs_retrieval_mode_check
        CHECK (retrieval_mode IN ('vector', 'hybrid')),
    top_k INTEGER NOT NULL
        CONSTRAINT rag_configs_top_k_check CHECK (top_k BETWEEN 1 AND 100),
    rerank_enabled BOOLEAN NOT NULL DEFAULT false,
    prompt_version TEXT NOT NULL
        CONSTRAINT rag_configs_prompt_version_check
        CHECK (length(btrim(prompt_version)) > 0),
    model_profile TEXT NOT NULL
        CONSTRAINT rag_configs_model_profile_check
        CHECK (length(btrim(model_profile)) > 0),
    -- Resolved embedding identity: the profile key plus the concrete
    -- provider/model/dimensions it resolved to at creation time, so the
    -- identity stays interpretable even if the registry changes later.
    -- Must be compatible with index_revisions embedding identity (RB-07).
    embedding_profile TEXT NOT NULL
        CONSTRAINT rag_configs_embedding_profile_check
        CHECK (length(btrim(embedding_profile)) > 0),
    embedding_provider TEXT NOT NULL,
    embedding_model TEXT NOT NULL,
    embedding_dimensions INTEGER NOT NULL
        CONSTRAINT rag_configs_embedding_dimensions_check
        CHECK (embedding_dimensions > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Names identify configurations in the UI and evaluation contracts;
-- duplicates are rejected so a name always resolves to one immutable identity.
CREATE UNIQUE INDEX rag_configs_name_key ON rag_configs (name);
