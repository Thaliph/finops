#!/bin/bash
set -e

# Check if github username and token are provided
if [ "$#" -ne 2 ]; then
    echo "Usage: $0 <github-username> <github-token>"
    exit 1
fi

GITHUB_USERNAME=$1
GITHUB_TOKEN=$2

# Create Kubernetes secret with GitHub credentials
kubectl create secret generic github-credentials \
  --from-literal=username="${GITHUB_USERNAME}" \
  --from-literal=token="${GITHUB_TOKEN}"

echo "GitHub credentials secret created successfully!"
