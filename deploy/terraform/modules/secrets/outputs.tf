output "secret_arns" {
  description = "Map of secret key to Secrets Manager ARN."
  value       = { for k, s in aws_secretsmanager_secret.this : k => s.arn }
}

output "control_plane_secret_arns" {
  description = "Secret ARNs the control plane task role may read. Excludes nothing today; the runner list below is the restricted one."
  value       = [for k, s in aws_secretsmanager_secret.this : s.arn]
}

output "runner_secret_arns" {
  description = "Secret ARNs the runner may receive. Never includes the GitHub App private key."
  value       = [aws_secretsmanager_secret.this["agent_provider_api_key"].arn]
}
