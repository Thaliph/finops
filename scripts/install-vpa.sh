#!/bin/bash
set -e

# Clone VPA repository
echo "Cloning VPA repository..."
git clone --depth=1 https://github.com/kubernetes/autoscaler.git

# Install VPA
echo "Installing VPA..."
cd autoscaler/vertical-pod-autoscaler
./hack/vpa-up.sh

echo "VPA installed successfully!"
