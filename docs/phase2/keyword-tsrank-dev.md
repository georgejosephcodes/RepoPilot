# Retrieval evaluation: keyword-tsrank

- Split: **dev**, 46 questions (42 answerable, scored; unanswerable questions are listed separately)
- Embedding model: `nvidia/nemotron-3-embed-1b:free` (2048 dimensions); 20 chunks retrieved per question
- Labels: sha256 `25b94b300dc8` (frozen); code: `1beee01-dirty`; run at 2026-09-27T08:57:40Z
- Repositories: `golang/example@7f05d21`, `sindresorhus/is@e9c026c`, `pallets/itsdangerous@672971d`, `pallets/click@06b2a67`

A chunk hits a labelled span when the file is equal and the lines overlap. Recall@k: an answering (grade 2) span is in the top k. MRR@20: mean of 1/rank of the first answering hit. Coverage@8: the share of a question's answering spans found in the top 8. Small groups move a lot with one question; read every number with its n.

## Results

| group | n | R@1 | R@3 | R@5 | R@8 | MRR@20 | cov@8 |
|---|---|---|---|---|---|---|---|
| all | 42 | 0.286 | 0.452 | 0.476 | 0.548 | 0.383 | 0.536 |
| split: dev | 42 | 0.286 | 0.452 | 0.476 | 0.548 | 0.383 | 0.536 |
| repo: golang/example | 10 | 0.600 | 0.700 | 0.700 | 0.800 | 0.664 | 0.750 |
| repo: pallets/click | 13 | 0.077 | 0.308 | 0.308 | 0.385 | 0.189 | 0.385 |
| repo: pallets/itsdangerous | 9 | 0.222 | 0.444 | 0.556 | 0.667 | 0.358 | 0.667 |
| repo: sindresorhus/is | 10 | 0.300 | 0.400 | 0.400 | 0.400 | 0.378 | 0.400 |
| kind: conceptual | 4 | 0.750 | 0.750 | 0.750 | 0.750 | 0.771 | 0.750 |
| kind: flow | 9 | 0.222 | 0.444 | 0.556 | 0.667 | 0.367 | 0.611 |
| kind: identifier | 9 | 0.667 | 1.000 | 1.000 | 1.000 | 0.796 | 1.000 |
| kind: literal | 5 | 0.000 | 0.000 | 0.000 | 0.200 | 0.051 | 0.200 |
| kind: location | 10 | 0.100 | 0.200 | 0.200 | 0.300 | 0.174 | 0.300 |
| kind: trap | 5 | 0.000 | 0.200 | 0.200 | 0.200 | 0.111 | 0.200 |

## Change against `vector` (2026-09-27T08:48:14Z)

Each cell is this run minus the earlier run, over the questions both runs scored.

| group | n | ΔR@1 | ΔR@3 | ΔR@5 | ΔR@8 | ΔMRR@20 | Δcov@8 |
|---|---|---|---|---|---|---|---|
| all | 42 | -0.262 | -0.357 | -0.381 | -0.357 | -0.298 | -0.353 |
| split: dev | 42 | -0.262 | -0.357 | -0.381 | -0.357 | -0.298 | -0.353 |
| repo: golang/example | 10 | -0.200 | -0.200 | -0.200 | -0.200 | -0.186 | -0.233 |
| repo: pallets/click | 13 | -0.308 | -0.385 | -0.538 | -0.462 | -0.349 | -0.423 |
| repo: pallets/itsdangerous | 9 | -0.556 | -0.444 | -0.333 | -0.333 | -0.491 | -0.333 |
| repo: sindresorhus/is | 10 | 0 | -0.400 | -0.400 | -0.400 | -0.171 | -0.400 |
| kind: conceptual | 4 | -0.250 | -0.250 | -0.250 | -0.250 | -0.229 | -0.250 |
| kind: flow | 9 | -0.111 | -0.333 | -0.222 | -0.222 | -0.199 | -0.278 |
| kind: identifier | 9 | 0 | 0 | 0 | 0 | 0 | 0 |
| kind: literal | 5 | -0.600 | -0.800 | -0.800 | -0.600 | -0.636 | -0.600 |
| kind: location | 10 | -0.300 | -0.400 | -0.500 | -0.500 | -0.348 | -0.483 |
| kind: trap | 5 | -0.600 | -0.600 | -0.800 | -0.800 | -0.629 | -0.700 |

First answering hit: **3 better, 25 worse, 14 unchanged**.

- Better: ts-10 (2 → 1), ts-11 (3 → 1), cl-8 (2 → 1)
- Worse: go-6 (3 → 11), go-12 (1 → 20), go-16 (1 → 3), ts-1 (1 → none), ts-6 (2 → 10), ts-7 (19 → none), ts-8 (1 → 10), ts-12 (10 → none), ts-16 (2 → 13), py-1 (1 → 13), py-3 (2 → 3), py-7 (1 → 5), py-9 (1 → 3), py-12 (1 → 16), py-15 (1 → 13), cl-1 (5 → none), cl-3 (1 → 3), cl-5 (11 → none), cl-7 (3 → none), cl-9 (1 → 2), cl-12 (3 → none), cl-14 (1 → 7), cl-15 (1 → 12), cl-17 (1 → 15), cl-18 (5 → none)

## Per question

| id | repo | kind | split | first hit | R@8 | cov@8 | top result |
|---|---|---|---|---|---|---|---|
| go-1 | golang/example | location | dev | 1 | yes | 1.00 | `hello/reverse/reverse.go:8-15` |
| go-3 | golang/example | flow | dev | 1 | yes | 1.00 | `ragserver/ragserver-langchaingo/main.go:105-139` |
| go-5 | golang/example | identifier | dev | 1 | yes | 1.00 | `gotypes/defsuses/main.go:22-43` |
| go-6 | golang/example | location | dev | 11 | no | 0.00 | `outyet/main.go:97-115` |
| go-7 | golang/example | flow | dev | 6 | yes | 0.50 | `gotypes/go-types.md:2-6` |
| go-9 | golang/example | identifier | dev | 1 | yes | 1.00 | `gotypes/nilfunc/main.go:53-92` |
| go-11 | golang/example | identifier | dev | 1 | yes | 1.00 | `gotypes/skeleton/main.go:28-59` |
| go-12 | golang/example | literal | dev | 20 | no | 0.00 | `ragserver/ragserver/main.go:124-192` |
| go-14 | golang/example | conceptual | dev | 1 | yes | 1.00 | `ragserver/README.md:30-37` |
| go-16 | golang/example | trap | dev | 3 | yes | 1.00 | `slog-handler-guide/indenthandler1/indent_handler.go:50-53` |
| ts-1 | sindresorhus/is | location | dev | none | no | 0.00 | `source/index.ts:1028-1147` |
| ts-3 | sindresorhus/is | flow | dev | 2 | yes | 1.00 | `source/index.ts:416-425` |
| ts-6 | sindresorhus/is | location | dev | 10 | no | 0.00 | `source/index.ts:1028-1147` |
| ts-7 | sindresorhus/is | location | dev | none | no | 0.00 | `test/test.ts:1124-1243` |
| ts-8 | sindresorhus/is | flow | dev | 10 | no | 0.00 | `source/index.ts:1028-1147` |
| ts-10 | sindresorhus/is | identifier | dev | 1 | yes | 1.00 | `source/index.ts:831-841` |
| ts-11 | sindresorhus/is | identifier | dev | 1 | yes | 1.00 | `source/index.ts:679-693` |
| ts-12 | sindresorhus/is | literal | dev | none | no | 0.00 | `test/test.ts:2334-2453` |
| ts-14 | sindresorhus/is | conceptual | dev | 1 | yes | 1.00 | `readme.md:903-905` |
| ts-16 | sindresorhus/is | trap | dev | 13 | no | 0.00 | `source/index.ts:1028-1147` |
| py-1 | pallets/itsdangerous | location | dev | 13 | no | 0.00 | `src/itsdangerous/signer.py:215-220` |
| py-3 | pallets/itsdangerous | flow | dev | 3 | yes | 1.00 | `tests/test_itsdangerous/test_timed.py:29-93` |
| py-6 | pallets/itsdangerous | location | dev | 7 | yes | 1.00 | `src/itsdangerous/exc.py:36-57` |
| py-7 | pallets/itsdangerous | flow | dev | 5 | yes | 1.00 | `src/itsdangerous/serializer.py:243-269` |
| py-9 | pallets/itsdangerous | identifier | dev | 3 | yes | 1.00 | `src/itsdangerous/signer.py:227-242` |
| py-10 | pallets/itsdangerous | identifier | dev | 1 | yes | 1.00 | `src/itsdangerous/signer.py:182-213` |
| py-12 | pallets/itsdangerous | literal | dev | 16 | no | 0.00 | `tests/test_itsdangerous/test_timed.py:29-93` |
| py-13 | pallets/itsdangerous | conceptual | dev | 1 | yes | 1.00 | `README.md:16-32` |
| py-15 | pallets/itsdangerous | trap | dev | 13 | no | 0.00 | `tests/test_itsdangerous/test_serializer.py:145-153` |
| cl-1 | pallets/click | location | dev | none | no | 0.00 | `tests/test_deprecations.py:13-51` |
| cl-3 | pallets/click | location | dev | 3 | yes | 1.00 | `tests/test_options.py:1977-2074` |
| cl-4 | pallets/click | location | dev | none | no | 0.00 | `tests/test_utils/test_echo_via_pager.py:55-174` |
| cl-5 | pallets/click | flow | dev | none | no | 0.00 | `docs/command-line-reference.md:1-13` |
| cl-7 | pallets/click | flow | dev | none | no | 0.00 | `src/click/shell_completion.py:591-592` |
| cl-8 | pallets/click | flow | dev | 1 | yes | 1.00 | `src/click/termui.py:168-286` |
| cl-9 | pallets/click | identifier | dev | 2 | yes | 1.00 | `tests/test_utils/test_make_default_short_help.py:6-63` |
| cl-11 | pallets/click | identifier | dev | 3 | yes | 1.00 | `tests/test_context.py:730-774` |
| cl-12 | pallets/click | literal | dev | none | no | 0.00 | `tests/test_arguments.py:992-1067` |
| cl-14 | pallets/click | literal | dev | 7 | yes | 1.00 | `src/click/shell_completion.py:278-386` |
| cl-15 | pallets/click | conceptual | dev | 12 | no | 0.00 | `tests/test_deprecations.py:13-51` |
| cl-17 | pallets/click | trap | dev | 15 | no | 0.00 | `tests/test_options.py:1778-1887` |
| cl-18 | pallets/click | trap | dev | none | no | 0.00 | `tests/test_basic.py:418-436` |

## Unanswerable questions

Not scored. The distance of the nearest chunk is recorded as data for a possible refusal threshold.

| id | repo | split | top distance | top result |
|---|---|---|---|---|
| go-4 | golang/example | dev | 0.0000 | `outyet/main.go:46-56` |
| ts-4 | sindresorhus/is | dev | 0.0000 | (nothing retrieved) |
| py-4 | pallets/itsdangerous | dev | 0.0000 | `src/itsdangerous/timed.py:22-27` |
| cl-20 | pallets/click | dev | 0.0000 | `tests/test_deprecations.py:13-51` |
