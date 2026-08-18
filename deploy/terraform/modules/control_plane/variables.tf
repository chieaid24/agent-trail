variable "name" {
  description = "Resource name prefix, e.g. agent-trail-dev."
  type        = string
}

variable "vpc_id" {
  description = "VPC id."
  type        = string
}

variable "public_subnet_ids" {
  description = "Public subnets for the ALB."
  type        = list(string)
}

variable "private_subnet_ids" {
  description = "Private subnets for the ECS service."
  type        = list(string)
}

variable "image" {
  description = "Control plane container image (immutable tag or digest)."
  type        = string
}

variable "container_port" {
  description = "Port the control plane listens on."
  type        = number
  default     = 8080
}

variable "desired_count" {
  description = "Number of control plane tasks."
  type        = number
  default     = 2
}

variable "cpu" {
  description = "Fargate task CPU units."
  type        = number
  default     = 512
}

variable "memory" {
  description = "Fargate task memory in MiB."
  type        = number
  default     = 1024
}

variable "certificate_arn" {
  description = "ACM certificate for the HTTPS listener."
  type        = string
}

variable "artifacts_bucket_arn" {
  description = "S3 bucket ARN for logs and artifacts."
  type        = string
}

variable "task_dispatch_queue_arn" {
  description = "SQS queue ARN for task dispatch."
  type        = string
}

variable "secret_arns" {
  description = "Secrets Manager ARNs the control plane may read (includes the GitHub App key and the RDS master secret)."
  type        = list(string)
}

variable "environment" {
  description = "Plain (non-secret) environment variables for the container."
  type        = map(string)
  default     = {}
}

variable "container_secrets" {
  description = "Env var name to Secrets Manager ARN, injected by ECS at start."
  type        = map(string)
  default     = {}
}

variable "log_retention_days" {
  description = "CloudWatch retention for control plane logs."
  type        = number
  default     = 30
}

variable "tags" {
  description = "Tags applied to every resource."
  type        = map(string)
  default     = {}
}
