# Retrieval evaluation: vector

- Split: **all**, 74 questions (66 answerable, scored; unanswerable questions are listed separately)
- Embedding model: `nvidia/nemotron-3-embed-1b:free` (2048 dimensions); 20 chunks retrieved per question
- Labels: sha256 `25b94b300dc8` (frozen); code: `2445060`; run at 2026-09-27T08:48:14Z
- Repositories: `golang/example@7f05d21`, `sindresorhus/is@e9c026c`, `pallets/itsdangerous@672971d`, `pallets/click@06b2a67`

A chunk hits a labelled span when the file is equal and the lines overlap. Recall@k: an answering (grade 2) span is in the top k. MRR@20: mean of 1/rank of the first answering hit. Coverage@8: the share of a question's answering spans found in the top 8. Small groups move a lot with one question; read every number with its n.

## Results

| group | n | R@1 | R@3 | R@5 | R@8 | MRR@20 | cov@8 |
|---|---|---|---|---|---|---|---|
| all | 66 | 0.530 | 0.803 | 0.864 | 0.894 | 0.677 | 0.876 |
| split: dev | 42 | 0.548 | 0.810 | 0.857 | 0.905 | 0.681 | 0.889 |
| split: test | 24 | 0.500 | 0.792 | 0.875 | 0.875 | 0.668 | 0.854 |
| repo: golang/example | 16 | 0.688 | 0.875 | 0.938 | 1.000 | 0.794 | 0.990 |
| repo: pallets/click | 19 | 0.421 | 0.684 | 0.842 | 0.842 | 0.559 | 0.816 |
| repo: pallets/itsdangerous | 15 | 0.733 | 0.867 | 0.867 | 0.933 | 0.817 | 0.933 |
| repo: sindresorhus/is | 16 | 0.312 | 0.812 | 0.812 | 0.812 | 0.567 | 0.781 |
| kind: conceptual | 8 | 0.750 | 0.875 | 0.875 | 0.875 | 0.819 | 0.875 |
| kind: flow | 13 | 0.231 | 0.615 | 0.692 | 0.769 | 0.448 | 0.769 |
| kind: identifier | 13 | 0.769 | 1.000 | 1.000 | 1.000 | 0.859 | 1.000 |
| kind: literal | 9 | 0.556 | 0.889 | 0.889 | 0.889 | 0.715 | 0.889 |
| kind: location | 14 | 0.500 | 0.714 | 0.786 | 0.857 | 0.623 | 0.845 |
| kind: trap | 9 | 0.444 | 0.778 | 1.000 | 1.000 | 0.661 | 0.889 |

## Per question

| id | repo | kind | split | first hit | R@8 | cov@8 | top result |
|---|---|---|---|---|---|---|---|
| go-1 | golang/example | location | dev | 1 | yes | 1.00 | `hello/reverse/reverse.go:8-15` |
| go-2 | golang/example | location | test | 1 | yes | 1.00 | `ragserver/ragserver/weaviate.go:19-46` |
| go-3 | golang/example | flow | dev | 1 | yes | 1.00 | `ragserver/ragserver/main.go:124-192` |
| go-5 | golang/example | identifier | dev | 1 | yes | 1.00 | `gotypes/defsuses/main.go:22-43` |
| go-6 | golang/example | location | dev | 3 | yes | 0.83 | `ragserver/README.md:10-18` |
| go-7 | golang/example | flow | dev | 6 | yes | 1.00 | `outyet/main.go:1-27` |
| go-8 | golang/example | flow | test | 5 | yes | 1.00 | `hello/hello.go:1-31` |
| go-9 | golang/example | identifier | dev | 1 | yes | 1.00 | `gotypes/nilfunc/main.go:53-92` |
| go-10 | golang/example | identifier | test | 1 | yes | 1.00 | `slog-handler-guide/indenthandler3/indent_handler.go:86-92` |
| go-11 | golang/example | identifier | dev | 1 | yes | 1.00 | `gotypes/skeleton/main.go:28-59` |
| go-12 | golang/example | literal | dev | 1 | yes | 1.00 | `helloserver/server.go:56-65` |
| go-13 | golang/example | literal | test | 2 | yes | 1.00 | `internal/cmd/weave/weave.go:42-161` |
| go-14 | golang/example | conceptual | dev | 1 | yes | 1.00 | `ragserver/README.md:30-37` |
| go-15 | golang/example | conceptual | test | 1 | yes | 1.00 | `slog-handler-guide/guide.md:341-365` |
| go-16 | golang/example | trap | dev | 1 | yes | 1.00 | `slog-handler-guide/indenthandler3/indent_handler.go:50-61` |
| go-17 | golang/example | trap | test | 2 | yes | 1.00 | `slog-handler-guide/indenthandler4/indent_handler.go:97-136` |
| ts-1 | sindresorhus/is | location | dev | 1 | yes | 1.00 | `source/index.ts:195-259` |
| ts-2 | sindresorhus/is | location | test | 2 | yes | 1.00 | `readme.md:284-286` |
| ts-3 | sindresorhus/is | flow | dev | 2 | yes | 1.00 | `readme.md:646-666` |
| ts-5 | sindresorhus/is | identifier | test | 1 | yes | 1.00 | `source/index.ts:387-389` |
| ts-6 | sindresorhus/is | location | dev | 2 | yes | 1.00 | `readme.md:523-525` |
| ts-7 | sindresorhus/is | location | dev | 19 | no | 0.00 | `readme.md:1-7` |
| ts-8 | sindresorhus/is | flow | dev | 1 | yes | 1.00 | `source/index.ts:1404-1414` |
| ts-9 | sindresorhus/is | flow | test | 11 | no | 0.00 | `test/type-tests.ts:253-256` |
| ts-10 | sindresorhus/is | identifier | dev | 2 | yes | 1.00 | `readme.md:430-432` |
| ts-11 | sindresorhus/is | identifier | dev | 3 | yes | 1.00 | `readme.md:505-513` |
| ts-12 | sindresorhus/is | literal | dev | 10 | no | 0.00 | `source/index.ts:1852-1856` |
| ts-13 | sindresorhus/is | literal | test | 1 | yes | 1.00 | `source/index.ts:1404-1414` |
| ts-14 | sindresorhus/is | conceptual | dev | 1 | yes | 1.00 | `readme.md:903-905` |
| ts-15 | sindresorhus/is | conceptual | test | 2 | yes | 1.00 | `AGENTS.md:3-9` |
| ts-16 | sindresorhus/is | trap | dev | 2 | yes | 1.00 | `readme.md:378-392` |
| ts-17 | sindresorhus/is | trap | test | 2 | yes | 0.50 | `readme.md:557-559` |
| py-1 | pallets/itsdangerous | location | dev | 1 | yes | 1.00 | `src/itsdangerous/signer.py:48-64` |
| py-2 | pallets/itsdangerous | location | test | 1 | yes | 1.00 | `src/itsdangerous/timed.py:35-43` |
| py-3 | pallets/itsdangerous | flow | dev | 2 | yes | 1.00 | `src/itsdangerous/timed.py:22-27` |
| py-5 | pallets/itsdangerous | identifier | test | 1 | yes | 1.00 | `src/itsdangerous/encoding.py:11-17` |
| py-6 | pallets/itsdangerous | location | dev | 7 | yes | 1.00 | `src/itsdangerous/timed.py:22-27` |
| py-7 | pallets/itsdangerous | flow | dev | 1 | yes | 1.00 | `src/itsdangerous/url_safe.py:15-69` |
| py-8 | pallets/itsdangerous | flow | test | 9 | no | 0.00 | `src/itsdangerous/signer.py:175-180` |
| py-9 | pallets/itsdangerous | identifier | dev | 1 | yes | 1.00 | `src/itsdangerous/serializer.py:367-395` |
| py-10 | pallets/itsdangerous | identifier | dev | 1 | yes | 1.00 | `src/itsdangerous/signer.py:182-213` |
| py-11 | pallets/itsdangerous | literal | test | 2 | yes | 1.00 | `tests/test_itsdangerous/test_signer.py:18-102` |
| py-12 | pallets/itsdangerous | literal | dev | 1 | yes | 1.00 | `src/itsdangerous/url_safe.py:15-69` |
| py-13 | pallets/itsdangerous | conceptual | dev | 1 | yes | 1.00 | `README.md:16-32` |
| py-14 | pallets/itsdangerous | conceptual | test | 1 | yes | 1.00 | `src/itsdangerous/signer.py:40-45` |
| py-15 | pallets/itsdangerous | trap | dev | 1 | yes | 1.00 | `src/itsdangerous/timed.py:170-228` |
| py-16 | pallets/itsdangerous | trap | test | 1 | yes | 1.00 | `src/itsdangerous/signer.py:31-37` |
| cl-1 | pallets/click | location | dev | 5 | yes | 1.00 | `docs/documentation.md:210-234` |
| cl-2 | pallets/click | location | test | 1 | yes | 1.00 | `src/click/core.py:3654-3676` |
| cl-3 | pallets/click | location | dev | 1 | yes | 1.00 | `src/click/_termui_impl.py:250-294` |
| cl-4 | pallets/click | location | dev | none | no | 0.00 | `tests/test_testing.py:16-31` |
| cl-5 | pallets/click | flow | dev | 11 | no | 0.00 | `examples/completion/completion.py:31-33` |
| cl-6 | pallets/click | flow | test | 3 | yes | 1.00 | `docs/commands-and-groups.md:370-396` |
| cl-7 | pallets/click | flow | dev | 3 | yes | 1.00 | `docs/faqs.md:177-205` |
| cl-8 | pallets/click | flow | dev | 2 | yes | 1.00 | `docs/prompts.md:1-19` |
| cl-9 | pallets/click | identifier | dev | 1 | yes | 1.00 | `src/click/utils.py:62-100` |
| cl-10 | pallets/click | identifier | test | 1 | yes | 1.00 | `src/click/decorators.py:51-97` |
| cl-11 | pallets/click | identifier | dev | 3 | yes | 1.00 | `src/click/core.py:957-964` |
| cl-12 | pallets/click | literal | dev | 3 | yes | 1.00 | `tests/test_arguments.py:71-83` |
| cl-13 | pallets/click | literal | test | 1 | yes | 1.00 | `src/click/core.py:103-105` |
| cl-14 | pallets/click | literal | dev | 1 | yes | 1.00 | `src/click/core.py:1647-1677` |
| cl-15 | pallets/click | conceptual | dev | 1 | yes | 1.00 | `docs/why.md:98-106` |
| cl-16 | pallets/click | conceptual | test | 18 | no | 0.00 | `tests/test_testing.py:260-268` |
| cl-17 | pallets/click | trap | dev | 1 | yes | 1.00 | `src/click/types.py:478-490` |
| cl-18 | pallets/click | trap | dev | 5 | yes | 0.50 | `docs/handling-files.md:81-101` |
| cl-19 | pallets/click | trap | test | 4 | yes | 1.00 | `src/click/termui.py:443-562` |

## Unanswerable questions

Not scored. The distance of the nearest chunk is recorded as data for a possible refusal threshold.

| id | repo | split | top distance | top result |
|---|---|---|---|---|
| go-4 | golang/example | dev | 0.7242 | `ragserver/README.md:10-18` |
| go-18 | golang/example | test | 0.7546 | `ragserver/README.md:10-18` |
| ts-4 | sindresorhus/is | dev | 0.8873 | `readme.md:907-918` |
| ts-18 | sindresorhus/is | test | 0.6784 | `test/type-tests.ts:358-405` |
| py-4 | pallets/itsdangerous | dev | 0.6228 | `src/itsdangerous/signer.py:215-220` |
| py-17 | pallets/itsdangerous | test | 0.6156 | `src/itsdangerous/signer.py:222-225` |
| cl-20 | pallets/click | dev | 0.6378 | `src/click/exceptions.py:68-111` |
| cl-21 | pallets/click | test | 0.4964 | `src/click/decorators.py:421-540` |
