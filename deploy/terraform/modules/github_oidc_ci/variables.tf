variable "name" {
  description = "Resource name prefix, e.g. agent-trail-dev."
  type        = string
}

variable "github_repository" {
  description = "owner/repo allowed to assume the CI role."
  type        = string

  validation {
    condition     = can(regex("^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$", var.github_repository))
    error_message = "github_repository must be owner/repo."
  }
}

variable "deploy_ref" {
  description = "Git ref allowed to deploy."
  type        = string
  default     = "refs/heads/main"
}

variable "ecr_repository_arns" {
  description = "ECR repositories CI may push to."
  type        = list(string)
}

variable "ecs_cluster_name" {
  description = "ECS cluster CI deploys to."
  type        = string
}

variable "ecs_service_name" {
  description = "ECS service CI updates."
  type        = string
}

variable "create_oidc_provider" {
  description = "Create the GitHub OIDC provider; false reuses an existing one in the account."
  type        = bool
  default     = true
}

variable "tags" {
  description = "Tags applied to every resource."
  type        = map(string)
  default     = {}
}
