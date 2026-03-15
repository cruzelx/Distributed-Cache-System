# ==============================================================================
# Outputs
# ==============================================================================

output "cluster_name" {
  description = "Name of the EKS cluster."
  value       = module.eks.cluster_name
}

output "cluster_endpoint" {
  description = "API server endpoint for the EKS cluster."
  value       = module.eks.cluster_endpoint
}

output "ecr_master_url" {
  description = "ECR repository URL for the master service image."
  value       = aws_ecr_repository.master.repository_url
}

output "ecr_auxiliary_url" {
  description = "ECR repository URL for the auxiliary service image."
  value       = aws_ecr_repository.auxiliary.repository_url
}

output "vpc_id" {
  description = "ID of the VPC created for the cluster."
  value       = module.vpc.vpc_id
}

output "configure_kubectl" {
  description = "Run this command to configure kubectl to talk to the new cluster."
  value       = "aws eks update-kubeconfig --region ap-southeast-2 --name ${module.eks.cluster_name}"
}
