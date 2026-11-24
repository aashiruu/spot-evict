variable "spot_run_id" {
  description = "Run identifier for the active Spot ASG run (empty scales ASG to 0)"
  type        = string
  default     = ""
}

variable "spot_watch_itn" {
  description = "Whether the Spot worker polls IMDSv2 for 2-minute Spot ITNs (false = Stage 3 periodic-only)"
  type        = bool
  default     = true
}

variable "spot_instance_types" {
  description = "Diversified Spot instance pool for capacity-optimized-prioritized allocation"
  type        = list(string)
  default     = ["t3.micro", "t3a.micro"]
}

resource "aws_launch_template" "spot_worker" {
  count = var.spot_run_id != "" ? 1 : 0

  name_prefix   = "spot-evict-${var.spot_run_id}-"
  image_id      = data.aws_ssm_parameter.al2023_amd64.value
  instance_type = var.spot_instance_types[0]

  iam_instance_profile {
    name = aws_iam_instance_profile.worker.name
  }

  network_interfaces {
    associate_public_ip_address = true
    security_groups             = [aws_security_group.worker.id]
  }

  instance_initiated_shutdown_behavior = "terminate"

  metadata_options {
    http_endpoint = "enabled"
    http_tokens   = "required"
  }

  user_data = base64encode(templatefile("${path.module}/userdata.sh.tftpl", {
    bucket     = aws_s3_bucket.artifacts.id
    run_id     = var.spot_run_id
    chunks     = var.job_chunks
    rounds     = var.job_rounds
    ckpt_every = var.checkpoint_every
    watch_itn  = var.spot_watch_itn
  }))

  tag_specifications {
    resource_type = "instance"
    tags = {
      Name    = "spot-evict-${var.spot_run_id}"
      run_id  = var.spot_run_id
      tier    = "spot"
      project = "spot-evict"
    }
  }

  lifecycle {
    create_before_destroy = true
  }
}

resource "aws_autoscaling_group" "spot_fleet" {
  count = var.spot_run_id != "" ? 1 : 0

  name_prefix         = "spot-evict-${var.spot_run_id}-"
  min_size            = 1
  max_size            = 1
  desired_capacity    = 1
  vpc_zone_identifier = aws_subnet.public[*].id
  health_check_type   = "EC2"
  force_delete        = true

  mixed_instances_policy {
    instances_distribution {
      on_demand_base_capacity                  = 0
      on_demand_percentage_above_base_capacity = 0
      spot_allocation_strategy                 = "capacity-optimized-prioritized"
    }

    launch_template {
      launch_template_specification {
        launch_template_id = aws_launch_template.spot_worker[0].id
        version            = "$Latest"
      }

      dynamic "override" {
        for_each = var.spot_instance_types
        content {
          instance_type = override.value
        }
      }
    }
  }

  tag {
    key                 = "Name"
    value               = "spot-evict-${var.spot_run_id}"
    propagate_at_launch = true
  }

  tag {
    key                 = "run_id"
    value               = var.spot_run_id
    propagate_at_launch = true
  }

  tag {
    key                 = "project"
    value               = "spot-evict"
    propagate_at_launch = true
  }
}

output "spot_asg_name" {
  value = length(aws_autoscaling_group.spot_fleet) > 0 ? aws_autoscaling_group.spot_fleet[0].name : ""
}
