# Phase 2 results: retrieval quality, measured

Phase 1 retrieval was vector search only. Phase 2 replaced it with hybrid retrieval and then reranking:
- **Hybrid retrieval:** vector search plus BM25 keyword search, fused with weighted Reciprocal Rank Fusion, with a penalty for test files.
- **Reranking:** a list reranker (the answer model returns the order of the top 20 candidates).

Every parameter was chosen on a dev split. A held-out test split was run once, at the end. This page summarises the evidence; every number links to the run that produced it.

## Evaluation set

`eval.json` holds 74 questions over 4 pinned repositories: `golang/example`, `sindresorhus/is`, `pallets/itsdangerous` and `pallets/click`.

| | dev | test | total |
|---|---|---|---|
| questions | 46 | 28 | 74 |
| answerable (scored) | 42 | 24 | 66 |
| unanswerable (must be refused) | 4 | 4 | 8 |

- **Question kinds:** location, flow, identifier, literal string, conceptual, "trap" (near-duplicate code or a misleading name), and unanswerable.
- **Labels:** each answerable question has labelled line spans (grade 2 answers it, grade 1 is related). The labels were frozen before the baseline was recorded.
- **Metrics:** a retrieved chunk hits when it is in the same file as a labelled span and overlaps its lines. R@k asks whether an answering span is in the top k. MRR@20 is the mean of 1 / rank of the first answering hit.

## Headline

| split | n | configuration | R@1 | R@3 | R@8 | MRR@20 |
|---|---|---|---|---|---|---|
| **test** | 24 | vector (baseline) | 0.500 | 0.792 | 0.875 | 0.668 |
| **test** | 24 | **hybrid + rerank** | **0.875** | **0.958** | **1.000** | **0.920** |
| all | 66 | vector (baseline) | 0.530 | 0.803 | 0.894 | 0.677 |
| all | 66 | hybrid + rerank | 0.909 | 0.985 | 1.000 | 0.946 |

Sources: [`baseline.md`](baseline.md) and [`final-all.md`](final-all.md), which has the per-question table and the breakdown by repository and by kind.

- **First answering hit:** 28 questions better, 1 worse (`go-11`, rank 1 to 2), 37 unchanged.
- **Dev against test:** the test numbers are a little below dev (MRR 0.920 against 0.960, R@3 0.958 against 1.000). That is the expected cost of choosing settings on dev. The gain still holds on unseen questions.

## Gate

The gate was fixed before any result was seen.

| # | item | result | evidence |
|---|---|---|---|
| 1 | Eval set exists; labels frozen before the baseline | **pass** | labels sha256 `25b94b300dc8`, `labels_frozen: true` in every run |
| 2 | On test and on all, MRR@20 and R@3 beat the baseline, R@8 not worse | **pass** | test MRR 0.668 → 0.920, R@3 0.792 → 0.958, R@8 0.875 → 1.000 ([`final-all.md`](final-all.md)) |
| 3 | Every question lost from the baseline's top 8 is explained; wins outnumber losses | **pass** | none lost (R@8 is 1.000); 28 better, 1 worse |
| 4 | End to end: unanswerable questions refused; mechanical checks pass; no answer correct under vector retrieval turns wrong | **pass** | 8 of 8 refused; 73 of 74 checks pass (the one failure, `cl-4`, fails in both modes); 0 regressions to wrong ([`answers.md`](answers.md), [`verdicts.md`](verdicts.md)) |
| 5 | Median end-to-end time grows by at most 2.0 s | **fail, by 1 ms** | +2.001 s ([`answers.md`](answers.md)); see below |
| 6 | `go vet`, `go test ./...` and `pytest` green | **pass** | CI and local runs |

### End-to-end answers (gate item 4)

All 74 questions were asked through the running API twice: once with `RETRIEVAL_MODE=vector` (Phase 1 retrieval, proven identical to the baseline) and once with `hybrid_rerank`.

| | vector | hybrid + rerank |
|---|---|---|
| mechanical checks passed | 69 of 74 | 73 of 74 |
| unanswerable questions refused, no citations | 8 of 8 | 8 of 8 |

Verdicts were given to the 33 questions whose answers differ between the modes, or fail a check, plus 10 unchanged pairs:

| verdict | vector | hybrid + rerank |
|---|---|---|
| correct | 25 | 28 |
| partly correct | 2 | 2 |
| wrong | 0 | 0 |
| wrongly refused | 4 | 1 |
| correctly refused | 2 | 2 |

- **Refusals fixed:** three questions that vector retrieval refused are now answered correctly (`cl-5`, `ts-7`, `ts-12`).
- **Answers completed:** two partial answers became complete (`go-6`, `ts-9`).
- **Two answers got vaguer:** `cl-13` and `cl-18` dropped from correct to partly correct. Both still cite the right code.

### Latency (gate item 5): failed, and why

| | median | p90 |
|---|---|---|
| vector | 1.85 s | 27.1 s |
| hybrid + rerank (measured, plus the stored rerank time for cached rankings) | 3.85 s | 49.7 s |

The median grew by **2.001 s** against a limit of 2.0 s. This fails by 1 ms, and it is reported as a fail. The breakdown:
- **Retrieval overhead: +1.65 s median.** Almost all of it is the rerank call (median 1.63 s). Keyword search adds about 6 ms.
- **Answer model: +0.19 s median** (2.02 s against 1.83 s). Prompts are slightly longer (median 2,253 input tokens against 2,104). Both runs also happened during slow periods of the free-tier model: 16 and 12 answers took over 10 s, which is why both p90 values are so high.
- **Reranking is the main cost.** It could be cut by reranking fewer candidates (depth 10 had a median of 1.49 s but a lower MRR, see [`hybrid-rerank-dev-d10.md`](hybrid-rerank-dev-d10.md)), or with a faster dedicated reranker. `RETRIEVAL_MODE=hybrid` skips reranking entirely.

## What was tried and not kept

| idea | result | report |
|---|---|---|
| Keyword scoring with PostgreSQL `ts_rank` | BM25 ranked better on dev (MRR@20 0.624 against 0.383) | [`keyword-tsrank-dev.md`](keyword-tsrank-dev.md), [`keyword-bm25-dev.md`](keyword-bm25-dev.md) |
| Other hybrid settings (keyword weight 0.5 and 1, no test penalty, pool 50) | a 12-run grid on dev chose weight 0.75, penalty 0.5, pool 20 | [`hybrid-dev.md`](hybrid-dev.md), [`runs/`](runs/) |
| Rerank depth 10 | faster (1.49 s median), but MRR 0.904 against 0.960 on dev | [`hybrid-rerank-dev-d10.md`](hybrid-rerank-dev-d10.md) |
| Query rewriting (the model adds likely identifiers to the keyword search) | not kept: rewrite calls had a median of about 7.9 s, far above the 1.5 s rule | [`rewrite-dev.md`](rewrite-dev.md) |
| Dedicated rerank models through OpenRouter | a 6-question probe: none beat the list reranker, and most are paid | `worker/scripts/probe_rerank.py` |

## Known limits

- **A small evaluation set.** 24 test questions, so one question moves R@3 by about 0.04. Read every number with its n.
- **Method-level labels and split methods.** A long method is split into several chunks, and the labels cover the whole method, so any piece counts as a hit. In `cl-4`, retrieval ranked first the tail piece of `CliRunner.isolation`, which only restores the streams. The lines that replace `stdin`/`stdout` are in the other piece. The model then correctly said the evidence was missing. Fetching neighbouring chunks, or repeating the method signature in each piece, would help; that is an indexing change for later.
- **Free-tier providers.** The answer model and the reranker share one free-tier quota (500 requests per day, 15 per minute) and had slow periods during these runs. A rerank call that times out (10 s) falls back to the hybrid order, and the answer is still produced.
- **Rerank cost.** Each question makes two model calls (rerank and answer), so the per-client question limit defaults to 7 per minute.

## Reproducing

```bash
# retrieval metrics, cached vectors and rankings: 0 requests after the first run
cd api && go run ./cmd/eval -variant hybrid-rerank -keyword-weight 0.75 -test-penalty 0.5 -rerank-depth 20 -split dev

# end-to-end answers through a running API, per retrieval mode
python3 worker/scripts/run_answers.py --mode hybrid_rerank --clones /path/to/clones
```

The clones must be at the commits pinned in `eval.json`.
