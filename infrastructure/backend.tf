# ==============================================================================
# Remote Backend Configuration
# ==============================================================================
# IMPORTANT: Before running `terraform init` in this directory you MUST replace
# the bucket name below with the actual value output by the bootstrap step:
#
#   cd infrastructure/bootstrap
#   terraform apply
#   terraform output state_bucket_name   # <-- copy this value
#
# Then update the `bucket` field below and run:
#   cd ../
#   terraform init
# ==============================================================================

terraform {
  backend "s3" {
    bucket         = "distributed-cache-tfstate-c4515326"
    key            = "prod/terraform.tfstate"
    region         = "ap-southeast-2"
    dynamodb_table = "distributed-cache-tf-locks"
    encrypt        = true
  }
}
