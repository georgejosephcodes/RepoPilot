"""The API validates filter languages against testdata/languages.json; it must list exactly what the scanner writes."""

import json
from pathlib import Path

from repopilot_worker.scan import LANGUAGES

FIXTURE = json.loads((Path(__file__).resolve().parents[2] / "testdata" / "languages.json").read_text(encoding="utf-8"))


def test_fixture_lists_every_scanned_language():
    assert FIXTURE["languages"] == sorted(set(LANGUAGES.values()))
