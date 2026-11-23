#!/usr/bin/env bash
set -euo pipefail

REGION="${AWS_REGION:-us-east-1}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TF_DIR="${SCRIPT_DIR}/../terraform"

echo "==> Running terraform destroy in ${TF_DIR} (region:${REGION})"
if [ -d "${TF_DIR}/.terraform" ]; then
  terraform -chdir="${TF_DIR}" destroy -auto-approve
else
  echo "Terraform not initialized; skipping terraform destroy."
fi

echo "==> Running orphan check..."
"${SCRIPT_DIR}/check-orphans.sh"
