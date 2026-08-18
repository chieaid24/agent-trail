variable "zone_name" {
  description = "Route53 hosted zone name, e.g. agent-trail.dev."
  type        = string
}

variable "create_zone" {
  description = "Create the hosted zone; false looks up an existing one."
  type        = bool
  default     = false
}

variable "domain_name" {
  description = "Fully qualified control plane domain, e.g. api.dev.agent-trail.dev."
  type        = string
}

variable "alb_dns_name" {
  description = "ALB DNS name to alias to."
  type        = string
}

variable "alb_zone_id" {
  description = "ALB hosted zone id."
  type        = string
}

variable "tags" {
  description = "Tags applied to every resource."
  type        = map(string)
  default     = {}
}
