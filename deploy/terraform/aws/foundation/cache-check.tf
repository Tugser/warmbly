data "aws_caller_identity" "current" {}

data "aws_iam_policy_document" "cache_check_trust" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["ecs-tasks.amazonaws.com"]
    }
    condition {
      test     = "StringEquals"
      variable = "aws:SourceAccount"
      values   = [data.aws_caller_identity.current.account_id]
    }
    condition {
      test     = "ArnLike"
      variable = "aws:SourceArn"
      values   = ["arn:aws:ecs:us-east-1:${data.aws_caller_identity.current.account_id}:*"]
    }
  }
}

resource "aws_iam_role" "cache_check" {
  name               = "${var.name}-cache-check"
  assume_role_policy = data.aws_iam_policy_document.cache_check_trust.json
}

resource "aws_cloudwatch_log_group" "cache_check" {
  name              = "/ecs/${var.name}/cache-check"
  retention_in_days = 1
}

data "aws_iam_policy_document" "cache_check" {
  statement {
    actions   = ["secretsmanager:GetSecretValue"]
    resources = [aws_secretsmanager_secret.cache.arn]
  }
  statement {
    actions   = ["logs:CreateLogStream", "logs:PutLogEvents"]
    resources = ["${aws_cloudwatch_log_group.cache_check.arn}:*"]
  }
}

resource "aws_iam_role_policy" "cache_check" {
  name   = "cache-connectivity-check"
  role   = aws_iam_role.cache_check.id
  policy = data.aws_iam_policy_document.cache_check.json
}

resource "aws_ecs_task_definition" "cache_check" {
  family                   = "${var.name}-cache-check"
  requires_compatibilities = ["FARGATE"]
  network_mode             = "awsvpc"
  cpu                      = "256"
  memory                   = "512"
  execution_role_arn       = aws_iam_role.cache_check.arn
  runtime_platform {
    cpu_architecture        = "ARM64"
    operating_system_family = "LINUX"
  }
  container_definitions = jsonencode([{
    name      = "cache-check"
    image     = "public.ecr.aws/docker/library/node:22-alpine"
    essential = true
    command = ["node", "-e", <<-JS
      const tls = require('node:tls');
      const host = process.env.CACHE_HOST;
      const password = process.env.REDIS_PASSWORD;
      const fail = () => { console.error('Valkey connection check failed'); process.exit(1); };
      const socket = tls.connect({ host, port: 6379, servername: host, rejectUnauthorized: true }, () => {
        socket.write('*2\r\n$4\r\nAUTH\r\n$' + Buffer.byteLength(password) + '\r\n' + password + '\r\n*1\r\n$4\r\nPING\r\n');
      });
      let reply = '';
      let verified = false;
      socket.setTimeout(15000, fail);
      socket.on('error', fail);
      socket.on('close', () => { if (!verified) fail(); });
      socket.on('data', chunk => {
        reply += chunk.toString();
        if (reply.includes('-')) fail();
        if (reply === '+OK\r\n+PONG\r\n') {
          verified = true;
          console.log('Valkey TLS and password authentication verified');
          socket.setTimeout(0);
          socket.end();
        }
      });
    JS
    ]
    environment = [{ name = "CACHE_HOST", value = aws_elasticache_replication_group.cache.primary_endpoint_address }]
    secrets     = [{ name = "REDIS_PASSWORD", valueFrom = "${aws_secretsmanager_secret.cache.arn}:REDIS_PASSWORD::" }]
    logConfiguration = {
      logDriver = "awslogs"
      options = {
        awslogs-group         = aws_cloudwatch_log_group.cache_check.name
        awslogs-region        = "us-east-1"
        awslogs-stream-prefix = "check"
      }
    }
  }])
  depends_on = [aws_iam_role_policy.cache_check]
}

output "cache_check_task_definition" {
  value = aws_ecs_task_definition.cache_check.arn
}
