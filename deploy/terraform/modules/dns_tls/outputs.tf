output "certificate_arn" {
  description = "Validated ACM certificate ARN for the ALB listener."
  value       = aws_acm_certificate_validation.this.certificate_arn
}

output "zone_id" {
  description = "Hosted zone id in use."
  value       = local.zone_id
}

output "domain_name" {
  description = "Control plane domain."
  value       = var.domain_name
}
