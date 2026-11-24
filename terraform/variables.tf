variable "aws_region" {
  description = "AWS region for all spot-evict resources"
  type        = string
  default     = "us-east-1"
}

variable "allowed_account_id" {
  description = "Hard-pinned non-production AWS account ID"
  type        = string
  default     = "529088258981"
}

variable "budget_limit_usd" {
  description = "Monthly USD budget cap for spot-evict"
  type        = string
  default     = "15.0"
}

variable "budget_alert_emails" {
  description = "Email addresses for AWS Budget threshold alerts"
  type        = list(string)
  default     = []
}

variable "max_fleet_size" {
  description = "Hard cap on ASG instances to prevent runaway spend"
  type        = number
  default     = 2

  validation {
    condition     = var.max_fleet_size >= 1 && var.max_fleet_size <= 4
    error_message = "max_fleet_size must be between 1 and 4."
  }
}
variable "baseline_instance_type" {
  description = "Small On-Demand instance type for baseline runs"
  type        = string
  default     = "t3.micro"
}

variable "baseline_run_id" {
  description = "Run identifier for the active On-Demand baseline run (empty disables the instance)"
  type        = string
  default     = ""
}

variable "job_chunks" {
  description = "Number of chunks for the full baseline job"
  type        = number
  default     = 100000

  validation {
    condition     = var.job_chunks >= 1 && var.job_chunks <= 100000
    error_message = "job_chunks must be between 1 and 100000."
  }
}

variable "job_rounds" {
  description = "SHA-256 rounds per chunk"
  type        = number
  default     = 10000
}
