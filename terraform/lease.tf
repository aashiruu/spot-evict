resource "aws_dynamodb_table" "leases" {
  name         = "spot-evict-leases"
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "run_id"

  attribute {
    name = "run_id"
    type = "S"
  }

  tags = {
    Name    = "spot-evict-leases"
    project = "spot-evict"
  }
}

resource "aws_iam_role_policy" "worker_dynamodb_lease" {
  name = "spot-evict-worker-dynamodb-lease"
  role = aws_iam_role.worker.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "dynamodb:GetItem",
          "dynamodb:UpdateItem",
          "dynamodb:DeleteItem"
        ]
        Resource = aws_dynamodb_table.leases.arn
      }
    ]
  })
}

output "leases_table_name" {
  value = aws_dynamodb_table.leases.name
}
