"""Safe `git clone` of an untrusted repository.

The repository is attacker-controlled input. We only read files from it: no
hooks, no submodules, no build steps, nothing from it is ever executed.
"""

import logging
import os
import re
import shutil
import signal
import subprocess
import tempfile
import time
from pathlib import Path

from . import urlcheck
from .errors import UserError

log = logging.getLogger(__name__)

_SHA = re.compile(r"[0-9a-f]{40}|[0-9a-f]{64}")
_POLL_S = 1.0
# Environment variables passed through to git so a proxy or custom CA keeps working.
_PASSTHROUGH = (
    "HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "NO_PROXY", "no_proxy",
    "SSL_CERT_FILE", "SSL_CERT_DIR",
)


class CloneError(UserError):
    """Cloning failed. The message is safe to show to users."""


def clone(url: str, dest: Path, timeout_s: int, max_mb: float) -> str:
    """Shallow-clone a public GitHub repository into `dest`. Returns the HEAD commit SHA."""
    try:
        ref = urlcheck.parse_github_url(url)
    except urlcheck.InvalidURL:
        raise CloneError("invalid repository URL") from None
    return _git_clone(ref.url, dest, "https", timeout_s, max_mb)


def _build_env(protocol: str, home: str) -> dict[str, str]:
    env = {
        "PATH": os.environ.get("PATH", "/usr/local/bin:/usr/bin:/bin"),
        "HOME": home,
        "LC_ALL": "C",
        "GIT_TERMINAL_PROMPT": "0",
        "GIT_CONFIG_NOSYSTEM": "1",
        "GIT_CONFIG_GLOBAL": "/dev/null",
        "GIT_ALLOW_PROTOCOL": protocol,
    }
    for name in _PASSTHROUGH:
        if name in os.environ:
            env[name] = os.environ[name]
    return env


def _build_cmd(url: str, dest: Path, protocol: str) -> list[str]:
    return [
        "git",
        "-c", "core.hooksPath=/dev/null",
        "-c", "protocol.allow=never",
        "-c", f"protocol.{protocol}.allow=always",
        "-c", "submodule.recurse=false",
        "clone", "--depth", "1", "--single-branch", "--no-tags",
        "--", url, str(dest),
    ]


def _dir_bytes(path: Path, skip_git: bool = False) -> int:
    """Total size of regular files under `path`, without following symlinks."""
    total = 0
    for root, dirs, files in os.walk(path, followlinks=False):
        if skip_git and root == str(path) and ".git" in dirs:
            dirs.remove(".git")
        for name in files:
            try:
                total += os.lstat(os.path.join(root, name)).st_size
            except OSError:
                pass
    return total


def _run(cmd: list[str], env: dict[str, str], timeout_s: float, watch: Path | None = None,
         max_bytes: int | None = None) -> tuple[int, bytes]:
    """Run without a shell, in its own process group, killing the whole group on timeout
    or when `watch` grows past `max_bytes`. Returns (exit code, stderr)."""
    proc = subprocess.Popen(
        cmd, shell=False, env=env, stdin=subprocess.DEVNULL,
        stdout=subprocess.PIPE, stderr=subprocess.PIPE, start_new_session=True,
    )
    deadline = time.monotonic() + timeout_s
    try:
        while True:
            try:
                _, err = proc.communicate(timeout=_POLL_S)
                return proc.returncode, err
            except subprocess.TimeoutExpired:
                pass
            if time.monotonic() >= deadline:
                _kill(proc)
                raise CloneError("clone timed out")
            if watch is not None and max_bytes is not None and _dir_bytes(watch) > max_bytes:
                _kill(proc)
                raise CloneError("repository too large")
    except BaseException:
        if proc.poll() is None:
            _kill(proc)
        raise


def _kill(proc: subprocess.Popen) -> None:
    try:
        os.killpg(proc.pid, signal.SIGKILL)
    except ProcessLookupError:
        pass
    proc.communicate()


def _git_clone(url: str, dest: Path, protocol: str, timeout_s: float, max_mb: float) -> str:
    """Clone without validating `url`. Call `clone`, not this, with untrusted input.

    Split out so tests can clone a local repository with protocol="file".
    """
    max_bytes = int(max_mb * 1024 * 1024)
    home = tempfile.mkdtemp(prefix="git-home-")
    try:
        env = _build_env(protocol, home)
        # While cloning, .git (packed objects) counts too, so the live limit is looser than the final one.
        code, err = _run(_build_cmd(url, dest, protocol), env, timeout_s, watch=dest, max_bytes=max_bytes * 2)
        if code != 0:
            detail = err.decode("utf-8", "replace")[-500:]
            log.warning("git clone failed (exit %s): %s", code, detail)
            if "not found" in detail.lower():
                raise CloneError("repository not found or not public")
            raise CloneError("could not clone repository")

        if _dir_bytes(dest, skip_git=True) > max_bytes:
            raise CloneError("repository too large")

        proc = subprocess.run(
            ["git", "-C", str(dest), "rev-parse", "HEAD"],
            shell=False, env=env, stdin=subprocess.DEVNULL, capture_output=True, timeout=20,
        )
        sha = proc.stdout.decode("ascii", "replace").strip()
        if proc.returncode != 0 or not _SHA.fullmatch(sha):
            raise CloneError("could not read commit")
        return sha
    finally:
        shutil.rmtree(home, ignore_errors=True)
