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

package controller

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	poolv1alpha1 "github.com/nikhil-thomas/Resource-Pool/api/v1alpha1"
	"github.com/nikhil-thomas/Resource-Pool/internal/lease"
	"github.com/nikhil-thomas/Resource-Pool/internal/provider"
)

const (
	finalizerName = "pool.dataverse.redhat.com/finalizer"

	// Resource phases
	phaseFailed    = "Failed"
	phasePending   = "Pending"
	phaseBound     = "Bound"
	phaseReleasing = "Releasing"

	// Default requeue times
	requeueAfterPending = 30 * time.Second
	requeueAfterError   = 1 * time.Minute
)

// ResourceBidReconciler reconciles a ResourceBid object
type ResourceBidReconciler struct {
	client.Client
	Scheme           *runtime.Scheme
	LeaseManager     *lease.Manager
	ProviderRegistry *provider.Registry
	Namespace        string
}

// +kubebuilder:rbac:groups=pool.dataverse.redhat.com,resources=resourcebids,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=pool.dataverse.redhat.com,resources=resourcebids/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=pool.dataverse.redhat.com,resources=resourcebids/finalizers,verbs=update
// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;update;patch

// Reconcile is part of the main kubernetes reconciliation loop
func (r *ResourceBidReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Fetch ResourceBid
	bid := &poolv1alpha1.ResourceBid{}
	err := r.Get(ctx, req.NamespacedName, bid)
	if err != nil {
		if errors.IsNotFound(err) {
			// Object not found, could have been deleted after reconcile request
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	log.Info("Reconciling ResourceBid", "bid", bid)
	// Validate spec.type field
	if bid.Spec.Type == "" {
		bid.Status.Phase = phaseFailed
		bid.Status.Message = "spec.type field is required"
		if err := r.Status().Update(ctx, bid); err != nil {
			log.Error(err, "Failed to update status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Get provider from registry
	prov, err := r.ProviderRegistry.Get(bid.Spec.Type)
	if err != nil {
		bid.Status.Phase = phaseFailed
		bid.Status.Message = fmt.Sprintf("Unknown provider type: %s", bid.Spec.Type)
		if err := r.Status().Update(ctx, bid); err != nil {
			log.Error(err, "Failed to update status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Handle deletion (finalizer logic)
	if !bid.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, bid, prov)
	}

	// Add finalizer if not present
	if !controllerutil.ContainsFinalizer(bid, finalizerName) {
		controllerutil.AddFinalizer(bid, finalizerName)
		if err := r.Update(ctx, bid); err != nil {
			return ctrl.Result{}, err
		}
		// Re-fetch after adding finalizer
		return ctrl.Result{Requeue: true}, nil
	}

	// If already bound, check if lease is still valid
	if bid.Status.Phase == phaseBound {
		// Already bound, return (no-op)
		// TODO: Optionally implement lease renewal logic here
		return ctrl.Result{}, nil
	}

	// Try to acquire a lease
	log.Info("Attempting to acquire lease for bid", "type", bid.Spec.Type)

	leaseLease, resourceName, err := r.LeaseManager.FindFreeLease(ctx, bid.Spec.Type, bid.Spec.RequiredLabels)
	if err != nil {
		// No free leases available
		bid.Status.Phase = "Pending"
		bid.Status.Message = fmt.Sprintf("Waiting for available resource: %v", err)

		if updateErr := r.Status().Update(ctx, bid); updateErr != nil {
			log.Error(updateErr, "Failed to update status to Pending")
			return ctrl.Result{}, updateErr
		}

		log.Info("No free leases available, requeuing", "after", requeueAfterPending)
		return ctrl.Result{RequeueAfter: requeueAfterPending}, nil
	}

	// Get resource details from provider — take a value copy so mutations below
	// don't corrupt the shared registry entry.
	resourcePtr := r.ProviderRegistry.GetResource(bid.Spec.Type, resourceName)
	if resourcePtr == nil {
		bid.Status.Phase = phaseFailed
		bid.Status.Message = fmt.Sprintf("Resource %s not found in provider cache", resourceName)
		if err := r.Status().Update(ctx, bid); err != nil {
			log.Error(err, "Failed to update status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}
	resource := *resourcePtr // local copy — safe to mutate

	// Determine lease duration
	duration := 1 * time.Hour // default
	if bid.Spec.LeaseDuration != nil {
		duration = bid.Spec.LeaseDuration.Duration
	}

	// Acquire the lease
	holderIdentity := fmt.Sprintf("%s/%s", bid.Namespace, bid.Name)
	err = r.LeaseManager.AcquireLease(ctx, leaseLease, holderIdentity, duration)
	if err != nil {
		log.Error(err, "Failed to acquire lease")
		bid.Status.Phase = phaseFailed
		bid.Status.Message = fmt.Sprintf("Failed to acquire lease: %v", err)
		if updateErr := r.Status().Update(ctx, bid); updateErr != nil {
			log.Error(updateErr, "Failed to update status")
			return ctrl.Result{}, updateErr
		}
		return ctrl.Result{RequeueAfter: requeueAfterError}, nil
	}

	// Call provider's AcquireResource hook — returns the name of the per-bid credential secret.
	secretName, err := prov.AcquireResource(ctx, resource, holderIdentity)
	if err != nil {
		log.Error(err, "Provider AcquireResource failed")
		bid.Status.Phase = phaseFailed
		bid.Status.Message = fmt.Sprintf("Failed to acquire resource: %v", err)
		if updateErr := r.Status().Update(ctx, bid); updateErr != nil {
			log.Error(updateErr, "Failed to update status")
		}
		if releaseErr := r.LeaseManager.ReleaseLease(ctx, leaseLease.Name); releaseErr != nil {
			log.Error(releaseErr, "Failed to release lease after AcquireResource error")
		}
		return ctrl.Result{RequeueAfter: requeueAfterError}, nil
	}
	if secretName != "" {
		resource.CredentialSecretName = secretName
	}

	// Get connection details from provider
	connDetails, err := prov.GetConnectionDetails(ctx, resource)
	if err != nil {
		log.Error(err, "Failed to get connection details")
		bid.Status.Phase = phaseFailed
		bid.Status.Message = fmt.Sprintf("Failed to get connection details: %v", err)
		if updateErr := r.Status().Update(ctx, bid); updateErr != nil {
			log.Error(updateErr, "Failed to update status")
		}
		// Release the lease since we failed
		if releaseErr := r.LeaseManager.ReleaseLease(ctx, leaseLease.Name); releaseErr != nil {
			log.Error(releaseErr, "Failed to release lease after GetConnectionDetails error")
		}
		return ctrl.Result{}, nil
	}

	// Update status with connection details
	now := metav1.Now()
	expiresAt := metav1.NewTime(now.Add(duration))

	bid.Status.Phase = "Bound"
	bid.Status.LeaseName = leaseLease.Name
	bid.Status.AcquiredAt = &now
	bid.Status.ExpiresAt = &expiresAt
	bid.Status.ConnectionDetails = connDetails
	bid.Status.CredentialSecretRef = &corev1.LocalObjectReference{
		Name: resource.CredentialSecretName,
	}
	bid.Status.Message = fmt.Sprintf("Successfully acquired %s (expires at %s)",
		leaseLease.Name,
		expiresAt.Format(time.RFC3339))

	// Update conditions
	setCondition(&bid.Status, metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionTrue,
		ObservedGeneration: bid.Generation,
		Reason:             "LeaseAcquired",
		Message:            "Resource is ready to use",
	})

	if err := r.Status().Update(ctx, bid); err != nil {
		log.Error(err, "Failed to update status")
		// Release the lease since we failed to update status
		if releaseErr := r.LeaseManager.ReleaseLease(ctx, leaseLease.Name); releaseErr != nil {
			log.Error(releaseErr, "Failed to release lease after status update error")
		}
		return ctrl.Result{}, err
	}

	log.Info("Successfully bound bid to resource",
		"lease", leaseLease.Name,
		"resource", resourceName,
		"type", bid.Spec.Type,
		"expiresAt", expiresAt.Format(time.RFC3339))

	return ctrl.Result{}, nil
}

// handleDeletion handles cleanup when ResourceBid is deleted
func (r *ResourceBidReconciler) handleDeletion(ctx context.Context, bid *poolv1alpha1.ResourceBid, prov provider.ResourceProvider) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	if !controllerutil.ContainsFinalizer(bid, finalizerName) {
		return ctrl.Result{}, nil
	}

	log.Info("Handling deletion of ResourceBid")

	// Update phase to Releasing
	if bid.Status.Phase != phaseReleasing {
		bid.Status.Phase = phaseReleasing
		bid.Status.Message = "Cleaning up resource"
		if err := r.Status().Update(ctx, bid); err != nil {
			log.Error(err, "Failed to update status to Releasing")
		}
	}

	// If no lease was acquired, just remove finalizer
	if bid.Status.LeaseName == "" {
		log.Info("No lease to release, removing finalizer")
		controllerutil.RemoveFinalizer(bid, finalizerName)
		return ctrl.Result{}, r.Update(ctx, bid)
	}

	// Get resource name from provider registry using lease
	resourceName := ""
	if bid.Status.LeaseName != "" {
		// Extract resource name from lease name
		// Lease name format: resource-lease-<providerType>-<resourceName>
		parts := len(lease.LeasePrefix) + len(bid.Spec.Type) + 1
		if len(bid.Status.LeaseName) > parts {
			resourceName = bid.Status.LeaseName[parts:]
		}
	}

	if resourceName == "" {
		log.Error(nil, "Could not determine resource name from lease", "leaseName", bid.Status.LeaseName)
	} else {
		resource := r.ProviderRegistry.GetResource(bid.Spec.Type, resourceName)
		if resource != nil {
			// Perform provider-specific cleanup
			if err := prov.ReleaseResource(ctx, r.Namespace, *resource, fmt.Sprintf("%s/%s", bid.Namespace, bid.Name)); err != nil {
				log.Error(err, "Failed to release resource", "resource", resourceName)
				// Continue anyway to release lease - don't block deletion
			}
		}
	}

	// Release the lease
	if err := r.LeaseManager.ReleaseLease(ctx, bid.Status.LeaseName); err != nil {
		log.Error(err, "Failed to release lease")
		return ctrl.Result{}, err
	}

	log.Info("Released lease", "lease", bid.Status.LeaseName)

	// Remove finalizer
	controllerutil.RemoveFinalizer(bid, finalizerName)
	if err := r.Update(ctx, bid); err != nil {
		return ctrl.Result{}, err
	}

	log.Info("Successfully cleaned up ResourceBid")
	return ctrl.Result{}, nil
}

// setCondition sets or updates a condition in the status
func setCondition(status *poolv1alpha1.ResourceBidStatus, newCondition metav1.Condition) {
	newCondition.LastTransitionTime = metav1.Now()

	for i, condition := range status.Conditions {
		if condition.Type == newCondition.Type {
			if condition.Status != newCondition.Status {
				status.Conditions[i] = newCondition
			}
			return
		}
	}

	status.Conditions = append(status.Conditions, newCondition)
}

// SetupWithManager sets up the controller with the Manager
func (r *ResourceBidReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&poolv1alpha1.ResourceBid{}).
		Named("resourcebid").
		Complete(r)
}
