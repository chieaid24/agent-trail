# Terraform creates the secret containers only. Values are set out-of-band
# (aws secretsmanager put-secret-value) so no secret material ever enters
# Terraform state or version control.
locals {
  secrets = {
    github_app_private_key = "GitHub App private key (PEM). Control plane only; never reaches runners."
    github_webhook_secret  = "GitHub webhook HMAC secret."
    agent_provider_api_key = "Agent provider API key handed to runner Jobs at dispatch."
    dashboard_auth_secret  = "Dashboard session signing secret."
  }
}

resource "aws_secretsmanager_secret" "this" {
  for_each = local.secrets

  name                    = "${var.name}/${replace(each.key, "_", "-")}"
  description             = each.value
  recovery_window_in_days = var.recovery_window_days

  tags = var.tags
}
