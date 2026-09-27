CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE repositories (
    id          BIGSERIAL PRIMARY KEY,
    url         TEXT        NOT NULL,
    owner       TEXT        NOT NULL,
    name        TEXT        NOT NULL,
    commit_sha  TEXT,
    status      TEXT        NOT NULL DEFAULT 'queued'
                CHECK (status IN ('queued', 'indexing', 'ready', 'failed')),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- GitHub owner/name are case-insensitive
CREATE UNIQUE INDEX repositories_owner_name_ci ON repositories (lower(owner), lower(name));

CREATE TABLE index_jobs (
    id           BIGSERIAL PRIMARY KEY,
    repo_id      BIGINT      NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    status       TEXT        NOT NULL DEFAULT 'queued'
                 CHECK (status IN ('queued', 'running', 'succeeded', 'failed')),
    phase        TEXT,
    files_total  INT,
    files_done   INT,
    error        TEXT,
    attempts     INT         NOT NULL DEFAULT 0,
    locked_at    TIMESTAMPTZ,
    started_at   TIMESTAMPTZ,
    finished_at  TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX index_jobs_claim ON index_jobs (status, created_at);

CREATE TABLE chunks (
    id          BIGSERIAL PRIMARY KEY,
    repo_id     BIGINT      NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    commit_sha  TEXT        NOT NULL,
    file_path   TEXT        NOT NULL,
    language    TEXT        NOT NULL,
    symbol      TEXT,
    kind        TEXT        NOT NULL,
    start_line  INT         NOT NULL,
    end_line    INT         NOT NULL,
    content     TEXT        NOT NULL,
    embedding   halfvec(2048) NOT NULL,   -- native size of the Phase 1 embedding model; halfvec because HNSW on vector stops at 2000 dimensions
    embed_model TEXT        NOT NULL,
    CHECK (start_line >= 1 AND end_line >= start_line)
);
CREATE INDEX chunks_repo ON chunks (repo_id);
CREATE INDEX chunks_embedding_hnsw ON chunks USING hnsw (embedding halfvec_cosine_ops);

-- Every vector is stored here as soon as its batch returns, so failed jobs resume,
-- re-indexing an unchanged repository costs no API requests, and identical code is embedded once.
CREATE TABLE embedding_cache (
    embed_model TEXT          NOT NULL,
    text_hash   TEXT          NOT NULL,       -- sha256 of the exact text that was embedded
    embedding   halfvec(2048) NOT NULL,
    created_at  TIMESTAMPTZ   NOT NULL DEFAULT now(),
    PRIMARY KEY (embed_model, text_hash)
);
