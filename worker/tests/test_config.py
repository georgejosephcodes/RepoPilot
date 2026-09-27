import pytest

from repopilot_worker import config

ENV = ["CLONE_TIMEOUT_SEC", "MAX_REPO_MB", "MAX_FILES", "MAX_FILE_KB", "WORK_DIR",
       "CHUNK_MAX_LINES", "CHUNK_OVERLAP_LINES", "CHUNK_MIN_GAP_LINES",
       "EMBED_BASE_URL", "EMBED_API_KEY", "EMBED_MODEL", "EMBED_DIM", "EMBED_SEND_DIMENSIONS", "EMBED_INPUT_TYPES",
       "EMBED_BATCH_SIZE", "EMBED_BATCH_TOKENS", "EMBED_MAX_CHARS", "EMBED_RPM", "EMBED_RESERVE_REQUESTS"]


@pytest.fixture(autouse=True)
def clean_env(monkeypatch):
    for name in ENV:
        monkeypatch.delenv(name, raising=False)


def test_defaults():
    cfg = config.load()
    assert (cfg.chunk_max_lines, cfg.chunk_overlap_lines, cfg.chunk_min_gap_lines) == (120, 10, 3)
    assert cfg.max_files == 5000 and cfg.max_file_kb == 200


def test_overrides(monkeypatch, tmp_path):
    monkeypatch.setenv("CHUNK_MAX_LINES", "50")
    monkeypatch.setenv("CHUNK_OVERLAP_LINES", "0")
    monkeypatch.setenv("WORK_DIR", str(tmp_path))
    cfg = config.load()
    assert (cfg.chunk_max_lines, cfg.chunk_overlap_lines, cfg.work_dir) == (50, 0, tmp_path)


@pytest.mark.parametrize("name,value,message", [
    ("CHUNK_MAX_LINES", "abc", "must be an integer"),
    ("CHUNK_MAX_LINES", "0", "at least 1"),
    ("CHUNK_OVERLAP_LINES", "-1", "at least 0"),
    ("MAX_FILES", "", None),
])
def test_invalid_values(monkeypatch, name, value, message):
    monkeypatch.setenv(name, value)
    if message is None:
        config.load()  # empty string means "use the default"
        return
    with pytest.raises(RuntimeError, match=message):
        config.load()


def test_overlap_must_be_smaller_than_max(monkeypatch):
    monkeypatch.setenv("CHUNK_MAX_LINES", "10")
    monkeypatch.setenv("CHUNK_OVERLAP_LINES", "10")
    with pytest.raises(RuntimeError, match="smaller than"):
        config.load()


def test_embedding_defaults():
    cfg = config.load()
    assert cfg.embed_base_url == "https://openrouter.ai/api/v1"
    assert cfg.embed_model == "nvidia/nemotron-3-embed-1b:free" and cfg.embed_dim == 2048
    assert cfg.embed_api_key == "" and cfg.embed_send_dimensions is False and cfg.embed_input_types is True
    assert (cfg.embed_batch_size, cfg.embed_batch_tokens, cfg.embed_max_chars) == (64, 40000, 8000)
    assert (cfg.embed_rpm, cfg.embed_reserve_requests) == (20, 10)


def test_embedding_overrides(monkeypatch):
    monkeypatch.setenv("EMBED_BASE_URL", "http://localhost:11434/v1")
    monkeypatch.setenv("EMBED_API_KEY", "abc")
    monkeypatch.setenv("EMBED_MODEL", "local-model")
    monkeypatch.setenv("EMBED_DIM", "768")
    monkeypatch.setenv("EMBED_SEND_DIMENSIONS", "true")
    monkeypatch.setenv("EMBED_INPUT_TYPES", "0")
    monkeypatch.setenv("EMBED_RESERVE_REQUESTS", "0")
    cfg = config.load()
    assert (cfg.embed_base_url, cfg.embed_api_key, cfg.embed_model, cfg.embed_dim) == (
        "http://localhost:11434/v1", "abc", "local-model", 768)
    assert cfg.embed_send_dimensions is True and cfg.embed_input_types is False and cfg.embed_reserve_requests == 0


@pytest.mark.parametrize("name,value,message", [
    ("EMBED_SEND_DIMENSIONS", "maybe", "true or false"),
    ("EMBED_DIM", "big", "must be an integer"),
    ("EMBED_BATCH_SIZE", "0", "at least 1"),
])
def test_embedding_invalid_values(monkeypatch, name, value, message):
    monkeypatch.setenv(name, value)
    with pytest.raises(RuntimeError, match=message):
        config.load()
