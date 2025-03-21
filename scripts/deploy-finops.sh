#!/bin/bash
set -e

# Apply the CRD
echo "Applying FinOps CRD..."
kubectl apply -f ../config/crd/tasks.thaliph.com_finops.yaml

# Create RBAC resources
echo "Creating RBAC resources..."
cat > rbac.yaml << EOF
apiVersion: v1
kind: ServiceAccount
metadata:
  name: finops-controller
  namespace: default
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: finops-controller
rules:
- apiGroups: ["tasks.thaliph.com"]
  resources: ["finops"]
  verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
- apiGroups: ["tasks.thaliph.com"]
  resources: ["finops/status", "finops/finalizers"]
  verbs: ["get", "update", "patch"]
- apiGroups: ["autoscaling.k8s.io"]
  resources: ["verticalpodautoscalers"]
  verbs: ["get", "list", "watch"]
- apiGroups: [""]
  resources: ["secrets"]
  verbs: ["get", "list", "watch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: finops-controller
subjects:
- kind: ServiceAccount
  name: finops-controller
  namespace: default
roleRef:
  kind: ClusterRole
  name: finops-controller
  apiGroup: rbac.authorization.k8s.io
EOF

kubectl apply -f rbac.yaml

# Deploy the controller
echo "Deploying FinOps controller..."
cat > deployment.yaml << EOF
apiVersion: apps/v1
kind: Deployment
metadata:
  name: finops-controller
  namespace: default
spec:
  replicas: 1
  selector:
    matchLabels:
      app: finops-controller
  template:
    metadata:
      labels:
        app: finops-controller
    spec:
      serviceAccountName: finops-controller
      containers:
      - name: manager
        image: finops-controller:latest
        imagePullPolicy: IfNotPresent
        resources:
          limits:
            cpu: 200m
            memory: 300Mi
          requests:
            cpu: 100m
            memory: 200Mi
EOF

kubectl apply -f deployment.yaml

echo "FinOps controller deployed successfully!"
