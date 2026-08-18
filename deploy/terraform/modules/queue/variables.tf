variable "name" {
  description = "Resource name prefix, e.g. agent-trail-dev."
  type        = string
}

variable "visibility_timeout_seconds" {
  description = "Visibility timeout; must exceed the runner controller processing window."
  type        = number
  default     = 300
}

variable "max_receive_count" {
  description = "Receives before a message moves to the dead-letter queue."
  type        = number
  default     = 5
}

variable "tags" {
  description = "Tags applied to every resource."
  type        = map(string)
  default     = {}
}
