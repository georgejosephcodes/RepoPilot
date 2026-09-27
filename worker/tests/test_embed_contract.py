"""The Go API (api/internal/embed/contract_test.go) loads the same fixture, so a change in either
side's request format, defaults, character cut, or normalisation fails a test."""

import json
from pathlib import Path

import pytest

from repopilot_worker import config, embed
from repopilot_worker.embed import OpenAICompatEmbedder

CONTRACT = json.loads((Path(__file__).resolve().parents[2] / "testdata" / "embed_contract.json").read_text(encoding="utf-8"))


def test_defaults_match_the_contract():
    d, cfg = CONTRACT["defaults"], config.Config()
    assert cfg.embed_base_url == d["base_url"]
    assert cfg.embed_model == d["model"]
    assert cfg.embed_dim == d["dimension"]
    assert cfg.embed_send_dimensions == d["send_dimensions"]
    assert cfg.embed_input_types == d["input_types"]
    assert cfg.embed_query_type == d["query_type"]
    assert cfg.embed_document_type == d["document_type"]
    assert cfg.embed_max_chars == d["max_chars"]


@pytest.mark.parametrize("case", CONTRACT["request_cases"], ids=[c["name"] for c in CONTRACT["request_cases"]])
def test_request_bodies_match_the_contract(case):
    dim = case["config"]["dimension"]
    sent = []

    def post(url, body, headers):
        sent.append(body)
        items = [{"index": i, "embedding": [1.0] + [0.0] * (dim - 1)} for i in range(len(body["input"]))]
        return 200, {"data": items}, {}

    e = OpenAICompatEmbedder("https://x/v1", "key", case["config"]["model"], dim,
                             send_dimensions=case["config"]["send_dimensions"],
                             input_types=case["config"]["input_types"], post=post,
                             **{k: case["config"][k] for k in ("query_type", "document_type") if k in case["config"]})
    if case["kind"] == "query":
        e.embed_query(case["texts"][0])
    else:
        e.embed_documents(case["texts"])
    assert sent == [case["body"]]


@pytest.mark.parametrize("case", CONTRACT["truncation"])
def test_truncation_matches_the_contract(case):
    e = OpenAICompatEmbedder("https://x/v1", "key", "m", 4, max_chars=case["max_chars"])
    assert e.prepare(case["input"]) == case["expected"]


@pytest.mark.parametrize("case", CONTRACT["normalization"])
def test_normalization_matches_the_contract(case):
    got = embed.normalize([float(x) for x in case["raw"]])
    assert got == pytest.approx(case["expected"], abs=1e-6)
