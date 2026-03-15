# ==============================================================================
# Distributed Cache System — Infrastructure Makefile
# ==============================================================================
# Usage:
#   make bootstrap   — create S3 state bucket + DynamoDB lock table (run once)
#   make init        — initialise Terraform with the remote backend
#   make plan        — preview infrastructure changes
#   make apply       — apply infrastructure changes
#   make connect     — configure kubectl to talk to the EKS cluster
#   make build-push  — build Docker images and push to ECR
#   make deploy      — apply all Kubernetes manifests (in dependency order)
#   make destroy          — tear down all infrastructure (prompts for confirmation)
#   make destroy-bootstrap — empty S3 state bucket and destroy bootstrap resources
#   make help             — print this message
# ==============================================================================

# Resolve ECR URLs from Terraform outputs at recipe evaluation time.
# These will be empty until `make apply` has been run at least once.
MASTER_ECR_URL  := $(shell terraform output -raw ecr_master_url 2>/dev/null)
AUX_ECR_URL     := $(shell terraform output -raw ecr_auxiliary_url 2>/dev/null)

# Derive the AWS account ID and region from the ECR URL.
AWS_REGION      := ap-southeast-2
AWS_ACCOUNT_ID  := $(shell aws sts get-caller-identity --query Account --output text 2>/dev/null)

# Image tags — override on the command line: make build-push TAG=v1.2.3
TAG             ?= latest

# Paths
REPO_ROOT       := $(shell dirname $(realpath $(lastword $(MAKEFILE_LIST))))
K8S_DIR         := $(REPO_ROOT)/k8s

.PHONY: bootstrap init plan apply connect build-push deploy destroy destroy-bootstrap help

# ------------------------------------------------------------------------------
# bootstrap
# Creates the S3 bucket and DynamoDB table that all other Terraform state uses.
# Run this exactly once per AWS account before anything else.
# After it completes, copy the `state_bucket_name` output into backend.tf.
# ------------------------------------------------------------------------------
bootstrap:
	@echo "==> Bootstrapping Terraform remote state infrastructure..."
	cd $(REPO_ROOT)/bootstrap && \
	  terraform init && \
	  terraform apply -auto-approve
	@echo ""
	@echo "NEXT STEP: copy the 'state_bucket_name' output above into"
	@echo "  infrastructure/backend.tf  (replace REPLACE_WITH_BOOTSTRAP_OUTPUT)"
	@echo "then run:  make init"

# ------------------------------------------------------------------------------
# init
# Initialises Terraform with the remote S3 backend.
# Run after updating backend.tf with the bootstrap output.
# ------------------------------------------------------------------------------
init:
	@echo "==> Initialising Terraform..."
	terraform init

# ------------------------------------------------------------------------------
# plan
# Shows what Terraform will create, change, or destroy.
# ------------------------------------------------------------------------------
plan:
	@echo "==> Planning infrastructure changes..."
	terraform plan

# ------------------------------------------------------------------------------
# apply
# Applies the Terraform plan.  Prompts for confirmation unless TF_AUTO_APPROVE
# is set: TF_AUTO_APPROVE=1 make apply
# ------------------------------------------------------------------------------
apply:
	@echo "==> Applying infrastructure changes..."
	@if [ "$$TF_AUTO_APPROVE" = "1" ]; then \
	  terraform apply -auto-approve; \
	else \
	  terraform apply; \
	fi

# ------------------------------------------------------------------------------
# connect
# Configures the local kubeconfig to talk to the EKS cluster.
# ------------------------------------------------------------------------------
connect:
	@echo "==> Configuring kubectl..."
	aws eks update-kubeconfig --region $(AWS_REGION) --name distributed-cache

# ------------------------------------------------------------------------------
# build-push
# Builds multi-platform Docker images from the repo root and pushes them to ECR.
# Requires: Docker, AWS CLI, `make apply` to have run at least once.
# ------------------------------------------------------------------------------
build-push:
	@if [ -z "$(MASTER_ECR_URL)" ] || [ -z "$(AUX_ECR_URL)" ]; then \
	  echo "ERROR: Could not read ECR URLs from Terraform output."; \
	  echo "       Run 'make apply' first to create the ECR repositories."; \
	  exit 1; \
	fi
	@echo "==> Authenticating Docker with ECR..."
	aws ecr get-login-password --region $(AWS_REGION) | \
	  docker login --username AWS --password-stdin $(AWS_ACCOUNT_ID).dkr.ecr.$(AWS_REGION).amazonaws.com

	@echo "==> Building and pushing master image ($(MASTER_ECR_URL):$(TAG))..."
	docker buildx build --platform linux/amd64 --push \
	  -t $(MASTER_ECR_URL):$(TAG) \
	  -f $(REPO_ROOT)/../master/Dockerfile $(REPO_ROOT)/../master

	@echo "==> Building and pushing auxiliary image ($(AUX_ECR_URL):$(TAG))..."
	docker buildx build --platform linux/amd64 --push \
	  -t $(AUX_ECR_URL):$(TAG) \
	  -f $(REPO_ROOT)/../auxiliary/Dockerfile $(REPO_ROOT)/../auxiliary

	@echo "==> Images pushed successfully."

# ------------------------------------------------------------------------------
# deploy
# Applies all Kubernetes manifests in dependency order.
# Substitutes MASTER_IMAGE / AUX_IMAGE placeholders in the manifests before
# applying so you never have to edit the YAML by hand.
# Requires: kubectl configured (run `make connect`), images pushed (make build-push).
# ------------------------------------------------------------------------------
deploy:
	@if [ -z "$(MASTER_ECR_URL)" ] || [ -z "$(AUX_ECR_URL)" ]; then \
	  echo "ERROR: Could not read ECR URLs from Terraform output."; \
	  echo "       Run 'make apply' first."; \
	  exit 1; \
	fi
	@echo "==> Deploying to Kubernetes (namespace, storageclass, configmap)..."
	kubectl apply -f $(K8S_DIR)/namespace.yaml
	kubectl apply -f $(K8S_DIR)/storageclass.yaml
	kubectl apply -f $(K8S_DIR)/configmap.yaml

	@echo "==> Deploying primary master..."
	sed "s|MASTER_IMAGE|$(MASTER_ECR_URL):$(TAG)|g" $(K8S_DIR)/master.yaml | kubectl apply -f -

	@echo "==> Deploying standby master..."
	sed "s|MASTER_IMAGE|$(MASTER_ECR_URL):$(TAG)|g" $(K8S_DIR)/master-standby.yaml | kubectl apply -f -

	@echo "==> Deploying auxiliary StatefulSet..."
	sed "s|AUX_IMAGE|$(AUX_ECR_URL):$(TAG)|g" $(K8S_DIR)/aux.yaml | kubectl apply -f -

	@echo "==> Deploying nginx..."
	kubectl apply -f $(K8S_DIR)/nginx.yaml

	@echo "==> Deploying monitoring (Prometheus + Grafana)..."
	kubectl apply -f $(K8S_DIR)/monitoring.yaml

	@echo ""
	@echo "==> All manifests applied."
	@echo "    Watch rollout:  kubectl get pods -n cache -w"
	@echo "    Grafana:        kubectl port-forward svc/grafana 3000:3000 -n cache"
	@echo "    Prometheus:     kubectl port-forward svc/prometheus 9090:9090 -n cache"

# ------------------------------------------------------------------------------
# destroy
# Tears down ALL infrastructure.  This is irreversible — the confirmation
# prompt is intentional.
# ------------------------------------------------------------------------------
destroy:
	@echo "WARNING: This will destroy all infrastructure including EKS, VPC, and ECR."
	@read -p "Type 'yes' to confirm: " confirm && [ "$$confirm" = "yes" ] || (echo "Aborted."; exit 1)
	@echo "==> Destroying infrastructure..."
	terraform destroy

# ------------------------------------------------------------------------------
# destroy-bootstrap
# Tears down the S3 state bucket and DynamoDB lock table created by `make bootstrap`.
# Run AFTER `make destroy` has completed — the state files must be gone first.
#
# Uses a Terraform override file to disable prevent_destroy without editing
# source, empties all S3 object versions (required before bucket deletion),
# then runs terraform destroy.
# ------------------------------------------------------------------------------
destroy-bootstrap:
	@# Read bucket name from backend.tf — works even after state is gone.
	@BUCKET=$$(grep 'bucket' $(REPO_ROOT)/backend.tf | head -1 | sed 's/.*= *"\(.*\)".*/\1/'); \
	if [ -z "$$BUCKET" ]; then \
	  echo "ERROR: Could not parse bucket name from backend.tf."; \
	  exit 1; \
	fi; \
	echo "WARNING: This will permanently delete the Terraform state bucket ($$BUCKET) and lock table."; \
	read -p "Type 'yes' to confirm: " confirm && [ "$$confirm" = "yes" ] || (echo "Aborted."; exit 1); \
	echo "==> Emptying S3 bucket $$BUCKET (all versions)..."; \
	VERSIONS=$$(aws s3api list-object-versions --bucket $$BUCKET --region $(AWS_REGION) \
	  --query '{Objects: Versions[].{Key:Key,VersionId:VersionId}}' --output json 2>/dev/null); \
	if echo "$$VERSIONS" | grep -q '"Key"'; then \
	  aws s3api delete-objects --bucket $$BUCKET --region $(AWS_REGION) --delete "$$VERSIONS" > /dev/null; \
	  echo "   Object versions deleted."; \
	fi; \
	MARKERS=$$(aws s3api list-object-versions --bucket $$BUCKET --region $(AWS_REGION) \
	  --query '{Objects: DeleteMarkers[].{Key:Key,VersionId:VersionId}}' --output json 2>/dev/null); \
	if echo "$$MARKERS" | grep -q '"Key"'; then \
	  aws s3api delete-objects --bucket $$BUCKET --region $(AWS_REGION) --delete "$$MARKERS" > /dev/null; \
	  echo "   Delete markers deleted."; \
	fi; \
	echo "==> Disabling prevent_destroy via override file..."; \
	printf 'resource "aws_s3_bucket" "tfstate" { lifecycle { prevent_destroy = false } }\n\
resource "aws_dynamodb_table" "tf_locks" { lifecycle { prevent_destroy = false } }\n' \
	  > $(REPO_ROOT)/bootstrap/override.tf; \
	echo "==> Destroying bootstrap resources..."; \
	cd $(REPO_ROOT)/bootstrap && terraform destroy -auto-approve; \
	rm -f $(REPO_ROOT)/bootstrap/override.tf; \
	echo "==> Bootstrap teardown complete."

# ------------------------------------------------------------------------------
# help
# ------------------------------------------------------------------------------
help:
	@echo ""
	@echo "Distributed Cache System — Infrastructure Targets"
	@echo "--------------------------------------------------"
	@echo "  bootstrap   Create S3 state bucket + DynamoDB lock table (run once)"
	@echo "  init        Initialise Terraform remote backend"
	@echo "  plan        Preview infrastructure changes"
	@echo "  apply       Apply infrastructure changes"
	@echo "  connect     Configure kubectl for the EKS cluster"
	@echo "  build-push  Build + push Docker images to ECR"
	@echo "  deploy      Apply all Kubernetes manifests in order"
	@echo "  destroy           Tear down all infrastructure (irreversible)"
	@echo "  destroy-bootstrap Empty S3 state bucket and destroy bootstrap resources"
	@echo "  help              Show this message"
	@echo ""
	@echo "Typical first-run workflow:"
	@echo "  1. make bootstrap"
	@echo "  2. Edit backend.tf with the state_bucket_name output"
	@echo "  3. make init"
	@echo "  4. make apply"
	@echo "  5. make connect"
	@echo "  6. make build-push"
	@echo "  7. make deploy"
	@echo ""
