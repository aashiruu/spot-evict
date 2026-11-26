#!/usr/bin/env bash
set -euo pipefail

RUN_ID="${1:?Usage: $0 <run-id> [chunks] [ckpt-every]}"
CHUNKS="${2:-40000}"
CKPT_EVERY="${3:-2000}"
REGION="${AWS_REGION:-us-east-1}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="${SCRIPT_DIR}/.."
TF_DIR="${ROOT_DIR}/terraform"

export AWS_MAX_ATTEMPTS=5
export AWS_RETRY_MODE=standard

mkdir -p "${ROOT_DIR}/bin" "${ROOT_DIR}/results"

echo "==> Building Linux amd64 worker binary..."
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o "${ROOT_DIR}/bin/worker" "${ROOT_DIR}/cmd/worker"

BUCKET=$(terraform -chdir="${TF_DIR}" output -raw artifacts_bucket)
echo "==> Uploading worker binary to s3://${BUCKET}/bin/worker..."
aws s3 cp "${ROOT_DIR}/bin/worker" "s3://${BUCKET}/bin/worker" --region "${REGION}"

echo "==> Launching Spot ASG for ${RUN_ID} (chunks=${CHUNKS}, ckpt_every=${CKPT_EVERY})..."
terraform -chdir="${TF_DIR}" apply -auto-approve \
  -var="spot_run_id=${RUN_ID}" \
  -var="job_chunks=${CHUNKS}" \
  -var="checkpoint_every=${CKPT_EVERY}"

ASG_NAME=$(terraform -chdir="${TF_DIR}" output -raw spot_asg_name)
echo "==> Spot ASG ${ASG_NAME} active. Waiting for s3://${BUCKET}/results/${RUN_ID}.json..."

while ! aws s3 ls "s3://${BUCKET}/results/${RUN_ID}.json" --region "${REGION}" >/dev/null 2>&1; do
  INSTANCES=$(aws autoscaling describe-auto-scaling-groups --region "${REGION}" \
    --auto-scaling-group-names "${ASG_NAME}" \
    --query 'AutoScalingGroups[0].Instances[*].[InstanceId,LifecycleState,HealthStatus]' \
    --output text 2>/dev/null || echo "polling")
  echo "[$(date -u +%H:%M:%S)] ASG instances: ${INSTANCES:-none}"
  sleep 15
done

aws s3 cp "s3://${BUCKET}/results/${RUN_ID}.json" "${ROOT_DIR}/results/${RUN_ID}.json" --region "${REGION}"
echo "==> Run ${RUN_ID} complete!"
cat "${ROOT_DIR}/results/${RUN_ID}.json"

echo "==> Scaling Spot ASG back to 0..."
terraform -chdir="${TF_DIR}" apply -auto-approve -var="spot_run_id="
