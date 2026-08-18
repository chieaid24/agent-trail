output "endpoint" {
  description = "host:port endpoint."
  value       = aws_db_instance.this.endpoint
}

output "address" {
  description = "DNS address of the instance."
  value       = aws_db_instance.this.address
}

output "db_name" {
  description = "Initial database name."
  value       = aws_db_instance.this.db_name
}

output "master_user_secret_arn" {
  description = "Secrets Manager ARN of the RDS-managed master credentials."
  value       = aws_db_instance.this.master_user_secret[0].secret_arn
}

output "identifier" {
  description = "RDS instance identifier for CloudWatch dimensions."
  value       = aws_db_instance.this.identifier
}

output "security_group_id" {
  description = "Security group attached to the instance."
  value       = aws_security_group.this.id
}
