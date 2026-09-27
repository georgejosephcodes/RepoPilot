import os
import subprocess
import time

import pytest

from repopilot_worker import clone as clone_mod
from repopilot_worker.clone import CloneError, _build_env, _git_clone, _run, clone

from conftest import git


def test_clone_local_repo_returns_head_sha(local_repo, tmp_path):
    dest = tmp_path / "dest"
    sha = _git_clone(f"file://{local_repo}", dest, "file", timeout_s=30, max_mb=50)
    assert sha == git(local_repo, "rev-parse", "HEAD")
    assert (dest / "main.py").read_text() == "def hello():\n    return 1\n"
    assert (dest / ".git").is_dir()


def test_clone_is_shallow(local_repo, tmp_path):
    (local_repo / "more.py").write_text("y = 2\n")
    git(local_repo, "add", ".")
    git(local_repo, "commit", "-q", "-m", "second")
    dest = tmp_path / "dest"
    _git_clone(f"file://{local_repo}", dest, "file", timeout_s=30, max_mb=50)
    assert git(dest, "rev-list", "--count", "HEAD") == "1"


def test_clone_missing_repo_raises_safe_error(tmp_path):
    with pytest.raises(CloneError) as exc:
        _git_clone(f"file://{tmp_path}/nope", tmp_path / "dest", "file", timeout_s=30, max_mb=50)
    assert str(tmp_path) not in str(exc.value)


def test_clone_size_cap(local_repo, tmp_path):
    (local_repo / "big.py").write_bytes(os.urandom(2 * 1024 * 1024))
    git(local_repo, "add", ".")
    git(local_repo, "commit", "-q", "-m", "big")
    with pytest.raises(CloneError, match="too large"):
        _git_clone(f"file://{local_repo}", tmp_path / "dest", "file", timeout_s=30, max_mb=1)


@pytest.mark.parametrize(
    "url",
    [
        "--upload-pack=touch /tmp/pwned",
        "-x/y",
        "https://github.com@evil.com/a/b",
        "https://evil.com/a/b",
        "http://github.com/a/b",
        "file:///etc/passwd",
        "https://github.com/a/b;rm -rf /",
        "https://github.com/a/$(id)",
        "",
    ],
)
def test_hostile_url_rejected_before_git_runs(url, tmp_path, monkeypatch):
    def boom(*a, **k):
        raise AssertionError("git must not be started for an invalid URL")

    monkeypatch.setattr(subprocess, "Popen", boom)
    monkeypatch.setattr(subprocess, "run", boom)
    with pytest.raises(CloneError, match="invalid repository URL"):
        clone(url, tmp_path / "dest", timeout_s=5, max_mb=10)


def test_public_clone_uses_https_only(monkeypatch, tmp_path):
    seen = {}

    def fake_git_clone(url, dest, protocol, timeout_s, max_mb):
        seen.update(url=url, protocol=protocol)
        return "a" * 40

    monkeypatch.setattr(clone_mod, "_git_clone", fake_git_clone)
    clone("HTTPS://GitHub.com/Foo/Bar.git", tmp_path / "d", 5, 10)
    assert seen == {"url": "https://github.com/Foo/Bar", "protocol": "https"}


def test_command_is_argv_with_double_dash(tmp_path):
    cmd = clone_mod._build_cmd("https://github.com/a/b", tmp_path / "d", "https")
    assert cmd[0] == "git"
    dash = cmd.index("--")
    assert cmd[dash + 1:] == ["https://github.com/a/b", str(tmp_path / "d")]
    assert "core.hooksPath=/dev/null" in cmd
    assert "protocol.allow=never" in cmd and "protocol.https.allow=always" in cmd
    assert "--depth" in cmd and "--no-tags" in cmd


def test_env_is_clean(monkeypatch):
    monkeypatch.setenv("GEMINI_API_KEY", "secret")
    monkeypatch.setenv("DATABASE_URL", "postgres://u:p@h/d")
    monkeypatch.setenv("GIT_DIR", "/evil")
    monkeypatch.setenv("GIT_SSH_COMMAND", "evil")
    monkeypatch.setenv("HTTPS_PROXY", "http://proxy:3128")
    env = _build_env("https", "/tmp/home")
    for leaked in ("GEMINI_API_KEY", "DATABASE_URL", "GIT_DIR", "GIT_SSH_COMMAND"):
        assert leaked not in env
    assert env["GIT_TERMINAL_PROMPT"] == "0"
    assert env["GIT_ALLOW_PROTOCOL"] == "https"
    assert env["GIT_CONFIG_GLOBAL"] == "/dev/null" and env["GIT_CONFIG_NOSYSTEM"] == "1"
    assert env["HOME"] == "/tmp/home"
    assert env["HTTPS_PROXY"] == "http://proxy:3128"  # proxy settings pass through


def test_run_kills_process_group_on_timeout(tmp_path):
    marker = tmp_path / "child.pid"
    # the shell starts a background child, records its pid, then waits
    cmd = ["sh", "-c", f"sleep 60 & echo $! > {marker}; wait"]
    start = time.monotonic()
    with pytest.raises(CloneError, match="timed out"):
        _run(cmd, {"PATH": os.environ["PATH"]}, timeout_s=2)
    assert time.monotonic() - start < 10
    pid = int(marker.read_text())
    time.sleep(0.2)
    with pytest.raises(ProcessLookupError):
        os.kill(pid, 0)  # grandchild is gone too


def test_run_returns_exit_code_and_stderr():
    code, err = _run(["sh", "-c", "echo oops >&2; exit 3"], {"PATH": os.environ["PATH"]}, timeout_s=5)
    assert code == 3 and b"oops" in err
