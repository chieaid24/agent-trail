variable "name" {
  description = "Resource name prefix, e.g. agent-trail-dev."
  type        = string
}

variable "vpc_id" {
  description = "VPC id."
  type        = string
}

variable "private_subnet_ids" {
  description = "Private subnets for the ECS service."
  type        = list(string)
}

variable "cluster_arn" {
  description = "ECS cluster the dashboard service joins (shared with control-plane)."
  type        = string
}

variable "https_listener_arn" {
  description = "ALB HTTPS listener that receives the path routing rules."
  type        = string
}

variable "api_target_group_arn" {
  description = "Control plane target group that API paths forward to."
  type        = string
}

variable "alb_security_group_id" {
  description = "ALB security group; granted egress to the dashboard tasks."
  type        = string
}

variable "api_security_group_id" {
  description = "Control plane task security group; granted ingress from the dashboard tasks."
  type        = string
}

variable "api_port" {
  description = "Port the control plane listens on."
  type        = number
  default     = 8080
}

variable "service_connect_namespace_arn" {
  description = "Service Connect namespace the dashboard joins as a client."
  type        = string
}

variable "image" {
  description = "Dashboard container image (immutable tag or digest)."
  type        = string
}

variable "container_port" {
  description = "Port the dashboard listens on."
  type        = number
  default     = 3000
}

variable "desired_count" {
  description = "Number of dashboard tasks."
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

variable "api_proxy_target" {
  description = "Origin the dashboard proxies /backend/* to (the control plane Service Connect URL)."
  type        = string
}

variable "log_retention_days" {
  description = "CloudWatch retention for dashboard logs."
  type        = number
  default     = 30
}

variable "tags" {
  description = "Tags applied to every resource."
  type        = map(string)
  default     = {}
}
