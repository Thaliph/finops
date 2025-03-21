#!/bin/bash
set -e

echo "=== Setting up test environment for FinOps CRD ==="

# Make sure we have necessary tools
command -v kind >/dev/null 2>&1 || { echo "Kind is required but not installed. Aborting."; exit 1; }
command -v kubectl >/dev/null 2>&1 || { echo "kubectl is required but not installed. Aborting."; exit 1; }
command -v docker >/dev/null 2>&1 || { echo "Docker is required but not installed. Aborting."; exit 1; }

# Make scripts executable
chmod +x scripts/*.sh examples/github-secret.sh

# Create Kind cluster
echo "Creating Kind cluster..."
cd scripts
./setup-kind.sh
cd ..

# Install VPA 
echo "Installing VPA..."
cd scripts
./install-vpa.sh
cd ..

# Build and push the controller image
echo "Building the controller..."
docker build -t finops-controller:latest .
kind load docker-image finops-controller:latest --name finops-test

# Deploy the controller
echo "Deploying the FinOps controller..."
cd scripts
./deploy-finops.sh
cd ..

# Deploy test application with VPA
echo "Deploying test application..."
kubectl apply -f examples/test-app.yaml

# Wait for VPA to collect metrics
echo "Waiting for VPA to collect metrics (60s)..."
sleep 60

# Ask for GitHub credentials
read -p "Enter your GitHub username: " github_username
read -sp "Enter your GitHub token: " github_token
echo

# Create GitHub secret
echo "Creating GitHub secret..."
./examples/github-secret.sh "$github_username" "$github_token"

# Update FinOps example with the user's GitHub username
sed -i "s/YOUR_GITHUB_USERNAME/$github_username/g" examples/finops-test.yaml

echo
echo "Before creating the FinOps resource:"
echo "1. Make sure you have a repository named 'test-repo' in your GitHub account"
echo "2. The repository should contain a file at 'kubernetes/deployment.yaml'"
echo "3. Make sure the file has resources sections with comments like: cpu: 100m #nginx"
echo
read -p "Press enter when ready..."

# Create the FinOps resource
kubectl apply -f examples/finops-test.yaml

echo
echo "FinOps resource created successfully!"
echo "To monitor its status:"
echo "kubectl get finops"
echo "kubectl describe finops test-finops"
echo
echo "Wait for 5 minutes (or according to your schedule) and check if a PR was created in your GitHub repo."
