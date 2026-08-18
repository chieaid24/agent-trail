variable "aws_region" {
  description = "AWS region."
  type        = string
  default     = "us-east-1"
}

variable "zone_name" {
  description = "Route53 hosted zone name."
  type        = string
}

variable "domain_name" {
  description = "Control plane domain for this environment."
  type        = string
}

variable "control_plane_image" {
  description = "Control plane image (immutable tag or digest)."
  type        = string
}

variable "github_repository" {
  description = "owner/repo allowed to deploy via CI."
  type        = string
  default     = "chieaid24/agent-trail"
}

variable "alert_email" {
  description = "Email subscribed to alerts. Empty skips the subscription."
  type        = string
  default     = ""
}
