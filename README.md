# FinOps Kubernetes Controller

## Overview

The FinOps Kubernetes Controller is a Kubernetes operator that helps optimize resource allocation based on Vertical Pod Autoscaler (VPA) recommendations. It automatically creates pull requests to update resource specifications in your GitHub repositories.

> **Note:** I'm not a professional developer - just having fun with Kubernetes controllers and automation! This project is a learning experiment.

## What it does

1. Watches for FinOps custom resources in your cluster
2. Fetches resource recommendations from Vertical Pod Autoscaler
3. Updates YAML files in your GitHub repositories to:
   - Set CPU request to the recommended value
   - Set Memory request and limit to the same recommended value
4. Creates or updates pull requests with these changes

## Quick Start

The easiest way to try this controller is to use the pre-built Docker image:

```bash
docker pull thaliph/finops:latest
```

To deploy to a Kind cluster for testing:

```bash
./hack/test-in-kind.sh
```

## Configuration

Create a FinOps resource to tell the controller which repository and file to update:

```yaml
apiVersion: finops.example.com/v1
kind: FinOps
metadata:
  name: finops-sample
spec:
  repository: "your-username/your-repo"   # GitHub repository
  path: "manifests"                       # Path in the repository
  fileName: "deployment.yaml"             # File to update
  vpaRef: "default/your-app-vpa"          # Reference to the VPA
  secretRef: "github-credentials"         # Secret with GitHub credentials
  schedule: "1h"                          # How often to check for updates
```

## Why This Exists

This controller helps close the loop between VPA recommendations and actually implementing them, making it easier to right-size your Kubernetes workloads while following best practices:

- Using CPU requests (for better cluster utilization)
- Setting memory requests = limits (to avoid OOM issues)
- Automating updates via pull requests (for review before applying)

## Contributing

Feel free to play around with this! This project is mostly for fun and learning, but I'm happy to see it evolve.

## License

MIT
