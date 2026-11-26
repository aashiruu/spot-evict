#!/usr/bin/env bash
set -euo pipefail

REGION="${AWS_REGION:-us-east-1}"
START_DATE="${1:?Usage: $0 YYYY-MM-DD YYYY-MM-DD}"
END_DATE="${2:?Usage: $0 YYYY-MM-DD YYYY-MM-DD}"

echo "=== 1. EC2 Spot Price History (${START_DATE} to ${END_DATE}) ==="
aws ec2 describe-spot-price-history \
  --region "${REGION}" \
  --instance-types t3.micro t3a.micro \
  --product-descriptions "Linux/UNIX" \
  --start-time "${START_DATE}T00:00:00Z" \
  --end-time "${END_DATE}T23:59:59Z" \
  --query 'SpotPriceHistory[*].[Timestamp,InstanceType,AvailabilityZone,SpotPrice]' \
  --output table

echo "=== 2. AWS Cost Explorer Billed Cost (Tag: project=spot-evict) ==="
aws ce get-cost-and-usage \
  --region us-east-1 \
  --time-period "Start=${START_DATE},End=${END_DATE}" \
  --granularity DAILY \
  --metrics "UnblendedCost" "UsageQuantity" \
  --filter '{"Tags":{"Key":"project","Values":["spot-evict"]}}' \
  --group-by Type=DIMENSION,Key=SERVICE \
  --output table

echo "=== 3. Untagged Spend Check (Group by Tag project) ==="
aws ce get-cost-and-usage \
  --region us-east-1 \
  --time-period "Start=${START_DATE},End=${END_DATE}" \
  --granularity DAILY \
  --metrics "UnblendedCost" "UsageQuantity" \
  --group-by Type=TAG,Key=project \
  --output table
