variable "migration_secret_key" {
  description = "Direct database URL key in the backend's pinned secret bundle"
  type        = string
  default     = "PRIMARY_DB_DIRECT"
  validation {
    condition     = contains(["PRIMARY_DB", "PRIMARY_DB_DIRECT"], var.migration_secret_key)
    error_message = "Migration credentials must come from the backend database configuration."
  }
}

resource "aws_cloudwatch_log_group" "migration" {
  name              = "/ecs/${var.name}/migration"
  retention_in_days = 14
}

resource "aws_iam_role" "migration_execution" {
  name               = "${var.name}-migration-execution"
  assume_role_policy = data.aws_iam_policy_document.trust.json
}

data "aws_iam_policy_document" "migration_execution" {
  statement {
    actions   = ["secretsmanager:GetSecretValue"]
    resources = [local.foundation.secret_arns["backend"]]
  }
  statement {
    actions   = ["logs:CreateLogStream", "logs:PutLogEvents"]
    resources = ["${aws_cloudwatch_log_group.migration.arn}:*"]
  }
  statement {
    actions   = ["ecr:GetAuthorizationToken"]
    resources = ["*"]
  }
  statement {
    actions   = ["ecr:BatchCheckLayerAvailability", "ecr:GetDownloadUrlForLayer", "ecr:BatchGetImage"]
    resources = ["arn:aws:ecr:us-east-1:${data.aws_caller_identity.current.account_id}:repository/${split(".amazonaws.com/", local.foundation.repository_urls["backend"])[1]}"]
  }
}

resource "aws_iam_role_policy" "migration_execution" {
  name   = "migration-configuration-and-logs"
  role   = aws_iam_role.migration_execution.id
  policy = data.aws_iam_policy_document.migration_execution.json
}

resource "aws_ecs_task_definition" "migration" {
  skip_destroy             = true
  family                   = "${var.name}-migration"
  requires_compatibilities = ["FARGATE"]
  network_mode             = "awsvpc"
  cpu                      = "256"
  memory                   = "512"
  execution_role_arn       = aws_iam_role.migration_execution.arn
  runtime_platform {
    cpu_architecture        = "ARM64"
    operating_system_family = "LINUX"
  }
  container_definitions = jsonencode([{
    name       = "migration"
    image      = var.services["backend"].image
    essential  = true
    entryPoint = ["/app/migrate"]
    command    = []
    environment = [
      { name = "DB_POOL_MODE", value = "direct" }
    ]
    secrets = [{
      name      = "PRIMARY_DB"
      valueFrom = "${local.foundation.secret_arns["backend"]}:${var.migration_secret_key}::${var.services["backend"].secret_version_id}"
    }]
    logConfiguration = {
      logDriver = "awslogs"
      options = {
        awslogs-group         = aws_cloudwatch_log_group.migration.name
        awslogs-region        = "us-east-1"
        awslogs-stream-prefix = "migration"
      }
    }
  }])
  depends_on = [aws_iam_role_policy.migration_execution]
}

output "migration_task_definition" {
  value = aws_ecs_task_definition.migration.arn
}

resource "aws_ecs_task_definition" "database_check" {
  skip_destroy             = true
  family                   = "${var.name}-database-check"
  requires_compatibilities = ["FARGATE"]
  network_mode             = "awsvpc"
  cpu                      = "256"
  memory                   = "512"
  execution_role_arn       = aws_iam_role.execution["backend"].arn
  task_role_arn            = aws_iam_role.application["backend"].arn
  runtime_platform {
    cpu_architecture        = "ARM64"
    operating_system_family = "LINUX"
  }
  container_definitions = jsonencode([merge({
    for key, value in local.service_containers["backend"] : key => value if !contains(["portMappings", "healthCheck"], key)
    }, {
    name       = "database-check"
    entryPoint = ["/bin/sh", "-c"]
    command    = ["warmblyctl status --json >/tmp/status.json && echo 'Go pooled database client and schema queries succeeded'"]
  })])
}

output "database_check_task_definition" {
  value = aws_ecs_task_definition.database_check.arn
}

resource "aws_ecs_task_definition" "realtime_database_check" {
  skip_destroy             = true
  family                   = "${var.name}-realtime-database-check"
  requires_compatibilities = ["FARGATE"]
  network_mode             = "awsvpc"
  cpu                      = "256"
  memory                   = "512"
  execution_role_arn       = aws_iam_role.execution["realtime"].arn
  runtime_platform {
    cpu_architecture        = "ARM64"
    operating_system_family = "LINUX"
  }
  container_definitions = jsonencode([merge({
    for key, value in local.service_containers["realtime"] : key => value if !contains(["portMappings", "healthCheck"], key)
    }, {
    name       = "realtime-database-check"
    entryPoint = ["/app/bin/realtime", "eval"]
    command    = [file("${path.module}/checks/realtime.exs")]
  })])
}

output "realtime_database_check_task_definition" {
  value = aws_ecs_task_definition.realtime_database_check.arn
}
