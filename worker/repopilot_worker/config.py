import os
import tempfile
from dataclasses import dataclass, field
from pathlib import Path


@dataclass(frozen=True)
class Config:
    clone_timeout_s: int = 120
    max_repo_mb: int = 200
    max_files: int = 5000
    max_file_kb: int = 200
    work_dir: Path = Path(tempfile.gettempdir()) / "repopilot"
    stale_after_s: int = 600
    max_attempts: int = 3
    chunk_max_lines: int = 120
    chunk_overlap_lines: int = 10
    chunk_min_gap_lines: int = 3
    # Embedding provider: any OpenAI-compatible embeddings API (OpenRouter by default).
    embed_base_url: str = "https://openrouter.ai/api/v1"
    embed_api_key: str = field(default="", repr=False)   # never printed
    embed_model: str = "nvidia/nemotron-3-embed-1b:free"
    embed_dim: int = 2048
    embed_send_dimensions: bool = False   # this model rejects any `dimensions` other than 2048
    embed_input_types: bool = True        # send input_type search_document / search_query
    embed_batch_size: int = 64
    embed_batch_tokens: int = 40000
    embed_max_chars: int = 8000
    embed_rpm: int = 20
    embed_reserve_requests: int = 10      # daily requests kept free for questions


def _int(name: str, default: int, minimum: int = 1) -> int:
    raw = os.environ.get(name)
    if raw is None or raw == "":
        return default
    try:
        value = int(raw)
    except ValueError:
        raise RuntimeError(f"{name} must be an integer") from None
    if value < minimum:
        raise RuntimeError(f"{name} must be at least {minimum}")
    return value


def _bool(name: str, default: bool) -> bool:
    raw = os.environ.get(name)
    if raw is None or raw == "":
        return default
    if raw.lower() in ("1", "true", "yes", "on"):
        return True
    if raw.lower() in ("0", "false", "no", "off"):
        return False
    raise RuntimeError(f"{name} must be true or false")


def _str(name: str, default: str) -> str:
    return os.environ.get(name) or default


def load() -> Config:
    d = Config()
    work_dir = os.environ.get("WORK_DIR")
    cfg = Config(
        clone_timeout_s=_int("CLONE_TIMEOUT_SEC", d.clone_timeout_s),
        max_repo_mb=_int("MAX_REPO_MB", d.max_repo_mb),
        max_files=_int("MAX_FILES", d.max_files),
        max_file_kb=_int("MAX_FILE_KB", d.max_file_kb),
        work_dir=Path(work_dir) if work_dir else d.work_dir,
        chunk_max_lines=_int("CHUNK_MAX_LINES", d.chunk_max_lines),
        chunk_overlap_lines=_int("CHUNK_OVERLAP_LINES", d.chunk_overlap_lines, minimum=0),
        chunk_min_gap_lines=_int("CHUNK_MIN_GAP_LINES", d.chunk_min_gap_lines),
        embed_base_url=_str("EMBED_BASE_URL", d.embed_base_url),
        embed_api_key=os.environ.get("EMBED_API_KEY", ""),
        embed_model=_str("EMBED_MODEL", d.embed_model),
        embed_dim=_int("EMBED_DIM", d.embed_dim),
        embed_send_dimensions=_bool("EMBED_SEND_DIMENSIONS", d.embed_send_dimensions),
        embed_input_types=_bool("EMBED_INPUT_TYPES", d.embed_input_types),
        embed_batch_size=_int("EMBED_BATCH_SIZE", d.embed_batch_size),
        embed_batch_tokens=_int("EMBED_BATCH_TOKENS", d.embed_batch_tokens),
        embed_max_chars=_int("EMBED_MAX_CHARS", d.embed_max_chars),
        embed_rpm=_int("EMBED_RPM", d.embed_rpm),
        embed_reserve_requests=_int("EMBED_RESERVE_REQUESTS", d.embed_reserve_requests, minimum=0),
    )
    if cfg.chunk_overlap_lines >= cfg.chunk_max_lines:
        raise RuntimeError("CHUNK_OVERLAP_LINES must be smaller than CHUNK_MAX_LINES")
    return cfg
