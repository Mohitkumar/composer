terraform {
  required_version = ">= 1.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
    random = {
      source  = "hashicorp/random"
      version = "~> 3.0"
    }
  }

  backend "s3" {
    # Configure backend in terraform.tfvars or via -backend-config
    # bucket = "your-terraform-state-bucket"
    # key    = "composer/terraform.tfstate"
    # region = "us-east-1"
  }
}

provider "aws" {
  region = var.aws_region

  default_tags {
    tags = {
      Project     = "composer"
      Environment = var.environment
      ManagedBy   = "terraform"
    }
  }
}

# Data sources
data "aws_availability_zones" "available" {
  state = "available"
}

data "aws_route53_zone" "main" {
  name = var.domain_name
}

# VPC Module
module "vpc" {
  source = "./modules/vpc"

  name                 = "${var.project_name}-vpc"
  cidr                 = var.vpc_cidr
  availability_zones   = slice(data.aws_availability_zones.available.names, 0, 2)
  public_subnet_cidrs  = var.public_subnet_cidrs
  private_subnet_cidrs = var.private_subnet_cidrs

  tags = {
    Name = "${var.project_name}-vpc"
  }
}

# RDS Module
module "rds" {
  source = "./modules/rds"

  name                 = "${var.project_name}-rds"
  vpc_id               = module.vpc.vpc_id
  private_subnet_ids    = module.vpc.private_subnet_ids
  allowed_security_groups = [module.ecs.security_group_id]
  db_name              = var.db_name
  db_username          = var.db_username
  db_password          = var.db_password
  instance_class       = var.rds_instance_class
  allocated_storage     = var.rds_allocated_storage
  multi_az             = var.rds_multi_az

  tags = {
    Name = "${var.project_name}-rds"
  }
}

# ElastiCache Module
module "elasticache" {
  source = "./modules/elasticache"

  name                 = "${var.project_name}-redis"
  vpc_id               = module.vpc.vpc_id
  private_subnet_ids    = module.vpc.private_subnet_ids
  allowed_security_groups = [module.ecs.security_group_id]
  node_type            = var.redis_node_type
  num_cache_nodes      = var.redis_num_cache_nodes
  parameter_group_name = var.redis_parameter_group_name

  tags = {
    Name = "${var.project_name}-redis"
  }
}

# ECS Module
module "ecs" {
  source = "./modules/ecs"

  project_name         = var.project_name
  vpc_id               = module.vpc.vpc_id
  private_subnet_ids   = module.vpc.private_subnet_ids
  public_subnet_ids    = module.vpc.public_subnet_ids
  rds_endpoint         = module.rds.endpoint
  rds_database         = var.db_name
  rds_username         = var.db_username
  rds_password         = var.db_password
  redis_endpoint       = module.elasticache.endpoint
  redis_auth_token     = module.elasticache.auth_token
  domain_name          = var.domain_name
  api_subdomain        = var.api_subdomain
  app_subdomain        = var.app_subdomain
  route53_zone_id      = data.aws_route53_zone.main.zone_id
  coordinator_replicas = var.coordinator_replicas
  worker_replicas      = var.worker_replicas
  ecr_repository_url   = var.ecr_repository_url
  image_tag            = var.image_tag
  otel_collector_image = var.otel_collector_image

  tags = {
    Name = "${var.project_name}-ecs"
  }
}

