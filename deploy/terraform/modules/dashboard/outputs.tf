output "service_name" {
  description = "ECS service name (CI deploys by updating this service)."
  value       = aws_ecs_service.this.name
}

output "service_security_group_id" {
  description = "Security group of the dashboard tasks."
  value       = aws_security_group.service.id
}

output "target_group_arn_suffix" {
  description = "Target group ARN suffix for CloudWatch dimensions."
  value       = aws_lb_target_group.this.arn_suffix
}

output "execution_role_arn" {
  description = "IAM role ECS assumes to pull the image and write logs."
  value       = aws_iam_role.execution.arn
}
