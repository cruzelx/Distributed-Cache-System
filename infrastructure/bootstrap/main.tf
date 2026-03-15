# ==============================================================================
# Bootstrap: Terraform Remote State Infrastructure
# ==============================================================================
# This configuration uses LOCAL state (no backend block) and creates the
# S3 bucket + DynamoDB table that all other Terraform configs will use as
# their remote backend.
#
# Run this ONCE before anything else:
#   cd infrastructure/bootstrap
#   terraform init
#   terraform apply
#
# Then copy the outputs into infrastructure/backend.tf before running
# `terraform init` in the infrastructure/ root.
# ==============================================================================

terraform {
  required_version = ">= 1.5"

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
}

provider "aws" {
  region = var.region
}

# Generate a short random suffix so the S3 bucket name is globally unique.
resource "random_id" "suffix" {
  byte_length = 4
}

# ------------------------------------------------------------------------------
# S3 Bucket — stores Terraform state files
# ------------------------------------------------------------------------------
resource "aws_s3_bucket" "tfstate" {
  bucket = "distributed-cache-tfstate-${random_id.suffix.hex}"

  # Prevent accidental deletion of the bucket while it contains state files.
  lifecycle {
    prevent_destroy = true
  }

  tags = {
    Name    = "distributed-cache-tfstate"
    Purpose = "terraform-remote-state"
  }
}

# Enable versioning so every state-file write is preserved and recoverable.
resource "aws_s3_bucket_versioning" "tfstate" {
  bucket = aws_s3_bucket.tfstate.id

  versioning_configuration {
    status = "Enabled"
  }
}

# Encrypt all objects at rest with AES-256 (SSE-S3).
resource "aws_s3_bucket_server_side_encryption_configuration" "tfstate" {
  bucket = aws_s3_bucket.tfstate.id

  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

# Block all public access — state files must never be publicly readable.
resource "aws_s3_bucket_public_access_block" "tfstate" {
  bucket = aws_s3_bucket.tfstate.id

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

# ------------------------------------------------------------------------------
# DynamoDB Table — provides state-locking to prevent concurrent applies
# ------------------------------------------------------------------------------
resource "aws_dynamodb_table" "tf_locks" {
  name         = "distributed-cache-tf-locks"
  billing_mode = "PAY_PER_REQUEST" # No capacity planning needed for a lock table.
  hash_key     = "LockID"

  attribute {
    name = "LockID"
    type = "S"
  }

  # Keep the table if you accidentally run `terraform destroy` here — losing
  # the lock table while state exists in S3 is recoverable but annoying.
  lifecycle {
    prevent_destroy = true
  }

  tags = {
    Name    = "distributed-cache-tf-locks"
    Purpose = "terraform-state-locking"
  }
}
