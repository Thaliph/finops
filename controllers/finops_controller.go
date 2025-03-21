package controllers

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/google/go-github/v55/github"
	"golang.org/x/oauth2"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	finopsv1 "github.com/alexismerle/k8s-ctrl/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	finalizerName = "finops.example.com/finalizer"
	tempDir       = "/tmp/finops-git"
)

// FinOpsReconciler reconciles a FinOps object
type FinOpsReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

//+kubebuilder:rbac:groups=finops.example.com,resources=finops,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=finops.example.com,resources=finops/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=finops.example.com,resources=finops/finalizers,verbs=update
//+kubebuilder:rbac:groups=autoscaling.k8s.io,resources=verticalpodautoscalers,verbs=get;list;watch
//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
//+kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// Reconcile is part of the main kubernetes reconciliation loop
func (r *FinOpsReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)
	
	var finops finopsv1.FinOps
	if err := r.Get(ctx, req.NamespacedName, &finops); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Handle finalizer
	if finops.ObjectMeta.DeletionTimestamp.IsZero() {
		if !containsString(finops.GetFinalizers(), finalizerName) {
			controllerutil.AddFinalizer(&finops, finalizerName)
			if err := r.Update(ctx, &finops); err != nil {
				return ctrl.Result{}, err
			}
		}
	} else {
		// Object is being deleted
		if containsString(finops.GetFinalizers(), finalizerName) {
			// Clean up resources if needed
			controllerutil.RemoveFinalizer(&finops, finalizerName)
			if err := r.Update(ctx, &finops); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// Parse schedule to determine when to next reconcile
	var requeueAfter time.Duration
	schedule := finops.Spec.Schedule
	
	// Simple duration based schedule
	if strings.HasSuffix(schedule, "s") || strings.HasSuffix(schedule, "m") || strings.HasSuffix(schedule, "h") {
		duration, err := time.ParseDuration(schedule)
		if err != nil {
			log.Error(err, "Failed to parse schedule duration", "schedule", schedule)
			return ctrl.Result{}, err
		}
		requeueAfter = duration
	} else {
		// Default to 5 minutes if not specified correctly
		requeueAfter = 5 * time.Minute
		log.Info("Schedule not in duration format, defaulting to 5 minutes")
	}

	// Get last run time
	var shouldRun bool
	if finops.Status.LastRun == nil {
		shouldRun = true
	} else {
		lastRun := finops.Status.LastRun.Time
		shouldRun = time.Since(lastRun) >= requeueAfter
	}

	if shouldRun {
		log.Info("Starting reconciliation")
		
		// Get VPA recommendations
		vpaRecommendation, err := r.getVPARecommendation(ctx, finops)
		if err != nil {
			log.Error(err, "Failed to get VPA recommendation")
			r.Recorder.Event(&finops, corev1.EventTypeWarning, "FetchFailed", "Failed to fetch VPA recommendation")
			return ctrl.Result{RequeueAfter: requeueAfter}, err
		}

		// Get Git credentials
		gitCreds, err := r.getGitCredentials(ctx, finops)
		if err != nil {
			log.Error(err, "Failed to get Git credentials")
			r.Recorder.Event(&finops, corev1.EventTypeWarning, "AuthFailed", "Failed to get GitHub credentials")
			return ctrl.Result{RequeueAfter: requeueAfter}, err
		}

		// Update GitHub repository
		prURL, err := r.updateGitHubRepository(ctx, finops, vpaRecommendation, gitCreds)
		if err != nil {
			log.Error(err, "Failed to update GitHub repository")
			r.Recorder.Event(&finops, corev1.EventTypeWarning, "GitHubUpdateFailed", "Failed to update GitHub repository")
			return ctrl.Result{RequeueAfter: requeueAfter}, err
		}

		// Update status
		now := metav1.Now()
		finops.Status.LastRun = &now
		finops.Status.CurrentPR = prURL
		finops.Status.LastRecommendation = vpaRecommendation

		if err := r.Status().Update(ctx, &finops); err != nil {
			log.Error(err, "Failed to update FinOps status")
			return ctrl.Result{RequeueAfter: requeueAfter}, err
		}

		r.Recorder.Event(&finops, corev1.EventTypeNormal, "Reconciled", "Successfully updated GitHub repository with new VPA recommendations")
	}

	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

// getVPARecommendation retrieves recommendation from the VPA
func (r *FinOpsReconciler) getVPARecommendation(ctx context.Context, finops finopsv1.FinOps) (*finopsv1.ResourceRecommendation, error) {
	log := log.FromContext(ctx)

	// Parse VPA reference
	parts := strings.Split(finops.Spec.VPARef, "/")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid VPA reference format, expected 'namespace/name', got %s", finops.Spec.VPARef)
	}
	vpaNamespace, vpaName := parts[0], parts[1]

	// Get VPA object (simplified for this example)
	// In a real implementation, you would use k8s client-go to get the VPA recommendation
	log.Info("Getting VPA recommendation", "namespace", vpaNamespace, "name", vpaName)
	
	// This is a simplification - in a real controller, you would:
	// 1. Use the autoscaling.k8s.io API client to get VPA recommendations
	// 2. Extract the target CPU and memory values

	// For this example, we'll return dummy values
	return &finopsv1.ResourceRecommendation{
		CPU:    "500m",
		Memory: "256Mi",
	}, nil
}

type GitCredentials struct {
	Username string
	Token    string
}

// getGitCredentials retrieves GitHub credentials from the referenced Secret
func (r *FinOpsReconciler) getGitCredentials(ctx context.Context, finops finopsv1.FinOps) (*GitCredentials, error) {
	var secret corev1.Secret
	err := r.Get(ctx, types.NamespacedName{
		Namespace: finops.Namespace,
		Name:      finops.Spec.SecretRef,
	}, &secret)
	if err != nil {
		return nil, fmt.Errorf("error fetching Secret: %w", err)
	}

	username, ok := secret.Data["username"]
	if !ok {
		return nil, fmt.Errorf("secret %s does not contain 'username' key", finops.Spec.SecretRef)
	}

	token, ok := secret.Data["token"]
	if !ok {
		return nil, fmt.Errorf("secret %s does not contain 'token' key", finops.Spec.SecretRef)
	}

	return &GitCredentials{
		Username: string(username),
		Token:    string(token),
	}, nil
}

// updateGitHubRepository clones the repo, updates the file, and creates/updates a PR
func (r *FinOpsReconciler) updateGitHubRepository(ctx context.Context, finops finopsv1.FinOps, recommendation *finopsv1.ResourceRecommendation, creds *GitCredentials) (string, error) {
	log := log.FromContext(ctx)
	
	// Create a temporary directory for Git operations
	repoDir := filepath.Join(tempDir, finops.Name)
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(repoDir) // Clean up

	// Clone the repository
	repoURL := fmt.Sprintf("https://github.com/%s.git", finops.Spec.Repository)
	log.Info("Cloning repository", "url", repoURL)
	
	repo, err := git.PlainClone(repoDir, false, &git.CloneOptions{
		URL: repoURL,
		Auth: &http.BasicAuth{
			Username: creds.Username,
			Password: creds.Token,
		},
	})
	if err != nil {
		return "", fmt.Errorf("failed to clone repository: %w", err)
	}

	// Create a new branch
	wt, err := repo.Worktree()
	if err != nil {
		return "", fmt.Errorf("failed to get worktree: %w", err)
	}

	branchName := fmt.Sprintf("finops-update-%s", time.Now().Format("20060102-150405"))
	log.Info("Creating branch", "branch", branchName)
	
	// Create and checkout new branch
	checkoutOpts := &git.CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName(branchName),
		Create: true,
	}
	if err := wt.Checkout(checkoutOpts); err != nil {
		return "", fmt.Errorf("failed to checkout branch: %w", err)
	}

	// Update the file with new recommendations
	filePath := filepath.Join(repoDir, finops.Spec.Path, finops.Spec.FileName)
	log.Info("Updating file", "path", filePath)
	
	// Create directory if it doesn't exist
	if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
		return "", fmt.Errorf("failed to create directory: %w", err)
	}

	// Write content to file (in a real implementation, you would update existing content)
	fileContent := fmt.Sprintf(`# Generated by FinOps Operator
resources:
  requests:
    cpu: %s
    memory: %s
`, recommendation.CPU, recommendation.Memory)

	if err := os.WriteFile(filePath, []byte(fileContent), 0644); err != nil {
		return "", fmt.Errorf("failed to write file: %w", err)
	}

	// Stage, commit and push changes
	if _, err := wt.Add(filepath.Join(finops.Spec.Path, finops.Spec.FileName)); err != nil {
		return "", fmt.Errorf("failed to add file: %w", err)
	}

	commitMsg := fmt.Sprintf("Update resource recommendations\n\nCPU: %s\nMemory: %s", 
		recommendation.CPU, recommendation.Memory)
	
	_, err = wt.Commit(commitMsg, &git.CommitOptions{
		Author: &object.Signature{
			Name:  "FinOps Operator",
			Email: "finops@example.com",
			When:  time.Now(),
		},
	})
	if err != nil {
		return "", fmt.Errorf("failed to commit changes: %w", err)
	}

	log.Info("Pushing changes")
	err = repo.Push(&git.PushOptions{
		Auth: &http.BasicAuth{
			Username: creds.Username,
			Password: creds.Token,
		},
	})
	if err != nil {
		return "", fmt.Errorf("failed to push changes: %w", err)
	}

	// Create or update Pull Request using GitHub API
	prURL, err := r.createOrUpdatePR(ctx, finops, branchName, commitMsg, creds)
	if err != nil {
		return "", fmt.Errorf("failed to create PR: %w", err)
	}

	return prURL, nil
}

// createOrUpdatePR creates or updates a pull request on GitHub
func (r *FinOpsReconciler) createOrUpdatePR(ctx context.Context, finops finopsv1.FinOps, branchName, commitMsg string, creds *GitCredentials) (string, error) {
	log := log.FromContext(ctx)
	
	// Set up GitHub client
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: creds.Token},
	)
	tc := oauth2.NewClient(ctx, ts)
	client := github.NewClient(tc)

	// Parse repository owner and name
	parts := strings.Split(finops.Spec.Repository, "/")
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid repository format: %s", finops.Spec.Repository)
	}
	owner, repoName := parts[0], parts[1]

	// Check if PR already exists for this FinOps resource
	var prURL string
	if finops.Status.CurrentPR != "" {
		// Extract PR number from URL
		prURL = finops.Status.CurrentPR
		log.Info("PR already exists, will update it", "URL", prURL)
		// In a real implementation, you would update the existing PR
		// Here we'll just return the existing PR URL
		return prURL, nil
	}

	// Create new PR
	title := fmt.Sprintf("Update resource recommendations for %s", finops.Name)
	body := fmt.Sprintf("This PR was created by the FinOps Operator for resource %s/%s.\n\n%s", 
		finops.Namespace, finops.Name, commitMsg)
	
	pr, _, err := client.PullRequests.Create(ctx, owner, repoName, &github.NewPullRequest{
		Title: github.String(title),
		Body:  github.String(body),
		Head:  github.String(branchName),
		Base:  github.String("main"), // Assuming main is the default branch
	})
	if err != nil {
		return "", fmt.Errorf("failed to create pull request: %w", err)
	}

	log.Info("Created new PR", "number", pr.GetNumber(), "URL", pr.GetHTMLURL())
	return pr.GetHTMLURL(), nil
}

// SetupWithManager sets up the controller with the Manager
func (r *FinOpsReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&finopsv1.FinOps{}).
		Complete(r)
}

// Helper functions
func containsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}
