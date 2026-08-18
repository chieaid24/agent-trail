variable "name" {
  description = "Resource name prefix, e.g. agent-trail-dev. Bucket name gets the account id appended for global uniqueness."
  type        = string
}

variable "artifact_expiration_days" {
  description = "Days before task artifacts and logs expire."
  type        = number
  default     = 90
}

variable "tags" {
  description = "Tags applied to every resource."
  type        = map(string)
  default     = {}
}
