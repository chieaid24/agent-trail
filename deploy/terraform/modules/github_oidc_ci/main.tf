data "aws_caller_identity" "current" {}

data "aws_region" "current" {}

locals {
  oidc_provider_arn = var.create_oidc_provider ? aws_iam_openid_connect_provider.github[0].arn : "arn:aws:iam::${data.aws_caller_identity.current.account_id}:oidc-provider/token.actions.githubusercontent.com"
}

resource "aws_iam_openid_connect_provider" "github" {
  count = var.create_oidc_provider ? 1 : 0

  url            = "https://token.actions.githubusercontent.com"
  client_id_list = ["sts.amazonaws.com"]

  tags = var.tags
}

data "aws_iam_policy_document" "ci_assume" {
  statement {
    actions = ["sts:AssumeRoleWithWebIdentity"]

    principals {
      type        = "Federated"
      identifiers = [local.oidc_provider_arn]
    }

    condition {
      test     = "StringEquals"
      variable = "token.actions.githubusercontent.com:aud"
      values   = ["sts.amazonaws.com"]
    }

    condition {
      test     = "StringEquals"
      variable = "token.actions.githubusercontent.com:sub"
      values   = ["repo:${var.github_repository}:ref:${var.deploy_ref}"]
    }
  }
}

resource "aws_iam_role" "ci" {
  name               = "${var.name}-ci-deploy"
  assume_role_policy = data.aws_iam_policy_document.ci_assume.json

  tags = var.tags
}

data "aws_iam_policy_document" "ci" {
  statement {
    sid       = "EcrLogin"
    actions   = ["ecr:GetAuthorizationToken"]
    resources = ["*"]
  }

  statement {
    sid = "EcrPush"
    actions = [
      "ecr:BatchCheckLayerAvailability",
      "ecr:CompleteLayerUpload",
      "ecr:InitiateLayerUpload",
      "ecr:PutImage",
      "ecr:UploadLayerPart",
      "ecr:BatchGetImage",
      "ecr:GetDownloadUrlForLayer",
    ]
    resources = var.ecr_repository_arns
  }

  statement {
    sid = "EcsDeploy"
    actions = [
      "ecs:DescribeServices",
      "ecs:DescribeTaskDefinition",
      "ecs:RegisterTaskDefinition",
      "ecs:UpdateService",
    ]
    resources = ["*"]

    condition {
      test     = "ArnEquals"
      variable = "ecs:cluster"
      values   = ["arn:aws:ecs:${data.aws_region.current.region}:${data.aws_caller_identity.current.account_id}:cluster/${var.ecs_cluster_name}"]
    }
  }

  statement {
    sid       = "PassTaskRoles"
    actions   = ["iam:PassRole"]
    resources = var.task_role_arns

    condition {
      test     = "StringEquals"
      variable = "iam:PassedToService"
      values   = ["ecs-tasks.amazonaws.com"]
    }
  }
}

resource "aws_iam_role_policy" "ci" {
  name   = "ci-deploy"
  role   = aws_iam_role.ci.id
  policy = data.aws_iam_policy_document.ci.json
}
