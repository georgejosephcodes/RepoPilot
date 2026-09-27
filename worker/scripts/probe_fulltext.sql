-- Probe: how PostgreSQL full-text search tokenises code, and what a generated
-- tsvector column on chunks would cost. Read-only on real tables: everything is built in a TEMP table that
-- disappears when the session ends. No API cost.
-- Run: cd /home/georgejoseph/Rag-project && docker compose exec -T postgres psql -U repopilot -d repopilot < worker/scripts/probe_fulltext.sql
\timing off
\echo '=== 1. default parser on code tokens (simple configuration)'
SELECT alias, token FROM ts_debug('simple',
  'want_bytes isAbsoluteModule2 HMACAlgorithm.get_signature src/itsdangerous/signer.py "Signature age %s > %s seconds" func (s *Server) poll() _{PROG_NAME}_COMPLETE make-pass')
WHERE alias <> 'blank';

\echo '=== 2. camelCase split with regexp_replace'
SELECT to_tsvector('simple', regexp_replace('isAbsoluteModule2 HMACAlgorithm getObjectType URLSafeSerializerMixin', '([a-z0-9])([A-Z])', '\1 \2', 'g'));

\echo '=== 3. english vs simple on a question'
SELECT to_tsvector('english', 'Where does the test runner replace stdin and stdout?') AS english,
       to_tsvector('simple',  'Where does the test runner replace stdin and stdout?') AS simple;

\echo '=== 4. generated column on a temp copy of chunks: accepted? how long? how big?'
CREATE TEMP TABLE probe_chunks AS SELECT id, repo_id, file_path, symbol, kind, start_line, end_line, content FROM chunks;
SELECT count(*) AS chunks, pg_size_pretty(sum(length(content))::bigint) AS content_size FROM probe_chunks;
\timing on
ALTER TABLE probe_chunks ADD COLUMN tsv tsvector GENERATED ALWAYS AS (
    setweight(to_tsvector('simple', coalesce(symbol, '') || ' ' || regexp_replace(coalesce(symbol, ''), '([a-z0-9])([A-Z])', '\1 \2', 'g')
                                     || ' ' || regexp_replace(file_path, '[/._-]', ' ', 'g')), 'A')
    || to_tsvector('simple', content)
    || to_tsvector('simple', regexp_replace(content, '([a-z0-9])([A-Z])', '\1 \2', 'g'))
) STORED;
CREATE INDEX probe_chunks_tsv ON probe_chunks USING gin (tsv);
\timing off
SELECT pg_size_pretty(pg_relation_size('probe_chunks_tsv')) AS gin_index_size,
       max(length(tsv)) AS max_lexemes_in_one_chunk FROM probe_chunks;

\echo '=== 5. sample searches (OR of terms, ts_rank_cd), top 5 each'
\echo '--- itsdangerous: want_bytes'
SELECT file_path, start_line, end_line, symbol, round(ts_rank_cd(tsv, q)::numeric, 4) AS rank
FROM probe_chunks, to_tsquery('simple', 'want_bytes | want & bytes') q
WHERE repo_id = (SELECT id FROM repositories WHERE name = 'itsdangerous') AND tsv @@ q
ORDER BY rank DESC, id LIMIT 5;
\echo '--- click cl-4: test runner replace stdin stdout capture output'
SELECT file_path, start_line, end_line, symbol, round(ts_rank_cd(tsv, q)::numeric, 4) AS rank
FROM probe_chunks, to_tsquery('simple', 'test | runner | replace | stdin | stdout | capture | output') q
WHERE repo_id = (SELECT id FROM repositories WHERE name = 'click') AND tsv @@ q
ORDER BY rank DESC, id LIMIT 5;
\echo '--- is ts-12: literal string'
SELECT file_path, start_line, end_line, symbol, round(ts_rank_cd(tsv, q)::numeric, 4) AS rank
FROM probe_chunks, to_tsquery('simple', 'object & wrappers & primitive') q
WHERE repo_id = (SELECT id FROM repositories WHERE name = 'is') AND tsv @@ q
ORDER BY rank DESC, id LIMIT 5;
\echo '--- lexemes of one real chunk (itsdangerous want_bytes), first 40'
SELECT string_agg(lexeme, ' ') FROM (SELECT (unnest(tsv)).lexeme FROM probe_chunks
  WHERE repo_id = (SELECT id FROM repositories WHERE name = 'itsdangerous') AND symbol = 'want_bytes' LIMIT 40) x;
