package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Status",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="LastRun",type="string",JSONPath=".status.lastRunTime"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// FinOps is the Schema for the finops API
type FinOps struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   FinOpsSpec   `json:"spec,omitempty"`
	Status FinOpsStatus `json:"status,omitempty"`
}

// FinOpsSpec defines the desired state of FinOps
type FinOpsSpec struct {
	// GitHub repository in format "owner/repo"
	GitHubRepository string `json:"githubRepository"`

	// Path to the file to modify within the repository
	FilePath string `json:"filePath"`

	// Name of the file to modify
	FileName string `json:"fileName"`

	// Reference to the VPA to get recommendations from
	VPAReference VPAReference `json:"vpaReference"`

	// Reference to secret containing GitHub credentials
	GitHubSecretRef SecretReference `json:"githubSecretRef"`

	// Schedule in cron format, e.g. "0 0 * * *" for daily at midnight
	Schedule string `json:"schedule"`
}

// VPAReference contains the reference details of the VPA
type VPAReference struct {
	// Name of the VPA
	Name string `json:"name"`

	// Namespace of the VPA, defaults to the same namespace as the FinOps resource
	// +optional
	Namespace string `json:"namespace,omitempty"`
}

// SecretReference contains the reference details of a secret
type SecretReference struct {
	// Name of the secret
	Name string `json:"name"`

	// Namespace of the secret, defaults to the same namespace as the FinOps resource
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// Key in the secret containing GitHub username
	UsernameKey string `json:"usernameKey,omitempty"`

	// Key in the secret containing GitHub token/password
	TokenKey string `json:"tokenKey,omitempty"`
}

// FinOpsStatus defines the observed state of FinOps
type FinOpsStatus struct {
	// Phase of the FinOps operation: Pending, Running, Succeeded, Failed
	Phase string `json:"phase,omitempty"`

	// Time when the last run was executed
	LastRunTime *metav1.Time `json:"lastRunTime,omitempty"`

	// URL of the latest pull request
	LatestPRURL string `json:"latestPRURL,omitempty"`

	// Message provides more information about the status
	Message string `json:"message,omitempty"`

	// Next scheduled run time
	NextRunTime *metav1.Time `json:"nextRunTime,omitempty"`

	// Latest recommendations from VPA
	LatestRecommendations map[string]ResourceRecommendation `json:"latestRecommendations,omitempty"`
}

// ResourceRecommendation contains resource recommendations from VPA
type ResourceRecommendation struct {
	// Recommended CPU value
	CPU string `json:"cpu,omitempty"`

	// Recommended Memory value
	Memory string `json:"memory,omitempty"`
}

// +kubebuilder:object:root=true

// FinOpsList contains a list of FinOps
type FinOpsList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []FinOps `json:"items"`
}

func init() {
	SchemeBuilder.Register(&FinOps{}, &FinOpsList{})
}
