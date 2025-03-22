package controllers

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"strconv"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/google/go-github/v55/github"
	"golang.org/x/oauth2"
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
	vpav1 "k8s.io/autoscaler/vertical-pod-autoscaler/pkg/apis/autoscaling.k8s.io/v1"
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
			return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil // Always requeue even on error
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
			return ctrl.Result{RequeueAfter: requeueAfter}, nil // Always requeue at next scheduled time
		}

		// Get Git credentials
		gitCreds, err := r.getGitCredentials(ctx, finops)
		if err != nil {
			log.Error(err, "Failed to get Git credentials")
			r.Recorder.Event(&finops, corev1.EventTypeWarning, "AuthFailed", "Failed to get GitHub credentials")
			return ctrl.Result{RequeueAfter: requeueAfter}, nil // Always requeue at next scheduled time
		}

		// Update GitHub repository
		prURL, err := r.updateGitHubRepository(ctx, finops, vpaRecommendation, gitCreds)
		if err != nil {
			log.Error(err, "Failed to update GitHub repository")
			r.Recorder.Event(&finops, corev1.EventTypeWarning, "GitHubUpdateFailed", "Failed to update GitHub repository")
			return ctrl.Result{RequeueAfter: requeueAfter}, nil // Always requeue at next scheduled time
		}

		// Update status
		now := metav1.Now()
		finops.Status.LastRun = &now
		finops.Status.CurrentPR = prURL
		finops.Status.LastRecommendation = vpaRecommendation

		if err := r.Status().Update(ctx, &finops); err != nil {
			log.Error(err, "Failed to update FinOps status")
			return ctrl.Result{RequeueAfter: requeueAfter}, nil // Always requeue at next scheduled time
		}

		r.Recorder.Event(&finops, corev1.EventTypeNormal, "Reconciled", "Successfully updated GitHub repository with new VPA recommendations")
	}

	// Always requeue after the scheduled time
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

	// Get VPA object
	log.Info("Getting VPA recommendation", "namespace", vpaNamespace, "name", vpaName)

	var vpa vpav1.VerticalPodAutoscaler
	if err := r.Get(ctx, types.NamespacedName{Namespace: vpaNamespace, Name: vpaName}, &vpa); err != nil {
		return nil, fmt.Errorf("failed to get VPA: %w", err)
	}

	// Add more defensive checks to handle nil cases
	if vpa.Status.Recommendation == nil {
		log.Info("VPA recommendation is nil, returning empty values")
		return &finopsv1.ResourceRecommendation{}, nil
	}

	if len(vpa.Status.Recommendation.ContainerRecommendations) == 0 {
		log.Info("No VPA container recommendations, returning empty values")
		return &finopsv1.ResourceRecommendation{}, nil
	}

	// Get resource recommendations and check if they exist
	targets := vpa.Status.Recommendation.ContainerRecommendations[0].Target
	if targets == nil {
		log.Info("VPA recommendation targets are nil, returning empty values")
		return &finopsv1.ResourceRecommendation{}, nil
	}

	// Safely extract CPU and memory values
	var cpuValue, memValue string
	if cpuQty, exists := targets["cpu"]; exists {
		cpuValue = cpuQty.String()
	} else {
		log.Info("CPU recommendation not found in VPA")
		cpuValue = "100m" // Default value if not found
	}

	if memQty, exists := targets["memory"]; exists {
		memValue = memQty.String()
	} else {
		log.Info("Memory recommendation not found in VPA")
		memValue = "128Mi" // Default value if not found
	}

	log.Info("Found VPA recommendations", "cpu", cpuValue, "memory", memValue)
	return &finopsv1.ResourceRecommendation{
		CPU:    cpuValue,
		Memory: memValue,
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

	// Clone with all branches instead of specifying RefSpecs
	repo, err := git.PlainClone(repoDir, false, &git.CloneOptions{
		URL: repoURL,
		Auth: &http.BasicAuth{
			Username: creds.Username,
			Password: creds.Token,
		},
			// Clone all branches
		SingleBranch: false,
		NoCheckout:   false,
	})
	if err != nil {
		return "", fmt.Errorf("failed to clone repository: %w", err)
	}

	// Create a new branch
	wt, err := repo.Worktree()
	if err != nil {
		return "", fmt.Errorf("failed to get worktree: %w", err)
	}

	// Create a branch name using the finops resource name
	branchName := fmt.Sprintf("resize/%s", finops.Name)
	log.Info("Working with branch", "branch", branchName)
	remoteBranchName := "origin/" + branchName
	
	// Check if branch exists locally or remotely
	localBranchExists := false
	remoteBranchExists := false
	
	// Check local branches
	branches, err := repo.Branches()
	if err != nil {
		log.Error(err, "Failed to list branches")
	} else {
		err = branches.ForEach(func(branch *plumbing.Reference) error {
			if branch.Name().Short() == branchName {
				localBranchExists = true
				return nil
			}
			return nil
		})
	}
	
	// Check remote branches
	remoteBranches, err := repo.References()
	if err != nil {
		log.Error(err, "Failed to list remote references")
	} else {
		err = remoteBranches.ForEach(func(branch *plumbing.Reference) error {
			if branch.Name().String() == "refs/remotes/"+remoteBranchName {
				remoteBranchExists = true
				return nil
			}
			return nil
		})
	}

	// Handle branch checkout or creation
	if remoteBranchExists {
		log.Info("Remote branch exists, checking out and pulling latest changes", "branch", branchName)
		
		// First checkout to track the remote branch
		err = wt.Checkout(&git.CheckoutOptions{
			Branch: plumbing.NewRemoteReferenceName("origin", branchName),
		})
		if err != nil {
			// If checkout fails, try to fetch first
			fetchErr := repo.Fetch(&git.FetchOptions{
				RemoteName: "origin",
				Auth: &http.BasicAuth{
					Username: creds.Username,
					Password: creds.Token,
				},
			})
			if fetchErr != nil && fetchErr != git.NoErrAlreadyUpToDate {
				log.Error(fetchErr, "Failed to fetch latest changes")
			}
			
			// Try checkout again after fetch
			err = wt.Checkout(&git.CheckoutOptions{
				Branch: plumbing.NewRemoteReferenceName("origin", branchName),
			})
			if err != nil {
				return "", fmt.Errorf("failed to checkout remote branch after fetch: %w", err)
			}
		}
		
		// Create and checkout a local branch that tracks the remote - removing the Track field
		err = wt.Checkout(&git.CheckoutOptions{
			Branch: plumbing.NewBranchReferenceName(branchName),
			Create: true,
		})
		if err != nil {
			return "", fmt.Errorf("failed to create local tracking branch: %w", err)
		}
		
		// Pull latest changes
		err = repo.Fetch(&git.FetchOptions{
			RemoteName: "origin",
			RefSpecs:   []config.RefSpec{config.RefSpec(fmt.Sprintf("refs/heads/%s:refs/remotes/origin/%s", branchName, branchName))},
			Auth: &http.BasicAuth{
				Username: creds.Username,
				Password: creds.Token,
			},
		})
		if err != nil && err != git.NoErrAlreadyUpToDate {
			log.Error(err, "Failed to fetch latest changes")
		}
		
		err = wt.Pull(&git.PullOptions{
			RemoteName: "origin", 
			Force: false,
			Auth: &http.BasicAuth{
				Username: creds.Username,
				Password: creds.Token,
			},
		})
		if err != nil && err != git.NoErrAlreadyUpToDate {
			log.Error(err, "Failed to pull latest changes")
		}
	} else if localBranchExists {
		log.Info("Local branch exists but not remote, checking out local", "branch", branchName)
		err = wt.Checkout(&git.CheckoutOptions{
			Branch: plumbing.NewBranchReferenceName(branchName),
		})
		if err != nil {
			return "", fmt.Errorf("failed to checkout local branch: %w", err)
		}
	} else {
		log.Info("Creating new branch", "branch", branchName)
		// Create and checkout a new branch
		err = wt.Checkout(&git.CheckoutOptions{
			Branch: plumbing.NewBranchReferenceName(branchName),
			Create: true,
		})
		if err != nil {
			return "", fmt.Errorf("failed to create new branch: %w", err)
		}
	}

	// Update the file with new recommendations
	filePath := filepath.Join(repoDir, finops.Spec.Path, finops.Spec.FileName)
	log.Info("Updating file", "path", filePath)

	// Create directory if it doesn't exist
	if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
		return "", fmt.Errorf("failed to create directory: %w", err)
	}

	// Read existing file, parse YAML, then only modify CPU request, memory request & limit
	existingContent, err := os.ReadFile(filePath)
	if os.IsNotExist(err) {
		existingContent = []byte{}
	} else if err != nil {
		return "", fmt.Errorf("failed to read file: %w", err)
	}

	// Process the file content to update the resource values
	newContent := updateResourceValues(string(existingContent), recommendation.CPU, recommendation.Memory)
	
	log.Info("File update details", 
		"oldSize", len(existingContent), 
		"newSize", len(newContent), 
		"cpu", recommendation.CPU, 
		"memory", recommendation.Memory)
	
	if err := os.WriteFile(filePath, []byte(newContent), 0644); err != nil {
		return "", fmt.Errorf("failed to write file: %w", err)
	}

	// For debugging, print the status of the file before and after changes
	log.Info("File contents before changes", "path", filePath, "exists", existingContent != nil, "size", len(existingContent))

	// After updating content
	// Debug the git operations
	status, err := wt.Status()
	if err != nil {
		log.Error(err, "Failed to get git status")
	} else {
		for path, fileStatus := range status {
			log.Info("Git file status", "path", path, "staging", fileStatus.Staging, "worktree", fileStatus.Worktree)
		}
	}

	// Make sure the file is added with its full path
	relativePath := filepath.Join(finops.Spec.Path, finops.Spec.FileName)
	log.Info("Adding file to git", "relativePath", relativePath)
	if _, err := wt.Add(relativePath); err != nil {
		return "", fmt.Errorf("failed to add file: %w", err)
	}

	// Verify file is staged
	status, _ = wt.Status()
	fileAdded := false
	for path, fileStatus := range status {
		if strings.Contains(path, finops.Spec.FileName) {
			fileAdded = true
			log.Info("File staged successfully", "path", path, "staging", fileStatus.Staging)
		}
	}

	if !fileAdded {
		log.Info("WARNING: File doesn't appear to be staged properly")
	}

	// Stage and commit changes
	if _, err := wt.Add(filepath.Join(finops.Spec.Path, finops.Spec.FileName)); err != nil {
		return "", fmt.Errorf("failed to add file: %w", err)
	}
	commitMsg := fmt.Sprintf("Update resource recommendations (CPU: %s, Memory: %s)", recommendation.CPU, recommendation.Memory)
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
		// Use force push to handle non-fast-forward issues
		Force: true,
	})
	if err != nil {
		return "", fmt.Errorf("failed to push changes: %w", err)
	}

	// Check if PR already exists and if it's still open
	if finops.Status.CurrentPR != "" {
		// Extract PR number from URL
		prURL := finops.Status.CurrentPR
		prNumber := extractPRNumber(prURL)
		log.Info("PR might exist, checking status", "URL", prURL, "Number", prNumber)
		
		if prNumber > 0 {
			parts := strings.Split(finops.Spec.Repository, "/")
			if len(parts) == 2 {
				owner, repoName := parts[0], parts[1]
				
				// Create GitHub client
				ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: creds.Token})
				tc := oauth2.NewClient(ctx, ts)
				client := github.NewClient(tc)
				
				// Check PR status
				pr, _, err := client.PullRequests.Get(ctx, owner, repoName, prNumber)
				if err == nil && pr != nil && pr.GetState() == "closed" {
					log.Info("Found existing PR but it's closed, will create a new one")
					finops.Status.CurrentPR = "" // Clear the current PR so a new one is created
				} else if err == nil && pr != nil && pr.GetState() == "open" {
					log.Info("Found existing open PR, will update it")
					// Continue using the same PR
				}
			}
		}
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
		log.Info("PR already exists, will not create a new one", "URL", prURL)
		return prURL, nil
	}

	// Check if there's already a PR open for this branch before creating a new one
	parts = strings.Split(finops.Spec.Repository, "/")
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid repository format: %s", finops.Spec.Repository)
	}
	owner, repoName = parts[0], parts[1]
	
	// List PRs to see if one already exists for this branch
	listOptions := &github.PullRequestListOptions{
		State: "open",
		Head:  branchName, // This should match the name of our branch
	}
	existingPrs, _, err := client.PullRequests.List(ctx, owner, repoName, listOptions)
	if err != nil {
		log.Error(err, "Error checking for existing PRs")
	} else if len(existingPrs) > 0 {
		// We found an existing PR for this branch
		pr := existingPrs[0]
		log.Info("Found existing PR for this branch", "number", pr.GetNumber(), "URL", pr.GetHTMLURL())
		return pr.GetHTMLURL(), nil
	}

	// Create new PR with more specific title
	title := fmt.Sprintf("Resize resources for %s", finops.Name)
	body := fmt.Sprintf("This PR was created by the FinOps Operator to update resource requirements for %s/%s.\n\n%s",
		finops.Namespace, finops.Name, commitMsg)

	pr, _, err := client.PullRequests.Create(ctx, owner, repoName, &github.NewPullRequest{
		Title: github.String(title),
		Body:  github.String(body),
		Head:  github.String(branchName),
		Base:  github.String("main"), // Assuming main is the default branch
	})
	if err != nil {
		// Check if this is the 422 error for "A pull request already exists"
		if strings.Contains(err.Error(), "422") && strings.Contains(err.Error(), "already exists") {
			// Try to find the PR again - it must exist but we didn't find it earlier
			listOptions := &github.PullRequestListOptions{
				State: "all", // Include all PRs, not just open ones
			}
			allPrs, _, listErr := client.PullRequests.List(ctx, owner, repoName, listOptions)
			if listErr == nil {
				for _, existingPr := range allPrs {
					// Compare on branch name
					if existingPr.GetHead().GetRef() == branchName {
						log.Info("Found existing PR after 422 error", 
							"number", existingPr.GetNumber(), 
							"URL", existingPr.GetHTMLURL(),
							"state", existingPr.GetState())
						return existingPr.GetHTMLURL(), nil
					}
				}
			}
			return "", fmt.Errorf("PR already exists but couldn't find it: %w", err)
		}
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

// Helper functions for resource value extraction:
func extractCPU(line string) string { 
    // Extract CPU value from a line like "    cpu: 100m" or similar
    parts := strings.Split(line, "cpu:")
    if len(parts) < 2 {
        return "100m" // Default if can't extract
    }
    
    // Get the value part and trim spaces/quotes
    value := strings.TrimSpace(parts[1])
    value = strings.Trim(value, "\"'")
    
    return value
}

func extractMem(line string) string {
    // Extract memory value from a line like "    memory: 128Mi" or similar
    parts := strings.Split(line, "memory:")
    if len(parts) < 2 {
        return "128Mi" // Default if can't extract
    }
    
    // Get the value part and trim spaces/quotes
    value := strings.TrimSpace(parts[1])
    value = strings.Trim(value, "\"'")
    
    return value
}

// Helper to extract PR number from URL
func extractPRNumber(prURL string) int {
	parts := strings.Split(prURL, "/")
	if len(parts) > 0 {
		numStr := parts[len(parts)-1]
		num, err := strconv.Atoi(numStr)
		if err == nil {
			return num
		}
	}
	return -1
}

// updateResourceValues updates CPU requests, memory requests/limits and removes CPU limits in YAML content
func updateResourceValues(content, cpuValue, memoryValue string) string {
    if content == "" {
        // If file doesn't exist, create a minimal valid YAML
        return fmt.Sprintf(`# Generated by FinOps Operator
resources:
  requests:
    cpu: %s
    memory: %s
  limits:
    memory: %s
`, cpuValue, memoryValue, memoryValue)
    }

    // Split content into lines for processing
    lines := strings.Split(content, "\n")
    
    // Track the state of where we are in the YAML
    inRequests := false
    inLimits := false
    inValue := false // For kustomize patches
    
    // Process line by line
    for i, line := range lines {
        trimmedLine := strings.TrimSpace(line)
        
        // Detect structure based on indentation and content
        if strings.HasPrefix(trimmedLine, "requests:") {
            inRequests = true
            inLimits = false
            continue
        } else if strings.HasPrefix(trimmedLine, "limits:") {
            inRequests = false
            inLimits = true
            continue
        } else if strings.Contains(trimmedLine, "value:") {
            inValue = true
            continue
        }
        
        // Handle CPU request
        if (inRequests || (inValue && strings.Contains(line, "requests:"))) && 
           strings.Contains(trimmedLine, "cpu:") {
            indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
            lines[i] = indent + "cpu: \"" + cpuValue + "\""
            continue
        }
        
        // Handle memory request
        if (inRequests || (inValue && strings.Contains(line, "requests:"))) && 
           strings.Contains(trimmedLine, "memory:") {
            indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
            lines[i] = indent + "memory: \"" + memoryValue + "\""
            continue
        }
        
        // Handle CPU limit (remove it)
        if (inLimits || (inValue && strings.Contains(line, "limits:"))) && 
           strings.Contains(trimmedLine, "cpu:") {
            lines[i] = "" // Remove this line
            continue
        }
        
        // Handle memory limit
        if (inLimits || (inValue && strings.Contains(line, "limits:"))) && 
           strings.Contains(trimmedLine, "memory:") {
            indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
            lines[i] = indent + "memory: \"" + memoryValue + "\""
            continue
        }
    }
    
    // Clean up empty lines and consecutive empty lines
    var cleanedLines []string
    wasEmpty := false
    
    for _, line := range lines {
        if line == "" {
            if (!wasEmpty) {
                cleanedLines = append(cleanedLines, line)
                wasEmpty = true
            }
        } else {
            cleanedLines = append(cleanedLines, line)
            wasEmpty = false
        }
    }
    
    return strings.Join(cleanedLines, "\n")
}
