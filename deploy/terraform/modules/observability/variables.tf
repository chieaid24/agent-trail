variable "name" {
  description = "Resource name prefix, e.g. agent-trail-dev."
  type        = string
}

variable "alert_email" {
  description = "Email address subscribed to the alerts topic. Empty skips the subscription."
  type        = string
  default     = ""
}

variable "enable_transaction_search" {
  description = "Whether this environment owns the account and Region Transaction Search configuration."
  type        = bool
  default     = false
}

variable "trace_retention_days" {
  description = "Retention for Transaction Search and Application Signals span log groups."
  type        = number
  default     = 30
}

variable "trace_indexing_percentage" {
  description = "Percentage of Transaction Search spans indexed as X-Ray trace summaries."
  type        = number
  default     = 1

  validation {
    condition     = var.trace_indexing_percentage >= 0 && var.trace_indexing_percentage <= 100
    error_message = "trace_indexing_percentage must be between 0 and 100."
  }
}

variable "alb_arn_suffix" {
  description = "ALB ARN suffix for HTTP metrics."
  type        = string
}

variable "target_group_arn_suffix" {
  description = "Target group ARN suffix for healthy-host metrics."
  type        = string
}

variable "db_instance_identifier" {
  description = "RDS instance identifier."
  type        = string
}

variable "task_dispatch_queue_name" {
  description = "SQS task dispatch queue name."
  type        = string
}

variable "dead_letter_queue_name" {
  description = "SQS dead-letter queue name."
  type        = string
}

variable "tags" {
  description = "Tags applied to every resource."
  type        = map(string)
  default     = {}
}
