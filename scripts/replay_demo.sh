#!/usr/bin/env bash
set -euo pipefail

echo "=== 1. Running unit & integration test suite (workload, checkpoint corruption fallback, IMDSv2 watcher, fencing, simulator) ==="
GOTOOLCHAIN=local go test -v ./...

echo ""
echo "=== 2. Building worker & simulator binaries ==="
GOTOOLCHAIN=local go build -trimpath -ldflags="-s -w" -o bin/worker ./cmd/worker
GOTOOLCHAIN=local go build -trimpath -ldflags="-s -w" -o bin/sim ./cmd/sim

echo ""
echo "=== 3. Running Monte Carlo checkpoint-interval & Spot cost simulation ==="
./bin/sim -base-sec 7200 -trials 500 -json-out results/stage7-8-sim-cost.json -csv-out results/stage7-8-sim-cost.csv

echo ""
echo "=== 4. Running deterministic local workload verification ==="
./bin/worker -run-id "replay-check" -chunks 500 -rounds 2000 -json
