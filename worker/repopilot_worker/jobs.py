"""Job queue on top of the index_jobs table.

Every function takes a connection opened with autocommit=True (see db.connect)
and wraps multi-statement work in `conn.transaction()`.
"""

from dataclasses import dataclass

import psycopg

MAX_ERROR_CHARS = 500

# A repository's status says whether a usable index exists; the job holds the outcome of the latest attempt.
# While an attempt runs, or when it ends without replacing the index, a repository that already has chunks stays
# 'ready'; progress of a re-index is visible on its job.
_STATUS_WITHOUT_NEW_INDEX = (
    "UPDATE repositories SET status = CASE WHEN EXISTS "
    "(SELECT 1 FROM chunks c WHERE c.repo_id = repositories.id) THEN 'ready' ELSE %s::text END WHERE id = %s"
)


@dataclass(frozen=True)
class Job:
    id: int
    repo_id: int
    url: str
    owner: str
    name: str
    attempts: int


# FOR UPDATE SKIP LOCKED: concurrent workers each lock a different queued row
# instead of waiting on, or double-taking, the same one.
_CLAIM = """
WITH picked AS (
    SELECT id FROM index_jobs
    WHERE status = 'queued'
    ORDER BY created_at, id
    FOR UPDATE SKIP LOCKED
    LIMIT 1
), upd AS (
    UPDATE index_jobs j
    SET status = 'running', started_at = now(), locked_at = now(),
        attempts = j.attempts + 1,
        phase = NULL, files_done = NULL, files_total = NULL, error = NULL, finished_at = NULL
    FROM picked
    WHERE j.id = picked.id
    RETURNING j.id, j.repo_id, j.attempts
)
SELECT upd.id, upd.repo_id, r.url, r.owner, r.name, upd.attempts
FROM upd JOIN repositories r ON r.id = upd.repo_id
"""


def claim_next(conn: psycopg.Connection) -> Job | None:
    """Atomically take the oldest queued job. Returns None when the queue is empty."""
    with conn.transaction():
        row = conn.execute(_CLAIM).fetchone()
        if row is None:
            return None
        conn.execute(_STATUS_WITHOUT_NEW_INDEX, ("indexing", row[1]))
    return Job(id=row[0], repo_id=row[1], url=row[2], owner=row[3], name=row[4], attempts=row[5])


def set_progress(
    conn: psycopg.Connection,
    job_id: int,
    phase: str,
    files_done: int | None = None,
    files_total: int | None = None,
) -> None:
    """Record the current phase. Also refreshes locked_at, so it doubles as a heartbeat."""
    conn.execute(
        "UPDATE index_jobs SET phase = %s, locked_at = now(), "
        "files_done = COALESCE(%s, files_done), files_total = COALESCE(%s, files_total) "
        "WHERE id = %s",
        (phase, files_done, files_total, job_id),
    )


def heartbeat(conn: psycopg.Connection, job_id: int) -> None:
    conn.execute("UPDATE index_jobs SET locked_at = now() WHERE id = %s", (job_id,))


def fail(conn: psycopg.Connection, job_id: int, message: str) -> None:
    """Mark the job and its repository failed. `message` must already be safe to show users."""
    with conn.transaction():
        row = conn.execute(
            "UPDATE index_jobs SET status = 'failed', error = %s, finished_at = now() "
            "WHERE id = %s RETURNING repo_id",
            (message[:MAX_ERROR_CHARS], job_id),
        ).fetchone()
        if row:
            conn.execute(_STATUS_WITHOUT_NEW_INDEX, ("failed", row[0]))


def release(conn: psycopg.Connection, job_id: int) -> None:
    """Put a running job back in the queue without counting the attempt.

    Used when a run stops early on purpose (--stop-after) so the job stays runnable.
    """
    with conn.transaction():
        row = conn.execute(
            "UPDATE index_jobs SET status = 'queued', attempts = GREATEST(attempts - 1, 0), "
            "phase = NULL, files_done = NULL, files_total = NULL, "
            "started_at = NULL, locked_at = NULL "
            "WHERE id = %s RETURNING repo_id",
            (job_id,),
        ).fetchone()
        if row:
            conn.execute(_STATUS_WITHOUT_NEW_INDEX, ("queued", row[0]))


def requeue_stale(conn: psycopg.Connection, older_than_seconds: int, max_attempts: int) -> tuple[int, int]:
    """Recover jobs whose worker died. Returns (requeued, failed)."""
    with conn.transaction():
        rows = conn.execute(
            """
            UPDATE index_jobs SET
                status = CASE WHEN attempts >= %(max)s THEN 'failed' ELSE 'queued' END,
                error = CASE WHEN attempts >= %(max)s THEN 'worker stopped responding' ELSE error END,
                finished_at = CASE WHEN attempts >= %(max)s THEN now() ELSE finished_at END,
                locked_at = NULL
            WHERE status = 'running'
              AND locked_at < now() - make_interval(secs => %(secs)s::double precision)
            RETURNING repo_id, status
            """,
            {"max": max_attempts, "secs": older_than_seconds},
        ).fetchall()
        for repo_id, status in rows:
            if status == "failed":
                conn.execute(_STATUS_WITHOUT_NEW_INDEX, ("failed", repo_id))
            else:
                conn.execute("UPDATE repositories SET status = %s WHERE id = %s", (status, repo_id))
    requeued = sum(1 for _, s in rows if s == "queued")
    return requeued, len(rows) - requeued
