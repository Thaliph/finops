module github.com/alexismerle/k8s-ctrl

go 1.21

require (
	github.com/go-git/go-git/v5 v5.10.0
	github.com/google/go-github/v55 v55.0.0
	github.com/onsi/ginkgo/v2 v2.12.0
	github.com/onsi/gomega v1.27.10
	golang.org/x/oauth2 v0.13.0
	k8s.io/api v0.28.3
	k8s.io/apimachinery v0.28.3
	k8s.io/client-go v0.28.3
	sigs.k8s.io/controller-runtime v0.16.3
)

// Additional dependencies will be added automatically by go mod
