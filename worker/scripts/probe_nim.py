"""Can NVIDIA NIM replace OpenRouter for embeddings, without re-indexing? And how does NIM's free reranker do?

Every call goes to NVIDIA's free API catalog only (NVIDIA_API_KEY). 0 OpenRouter requests, 0 Gemini requests:
the OpenRouter vectors to compare against are read from the database (question vectors from
query_embedding_cache, chunk vectors from chunks), so the probe costs nothing on the tight budgets.

Calls (12 in total, paced 2 s apart, well under the claimed 40 per minute):
  1-3   embeddings for 3 cached dev questions, input_type = search_query | query | (none)
  4-6   embeddings for 3 itsdangerous chunks re-chunked from /tmp/demo, input_type = search_document | passage | (none)
  7-12  reranking (nv-rerankqa-mistral-4b-v3) of the hybrid top 20 for the 6 dev questions of probe_rerank.py
It prints cosine similarity against the stored vectors (1.0 means the same vectors, so no re-index), the rate
limit headers NVIDIA sends back, and the reranker's first answering rank before and after. The key is never printed.
Run:  cd /home/georgejoseph/Rag-project && set -a && . ./.env && set +a && worker/.venv/bin/python worker/scripts/probe_nim.py
"""

import hashlib
import json
import math
import os
import sys
import time
import urllib.error
import urllib.request
from pathlib import Path

import psycopg

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from repopilot_worker.chunk import chunk_file  # noqa: E402
from scripts.probe_rerank import QUESTIONS, complete, doc_text, first_hit  # noqa: E402

ROOT = Path(__file__).resolve().parents[2]
KEY = os.environ.get("NVIDIA_API_KEY", "")
STORED_MODEL = os.environ.get("EMBED_MODEL", "nvidia/nemotron-3-embed-1b:free")
MAX_CHARS = int(os.environ.get("EMBED_MAX_CHARS", "8000"))
NIM_EMBED_URL = "https://integrate.api.nvidia.com/v1/embeddings"
NIM_EMBED_MODEL = "nvidia/nemotron-3-embed-1b"
NIM_RERANK_URL = "https://ai.api.nvidia.com/v1/retrieval/nvidia/nv-rerankqa-mistral-4b-v3/reranking"
NIM_RERANK_MODEL = "nvidia/nv-rerankqa-mistral-4b-v3"
calls = 0


def post(url, body):
    global calls
    calls += 1
    req = urllib.request.Request(url, data=json.dumps(body).encode(), method="POST",
                                 headers={"Authorization": "Bearer " + KEY, "Content-Type": "application/json",
                                          "Accept": "application/json"})
    t0 = time.monotonic()
    try:
        with urllib.request.urlopen(req, timeout=90) as resp:
            limits = {k: v for k, v in resp.headers.items() if "limit" in k.lower() or "remaining" in k.lower()}
            return resp.status, json.load(resp), limits, (time.monotonic() - t0) * 1000
    except urllib.error.HTTPError as e:
        return e.code, {"error_body": e.read()[:300].decode("utf-8", "replace")}, {}, (time.monotonic() - t0) * 1000
    finally:
        time.sleep(2)


def unit(v):
    n = math.sqrt(sum(x * x for x in v))
    return [x / n for x in v]


def cos(a, b):
    return sum(x * y for x, y in zip(unit(a), unit(b)))


def parse_vec(text):
    return [float(x) for x in text.strip("[]").split(",")]


def embed(texts, input_type):
    body = {"model": NIM_EMBED_MODEL, "input": texts, "encoding_format": "float"}
    if input_type:
        body["input_type"] = input_type
    status, data, limits, ms = post(NIM_EMBED_URL, body)
    if status != 200:
        return None, f"HTTP {status} {data.get('error_body', '')[:200]}", limits, ms
    items = sorted(data["data"], key=lambda d: d["index"])
    return [d["embedding"] for d in items], "", limits, ms


def main():
    if not KEY or not os.environ.get("DATABASE_URL"):
        sys.exit("NVIDIA_API_KEY and DATABASE_URL must be set")
    labels = {q["id"]: q for q in json.loads((ROOT / "docs/phase2/eval.json").read_text())["questions"]}
    run = {q["id"]: q for q in json.loads((ROOT / "docs/phase2/runs/hybrid-dev-chosen.json").read_text())["questions"]}

    with psycopg.connect(os.environ["DATABASE_URL"]) as conn:
        # --- questions: stored OpenRouter vectors from the question cache
        qids = QUESTIONS[:3]
        qtexts = [labels[q]["question"].strip()[:MAX_CHARS] for q in qids]
        stored_q = []
        for t in qtexts:
            h = hashlib.sha256(t.encode()).hexdigest()
            row = conn.execute("SELECT embedding::text FROM query_embedding_cache WHERE embed_model = %s AND text_hash = %s",
                               (STORED_MODEL, h)).fetchone()
            if row is None:
                sys.exit(f"question vector not cached for {t!r}; run the dev eval once first")
            stored_q.append(parse_vec(row[0]))

        # --- documents: re-chunk one real file and match chunks by line range to their stored vectors
        path = Path("/tmp/demo/itsdangerous/src/itsdangerous/encoding.py")
        if not path.is_file():
            sys.exit(f"{path} not found (the demo clones are needed)")
        chunks = chunk_file("src/itsdangerous/encoding.py", "python", path.read_bytes())[:3]
        repo_id = conn.execute("SELECT id FROM repositories WHERE lower(owner) = 'pallets' AND lower(name) = 'itsdangerous'").fetchone()[0]
        stored_d, dtexts = [], []
        for c in chunks:
            row = conn.execute("SELECT embedding::text FROM chunks WHERE repo_id = %s AND file_path = %s AND start_line = %s AND end_line = %s",
                               (repo_id, c.file_path, c.start_line, c.end_line)).fetchone()
            if row is None:
                sys.exit(f"no stored chunk for lines {c.start_line}-{c.end_line}")
            stored_d.append(parse_vec(row[0]))
            dtexts.append(c.embed_text[:MAX_CHARS])

        # --- rerank inputs
        items = []
        for qid in QUESTIONS:
            retrieved = run[qid]["retrieved"]
            content = dict(conn.execute("SELECT id, content FROM chunks WHERE id = ANY(%s)", ([r["chunk_id"] for r in retrieved],)).fetchall())
            items.append((qid, labels[qid], retrieved, [doc_text(r, content.get(r["chunk_id"], "")) for r in retrieved]))

    print(f"=== embeddings: NIM {NIM_EMBED_MODEL} against stored OpenRouter {STORED_MODEL} (cosine; >= 0.999 means the same vectors)")
    for kind, texts, stored, types in (("questions", qtexts, stored_q, ["search_query", "query", None]),
                                       ("documents", dtexts, stored_d, ["search_document", "passage", None])):
        for it in types:
            vecs, err, limits, ms = embed(texts, it)
            if vecs is None:
                print(f"  {kind:9} input_type={it!s:15} {err}")
                continue
            sims = [cos(v, s) for v, s in zip(vecs, stored)]
            print(f"  {kind:9} input_type={it!s:15} dim {len(vecs[0])}  cosines {', '.join(f'{x:.5f}' for x in sims)}  {ms:.0f} ms  {limits}")

    print(f"\n=== reranking: {NIM_RERANK_MODEL} on the hybrid top 20 (first answering rank before -> after)")
    for qid, lab, retrieved, docs in items:
        body = {"model": NIM_RERANK_MODEL, "query": {"text": lab["question"]}, "passages": [{"text": d} for d in docs], "truncate": "END"}
        status, data, limits, ms = post(NIM_RERANK_URL, body)
        if status != 200:
            print(f"  {qid}: HTTP {status} {data.get('error_body', '')[:200]}")
            continue
        order = [r["index"] for r in sorted(data.get("rankings", []), key=lambda r: -r["logit"])]
        before = first_hit(range(len(retrieved)), retrieved, lab["relevant"])
        after = first_hit(complete(order, len(docs)), retrieved, lab["relevant"])
        print(f"  {qid}: {before or 'none'} -> {after or 'none'}  {ms:.0f} ms  {limits}")

    print(f"\nNIM requests: {calls}; OpenRouter 0; Gemini 0")


if __name__ == "__main__":
    main()
