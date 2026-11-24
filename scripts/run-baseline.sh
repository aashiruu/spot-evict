#!/usr/bin/env bash
set -euo pipefail

RUN_ID="${1:?Usage: $0 <run-id>}"
CHUNKS="${2:-100000}"
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

echo "==> Launching On-Demand baseline instance for ${RUN_ID} (chunks=${CHUNKS})..."
terraform -chdir="${TF_DIR}" apply -auto-approve \
  -var="baseline_run_id=${RUN_ID}" \
  -var="job_chunks=${CHUNKS}"

INSTANCE_ID=$(terraform -chdir="${TF_DIR}" output -raw baseline_instance_id)
echo "==> Instance ${INSTANCE_ID} launched. Waiting for s3://${BUCKET}/results/${RUN_ID}.json..."

while ! aws s3 ls "s3://${BUCKET}/results/${RUN_ID}.json" --region "${REGION}" >/dev/null 2>&1; do
  STATE=$(aws ec2 describe-instances --region "${REGION}" --instance-ids "${INSTANCE_ID}" \
    --query 'Reservations[0].Instances[0].State.Name' --output text 2>/dev/null || echo "poll-error")
  echo "[$(date -u +%H:%M:%S)] instance=${INSTANCE_ID} state=${STATE}"
  if [ "${STATE}" = "terminated" ] || [ "${STATE}" = "shutting-down" ]; then
    sleep 5
    break
  fi
  sleep 15
done

aws s3 cp "s3://${BUCKET}/results/${RUN_ID}.json" "${ROOT_DIR}/results/${RUN_ID}.json" --region "${REGION}"

echo "==> Waiting for instance ${INSTANCE_ID} to reach terminated state..."
aws ec2 wait instance-terminated --region "${REGION}" --instance-ids "${INSTANCE_ID}"

LAUNCH_TIME=$(aws ec2 describe-instances --region "${REGION}" --instance-ids "${INSTANCE_ID}" \
  --query 'Reservations[0].Instances[0].LaunchTime' --output text)
TRANSITION_REASON=$(aws ec2 describe-instances --region "${REGION}" --instance-ids "${INSTANCE_ID}" \
  --query 'Reservations[0].Instances[0].StateTransitionReason' --output text)

echo "==> Run ${RUN_ID} complete!"
echo "LaunchTime:            ${LAUNCH_TIME}"
echo "StateTransitionReason: ${TRANSITION_REASON}"
cat "${ROOT_DIR}/results/${RUN_ID}.json"

# Clean up instance resource from terraform state
terraform -chdir="${TF_DIR}" apply -auto-approve -var="baseline_run_id="
