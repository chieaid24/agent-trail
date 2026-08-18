output "ci_role_arn" {
  description = "IAM role GitHub Actions assumes to deploy."
  value       = aws_iam_role.ci.arn
}

output "oidc_provider_arn" {
  description = "GitHub OIDC provider ARN in use."
  value       = local.oidc_provider_arn
}
