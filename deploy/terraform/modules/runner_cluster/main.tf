data "aws_iam_policy_document" "cluster_assume" {
  statement {
    actions = ["sts:AssumeRole"]

    principals {
      type        = "Service"
      identifiers = ["eks.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "cluster" {
  name               = "${var.name}-eks-cluster"
  assume_role_policy = data.aws_iam_policy_document.cluster_assume.json

  tags = var.tags
}

resource "aws_iam_role_policy_attachment" "cluster" {
  role       = aws_iam_role.cluster.name
  policy_arn = "arn:aws:iam::aws:policy/AmazonEKSClusterPolicy"
}

resource "aws_eks_cluster" "this" {
  name     = var.name
  role_arn = aws_iam_role.cluster.arn
  version  = var.kubernetes_version

  vpc_config {
    subnet_ids              = var.private_subnet_ids
    endpoint_public_access  = false
    endpoint_private_access = true
  }

  access_config {
    authentication_mode = "API"
  }

  enabled_cluster_log_types = ["api", "audit", "authenticator"]

  tags = var.tags

  depends_on = [aws_iam_role_policy_attachment.cluster]
}

data "aws_iam_policy_document" "node_assume" {
  statement {
    actions = ["sts:AssumeRole"]

    principals {
      type        = "Service"
      identifiers = ["ec2.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "node" {
  name               = "${var.name}-eks-node"
  assume_role_policy = data.aws_iam_policy_document.node_assume.json

  tags = var.tags
}

resource "aws_iam_role_policy_attachment" "node" {
  for_each = toset([
    "arn:aws:iam::aws:policy/AmazonEKSWorkerNodePolicy",
    "arn:aws:iam::aws:policy/AmazonEKS_CNI_Policy",
    "arn:aws:iam::aws:policy/AmazonEC2ContainerRegistryReadOnly",
  ])

  role       = aws_iam_role.node.name
  policy_arn = each.value
}

resource "aws_eks_node_group" "runners" {
  cluster_name    = aws_eks_cluster.this.name
  node_group_name = "runners"
  node_role_arn   = aws_iam_role.node.arn
  subnet_ids      = var.private_subnet_ids
  instance_types  = var.node_instance_types
  ami_type        = "AL2023_ARM_64_STANDARD"

  scaling_config {
    min_size     = var.node_min_size
    max_size     = var.node_max_size
    desired_size = var.node_desired_size
  }

  update_config {
    max_unavailable = 1
  }

  labels = {
    "agent-trail.dev/role" = "runner"
  }

  tags = var.tags

  depends_on = [aws_iam_role_policy_attachment.node]

  lifecycle {
    ignore_changes = [scaling_config[0].desired_size]
  }
}

# IRSA: OIDC provider so in-cluster service accounts assume narrow IAM roles.
data "tls_certificate" "oidc" {
  url = aws_eks_cluster.this.identity[0].oidc[0].issuer
}

resource "aws_iam_openid_connect_provider" "this" {
  url             = aws_eks_cluster.this.identity[0].oidc[0].issuer
  client_id_list  = ["sts.amazonaws.com"]
  thumbprint_list = [data.tls_certificate.oidc.certificates[0].sha1_fingerprint]

  tags = var.tags
}

locals {
  oidc_hostpath = replace(aws_eks_cluster.this.identity[0].oidc[0].issuer, "https://", "")
}

# Runner controller: consumes the dispatch queue, creates Jobs, reads only
# the runner-safe secrets. Kubernetes RBAC (Job create in the runner
# namespace) lives in the deploy/k8s manifests, not IAM.
data "aws_iam_policy_document" "controller_assume" {
  statement {
    actions = ["sts:AssumeRoleWithWebIdentity"]

    principals {
      type        = "Federated"
      identifiers = [aws_iam_openid_connect_provider.this.arn]
    }

    condition {
      test     = "StringEquals"
      variable = "${local.oidc_hostpath}:sub"
      values   = ["system:serviceaccount:${var.runner_namespace}:runner-controller"]
    }

    condition {
      test     = "StringEquals"
      variable = "${local.oidc_hostpath}:aud"
      values   = ["sts.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "runner_controller" {
  name               = "${var.name}-runner-controller"
  assume_role_policy = data.aws_iam_policy_document.controller_assume.json

  tags = var.tags
}

data "aws_iam_policy_document" "runner_controller" {
  statement {
    sid = "ConsumeDispatchQueue"
    actions = [
      "sqs:ReceiveMessage",
      "sqs:DeleteMessage",
      "sqs:GetQueueAttributes",
      "sqs:ChangeMessageVisibility",
    ]
    resources = [var.task_dispatch_queue_arn]
  }

  statement {
    sid       = "RunnerSecrets"
    actions   = ["secretsmanager:GetSecretValue"]
    resources = var.runner_secret_arns
  }
}

resource "aws_iam_role_policy" "runner_controller" {
  name   = "runner-controller"
  role   = aws_iam_role.runner_controller.id
  policy = data.aws_iam_policy_document.runner_controller.json
}

# Runner task: what an individual task Job may do - upload artifacts, nothing else.
data "aws_iam_policy_document" "task_assume" {
  statement {
    actions = ["sts:AssumeRoleWithWebIdentity"]

    principals {
      type        = "Federated"
      identifiers = [aws_iam_openid_connect_provider.this.arn]
    }

    condition {
      test     = "StringEquals"
      variable = "${local.oidc_hostpath}:sub"
      values   = ["system:serviceaccount:${var.runner_namespace}:runner-task"]
    }

    condition {
      test     = "StringEquals"
      variable = "${local.oidc_hostpath}:aud"
      values   = ["sts.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "runner_task" {
  name               = "${var.name}-runner-task"
  assume_role_policy = data.aws_iam_policy_document.task_assume.json

  tags = var.tags
}

data "aws_iam_policy_document" "runner_task" {
  statement {
    sid       = "UploadArtifacts"
    actions   = ["s3:PutObject"]
    resources = ["${var.artifacts_bucket_arn}/task-artifacts/*"]
  }
}

resource "aws_iam_role_policy" "runner_task" {
  name   = "runner-task"
  role   = aws_iam_role.runner_task.id
  policy = data.aws_iam_policy_document.runner_task.json
}
