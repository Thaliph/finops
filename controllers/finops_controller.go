package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"github.com/google/go-github/v43/github"
	"github.com/robfig/cron/v3"
	vpa_api "k8s.io/autoscaler/vertical-pod-autoscaler/pkg/apis/autoscaling.k8s.io/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	tasksv1alpha1 "github.com/thaliph/k8s-ctrl/api/v1alpha1"
	"golang.org/x/oauth2"
)

// FinOpsReconciler reconciles a FinOps object
type FinOpsReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Log      logr.Logger
	crontabs map[string]cron.EntryID
	cronImpl *cron.Cron
}

// +kubebuilder:rbac:groups=tasks.thaliph.com,resources=finops,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=tasks.thaliph.com,resources=finops/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=tasks.thaliph.com,resources=finops/finalizers,verbs=update
// +kubebuilder:rbac:groups=autoscaling.k8s.io,resources=verticalpodautoscalers,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// NewFinOpsReconciler creates a new FinOpsReconciler
func NewFinOpsReconciler(client client.Client, scheme *runtime.Scheme) *FinOpsReconciler {
	cronImpl := cron.New(cron.WithSeconds())
	cronImpl.Start()
	
	return &FinOpsReconciler{
		Client:   client,
		Scheme:   scheme,
		crontabs: make(map[string]cron.EntryID),
		cronImpl: cronImpl,
	}
}

// Reconcile handles the reconciliation logic for FinOps resources
func (r *FinOpsReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling FinOps", "finops", req.NamespacedName)

	// Fetch the FinOps instance
	finops := &tasksv1alpha1.FinOps{}
	err := r.Get(ctx, req.NamespacedName, finops)
	if err != nil {
		if errors.IsNotFound(err) {
			// Remove the cron job if it exists
			if id, exists := r.crontabs[req.String()]; exists {
				r.cronImpl.Remove(id)
				delete(r.crontabs, req.String())
				logger.Info("Removed cron job for deleted FinOps resource")
			}
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get FinOps resource")
		return ctrl.Result{}, err
	}

	// Set up the cron job for this resource
	if id, exists := r.crontabs[req.String()]; exists {
		// Update the existing cron job if schedule changed
		r.cronImpl.Remove(id)
		delete(r.crontabs, req.String())
	}

	// Create a new cron job
	id, err := r.cronImpl.AddFunc(finops.Spec.Schedule, func() {
		r.processingFinOps(finops)
	})

	if err != nil {
		logger.Error(err, "Failed to set up cron job")
		finops.Status.Phase = "Failed"
		finops.Status.Message = fmt.Sprintf("Failed to set up cron job: %v", err)
		if updateErr := r.Status().Update(ctx, finops); updateErr != nil {
			logger.Error(updateErr, "Failed to update FinOps status")
		}
		return ctrl.Result{}, err
	}

	// Store cron id for future reference
	r.crontabs[req.String()] = id

	// Calculate and set the next run time
	schedule, err := cron.ParseStandard(finops.Spec.Schedule)
	if err == nil {
		nextRun := schedule.Next(time.Now())
		t := metav1.NewTime(nextRun)
		finops.Status.NextRunTime = &t
		
		if finops.Status.Phase == "" {
			finops.Status.Phase = "Scheduled"
		}
		
		if err := r.Status().Update(ctx, finops); err != nil {
			logger.Error(err, "Failed to update FinOps status")
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// processingFinOps processes the FinOps resource to create PR with VPA recommendations
func (r *FinOpsReconciler) processingFinOps(finops *tasksv1alpha1.FinOps) {
	ctx := context.Background()
	logger := r.Log.WithValues("finops", types.NamespacedName{Name: finops.Name, Namespace: finops.Namespace})
	
	// Update status to Running
	finops.Status.Phase = "Running"
	now := metav1.Now()
	finops.Status.LastRunTime = &now
	if err := r.Status().Update(ctx, finops); err != nil {
		logger.Error(err, "Failed to update FinOps status to Running")
		return
	}

	// 1. Get VPA recommendations
	vpaNamespace := finops.Spec.VPAReference.Namespace
	if vpaNamespace == "" {
		vpaNamespace = finops.Namespace
	}

	vpa := &vpa_api.VerticalPodAutoscaler{}
	err := r.Get(ctx, types.NamespacedName{
		Name:      finops.Spec.VPAReference.Name,
		Namespace: vpaNamespace,
	}, vpa)

	if err != nil {
		logger.Error(err, "Failed to get VPA resource")
		finops.Status.Phase = "Failed"
		finops.Status.Message = fmt.Sprintf("Failed to get VPA resource: %v", err)
		if updateErr := r.Status().Update(ctx, finops); updateErr != nil {
			logger.Error(updateErr, "Failed to update FinOps status")
		}
		return
	}

	// 2. Extract recommendations
	recommendations := make(map[string]tasksv1alpha1.ResourceRecommendation)
	if vpa.Status.Recommendation != nil {
		for _, containerRecommendation := range vpa.Status.Recommendation.ContainerRecommendations {
			recommendations[containerRecommendation.ContainerName] = tasksv1alpha1.ResourceRecommendation{
				CPU:    containerRecommendation.Target.Cpu().String(),
				Memory: containerRecommendation.Target.Memory().String(),
			}
		}
	}

	if len(recommendations) == 0 {
		logger.Info("No recommendations available from VPA")
		finops.Status.Phase = "Completed"
		finops.Status.Message = "No recommendations available from VPA"
		finops.Status.LatestRecommendations = recommendations
		if err := r.Status().Update(ctx, finops); err != nil {
			logger.Error(err, "Failed to update FinOps status")
		}
		return
	}

	// Store recommendations in status
	finops.Status.LatestRecommendations = recommendations

	// 3. Get GitHub credentials from secret
	secretNamespace := finops.Spec.GitHubSecretRef.Namespace
	if secretNamespace == "" {
		secretNamespace = finops.Namespace
	}

	secret := &corev1.Secret{}
	err = r.Get(ctx, types.NamespacedName{
		Name:      finops.Spec.GitHubSecretRef.Name,
		Namespace: secretNamespace,
	}, secret)

	if err != nil {
		logger.Error(err, "Failed to get GitHub credentials secret")
		finops.Status.Phase = "Failed"
		finops.Status.Message = fmt.Sprintf("Failed to get GitHub credentials: %v", err)
		if updateErr := r.Status().Update(ctx, finops); updateErr != nil {
			logger.Error(updateErr, "Failed to update FinOps status")
		}
		return
	}

	// Get username and token from secret
	usernameKey := finops.Spec.GitHubSecretRef.UsernameKey
	if usernameKey == "" {
		usernameKey = "username"
	}

	tokenKey := finops.Spec.GitHubSecretRef.TokenKey
	if tokenKey == "" {
		tokenKey = "token"
	}

	username := string(secret.Data[usernameKey])
	token := string(secret.Data[tokenKey])

	if username == "" || token == "" {
		logger.Error(nil, "GitHub username or token is empty")
		finops.Status.Phase = "Failed"
		finops.Status.Message = "GitHub username or token is empty"
		if updateErr := r.Status().Update(ctx, finops); updateErr != nil {
			logger.Error(updateErr, "Failed to update FinOps status")
		}
		return
	}

	// 4. Create GitHub client
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)
	tc := oauth2.NewClient(ctx, ts)
	client := github.NewClient(tc)

	// Parse owner and repo
	ownerRepo := strings.Split(finops.Spec.GitHubRepository, "/")
	if len(ownerRepo) != 2 {
		logger.Error(nil, "Invalid GitHub repository format")
		finops.Status.Phase = "Failed"
		finops.Status.Message = "Invalid GitHub repository format, should be owner/repo"
		if updateErr := r.Status().Update(ctx, finops); updateErr != nil {
			logger.Error(updateErr, "Failed to update FinOps status")
		}
		return
	}

	owner := ownerRepo[0]
	repo := ownerRepo[1]

	// 5. Get the file content from GitHub
	fileContent, _, _, err := client.Repositories.GetContents(ctx, owner, repo, 
		fmt.Sprintf("%s/%s", finops.Spec.FilePath, finops.Spec.FileName), nil)
	
	if err != nil {
		logger.Error(err, "Failed to get file content from GitHub")
		finops.Status.Phase = "Failed"
		finops.Status.Message = fmt.Sprintf("Failed to get file from GitHub: %v", err)
		if updateErr := r.Status().Update(ctx, finops); updateErr != nil {
			logger.Error(updateErr, "Failed to update FinOps status")
		}
		return
	}

	content, err := fileContent.GetContent()
	if err != nil {
		logger.Error(err, "Failed to decode file content")
		finops.Status.Phase = "Failed"
		finops.Status.Message = fmt.Sprintf("Failed to decode file content: %v", err)
		if updateErr := r.Status().Update(ctx, finops); updateErr != nil {
			logger.Error(updateErr, "Failed to update FinOps status")
		}
		return
	}

	// 6. Update file content with VPA recommendations
	// This is a simple example that assumes the file is a YAML/JSON that can be directly updated
	// In a real implementation, you would need to parse the file format correctly
	updatedContent := content
	for containerName, recommendation := range recommendations {
		// This is a simplified approach - in real life you'd need to properly parse and update the file
		// depending on its format (YAML, JSON, HCL, etc.)
		cpuPattern := fmt.Sprintf("cpu: .*#%s", containerName)
		memoryPattern := fmt.Sprintf("memory: .*#%s", containerName)
		
		cpuReplacement := fmt.Sprintf("cpu: %s #%s", recommendation.CPU, containerName)
		memoryReplacement := fmt.Sprintf("memory: %s #%s", recommendation.Memory, containerName)
		
		// Very simple pattern replacement - would need to be more sophisticated in real implementation
		updatedContent = strings.ReplaceAll(updatedContent, cpuPattern, cpuReplacement)
		updatedContent = strings.ReplaceAll(updatedContent, memoryPattern, memoryReplacement)
	}

	// If content didn't change, don't create a PR
	if updatedContent == content {
		logger.Info("No changes needed in the file")
		finops.Status.Phase = "Completed"
		finops.Status.Message = "No changes needed in the file"
		if updateErr := r.Status().Update(ctx, finops); updateErr != nil {
			logger.Error(updateErr, "Failed to update FinOps status")
		}
		return
	}

	// 7. Create a branch for the PR
	branchRef := fmt.Sprintf("refs/heads/finops-update-%s-%s", 
		finops.Name, time.Now().Format("20060102-150405"))
	
	// Get the reference to the main branch
	mainBranch, _, err := client.Repositories.GetBranch(ctx, owner, repo, "main", false)
	if err != nil {
		// Try master if main fails
		mainBranch, _, err = client.Repositories.GetBranch(ctx, owner, repo, "master", false)
		if err != nil {
			logger.Error(err, "Failed to get main/master branch")
			finops.Status.Phase = "Failed"
			finops.Status.Message = fmt.Sprintf("Failed to get main/master branch: %v", err)
			if updateErr := r.Status().Update(ctx, finops); updateErr != nil {
				logger.Error(updateErr, "Failed to update FinOps status")
			}
			return
		}
	}

	// Create new branch
	newRef := &github.Reference{
		Ref: github.String(branchRef),
		Object: &github.GitObject{
			SHA: mainBranch.Commit.SHA,
		},
	}
	
	_, _, err = client.Git.CreateRef(ctx, owner, repo, newRef)
	if err != nil {
		logger.Error(err, "Failed to create branch")
		finops.Status.Phase = "Failed"
		finops.Status.Message = fmt.Sprintf("Failed to create branch: %v", err)
		if updateErr := r.Status().Update(ctx, finops); updateErr != nil {
			logger.Error(updateErr, "Failed to update FinOps status")
		}
		return
	}

	// 8. Update the file in the new branch
	opts := &github.RepositoryContentFileOptions{
		Message: github.String("Update resource requirements based on VPA recommendations"),
		Content: []byte(updatedContent),
		SHA:     fileContent.SHA,
		Branch:  github.String(branchRef),
		Author: &github.CommitAuthor{
			Name:  github.String("FinOps Controller"),
			Email: github.String("finops-controller@example.com"),
		},
	}

	_, _, err = client.Repositories.UpdateFile(ctx, owner, repo, 
		fmt.Sprintf("%s/%s", finops.Spec.FilePath, finops.Spec.FileName), opts)
	
	if err != nil {
		logger.Error(err, "Failed to update file in GitHub")
		finops.Status.Phase = "Failed"
		finops.Status.Message = fmt.Sprintf("Failed to update file: %v", err)
		if updateErr := r.Status().Update(ctx, finops); updateErr != nil {
			logger.Error(updateErr, "Failed to update FinOps status")
		}
		return
	}

	// 9. Create a pull request
	baseBranch := "main"
	if mainBranch == nil {
		baseBranch = "master"
	}

	recommendationsJSON, _ := json.MarshalIndent(recommendations, "", "  ")
	prTitle := fmt.Sprintf("FinOps: Update resource requirements for %s", finops.Name)
	prBody := fmt.Sprintf("This PR updates resource requirements based on VPA recommendations.\n\n"+
		"Generated by FinOps controller for resource '%s' in namespace '%s'.\n\n"+
		"Recommendations:\n```json\n%s\n```",
		finops.Name, finops.Namespace, string(recommendationsJSON))

	newPR := &github.NewPullRequest{
		Title: github.String(prTitle),
		Body:  github.String(prBody),
		Head:  github.String(branchRef),
		Base:  github.String(baseBranch),
	}

	pr, _, err := client.PullRequests.Create(ctx, owner, repo, newPR)
	if err != nil {
		logger.Error(err, "Failed to create pull request")
		finops.Status.Phase = "Failed"
		finops.Status.Message = fmt.Sprintf("Failed to create pull request: %v", err)
		if updateErr := r.Status().Update(ctx, finops); updateErr != nil {
			logger.Error(updateErr, "Failed to update FinOps status")
		}
		return
	}

	// 10. Update the FinOps status with success
	finops.Status.Phase = "Succeeded"
	finops.Status.Message = fmt.Sprintf("Pull request created successfully: %s", *pr.HTMLURL)
	finops.Status.LatestPRURL = *pr.HTMLURL
	
	// Calculate next run time
	schedule, err := cron.ParseStandard(finops.Spec.Schedule)
	if err == nil {
		nextRun := schedule.Next(time.Now())
		t := metav1.NewTime(nextRun)
		finops.Status.NextRunTime = &t
	}

	if err := r.Status().Update(ctx, finops); err != nil {
		logger.Error(err, "Failed to update FinOps status")
	}
}

// SetupWithManager sets up the controller with the Manager
func (r *FinOpsReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&tasksv1alpha1.FinOps{}).
		Complete(r)
}
