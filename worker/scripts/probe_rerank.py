"""Probe: can we rerank the hybrid top 20, with what, at what cost and latency?

Takes 6 dev questions from docs/phase2/runs/hybrid-dev-chosen.json (their top 20 chunks, read from the database by
id), reranks them with each OpenRouter rerank model and with Gemini as a list reranker, and prints the first
answering rank before and after (labels from docs/phase2/eval.json), latency, and the cost fields each API
returns. Dev questions only. Keys are read from the environment and never printed.

Budget: OpenRouter at most 25 requests (1 per question per model; a model that fails its first call is not tried
again), Gemini 8 requests (6 questions + 2 repeats to check stability), paced for 15 per minute.
Run:  cd /home/georgejoseph/Rag-project && set -a && . ./.env && set +a && worker/.venv/bin/python worker/scripts/probe_rerank.py
"""

import json
import os
import re
import sys
import time
import urllib.error
import urllib.request
from pathlib import Path

import psycopg

ROOT = Path(__file__).resolve().parents[2]
QUESTIONS = ["ts-1", "cl-7", "go-1", "go-7", "cl-5", "cl-4"]  # dev only: two losses of fusion, one tie, three weak hits
OR_BASE = os.environ.get("EMBED_BASE_URL", "https://openrouter.ai/api/v1").rstrip("/")
OR_KEY = os.environ.get("EMBED_API_KEY", "")
GEM_KEY = os.environ.get("LLM_API_KEY") or os.environ.get("GEMINI_API_KEY", "")
GEM_MODEL = os.environ.get("LLM_MODEL", "gemini-3.5-flash-lite")
OR_MODELS = ["nvidia/llama-nemotron-rerank-vl-1b-v2:free", "cohere/rerank-4-fast", "voyageai/rerank-2.5-lite",
             "qwen/qwen3-reranker-8b", "cohere/rerank-v3.5", "voyageai/rerank-2.5", "cohere/rerank-4-pro"]
DOC_CHARS = 2000
OR_CAP = 25
calls = {"openrouter": 0, "gemini": 0}


def post(url, body, headers, timeout=90):
    req = urllib.request.Request(url, data=json.dumps(body).encode(), method="POST",
                                 headers={"Content-Type": "application/json", **headers})
    t0 = time.monotonic()
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            return resp.status, json.load(resp), (time.monotonic() - t0) * 1000
    except urllib.error.HTTPError as e:
        return e.code, {"error_body": e.read()[:300].decode("utf-8", "replace")}, (time.monotonic() - t0) * 1000
    except Exception as e:  # network problems: report the type only
        return 0, {"error_body": type(e).__name__}, (time.monotonic() - t0) * 1000


def first_hit(order, retrieved, relevant):
    for rank, idx in enumerate(order, 1):
        r = retrieved[idx]
        for s in relevant:
            if s["grade"] == 2 and s["file"] == r["file"] and r["start_line"] <= s["end_line"] and s["start_line"] <= r["end_line"]:
                return rank
    return 0


def complete(order, n):
    """Keep the reranker's order, then append anything it left out in the original order."""
    seen = []
    for i in order:
        if 0 <= i < n and i not in seen:
            seen.append(i)
    return seen + [i for i in range(n) if i not in seen]


def doc_text(r, content):
    head = f"{r['file']}:{r['start_line']}-{r['end_line']} {r.get('symbol') or ''}".strip()
    return head + "\n" + content[:DOC_CHARS]


def openrouter(model, query, docs):
    calls["openrouter"] += 1
    status, data, ms = post(OR_BASE + "/rerank", {"model": model, "query": query, "documents": docs},
                            {"Authorization": "Bearer " + OR_KEY})
    if status != 200:
        return None, status, data.get("error_body", ""), ms, None
    order = [r["index"] for r in sorted(data.get("results", []), key=lambda r: -r["relevance_score"])]
    return order, status, "", ms, {"usage": data.get("usage"), "provider": data.get("provider")}


GEM_PROMPT = """You rank code search results. Question about a repository:
{question}

Candidates, numbered:
{candidates}

Return JSON only: {{"ranking": [candidate numbers that help answer the question, most useful first]}}.
Leave out candidates that do not help. Use only the numbers shown."""


def gemini(question, docs):
    calls["gemini"] += 1
    cands = "\n\n".join(f"[{i + 1}] {d}" for i, d in enumerate(docs))
    body = {
        "contents": [{"role": "user", "parts": [{"text": GEM_PROMPT.format(question=question, candidates=cands)}]}],
        "generationConfig": {"temperature": 0, "responseMimeType": "application/json", "maxOutputTokens": 256,
                             "thinkingConfig": {"thinkingLevel": "minimal"}},
    }
    url = f"https://generativelanguage.googleapis.com/v1beta/models/{GEM_MODEL}:generateContent"
    status, data, ms = post(url, body, {"x-goog-api-key": GEM_KEY})
    if status != 200:
        return None, status, data.get("error_body", ""), ms, None
    text = ""
    try:
        text = "".join(p.get("text", "") for p in data["candidates"][0]["content"]["parts"])
        ranking = json.loads(text)["ranking"]
        order = [int(n) - 1 for n in ranking]
    except Exception as e:
        return None, status, f"unparseable: {type(e).__name__}: {text[:120]}", ms, None
    return order, status, "", ms, {"usage": data.get("usageMetadata"), "listed": len(order)}


def main():
    if not OR_KEY or not GEM_KEY or not os.environ.get("DATABASE_URL"):
        sys.exit("EMBED_API_KEY, GEMINI_API_KEY (or LLM_API_KEY) and DATABASE_URL must be set")
    labels = {q["id"]: q for q in json.loads((ROOT / "docs/phase2/eval.json").read_text())["questions"]}
    run = {q["id"]: q for q in json.loads((ROOT / "docs/phase2/runs/hybrid-dev-chosen.json").read_text())["questions"]}
    items = []
    with psycopg.connect(os.environ["DATABASE_URL"]) as conn:
        for qid in QUESTIONS:
            assert labels[qid]["split"] == "dev", qid
            retrieved = run[qid]["retrieved"]
            ids = [r["chunk_id"] for r in retrieved]
            content = dict(conn.execute("SELECT id, content FROM chunks WHERE id = ANY(%s)", (ids,)).fetchall())
            docs = [doc_text(r, content.get(r["chunk_id"], "")) for r in retrieved]
            items.append((qid, labels[qid], retrieved, docs))

    print(f"{len(items)} dev questions, 20 candidates each (hybrid w0.75 p0.5 n20); first answering rank before -> after\n")
    for qid, lab, retrieved, docs in items:
        print(f"{qid:6} hybrid {first_hit(range(len(retrieved)), retrieved, lab['relevant']) or 'none'}  {lab['question']}")

    failed = set()
    for model in OR_MODELS:
        print(f"\n=== OpenRouter {model}")
        for qid, lab, retrieved, docs in items:
            if model in failed or calls["openrouter"] >= OR_CAP:
                break
            order, status, err, ms, meta = openrouter(model, lab["question"], docs)
            if order is None:
                failed.add(model)
                print(f"  {qid}: HTTP {status} {err[:200]}")
                continue
            before = first_hit(range(len(retrieved)), retrieved, lab["relevant"])
            after = first_hit(complete(order, len(docs)), retrieved, lab["relevant"])
            print(f"  {qid}: {before or 'none'} -> {after or 'none'}  {ms:.0f} ms  {meta}")
            time.sleep(4)  # stay far below 20 per minute

    print(f"\n=== Gemini {GEM_MODEL} as a list reranker")
    runs = items + [items[0], items[0]]  # two repeats of the first question: is the order stable?
    for n, (qid, lab, retrieved, docs) in enumerate(runs):
        order, status, err, ms, meta = gemini(lab["question"], docs)
        if order is None:
            print(f"  {qid}: HTTP {status} {err[:200]}")
        else:
            before = first_hit(range(len(retrieved)), retrieved, lab["relevant"])
            after = first_hit(complete(order, len(docs)), retrieved, lab["relevant"])
            tag = " (repeat)" if n >= len(items) else ""
            print(f"  {qid}{tag}: {before or 'none'} -> {after or 'none'}  {ms:.0f} ms  order {order[:8]}  {meta}")
        time.sleep(5)

    print(f"\nrequests: {calls}")


if __name__ == "__main__":
    main()
