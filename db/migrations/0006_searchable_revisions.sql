-- RB-07: new retrieval must explicitly select one saved embedding profile and
-- only see the live document's fully published active revision. Historical
-- chunks remain directly addressable by their immutable evidence IDs.
CREATE FUNCTION searchable_chunks(requested_config_id UUID)
RETURNS SETOF doc_chunks LANGUAGE sql STABLE AS $$
    SELECT c.* FROM doc_chunks c
    JOIN documents d ON d.id=c.document_id AND d.active_revision_id=c.index_revision_id
    JOIN index_revisions r ON r.id=c.index_revision_id
    JOIN rag_configs cfg ON cfg.id=requested_config_id
    WHERE d.deleted_at IS NULL AND r.status='ready' AND c.embedding IS NOT NULL
      AND r.embedding_provider=cfg.embedding_provider
      AND r.embedding_model=cfg.embedding_model
      AND r.embedding_dimensions=cfg.embedding_dimensions;
$$;

CREATE FUNCTION validate_ready_revision() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.status='ready' AND OLD.status<>'ready' THEN
        IF NOT EXISTS (SELECT 1 FROM doc_chunks WHERE index_revision_id=NEW.id)
           OR EXISTS (SELECT 1 FROM doc_chunks WHERE index_revision_id=NEW.id
                      AND (embedding IS NULL OR vector_dims(embedding)<>NEW.embedding_dimensions)) THEN
            RAISE EXCEPTION 'cannot publish incomplete revision';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER index_revisions_publish_check BEFORE UPDATE ON index_revisions
    FOR EACH ROW EXECUTE FUNCTION validate_ready_revision();
