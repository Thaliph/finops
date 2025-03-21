package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// FinOpsSpec defines the desired state of FinOps
type FinOpsSpec struct {
	// Repository is the GitHub repository name
	// Format: "owner/repo"
	Repository string `json:"repository"`

	// Path is the directory path within the repository
	Path string `json:"path"`

	// FileName is the name of the file to update
	FileName string `json:"fileName"`

	// VPARef is a reference to a Vertical Pod Autoscaler
	// Format: "namespace/name"
	VPARef string `json:"vpaRef"`

	// SecretRef is a reference to a Kubernetes Secret containing Git credentials
	// The secret should contain 'username' and 'token' keys
	SecretRef string `json:"secretRef"`

	// Schedule defines how often to retrieve VPA recommendations
	// Can be either cron format or a duration like "30s"
	Schedule string `json:"schedule"`
}

// FinOpsStatus defines the observed state of FinOps
type FinOpsStatus struct {
	// LastRun is the timestamp of the last successful run
	LastRun *metav1.Time `json:"lastRun,omitempty"`

	// CurrentPR is the URL of the current open PR (if any)
	CurrentPR string `json:"currentPR,omitempty"`

	// LastRecommendation contains the most recent VPA recommendation
	LastRecommendation *ResourceRecommendation `json:"lastRecommendation,omitempty"`

	// Conditions represent the latest available observations of the FinOps state
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// ResourceRecommendation contains the CPU and memory recommendations
type ResourceRecommendation struct {
	// CPU recommendation in millicores
	CPU string `json:"cpu,omitempty"`
	
	// Memory recommendation in bytes
	Memory string `json:"memory,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:printcolumn:name="Repository",type=string,JSONPath=`.spec.repository`
//+kubebuilder:printcolumn:name="VPA",type=string,JSONPath=`.spec.vpaRef`
//+kubebuilder:printcolumn:name="Schedule",type=string,JSONPath=`.spec.schedule`
//+kubebuilder:printcolumn:name="LastRun",type=string,JSONPath=`.status.lastRun`
//+kubebuilder:printcolumn:name="CurrentPR",type=string,JSONPath=`.status.currentPR`
//+kubebuilder:resource:shortName=fo

// FinOps is the Schema for the finops API
type FinOps struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   FinOpsSpec   `json:"spec,omitempty"`
	Status FinOpsStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// FinOpsList contains a list of FinOps
type FinOpsList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []FinOps `json:"items"`
}

func init() {
	SchemeBuilder.Register(&FinOps{}, &FinOpsList{})
}
