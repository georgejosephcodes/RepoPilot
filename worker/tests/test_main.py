from repopilot_worker import main as main_mod
from repopilot_worker.config import Config


def test_startup_requires_a_key_when_embedding(conn):
    assert main_mod._startup_checks(conn, Config(embed_api_key=""), needs_embedding=True) == "EMBED_API_KEY is not set"


def test_startup_detects_dimension_mismatch(conn):
    message = main_mod._startup_checks(conn, Config(embed_api_key="k", embed_dim=768), needs_embedding=True)
    assert "EMBED_DIM is 768" in message and "column is 2048" in message


def test_startup_ok_when_configuration_matches_schema(conn):
    assert main_mod._startup_checks(conn, Config(embed_api_key="k", embed_dim=2048), needs_embedding=True) is None


def test_startup_skips_embedding_checks_for_early_stages(conn):
    assert main_mod._startup_checks(conn, Config(embed_api_key="", embed_dim=1), needs_embedding=False) is None
