-- Keyword search over chunks (Phase 2). A generated tsvector, so the worker needs no change and
-- existing rows are filled by PostgreSQL when the column is added: no re-embedding, no API cost.
-- Text is normalised to runs of [A-Za-z0-9_] first, because the default parser would read dotted names as host
-- names and paths as single file tokens. The vector has three parts:
--   weight A: the symbol, its camelCase parts, and the words of the file path;
--   the content as written (lowercased by the 'simple' configuration);
--   the content with camelCase words split (isAbsoluteModule2 -> is absolute module2).
-- 'simple' does not stem, so identifiers stay exact. Idempotent: safe to apply again.
ALTER TABLE chunks ADD COLUMN IF NOT EXISTS search_tsv tsvector GENERATED ALWAYS AS (
    setweight(to_tsvector('simple',
        regexp_replace(coalesce(symbol, '') || ' '
            || regexp_replace(coalesce(symbol, ''), '([a-z0-9])([A-Z])', '\1 \2', 'g') || ' ' || file_path,
            '[^A-Za-z0-9_]+', ' ', 'g')), 'A')
    || to_tsvector('simple', regexp_replace(content, '[^A-Za-z0-9_]+', ' ', 'g'))
    || to_tsvector('simple', regexp_replace(regexp_replace(content, '([a-z0-9])([A-Z])', '\1 \2', 'g'), '[^A-Za-z0-9_]+', ' ', 'g'))
) STORED;
CREATE INDEX IF NOT EXISTS chunks_search_tsv ON chunks USING gin (search_tsv);
