module github.com/thaliph/k8s-ctrl

go 1.20

require (
	github.com/google/go-github/v43 v43.0.0
	github.com/robfig/cron/v3 v3.0.1
	golang.org/x/oauth2 v0.15.0
	k8s.io/api v0.28.4
	k8s.io/apimachinery v0.28.4
	k8s.io/autoscaler/vertical-pod-autoscaler v0.14.0
	k8s.io/client-go v0.28.4
	sigs.k8s.io/controller-runtime v0.16.3
)
