package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Status",type="string",JSONPath=".status.state"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// Task is a specification for a Task resource
type Task struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   TaskSpec   `json:"spec,omitempty"`
	Status TaskStatus `json:"status,omitempty"`
}

// TaskSpec defines the desired state of Task
type TaskSpec struct {
	// Description of the task
	Description string `json:"description"`
	
	// Owner of the task
	Owner string `json:"owner,omitempty"`
	
	// Priority of the task (Low, Medium, High)
	Priority string `json:"priority,omitempty"`
	
	// Deadline for the task
	Deadline *metav1.Time `json:"deadline,omitempty"`
}

// TaskStatus defines the observed state of Task
type TaskStatus struct {
	// State of the task (New, InProgress, Completed, Blocked)
	State string `json:"state,omitempty"`
	
	// Time when the task was last updated
	LastUpdateTime metav1.Time `json:"lastUpdateTime,omitempty"`
	
	// Message provides more information about the task status
	Message string `json:"message,omitempty"`
}

// +kubebuilder:object:root=true

// TaskList contains a list of Task
type TaskList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Task `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Task{}, &TaskList{})
}
