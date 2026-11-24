#!/usr/bin/env bash
set -euo pipefail
REGION="${AWS_REGION:-us-east-1}"

echo "Checking running/pending EC2 instances tagged project=spot-evict..."
aws ec2 describe-instances --region "${REGION}" \
  --filters "Name=tag:project,Values=spot-evict" "Name=instance-state-name,Values=pending,running,stopping,stopped" \
  --query 'Reservations[*].Instances[*].[InstanceId,InstanceLifecycle,State.Name,Tags[?Key==`Name`].Value|[0]]' \
  --output table

echo "Checking Auto Scaling Groups with prefix spot-evict-..."
aws autoscaling describe-auto-scaling-groups --region "${REGION}" \
  --query 'AutoScalingGroups[?starts_with(AutoScalingGroupName, `spot-evict-`)].[AutoScalingGroupName,DesiredCapacity,length(Instances)]' \
  --output table

echo "Checking unattached EBS volumes tagged project=spot-evict..."
aws ec2 describe-volumes --region "${REGION}" \
  --filters "Name=tag:project,Values=spot-evict" "Name=status,Values=available" \
  --query 'Volumes[*].[VolumeId,Size,State]' \
  --output table
