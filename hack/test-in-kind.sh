#!/bin/bash

set -e

# Define variables
IMAGE_TAG="controller:latest"
KIND_CLUSTER_NAME="finops-test"
NAMESPACE="default"

# Ensure kind cluster exists
echo "Checking for existing Kind cluster..."
if ! kind get clusters | grep -q "${KIND_CLUSTER_NAME}"; then
  echo "Creating Kind cluster ${KIND_CLUSTER_NAME}..."
  make kind-create
else
  echo "Kind cluster ${KIND_CLUSTER_NAME} already exists."
fi

# Build Docker image
echo "Building Docker image..."
docker build -t ${IMAGE_TAG} .

# Load the image into kind
echo "Loading image into Kind cluster..."
kind load docker-image ${IMAGE_TAG} --name ${KIND_CLUSTER_NAME}

# Install CRDs
echo "Installing CRDs..."
make install

# Apply RBAC
echo "Applying RBAC..."
kubectl apply -f config/rbac

# Create namespace if it doesn't exist
echo "Creating namespace ${NAMESPACE}..."
kubectl create namespace ${NAMESPACE} --dry-run=client -o yaml | kubectl apply -f -

# Create GitHub credentials secret
echo "Checking for GitHub credentials secret..."
if ! kubectl get secret github-credentials -n default &> /dev/null; then
  echo "Creating GitHub credentials secret..."
  make deploy-secret
else
  echo "GitHub credentials secret already exists."
fi

# Deploy sample FinOps resource
echo "Deploying sample FinOps resource..."
kubectl apply -f config/samples

# Follow logs
echo "Following controller logs..."
kubectl -n ${NAMESPACE} wait --for=condition=Ready pod -l app=finops-controller --timeout=60s
kubectl -n ${NAMESPACE} logs -f -l app=finops-controller
