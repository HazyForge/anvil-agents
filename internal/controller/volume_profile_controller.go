package controller

import (
	"context"
	"fmt"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	controlv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

const (
	volumeProfileReady                   = "Ready"
	volumeProfileCapacityPolicySatisfied = "CapacityPolicySatisfied"
	externalVolumeSyncReady              = "Ready"
)

// +kubebuilder:rbac:groups="control.anvil.hazyforge.io",resources=volumeprofiles,verbs=get;list;watch
// +kubebuilder:rbac:groups="control.anvil.hazyforge.io",resources=volumeprofiles/status,verbs=get;patch;update
type VolumeProfileReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

func (r *VolumeProfileReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	obj := &controlv1alpha1.VolumeProfile{}
	if err := r.Get(ctx, req.NamespacedName, obj); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !obj.GetDeletionTimestamp().IsZero() {
		return ctrl.Result{}, nil
	}

	original := obj.DeepCopy()
	status := obj.Status
	status.ObservedGeneration = obj.Generation
	status.VolumeCount = int32(len(obj.Spec.Volumes))
	status.LastError = ""
	now := metav1.Now()

	existingSync := map[string]*controlv1alpha1.ExternalVolumeSyncStatus{}
	for i := range status.Volumes {
		if status.Volumes[i].ExternalSync != nil {
			existingSync[status.Volumes[i].Name] = status.Volumes[i].ExternalSync
		}
	}

	total := resource.Quantity{}
	seen := map[string]struct{}{}
	volumeStatuses := make([]controlv1alpha1.VolumeProfileVolumeStatus, 0, len(obj.Spec.Volumes))
	var blockReason, blockMessage string
	for _, volume := range obj.Spec.Volumes {
		name := strings.TrimSpace(volume.Name)
		if name == "" && blockReason == "" {
			blockReason = "InvalidVolumeName"
			blockMessage = "spec.volumes entries must set name."
		}
		if name != "" {
			if _, ok := seen[name]; ok && blockReason == "" {
				blockReason = "DuplicateVolumeName"
				blockMessage = fmt.Sprintf("spec.volumes contains duplicate entry %q.", name)
			}
			seen[name] = struct{}{}
		}
		requested := ""
		if !volume.Size.Request.IsZero() {
			if volume.Size.Request.Sign() < 0 && blockReason == "" {
				blockReason = "InvalidVolumeSize"
				blockMessage = fmt.Sprintf("spec.volumes[%q].size.request cannot be negative.", name)
			}
			total.Add(volume.Size.Request)
			requested = volume.Size.Request.String()
		}
		volumeStatuses = append(volumeStatuses, controlv1alpha1.VolumeProfileVolumeStatus{
			Name:             name,
			Purpose:          strings.TrimSpace(volume.Purpose),
			RequestedStorage: requested,
			ExternalSync:     externalVolumeSyncStatus(volume.ExternalSync, existingSync[name], now),
		})
	}
	status.Volumes = volumeStatuses
	status.TotalRequestedStorage = total.String()

	if len(obj.Spec.Volumes) == 0 && blockReason == "" {
		blockReason = "NoVolumes"
		blockMessage = "spec.volumes must contain at least one reusable volume entry."
	}

	if obj.Spec.CapacityPolicy != nil && obj.Spec.CapacityPolicy.MaxNodeAllocatableEphemeralStoragePercent != nil {
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               volumeProfileCapacityPolicySatisfied,
			Status:             metav1.ConditionUnknown,
			ObservedGeneration: obj.Generation,
			LastTransitionTime: now,
			Reason:             "NotEvaluated",
			Message:            "capacity percentage policy is recorded as intent; node capacity resolution is not implemented in this slice.",
		})
	} else {
		apimeta.RemoveStatusCondition(&status.Conditions, volumeProfileCapacityPolicySatisfied)
	}

	if blockReason != "" {
		status.Phase = controlv1alpha1.VolumeProfilePhaseBlocked
		status.LastError = blockMessage
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               volumeProfileReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: obj.Generation,
			LastTransitionTime: now,
			Reason:             blockReason,
			Message:            blockMessage,
		})
		obj.Status = status
		return r.patchVolumeProfileStatus(ctx, original, obj)
	}

	status.Phase = controlv1alpha1.VolumeProfilePhaseReady
	apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
		Type:               volumeProfileReady,
		Status:             metav1.ConditionTrue,
		ObservedGeneration: obj.Generation,
		LastTransitionTime: now,
		Reason:             "ProfileAccepted",
		Message:            fmt.Sprintf("VolumeProfile declares %d reusable volume entries.", len(obj.Spec.Volumes)),
	})
	obj.Status = status
	return r.patchVolumeProfileStatus(ctx, original, obj)
}

func (r *VolumeProfileReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("volumeprofile").
		For(&controlv1alpha1.VolumeProfile{}).
		Complete(r)
}

func (r *VolumeProfileReconciler) patchVolumeProfileStatus(ctx context.Context, original, obj *controlv1alpha1.VolumeProfile) (ctrl.Result, error) {
	if err := r.Status().Patch(ctx, obj, client.MergeFrom(original)); err != nil {
		if apierrors.IsConflict(err) {
			return ctrl.Result{Requeue: true}, nil
		}
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func externalVolumeSyncStatus(spec *controlv1alpha1.ExternalVolumeSyncSpec, existing *controlv1alpha1.ExternalVolumeSyncStatus, now metav1.Time) *controlv1alpha1.ExternalVolumeSyncStatus {
	if spec == nil {
		return nil
	}
	status := &controlv1alpha1.ExternalVolumeSyncStatus{
		Provider:  spec.Provider,
		Direction: spec.Direction,
	}
	if existing != nil {
		status.LastAttemptTime = existing.LastAttemptTime
		status.LastSuccessTime = existing.LastSuccessTime
		status.LastError = existing.LastError
		status.Conditions = append([]metav1.Condition(nil), existing.Conditions...)
	}
	if spec.Disabled {
		status.Phase = controlv1alpha1.ExternalVolumeSyncPhaseDisabled
		status.Message = "external volume sync is disabled for this volume."
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               externalVolumeSyncReady,
			Status:             metav1.ConditionTrue,
			LastTransitionTime: now,
			Reason:             "Disabled",
			Message:            status.Message,
		})
		return status
	}
	status.Phase = controlv1alpha1.ExternalVolumeSyncPhaseStubOnly
	status.Message = "external volume sync is declared but sync execution is not implemented yet."
	apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
		Type:               externalVolumeSyncReady,
		Status:             metav1.ConditionFalse,
		LastTransitionTime: now,
		Reason:             "StubOnly",
		Message:            status.Message,
	})
	return status
}
