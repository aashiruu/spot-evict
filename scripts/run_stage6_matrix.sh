#!/usr/bin/env bash
set -euo pipefail

REGION="us-east-1"
BUCKET=$(terraform -chdir=terraform output -raw artifacts_bucket)
FIS_ROLE=$(terraform -chdir=terraform output -raw fis_role_arn)
EXPECTED_CHECKSUM="666214f8a41e866bc5e9887d4bf999e1b3066e778fa48d67bb1837b4b71a15c4"

# Budget-fitted N=1 per strategy cell (~$0.20 FIS + ~$0.0004 EC2 per cell = ~$0.80 total)
# Fixed FIS interruption schedule: fire 45s after initial Spot worker launches, with 2-minute ITN window (PT2M)
STRATEGIES=(
  "A-none:false:0:false"
  "B-notice-only:true:0:true"
  "C-periodic:true:15000:false"
  "D-periodic-plus-notice:true:15000:true"
)

mkdir -p results/stage6_events

for spec in "${STRATEGIES[@]}"; do
  IFS=':' read -r NAME ENABLE_S3 CKPT_EVERY WATCH_ITN <<< "$spec"
  RUN_ID="stage6-${NAME}-$(date -u +%H%M%S)"
  echo ""
  echo "=================================================================="
  echo "[$(date -u +%H:%M:%S)] Starting Strategy ${NAME} (run_id=${RUN_ID}, enable_s3=${ENABLE_S3}, ckpt_every=${CKPT_EVERY}, watch_itn=${WATCH_ITN})"
  echo "=================================================================="

  terraform -chdir=terraform apply -auto-approve \
    -var="spot_run_id=${RUN_ID}" \
    -var="spot_enable_s3=${ENABLE_S3}" \
    -var="checkpoint_every=${CKPT_EVERY}" \
    -var="spot_watch_itn=${WATCH_ITN}" >/dev/null

  # Wait for initial Spot instance to launch
  INST1=""
  while [ -z "$INST1" ]; do
    INST1=$(aws ec2 describe-instances --region "$REGION" \
      --filters "Name=tag:run_id,Values=${RUN_ID}" "Name=instance-state-name,Values=pending,running" \
      --query 'Reservations[0].Instances[0].InstanceId' --output text)
    [ "$INST1" = "None" ] && INST1=""
    sleep 2
  done
  echo "[$(date -u +%H:%M:%S)] Initial Spot instance launched: ${INST1}; waiting 45s fixed schedule before FIS interruption..."
  sleep 45

  # Create & start AWS FIS Spot interruption template (durationBeforeInterruption=PT2M)
  TPL_ID=$(aws fis create-experiment-template --region "$REGION" \
    --description "Stage 6 fixed-schedule Spot ITN for ${RUN_ID}" \
    --role-arn "$FIS_ROLE" \
    --stop-conditions '[{"source":"none"}]' \
    --targets "{\"SpotTargets\":{\"resourceType\":\"aws:ec2:spot-instance\",\"resourceArns\":[\"arn:aws:ec2:${REGION}:529088258981:instance/${INST1}\"],\"selectionMode\":\"ALL\"}}" \
    --actions '{"InterruptSpot":{"actionId":"aws:ec2:send-spot-instance-interruptions","parameters":{"durationBeforeInterruption":"PT2M"},"targets":{"SpotInstances":"SpotTargets"}}}' \
    --query 'experimentTemplate.id' --output text)

  EXP_ID=$(aws fis start-experiment --region "$REGION" \
    --experiment-template-id "$TPL_ID" \
    --query 'experiment.id' --output text)
  echo "[$(date -u +%H:%M:%S)] Started FIS experiment ${EXP_ID} (template=${TPL_ID}) against ${INST1}"

  # Wait until final completion result appears in S3 (results/${RUN_ID}.json)
  until aws s3 ls "s3://${BUCKET}/results/${RUN_ID}.json" --region "$REGION" >/dev/null 2>&1; do
    sleep 5
  done
  echo "[$(date -u +%H:%M:%S)] Job completed! Downloading result + JSONL event logs..."

  aws s3 cp "s3://${BUCKET}/results/${RUN_ID}.json" "results/${RUN_ID}.json" --region "$REGION"
  mkdir -p "results/stage6_events/${RUN_ID}"
  aws s3 sync "s3://${BUCKET}/events/${RUN_ID}/" "results/stage6_events/${RUN_ID}/" --region "$REGION"
  cat "results/stage6_events/${RUN_ID}"/*.jsonl | sort > "results/${RUN_ID}.jsonl"

  # Tear down ASG and clean FIS template immediately
  terraform -chdir=terraform apply -auto-approve -var="spot_run_id=" >/dev/null
  aws fis delete-experiment-template --region "$REGION" --id "$TPL_ID" >/dev/null || true

  echo "[$(date -u +%H:%M:%S)] Strategy ${NAME} complete and ASG scaled to 0."
done
