"""Tests for scripts/run_demo.py. No network and no database: the pure functions (validation, checks, report
rendering, resume) are exercised directly; conftest.py's `git` helper builds tiny real clones for the setup checks."""

import json

import pytest

from scripts import run_demo as demo


def make_data(**overrides):
    data = {
        "repositories": [
            {
                "name": "a/b",
                "language": "Python",
                "commit": "1" * 40,
                "questions": [
                    {"id": "q1", "kind": "location", "question": "where is X", "expected_files": ["x.py"], "expected_symbols": ["X"]},
                    {"id": "q2", "kind": "unanswerable", "question": "how does billing work", "expected_files": []},
                ],
            }
        ]
    }
    data.update(overrides)
    return data


# ---------------------------------------------------------------- validate_questions

def test_validate_questions_accepts_a_good_file():
    assert demo.validate_questions(make_data()) == []


def test_validate_questions_rejects_a_short_commit():
    data = make_data()
    data["repositories"][0]["commit"] = "abc123"
    assert any("commit" in p for p in demo.validate_questions(data))


def test_validate_questions_rejects_a_duplicate_id():
    data = make_data()
    data["repositories"][0]["questions"].append({"id": "q1", "kind": "flow", "question": "x", "expected_files": ["x.py"]})
    assert any("duplicate" in p for p in demo.validate_questions(data))


def test_validate_questions_rejects_an_unknown_kind():
    data = make_data()
    data["repositories"][0]["questions"][0]["kind"] = "vibes"
    assert any("unknown kind" in p for p in demo.validate_questions(data))


def test_validate_questions_rejects_an_unanswerable_question_with_expected_files():
    data = make_data()
    data["repositories"][0]["questions"][1]["expected_files"] = ["x.py"]
    assert any("expects no files" not in p and "unanswerable" in p for p in demo.validate_questions(data))


def test_validate_questions_rejects_an_answerable_question_without_expected_files():
    data = make_data()
    data["repositories"][0]["questions"][0]["expected_files"] = []
    assert any("needs expected files" in p for p in demo.validate_questions(data))


def test_validate_questions_rejects_missing_repositories():
    assert demo.validate_questions({}) == ["no repositories"]
    assert demo.validate_questions([]) == ["no repositories"]


# ---------------------------------------------------------------- flatten / find_repo / same_commit

def test_flatten_carries_repo_fields_onto_each_question():
    items = demo.flatten(make_data())
    assert [i["id"] for i in items] == ["q1", "q2"]
    assert items[0]["repo"] == "a/b" and items[0]["commit"] == "1" * 40 and items[0]["language"] == "Python"


def test_find_repo_matches_owner_slash_name_case_insensitively():
    repos = [{"owner": "Golang", "name": "Example", "id": 1}, {"owner": "a", "name": "b", "id": 2}]
    assert demo.find_repo(repos, "golang/example")["id"] == 1
    assert demo.find_repo(repos, "nope/nope") is None


@pytest.mark.parametrize(
    "a, b, want",
    [
        ("abc1234", "abc1234567890", True),
        ("abc1234567890", "abc1234", True),
        ("abc1234", "abc9999", False),
        (None, "abc1234", False),
        ("abc12", "abc12", False),  # too short to trust
        ("", "", False),
    ],
)
def test_same_commit(a, b, want):
    assert demo.same_commit(a, b) is want


# ---------------------------------------------------------------- citation_problems / marker_problems

def write_file(tmp_path, name, text):
    path = tmp_path / name
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text)
    return path


def test_citation_problems_empty_when_snippet_matches_the_real_lines(tmp_path):
    write_file(tmp_path, "x.py", "one\ntwo\nthree\n")
    citations = [{"n": 1, "file": "x.py", "start_line": 2, "end_line": 3, "snippet": "two\nthree"}]
    assert demo.citation_problems(citations, tmp_path) == []


def test_citation_problems_catches_a_wrong_snippet(tmp_path):
    write_file(tmp_path, "x.py", "one\ntwo\nthree\n")
    citations = [{"n": 1, "file": "x.py", "start_line": 1, "end_line": 1, "snippet": "wrong"}]
    assert "differs" in demo.citation_problems(citations, tmp_path)[0]


def test_citation_problems_catches_a_missing_file(tmp_path):
    citations = [{"n": 1, "file": "missing.py", "start_line": 1, "end_line": 1, "snippet": "x"}]
    assert "not found" in demo.citation_problems(citations, tmp_path)[0]


def test_citation_problems_rejects_a_path_escaping_the_repository(tmp_path):
    outside = tmp_path.parent / "secret.txt"
    outside.write_text("s\n")
    citations = [{"n": 1, "file": "../secret.txt", "start_line": 1, "end_line": 1, "snippet": "s"}]
    assert "outside" in demo.citation_problems(citations, tmp_path)[0]


def test_citation_problems_rejects_a_bad_line_range(tmp_path):
    write_file(tmp_path, "x.py", "one\n")
    citations = [{"n": 1, "file": "x.py", "start_line": 5, "end_line": 1, "snippet": ""}]
    assert "bad line range" in demo.citation_problems(citations, tmp_path)[0]


def test_marker_problems_empty_when_consistent():
    assert demo.marker_problems("see [1] and [2]", [{"n": 1}, {"n": 2}]) == []


def test_marker_problems_catches_an_uncited_marker():
    assert "[9] has no citation" in "; ".join(demo.marker_problems("see [9]", []))


def test_marker_problems_catches_an_unused_citation():
    assert "[1] is not used" in "; ".join(demo.marker_problems("no markers here", [{"n": 1}]))


def test_marker_problems_treats_adjacent_markers_as_separate():
    assert demo.marker_problems("chain [1][2]", [{"n": 1}, {"n": 2}]) == []


def test_marker_problems_ignores_an_index_glued_to_an_identifier():
    assert demo.marker_problems("items[1] is fine", []) == []


# ---------------------------------------------------------------- check_response

def location_item(expected_files=("x.py",)):
    return {"id": "q1", "kind": "location", "question": "where is X", "expected_files": list(expected_files), "repo": "r"}


def unanswerable_item():
    return {"id": "q2", "kind": "unanswerable", "question": "billing?", "expected_files": [], "repo": "r"}


def test_check_response_passes_a_correct_grounded_answer(tmp_path):
    write_file(tmp_path, "x.py", "a\nb\n")
    body = {"answer": "X is here [1].", "grounded": True, "refused": False, "truncated": False,
            "citations": [{"n": 1, "file": "x.py", "start_line": 1, "end_line": 2, "snippet": "a\nb"}]}
    result = demo.check_response(location_item(), body, tmp_path)
    assert result["passed"] is True
    assert result["problems"] == []
    assert result["cited_files"] == ["x.py"]


def test_check_response_fails_when_the_expected_file_is_not_cited(tmp_path):
    write_file(tmp_path, "y.py", "a\n")
    body = {"answer": "see [1]", "grounded": True, "refused": False, "truncated": False,
            "citations": [{"n": 1, "file": "y.py", "start_line": 1, "end_line": 1, "snippet": "a"}]}
    result = demo.check_response(location_item(expected_files=["x.py"]), body, tmp_path)
    assert result["passed"] is False
    assert any("expected files" in p for p in result["problems"])


def test_check_response_fails_a_wrongly_refused_location_answer(tmp_path):
    body = {"answer": "I don't know.", "grounded": False, "refused": True, "truncated": False, "citations": []}
    result = demo.check_response(location_item(), body, tmp_path)
    assert result["passed"] is False
    assert result["checks"]["not_refused"] is False


def test_check_response_fails_a_truncated_answer(tmp_path):
    write_file(tmp_path, "x.py", "a\n")
    body = {"answer": "see [1]", "grounded": True, "refused": False, "truncated": True,
            "citations": [{"n": 1, "file": "x.py", "start_line": 1, "end_line": 1, "snippet": "a"}]}
    result = demo.check_response(location_item(), body, tmp_path)
    assert result["passed"] is False and result["checks"]["not_truncated"] is False


def test_check_response_passes_a_correct_refusal(tmp_path):
    body = {"answer": "The code does not contain any billing logic.", "grounded": False, "refused": True,
            "truncated": False, "citations": []}
    result = demo.check_response(unanswerable_item(), body, tmp_path)
    assert result["passed"] is True
    # answerable-only checks do not apply to a refusal
    assert result["checks"]["expected_file_cited"] is None and result["checks"]["grounded"] is None


def test_check_response_fails_when_an_unanswerable_question_gets_a_fabricated_citation(tmp_path):
    write_file(tmp_path, "x.py", "a\n")
    body = {"answer": "billing is in [1]", "grounded": True, "refused": False, "truncated": False,
            "citations": [{"n": 1, "file": "x.py", "start_line": 1, "end_line": 1, "snippet": "a"}]}
    result = demo.check_response(unanswerable_item(), body, tmp_path)
    assert result["passed"] is False
    assert result["checks"]["refusal_correct"] is False


def test_check_response_rejects_a_malformed_body(tmp_path):
    result = demo.check_response(location_item(), {"error": {"code": "x", "message": "y"}}, tmp_path)
    assert result["passed"] is False
    assert "not a query answer" in result["problems"][0]


# ---------------------------------------------------------------- run_questions (resume, pacing, stop on error)

def test_run_questions_asks_only_unanswered_items_and_saves_after_each():
    items = [{"id": "a", "repo": "r", "kind": "location", "question": "?"}, {"id": "b", "repo": "r", "kind": "location", "question": "?"}]
    results = {"a": {"response": "already answered"}}
    asked, saved, slept = [], [], []

    def ask(item):
        asked.append(item["id"])
        return {"ok": True, "body": {"answer": "x", "citations": []}, "elapsed_ms": 5}

    stop = demo.run_questions(items, results, ask, lambda r: saved.append(dict(r)), slept.append)
    assert stop is None
    assert asked == ["b"]
    assert "b" in results and results["b"]["response"]["answer"] == "x"
    assert saved and "b" in saved[-1]
    assert slept == []  # only one question was asked, so no pacing delay was needed


def test_run_questions_paces_between_multiple_questions():
    items = [{"id": "a", "repo": "r", "kind": "location", "question": "?"}, {"id": "b", "repo": "r", "kind": "location", "question": "?"}]
    slept = []
    demo.run_questions(items, {}, lambda i: {"ok": True, "body": {"answer": "x", "citations": []}, "elapsed_ms": 1},
                        lambda r: None, slept.append, pace=9)
    assert slept == [9]


def test_run_questions_stops_and_keeps_earlier_results_on_a_failure():
    items = [{"id": "a", "repo": "r", "kind": "location", "question": "?"}, {"id": "b", "repo": "r", "kind": "location", "question": "?"}]
    results = {}

    def ask(item):
        if item["id"] == "a":
            return {"ok": True, "body": {"answer": "x", "citations": []}, "elapsed_ms": 1}
        return {"ok": False, "message": "rate_limited: slow down"}

    stop = demo.run_questions(items, results, ask, lambda r: None, lambda s: None)
    assert stop is not None and "b" in stop and "rate_limited" in stop
    assert "a" in results and "b" not in results


# ---------------------------------------------------------------- report rendering

def test_render_report_counts_pass_and_ask_totals(tmp_path):
    write_file(tmp_path, "x.py", "a\n")
    items = [location_item(), unanswerable_item()]
    results = {
        "q1": {"repo": "r", "elapsed_ms": 1000, "response": {"answer": "[1]", "grounded": True, "refused": False, "truncated": False,
                                                               "citations": [{"n": 1, "file": "x.py", "start_line": 1, "end_line": 1, "snippet": "a"}]}},
    }
    report = demo.render_report(items, results, {"r": tmp_path})
    assert "Questions asked: 1 of 2" in report
    assert "not asked" in report  # q2 was never asked
    assert "q1" in report and "q2" in report


def test_render_report_lists_failed_checks_with_the_reason(tmp_path):
    write_file(tmp_path, "x.py", "a\n")
    results = {"q1": {"repo": "r", "elapsed_ms": 1, "response": {"answer": "no citations here", "grounded": False, "refused": False,
                                                                   "truncated": False, "citations": []}}}
    report = demo.render_report([location_item()], results, {"r": tmp_path})
    assert "## Failed checks" in report
    assert "q1" in report.split("## Failed checks")[1]


# ---------------------------------------------------------------- storage round-trip

def test_save_and_load_results_round_trip(tmp_path):
    path = tmp_path / "raw.json"
    demo.save_results(path, {"q1": {"response": {"answer": "x"}}}, "http://localhost:8080")
    assert demo.load_results(path) == {"q1": {"response": {"answer": "x"}}}


def test_load_results_of_a_missing_file_is_empty(tmp_path):
    assert demo.load_results(tmp_path / "nope.json") == {}


def test_save_results_is_valid_json_even_after_two_writes(tmp_path):
    path = tmp_path / "raw.json"
    demo.save_results(path, {"a": 1}, "x")
    demo.save_results(path, {"a": 1, "b": 2}, "x")
    assert json.loads(path.read_text())["results"] == {"a": 1, "b": 2}


# ---------------------------------------------------------------- setup_problems (real git clones, no network)

def test_setup_problems_passes_for_a_clone_at_the_right_commit(local_repo):
    commit = demo.git_head(local_repo)
    data = {"repositories": [{"name": "demo/src", "commit": commit, "questions": []}]}
    problems, ids, roots = demo.setup_problems(data, local_repo.parent, None)
    assert problems == []
    assert roots["demo/src"] == local_repo.parent / "src"


def test_setup_problems_catches_a_commit_mismatch(local_repo):
    data = {"repositories": [{"name": "demo/src", "commit": "f" * 40, "questions": []}]}
    problems, _, _ = demo.setup_problems(data, local_repo.parent, None)
    assert any("but the questions pin" in p for p in problems)


def test_setup_problems_catches_a_missing_clone(tmp_path):
    data = {"repositories": [{"name": "demo/repo", "commit": "1" * 40, "questions": []}]}
    problems, _, _ = demo.setup_problems(data, tmp_path, None)
    assert any("no git clone" in p for p in problems)


def test_setup_problems_checks_the_api_state_when_given(local_repo):
    commit = demo.git_head(local_repo)
    data = {"repositories": [{"name": "demo/src", "commit": commit, "questions": []}]}
    api_repos = [{"owner": "demo", "name": "src", "id": 7, "status": "queued", "commit_sha": commit}]
    problems, ids, _ = demo.setup_problems(data, local_repo.parent, api_repos)
    assert ids["demo/src"] == 7
    assert any("status is queued" in p for p in problems)


def test_setup_problems_reports_a_repository_not_indexed(local_repo):
    commit = demo.git_head(local_repo)
    data = {"repositories": [{"name": "demo/src", "commit": commit, "questions": []}]}
    problems, ids, _ = demo.setup_problems(data, local_repo.parent, [])
    assert any("not indexed" in p for p in problems)
    assert "demo/src" not in ids
