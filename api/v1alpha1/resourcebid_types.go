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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ResourceBidSpec defines the desired state of ResourceBid
type ResourceBidSpec struct {
	// Type specifies the resource provider type (e.g., "snowflake", "postgres", "aws-account")
	// This field is required and determines which provider will handle the resource
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Type string `json:"type"`

	// LeaseDuration specifies how long to hold the resource
	// Defaults to 1 hour if not specified
	// +optional
	// +kubebuilder:default="1h"
	LeaseDuration *metav1.Duration `json:"leaseDuration,omitempty"`

	// RequiredLabels specifies labels that must match on the resource
	// Used to filter which resources from the pool can be assigned
	// For example: {"tier": "standard", "region": "us-east-1"}
	// +optional
	RequiredLabels map[string]string `json:"requiredLabels,omitempty"`

	// Priority affects assignment order when multiple claims are pending
	// Higher priority claims are assigned first
	// +optional
	// +kubebuilder:default=0
	Priority int32 `json:"priority,omitempty"`
}

// ResourceBidStatus defines the observed state of ResourceBid
type ResourceBidStatus struct {
	// Phase indicates the current lifecycle phase
	// +kubebuilder:validation:Enum=Pending;Bound;Failed;Releasing
	// +optional
	Phase string `json:"phase,omitempty"`

	// LeaseName is the name of the Kubernetes Lease object that was acquired
	// This can be used to inspect the lease directly
	// +optional
	LeaseName string `json:"leaseName,omitempty"`

	// AcquiredAt is the timestamp when the lease was successfully acquired
	// +optional
	AcquiredAt *metav1.Time `json:"acquiredAt,omitempty"`

	// ExpiresAt is when the lease will expire (AcquiredAt + LeaseDuration)
	// +optional
	ExpiresAt *metav1.Time `json:"expiresAt,omitempty"`

	// ConnectionDetails contains provider-specific connection information
	// This is the primary output consumed by users/CI pipelines
	// The keys and values are provider-specific (e.g., for Snowflake: accountUrl, warehouse, etc.)
	// +optional
	ConnectionDetails map[string]string `json:"connectionDetails,omitempty"`

	// CredentialSecretRef points to the Secret containing credentials
	// Secret is in the same namespace as this ResourceBid
	// +optional
	CredentialSecretRef *corev1.LocalObjectReference `json:"credentialSecretRef,omitempty"`

	// Message provides human-readable status information
	// Examples: "No free resources available", "Successfully acquired resource-lease-snowflake-1"
	// +optional
	Message string `json:"message,omitempty"`

	// Conditions represent the latest available observations of the claim's state
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=rb;rbs
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=`.spec.type`
// +kubebuilder:printcolumn:name="Lease",type=string,JSONPath=`.status.leaseName`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// ResourceBid is the Schema for the resourcebids API
type ResourceBid struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of ResourceBid
	// +required
	Spec ResourceBidSpec `json:"spec"`

	// status defines the observed state of ResourceBid
	// +optional
	Status ResourceBidStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// ResourceBidList contains a list of ResourceBid
type ResourceBidList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []ResourceBid `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ResourceBid{}, &ResourceBidList{})
}
