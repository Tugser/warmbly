terraform {
  required_version = ">= 1.11, < 2.0"
  backend "s3" {
    key          = "warmbly/production/planetscale.tfstate"
    region       = "us-east-1"
    encrypt      = true
    use_lockfile = true
  }
  required_providers {
    planetscale = {
      source  = "planetscale/planetscale"
      version = "~> 1.14.1"
    }
  }
}

provider "planetscale" {}

variable "organization" {
  type = string
}

variable "database" {
  description = "Existing Postgres database name"
  type        = string
}

variable "branch" {
  description = "Existing production branch name"
  type        = string
}

variable "branch_id" {
  description = "ID of the existing branch to adopt, never a new branch"
  type        = string
}

variable "cluster_size" {
  description = "Current cluster SKU; adoption must not resize the database"
  type        = string
}

data "planetscale_database_postgres" "existing" {
  organization = var.organization
  id           = var.database
}

resource "planetscale_postgres_branch" "production" {
  organization = var.organization
  database     = var.database
  name         = var.branch
  cluster_size = var.cluster_size

  lifecycle {
    prevent_destroy = true
    ignore_changes  = [parameters]
  }
}

import {
  to = planetscale_postgres_branch.production
  id = jsonencode({
    organization = var.organization
    database     = var.database
    id           = var.branch_id
  })
}

output "database" {
  value = {
    name        = data.planetscale_database_postgres.existing.name
    branch      = planetscale_postgres_branch.production.name
    branch_id   = planetscale_postgres_branch.production.id
    replicas    = planetscale_postgres_branch.production.replicas
    cluster_sku = planetscale_postgres_branch.production.cluster_size
  }
}
