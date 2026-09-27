"""Database writes for embeddings and chunks.

Vectors travel as text literals cast with ::halfvec, so no extra Python package is needed.
"""

import psycopg

from .chunk import Chunk

Vector = list[float]


def vector_literal(vec: Vector) -> str:
    return "[" + ",".join(format(x, ".8g") for x in vec) + "]"


def parse_vector(text: str) -> Vector:
    return [float(x) for x in text.strip().strip("[]").split(",") if x != ""]


# ---------------------------------------------------------------- embedding cache

def cache_get(conn: psycopg.Connection, model: str, hashes: list[str]) -> dict[str, Vector]:
    """Cached vectors for the given text hashes. Missing hashes are absent from the result."""
    if not hashes:
        return {}
    rows = conn.execute(
        "SELECT text_hash, embedding::text FROM embedding_cache WHERE embed_model = %s AND text_hash = ANY(%s)",
        (model, hashes),
    ).fetchall()
    return {h: parse_vector(v) for h, v in rows}


def cache_put(conn: psycopg.Connection, model: str, items: list[tuple[str, Vector]]) -> None:
    """Store vectors in their own statement (autocommit), independent of any job transaction."""
    if not items:
        return
    with conn.cursor() as cur:
        cur.executemany(
            "INSERT INTO embedding_cache (embed_model, text_hash, embedding) VALUES (%s, %s, %s::halfvec) "
            "ON CONFLICT DO NOTHING",
            [(model, h, vector_literal(v)) for h, v in items],
        )


# ---------------------------------------------------------------- chunks

def _clean(text: str | None) -> str | None:
    # PostgreSQL text cannot hold NUL. A NUL past the scanner's 8 KB binary sniff would abort the job.
    return text.replace("\x00", "�") if text is not None else None


def replace_chunks(
    conn: psycopg.Connection,
    job_id: int,
    repo_id: int,
    commit_sha: str,
    chunks: list[Chunk],
    vectors: list[Vector],
    embed_model: str,
) -> None:
    """Swap a repository's chunks for a new set and finish the job, in one transaction.

    A failure anywhere rolls everything back, so the previous index stays intact.
    Deleting by repository (not by commit) means queries never mix two commits.
    """
    if len(chunks) != len(vectors):
        raise ValueError("chunks and vectors must have the same length")
    rows = [
        (repo_id, commit_sha, _clean(c.file_path), c.language, _clean(c.symbol), c.kind,
         c.start_line, c.end_line, _clean(c.content), vector_literal(v), embed_model)
        for c, v in zip(chunks, vectors)
    ]
    with conn.transaction():
        conn.execute("DELETE FROM chunks WHERE repo_id = %s", (repo_id,))
        with conn.cursor() as cur:
            cur.executemany(
                "INSERT INTO chunks (repo_id, commit_sha, file_path, language, symbol, kind, "
                "start_line, end_line, content, embedding, embed_model) "
                "VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s, %s::halfvec, %s)",
                rows,
            )
        conn.execute("UPDATE repositories SET commit_sha = %s, status = 'ready' WHERE id = %s", (commit_sha, repo_id))
        conn.execute(
            "UPDATE index_jobs SET status = 'succeeded', phase = NULL, finished_at = now() WHERE id = %s",
            (job_id,),
        )
