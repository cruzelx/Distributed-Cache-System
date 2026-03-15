# Copy these values into infrastructure/backend.tf before running
# `terraform init` in the infrastructure/ root directory.

output "state_bucket_name" {
  description = "Name of the S3 bucket that stores Terraform remote state. Paste this into infrastructure/backend.tf."
  value       = aws_s3_bucket.tfstate.bucket
}

output "lock_table_name" {
  description = "Name of the DynamoDB table used for Terraform state locking."
  value       = aws_dynamodb_table.tf_locks.name
}
