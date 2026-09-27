#!/usr/bin/env bash
# Run the fixed hybrid grid on the dev split (results: docs/phase2/hybrid-dev.md). No API requests: every question
# vector is already cached. Writes docs/phase2/runs/hybrid-dev-w<weight>-p<penalty>-n<pool>.json and prints one
# summary line per setting. Run from api/ with .env loaded:
#   cd /home/georgejoseph/Rag-project/api && set -a && . ../.env && set +a && bash scripts/hybrid_grid.sh
set -euo pipefail
go build -o /tmp/repopilot-eval ./cmd/eval
for w in 1 0.75 0.5; do
  for p in 1 0.5; do
    for n in 20 50; do
      out="../docs/phase2/runs/hybrid-dev-w${w}-p${p}-n${n}.json"
      line=$(/tmp/repopilot-eval -split dev -variant hybrid -rrf-k 60 -keyword-weight "$w" -test-penalty "$p" -pool "$n" \
        -max-requests 0 -out "$out" | grep '^hybrid:')
      echo "weight $w  penalty $p  pool $n  ${line#hybrid: }"
    done
  done
done
