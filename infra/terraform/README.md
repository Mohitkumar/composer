# Composer ECS Terraform Infrastructure

This Terraform configuration deploys the Composer application on AWS ECS with the following components:

## Architecture

- **ECS Fargate Cluster**: Runs coordinator, worker, web UI, and OTEL collector
- **RDS PostgreSQL**: Database for workflow definitions and state
- **ElastiCache Redis**: Message queue for task distribution
- **Application Load Balancer**: Routes traffic to API and Web UI
- **AWS OTEL Collector**: Collects traces and metrics, sends to X-Ray and CloudWatch
- **Route53**: DNS records for api.fleetconnect.ai and app.fleetconnect.ai

## Prerequisites

1. AWS CLI configured with appropriate credentials
2. Terraform >= 1.0
3. Domain `fleetconnect.ai` already set up in Route53
4. ACM Certificate for `*.fleetconnect.ai` (created separately or via Terraform)
5. ECR repositories for container images:
   - `coordinator`
   - `worker`
   - `web`

## Setup

1. Copy `terraform.tfvars.example` to `terraform.tfvars`:
   ```bash
   cp terraform.tfvars.example terraform.tfvars
   ```

2. Edit `terraform.tfvars` with your values:
   - Set `db_password` to a secure password
   - Set `ecr_repository_url` to your ECR repository URL
   - Configure other variables as needed

3. Initialize Terraform:
   ```bash
   terraform init
   ```

4. Review the plan:
   ```bash
   terraform plan
   ```

5. Apply the configuration:
   ```bash
   terraform apply
   ```

## Building and Pushing Container Images

Before deploying, you need to build and push container images to ECR:

```bash
# Login to ECR
aws ecr get-login-password --region us-east-1 | docker login --username AWS --password-stdin YOUR_ACCOUNT_ID.dkr.ecr.us-east-1.amazonaws.com

# Build and push coordinator
docker build -f infra/docker/Dockerfile.coordinator -t YOUR_ACCOUNT_ID.dkr.ecr.us-east-1.amazonaws.com/composer/coordinator:latest .
docker push YOUR_ACCOUNT_ID.dkr.ecr.us-east-1.amazonaws.com/composer/coordinator:latest

# Build and push worker
docker build -f infra/docker/Dockerfile.worker -t YOUR_ACCOUNT_ID.dkr.ecr.us-east-1.amazonaws.com/composer/worker:latest .
docker push YOUR_ACCOUNT_ID.dkr.ecr.us-east-1.amazonaws.com/composer/worker:latest

# Build and push web
docker build -f infra/docker/Dockerfile.web -t YOUR_ACCOUNT_ID.dkr.ecr.us-east-1.amazonaws.com/composer/web:latest .
docker push YOUR_ACCOUNT_ID.dkr.ecr.us-east-1.amazonaws.com/composer/web:latest
```

## ACM Certificate

You need to create an ACM certificate for `*.fleetconnect.ai` in the same region as your infrastructure. You can do this via AWS Console or Terraform:

```hcl
resource "aws_acm_certificate" "main" {
  domain_name       = "*.fleetconnect.ai"
  validation_method = "DNS"

  lifecycle {
    create_before_destroy = true
  }
}
```

## Observability

- **Traces**: Sent to AWS X-Ray via OTEL Collector
- **Metrics**: Sent to CloudWatch via OTEL Collector
- **Logs**: CloudWatch Logs for all ECS tasks

Access X-Ray console to view traces:
- AWS Console > X-Ray > Service Map

Access CloudWatch to view metrics and logs:
- AWS Console > CloudWatch > Metrics
- AWS Console > CloudWatch > Logs

## Outputs

After applying, Terraform will output:
- `api_url`: API endpoint URL
- `app_url`: Web UI URL
- `alb_dns_name`: ALB DNS name
- `rds_endpoint`: RDS endpoint (sensitive)
- `redis_endpoint`: Redis endpoint (sensitive)

## Notes

- The OTEL Collector uses the default AWS OTEL Collector image which includes X-Ray and CloudWatch exporters
- Coordinator instances use service discovery for gRPC communication
- All services run in private subnets with NAT Gateway for outbound internet access
- ALB is in public subnets for internet access
- Database and Redis are only accessible from ECS security group

## Security Considerations

- Use AWS Secrets Manager for database passwords in production
- Enable RDS Multi-AZ for production
- Enable ElastiCache Multi-AZ for production
- Use WAF on ALB for additional security
- Enable VPC Flow Logs
- Enable CloudTrail for audit logging

