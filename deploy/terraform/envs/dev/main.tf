provider "aws" {
  region = var.aws_region

  default_tags {
    tags = local.tags
  }
}

locals {
  name = "agent-trail-dev"
  tags = {
    Project     = "agent-trail"
    Environment = "dev"
    ManagedBy   = "terraform"
  }
}

module "network" {
  source = "../../modules/network"

  name     = local.name
  vpc_cidr = "10.40.0.0/16"
  az_count = 2
}

module "secrets" {
  source = "../../modules/secrets"

  name = local.name
}

module "object_storage" {
  source = "../../modules/object_storage"

  name                     = local.name
  artifact_expiration_days = 30
}

module "queue" {
  source = "../../modules/queue"

  name = local.name
}

module "container_registry" {
  source = "../../modules/container_registry"

  name = local.name
}

module "dns_tls" {
  source = "../../modules/dns_tls"

  zone_name    = var.zone_name
  domain_name  = var.domain_name
  alb_dns_name = module.control_plane.alb_dns_name
  alb_zone_id  = module.control_plane.alb_zone_id
}

module "control_plane" {
  source = "../../modules/control_plane"

  name               = local.name
  vpc_id             = module.network.vpc_id
  public_subnet_ids  = module.network.public_subnet_ids
  private_subnet_ids = module.network.private_subnet_ids
  image              = var.control_plane_image
  desired_count      = 1
  certificate_arn    = module.dns_tls.certificate_arn

  artifacts_bucket_arn    = module.object_storage.bucket_arn
  task_dispatch_queue_arn = module.queue.queue_arn
  secret_arns = concat(
    module.secrets.control_plane_secret_arns,
    [module.database.master_user_secret_arn],
  )

  environment = {
    LOG_LEVEL = "debug"
  }

  container_secrets = {
    GITHUB_WEBHOOK_SECRET = module.secrets.secret_arns["github_webhook_secret"]
  }
}

module "dashboard" {
  source = "../../modules/dashboard"

  name               = local.name
  vpc_id             = module.network.vpc_id
  private_subnet_ids = module.network.private_subnet_ids
  image              = var.dashboard_image
  desired_count      = 1
  cpu                = 256
  memory             = 512

  cluster_arn                   = module.control_plane.cluster_arn
  https_listener_arn            = module.control_plane.https_listener_arn
  api_target_group_arn          = module.control_plane.target_group_arn
  alb_security_group_id         = module.control_plane.alb_security_group_id
  api_security_group_id         = module.control_plane.service_security_group_id
  api_port                      = module.control_plane.container_port
  service_connect_namespace_arn = module.control_plane.service_connect_namespace_arn
  api_proxy_target              = module.control_plane.service_connect_url
}

module "database" {
  source = "../../modules/database"

  name                       = local.name
  vpc_id                     = module.network.vpc_id
  subnet_ids                 = module.network.private_subnet_ids
  allowed_security_group_ids = [module.control_plane.service_security_group_id]

  instance_class      = "db.t4g.micro"
  multi_az            = false
  deletion_protection = false
}

module "runner_cluster" {
  source = "../../modules/runner_cluster"

  name               = local.name
  private_subnet_ids = module.network.private_subnet_ids

  node_min_size     = 1
  node_max_size     = 2
  node_desired_size = 1

  artifacts_bucket_arn    = module.object_storage.bucket_arn
  task_dispatch_queue_arn = module.queue.queue_arn
  runner_secret_arns      = module.secrets.runner_secret_arns
}

module "observability" {
  source = "../../modules/observability"

  name                      = local.name
  alert_email               = var.alert_email
  enable_transaction_search = true

  alb_arn_suffix           = module.control_plane.alb_arn_suffix
  target_group_arn_suffix  = module.control_plane.target_group_arn_suffix
  db_instance_identifier   = module.database.identifier
  task_dispatch_queue_name = module.queue.queue_name
  dead_letter_queue_name   = module.queue.dead_letter_queue_name
}

module "github_oidc_ci" {
  source = "../../modules/github_oidc_ci"

  name              = local.name
  github_repository = var.github_repository
  ecr_repository_arns = [
    module.container_registry.repository_arns["control-plane"],
    module.container_registry.repository_arns["runner"],
    module.container_registry.repository_arns["web"],
  ]
  ecs_cluster_name = module.control_plane.cluster_name
  ecs_service_name = module.control_plane.service_name
  task_role_arns = [
    module.control_plane.execution_role_arn,
    module.control_plane.task_role_arn,
    module.dashboard.execution_role_arn,
  ]
}
