# Image URL to use all building/pushing image targets
IMG ?= controller:latest
# ENVTEST_K8S_VERSION refers to the version of kubebuilder assets to be downloaded by envtest binary.
ENVTEST_K8S_VERSION = 1.28.0

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

# Setting SHELL to bash allows bash commands to be executed by recipes.
# Options are set to exit when a recipe line exits non-zero or a piped command fails.
SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

.PHONY: all
all: build

##@ General

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

.PHONY: tidy
tidy: ## Run go mod tidy against code.
	go mod tidy

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

.PHONY: test
test: fmt vet ## Run tests.
	go test ./... -coverprofile cover.out

.PHONY: generate
generate: tidy ## Generate code (DeepCopy functions, etc).
	chmod +x hack/update-codegen.sh
	./hack/update-codegen.sh

##@ Build

.PHONY: build
build: generate fmt vet ## Build manager binary.
	go build -o bin/manager main.go

# Use a direct run command that doesn't depend on generation to avoid the segfault
.PHONY: run-direct
run-direct: ## Run the controller without code generation (when manually implementing DeepCopy).
	go run ./main.go

.PHONY: run
run: ## Run a controller from your host.
	$(MAKE) tidy
	$(MAKE) fmt
	$(MAKE) vet
	go run ./main.go

##@ Deployment

.PHONY: install
install: ## Install CRDs into the K8s cluster specified in ~/.kube/config.
	kubectl apply -f config/crd/bases

.PHONY: uninstall
uninstall: ## Uninstall CRDs from the K8s cluster specified in ~/.kube/config.
	kubectl delete -f config/crd/bases

.PHONY: deploy
deploy: ## Deploy controller to the K8s cluster specified in ~/.kube/config.
	kubectl apply -f config/rbac
	kubectl apply -f config/manager

.PHONY: undeploy
undeploy: ## Undeploy controller from the K8s cluster specified in ~/.kube/config.
	kubectl delete -f config/manager
	kubectl delete -f config/rbac

##@ Kind Cluster

.PHONY: kind-create
kind-create: ## Create a kind cluster for testing
	kind create cluster --name finops-test --config=./hack/kind.yaml || true
	kubectl apply -f https://raw.githubusercontent.com/kubernetes/autoscaler/vpa-release-1.0/vertical-pod-autoscaler/deploy/vpa-v1-crd-gen.yaml
	kubectl apply -f https://raw.githubusercontent.com/kubernetes/autoscaler/vpa-release-1.0/vertical-pod-autoscaler/deploy/vpa-rbac.yaml
	kubectl apply -f https://raw.githubusercontent.com/kubernetes/autoscaler/vpa-release-1.0/vertical-pod-autoscaler/deploy/updater-deployment.yaml
	kubectl apply -f https://raw.githubusercontent.com/kubernetes/autoscaler/vpa-release-1.0/vertical-pod-autoscaler/deploy/recommender-deployment.yaml
	kubectl apply -f https://raw.githubusercontent.com/kubernetes/autoscaler/vpa-release-1.0/vertical-pod-autoscaler/deploy/admission-controller-deployment.yaml

.PHONY: kind-delete
kind-delete: ## Delete the kind cluster
	kind delete cluster --name finops-test

.PHONY: apply-crd
apply-crd: ## Apply the CRD to the kind cluster
	kubectl apply -f config/crd/bases

.PHONY: deploy-sample
deploy-sample: ## Deploy a sample FinOps resource
	kubectl apply -f config/samples/

.PHONY: deploy-secret
deploy-secret: ## Deploy a sample GitHub secret
	@read -p "GitHub Username: " username; \
	read -sp "GitHub Token: " token; \
	kubectl create secret generic github-credentials --from-literal=username=$$username --from-literal=token=$$token

.PHONY: kind-setup
kind-setup: kind-create apply-crd deploy-secret deploy-sample ## Setup the kind cluster with all required resources
	@echo "Kind cluster setup complete"
