-- Sprint 8: settings-managed provider/model profiles.
--
-- Model names are application settings, not credentials. The provider adapter
-- and its endpoint/key remain server-side runtime configuration. Profiles can
-- be added from the Settings UI; existing RAG configs keep a concrete provider
-- and model snapshot so changing a profile cannot rewrite historical runs.

CREATE TABLE model_profiles (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL
        CONSTRAINT model_profiles_name_check
        CHECK (length(btrim(name)) BETWEEN 1 AND 120),
    kind TEXT NOT NULL
        CONSTRAINT model_profiles_kind_check
        CHECK (kind IN ('generation', 'embedding')),
    provider TEXT NOT NULL
        CONSTRAINT model_profiles_provider_check
        CHECK (length(btrim(provider)) > 0),
    model TEXT NOT NULL
        CONSTRAINT model_profiles_model_check
        CHECK (length(btrim(model)) > 0),
    dimensions INTEGER,
    enabled BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT model_profiles_dimensions_check CHECK (
        (kind = 'embedding' AND dimensions > 0)
        OR (kind = 'generation' AND dimensions IS NULL)
    ),
    CONSTRAINT model_profiles_name_kind_key UNIQUE (kind, name)
);

-- The shipped catalog is data now. These rows make a new environment usable
-- immediately while leaving additions and future profile edits to Settings.
INSERT INTO model_profiles (name, kind, provider, model, dimensions)
VALUES
    ('openai-gpt-4o-mini', 'generation', 'openai', 'gpt-4o-mini', NULL),
    ('opencode-go-glm-5.3-flash', 'generation', 'opencode-go', 'glm-5.3-flash', NULL),
    ('opencode-zen-big-pickle', 'generation', 'opencode-zen', 'big-pickle', NULL),
    ('opencode-zen-mimo-v2.5-free', 'generation', 'opencode-zen', 'mimo-v2.5-free', NULL),
    ('huggingface-gemma-3-4b-it-free', 'generation', 'huggingface-chat', 'google/gemma-3-4b-it:featherless-ai', NULL),
    ('openai-text-embedding-3-small', 'embedding', 'openai', 'text-embedding-3-small', 1536),
    ('huggingface-bge-small-en-v1.5', 'embedding', 'huggingface', 'BAAI/bge-small-en-v1.5', 384)
ON CONFLICT (kind, name) DO NOTHING;

-- Persist the generation identity beside the profile key. This mirrors the
-- embedding identity already stored by RB-03 and lets a UI-created profile be
-- executed without consulting a mutable in-process model-name registry.
ALTER TABLE rag_configs
    ADD COLUMN model_provider TEXT,
    ADD COLUMN model_name TEXT;

UPDATE rag_configs c
SET model_provider = p.provider,
    model_name = p.model
FROM model_profiles p
WHERE p.kind = 'generation' AND p.name = c.model_profile;

-- Every configuration created by the shipped binary has a seeded profile. The
-- fallback keeps the migration total for a database containing an older
-- manually inserted row; execution will report a provider failure rather than
-- silently selecting a different model.
UPDATE rag_configs
SET model_provider = COALESCE(NULLIF(model_provider, ''), 'unknown'),
    model_name = COALESCE(NULLIF(model_name, ''), model_profile)
WHERE model_provider IS NULL OR model_name IS NULL;

ALTER TABLE rag_configs
    ALTER COLUMN model_provider SET NOT NULL,
    ALTER COLUMN model_name SET NOT NULL,
    ADD CONSTRAINT rag_configs_model_provider_check CHECK (length(btrim(model_provider)) > 0),
    ADD CONSTRAINT rag_configs_model_name_check CHECK (length(btrim(model_name)) > 0);
