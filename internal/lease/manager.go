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

package lease

import (
	"context"
	"fmt"
	"time"

	coordinationv1 "k8s.io/api/coordination/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/nikhil-thomas/Resource-Pool/internal/provider"
)

const (
	// LabelManagedBy identifies leases managed by this operator
	LabelManagedBy = "app.kubernetes.io/managed-by"
	ManagedByValue = "resource-pool-operator"

	// LabelResourceType stores the provider type (e.g., "snowflake", "postgres")
	LabelResourceType = "pool.dataverse.redhat.com/resource-type"

	// LabelResourceName stores the resource name
	LabelResourceName = "pool.dataverse.redhat.com/resource-name"

	// LeasePrefix is prepended to all lease names
	LeasePrefix = "resource-lease-"
)

// Manager handles Kubernetes Lease operations for the resource pool
type Manager struct {
	client    client.Client
	namespace string
}

// NewManager creates a new lease manager
func NewManager(client client.Client, namespace string) *Manager {
	return &Manager{
		client:    client,
		namespace: namespace,
	}
}

// InitializeLeasesForProvider creates one Lease per resource for a given provider
// This should be called once for each provider at operator startup
func (m *Manager) InitializeLeasesForProvider(ctx context.Context, providerType string, resources []provider.Resource) error {
	log := log.FromContext(ctx)

	for _, resource := range resources {
		// Lease name format: resource-lease-<providerType>-<resourceName>
		leaseName := fmt.Sprintf("%s%s-%s", LeasePrefix, providerType, resource.Name)

		// Prepare labels - merge managed-by labels with resource labels
		leaseLabels := map[string]string{
			LabelManagedBy:    ManagedByValue,
			LabelResourceType: providerType,
			LabelResourceName: resource.Name,
		}

		// Add all resource labels (for filtering)
		for k, v := range resource.Labels {
			leaseLabels[k] = v
		}

		lease := &coordinationv1.Lease{
			ObjectMeta: metav1.ObjectMeta{
				Name:      leaseName,
				Namespace: m.namespace,
				Labels:    leaseLabels,
			},
			Spec: coordinationv1.LeaseSpec{
				// Leave holderIdentity, acquireTime, leaseDurationSeconds nil
				// This indicates the lease is free
			},
		}

		err := m.client.Create(ctx, lease)
		if err != nil {
			if client.IgnoreAlreadyExists(err) == nil {
				log.V(1).Info("Lease already exists", "lease", leaseName)
				continue
			}
			return fmt.Errorf("failed to create lease %s: %w", leaseName, err)
		}

		log.Info("Created lease", "lease", leaseName, "provider", providerType, "resource", resource.Name)
	}

	return nil
}

// FindFreeLease finds the first available lease matching provider type and required labels
// Returns the lease and the resource name, or error if none available
func (m *Manager) FindFreeLease(ctx context.Context, providerType string, requiredLabels map[string]string) (*coordinationv1.Lease, string, error) {
	log := log.FromContext(ctx)

	// Build label selector
	// We always filter by managed-by and resource-type
	selectorLabels := map[string]string{
		LabelManagedBy:    ManagedByValue,
		LabelResourceType: providerType,
	}

	// List all leases for this provider
	leaseList := &coordinationv1.LeaseList{}
	labelSelector := labels.SelectorFromSet(selectorLabels)

	err := m.client.List(ctx, leaseList, &client.ListOptions{
		Namespace:     m.namespace,
		LabelSelector: labelSelector,
	})
	if err != nil {
		return nil, "", fmt.Errorf("failed to list leases: %w", err)
	}

	log.V(1).Info("Found leases for provider", "provider", providerType, "count", len(leaseList.Items))

	// Check each lease
	for i := range leaseList.Items {
		lease := &leaseList.Items[i]

		// Check if lease is free
		if !m.isLeaseFree(lease) {
			log.V(1).Info("Lease is busy", "lease", lease.Name, "holder", ptr.Deref(lease.Spec.HolderIdentity, ""))
			continue
		}

		// Check if lease labels match required labels
		if !matchesLabels(lease.Labels, requiredLabels) {
			log.V(1).Info("Lease doesn't match required labels",
				"lease", lease.Name,
				"required", requiredLabels,
				"actual", lease.Labels)
			continue
		}

		// Found a matching free lease!
		resourceName := lease.Labels[LabelResourceName]
		log.Info("Found free lease", "lease", lease.Name, "resource", resourceName)
		return lease, resourceName, nil
	}

	return nil, "", fmt.Errorf("no free leases available for provider %s matching labels: %v", providerType, requiredLabels)
}

// isLeaseFree checks if a lease is currently available
func (m *Manager) isLeaseFree(lease *coordinationv1.Lease) bool {
	// No holder = free
	if lease.Spec.HolderIdentity == nil {
		return true
	}

	// Check if lease has expired
	if lease.Spec.AcquireTime != nil && lease.Spec.LeaseDurationSeconds != nil {
		acquireTime := lease.Spec.AcquireTime.Time
		duration := time.Duration(*lease.Spec.LeaseDurationSeconds) * time.Second
		expiresAt := acquireTime.Add(duration)

		if time.Now().After(expiresAt) {
			return true // Lease has expired
		}
	}

	return false
}

// matchesLabels checks if lease labels satisfy required labels
func matchesLabels(leaseLabels, requiredLabels map[string]string) bool {
	if len(requiredLabels) == 0 {
		return true // no requirements = matches all
	}

	for key, requiredValue := range requiredLabels {
		leaseValue, exists := leaseLabels[key]
		if !exists || leaseValue != requiredValue {
			return false
		}
	}

	return true
}

// AcquireLease locks a lease for a ResourceClaim
func (m *Manager) AcquireLease(ctx context.Context, lease *coordinationv1.Lease, holderIdentity string, duration time.Duration) error {
	log := log.FromContext(ctx)

	// Refresh lease to get latest version
	fresh := &coordinationv1.Lease{}
	err := m.client.Get(ctx, client.ObjectKeyFromObject(lease), fresh)
	if err != nil {
		return fmt.Errorf("failed to get fresh lease: %w", err)
	}

	// Check if still free (race condition protection)
	if !m.isLeaseFree(fresh) {
		return fmt.Errorf("lease %s is no longer free (holder: %s)",
			fresh.Name, ptr.Deref(fresh.Spec.HolderIdentity, ""))
	}

	// Acquire the lease
	now := metav1.NowMicro()
	fresh.Spec.HolderIdentity = &holderIdentity
	fresh.Spec.AcquireTime = &now
	fresh.Spec.LeaseDurationSeconds = ptr.To(int32(duration.Seconds()))

	err = m.client.Update(ctx, fresh)
	if err != nil {
		return fmt.Errorf("failed to update lease: %w", err)
	}

	log.Info("Acquired lease",
		"lease", fresh.Name,
		"holder", holderIdentity,
		"duration", duration.String())

	return nil
}

// ReleaseLease frees a lease (clears holder)
func (m *Manager) ReleaseLease(ctx context.Context, leaseName string) error {
	log := log.FromContext(ctx)

	lease := &coordinationv1.Lease{}
	err := m.client.Get(ctx, client.ObjectKey{
		Name:      leaseName,
		Namespace: m.namespace,
	}, lease)
	if err != nil {
		return fmt.Errorf("failed to get lease: %w", err)
	}

	// Clear holder information
	lease.Spec.HolderIdentity = nil
	lease.Spec.AcquireTime = nil
	lease.Spec.LeaseDurationSeconds = nil

	err = m.client.Update(ctx, lease)
	if err != nil {
		return fmt.Errorf("failed to update lease: %w", err)
	}

	log.Info("Released lease", "lease", leaseName)
	return nil
}

// GetResourceNameFromLease extracts the resource name from lease labels
func (m *Manager) GetResourceNameFromLease(lease *coordinationv1.Lease) string {
	return lease.Labels[LabelResourceName]
}
