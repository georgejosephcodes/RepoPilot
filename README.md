# RepoPilot

Ask questions about a GitHub repository and get answers grounded in its code, with file and line citations you can check.

RepoPilot indexes a repository into syntax-aware chunks, finds the chunks relevant to a question with hybrid search and reranking, and has a language model answer from those chunks only. Every citation is validated on the server against what was actually retrieved. Questions the code cannot answer are refused.

[![CI](https://github.com/georgejosephcodes/RepoPilot/actions/workflows/ci.yml/badge.svg)](https://github.com/georgejosephcodes/RepoPilot/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

## Results

Retrieval was measured on a labelled set of 74 questions over 4 real repositories. Settings were chosen on a dev split; the numbers below are the held-out **test split**, run once.

| retrieval | R@1 | R@3 | R@8 | MRR@20 |
|---|---|---|---|---|
| vector search (baseline) | 0.500 | 0.792 | 0.875 | 0.668 |
| **hybrid + rerank** | **0.875** | **0.958** | **1.000** | **0.920** |

n = 24 answerable test questions.
- **End to end:** asking all 74 questions through the API, 73 answers passed every mechanical check (citations match the real lines, an expected file is cited), and all 8 unanswerable questions were refused.
- **Cost:** reranking adds about 1.6 s per question (median).

Full write-up, including what failed: [`docs/phase2/results.md`](docs/phase2/results.md).

## How it works

```
index (worker, once per repository)
  GitHub URL -> shallow clone -> scan -> tree-sitter parse -> chunks (one per function, class or method)
             -> embed each chunk -> Postgres + pgvector

query (API, per question)
  question -> embed
           -> vector search + BM25 keyword search (same repository, optional language/path filters)
           -> weighted reciprocal rank fusion, test files down-weighted
           -> LLM reranks the top 20
           -> LLM answers from the top 8, citing [n]
           -> server checks every [n] against the retrieved chunks
           -> answer + file:line citations + snippets
```

- **API** (`api/`): Go and Gin. It serves the REST API and a terminal-style web UI.
- **Indexing worker** (`worker/`): Python, because tree-sitter and the embedding clients are most mature there.
- **Storage:** everything lives in PostgreSQL with pgvector, which also serves as the job queue (`FOR UPDATE SKIP LOCKED`).
- **Design:** decisions and trade-offs are in [`ARCHITECTURE.md`](ARCHITECTURE.md).

## Example

```text
repopilot ~ pallets/click ▸ ask --lang python where is the help text formatted?
filters: language python
The help text is formatted by the `format_help_text` method in `src/click/core.py`, which is called internally by `format_help` [1][2].
sources:
  [1]  src/click/core.py:1284-1301  Command.format_help
  [2]  src/click/core.py:1303-1319  Command.format_help_text
type 'show <n>' to view a snippet
3.5s · hybrid_rerank (rerank 1.8s) · 8 chunks · 1,426 in / 40 out tokens
```

Commands: `add <github-url>`, `repos`, `use <id|owner/name>`, `status`, `ask [--lang go,python] [--path dir/] <question>` (the word `ask` is optional), `show <n>` to see a cited snippet, `help`.

## Getting started

You need:
- Docker with Compose;
- Go 1.26 or newer;
- Python 3 (tested on 3.14);
- `git`;
- two free API keys: one for embeddings ([OpenRouter](https://openrouter.ai), or [NVIDIA NIM](https://build.nvidia.com) for a larger free quota) and one for answers ([Gemini](https://aistudio.google.com)).

```bash
cp .env.example .env              # set EMBED_API_KEY and GEMINI_API_KEY (see the comments for NIM)
docker compose up -d              # Postgres + pgvector on port 5433; applies migrations/ on first start
cd worker && python3 -m venv .venv && .venv/bin/pip install -r requirements.txt && cd ..
```

Run the API and the worker in two terminals from the repository root:

```bash
# terminal 1: API and web UI on http://localhost:8080
cd api && set -a && . ../.env && set +a && go run ./cmd/server

# terminal 2: indexing worker
cd worker && set -a && . ../.env && set +a && .venv/bin/python -m repopilot_worker.main
```

Open http://localhost:8080, type `add https://github.com/<owner>/<repo>`, wait for `[ready]`, then ask a question.

## Configuration

Every setting is in [`.env.example`](.env.example) with its default. The ones you are most likely to change:

| variable | purpose |
|---|---|
| `EMBED_BASE_URL`, `EMBED_API_KEY`, `EMBED_MODEL` | Embedding provider (any OpenAI-compatible API) |
| `GEMINI_API_KEY`, `LLM_MODEL` | Answer and rerank model |
| `RETRIEVAL_MODE` | `hybrid_rerank` (default), `hybrid` (no rerank, about 1.6 s faster) or `vector` |
| `RETRIEVAL_TOP_K` | Chunks given to the answer model (default 8) |
| `QUERY_RATE_LIMIT_PER_MIN` | Questions per minute per client (default 7, to stay inside the free Gemini tier) |

## Tests and evaluation

```bash
cd api && go vet ./... && go test ./...            # database tests run when TEST_DATABASE_URL is set
cd worker && .venv/bin/pytest -q
```

The web UI has a self-test at `http://localhost:8080/#selftest`.

The retrieval evaluation (`api/cmd/eval`) and the end-to-end answer runner (`worker/scripts/run_answers.py`) are described in [`docs/phase2/results.md`](docs/phase2/results.md#reproducing). Question vectors and rerankings are cached in Postgres, so re-running an evaluation costs no API requests.

## Repository layout

```
api/cmd/server           API server and web UI
api/cmd/eval             retrieval evaluation against docs/phase2/eval.json
api/cmd/search-debug     shows a question's rank in every retrieval list
api/internal/            retrieval, rerank, pipeline, rag (prompt, citations), embed, llm, httpapi, repos
api/web/                 terminal-style UI (plain HTML, CSS, JS)
worker/repopilot_worker  clone, scan, parse, chunk, embed, store
worker/scripts           evaluation runners and provider probes
migrations/              SQL schema, applied in order
docs/phase1, docs/phase2 evaluation sets, runs and reports
```

## Limits

- **Public repositories only.** A change re-indexes the whole repository.
- **Free tiers.** Embeddings and answers use free tiers, so large repositories and heavy use hit daily limits. The worker checks its budget before embedding, and vectors are cached, so a retry after the daily reset pays only for new chunks.
- **Long functions.** A function longer than the chunk limit is split into pieces, and an answer can miss the piece that holds the key lines (see `cl-4` in the results).
- **Model output.** Citation checks prove that the cited code was retrieved and quoted exactly. They do not prove that the model read it correctly.

## Roadmap

1. Issues and pull requests: explain an issue in the context of the code, and find the relevant files.
2. Architecture view: a dependency graph and a suggested reading order for a new repository.
3. Production hardening: incremental re-indexing, authentication, metrics.

Ideas and bug reports are welcome as [issues](https://github.com/georgejosephcodes/RepoPilot/issues).

## Contributing

See [`CONTRIBUTING.md`](CONTRIBUTING.md).

## License

[MIT](LICENSE)
