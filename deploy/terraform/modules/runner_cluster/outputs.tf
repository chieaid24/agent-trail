output "cluster_name" {
  description = "EKS cluster name."
  value       = aws_eks_cluster.this.name
}

output "cluster_endpoint" {
  description = "EKS API endpoint (private)."
  value       = aws_eks_cluster.this.endpoint
}

output "oidc_provider_arn" {
  description = "IRSA OIDC provider ARN."
  value       = aws_iam_openid_connect_provider.this.arn
}

output "runner_controller_role_arn" {
  description = "IAM role for the runner-controller service account."
  value       = aws_iam_role.runner_controller.arn
}

output "runner_task_role_arn" {
  description = "IAM role for the runner-task service account."
  value       = aws_iam_role.runner_task.arn
}

output "runner_namespace" {
  description = "Namespace runner Jobs execute in."
  value       = var.runner_namespace
}
