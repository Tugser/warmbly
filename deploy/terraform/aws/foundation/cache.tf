resource "aws_secretsmanager_secret" "cache" {
  name                    = "${var.name}/cache"
  description             = "Valkey password; values are provisioned outside Terraform"
  recovery_window_in_days = 30
}

ephemeral "aws_secretsmanager_secret_version" "cache" {
  secret_id = aws_secretsmanager_secret.cache.id
}

resource "aws_security_group" "cache" {
  name_prefix = "${var.name}-cache-"
  description = "Valkey access from application tasks only"
  vpc_id      = aws_vpc.main.id
}

resource "aws_vpc_security_group_ingress_rule" "cache" {
  security_group_id            = aws_security_group.cache.id
  referenced_security_group_id = aws_security_group.tasks.id
  ip_protocol                  = "tcp"
  from_port                    = 6379
  to_port                      = 6379
}

resource "aws_elasticache_subnet_group" "cache" {
  name       = "${var.name}-cache"
  subnet_ids = aws_subnet.private[*].id
}

resource "aws_elasticache_parameter_group" "cache" {
  name   = "${var.name}-cache"
  family = "valkey9"
  parameter {
    name  = "maxmemory-policy"
    value = "noeviction"
  }
}

resource "aws_elasticache_replication_group" "cache" {
  replication_group_id       = "${var.name}-cache"
  description                = "Shared application cache with standalone Redis protocol compatibility"
  engine                     = "valkey"
  engine_version             = "9.0"
  node_type                  = "cache.t4g.micro"
  num_cache_clusters         = 1
  automatic_failover_enabled = false
  multi_az_enabled           = false
  port                       = 6379
  subnet_group_name          = aws_elasticache_subnet_group.cache.name
  security_group_ids         = [aws_security_group.cache.id]
  parameter_group_name       = aws_elasticache_parameter_group.cache.name
  at_rest_encryption_enabled = true
  transit_encryption_enabled = true
  transit_encryption_mode    = "required"
  auth_token_wo              = jsondecode(ephemeral.aws_secretsmanager_secret_version.cache.secret_string).REDIS_PASSWORD
  auth_token_wo_version      = 1
  snapshot_retention_limit   = 1
  snapshot_window            = "05:00-06:00"
  maintenance_window         = "sun:06:00-sun:07:00"
  auto_minor_version_upgrade = true
  apply_immediately          = false

  tags = {
    managed_by       = "aws-skills"
    skill            = "elasticache"
    skill_version    = "1.0.0"
    created_by       = "elasticache-skill"
    generation_model = "gpt-6"
  }
}

output "cache" {
  value = {
    replication_group_id = aws_elasticache_replication_group.cache.id
    endpoint             = aws_elasticache_replication_group.cache.primary_endpoint_address
    port                 = aws_elasticache_replication_group.cache.port
    credential_secret    = aws_secretsmanager_secret.cache.arn
    tls_required         = true
    node_type            = aws_elasticache_replication_group.cache.node_type
    replica_count        = 0
  }
}
