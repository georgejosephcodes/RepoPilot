import os

import psycopg


def connect(autocommit: bool = True) -> psycopg.Connection:
    """Connect using DATABASE_URL.

    Autocommit is on so each statement outside `conn.transaction()` commits
    immediately. Multi-statement work uses `with conn.transaction():`.
    """
    dsn = os.environ.get("DATABASE_URL")
    if not dsn:
        raise RuntimeError("DATABASE_URL is required")
    return psycopg.connect(dsn, autocommit=autocommit)


def check(conn: psycopg.Connection) -> dict:
    """Return DB facts the worker depends on: server version, pgvector version, tables."""
    with conn.cursor() as cur:
        cur.execute("SELECT version()")
        server = cur.fetchone()[0]
        cur.execute("SELECT extversion FROM pg_extension WHERE extname = 'vector'")
        row = cur.fetchone()
        cur.execute(
            "SELECT table_name FROM information_schema.tables "
            "WHERE table_schema = 'public' ORDER BY table_name"
        )
        tables = [r[0] for r in cur.fetchall()]
    return {"server": server, "pgvector": row[0] if row else None, "tables": tables}


def embedding_dimension(conn: psycopg.Connection) -> int | None:
    """Dimension of chunks.embedding as declared in the schema (halfvec(N) stores N as its type modifier)."""
    row = conn.execute(
        "SELECT atttypmod FROM pg_attribute WHERE attrelid = 'chunks'::regclass AND attname = 'embedding'"
    ).fetchone()
    return row[0] if row and row[0] > 0 else None
