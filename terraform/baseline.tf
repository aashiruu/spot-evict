data "aws_availability_zones" "available" {
  state = "available"
}

resource "aws_vpc" "main" {
  cidr_block           = "10.42.0.0/16"
  enable_dns_support   = true
  enable_dns_hostnames = true

  tags = {
    Name = "spot-evict-vpc"
  }
}

resource "aws_internet_gateway" "main" {
  vpc_id = aws_vpc.main.id

  tags = {
    Name = "spot-evict-igw"
  }
}

resource "aws_subnet" "public" {
  count                   = 2
  vpc_id                  = aws_vpc.main.id
  cidr_block              = cidrsubnet(aws_vpc.main.cidr_block, 8, count.index)
  availability_zone       = data.aws_availability_zones.available.names[count.index]
  map_public_ip_on_launch = true

  tags = {
    Name = "spot-evict-public-${count.index}"
  }
}

resource "aws_route_table" "public" {
  vpc_id = aws_vpc.main.id

  route {
    cidr_block = "0.0.0.0/0"
    gateway_id = aws_internet_gateway.main.id
  }

  tags = {
    Name = "spot-evict-public-rt"
  }
}

resource "aws_route_table_association" "public" {
  count          = 2
  subnet_id      = aws_subnet.public[count.index].id
  route_table_id = aws_route_table.public.id
}

data "aws_ssm_parameter" "al2023_amd64" {
  name = "/aws/service/ami-amazon-linux-latest/al2023-ami-kernel-default-x86_64"
}

resource "random_id" "bucket_suffix" {
  byte_length = 4
}

resource "aws_s3_bucket" "artifacts" {
  bucket        = "spot-evict-${var.allowed_account_id}-${random_id.bucket_suffix.hex}"
  force_destroy = true
}

resource "aws_s3_bucket_public_access_block" "artifacts" {
  bucket                  = aws_s3_bucket.artifacts.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_iam_role" "worker" {
  name = "spot-evict-worker-role"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Action = "sts:AssumeRole"
      Effect = "Allow"
      Principal = {
        Service = "ec2.amazonaws.com"
      }
    }]
  })
}

resource "aws_iam_role_policy" "worker_s3" {
  name = "spot-evict-worker-s3"
  role = aws_iam_role.worker.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Action = [
        "s3:GetObject",
        "s3:PutObject",
        "s3:DeleteObject",
        "s3:ListBucket"
      ]
      Resource = [
        aws_s3_bucket.artifacts.arn,
        "${aws_s3_bucket.artifacts.arn}/*"
      ]
    }]
  })
}

resource "aws_iam_instance_profile" "worker" {
  name = "spot-evict-worker-profile"
  role = aws_iam_role.worker.name
}

resource "aws_security_group" "worker" {
  name        = "spot-evict-worker-sg"
  description = "Egress-only security group for spot-evict workers"
  vpc_id      = aws_vpc.main.id

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
}

resource "aws_instance" "baseline" {
  count = var.baseline_run_id != "" ? 1 : 0

  ami                                  = data.aws_ssm_parameter.al2023_amd64.value
  instance_type                        = var.baseline_instance_type
  subnet_id                            = aws_subnet.public[0].id
  vpc_security_group_ids               = [aws_security_group.worker.id]
  iam_instance_profile                 = aws_iam_instance_profile.worker.name
  associate_public_ip_address          = true
  instance_initiated_shutdown_behavior = "terminate"
  user_data_replace_on_change          = true

  metadata_options {
    http_endpoint = "enabled"
    http_tokens   = "required"
  }

  user_data = templatefile("${path.module}/userdata.sh.tftpl", {
    bucket     = aws_s3_bucket.artifacts.id
    run_id     = var.baseline_run_id
    chunks     = var.job_chunks
    rounds     = var.job_rounds
    ckpt_every = var.checkpoint_every
    watch_itn  = false
    enable_s3  = true
  })

  tags = {
    Name   = "spot-evict-${var.baseline_run_id}"
    run_id = var.baseline_run_id
  }
}

output "artifacts_bucket" {
  value = aws_s3_bucket.artifacts.id
}

output "baseline_instance_id" {
  value = length(aws_instance.baseline) > 0 ? aws_instance.baseline[0].id : ""
}
