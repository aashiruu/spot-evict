resource "aws_iam_role" "fis" {
  name = "spot-evict-fis-role"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "fis.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })
}

resource "aws_iam_role_policy" "fis_spot" {
  name = "spot-evict-fis-spot-policy"
  role = aws_iam_role.fis.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "ec2:SendSpotInstanceInterruptions",
          "ec2:DescribeInstances"
        ]
        Resource = "*"
      }
    ]
  })
}

resource "aws_cloudwatch_log_group" "spot_itn_events" {
  name              = "/aws/events/spot-evict-spot-interruptions"
  retention_in_days = 7
}

resource "aws_cloudwatch_event_rule" "spot_itn" {
  name        = "spot-evict-spot-itn-warning"
  description = "Captures EC2 Spot Instance Interruption Warning events for Stage 4 IMDS vs EventBridge comparison"

  event_pattern = jsonencode({
    source      = ["aws.ec2"]
    detail-type = ["EC2 Spot Instance Interruption Warning"]
  })
}

resource "aws_cloudwatch_log_resource_policy" "eventbridge_logs" {
  policy_name = "spot-evict-eventbridge-to-cwlogs"

  policy_document = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = ["events.amazonaws.com", "delivery.logs.amazonaws.com"] }
      Action    = ["logs:CreateLogStream", "logs:PutLogEvents"]
      Resource  = "${aws_cloudwatch_log_group.spot_itn_events.arn}:*"
    }]
  })
}

resource "aws_cloudwatch_event_target" "spot_itn_cw" {
  rule      = aws_cloudwatch_event_rule.spot_itn.name
  target_id = "cw-logs-spot-itn"
  arn       = aws_cloudwatch_log_group.spot_itn_events.arn
}

output "fis_role_arn" {
  value = aws_iam_role.fis.arn
}

output "spot_itn_log_group" {
  value = aws_cloudwatch_log_group.spot_itn_events.name
}
