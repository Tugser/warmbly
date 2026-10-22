variable "github_repository" {
  description = "Repository allowed to deploy through the protected GitHub environment"
  type        = string
  default     = null
}

variable "github_environment" {
  type    = string
  default = "aws-production"
}

resource "aws_iam_openid_connect_provider" "github" {
  count          = var.github_repository == null ? 0 : 1
  url            = "https://token.actions.githubusercontent.com"
  client_id_list = ["sts.amazonaws.com"]
}

data "aws_iam_policy_document" "github_trust" {
  count = var.github_repository == null ? 0 : 1
  statement {
    actions = ["sts:AssumeRoleWithWebIdentity"]
    principals {
      type        = "Federated"
      identifiers = [aws_iam_openid_connect_provider.github[0].arn]
    }
    condition {
      test     = "StringEquals"
      variable = "token.actions.githubusercontent.com:aud"
      values   = ["sts.amazonaws.com"]
    }
    condition {
      test     = "StringEquals"
      variable = "token.actions.githubusercontent.com:sub"
      values   = ["repo:${var.github_repository}:environment:${var.github_environment}"]
    }
  }
}

resource "aws_iam_role" "github_deploy" {
  count                = var.github_repository == null ? 0 : 1
  name                 = "${var.name}-github-deploy"
  assume_role_policy   = data.aws_iam_policy_document.github_trust[0].json
  max_session_duration = 3600
}

data "aws_iam_policy_document" "github_deploy" {
  count = var.github_repository == null ? 0 : 1
  statement {
    actions   = ["ecs:DescribeTaskDefinition"]
    resources = ["*"]
  }
  statement {
    actions   = ["ecs:DescribeServices", "ecs:UpdateService"]
    resources = [for service in ["backend", "consumer"] : "arn:aws:ecs:us-east-1:${data.aws_caller_identity.current.account_id}:service/${var.name}/${var.name}-${service}"]
  }
  statement {
    actions   = ["ecs:DescribeTasks"]
    resources = ["arn:aws:ecs:us-east-1:${data.aws_caller_identity.current.account_id}:task/${var.name}/*"]
  }
  statement {
    actions   = ["ecs:RunTask"]
    resources = ["arn:aws:ecs:us-east-1:${data.aws_caller_identity.current.account_id}:task-definition/${var.name}-migration:*"]
    condition {
      test     = "ArnEquals"
      variable = "ecs:cluster"
      values   = [local.foundation.cluster_arn]
    }
  }
  statement {
    actions = ["iam:PassRole"]
    resources = concat([aws_iam_role.migration_execution.arn], flatten([
      for name in ["backend", "consumer"] : [aws_iam_role.execution[name].arn, aws_iam_role.application[name].arn]
    ]))
    condition {
      test     = "StringEquals"
      variable = "iam:PassedToService"
      values   = ["ecs-tasks.amazonaws.com"]
    }
  }
}

resource "aws_iam_role_policy" "github_deploy" {
  count  = var.github_repository == null ? 0 : 1
  name   = "migrate-before-deploy"
  role   = aws_iam_role.github_deploy[0].id
  policy = data.aws_iam_policy_document.github_deploy[0].json
}

output "github_deploy_role_arn" {
  value = var.github_repository == null ? null : aws_iam_role.github_deploy[0].arn
}
