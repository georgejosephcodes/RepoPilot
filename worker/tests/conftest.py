import os
import secrets
import subprocess
from pathlib import Path

import psycopg
import pytest
from psycopg.conninfo import make_conninfo

REPO_ROOT = Path(__file__).resolve().parents[2]


@pytest.fixture(scope="session")
def test_dsn():
    """A throwaway database with the real schema, created and dropped per test session.

    Needs TEST_DATABASE_URL pointing at any database on a server where the user may
    CREATE DATABASE. Isolation means tests never touch, or claim jobs from, dev data.
    """
    base = os.environ.get("TEST_DATABASE_URL")
    if not base:
        pytest.skip("TEST_DATABASE_URL not set")
    name = f"rp_test_{secrets.token_hex(4)}"
    with psycopg.connect(base, autocommit=True) as admin:
        admin.execute(f'CREATE DATABASE "{name}"')
    dsn = make_conninfo(base, dbname=name)
    try:
        with psycopg.connect(dsn, autocommit=True) as conn:
            conn.execute((REPO_ROOT / "migrations" / "001_init.sql").read_text())
        yield dsn
    finally:
        with psycopg.connect(base, autocommit=True) as admin:
            admin.execute(f'DROP DATABASE IF EXISTS "{name}" WITH (FORCE)')


@pytest.fixture
def conn(test_dsn):
    with psycopg.connect(test_dsn, autocommit=True) as c:
        c.execute("TRUNCATE repositories, index_jobs, chunks, embedding_cache RESTART IDENTITY CASCADE")
        yield c


@pytest.fixture
def make_job(conn):
    """Insert a repository plus a queued job. Returns (repo_id, job_id)."""
    counter = iter(range(1, 10_000))

    def _make(owner="octo", name=None):
        n = next(counter)
        name = name or f"repo{n}"
        repo_id = conn.execute(
            "INSERT INTO repositories (url, owner, name) VALUES (%s, %s, %s) RETURNING id",
            (f"https://github.com/{owner}/{name}", owner, name),
        ).fetchone()[0]
        job_id = conn.execute("INSERT INTO index_jobs (repo_id) VALUES (%s) RETURNING id", (repo_id,)).fetchone()[0]
        return repo_id, job_id

    return _make


GIT_ENV = {
    "GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@example.com",
    "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@example.com",
    "GIT_CONFIG_GLOBAL": "/dev/null", "GIT_CONFIG_NOSYSTEM": "1",
}


def git(cwd, *args):
    env = {**os.environ, **GIT_ENV}
    return subprocess.run(["git", *args], cwd=cwd, env=env, check=True, capture_output=True, text=True).stdout.strip()


@pytest.fixture
def local_repo(tmp_path):
    """A small local git repository with one commit."""
    src = tmp_path / "src"
    src.mkdir()
    git(src, "init", "-q")
    (src / "main.py").write_text("def hello():\n    return 1\n")
    (src / "README.md").write_text("# demo\n")
    git(src, "add", ".")
    git(src, "commit", "-q", "-m", "init")
    return src
