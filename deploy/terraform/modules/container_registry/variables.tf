variable "name" {
  description = "Resource name prefix, e.g. agent-trail-dev."
  type        = string
}

variable "keep_last_images" {
  description = "How many images each repository retains."
  type        = number
  default     = 20
}

variable "tags" {
  description = "Tags applied to every resource."
  type        = map(string)
  default     = {}
}
