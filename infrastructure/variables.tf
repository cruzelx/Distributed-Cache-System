# ==============================================================================
# Input Variables
# ==============================================================================

variable "region" {
  description = "AWS region to deploy into."
  type        = string
  default     = "ap-southeast-2"
}

variable "cluster_name" {
  description = "Name for the EKS cluster and associated resources."
  type        = string
  default     = "distributed-cache"
}

variable "node_instance_type" {
  description = "EC2 instance type for EKS managed node group workers."
  type        = string
  default     = "t3.medium"
}

variable "node_min" {
  description = "Minimum number of worker nodes in the managed node group."
  type        = number
  default     = 2
}

variable "node_max" {
  description = "Maximum number of worker nodes in the managed node group."
  type        = number
  default     = 5
}

variable "node_desired" {
  description = "Desired number of worker nodes in the managed node group."
  type        = number
  default     = 3
}

variable "aux_replicas" {
  description = "Number of auxiliary (cache) StatefulSet replicas."
  type        = number
  default     = 3
}

variable "replication_factor" {
  description = "Number of aux nodes each key is replicated to."
  type        = number
  default     = 2
}

variable "lru_capacity" {
  description = "Maximum number of entries in each auxiliary node's LRU cache."
  type        = number
  default     = 512
}
