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

package provider

import (
	"context"

	corev1 "k8s.io/api/core/v1"
)

// Resource represents a single managed resource instance
type Resource struct {
	// Name is the unique identifier for this resource (used in lease name)
	Name string

	// Labels are used for filtering which resources can be assigned to claims
	// For example: {"tier": "standard", "region": "us-east-1"}
	Labels map[string]string

	// CredentialSecretName is the name of the Kubernetes Secret containing credentials
	CredentialSecretName string

	// Metadata contains provider-specific metadata
	// This can store any additional information needed by the provider
	Metadata map[string]string
}

// ResourceProvider is the interface that all resource type handlers must implement
// Each provider (snowflake, postgres, aws-account, etc.) implements this interface
type ResourceProvider interface {
	// Name returns the provider type name (e.g., "snowflake", "postgres")
	// This is used in ResourceClaim.Spec.Type to select the provider
	Name() string

	// Initialize is called once at operator startup
	// Provider should load configuration from ConfigMap and return list of managed resources
	// The namespace parameter indicates where to find the provider's ConfigMap
	Initialize(ctx context.Context, namespace string) ([]Resource, error)

	// GetConnectionDetails returns connection information for a resource
	// This is what gets populated in ResourceClaim.Status.ConnectionDetails
	// The returned map is provider-specific (e.g., for Snowflake: accountUrl, warehouse, etc.)
	GetConnectionDetails(ctx context.Context, resource Resource) (map[string]string, error)

	// GetCredentialSecret retrieves the Secret for a resource
	// Returns the Secret object containing credentials (username, password, keys, etc.)
	GetCredentialSecret(ctx context.Context, namespace string, resource Resource) (*corev1.Secret, error)

	// AcquireResource is called when a lease is acquired.
	// Providers should perform per-claim setup here (e.g. creating an app user,
	// rotating credentials) and return the name of the Kubernetes Secret that
	// holds the credentials for the claim.  An empty string means "use the
	// resource's default CredentialSecretName".
	// The claimName parameter is "namespace/name" of the ResourceClaim.
	AcquireResource(ctx context.Context, resource Resource, claimName string) (string, error)

	// ReleaseResource is called during cleanup (when claim is deleted).
	// Should reset resource state, revoke app-user credentials, and rotate admin credentials.
	// claimName is "namespace/name" of the ResourceClaim (same convention as AcquireResource).
	ReleaseResource(ctx context.Context, namespace string, resource Resource, claimName string) error
}
