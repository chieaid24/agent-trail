output "queue_url" {
  description = "Task dispatch queue URL."
  value       = aws_sqs_queue.task_dispatch.url
}

output "queue_name" {
  description = "Task dispatch queue name for CloudWatch dimensions."
  value       = aws_sqs_queue.task_dispatch.name
}

output "dead_letter_queue_name" {
  description = "Dead-letter queue name for CloudWatch dimensions."
  value       = aws_sqs_queue.dead_letter.name
}

output "queue_arn" {
  description = "Task dispatch queue ARN."
  value       = aws_sqs_queue.task_dispatch.arn
}

output "dead_letter_queue_arn" {
  description = "Dead-letter queue ARN."
  value       = aws_sqs_queue.dead_letter.arn
}
