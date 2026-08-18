variable "name" {
  description = "Resource name prefix, e.g. agent-trail-dev."
  type        = string
}

variable "recovery_window_days" {
  description = "Secrets Manager recovery window on deletion."
  type        = number
  default     = 7
}

variable "tags" {
  description = "Tags applied to every resource."
  type        = map(string)
  default     = {}
}
