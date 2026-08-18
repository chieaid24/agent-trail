terraform {
  required_version = ">= 1.9.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.0"
    }
    tls = {
      source  = "hashicorp/tls"
      version = "~> 4.0"
    }
  }

  # Partial backend: pass bucket/key/region/dynamodb_table at init time.
  # Never initialised in CI; fmt and validate run with -backend=false.
  backend "s3" {}
}
