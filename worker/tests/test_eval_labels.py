"""Tests for scripts/eval_labels.py. No network and no database: schema checks run on dicts, span checks on the tiny
real clone built by conftest.py's `local_repo` fixture (main.py is `def hello():` / `    return 1`)."""

import copy
import json

import pytest

from scripts import eval_labels as ev
from scripts.run_demo import git_head


def span(file="main.py", start=1, end=2, grade=2, **extra):
    return {"file": file, "start_line": start, "end_line": end, "grade": grade, **extra}


def question(qid="q1", kind="location", relevant=None, **overrides):
    q = {
        "id": qid,
        "repo": "demo/src",
        "kind": kind,
        "split": "dev",
        "question": "where is hello",
        "relevant": [span(symbol="hello")] if relevant is None else relevant,
        "expected_answer": "main.py",
        "why_hard": "",
        "origin": "phase2",
    }
    q.update(overrides)
    return q


def make_data(questions=None, commit="1" * 40):
    return {
        "version": 1,
        "labels_frozen": False,
        "repositories": [{"name": "demo/src", "commit": commit}],
        "questions": [question()] if questions is None else questions,
    }


def one_problem(data, text):
    problems = ev.schema_problems(data)
    assert any(text in p for p in problems), problems


# ---------------------------------------------------------------- schema

def test_a_valid_file_has_no_schema_problems():
    data = make_data([question(), question("q2", "unanswerable", relevant=[])])
    assert ev.schema_problems(data) == []


def test_short_or_uppercase_commit_is_rejected():
    one_problem(make_data(commit="abc1234"), "full 40-character")
    one_problem(make_data(commit="A" * 40), "full 40-character")


def test_duplicate_repository_is_rejected():
    data = make_data()
    data["repositories"].append(copy.deepcopy(data["repositories"][0]))
    one_problem(data, "listed twice")


def test_duplicate_question_id_is_rejected():
    one_problem(make_data([question(), question()]), "duplicate id")


@pytest.mark.parametrize("field", ev.QUESTION_FIELDS)
def test_every_field_is_required(field):
    q = question()
    del q[field]
    one_problem(make_data([q]), f"missing {field}")


@pytest.mark.parametrize(
    "overrides, text",
    [
        ({"repo": "x/y"}, "unknown repository"),
        ({"kind": "vibes"}, "unknown kind"),
        ({"split": "train"}, "split must be"),
        ({"origin": "phase3"}, "origin must be"),
        ({"question": "  "}, "empty question"),
        ({"kind": "trap", "why_hard": ""}, "trap question needs why_hard"),
    ],
)
def test_question_field_values_are_checked(overrides, text):
    one_problem(make_data([question(**overrides)]), text)


@pytest.mark.parametrize(
    "bad, text",
    [
        (span(start=0), "bad line range"),
        (span(start=3, end=2), "bad line range"),
        (span(start=True), "bad line range"),
        (span(grade=3), "grade must be 1 or 2"),
        (span(file="../etc/passwd"), "clean relative path"),
        (span(file="/abs.py"), "clean relative path"),
        (span(symbol=""), "symbol must be a non-empty string"),
        ({"start_line": 1, "end_line": 2, "grade": 2}, "needs a file"),
    ],
)
def test_span_fields_are_checked(bad, text):
    one_problem(make_data([question(relevant=[bad])]), text)


def test_unanswerable_question_must_have_no_spans():
    one_problem(make_data([question(kind="unanswerable")]), "must have no spans")


def test_answerable_question_needs_a_grade_2_span():
    one_problem(make_data([question(relevant=[span(grade=1)])]), "needs a grade-2 span")
    one_problem(make_data([question(relevant=[])]), "needs a grade-2 span")


# ---------------------------------------------------------------- spans against a real clone

def clone_data(local_repo, questions):
    return make_data(questions, commit=git_head(local_repo))


def test_spans_inside_real_files_pass(local_repo):
    data = clone_data(local_repo, [question(), question("q2", relevant=[span(file="README.md", start=1, end=1)])])
    assert ev.span_problems(data, local_repo.parent) == []


def test_missing_clone_and_wrong_commit_are_reported(local_repo, tmp_path):
    assert any("no git clone" in p for p in ev.span_problems(make_data(), tmp_path / "nowhere"))
    assert any("but the file pins" in p for p in ev.span_problems(make_data(commit="f" * 40), local_repo.parent))


def test_missing_file_is_reported(local_repo):
    data = clone_data(local_repo, [question(relevant=[span(file="nope.py")])])
    assert any("file not found" in p for p in ev.span_problems(data, local_repo.parent))


def test_span_past_the_end_of_the_file_is_reported(local_repo):
    data = clone_data(local_repo, [question(relevant=[span(start=2, end=3)])])
    assert any("has only 2 lines" in p for p in ev.span_problems(data, local_repo.parent))


def test_symbol_outside_its_span_is_reported(local_repo):
    data = clone_data(local_repo, [question(relevant=[span(start=2, end=2, symbol="hello")])])
    assert any("'hello' does not appear" in p for p in ev.span_problems(data, local_repo.parent))


def test_dotted_symbol_is_matched_by_its_last_part(local_repo):
    data = clone_data(local_repo, [question(relevant=[span(symbol="Greeter.hello")])])
    assert ev.span_problems(data, local_repo.parent) == []


def test_path_escaping_the_clone_is_reported(local_repo):
    (local_repo.parent / "outside.py").write_text("x\n")
    data = clone_data(local_repo, [question(relevant=[span(file="sub/../../outside.py", start=1, end=1)])])
    assert any("outside the repository" in p for p in ev.span_problems(data, local_repo.parent))


# ---------------------------------------------------------------- summary and main

def test_summary_counts_by_repository_kind_and_split():
    data = make_data([question(), question("q2", split="test"), question("q3", "unanswerable", relevant=[])])
    text = ev.summary(data)
    assert text.startswith("3 questions, 2 spans; dev 2, test 1")
    assert "demo/src" in text and "1/1" in text


def test_main_exit_codes(local_repo, tmp_path, capsys):
    good = tmp_path / "good.json"
    good.write_text(json.dumps(clone_data(local_repo, [question()])))
    assert ev.main([str(good), "--clones", str(local_repo.parent)]) == 0
    assert capsys.readouterr().out.strip().endswith("ok")

    bad = tmp_path / "bad.json"
    bad.write_text(json.dumps(make_data([question(kind="vibes")])))
    assert ev.main([str(bad), "--clones", str(local_repo.parent)]) == 1
    assert "unknown kind" in capsys.readouterr().out
