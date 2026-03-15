variable "region" {
  description = "AWS region to create the Terraform state bucket and lock table in."
  type        = string
  default     = "ap-southeast-2"
}
