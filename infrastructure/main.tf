# ==============================================================================
# Main Infrastructure — VPC, ECR, EKS, EBS CSI Driver
# ==============================================================================

# ------------------------------------------------------------------------------
# VPC
# Puts the cluster in 3 AZs with public subnets (for the NLB) and private
# subnets (for the worker nodes).  A single shared NAT gateway keeps egress
# costs low for a hobby/dev environment.
# ------------------------------------------------------------------------------
module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "~> 5.0"

  name = "${var.cluster_name}-vpc"
  cidr = "10.0.0.0/16"

  azs             = ["ap-southeast-2a", "ap-southeast-2b", "ap-southeast-2c"]
  public_subnets  = ["10.0.1.0/24", "10.0.2.0/24", "10.0.3.0/24"]
  private_subnets = ["10.0.10.0/24", "10.0.11.0/24", "10.0.12.0/24"]

  # One NAT gateway shared across all AZs — sufficient for non-production use.
  enable_nat_gateway     = true
  single_nat_gateway     = true
  enable_dns_hostnames   = true
  enable_dns_support     = true

  # Tags required by EKS to discover subnets for load balancers and nodes.
  public_subnet_tags = {
    "kubernetes.io/cluster/${var.cluster_name}" = "shared"
    "kubernetes.io/role/elb"                    = "1"
  }

  private_subnet_tags = {
    "kubernetes.io/cluster/${var.cluster_name}" = "shared"
    "kubernetes.io/role/internal-elb"           = "1"
  }

  tags = {
    Project     = var.cluster_name
    Environment = "production"
  }
}

# ------------------------------------------------------------------------------
# ECR Repositories
# One repository per service.  MUTABLE tags let the Makefile overwrite `latest`
# on every build without creating new tag entries.
# ------------------------------------------------------------------------------
resource "aws_ecr_repository" "master" {
  name                 = "distributed-cache/master"
  image_tag_mutability = "MUTABLE"

  image_scanning_configuration {
    scan_on_push = true
  }

  tags = {
    Project = var.cluster_name
  }
}

resource "aws_ecr_repository" "auxiliary" {
  name                 = "distributed-cache/auxiliary"
  image_tag_mutability = "MUTABLE"

  image_scanning_configuration {
    scan_on_push = true
  }

  tags = {
    Project = var.cluster_name
  }
}

# ------------------------------------------------------------------------------
# EKS Cluster
# ------------------------------------------------------------------------------
module "eks" {
  source  = "terraform-aws-modules/eks/aws"
  version = "~> 20.0"

  cluster_name    = var.cluster_name
  cluster_version = "1.31"

  vpc_id     = module.vpc.vpc_id
  subnet_ids = module.vpc.private_subnets

  # Allow kubectl access from anywhere — lock this down to specific CIDRs in
  # a production environment.
  cluster_endpoint_public_access = true

  eks_managed_node_groups = {
    main = {
      instance_types = [var.node_instance_type]
      min_size       = var.node_min
      max_size       = var.node_max
      desired_size   = var.node_desired
      disk_size      = 20 # GiB per node

      # Use the latest Amazon Linux 2 EKS-optimised AMI.
      ami_type = "AL2_x86_64"

      labels = {
        role = "worker"
      }

      tags = {
        Project = var.cluster_name
      }
    }
  }

  tags = {
    Project     = var.cluster_name
    Environment = "production"
  }
}

# ------------------------------------------------------------------------------
# IAM Role for the EBS CSI Driver
# The driver runs as a Kubernetes service account; we give that SA permission
# to call the EBS APIs via IRSA (IAM Roles for Service Accounts).
# ------------------------------------------------------------------------------
data "aws_iam_policy_document" "ebs_csi_assume_role" {
  statement {
    effect = "Allow"

    principals {
      type        = "Federated"
      identifiers = [module.eks.oidc_provider_arn]
    }

    actions = ["sts:AssumeRoleWithWebIdentity"]

    condition {
      test     = "StringEquals"
      variable = "${replace(module.eks.cluster_oidc_issuer_url, "https://", "")}:sub"
      values   = ["system:serviceaccount:kube-system:ebs-csi-controller-sa"]
    }

    condition {
      test     = "StringEquals"
      variable = "${replace(module.eks.cluster_oidc_issuer_url, "https://", "")}:aud"
      values   = ["sts.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "ebs_csi_driver" {
  name               = "${var.cluster_name}-ebs-csi-driver"
  assume_role_policy = data.aws_iam_policy_document.ebs_csi_assume_role.json

  tags = {
    Project = var.cluster_name
  }
}

resource "aws_iam_role_policy_attachment" "ebs_csi_driver" {
  role       = aws_iam_role.ebs_csi_driver.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AmazonEBSCSIDriverPolicy"
}

# Install the EBS CSI driver as an EKS managed add-on.
resource "aws_eks_addon" "ebs_csi_driver" {
  cluster_name             = module.eks.cluster_name
  addon_name               = "aws-ebs-csi-driver"
  addon_version            = "v1.35.0-eksbuild.1" # update as new versions release
  service_account_role_arn = aws_iam_role.ebs_csi_driver.arn

  # If you have existing PVCs managed by the addon, set to NONE to avoid
  # disrupting them during upgrades.
  resolve_conflicts_on_update = "PRESERVE"

  depends_on = [
    aws_iam_role_policy_attachment.ebs_csi_driver,
  ]

  tags = {
    Project = var.cluster_name
  }
}
