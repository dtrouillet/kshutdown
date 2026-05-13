/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ShutdownGroupState represents the lifecycle state of a ShutdownGroup.
// +kubebuilder:validation:Enum=up;down;unknown
type ShutdownGroupState string

const (
	StateUp      ShutdownGroupState = "up"
	StateDown    ShutdownGroupState = "down"
	StateUnknown ShutdownGroupState = "unknown"
)

// ShutdownGroupSpec defines the desired state of ShutdownGroup.
type ShutdownGroupSpec struct {
	// Targets defines the set of workloads to manage.
	// Each entry selects resources by labelSelector, optionally scoped to a namespace.
	// If namespace is omitted, the ShutdownGroup's own namespace is used.
	// +kubebuilder:validation:MinItems=1
	Targets []Target `json:"targets"`
}

// Target selects a set of workloads within a namespace.
type Target struct {
	// Namespace to search in. Defaults to the ShutdownGroup's own namespace.
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// LabelSelector selects workloads within the namespace.
	// +kubebuilder:validation:Required
	LabelSelector map[string]string `json:"labelSelector"`
}

// ShutdownGroupStatus defines the observed state of ShutdownGroup.
type ShutdownGroupStatus struct {
	// State is the current lifecycle state of the ShutdownGroup.
	// +kubebuilder:validation:Enum=up;down;unknown
	// +optional
	State ShutdownGroupState `json:"state,omitempty"`

	// Since is the timestamp of the last state transition.
	// +optional
	Since *metav1.Time `json:"since,omitempty"`

	// Operator is the Kubernetes username who triggered the last operation.
	// +optional
	Operator string `json:"operator,omitempty"`

	// Reason is the human-readable justification provided at shutdown time.
	// +optional
	Reason string `json:"reason,omitempty"`

	// Snapshot holds replica counts captured before scale-down.
	// Written once during reconcileDown. Cleared during reconcileUp.
	// Must never be overwritten if non-empty.
	// +optional
	Snapshot []ResourceSnapshot `json:"snapshot,omitempty"`

	// History records the last operations for audit purposes.
	// +optional
	History []HistoryEntry `json:"history,omitempty"`
}

// ResourceSnapshot captures the state of a workload before scale-down.
type ResourceSnapshot struct {
	// Namespace of the captured resource.
	Namespace string `json:"namespace"`
	// Name of the captured resource.
	Name string `json:"name"`
	// Kind is Deployment, StatefulSet, or CronJob.
	// +kubebuilder:validation:Enum=Deployment;StatefulSet;CronJob
	Kind string `json:"kind"`
	// PreviousReplicas is the replica count before scale-down.
	// For CronJob: 0=was active, 1=was already suspended.
	// +kubebuilder:validation:Minimum=0
	PreviousReplicas int32 `json:"previousReplicas"`
}

// HistoryEntry records a single down or up operation.
type HistoryEntry struct {
	// Operation is "down" or "up".
	// +kubebuilder:validation:Enum=down;up
	Operation string `json:"operation"`
	// At is the timestamp of the operation.
	At metav1.Time `json:"at"`
	// Operator is the Kubernetes username.
	Operator string `json:"operator"`
	// Reason is the justification (only present for "down" operations).
	// +optional
	Reason string `json:"reason,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=sg
// +kubebuilder:printcolumn:name="State",type=string,JSONPath=`.status.state`
// +kubebuilder:printcolumn:name="Since",type=date,JSONPath=`.status.since`
// +kubebuilder:printcolumn:name="Operator",type=string,JSONPath=`.status.operator`
// +kubebuilder:printcolumn:name="Reason",type=string,JSONPath=`.status.reason`

// ShutdownGroup is the Schema for the shutdowngroups API.
type ShutdownGroup struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +optional
	Spec ShutdownGroupSpec `json:"spec,omitempty"`

	// +optional
	Status ShutdownGroupStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ShutdownGroupList contains a list of ShutdownGroup.
type ShutdownGroupList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ShutdownGroup `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ShutdownGroup{}, &ShutdownGroupList{})
}
