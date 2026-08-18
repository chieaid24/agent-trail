variable "name" {
  description = "Resource name prefix, e.g. agent-trail-dev."
  type        = string
}

variable "vpc_id" {
  description = "VPC id."
  type        = string
}

variable "private_subnet_ids" {
  description = "Private subnets for the EKS control plane ENIs and nodes."
  type        = list(string)
}

variable "kubernetes_version" {
  description = "EKS Kubernetes version."
  type        = string
  default     = "1.33"
}

variable "node_instance_types" {
  description = "Instance types for the runner node group."
  type        = list(string)
  default     = ["m7g.large"]
}

variable "node_min_size" {
  description = "Minimum runner nodes."
  type        = number
  default     = 1
}

variable "node_max_size" {
  description = "Maximum runner nodes."
  type        = number
  default     = 4
}

variable "node_desired_size" {
  description = "Desired runner nodes."
  type        = number
  default     = 1
}

variable "artifacts_bucket_arn" {
  description = "S3 bucket ARN runner Jobs may upload artifacts to."
  type        = string
}

variable "task_dispatch_queue_arn" {
  description = "SQS queue ARN the runner controller consumes."
  type        = string
}

variable "runner_secret_arns" {
  description = "Secrets Manager ARNs the runner controller may read for Jobs (never the GitHub App key)."
  type        = list(string)
}

variable "runner_namespace" {
  description = "Namespace runner Jobs execute in."
  type        = string
  default     = "agent-trail-runners"
}

variable "tags" {
  description = "Tags applied to every resource."
  type        = map(string)
  default     = {}
}
