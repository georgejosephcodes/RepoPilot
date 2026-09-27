"""Walk a cloned repository and decide which files are worth indexing.

Everything under the root is untrusted. We never follow symlinks, never open
anything that is not a regular file, and never produce a path outside the root.
"""

import os
import stat
from collections import Counter
from dataclasses import dataclass, field
from pathlib import Path

IGNORED_DIRS = frozenset({
    ".git", "node_modules", "vendor", "dist", "build", "target", ".venv", "venv",
    "__pycache__", ".next", ".idea", ".vscode",
})
LOCKFILES = frozenset({
    "package-lock.json", "yarn.lock", "pnpm-lock.yaml", "go.sum", "poetry.lock",
    "Cargo.lock", "Pipfile.lock",
})
GENERATED_SUFFIXES = (".min.js", ".pb.go", "_pb2.py")
GENERATED_MARKERS = (b"Code generated", b"DO NOT EDIT", b"@generated")
LANGUAGES = {
    ".py": "python",
    ".go": "go",
    ".ts": "typescript", ".tsx": "typescript",
    ".js": "javascript", ".jsx": "javascript", ".mjs": "javascript", ".cjs": "javascript",
    ".md": "markdown",
}
_HEAD_BYTES = 8192


@dataclass(frozen=True)
class ScannedFile:
    rel_path: str  # relative to the root, forward slashes
    abs_path: Path
    language: str
    size: int
    lines: int


@dataclass
class ScanResult:
    files: list[ScannedFile] = field(default_factory=list)
    skipped: Counter = field(default_factory=Counter)

    def by_language(self) -> dict[str, int]:
        return dict(sorted(Counter(f.language for f in self.files).items()))


def _count_lines(data: bytes) -> int:
    if not data:
        return 0
    return data.count(b"\n") + (0 if data.endswith(b"\n") else 1)


def scan(root: Path, max_files: int, max_file_kb: int) -> ScanResult:
    root = Path(root)
    max_bytes = max_file_kb * 1024
    result = ScanResult()

    for dirpath, dirnames, filenames in os.walk(root, followlinks=False):
        # Prune in place so os.walk never descends into skipped directories.
        kept = []
        for d in sorted(dirnames):
            full = os.path.join(dirpath, d)
            if os.path.islink(full):
                result.skipped["symlink"] += 1
            elif d in IGNORED_DIRS:
                result.skipped["ignored_dir"] += 1
            else:
                kept.append(d)
        dirnames[:] = kept

        for name in sorted(filenames):
            full = os.path.join(dirpath, name)
            reason = _classify(full, name, max_bytes)
            if isinstance(reason, str):
                result.skipped[reason] += 1
                continue
            language, size, _ = reason

            if len(result.files) >= max_files:
                result.skipped["over_file_cap"] += 1
                continue

            rel = os.path.relpath(full, root).replace(os.sep, "/")
            if rel.startswith("../") or rel == "..":
                raise RuntimeError("scan produced a path outside the root")

            with open(full, "rb") as fh:
                data = fh.read(max_bytes + 1)
            result.files.append(
                ScannedFile(rel_path=rel, abs_path=Path(full), language=language,
                            size=size, lines=_count_lines(data))
            )
    result.files.sort(key=lambda f: f.rel_path)  # stable order for chunking and tests
    return result


def _classify(full: str, name: str, max_bytes: int):
    """Return a skip-reason string, or (language, size, head) for a file to keep."""
    try:
        st = os.lstat(full)
    except OSError:
        return "unreadable"
    if stat.S_ISLNK(st.st_mode):
        return "symlink"
    if not stat.S_ISREG(st.st_mode):
        return "unsupported"  # FIFO, socket, device: never open these

    if name in LOCKFILES:
        return "lockfile"
    if name.endswith(GENERATED_SUFFIXES):
        return "generated"
    language = LANGUAGES.get(os.path.splitext(name)[1].lower())
    if language is None:
        return "unsupported"
    if st.st_size > max_bytes:
        return "too_large"

    try:
        with open(full, "rb") as fh:
            head = fh.read(_HEAD_BYTES)
    except OSError:
        return "unreadable"
    if b"\0" in head:
        return "binary"
    first_lines = b"\n".join(head.split(b"\n")[:5])
    if any(marker in first_lines for marker in GENERATED_MARKERS):
        return "generated"
    return language, st.st_size, head
