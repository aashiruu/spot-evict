#!/usr/bin/env bash
set -euo pipefail
REGION="${AWS_REGION:-us-east-1}"
TF_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/terraform"

echo "==> Scaling active Spot ASG and On-Demand baseline to 0..."
terraform -chdir="${TF_DIR}" apply -auto-approve \
  -var="spot_run_id=" \
  -var="baseline_run_id="

echo "==> Running orphan check..."
"$(dirname "${BASH_SOURCE[0]}")/orphan-check.sh"
