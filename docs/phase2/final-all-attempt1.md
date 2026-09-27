# Retrieval evaluation: hybrid-rerank

- Split: **all**, 74 questions (66 answerable, scored; unanswerable questions are listed separately)
- Embedding model: `nvidia/nemotron-3-embed-1b` (2048 dimensions); 20 chunks retrieved per question
- Settings: keyword_weight 0.75, pool 20, rerank_depth 20, rrf_k 60, test_penalty 0.5
- Rerank: `rerank-v1/gemini-3.5-flash-lite`, depth 20; 13 upstream call(s) this run, 61 from cache; 1 fallback(s) (timeout 1); rerank time (measured when each ranking was made) median 1624 ms, p90 2143 ms, max 10005 ms
- Labels: sha256 `25b94b300dc8` (frozen); code: `1c5fe4d`; run at 2026-09-27T14:36:28Z
- Repositories: `golang/example@7f05d21`, `sindresorhus/is@e9c026c`, `pallets/itsdangerous@672971d`, `pallets/click@06b2a67`

A chunk hits a labelled span when the file is equal and the lines overlap. Recall@k: an answering (grade 2) span is in the top k. MRR@20: mean of 1/rank of the first answering hit. Coverage@8: the share of a question's answering spans found in the top 8. Small groups move a lot with one question; read every number with its n.

## Results

| group | n | R@1 | R@3 | R@5 | R@8 | MRR@20 | cov@8 |
|---|---|---|---|---|---|---|---|
| all | 66 | 0.909 | 0.985 | 1.000 | 1.000 | 0.946 | 0.970 |
| split: dev | 42 | 0.929 | 1.000 | 1.000 | 1.000 | 0.960 | 0.976 |
| split: test | 24 | 0.875 | 0.958 | 1.000 | 1.000 | 0.920 | 0.958 |
| repo: golang/example | 16 | 0.875 | 1.000 | 1.000 | 1.000 | 0.938 | 1.000 |
| repo: pallets/click | 19 | 0.895 | 1.000 | 1.000 | 1.000 | 0.930 | 0.947 |
| repo: pallets/itsdangerous | 15 | 0.933 | 0.933 | 1.000 | 1.000 | 0.950 | 0.967 |
| repo: sindresorhus/is | 16 | 0.938 | 1.000 | 1.000 | 1.000 | 0.969 | 0.969 |
| kind: conceptual | 8 | 1.000 | 1.000 | 1.000 | 1.000 | 1.000 | 1.000 |
| kind: flow | 13 | 0.846 | 0.923 | 1.000 | 1.000 | 0.891 | 0.885 |
| kind: identifier | 13 | 0.923 | 1.000 | 1.000 | 1.000 | 0.962 | 1.000 |
| kind: literal | 9 | 1.000 | 1.000 | 1.000 | 1.000 | 1.000 | 1.000 |
| kind: location | 14 | 1.000 | 1.000 | 1.000 | 1.000 | 1.000 | 1.000 |
| kind: trap | 9 | 0.667 | 1.000 | 1.000 | 1.000 | 0.815 | 0.944 |

## Change against `vector` (2026-09-27T08:48:14Z)

Each cell is this run minus the earlier run, over the questions both runs scored.

| group | n | ΔR@1 | ΔR@3 | ΔR@5 | ΔR@8 | ΔMRR@20 | Δcov@8 |
|---|---|---|---|---|---|---|---|
| all | 66 | +0.379 | +0.182 | +0.136 | +0.106 | +0.269 | +0.093 |
| split: dev | 42 | +0.381 | +0.190 | +0.143 | +0.095 | +0.279 | +0.087 |
| split: test | 24 | +0.375 | +0.167 | +0.125 | +0.125 | +0.252 | +0.104 |
| repo: golang/example | 16 | +0.188 | +0.125 | +0.062 | 0 | +0.144 | +0.010 |
| repo: pallets/click | 19 | +0.474 | +0.316 | +0.158 | +0.158 | +0.370 | +0.132 |
| repo: pallets/itsdangerous | 15 | +0.200 | +0.067 | +0.133 | +0.067 | +0.133 | +0.033 |
| repo: sindresorhus/is | 16 | +0.625 | +0.188 | +0.188 | +0.188 | +0.401 | +0.188 |
| kind: conceptual | 8 | +0.250 | +0.125 | +0.125 | +0.125 | +0.181 | +0.125 |
| kind: flow | 13 | +0.615 | +0.308 | +0.308 | +0.231 | +0.443 | +0.115 |
| kind: identifier | 13 | +0.154 | 0 | 0 | 0 | +0.103 | 0 |
| kind: literal | 9 | +0.444 | +0.111 | +0.111 | +0.111 | +0.285 | +0.111 |
| kind: location | 14 | +0.500 | +0.286 | +0.214 | +0.143 | +0.377 | +0.155 |
| kind: trap | 9 | +0.222 | +0.222 | 0 | 0 | +0.154 | +0.056 |

First answering hit: **28 better, 1 worse, 37 unchanged**.

- Better: go-6 (3 → 1), go-7 (6 → 1), go-8 (5 → 1), go-13 (2 → 1), ts-2 (2 → 1), ts-3 (2 → 1), ts-6 (2 → 1), ts-7 (19 → 1), ts-9 (11 → 1), ts-10 (2 → 1), ts-11 (3 → 1), ts-12 (10 → 1), ts-15 (2 → 1), ts-17 (2 → 1), py-3 (2 → 1), py-6 (7 → 1), py-8 (9 → 4), py-11 (2 → 1), cl-1 (5 → 1), cl-4 (none → 1), cl-5 (11 → 1), cl-7 (3 → 1), cl-8 (2 → 1), cl-11 (3 → 1), cl-12 (3 → 1), cl-16 (18 → 1), cl-18 (5 → 3), cl-19 (4 → 1)
- Worse: go-11 (1 → 2)

## Per question

| id | repo | kind | split | first hit | R@8 | cov@8 | top result |
|---|---|---|---|---|---|---|---|
| go-1 | golang/example | location | dev | 1 | yes | 1.00 | `hello/reverse/reverse.go:8-15` |
| go-2 | golang/example | location | test | 1 | yes | 1.00 | `ragserver/ragserver/weaviate.go:19-46` |
| go-3 | golang/example | flow | dev | 1 | yes | 1.00 | `ragserver/ragserver/main.go:124-192` |
| go-5 | golang/example | identifier | dev | 1 | yes | 1.00 | `gotypes/defsuses/main.go:22-43` |
| go-6 | golang/example | location | dev | 1 | yes | 1.00 | `ragserver/ragserver/main.go:29-61` |
| go-7 | golang/example | flow | dev | 1 | yes | 1.00 | `outyet/main.go:83-95` |
| go-8 | golang/example | flow | test | 1 | yes | 1.00 | `hello/hello.go:44-72` |
| go-9 | golang/example | identifier | dev | 1 | yes | 1.00 | `gotypes/nilfunc/main.go:53-92` |
| go-10 | golang/example | identifier | test | 1 | yes | 1.00 | `slog-handler-guide/indenthandler3/indent_handler.go:86-92` |
| go-11 | golang/example | identifier | dev | 2 | yes | 1.00 | `gotypes/skeleton/main.go:1-26` |
| go-12 | golang/example | literal | dev | 1 | yes | 1.00 | `helloserver/server.go:56-65` |
| go-13 | golang/example | literal | test | 1 | yes | 1.00 | `internal/cmd/weave/weave.go:1-40` |
| go-14 | golang/example | conceptual | dev | 1 | yes | 1.00 | `ragserver/README.md:30-37` |
| go-15 | golang/example | conceptual | test | 1 | yes | 1.00 | `slog-handler-guide/guide.md:341-365` |
| go-16 | golang/example | trap | dev | 1 | yes | 1.00 | `slog-handler-guide/indenthandler3/indent_handler.go:50-61` |
| go-17 | golang/example | trap | test | 2 | yes | 1.00 | `slog-handler-guide/indenthandler4/indent_handler.go:97-136` |
| ts-1 | sindresorhus/is | location | dev | 1 | yes | 1.00 | `source/index.ts:195-259` |
| ts-2 | sindresorhus/is | location | test | 1 | yes | 1.00 | `source/index.ts:586-588` |
| ts-3 | sindresorhus/is | flow | dev | 1 | yes | 1.00 | `source/index.ts:426-440` |
| ts-5 | sindresorhus/is | identifier | test | 1 | yes | 1.00 | `source/index.ts:387-389` |
| ts-6 | sindresorhus/is | location | dev | 1 | yes | 1.00 | `source/index.ts:666-672` |
| ts-7 | sindresorhus/is | location | dev | 1 | yes | 1.00 | `source/index.ts:282-385` |
| ts-8 | sindresorhus/is | flow | dev | 1 | yes | 1.00 | `source/index.ts:1404-1414` |
| ts-9 | sindresorhus/is | flow | test | 1 | yes | 0.50 | `source/index.ts:1167-1174` |
| ts-10 | sindresorhus/is | identifier | dev | 1 | yes | 1.00 | `source/index.ts:831-841` |
| ts-11 | sindresorhus/is | identifier | dev | 1 | yes | 1.00 | `source/index.ts:679-693` |
| ts-12 | sindresorhus/is | literal | dev | 1 | yes | 1.00 | `source/index.ts:195-259` |
| ts-13 | sindresorhus/is | literal | test | 1 | yes | 1.00 | `source/index.ts:1404-1414` |
| ts-14 | sindresorhus/is | conceptual | dev | 1 | yes | 1.00 | `readme.md:903-905` |
| ts-15 | sindresorhus/is | conceptual | test | 1 | yes | 1.00 | `readme.md:762-787` |
| ts-16 | sindresorhus/is | trap | dev | 2 | yes | 1.00 | `readme.md:378-392` |
| ts-17 | sindresorhus/is | trap | test | 1 | yes | 1.00 | `source/index.ts:823-825` |
| py-1 | pallets/itsdangerous | location | dev | 1 | yes | 1.00 | `src/itsdangerous/signer.py:48-64` |
| py-2 | pallets/itsdangerous | location | test | 1 | yes | 1.00 | `src/itsdangerous/timed.py:35-43` |
| py-3 | pallets/itsdangerous | flow | dev | 1 | yes | 1.00 | `src/itsdangerous/timed.py:72-158` |
| py-5 | pallets/itsdangerous | identifier | test | 1 | yes | 1.00 | `src/itsdangerous/encoding.py:11-17` |
| py-6 | pallets/itsdangerous | location | dev | 1 | yes | 1.00 | `src/itsdangerous/signer.py:15-28` |
| py-7 | pallets/itsdangerous | flow | dev | 1 | yes | 1.00 | `src/itsdangerous/url_safe.py:15-69` |
| py-8 | pallets/itsdangerous | flow | test | 4 | yes | 0.50 | `src/itsdangerous/signer.py:76-127` |
| py-9 | pallets/itsdangerous | identifier | dev | 1 | yes | 1.00 | `src/itsdangerous/serializer.py:367-395` |
| py-10 | pallets/itsdangerous | identifier | dev | 1 | yes | 1.00 | `src/itsdangerous/signer.py:182-213` |
| py-11 | pallets/itsdangerous | literal | test | 1 | yes | 1.00 | `src/itsdangerous/signer.py:129-173` |
| py-12 | pallets/itsdangerous | literal | dev | 1 | yes | 1.00 | `src/itsdangerous/url_safe.py:15-69` |
| py-13 | pallets/itsdangerous | conceptual | dev | 1 | yes | 1.00 | `README.md:16-32` |
| py-14 | pallets/itsdangerous | conceptual | test | 1 | yes | 1.00 | `src/itsdangerous/signer.py:40-45` |
| py-15 | pallets/itsdangerous | trap | dev | 1 | yes | 1.00 | `src/itsdangerous/timed.py:170-228` |
| py-16 | pallets/itsdangerous | trap | test | 1 | yes | 1.00 | `src/itsdangerous/signer.py:31-37` |
| cl-1 | pallets/click | location | dev | 1 | yes | 1.00 | `src/click/formatting.py:127-144` |
| cl-2 | pallets/click | location | test | 1 | yes | 1.00 | `src/click/core.py:3654-3676` |
| cl-3 | pallets/click | location | dev | 1 | yes | 1.00 | `src/click/_termui_impl.py:250-294` |
| cl-4 | pallets/click | location | dev | 1 | yes | 1.00 | `src/click/testing.py:508-594` |
| cl-5 | pallets/click | flow | dev | 1 | yes | 0.50 | `src/click/core.py:2048-2114` |
| cl-6 | pallets/click | flow | test | 3 | yes | 1.00 | `docs/options.md:608-628` |
| cl-7 | pallets/click | flow | dev | 1 | yes | 1.00 | `src/click/core.py:1647-1677` |
| cl-8 | pallets/click | flow | dev | 1 | yes | 1.00 | `src/click/termui.py:168-286` |
| cl-9 | pallets/click | identifier | dev | 1 | yes | 1.00 | `src/click/utils.py:62-100` |
| cl-10 | pallets/click | identifier | test | 1 | yes | 1.00 | `src/click/decorators.py:51-97` |
| cl-11 | pallets/click | identifier | dev | 1 | yes | 1.00 | `src/click/core.py:195-231` |
| cl-12 | pallets/click | literal | dev | 1 | yes | 1.00 | `src/click/core.py:1392-1426` |
| cl-13 | pallets/click | literal | test | 1 | yes | 1.00 | `src/click/core.py:103-105` |
| cl-14 | pallets/click | literal | dev | 1 | yes | 1.00 | `src/click/core.py:1647-1677` |
| cl-15 | pallets/click | conceptual | dev | 1 | yes | 1.00 | `docs/why.md:98-106` |
| cl-16 | pallets/click | conceptual | test | 1 | yes | 1.00 | `docs/testing.md:24-54` |
| cl-17 | pallets/click | trap | dev | 1 | yes | 1.00 | `src/click/types.py:478-490` |
| cl-18 | pallets/click | trap | dev | 3 | yes | 0.50 | `docs/handling-files.md:81-101` |
| cl-19 | pallets/click | trap | test | 1 | yes | 1.00 | `src/click/_termui_impl.py:204-221` |

## Unanswerable questions

Not scored. The distance of the nearest chunk is recorded as data for a possible refusal threshold.

| id | repo | split | top distance | top result |
|---|---|---|---|---|
| go-4 | golang/example | dev | 0.7599 | `outyet/main.go:58-63` |
| go-18 | golang/example | test | 0.8424 | `ragserver/ragserver/weaviate.go:19-46` |
| ts-4 | sindresorhus/is | dev | 0.8873 | `readme.md:907-918` |
| ts-18 | sindresorhus/is | test | 0.7057 | `source/index.ts:1007-1007` |
| py-4 | pallets/itsdangerous | dev | 0.6608 | `src/itsdangerous/timed.py:22-27` |
| py-17 | pallets/itsdangerous | test | 0.6552 | `src/itsdangerous/signer.py:76-127` |
| cl-20 | pallets/click | dev | 0.6492 | `src/click/core.py:1184-1190` |
| cl-21 | pallets/click | test | 0.6059 | `docs/quickstart.md:6-14` |
