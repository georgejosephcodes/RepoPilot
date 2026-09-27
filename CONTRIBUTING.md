# Contributing to RepoPilot

Thanks for your interest. Bug reports, ideas and pull requests are all welcome.

## Before you start

- **Bugs and small fixes:** open a pull request directly, or an issue first if you are unsure.
- **Larger changes:** open an issue first. This covers a new retrieval method, a new provider, or a schema change. It lets us agree on the approach before you spend time on it.
- **Retrieval or prompt changes need numbers.** The evaluation set in `docs/phase2/eval.json` exists so that "better" can be shown rather than claimed (see [Evaluation](#evaluation)).

## Setup

Follow [Getting started](README.md#getting-started) in the README: Docker, Go, Python, and two free API keys. The unit tests need no keys and no database.

## Running the tests

```bash
cd api && gofmt -l . && go vet ./... && go test ./...
cd worker && .venv/bin/pytest -q
```

- **Database tests** run only when `TEST_DATABASE_URL` is set. Start the Compose database (`docker compose up -d`), then run with `TEST_DATABASE_URL=$DATABASE_URL`. The Go tests create and delete their own rows; the worker tests create and drop a throwaway database.
- **Web UI:** open `http://localhost:8080/#selftest` with the API running.
- **CI** runs all of the above, database tests included, on every pull request.

## Evaluation

Any change that can move retrieval quality should include before-and-after numbers on the **dev split**. That covers chunking, embeddings, keyword search, fusion, reranking and filters.

```bash
cd api && set -a && . ../.env && set +a
go run ./cmd/eval -variant hybrid-rerank -keyword-weight 0.75 -test-penalty 0.5 -rerank-depth 20 -split dev -dry-run
go run ./cmd/eval -variant hybrid-rerank -keyword-weight 0.75 -test-penalty 0.5 -rerank-depth 20 -split dev \
  -compare ../docs/phase2/runs/hybrid-rerank-dev-d20.json -report /tmp/my-change.md
```

- **The dry run** shows how many API requests a run will make. Question vectors and rankings are cached in Postgres, so repeat runs are free.
- **The report** lists every question that got better or worse. Paste the summary into the pull request.
- **Tune on dev only.** The test split (`-split test`, which needs `-final`) is held out so that its numbers stay honest.
- **Label corrections:** if you find a wrong label in `eval.json`, open an issue with the evidence (file and lines). Every run is then repeated with the corrected labels, and numbers from different label versions are never compared.

To index the evaluation repositories locally, add each repository from `eval.json` through the UI. Then check out the pinned commits in local clones, if you want to run `worker/scripts/run_answers.py`.

## Code style

- **Go:** `gofmt`, `go vet`, standard library first. Errors are typed and mapped to API errors in one place (`internal/rag/errors.go`). Tests use fakes, not real providers.
- **Python:** standard library first; the worker's dependencies are in `worker/requirements.txt`. Tests must not call real APIs.
- **JavaScript:** plain browser JavaScript with no build step. Pure logic goes in `api/web/core.js`, with self-test cases in `selftest.js`.
- **Comments** say *why*, not what.
- **Settings** go in `.env.example` with a comment and a default.
- **Schema changes** go in a new numbered file in `migrations/`, written to be safe to run twice. Existing files are never edited.

## Commits and pull requests

- Commit messages follow [Conventional Commits](https://www.conventionalcommits.org): `feat:`, `fix:`, `docs:`, `test:`, `refactor:`, `chore:`.
- Keep each pull request to one change. Say how you tested it; the template has a checklist.
- Never commit `.env` or API keys. If a key leaks, revoke it at the provider first.

## Providers and cost

The defaults use free tiers (OpenRouter or NVIDIA NIM for embeddings, Gemini for answers and reranking). Please do not add code paths that silently call paid models; a paid provider must be something the user turns on explicitly.

## License

By contributing, you agree that your contributions are licensed under the [MIT License](LICENSE).
