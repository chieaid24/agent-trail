locals {
  container_name = "dashboard"
}

resource "aws_security_group" "service" {
  name        = "${var.name}-dashboard"
  description = "Dashboard tasks for ${var.name}"
  vpc_id      = var.vpc_id

  tags = merge(var.tags, { Name = "${var.name}-dashboard" })
}

resource "aws_vpc_security_group_ingress_rule" "service_from_alb" {
  security_group_id            = aws_security_group.service.id
  referenced_security_group_id = var.alb_security_group_id
  from_port                    = var.container_port
  to_port                      = var.container_port
  ip_protocol                  = "tcp"
  description                  = "From ALB"
}

resource "aws_vpc_security_group_egress_rule" "service_all" {
  security_group_id = aws_security_group.service.id
  cidr_ipv4         = "0.0.0.0/0"
  ip_protocol       = "-1"
  description       = "Unrestricted outbound; reaches the control plane, ECR, and CloudWatch"
}

resource "aws_vpc_security_group_egress_rule" "alb_to_service" {
  security_group_id            = var.alb_security_group_id
  referenced_security_group_id = aws_security_group.service.id
  from_port                    = var.container_port
  to_port                      = var.container_port
  ip_protocol                  = "tcp"
  description                  = "To dashboard tasks"
}

resource "aws_vpc_security_group_ingress_rule" "api_from_service" {
  security_group_id            = var.api_security_group_id
  referenced_security_group_id = aws_security_group.service.id
  from_port                    = var.api_port
  to_port                      = var.api_port
  ip_protocol                  = "tcp"
  description                  = "From dashboard tasks over Service Connect"
}

resource "aws_lb_target_group" "this" {
  name        = "${var.name}-dashboard"
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

# rules run before the listener's default action; the default stays on the control plane
resource "aws_lb_listener_rule" "metrics_not_found" {
  listener_arn = var.https_listener_arn
  priority     = 5

  action {
    type = "fixed-response"

    fixed_response {
      content_type = "text/plain"
      message_body = "not found"
      status_code  = "404"
    }
  }

  condition {
    path_pattern {
      values = ["/metrics"]
    }
  }

  tags = var.tags
}

# an ALB rule holds at most five path values, so the API paths span two rules
resource "aws_lb_listener_rule" "api_prefixes" {
  listener_arn = var.https_listener_arn
  priority     = 10

  action {
    type             = "forward"
    target_group_arn = var.api_target_group_arn
  }

  condition {
    path_pattern {
      values = ["/webhooks/*", "/auth/*", "/api/*"]
    }
  }

  tags = var.tags
}

resource "aws_lb_listener_rule" "api_endpoints" {
  listener_arn = var.https_listener_arn
  priority     = 11

  action {
    type             = "forward"
    target_group_arn = var.api_target_group_arn
  }

  condition {
    path_pattern {
      values = ["/healthz", "/readyz", "/me"]
    }
  }

  tags = var.tags
}

resource "aws_lb_listener_rule" "dashboard" {
  listener_arn = var.https_listener_arn
  priority     = 20

  action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.this.arn
  }

  condition {
    path_pattern {
      values = ["/*"]
    }
  }

  tags = var.tags
}

resource "aws_cloudwatch_log_group" "this" {
  name              = "/ecs/${var.name}/dashboard"
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
  name               = "${var.name}-dashboard-execution"
  assume_role_policy = data.aws_iam_policy_document.ecs_assume.json

  tags = var.tags
}

resource "aws_iam_role_policy_attachment" "execution_managed" {
  role       = aws_iam_role.execution.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AmazonECSTaskExecutionRolePolicy"
}

data "aws_region" "current" {}

# no task role: the dashboard holds no AWS credentials
resource "aws_ecs_task_definition" "this" {
  family                   = "${var.name}-dashboard"
  requires_compatibilities = ["FARGATE"]
  network_mode             = "awsvpc"
  cpu                      = var.cpu
  memory                   = var.memory
  execution_role_arn       = aws_iam_role.execution.arn

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
          name          = "http"
          containerPort = var.container_port
          protocol      = "tcp"
          appProtocol   = "http"
        }
      ]

      environment = [
        { name = "API_PROXY_TARGET", value = var.api_proxy_target },
        { name = "NODE_ENV", value = "production" },
        { name = "HOSTNAME", value = "0.0.0.0" },
        { name = "PORT", value = tostring(var.container_port) },
      ]

      readonlyRootFilesystem = true

      logConfiguration = {
        logDriver = "awslogs"
        options = {
          "awslogs-group"         = aws_cloudwatch_log_group.this.name
          "awslogs-region"        = data.aws_region.current.region
          "awslogs-stream-prefix" = local.container_name
        }
      }

      healthCheck = {
        command     = ["CMD", "wget", "-q", "-O", "/dev/null", "http://localhost:${var.container_port}/healthz"]
        interval    = 15
        timeout     = 5
        retries     = 3
        startPeriod = 15
      }
    }
  ])

  tags = var.tags
}

resource "aws_ecs_service" "this" {
  name            = "dashboard"
  cluster         = var.cluster_arn
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

  # client-only membership: resolves control-plane by name, publishes nothing
  service_connect_configuration {
    enabled   = true
    namespace = var.service_connect_namespace_arn
  }

  depends_on = [aws_lb_listener_rule.dashboard]

  tags = var.tags
}
