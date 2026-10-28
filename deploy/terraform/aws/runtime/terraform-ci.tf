variable "terraform_ci" {
  description = "Create the OIDC roles terraform-aws.yml plans and applies the AWS stacks with"
  type        = bool
  default     = false
}

locals {
  terraform_ci = var.terraform_ci && var.github_repository != null
}

data "aws_iam_policy_document" "terraform_trust" {
  for_each = local.terraform_ci ? {
    plan  = ["aws-infrastructure-plan"]
    apply = ["aws-infrastructure", "aws-infrastructure-approval"]
  } : {}
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
      values   = [for environment in each.value : "repo:${var.github_repository}:environment:${environment}"]
    }
  }
}

resource "aws_iam_role" "terraform" {
  for_each             = data.aws_iam_policy_document.terraform_trust
  name                 = "${var.name}-terraform-${each.key}"
  assume_role_policy   = each.value.json
  max_session_duration = 3600
}

# Plans run on pull requests, so the plan role reads configuration and never data.
resource "aws_iam_role_policy_attachment" "terraform_plan_read" {
  count      = local.terraform_ci ? 1 : 0
  role       = aws_iam_role.terraform["plan"].name
  policy_arn = "arn:aws:iam::aws:policy/ReadOnlyAccess"
}
data "aws_iam_policy_document" "terraform_plan" {
  count = local.terraform_ci ? 1 : 0
  statement {
    actions   = ["s3:PutObject", "s3:DeleteObject"]
    resources = ["arn:aws:s3:::${var.state_bucket}/warmbly/production/*.tflock"]
  }
  statement {
    effect = "Deny"
    actions = [
      "secretsmanager:GetSecretValue", "kms:Decrypt",
      "logs:GetLogEvents", "logs:FilterLogEvents", "logs:StartQuery", "logs:GetQueryResults",
      "dynamodb:GetItem", "dynamodb:BatchGetItem", "dynamodb:Query", "dynamodb:Scan",
      "sqs:ReceiveMessage", "kinesis:GetRecords", "athena:GetQueryResults", "ecr:GetDownloadUrlForLayer",
    ]
    resources = ["*"]
  }
  statement {
    effect    = "Deny"
    actions   = ["ssm:GetParameter*"]
    resources = ["arn:aws:ssm:us-east-1:${data.aws_caller_identity.current.account_id}:parameter/${var.name}/*"]
  }
  statement {
    effect        = "Deny"
    actions       = ["s3:GetObject", "s3:GetObjectVersion"]
    not_resources = ["arn:aws:s3:::${var.state_bucket}/warmbly/production/*"]
  }
}
resource "aws_iam_role_policy" "terraform_plan" {
  count  = local.terraform_ci ? 1 : 0
  name   = "state-lock-and-no-data"
  role   = aws_iam_role.terraform["plan"].id
  policy = data.aws_iam_policy_document.terraform_plan[0].json
}

# The apply role administers the stacks; it is reachable only from main through the protected environments.
resource "aws_iam_role_policy_attachment" "terraform_apply_power" {
  count      = local.terraform_ci ? 1 : 0
  role       = aws_iam_role.terraform["apply"].name
  policy_arn = "arn:aws:iam::aws:policy/PowerUserAccess"
}
data "aws_iam_policy_document" "terraform_apply" {
  count = local.terraform_ci ? 1 : 0
  statement {
    actions   = ["iam:Get*", "iam:List*", "iam:CreateServiceLinkedRole"]
    resources = ["*"]
  }
  statement {
    actions = ["iam:*"]
    resources = [
      "arn:aws:iam::${data.aws_caller_identity.current.account_id}:role/${var.name}-*",
      "arn:aws:iam::${data.aws_caller_identity.current.account_id}:instance-profile/${var.name}-*",
      "arn:aws:iam::${data.aws_caller_identity.current.account_id}:oidc-provider/token.actions.githubusercontent.com",
    ]
  }
  statement {
    effect    = "Deny"
    actions   = ["iam:AttachRolePolicy", "iam:PutRolePermissionsBoundary"]
    resources = ["*"]
    condition {
      test     = "ArnLike"
      variable = "iam:PolicyARN"
      values   = ["arn:aws:iam::aws:policy/AdministratorAccess", "arn:aws:iam::aws:policy/IAMFullAccess"]
    }
  }
  # The CI roles cannot change their own permissions or trust; an administrator applies those by hand.
  statement {
    effect = "Deny"
    actions = [
      "iam:PutRolePolicy", "iam:DeleteRolePolicy", "iam:AttachRolePolicy", "iam:DetachRolePolicy",
      "iam:UpdateAssumeRolePolicy", "iam:PutRolePermissionsBoundary", "iam:DeleteRolePermissionsBoundary",
      "iam:DeleteRole", "iam:UpdateRole",
    ]
    resources = ["arn:aws:iam::${data.aws_caller_identity.current.account_id}:role/${var.name}-terraform-*"]
  }
}
resource "aws_iam_role_policy" "terraform_apply" {
  count  = local.terraform_ci ? 1 : 0
  name   = "manage-warmbly-roles"
  role   = aws_iam_role.terraform["apply"].id
  policy = data.aws_iam_policy_document.terraform_apply[0].json
}

output "terraform_role_arns" {
  value = { for name, role in aws_iam_role.terraform : name => role.arn }
}
