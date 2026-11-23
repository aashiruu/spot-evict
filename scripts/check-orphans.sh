#!/usr/bin/env bash
set -euo pipefail

REGION="${AWS_REGION:-us-east-1}"

echo "==> Checking for any AWS resources tagged project=spot-evict in ${REGION}..."
ORPHANS=$(aws resourcegroupstaggingapi get-resources \
  --region "${REGION}" \
  --tag-filters Key=project,Values=spot-evict \
  --query 'ResourceTagMappingList[].ResourceARN' \
  --output text)

if [ -n "${ORPHANS}" ]; then
  echo "FOUND TAGGED RESOURCES:"
  echo "${ORPHANS}" | tr '\t' '\n'
  exit 1
fi

echo "OK: Zero tagged resources found in ${REGION}."
