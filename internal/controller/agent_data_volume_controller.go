package controller

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	controlv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

const (
	agentDataVolumeReady       = "Ready"
	agentDataVolumeDefaultSize = "10Gi"
	agentDataVolumeLabel       = "control.anvil.hazyforge.io/agent-data-volume"
)

// +kubebuilder:rbac:groups="control.anvil.hazyforge.io",resources=agentdatavolumes,verbs=create;delete;get;list;patch;update;watch
// +kubebuilder:rbac:groups="control.anvil.hazyforge.io",resources=agentdatavolumes/status,verbs=get;patch;update
// +kubebuilder:rbac:groups="control.anvil.hazyforge.io",resources=agentdatavolumes/finalizers,verbs=update
// +kubebuilder:rbac:groups="control.anvil.hazyforge.io",resources=volumeprofiles,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=create;delete;get;list;patch;update;watch
type AgentDataVolumeReconciler struct {
	client.Client
	Scheme              *runtime.Scheme
	DefaultStorageClass string
}

func (r *AgentDataVolumeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	obj := &controlv1alpha1.AgentDataVolume{}
	if err := r.Get(ctx, req.NamespacedName, obj); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !obj.GetDeletionTimestamp().IsZero() {
		return ctrl.Result{}, nil
	}

	original := obj.DeepCopy()
	status := obj.Status
	status.ObservedGeneration = obj.Generation
	now := metav1.Now()

	effective, profileRef, profileVolumeName, profileBlockReason, profileBlockMessage, err := r.resolveAgentDataVolumeProfile(ctx, obj)
	if err != nil {
		return ctrl.Result{}, err
	}
	status.ProfileRef = profileRef
	status.ProfileVolumeName = profileVolumeName
	status.MountPath = agentDataVolumeMountPath(effective)
	status.SubPath = strings.TrimSpace(effective.Spec.SubPath)
	status.NodeSelector = cloneStringMap(effective.Spec.NodeSelector)
	status.ExtraEnv = append([]corev1.EnvVar(nil), effective.Spec.ExtraEnv...)
	status.ExternalSync = externalVolumeSyncStatus(effective.Spec.ExternalSync, status.ExternalSync, now)
	if profileBlockReason != "" {
		status.Phase = controlv1alpha1.AgentDataVolumePhaseBlocked
		status.LastError = profileBlockMessage
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               agentDataVolumeReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: obj.Generation,
			LastTransitionTime: now,
			Reason:             profileBlockReason,
			Message:            profileBlockMessage,
		})
		obj.Status = status
		return r.patchAgentDataVolumeStatus(ctx, original, obj)
	}

	if effective.Spec.Size.Sign() < 0 {
		status.Phase = controlv1alpha1.AgentDataVolumePhaseBlocked
		status.LastError = "spec.size cannot be negative."
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               agentDataVolumeReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: obj.Generation,
			LastTransitionTime: now,
			Reason:             "InvalidSize",
			Message:            status.LastError,
		})
		obj.Status = status
		return r.patchAgentDataVolumeStatus(ctx, original, obj)
	}
	desiredClaimName := agentDataVolumeClaimName(effective)
	if status.ClaimRef != nil && strings.TrimSpace(status.ClaimRef.Name) != "" && strings.TrimSpace(status.ClaimRef.Name) != desiredClaimName {
		status.Phase = controlv1alpha1.AgentDataVolumePhaseBlocked
		status.LastError = fmt.Sprintf("spec.claimName resolves to %q, but this AgentDataVolume already resolved immutable claim %q.", desiredClaimName, strings.TrimSpace(status.ClaimRef.Name))
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               agentDataVolumeReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: obj.Generation,
			LastTransitionTime: now,
			Reason:             "ImmutableClaimDrift",
			Message:            status.LastError,
		})
		obj.Status = status
		return r.patchAgentDataVolumeStatus(ctx, original, obj)
	}

	pvc, err := r.ensureAgentDataVolumePVC(ctx, obj, effective)
	if err != nil {
		return ctrl.Result{}, err
	}
	if owner := metav1.GetControllerOf(pvc); owner == nil || owner.UID != obj.UID || owner.APIVersion != controlv1alpha1.GroupVersion.String() || owner.Kind != "AgentDataVolume" {
		status.Phase = controlv1alpha1.AgentDataVolumePhaseBlocked
		status.LastError = fmt.Sprintf("PersistentVolumeClaim %s/%s is not controller-owned by this AgentDataVolume; automatic claim adoption is forbidden.", pvc.Namespace, pvc.Name)
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               agentDataVolumeReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: obj.Generation,
			LastTransitionTime: now,
			Reason:             "ForeignClaimCollision",
			Message:            status.LastError,
		})
		obj.Status = status
		return r.patchAgentDataVolumeStatus(ctx, original, obj)
	}
	expansionPending, driftReason, driftMessage, err := r.reconcileAgentDataVolumePVC(ctx, effective, pvc)
	if err != nil {
		return ctrl.Result{}, err
	}

	status.ClaimRef = &controlv1alpha1.NamespacedObjectReference{Name: pvc.Name, Namespace: pvc.Namespace}
	status.StorageClassName = ""
	if pvc.Spec.StorageClassName != nil {
		status.StorageClassName = strings.TrimSpace(*pvc.Spec.StorageClassName)
	}
	status.VolumeName = pvc.Spec.VolumeName
	status.Capacity = ""
	if capacity, ok := pvc.Status.Capacity[corev1.ResourceStorage]; ok {
		status.Capacity = capacity.String()
	}
	status.LastError = ""
	if driftMessage != "" {
		status.Phase = controlv1alpha1.AgentDataVolumePhaseBlocked
		status.LastError = driftMessage
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               agentDataVolumeReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: obj.Generation,
			LastTransitionTime: now,
			Reason:             driftReason,
			Message:            driftMessage,
		})
		obj.Status = status
		return r.patchAgentDataVolumeStatus(ctx, original, obj)
	}
	switch pvc.Status.Phase {
	case corev1.ClaimLost:
		status.Phase = controlv1alpha1.AgentDataVolumePhaseBlocked
		status.LastError = fmt.Sprintf("PersistentVolumeClaim %s/%s is Lost.", pvc.Namespace, pvc.Name)
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               agentDataVolumeReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: obj.Generation,
			LastTransitionTime: now,
			Reason:             "ClaimLost",
			Message:            status.LastError,
		})
	case corev1.ClaimBound:
		if expansionPending {
			status.Phase = controlv1alpha1.AgentDataVolumePhasePending
			desiredSize := agentDataVolumeSize(effective)
			message := fmt.Sprintf("PersistentVolumeClaim %s/%s is expanding to %s.", pvc.Namespace, pvc.Name, desiredSize.String())
			apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
				Type:               agentDataVolumeReady,
				Status:             metav1.ConditionFalse,
				ObservedGeneration: obj.Generation,
				LastTransitionTime: now,
				Reason:             "ExpansionPending",
				Message:            message,
			})
			break
		}
		status.Phase = controlv1alpha1.AgentDataVolumePhaseReady
		reason := "ClaimBound"
		message := fmt.Sprintf("PersistentVolumeClaim %s/%s is bound to %s.", pvc.Namespace, pvc.Name, pvc.Spec.VolumeName)
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               agentDataVolumeReady,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: obj.Generation,
			LastTransitionTime: now,
			Reason:             reason,
			Message:            message,
		})
	default:
		status.Phase = controlv1alpha1.AgentDataVolumePhasePending
		message := fmt.Sprintf("PersistentVolumeClaim %s/%s is %s and is not bound yet.", pvc.Namespace, pvc.Name, firstNonEmpty(string(pvc.Status.Phase), "Pending"))
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               agentDataVolumeReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: obj.Generation,
			LastTransitionTime: now,
			Reason:             "ClaimPending",
			Message:            message,
		})
	}
	obj.Status = status
	return r.patchAgentDataVolumeStatus(ctx, original, obj)
}

func (r *AgentDataVolumeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("agentdatavolume").
		For(&controlv1alpha1.AgentDataVolume{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Watches(&controlv1alpha1.VolumeProfile{}, handler.EnqueueRequestsFromMapFunc(r.agentDataVolumesForVolumeProfile)).
		Complete(r)
}

func (r *AgentDataVolumeReconciler) patchAgentDataVolumeStatus(ctx context.Context, original, obj *controlv1alpha1.AgentDataVolume) (ctrl.Result, error) {
	if err := r.Status().Patch(ctx, obj, client.MergeFrom(original)); err != nil {
		if apierrors.IsConflict(err) {
			return ctrl.Result{Requeue: true}, nil
		}
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *AgentDataVolumeReconciler) resolveAgentDataVolumeProfile(ctx context.Context, obj *controlv1alpha1.AgentDataVolume) (*controlv1alpha1.AgentDataVolume, *controlv1alpha1.NamespacedObjectReference, string, string, string, error) {
	effective := obj.DeepCopy()
	if obj == nil || obj.Spec.ProfileRef == nil {
		return effective, nil, "", "", "", nil
	}
	profileName := strings.TrimSpace(obj.Spec.ProfileRef.Name)
	if profileName == "" {
		return effective, nil, "", "InvalidProfileRef", "spec.profileRef.name must be set when profileRef is declared.", nil
	}
	profileRef := &controlv1alpha1.NamespacedObjectReference{Name: profileName, Namespace: obj.Namespace}
	profile := &controlv1alpha1.VolumeProfile{}
	if err := r.Get(ctx, client.ObjectKey{Name: profileName, Namespace: obj.Namespace}, profile); err != nil {
		if apierrors.IsNotFound(err) {
			return effective, profileRef, "", "ProfileNotFound", fmt.Sprintf("VolumeProfile %s/%s was not found.", obj.Namespace, profileName), nil
		}
		return nil, nil, "", "", "", err
	}
	if profile.Spec.ApplicationRef != nil && obj.Spec.ApplicationRef != nil {
		profileApplication := strings.TrimSpace(profile.Spec.ApplicationRef.Name)
		volumeApplication := strings.TrimSpace(obj.Spec.ApplicationRef.Name)
		if profileApplication != "" && volumeApplication != "" && profileApplication != volumeApplication {
			return effective, profileRef, "", "ProfileApplicationMismatch", fmt.Sprintf("VolumeProfile %s/%s is scoped to Application %q, but AgentDataVolume %s/%s is scoped to %q.", obj.Namespace, profileName, profileApplication, obj.Namespace, obj.Name, volumeApplication), nil
		}
	}
	profileVolume, volumeName, blockReason, blockMessage := resolveVolumeProfileVolume(profile, obj.Spec.ProfileVolumeName)
	if blockReason != "" {
		return effective, profileRef, volumeName, blockReason, blockMessage, nil
	}
	applyVolumeProfileDefaults(effective, profileVolume)
	return effective, profileRef, volumeName, "", "", nil
}

func resolveVolumeProfileVolume(profile *controlv1alpha1.VolumeProfile, selectedName string) (*controlv1alpha1.VolumeProfileVolumeSpec, string, string, string) {
	if profile == nil {
		return nil, "", "ProfileNotFound", "referenced VolumeProfile was not loaded."
	}
	name := strings.TrimSpace(selectedName)
	if name == "" {
		if len(profile.Spec.Volumes) == 1 {
			return &profile.Spec.Volumes[0], strings.TrimSpace(profile.Spec.Volumes[0].Name), "", ""
		}
		return nil, "", "ProfileVolumeRequired", fmt.Sprintf("VolumeProfile %s/%s has %d volume entries; spec.profileVolumeName must select one.", profile.Namespace, profile.Name, len(profile.Spec.Volumes))
	}
	for i := range profile.Spec.Volumes {
		if strings.TrimSpace(profile.Spec.Volumes[i].Name) == name {
			return &profile.Spec.Volumes[i], name, "", ""
		}
	}
	return nil, name, "ProfileVolumeNotFound", fmt.Sprintf("VolumeProfile %s/%s does not contain volume entry %q.", profile.Namespace, profile.Name, name)
}

func applyVolumeProfileDefaults(obj *controlv1alpha1.AgentDataVolume, profileVolume *controlv1alpha1.VolumeProfileVolumeSpec) {
	if obj == nil || profileVolume == nil {
		return
	}
	if strings.TrimSpace(obj.Spec.StorageClassName) == "" {
		obj.Spec.StorageClassName = strings.TrimSpace(profileVolume.StorageClassName)
	}
	if obj.Spec.Size.IsZero() && !profileVolume.Size.Request.IsZero() {
		obj.Spec.Size = profileVolume.Size.Request.DeepCopy()
	}
	if len(obj.Spec.AccessModes) == 0 && len(profileVolume.AccessModes) > 0 {
		obj.Spec.AccessModes = append([]corev1.PersistentVolumeAccessMode(nil), profileVolume.AccessModes...)
	}
	if strings.TrimSpace(obj.Spec.MountPath) == "" {
		obj.Spec.MountPath = strings.TrimSpace(profileVolume.MountPath)
	}
	if strings.TrimSpace(obj.Spec.SubPath) == "" {
		obj.Spec.SubPath = strings.TrimSpace(profileVolume.SubPath)
	}
	obj.Spec.NodeSelector = mergeStringMap(profileVolume.NodeSelector, obj.Spec.NodeSelector)
	obj.Spec.ExtraEnv = mergeEnvVars(profileVolume.ExtraEnv, obj.Spec.ExtraEnv)
	if obj.Spec.ExternalSync == nil && profileVolume.ExternalSync != nil {
		obj.Spec.ExternalSync = profileVolume.ExternalSync.DeepCopy()
	}
}

func (r *AgentDataVolumeReconciler) agentDataVolumesForVolumeProfile(ctx context.Context, obj client.Object) []reconcile.Request {
	if obj == nil {
		return nil
	}
	volumes := &controlv1alpha1.AgentDataVolumeList{}
	if err := r.List(ctx, volumes, client.InNamespace(obj.GetNamespace())); err != nil {
		return nil
	}
	requests := make([]reconcile.Request, 0, len(volumes.Items))
	for i := range volumes.Items {
		ref := volumes.Items[i].Spec.ProfileRef
		if ref == nil || strings.TrimSpace(ref.Name) != obj.GetName() {
			continue
		}
		requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKey{Namespace: volumes.Items[i].Namespace, Name: volumes.Items[i].Name}})
	}
	return requests
}

func (r *AgentDataVolumeReconciler) ensureAgentDataVolumePVC(ctx context.Context, owner, effective *controlv1alpha1.AgentDataVolume) (*corev1.PersistentVolumeClaim, error) {
	name := agentDataVolumeClaimName(effective)
	pvc := &corev1.PersistentVolumeClaim{}
	key := client.ObjectKey{Name: name, Namespace: owner.Namespace}
	if err := r.Get(ctx, key, pvc); err != nil {
		if client.IgnoreNotFound(err) != nil {
			return nil, err
		}
		storageClassName := r.agentDataVolumeStorageClassName(effective)
		var storageClassNameRef *string
		if storageClassName != "" {
			storageClassNameRef = &storageClassName
		}
		pvc = &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: owner.Namespace,
				Labels:    agentDataVolumeLabels(effective),
			},
			Spec: corev1.PersistentVolumeClaimSpec{
				AccessModes:      agentDataVolumeAccessModes(effective),
				StorageClassName: storageClassNameRef,
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: agentDataVolumeSize(effective),
					},
				},
			},
		}
		if err := controllerutil.SetControllerReference(owner, pvc, r.Scheme); err != nil {
			return nil, err
		}
		if err := r.Create(ctx, pvc); err != nil && !apierrors.IsAlreadyExists(err) {
			return nil, err
		}
		if err := r.Get(ctx, key, pvc); err != nil {
			return nil, err
		}
	}
	return pvc, nil
}

func (r *AgentDataVolumeReconciler) reconcileAgentDataVolumePVC(ctx context.Context, obj *controlv1alpha1.AgentDataVolume, pvc *corev1.PersistentVolumeClaim) (bool, string, string, error) {
	desiredStorageClass := r.agentDataVolumeStorageClassName(obj)
	actualStorageClass := ""
	if pvc.Spec.StorageClassName != nil {
		actualStorageClass = strings.TrimSpace(*pvc.Spec.StorageClassName)
	}
	if desiredStorageClass != "" && actualStorageClass != desiredStorageClass {
		return false, "ImmutableStorageClassDrift", fmt.Sprintf("spec.storageClassName resolves to %q, but PersistentVolumeClaim %s/%s uses immutable storageClassName %q.", desiredStorageClass, pvc.Namespace, pvc.Name, actualStorageClass), nil
	}
	desiredAccessModes := agentDataVolumeAccessModes(obj)
	if !agentDataVolumeAccessModesEqual(desiredAccessModes, pvc.Spec.AccessModes) {
		return false, "ImmutableAccessModesDrift", fmt.Sprintf("spec.accessModes %v do not match immutable PersistentVolumeClaim %s/%s accessModes %v.", desiredAccessModes, pvc.Namespace, pvc.Name, pvc.Spec.AccessModes), nil
	}

	currentRequest := pvc.Spec.Resources.Requests[corev1.ResourceStorage]
	if obj.Spec.Size.IsZero() {
		return false, "", "", nil
	}
	desired := obj.Spec.Size.DeepCopy()
	if currentRequest.Cmp(desired) > 0 {
		return false, "SizeReductionNotAllowed", fmt.Sprintf("spec.size %s is smaller than the existing PersistentVolumeClaim request %s; AgentDataVolume size may only increase.", desired.String(), currentRequest.String()), nil
	}
	if currentRequest.Cmp(desired) < 0 {
		original := pvc.DeepCopy()
		if pvc.Spec.Resources.Requests == nil {
			pvc.Spec.Resources.Requests = corev1.ResourceList{}
		}
		pvc.Spec.Resources.Requests[corev1.ResourceStorage] = desired
		if err := r.Patch(ctx, pvc, client.MergeFrom(original)); err != nil {
			return false, "", "", fmt.Errorf("expand PersistentVolumeClaim %s/%s to %s: %w", pvc.Namespace, pvc.Name, desired.String(), err)
		}
	}
	capacity := pvc.Status.Capacity[corev1.ResourceStorage]
	return capacity.Cmp(desired) < 0, "", "", nil
}

func agentDataVolumeAccessModesEqual(left, right []corev1.PersistentVolumeAccessMode) bool {
	if len(left) != len(right) {
		return false
	}
	counts := map[corev1.PersistentVolumeAccessMode]int{}
	for _, mode := range left {
		counts[mode]++
	}
	for _, mode := range right {
		counts[mode]--
		if counts[mode] < 0 {
			return false
		}
	}
	for _, count := range counts {
		if count != 0 {
			return false
		}
	}
	return true
}

func agentDataVolumeClaimName(obj *controlv1alpha1.AgentDataVolume) string {
	if obj == nil {
		return ""
	}
	if claimName := strings.TrimSpace(obj.Spec.ClaimName); claimName != "" {
		return claimName
	}
	return agentRunChildName("agent-data", obj.Name)
}

func (r *AgentDataVolumeReconciler) agentDataVolumeStorageClassName(obj *controlv1alpha1.AgentDataVolume) string {
	if obj != nil {
		if storageClassName := strings.TrimSpace(obj.Spec.StorageClassName); storageClassName != "" {
			return storageClassName
		}
	}
	return strings.TrimSpace(r.DefaultStorageClass)
}

func agentDataVolumeSize(obj *controlv1alpha1.AgentDataVolume) resource.Quantity {
	if obj != nil && !obj.Spec.Size.IsZero() {
		return obj.Spec.Size
	}
	return resource.MustParse(agentDataVolumeDefaultSize)
}

func agentDataVolumeAccessModes(obj *controlv1alpha1.AgentDataVolume) []corev1.PersistentVolumeAccessMode {
	if obj != nil && len(obj.Spec.AccessModes) > 0 {
		return append([]corev1.PersistentVolumeAccessMode(nil), obj.Spec.AccessModes...)
	}
	return []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}
}

func agentDataVolumeMountPath(obj *controlv1alpha1.AgentDataVolume) string {
	if obj != nil {
		if mountPath := strings.TrimSpace(obj.Spec.MountPath); mountPath != "" {
			return mountPath
		}
		switch obj.Spec.Backend {
		case controlv1alpha1.AgentRunHarnessBackendCodex:
			return "/codex-home"
		case controlv1alpha1.AgentRunHarnessBackendHermesAgent:
			return "/opt/anvil/hermes"
		case controlv1alpha1.AgentRunHarnessBackendOpenClaw:
			return "/opt/anvil/openclaw"
		case controlv1alpha1.AgentRunHarnessBackendGrokBuild:
			return "/opt/anvil/grok-build"
		case controlv1alpha1.AgentRunHarnessBackendPiAgent:
			return "/opt/anvil/pi"
		}
	}
	return "/agent-state"
}

func agentDataVolumeStatusCurrent(obj *controlv1alpha1.AgentDataVolume) bool {
	return obj != nil && obj.Status.ObservedGeneration != 0 && obj.Status.ObservedGeneration == obj.Generation
}

func agentDataVolumeResolvedMountPath(obj *controlv1alpha1.AgentDataVolume) string {
	if agentDataVolumeStatusCurrent(obj) {
		if mountPath := strings.TrimSpace(obj.Status.MountPath); mountPath != "" {
			return mountPath
		}
	}
	return agentDataVolumeMountPath(obj)
}

func agentDataVolumeResolvedSubPath(obj *controlv1alpha1.AgentDataVolume) string {
	if agentDataVolumeStatusCurrent(obj) {
		return strings.TrimSpace(obj.Status.SubPath)
	}
	if obj != nil {
		return strings.TrimSpace(obj.Spec.SubPath)
	}
	return ""
}

func agentDataVolumeResolvedNodeSelector(obj *controlv1alpha1.AgentDataVolume) map[string]string {
	if agentDataVolumeStatusCurrent(obj) && obj.Status.NodeSelector != nil {
		return cloneStringMap(obj.Status.NodeSelector)
	}
	if obj != nil {
		return cloneStringMap(obj.Spec.NodeSelector)
	}
	return nil
}

func agentDataVolumeResolvedExtraEnv(obj *controlv1alpha1.AgentDataVolume) []corev1.EnvVar {
	if agentDataVolumeStatusCurrent(obj) && obj.Status.ExtraEnv != nil {
		return append([]corev1.EnvVar(nil), obj.Status.ExtraEnv...)
	}
	if obj != nil {
		return append([]corev1.EnvVar(nil), obj.Spec.ExtraEnv...)
	}
	return nil
}

func agentDataVolumeLabels(obj *controlv1alpha1.AgentDataVolume) map[string]string {
	labels := map[string]string{
		agentDataVolumeLabel: sanitizeLabelValue(obj.Name),
	}
	if backend := strings.TrimSpace(string(obj.Spec.Backend)); backend != "" {
		labels["control.anvil.hazyforge.io/agent-data-backend"] = sanitizeLabelValue(backend)
	}
	if agentName := strings.TrimSpace(obj.Spec.AgentName); agentName != "" {
		labels["control.anvil.hazyforge.io/agent-data-agent"] = sanitizeLabelValue(agentName)
	}
	if obj.Spec.ProfileRef != nil {
		if profileName := strings.TrimSpace(obj.Spec.ProfileRef.Name); profileName != "" {
			labels["control.anvil.hazyforge.io/volume-profile"] = sanitizeLabelValue(profileName)
		}
	}
	if profileVolume := strings.TrimSpace(obj.Spec.ProfileVolumeName); profileVolume != "" {
		labels["control.anvil.hazyforge.io/volume-profile-entry"] = sanitizeLabelValue(profileVolume)
	}
	return labels
}
