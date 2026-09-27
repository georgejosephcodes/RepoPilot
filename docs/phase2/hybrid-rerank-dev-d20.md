# Retrieval evaluation: hybrid-rerank

- Split: **dev**, 46 questions (42 answerable, scored; unanswerable questions are listed separately)
- Embedding model: `nvidia/nemotron-3-embed-1b` (2048 dimensions); 20 chunks retrieved per question
- Settings: keyword_weight 0.75, pool 20, rerank_depth 20, rrf_k 60, test_penalty 0.5
- Rerank: `rerank-v1/gemini-3.5-flash-lite`, depth 20; 0 upstream call(s) this run, 46 from cache; 0 fallback(s); rerank time (measured when each ranking was made) median 1690 ms, p90 2161 ms, max 2422 ms
- Labels: sha256 `25b94b300dc8` (frozen); code: `662af1d`; run at 2026-09-27T10:11:29Z
- Repositories: `golang/example@7f05d21`, `sindresorhus/is@e9c026c`, `pallets/itsdangerous@672971d`, `pallets/click@06b2a67`

A chunk hits a labelled span when the file is equal and the lines overlap. Recall@k: an answering (grade 2) span is in the top k. MRR@20: mean of 1/rank of the first answering hit. Coverage@8: the share of a question's answering spans found in the top 8. Small groups move a lot with one question; read every number with its n.

## Results

| group | n | R@1 | R@3 | R@5 | R@8 | MRR@20 | cov@8 |
|---|---|---|---|---|---|---|---|
| all | 42 | 0.929 | 1.000 | 1.000 | 1.000 | 0.960 | 0.976 |
| split: dev | 42 | 0.929 | 1.000 | 1.000 | 1.000 | 0.960 | 0.976 |
| repo: golang/example | 10 | 0.900 | 1.000 | 1.000 | 1.000 | 0.950 | 1.000 |
| repo: pallets/click | 13 | 0.923 | 1.000 | 1.000 | 1.000 | 0.949 | 0.923 |
| repo: pallets/itsdangerous | 9 | 1.000 | 1.000 | 1.000 | 1.000 | 1.000 | 1.000 |
| repo: sindresorhus/is | 10 | 0.900 | 1.000 | 1.000 | 1.000 | 0.950 | 1.000 |
| kind: conceptual | 4 | 1.000 | 1.000 | 1.000 | 1.000 | 1.000 | 1.000 |
| kind: flow | 9 | 1.000 | 1.000 | 1.000 | 1.000 | 1.000 | 0.944 |
| kind: identifier | 9 | 0.889 | 1.000 | 1.000 | 1.000 | 0.944 | 1.000 |
| kind: literal | 5 | 1.000 | 1.000 | 1.000 | 1.000 | 1.000 | 1.000 |
| kind: location | 10 | 1.000 | 1.000 | 1.000 | 1.000 | 1.000 | 1.000 |
| kind: trap | 5 | 0.600 | 1.000 | 1.000 | 1.000 | 0.767 | 0.900 |

## Change against `vector` (2026-09-27T08:48:14Z)

Each cell is this run minus the earlier run, over the questions both runs scored.

| group | n | ΔR@1 | ΔR@3 | ΔR@5 | ΔR@8 | ΔMRR@20 | Δcov@8 |
|---|---|---|---|---|---|---|---|
| all | 42 | +0.381 | +0.190 | +0.143 | +0.095 | +0.279 | +0.087 |
| split: dev | 42 | +0.381 | +0.190 | +0.143 | +0.095 | +0.279 | +0.087 |
| repo: golang/example | 10 | +0.100 | +0.100 | +0.100 | 0 | +0.100 | +0.017 |
| repo: pallets/click | 13 | +0.538 | +0.308 | +0.154 | +0.154 | +0.411 | +0.115 |
| repo: pallets/itsdangerous | 9 | +0.222 | +0.111 | +0.111 | 0 | +0.151 | 0 |
| repo: sindresorhus/is | 10 | +0.600 | +0.200 | +0.200 | +0.200 | +0.401 | +0.200 |
| kind: conceptual | 4 | 0 | 0 | 0 | 0 | 0 | 0 |
| kind: flow | 9 | +0.667 | +0.222 | +0.222 | +0.111 | +0.434 | +0.056 |
| kind: identifier | 9 | +0.222 | 0 | 0 | 0 | +0.148 | 0 |
| kind: literal | 5 | +0.400 | +0.200 | +0.200 | +0.200 | +0.313 | +0.200 |
| kind: location | 10 | +0.600 | +0.400 | +0.300 | +0.200 | +0.477 | +0.217 |
| kind: trap | 5 | 0 | +0.200 | 0 | 0 | +0.027 | 0 |

First answering hit: **18 better, 1 worse, 23 unchanged**.

- Better: go-6 (3 → 1), go-7 (6 → 1), ts-3 (2 → 1), ts-6 (2 → 1), ts-7 (19 → 1), ts-10 (2 → 1), ts-11 (3 → 1), ts-12 (10 → 1), py-3 (2 → 1), py-6 (7 → 1), cl-1 (5 → 1), cl-4 (none → 1), cl-5 (11 → 1), cl-7 (3 → 1), cl-8 (2 → 1), cl-11 (3 → 1), cl-12 (3 → 1), cl-18 (5 → 3)
- Worse: go-11 (1 → 2)

## Per question

| id | repo | kind | split | first hit | R@8 | cov@8 | top result |
|---|---|---|---|---|---|---|---|
| go-1 | golang/example | location | dev | 1 | yes | 1.00 | `hello/reverse/reverse.go:8-15` |
| go-3 | golang/example | flow | dev | 1 | yes | 1.00 | `ragserver/ragserver/main.go:124-192` |
| go-5 | golang/example | identifier | dev | 1 | yes | 1.00 | `gotypes/defsuses/main.go:22-43` |
| go-6 | golang/example | location | dev | 1 | yes | 1.00 | `ragserver/ragserver/main.go:29-61` |
| go-7 | golang/example | flow | dev | 1 | yes | 1.00 | `outyet/main.go:83-95` |
| go-9 | golang/example | identifier | dev | 1 | yes | 1.00 | `gotypes/nilfunc/main.go:53-92` |
| go-11 | golang/example | identifier | dev | 2 | yes | 1.00 | `gotypes/skeleton/main.go:1-26` |
| go-12 | golang/example | literal | dev | 1 | yes | 1.00 | `helloserver/server.go:56-65` |
| go-14 | golang/example | conceptual | dev | 1 | yes | 1.00 | `ragserver/README.md:30-37` |
| go-16 | golang/example | trap | dev | 1 | yes | 1.00 | `slog-handler-guide/indenthandler3/indent_handler.go:50-61` |
| ts-1 | sindresorhus/is | location | dev | 1 | yes | 1.00 | `source/index.ts:195-259` |
| ts-3 | sindresorhus/is | flow | dev | 1 | yes | 1.00 | `source/index.ts:426-440` |
| ts-6 | sindresorhus/is | location | dev | 1 | yes | 1.00 | `source/index.ts:666-672` |
| ts-7 | sindresorhus/is | location | dev | 1 | yes | 1.00 | `source/index.ts:282-385` |
| ts-8 | sindresorhus/is | flow | dev | 1 | yes | 1.00 | `source/index.ts:1404-1414` |
| ts-10 | sindresorhus/is | identifier | dev | 1 | yes | 1.00 | `source/index.ts:831-841` |
| ts-11 | sindresorhus/is | identifier | dev | 1 | yes | 1.00 | `source/index.ts:679-693` |
| ts-12 | sindresorhus/is | literal | dev | 1 | yes | 1.00 | `source/index.ts:195-259` |
| ts-14 | sindresorhus/is | conceptual | dev | 1 | yes | 1.00 | `readme.md:903-905` |
| ts-16 | sindresorhus/is | trap | dev | 2 | yes | 1.00 | `readme.md:378-392` |
| py-1 | pallets/itsdangerous | location | dev | 1 | yes | 1.00 | `src/itsdangerous/signer.py:48-64` |
| py-3 | pallets/itsdangerous | flow | dev | 1 | yes | 1.00 | `src/itsdangerous/timed.py:72-158` |
| py-6 | pallets/itsdangerous | location | dev | 1 | yes | 1.00 | `src/itsdangerous/signer.py:15-28` |
| py-7 | pallets/itsdangerous | flow | dev | 1 | yes | 1.00 | `src/itsdangerous/url_safe.py:15-69` |
| py-9 | pallets/itsdangerous | identifier | dev | 1 | yes | 1.00 | `src/itsdangerous/serializer.py:367-395` |
| py-10 | pallets/itsdangerous | identifier | dev | 1 | yes | 1.00 | `src/itsdangerous/signer.py:182-213` |
| py-12 | pallets/itsdangerous | literal | dev | 1 | yes | 1.00 | `src/itsdangerous/url_safe.py:15-69` |
| py-13 | pallets/itsdangerous | conceptual | dev | 1 | yes | 1.00 | `README.md:16-32` |
| py-15 | pallets/itsdangerous | trap | dev | 1 | yes | 1.00 | `src/itsdangerous/timed.py:170-228` |
| cl-1 | pallets/click | location | dev | 1 | yes | 1.00 | `src/click/formatting.py:127-144` |
| cl-3 | pallets/click | location | dev | 1 | yes | 1.00 | `src/click/_termui_impl.py:250-294` |
| cl-4 | pallets/click | location | dev | 1 | yes | 1.00 | `src/click/testing.py:508-594` |
| cl-5 | pallets/click | flow | dev | 1 | yes | 0.50 | `src/click/core.py:2048-2114` |
| cl-7 | pallets/click | flow | dev | 1 | yes | 1.00 | `src/click/core.py:1647-1677` |
| cl-8 | pallets/click | flow | dev | 1 | yes | 1.00 | `src/click/termui.py:168-286` |
| cl-9 | pallets/click | identifier | dev | 1 | yes | 1.00 | `src/click/utils.py:62-100` |
| cl-11 | pallets/click | identifier | dev | 1 | yes | 1.00 | `src/click/core.py:195-231` |
| cl-12 | pallets/click | literal | dev | 1 | yes | 1.00 | `src/click/core.py:1392-1426` |
| cl-14 | pallets/click | literal | dev | 1 | yes | 1.00 | `src/click/core.py:1647-1677` |
| cl-15 | pallets/click | conceptual | dev | 1 | yes | 1.00 | `docs/why.md:98-106` |
| cl-17 | pallets/click | trap | dev | 1 | yes | 1.00 | `src/click/types.py:478-490` |
| cl-18 | pallets/click | trap | dev | 3 | yes | 0.50 | `docs/handling-files.md:81-101` |

## Unanswerable questions

Not scored. The distance of the nearest chunk is recorded as data for a possible refusal threshold.

| id | repo | split | top distance | top result |
|---|---|---|---|---|
| go-4 | golang/example | dev | 0.7599 | `outyet/main.go:58-63` |
| ts-4 | sindresorhus/is | dev | 0.8873 | `readme.md:907-918` |
| py-4 | pallets/itsdangerous | dev | 0.6608 | `src/itsdangerous/timed.py:22-27` |
| cl-20 | pallets/click | dev | 0.6492 | `src/click/core.py:1184-1190` |
