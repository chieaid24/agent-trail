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

output "cluster_arn" {
  description = "ECS cluster ARN (the dashboard service joins this cluster)."
  value       = aws_ecs_cluster.this.arn
}

output "https_listener_arn" {
  description = "HTTPS listener ARN for path routing rules."
  value       = aws_lb_listener.https.arn
}

output "target_group_arn" {
  description = "Control plane target group ARN for listener rules."
  value       = aws_lb_target_group.this.arn
}

output "alb_security_group_id" {
  description = "ALB security group (grant it egress to other services)."
  value       = aws_security_group.alb.id
}

output "container_port" {
  description = "Port the control plane listens on."
  value       = var.container_port
}

output "execution_role_arn" {
  description = "IAM role ECS assumes to start control plane tasks."
  value       = aws_iam_role.execution.arn
}

output "service_connect_namespace_arn" {
  description = "Service Connect namespace ARN shared by the cluster's services."
  value       = aws_service_discovery_http_namespace.this.arn
}

output "service_connect_url" {
  description = "In-VPC URL other services use to reach the control plane."
  value       = "http://${local.discovery_name}:${var.container_port}"
}
