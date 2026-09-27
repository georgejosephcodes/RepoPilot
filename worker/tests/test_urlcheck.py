import json
from pathlib import Path

import pytest

from repopilot_worker.urlcheck import InvalidURL, parse_github_url

FIXTURE = Path(__file__).resolve().parents[2] / "testdata" / "urls.json"
CASES = json.loads(FIXTURE.read_text(encoding="utf-8"))


def test_fixture_is_present():
    assert len(CASES) >= 60


@pytest.mark.parametrize("case", CASES, ids=[c["name"] for c in CASES])
def test_shared_fixture(case):
    if not case["valid"]:
        with pytest.raises(InvalidURL):
            parse_github_url(case["input"])
        return
    ref = parse_github_url(case["input"])
    assert (ref.owner, ref.name, ref.url) == (case["owner"], case["repo"], case["canonical"])


@pytest.mark.parametrize(
    "url,valid",
    [
        ("https://github.com/" + "a" * 39 + "/b", True),
        ("https://github.com/" + "a" * 40 + "/b", False),
        ("https://github.com/a/" + "b" * 100, True),
        ("https://github.com/a/" + "b" * 101, False),
        ("a" * 10000, False),
        ("https://" + "a" * 150 + ".com/a/b", False),
    ],
)
def test_length_boundaries(url, valid):
    if valid:
        parse_github_url(url)
    else:
        with pytest.raises(InvalidURL):
            parse_github_url(url)


def test_canonical_ignores_input_form():
    forms = [
        "https://github.com/Foo/Bar",
        "https://github.com/Foo/Bar.git",
        "https://github.com/Foo/Bar/",
        "HTTPS://GITHUB.COM/Foo/Bar",
    ]
    assert {parse_github_url(f).url for f in forms} == {"https://github.com/Foo/Bar"}
