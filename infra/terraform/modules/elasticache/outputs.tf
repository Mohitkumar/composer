output "endpoint" {
  description = "ElastiCache Redis endpoint"
  value       = aws_elasticache_replication_group.main.configuration_endpoint_address != "" ? aws_elasticache_replication_group.main.configuration_endpoint_address : aws_elasticache_replication_group.main.primary_endpoint_address
}

output "port" {
  description = "ElastiCache Redis port"
  value       = aws_elasticache_replication_group.main.port
}

output "auth_token" {
  description = "ElastiCache auth token"
  value       = var.auth_token_enabled ? random_password.redis_auth_token[0].result : null
  sensitive   = true
}

output "security_group_id" {
  description = "ElastiCache security group ID"
  value       = aws_security_group.redis.id
}

