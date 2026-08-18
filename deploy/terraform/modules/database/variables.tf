variable "name" {
  description = "Resource name prefix, e.g. agent-trail-dev."
  type        = string
}

variable "vpc_id" {
  description = "VPC to place the database in."
  type        = string
}

variable "subnet_ids" {
  description = "Private subnet ids for the DB subnet group."
  type        = list(string)

  validation {
    condition     = length(var.subnet_ids) >= 2
    error_message = "RDS requires subnets in at least two AZs."
  }
}

variable "allowed_security_group_ids" {
  description = "Security groups allowed to reach Postgres (control plane only; runners must never connect)."
  type        = list(string)
}

variable "engine_version" {
  description = "PostgreSQL engine version."
  type        = string
  default     = "16.8"
}

variable "instance_class" {
  description = "RDS instance class."
  type        = string
  default     = "db.t4g.micro"
}

variable "allocated_storage_gb" {
  description = "Initial storage in GiB."
  type        = number
  default     = 20
}

variable "max_allocated_storage_gb" {
  description = "Storage autoscaling ceiling in GiB."
  type        = number
  default     = 100
}

variable "multi_az" {
  description = "Enable Multi-AZ standby."
  type        = bool
  default     = false
}

variable "deletion_protection" {
  description = "Protect the instance from deletion."
  type        = bool
  default     = true
}

variable "backup_retention_days" {
  description = "Automated backup retention."
  type        = number
  default     = 7
}

variable "tags" {
  description = "Tags applied to every resource."
  type        = map(string)
  default     = {}
}
