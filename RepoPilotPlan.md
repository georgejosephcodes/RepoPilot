# RepoPilot — Build Plan (Phase 1 detailed, Phases 2-5 outlined)

> This file is the original outline. Design decisions and their reasons are in [ARCHITECTURE.md](ARCHITECTURE.md).

## Context

RepoPilot (spec: `RepoPilot.md`) is a RAG-based developer tool. The working directory started empty except for the spec, so this is a greenfield build.

Decisions:
- Go = main backend/API. Python = indexing/ML worker. No extra microservices.
- Free-tier cloud AI. LLM and embedding provider each sit behind an interface so they can be swapped.
- Target repos to parse: Python, TypeScript/JS, Go.
- UI is **terminal-style** (looks and behaves like a CLI) in plain HTML/JS for now. A real CLI comes later and reuses the same command set and REST API.
- Build and verify **one phase at a time**. Phase 1 must stay small. No tech added without a current need.

Provider decisions:
- **Gemini API for both LLM and embeddings** (free tier, one API key, one quota). Groq or others can be added later as another adapter behind the same interface.
- Exact model names and embedding output dimension get verified against current Gemini docs before coding. Use 768 dims, and store the model name with each chunk.
- HTTP framework: **Gin**.

## Phase 1 — Working MVP (end-to-end repo Q&A with citations)

### Architecture (kept minimal)

```
Web UI ──> Go API (Gin) ──> Postgres + pgvector <── Python worker
                │                                        │
                ├─ Embedder iface (Gemini)               ├─ git clone, tree-sitter, chunk
                └─ LLM iface (Gemini)                    └─ Embedder (Gemini)
```

- **No Redis and no queue in Phase 1.** Go inserts a row in `index_jobs`. Python worker polls with `SELECT ... FOR UPDATE SKIP LOCKED`. Redis/streams come in Phase 5 only if needed.
- Query path: Go embeds question, runs pgvector cosine search, builds prompt, calls LLM, validates citations.
- Index path: Python worker clones, parses, chunks, embeds, writes chunks.

### Repo layout

```
RepoPilot/
  docker-compose.yml        # postgres+pgvector (later: api, worker)
  migrations/001_init.sql
  api/                      # Go
    cmd/server/main.go
    internal/httpapi/       # Gin router, handlers
    internal/repos/         # URL validation, repo CRUD, job enqueue
    internal/retrieval/     # vector search (SQL)
    internal/rag/           # prompt builder, citation parser/validator
    internal/embed/         # Embedder interface + gemini.go
    internal/llm/           # LLM interface + gemini.go
  worker/                   # Python
    repopilot_worker/{main,jobs,clone,scan,parse,chunk,embed,db}.py
    tests/
  web/                      # minimal UI (see step 8)
  .env.example
```

### Schema (only what Phase 1 needs)

- `repositories(id, url, owner, name, commit_sha, status, created_at)`
- `index_jobs(id, repo_id, status, error, attempts, started_at, finished_at)`
- `chunks(id, repo_id, commit_sha, file_path, language, symbol, kind, start_line, end_line, content, embedding vector(768), embed_model)`
- HNSW index on `embedding` with cosine ops; btree on `(repo_id)`.
- Skip `users`, `files`, `symbols`, `conversations` until a later phase needs them (chunk rows already carry symbol metadata).

### API (Phase 1)

- `POST /api/repositories` `{url}` → validates, creates repo + job, returns id
- `GET /api/repositories`, `GET /api/repositories/:id` (includes status/progress)
- `POST /api/repositories/:id/query` `{question}` → `{answer, citations[{file,start_line,end_line,symbol,snippet}]}`

### Build order (each step verified before the next)

1. **Scaffold + DB.** docker-compose Postgres with pgvector, migration, Go `/healthz`, Python worker connects. *Verify:* `docker compose up`, tables exist, `CREATE EXTENSION vector` OK.
2. **Ingestion API.** URL validation (https, `github.com` only, `owner/repo` shape, no shell, argv-list `git clone --depth 1`, timeout, size cap). Create repo + job rows. *Verify:* Go unit tests for URL validator (good/bad/injection strings); POST creates rows.
3. **Worker: clone + scan.** Poll job, clone to temp dir, walk files, skip `.git`, `node_modules`, vendor, lockfiles, binaries, huge files; map extension → language. Record commit SHA. *Verify:* run on a small public repo, print file counts by language.
4. **Worker: tree-sitter parse + chunk.** Per-language node types (Python `function_definition`/`class_definition`; Go `function_declaration`/`method_declaration`/type decls; TS/JS functions, classes, methods, arrow-function consts). Big symbols split on line boundaries with small overlap. Files with no symbols and docs (`.md`) use line-window chunks. Embedding text = header (path, symbol, language) + code. *Verify:* pytest with fixture files per language; assert symbol names and exact `start_line/end_line`; spot-check 5 chunks against the real file.
5. **Worker: embed + store.** `Embedder` interface, batched Gemini calls, retry/backoff on rate limit, idempotent write (delete chunks for `(repo_id, commit_sha)` before insert). Job status transitions + error message. *Verify:* index a small repo, row count > 0, all embeddings 768-dim, re-run does not duplicate.
6. **Go: retrieval.** `Embedder` interface (Go side, query task type), top-K cosine search scoped to `repo_id`. *Verify:* Go test with fake embedder; manual SQL sanity query returns relevant chunks for a known question.
7. **Go: RAG + citations.** Numbered context blocks `[n] path:start-end (symbol)`. System prompt: answer only from context, cite `[n]`, say "not enough evidence" otherwise, separate evidence from inference. Parse `[n]` from the answer, drop invalid refs, return file/line citations. `LLM` interface + Gemini adapter. *Verify:* unit tests with fake LLM (valid ref, invalid ref, no refs); live call on real question.
8. **Terminal-style UI.** One static page (`web/index.html`, `terminal.js`, `terminal.css`) served by Go. Dark monospace terminal with prompt `repopilot ~ <owner/name or (no repo)> ▸`, scrollback, blinking cursor, Up/Down command history (`localStorage` in try/catch). Commands:
   - `help [cmd]`, `clear`, `show <n>`: client-side.
   - `add <url>`: `POST /api/repositories`, then poll `GET /api/repositories/:id` every ~2s and print a line on each status/phase change until `ready` or `failed`.
   - `repos`: `GET /api/repositories`. `use <id|owner/name>` and `status [id]`: `GET /api/repositories/:id`.
   - `ask <question>` or bare text: `POST /api/repositories/:id/query`. Print answer, then `sources:` list `[n] path:start-end  symbol`. `show <n>` prints the snippet with line numbers. `grounded:false` prints a warning line. API errors print `error: <code>: <message>`.
   - All output set with `textContent`, never `innerHTML`.
   Command names and output formats are defined in `ARCHITECTURE.md` section 11.4 so the later CLI matches. *Verify:* run `help`, `add`, `status`, `use`, ask a question, `show 1`, ask an unanswerable question (warning/refusal shown), and confirm a snippet containing `<script>` renders as literal text. Plain static HTML/JS; React or xterm.js only if a later phase needs them.
9. **Demo pass.** Index 1 repo per language (small ones). Ask ~5 questions each. Record results and gaps in README.

### Phase 1 exit criteria
- In the terminal UI, `add <url>` → status goes queued → indexing → ready.
- Ask a question → grounded answer with file:line citations that open the correct code.
- Swapping LLM or embedder only touches its adapter file.
- Tests pass for URL validation, chunker (3 languages), citation validation.

## Later phases (each gets its own plan + verification gate)

- **Phase 2 — Retrieval quality:** Postgres full-text/BM25 + vector fusion (RRF), metadata filters (language, path), reranker (free/local cross-encoder or LLM-rerank), query rewriting, small eval set with Recall@K/MRR, Redis query cache, citation accuracy fixes. *Build the eval set first* so improvements are measurable.
- **Phase 3 — Contribution assistant:** GitHub Issues/PRs ingestion (API, rate limits), embed and link to code, "explain issue in repo context", relevant files/functions, similar past PRs, onboarding flow.
- **Phase 4 — Repo intelligence:** import/package dependency graph from tree-sitter, call relationships where feasible, overview generation, learning path, code-flow explanations.
- **Phase 5 — Production:** incremental Git-diff indexing, real job queue + retries, auth, rate limiting, Prometheus metrics/logging, eval dashboard, Docker/CI (GitHub Actions), perf tuning.

## Verification (end-to-end, Phase 1)

1. `docker compose up -d`, run migration, start Go API and Python worker.
2. `curl -X POST /api/repositories -d '{"url":"https://github.com/<small-repo>"}'`, poll status until `ready`.
3. `curl -X POST /api/repositories/:id/query -d '{"question":"Where is X handled?"}'`.
4. Open each citation's file at the cited lines; confirm the answer matches the code.
5. Ask an unanswerable question; confirm the system says evidence is missing instead of inventing.
6. `go test ./...` and `pytest` green.

## Risks / notes
- Gemini free-tier rate limits (LLM and embeddings share one quota): batch embeddings, backoff, keep demo repos small.
- Tree-sitter grammar packaging differs by language; pick one Python package that bundles all three grammars (verify at step 4).
- Embedding model must be identical for indexing and querying; `embed_model` column guards against silent mismatch.
- Never pass repo URL or paths through a shell.
