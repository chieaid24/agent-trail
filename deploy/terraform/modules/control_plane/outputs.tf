output "alb_dns_name" {
  description = "ALB DNS name for the dns_tls alias record."
  value       = aws_lb.this.dns_name
}

output "alb_zone_id" {
  description = "ALB hosted zone id for the alias record."
  value       = aws_lb.this.zone_id
}

output "alb_arn_suffix" {
  description = "ALB ARN suffix for CloudWatch dimensions."
  value       = aws_lb.this.arn_suffix
}

output "target_group_arn_suffix" {
  description = "Target group ARN suffix for CloudWatch dimensions."
  value       = aws_lb_target_group.this.arn_suffix
}

output "service_security_group_id" {
  description = "Security group of the control plane tasks (grant this Postgres access)."
  value       = aws_security_group.service.id
}

output "task_role_arn" {
  description = "IAM role the running control plane assumes."
  value       = aws_iam_role.task.arn
}

output "cluster_name" {
  description = "ECS cluster name."
  value       = aws_ecs_cluster.this.name
}

output "service_name" {
  description = "ECS service name (CI deploys by updating this service)."
  value       = aws_ecs_service.this.name
}
