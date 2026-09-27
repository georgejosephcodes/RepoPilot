"""Check that embedding questions in one batch gives the same vectors as embedding them one by one.

Uses the same settings as the API (EMBED_BASE_URL, EMBED_API_KEY, EMBED_MODEL, input_type search_query) and never
prints the key. Costs 4 requests: three single questions, then the same three as one batch.
Run:  cd /home/georgejoseph/Rag-project && set -a && . ./.env && set +a && worker/.venv/bin/python worker/scripts/probe_batch_queries.py
Pass: every pair has cosine similarity of at least 0.9999. Standard library only.
"""

import json
import math
import os
import sys
import urllib.error
import urllib.request

BASE = os.environ.get("EMBED_BASE_URL", "https://openrouter.ai/api/v1").rstrip("/")
KEY = os.environ.get("EMBED_API_KEY", "")
MODEL = os.environ.get("EMBED_MODEL", "nvidia/nemotron-3-embed-1b:free")
QUESTIONS = [
    "Where is the HMAC signature computed?",
    "How does a group find and run the subcommand named on the command line?",
    "What does isAbsoluteModule2 do?",
]
THRESHOLD = 0.9999


def embed(texts):
    body = json.dumps({"model": MODEL, "input": texts, "input_type": "search_query"}).encode()
    req = urllib.request.Request(BASE + "/embeddings", data=body, method="POST",
                                 headers={"Authorization": "Bearer " + KEY, "Content-Type": "application/json"})
    try:
        with urllib.request.urlopen(req, timeout=90) as resp:
            data = json.load(resp)
    except urllib.error.HTTPError as e:
        sys.exit(f"HTTP {e.code}: {e.read()[:300].decode('utf-8', 'replace')}")
    items = data["data"]
    if len(items) != len(texts) or sorted(i["index"] for i in items) != list(range(len(texts))):
        sys.exit(f"unexpected indexes: {[i.get('index') for i in items]}")
    return [i["embedding"] for i in sorted(items, key=lambda i: i["index"])]


def cos(a, b):
    dot = sum(x * y for x, y in zip(a, b))
    return dot / (math.sqrt(sum(x * x for x in a)) * math.sqrt(sum(y * y for y in b)))


def main():
    if not KEY:
        sys.exit("EMBED_API_KEY is not set")
    singles = [embed([q])[0] for q in QUESTIONS]
    batch = embed(QUESTIONS)
    worst = 1.0
    for q, s, b in zip(QUESTIONS, singles, batch):
        c = cos(s, b)
        worst = min(worst, c)
        print(f"{c:.8f}  len={len(s)}  {q}")
    print(f"model {MODEL}: worst cosine {worst:.8f} -> {'PASS' if worst >= THRESHOLD else 'FAIL'} (threshold {THRESHOLD})")
    return 0 if worst >= THRESHOLD else 1


if __name__ == "__main__":
    sys.exit(main())
