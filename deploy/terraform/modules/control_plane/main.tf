locals {
  container_name                  = "control-plane"
  cloudwatch_agent_container_name = "cloudwatch-agent"
  cloudwatch_agent_image          = "public.ecr.aws/cloudwatch-agent/cloudwatch-agent:1.300071.0b1720-arm64@sha256:b063fe88e714d31c6e4f2f0f217fc5fe328855ad314e453901d3f97398acb4b7"
  port_name                       = "http"
  # Service Connect name; the dashboard proxies to http://control-plane:<container_port>
  discovery_name = "control-plane"
}

resource "aws_security_group" "alb" {
  name        = "${var.name}-alb"
  description = "ALB ingress for ${var.name}"
  vpc_id      = var.vpc_id

  tags = merge(var.tags, { Name = "${var.name}-alb" })
}

resource "aws_vpc_security_group_ingress_rule" "alb_https" {
  security_group_id = aws_security_group.alb.id
  cidr_ipv4         = "0.0.0.0/0"
  from_port         = 443
  to_port           = 443
  ip_protocol       = "tcp"
  description       = "HTTPS from anywhere"
}

resource "aws_vpc_security_group_egress_rule" "alb_to_service" {
  security_group_id            = aws_security_group.alb.id
  referenced_security_group_id = aws_security_group.service.id
  from_port                    = var.container_port
  to_port                      = var.container_port
  ip_protocol                  = "tcp"
  description                  = "To control plane tasks"
}

resource "aws_security_group" "service" {
  name        = "${var.name}-control-plane"
  description = "Control plane tasks for ${var.name}"
  vpc_id      = var.vpc_id

  tags = merge(var.tags, { Name = "${var.name}-control-plane" })
}

resource "aws_vpc_security_group_ingress_rule" "service_from_alb" {
  security_group_id            = aws_security_group.service.id
  referenced_security_group_id = aws_security_group.alb.id
  from_port                    = var.container_port
  to_port                      = var.container_port
  ip_protocol                  = "tcp"
  description                  = "From ALB"
}

resource "aws_vpc_security_group_egress_rule" "service_all" {
  security_group_id = aws_security_group.service.id
  cidr_ipv4         = "0.0.0.0/0"
  ip_protocol       = "-1"
  description       = "Outbound to GitHub, AWS APIs, EKS API"
}

resource "aws_lb" "this" {
  name               = var.name
  load_balancer_type = "application"
  security_groups    = [aws_security_group.alb.id]
  subnets            = var.public_subnet_ids

  drop_invalid_header_fields = true

  tags = var.tags
}

resource "aws_lb_target_group" "this" {
  name        = var.name
  port        = var.container_port
  protocol    = "HTTP"
  vpc_id      = var.vpc_id
  target_type = "ip"

  health_check {
    path                = "/healthz"
    matcher             = "200"
    interval            = 15
    healthy_threshold   = 2
    unhealthy_threshold = 3
  }

  deregistration_delay = 30

  tags = var.tags
}

resource "aws_lb_listener" "https" {
  load_balancer_arn = aws_lb.this.arn
  port              = 443
  protocol          = "HTTPS"
  ssl_policy        = "ELBSecurityPolicy-TLS13-1-2-2021-06"
  certificate_arn   = var.certificate_arn

  default_action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.this.arn
  }
}

resource "aws_cloudwatch_log_group" "this" {
  name              = "/ecs/${var.name}/control-plane"
  retention_in_days = var.log_retention_days

  tags = var.tags
}

data "aws_iam_policy_document" "ecs_assume" {
  statement {
    actions = ["sts:AssumeRole"]

    principals {
      type        = "Service"
      identifiers = ["ecs-tasks.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "execution" {
  name               = "${var.name}-control-plane-execution"
  assume_role_policy = data.aws_iam_policy_document.ecs_assume.json

  tags = var.tags
}

resource "aws_iam_role_policy_attachment" "execution_managed" {
  role       = aws_iam_role.execution.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AmazonECSTaskExecutionRolePolicy"
}

data "aws_iam_policy_document" "execution_secrets" {
  statement {
    actions   = ["secretsmanager:GetSecretValue"]
    resources = var.secret_arns
  }
}

resource "aws_iam_role_policy" "execution_secrets" {
  name   = "read-injected-secrets"
  role   = aws_iam_role.execution.id
  policy = data.aws_iam_policy_document.execution_secrets.json
}

resource "aws_iam_role" "task" {
  name               = "${var.name}-control-plane"
  assume_role_policy = data.aws_iam_policy_document.ecs_assume.json

  tags = var.tags
}

data "aws_iam_policy_document" "task" {
  statement {
    sid = "Artifacts"
    actions = [
      "s3:GetObject",
      "s3:PutObject",
      "s3:DeleteObject",
      "s3:ListBucket",
    ]
    resources = [
      var.artifacts_bucket_arn,
      "${var.artifacts_bucket_arn}/*",
    ]
  }

  statement {
    sid = "TaskDispatch"
    actions = [
      "sqs:SendMessage",
      "sqs:GetQueueAttributes",
    ]
    resources = [var.task_dispatch_queue_arn]
  }

  statement {
    sid       = "Secrets"
    actions   = ["secretsmanager:GetSecretValue"]
    resources = var.secret_arns
  }

  statement {
    sid = "PublishOTLPTelemetry"
    actions = [
      "cloudwatch:PutMetricData",
      "xray:PutTraceSegments",
    ]
    resources = ["*"]
  }
}

resource "aws_iam_role_policy" "task" {
  name   = "control-plane"
  role   = aws_iam_role.task.id
  policy = data.aws_iam_policy_document.task.json
}

resource "aws_ecs_cluster" "this" {
  name = var.name

  setting {
    name  = "containerInsights"
    value = "enabled"
  }

  tags = var.tags
}

resource "aws_service_discovery_http_namespace" "this" {
  name        = var.name
  description = "Service Connect namespace for ${var.name}"

  tags = var.tags
}

resource "aws_ecs_task_definition" "this" {
  family                   = "${var.name}-control-plane"
  requires_compatibilities = ["FARGATE"]
  network_mode             = "awsvpc"
  cpu                      = var.cpu
  memory                   = var.memory
  execution_role_arn       = aws_iam_role.execution.arn
  task_role_arn            = aws_iam_role.task.arn

  runtime_platform {
    operating_system_family = "LINUX"
    cpu_architecture        = "ARM64"
  }

  container_definitions = jsonencode([
    {
      name      = local.container_name
      image     = var.image
      essential = true

      portMappings = [
        {
          name          = local.port_name
          containerPort = var.container_port
          protocol      = "tcp"
          appProtocol   = "http"
        }
      ]

      environment = [
        for k, v in merge(var.environment, {
          OTEL_EXPORTER_OTLP_ENDPOINT = "localhost:4317"
        }) : { name = k, value = v }
      ]

      secrets = [
        for k, arn in var.container_secrets : { name = k, valueFrom = arn }
      ]

      readonlyRootFilesystem = true

      dependsOn = [
        {
          containerName = local.cloudwatch_agent_container_name
          condition     = "HEALTHY"
        }
      ]

      logConfiguration = {
        logDriver = "awslogs"
        options = {
          "awslogs-group"         = aws_cloudwatch_log_group.this.name
          "awslogs-region"        = data.aws_region.current.region
          "awslogs-stream-prefix" = "control-plane"
        }
      }

      healthCheck = {
        command     = ["CMD-SHELL", "wget -q -O /dev/null http://localhost:${var.container_port}/healthz || exit 1"]
        interval    = 15
        timeout     = 5
        retries     = 3
        startPeriod = 15
      }
    },
    {
      name              = local.cloudwatch_agent_container_name
      image             = local.cloudwatch_agent_image
      essential         = true
      cpu               = 128
      memory            = 512
      memoryReservation = 256

      environment = [
        {
          name  = "CW_CONFIG_CONTENT"
          value = file("${path.module}/cloudwatch-agent.json")
        }
      ]

      portMappings = [
        {
          containerPort = 4317
          protocol      = "tcp"
        },
        {
          containerPort = 4318
          protocol      = "tcp"
        }
      ]

      logConfiguration = {
        logDriver = "awslogs"
        options = {
          "awslogs-group"         = aws_cloudwatch_log_group.this.name
          "awslogs-region"        = data.aws_region.current.region
          "awslogs-stream-prefix" = "cloudwatch-agent"
        }
      }

      healthCheck = {
        command = [
          "CMD",
          "/opt/aws/amazon-cloudwatch-agent/bin/config-translator",
          "-output",
          "/tmp/health.toml",
          "-mode",
          "onPremise",
          "-os",
          "linux",
        ]
        interval    = 15
        timeout     = 5
        retries     = 3
        startPeriod = 20
      }
    }
  ])

  tags = var.tags
}

data "aws_region" "current" {}

resource "aws_ecs_service" "this" {
  name            = "control-plane"
  cluster         = aws_ecs_cluster.this.id
  task_definition = aws_ecs_task_definition.this.arn
  desired_count   = var.desired_count
  launch_type     = "FARGATE"

  network_configuration {
    subnets          = var.private_subnet_ids
    security_groups  = [aws_security_group.service.id]
    assign_public_ip = false
  }

  load_balancer {
    target_group_arn = aws_lb_target_group.this.arn
    container_name   = local.container_name
    container_port   = var.container_port
  }

  deployment_circuit_breaker {
    enable   = true
    rollback = true
  }

  service_connect_configuration {
    enabled   = true
    namespace = aws_service_discovery_http_namespace.this.arn

    service {
      port_name      = local.port_name
      discovery_name = local.discovery_name

      client_alias {
        port     = var.container_port
        dns_name = local.discovery_name
      }
    }
  }

  depends_on = [aws_lb_listener.https]

  tags = var.tags
}
