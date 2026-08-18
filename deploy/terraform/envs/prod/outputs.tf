output "control_plane_url" {
  description = "Control plane HTTPS endpoint."
  value       = "https://${module.dns_tls.domain_name}"
}

output "ecr_repository_urls" {
  description = "Where CI pushes images."
  value       = module.container_registry.repository_urls
}

output "ci_role_arn" {
  description = "Role GitHub Actions assumes to deploy."
  value       = module.github_oidc_ci.ci_role_arn
}

output "runner_cluster_name" {
  description = "EKS cluster running task Jobs."
  value       = module.runner_cluster.cluster_name
}
