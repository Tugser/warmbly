terraform {
  required_version = ">= 1.11, < 2.0"
  backend "s3" {
    key          = "warmbly/production/confluent.tfstate"
    region       = "us-east-1"
    encrypt      = true
    use_lockfile = true
  }
  required_providers {
    confluent = {
      source  = "confluentinc/confluent"
      version = "~> 2.91.0"
    }
  }
}

provider "confluent" {}

variable "environment_id" {
  type = string
  validation {
    condition     = startswith(var.environment_id, "env-")
    error_message = "Provide the ID of the existing Confluent environment."
  }
}

variable "environment_name" {
  type = string
}

variable "kafka_cluster_id" {
  type = string
  validation {
    condition     = startswith(var.kafka_cluster_id, "lkc-")
    error_message = "Provide the ID of the existing Kafka cluster."
  }
}

variable "kafka_cluster_name" {
  type = string
}

variable "kafka_region" {
  description = "Region of the existing cluster; adoption must preserve its placement"
  type        = string
}

variable "kafka_availability" {
  description = "Existing availability configuration; adoption must not change it"
  type        = string
}

variable "service_account_id" {
  type = string
}

variable "service_account_name" {
  type = string
}

variable "service_account_description" {
  type    = string
  default = ""
}

resource "confluent_environment" "production" {
  display_name = var.environment_name
  stream_governance {
    package = "ESSENTIALS"
  }
  lifecycle {
    prevent_destroy = true
  }
}

resource "confluent_kafka_cluster" "production" {
  display_name = var.kafka_cluster_name
  availability = var.kafka_availability
  cloud        = "AWS"
  region       = var.kafka_region
  basic {}
  environment {
    id = confluent_environment.production.id
  }
  lifecycle {
    prevent_destroy = true
  }
}

resource "confluent_service_account" "production" {
  display_name = var.service_account_name
  description  = var.service_account_description
  lifecycle {
    prevent_destroy = true
  }
}

data "confluent_schema_registry_cluster" "existing" {
  environment {
    id = var.environment_id
  }
}

import {
  to = confluent_environment.production
  id = var.environment_id
}

import {
  to = confluent_kafka_cluster.production
  id = "${var.environment_id}/${var.kafka_cluster_id}"
}

import {
  to = confluent_service_account.production
  id = var.service_account_id
}

output "event_bus" {
  value = {
    environment_id           = confluent_environment.production.id
    cluster_id               = confluent_kafka_cluster.production.id
    bootstrap_endpoint       = confluent_kafka_cluster.production.bootstrap_endpoint
    schema_registry_endpoint = data.confluent_schema_registry_cluster.existing.rest_endpoint
    service_account_id       = confluent_service_account.production.id
  }
}
