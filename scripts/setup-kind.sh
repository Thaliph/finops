#!/bin/bash
set -e

# Create a kind config file
cat > kind-config.yaml << EOF
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- role: control-plane
  kubeadmConfigPatches:
  - |
    kind: ClusterConfiguration
    apiServer:
      extraArgs:
        enable-admission-plugins: NodeRestriction,MutatingAdmissionWebhook,ValidatingAdmissionWebhook
# We need the cert for the admission webhook
  extraMounts:
  - hostPath: ./certs
    containerPath: /etc/ssl/certs
EOF

# Create directory for certs if it doesn't exist
mkdir -p certs

# Create kind cluster
echo "Creating Kind cluster..."
kind create cluster --config kind-config.yaml --name finops-test

# Load Docker images
echo "Loading controller image into Kind..."
docker build -t finops-controller:latest ..
kind load docker-image finops-controller:latest --name finops-test

echo "Kind cluster created successfully!"
