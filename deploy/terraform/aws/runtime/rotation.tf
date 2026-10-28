# The rotator generates replacement keys inside AWS and writes them as new
# secret versions. Nothing outside this task ever holds a value; callers see
# version ids only.
resource "aws_cloudwatch_log_group" "rotator" {
  name              = "/ecs/${var.name}/rotator"
  retention_in_days = 30
}
resource "aws_iam_role" "rotator_execution" {
  name               = "${var.name}-rotator-execution"
  assume_role_policy = data.aws_iam_policy_document.trust.json
}
resource "aws_iam_role" "rotator" {
  name               = "${var.name}-rotator-task"
  assume_role_policy = data.aws_iam_policy_document.trust.json
}
data "aws_iam_policy_document" "rotator_execution" {
  statement {
    actions   = ["logs:CreateLogStream", "logs:PutLogEvents"]
    resources = ["${aws_cloudwatch_log_group.rotator.arn}:*"]
  }
}
resource "aws_iam_role_policy" "rotator_execution" {
  name   = "rotator-logs"
  role   = aws_iam_role.rotator_execution.id
  policy = data.aws_iam_policy_document.rotator_execution.json
}
data "aws_iam_policy_document" "rotator" {
  statement {
    actions   = ["secretsmanager:GetSecretValue", "secretsmanager:PutSecretValue", "secretsmanager:DescribeSecret"]
    resources = concat([for name in keys(var.services) : local.foundation.secret_arns[name]], [local.foundation.rotation_secret_arn])
  }
}
resource "aws_iam_role_policy" "rotator" {
  name   = "rotate-service-secrets"
  role   = aws_iam_role.rotator.id
  policy = data.aws_iam_policy_document.rotator.json
}
resource "aws_ecs_task_definition" "rotator" {
  skip_destroy             = true
  family                   = "${var.name}-rotator"
  requires_compatibilities = ["FARGATE"]
  network_mode             = "awsvpc"
  cpu                      = "256"
  memory                   = "512"
  execution_role_arn       = aws_iam_role.rotator_execution.arn
  task_role_arn            = aws_iam_role.rotator.arn
  runtime_platform {
    cpu_architecture        = "ARM64"
    operating_system_family = "LINUX"
  }
  container_definitions = jsonencode([{
    name      = "rotator"
    image     = "public.ecr.aws/docker/library/node:22-alpine"
    essential = true
    command   = ["sh", "-c", "npm install --prefix /tmp/rotate --omit=dev --ignore-scripts --no-audit --no-fund @aws-sdk/client-secrets-manager@3.1155.0 >/dev/null 2>&1 && printf '%s' \"$ROTATE_SOURCE\" > /tmp/rotate/rotate.cjs && NODE_PATH=/tmp/rotate/node_modules exec node /tmp/rotate/rotate.cjs"]
    environment = [
      { name = "AWS_REGION", value = "us-east-1" },
      { name = "ROTATE_ACTION", value = "status" },
      { name = "SERVICE_SECRET_ARNS", value = jsonencode({ for name in keys(var.services) : name => local.foundation.secret_arns[name] }) },
      { name = "ROTATION_SECRET_ARN", value = local.foundation.rotation_secret_arn },
      { name = "ROTATE_SOURCE", value = file("${path.module}/rotate/rotate.cjs") }
    ]
    logConfiguration = {
      logDriver = "awslogs"
      options = {
        awslogs-group         = aws_cloudwatch_log_group.rotator.name
        awslogs-region        = "us-east-1"
        awslogs-stream-prefix = "rotate"
      }
    }
  }])
  depends_on = [aws_iam_role_policy.rotator_execution, aws_iam_role_policy.rotator]
}
output "rotator_task_definition" { value = aws_ecs_task_definition.rotator.arn }
