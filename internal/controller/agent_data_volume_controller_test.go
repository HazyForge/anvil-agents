package controller

import (
	"context"
	"fmt"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	controlv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

type agentDataVolumeAlreadyExistsClient struct {
	client.Client
	firstPVCGet bool
}

func (c *agentDataVolumeAlreadyExistsClient) Get(ctx context.Context, key client.ObjectKey, object client.Object, options ...client.GetOption) error {
	if _, ok := object.(*corev1.PersistentVolumeClaim); ok && !c.firstPVCGet {
		c.firstPVCGet = true
		return apierrors.NewNotFound(schema.GroupResource{Resource: "persistentvolumeclaims"}, key.Name)
	}
	return c.Client.Get(ctx, key, object, options...)
}

func (c *agentDataVolumeAlreadyExistsClient) Create(_ context.Context, object client.Object, _ ...client.CreateOption) error {
	if _, ok := object.(*corev1.PersistentVolumeClaim); ok {
		return apierrors.NewAlreadyExists(schema.GroupResource{Resource: "persistentvolumeclaims"}, object.GetName())
	}
	return fmt.Errorf("unexpected create of %T", object)
}

func TestAgentDataVolumePendingPVCStaysPending(t *testing.T) {
	t.Parallel()

	volume, pvc, scheme := agentDataVolumeTestObjects(t)
	pvc.Status.Phase = corev1.ClaimPending
	reconciler, c := agentDataVolumeTestReconciler(t, scheme, volume, pvc)
	reconcileAgentDataVolumeForTest(t, reconciler, volume)
	updated := getAgentDataVolumeForTest(t, c, volume)
	if updated.Status.Phase != controlv1alpha1.AgentDataVolumePhasePending {
		t.Fatalf("phase = %q, want Pending", updated.Status.Phase)
	}
	if ready := findAgentDataVolumeCondition(updated.Status.Conditions, agentDataVolumeReady); ready == nil || ready.Status != metav1.ConditionFalse || ready.Reason != "ClaimPending" {
		t.Fatalf("ready condition = %#v, want False/ClaimPending", ready)
	}
}

func TestAgentDataVolumeExpandsPVCMonotonically(t *testing.T) {
	t.Parallel()

	volume, pvc, scheme := agentDataVolumeTestObjects(t)
	volume.Spec.Size = resource.MustParse("20Gi")
	pvc.Status.Phase = corev1.ClaimBound
	pvc.Status.Capacity = corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("10Gi")}
	reconciler, c := agentDataVolumeTestReconciler(t, scheme, volume, pvc)
	reconcileAgentDataVolumeForTest(t, reconciler, volume)

	updatedPVC := &corev1.PersistentVolumeClaim{}
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: pvc.Namespace, Name: pvc.Name}, updatedPVC); err != nil {
		t.Fatalf("get PVC: %v", err)
	}
	request := updatedPVC.Spec.Resources.Requests[corev1.ResourceStorage]
	if request.Cmp(resource.MustParse("20Gi")) != 0 {
		t.Fatalf("PVC request = %s, want 20Gi", request.String())
	}
	updated := getAgentDataVolumeForTest(t, c, volume)
	if updated.Status.Phase != controlv1alpha1.AgentDataVolumePhasePending {
		t.Fatalf("phase = %q, want Pending while capacity expands", updated.Status.Phase)
	}
}

func TestAgentDataVolumeBlocksSizeReduction(t *testing.T) {
	t.Parallel()

	volume, pvc, scheme := agentDataVolumeTestObjects(t)
	volume.Spec.Size = resource.MustParse("5Gi")
	pvc.Status.Phase = corev1.ClaimBound
	reconciler, c := agentDataVolumeTestReconciler(t, scheme, volume, pvc)
	reconcileAgentDataVolumeForTest(t, reconciler, volume)
	updated := getAgentDataVolumeForTest(t, c, volume)
	if updated.Status.Phase != controlv1alpha1.AgentDataVolumePhaseBlocked || !strings.Contains(updated.Status.LastError, "may only increase") {
		t.Fatalf("unexpected status: %#v", updated.Status)
	}
}

func TestAgentDataVolumeBlocksImmutablePVCDrift(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		mutate func(*controlv1alpha1.AgentDataVolume, *corev1.PersistentVolumeClaim)
		reason string
	}{
		{
			name: "storage class",
			mutate: func(_ *controlv1alpha1.AgentDataVolume, pvc *corev1.PersistentVolumeClaim) {
				value := "other-class"
				pvc.Spec.StorageClassName = &value
			},
			reason: "ImmutableStorageClassDrift",
		},
		{
			name: "access modes",
			mutate: func(_ *controlv1alpha1.AgentDataVolume, pvc *corev1.PersistentVolumeClaim) {
				pvc.Spec.AccessModes = []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany}
			},
			reason: "ImmutableAccessModesDrift",
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			volume, pvc, scheme := agentDataVolumeTestObjects(t)
			test.mutate(volume, pvc)
			reconciler, c := agentDataVolumeTestReconciler(t, scheme, volume, pvc)
			reconcileAgentDataVolumeForTest(t, reconciler, volume)
			updated := getAgentDataVolumeForTest(t, c, volume)
			condition := findAgentDataVolumeCondition(updated.Status.Conditions, agentDataVolumeReady)
			if updated.Status.Phase != controlv1alpha1.AgentDataVolumePhaseBlocked || condition == nil || condition.Reason != test.reason {
				t.Fatalf("status = %#v condition=%#v, want Blocked/%s", updated.Status, condition, test.reason)
			}
		})
	}
}

func TestAgentDataVolumeBlocksClaimNameChangeAfterResolution(t *testing.T) {
	t.Parallel()

	volume, _, scheme := agentDataVolumeTestObjects(t)
	volume.Spec.ClaimName = "new-claim"
	volume.Status = controlv1alpha1.AgentDataVolumeStatus{
		ObservedGeneration: 1,
		ClaimRef:           &controlv1alpha1.NamespacedObjectReference{Name: "agent-home", Namespace: volume.Namespace},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(volume).WithStatusSubresource(volume).Build()
	reconciler := &AgentDataVolumeReconciler{Client: c, Scheme: scheme}
	reconcileAgentDataVolumeForTest(t, reconciler, volume)
	updated := getAgentDataVolumeForTest(t, c, volume)
	if updated.Status.Phase != controlv1alpha1.AgentDataVolumePhaseBlocked || !strings.Contains(updated.Status.LastError, "immutable claim") {
		t.Fatalf("unexpected status: %#v", updated.Status)
	}
	claims := &corev1.PersistentVolumeClaimList{}
	if err := c.List(context.Background(), claims); err != nil {
		t.Fatalf("list PVCs: %v", err)
	}
	if len(claims.Items) != 0 {
		t.Fatalf("created replacement claims despite immutable claim drift: %#v", claims.Items)
	}
}

func TestAgentDataVolumeRejectsForeignClaimCollision(t *testing.T) {
	t.Parallel()

	volume, pvc, scheme := agentDataVolumeTestObjects(t)
	pvc.OwnerReferences = nil
	reconciler, c := agentDataVolumeTestReconciler(t, scheme, volume, pvc)
	reconcileAgentDataVolumeForTest(t, reconciler, volume)
	updated := getAgentDataVolumeForTest(t, c, volume)
	condition := findAgentDataVolumeCondition(updated.Status.Conditions, agentDataVolumeReady)
	if updated.Status.Phase != controlv1alpha1.AgentDataVolumePhaseBlocked || condition == nil || condition.Reason != "ForeignClaimCollision" {
		t.Fatalf("status = %#v condition=%#v, want Blocked/ForeignClaimCollision", updated.Status, condition)
	}
}

func TestAgentDataVolumeRecoversSelfOwnedClaimAfterStatusGap(t *testing.T) {
	t.Parallel()

	volume, pvc, scheme := agentDataVolumeTestObjects(t)
	volume.Status = controlv1alpha1.AgentDataVolumeStatus{}
	reconciler, c := agentDataVolumeTestReconciler(t, scheme, volume, pvc)
	reconcileAgentDataVolumeForTest(t, reconciler, volume)
	updated := getAgentDataVolumeForTest(t, c, volume)
	if updated.Status.Phase == controlv1alpha1.AgentDataVolumePhaseBlocked {
		t.Fatalf("self-owned claim was blocked after status gap: %#v", updated.Status)
	}
	if updated.Status.ClaimRef == nil || updated.Status.ClaimRef.Name != pvc.Name || updated.Status.ClaimUID != string(pvc.UID) {
		t.Fatalf("claim identity = %#v uid=%q, want %s/%s", updated.Status.ClaimRef, updated.Status.ClaimUID, pvc.Name, pvc.UID)
	}
}

func TestAgentDataVolumeRecoversCreateAlreadyExistsRace(t *testing.T) {
	t.Parallel()

	volume, pvc, scheme := agentDataVolumeTestObjects(t)
	volume.Status = controlv1alpha1.AgentDataVolumeStatus{}
	base := fake.NewClientBuilder().WithScheme(scheme).WithObjects(volume, pvc).WithStatusSubresource(&controlv1alpha1.AgentDataVolume{}, &controlv1alpha1.VolumeProfile{}, &corev1.PersistentVolumeClaim{}).Build()
	reconciler := &AgentDataVolumeReconciler{Client: &agentDataVolumeAlreadyExistsClient{Client: base}, Scheme: scheme}
	reconcileAgentDataVolumeForTest(t, reconciler, volume)
	updated := getAgentDataVolumeForTest(t, base, volume)
	if updated.Status.Phase == controlv1alpha1.AgentDataVolumePhaseBlocked {
		t.Fatalf("self-owned AlreadyExists claim was blocked: %#v", updated.Status)
	}
	if updated.Status.ClaimRef == nil || updated.Status.ClaimRef.Name != pvc.Name || updated.Status.ClaimUID != string(pvc.UID) {
		t.Fatalf("claim identity = %#v uid=%q, want %s/%s", updated.Status.ClaimRef, updated.Status.ClaimUID, pvc.Name, pvc.UID)
	}
}

func TestAgentDataVolumeRejectsReplacementClaimUID(t *testing.T) {
	t.Parallel()

	volume, pvc, scheme := agentDataVolumeTestObjects(t)
	volume.Status.ClaimUID = "original-pvc-uid"
	pvc.UID = "replacement-pvc-uid"
	reconciler, c := agentDataVolumeTestReconciler(t, scheme, volume, pvc)
	reconcileAgentDataVolumeForTest(t, reconciler, volume)
	updated := getAgentDataVolumeForTest(t, c, volume)
	condition := findAgentDataVolumeCondition(updated.Status.Conditions, agentDataVolumeReady)
	if updated.Status.Phase != controlv1alpha1.AgentDataVolumePhaseBlocked || condition == nil || condition.Reason != "ClaimIdentityChanged" {
		t.Fatalf("status = %#v condition=%#v, want Blocked/ClaimIdentityChanged", updated.Status, condition)
	}
}

func TestAgentDataVolumeRejectsBoundClaimBeforeUIDPersistence(t *testing.T) {
	t.Parallel()

	volume, pvc, scheme := agentDataVolumeTestObjects(t)
	volume.Status = controlv1alpha1.AgentDataVolumeStatus{}
	pvc.Spec.VolumeName = "prebound-volume"
	reconciler, c := agentDataVolumeTestReconciler(t, scheme, volume, pvc)
	reconcileAgentDataVolumeForTest(t, reconciler, volume)
	updated := getAgentDataVolumeForTest(t, c, volume)
	condition := findAgentDataVolumeCondition(updated.Status.Conditions, agentDataVolumeReady)
	if updated.Status.Phase != controlv1alpha1.AgentDataVolumePhaseBlocked || condition == nil || condition.Reason != "UnverifiedBoundClaim" {
		t.Fatalf("status = %#v condition=%#v, want Blocked/UnverifiedBoundClaim", updated.Status, condition)
	}
}

func TestAgentDataVolumeRejectsGeneralRuntimeEnvironment(t *testing.T) {
	t.Parallel()

	volume, _, scheme := agentDataVolumeTestObjects(t)
	volume.Status = controlv1alpha1.AgentDataVolumeStatus{}
	volume.Spec.MountPath = "/agent-home"
	volume.Spec.ExtraEnv = []controlv1alpha1.AgentDataVolumePathEnvVar{{Name: "PATH", Value: "/agent-home/bin"}}
	reconciler, c := agentDataVolumeTestReconcilerWithObjects(t, scheme, volume)
	reconcileAgentDataVolumeForTest(t, reconciler, volume)
	updated := getAgentDataVolumeForTest(t, c, volume)
	condition := findAgentDataVolumeCondition(updated.Status.Conditions, agentDataVolumeReady)
	if updated.Status.Phase != controlv1alpha1.AgentDataVolumePhaseBlocked || condition == nil || condition.Reason != "InvalidPathEnvironment" {
		t.Fatalf("status = %#v condition=%#v, want Blocked/InvalidPathEnvironment", updated.Status, condition)
	}
}

func TestAgentDataVolumeUsesClusterDefaultStorageClass(t *testing.T) {
	t.Parallel()

	volume, _, scheme := agentDataVolumeTestObjects(t)
	volume.Spec.StorageClassName = ""
	reconciler, c := agentDataVolumeTestReconcilerWithObjects(t, scheme, volume)
	reconcileAgentDataVolumeForTest(t, reconciler, volume)

	pvc := &corev1.PersistentVolumeClaim{}
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: volume.Namespace, Name: volume.Spec.ClaimName}, pvc); err != nil {
		t.Fatalf("get PVC: %v", err)
	}
	if pvc.Spec.StorageClassName != nil {
		t.Fatalf("storageClassName = %#v, want nil for cluster default", pvc.Spec.StorageClassName)
	}
}

func TestAgentDataVolumeAdoptsLegacyStorageClassWhenUnspecified(t *testing.T) {
	t.Parallel()

	volume, pvc, scheme := agentDataVolumeTestObjects(t)
	volume.Spec.StorageClassName = ""
	reconciler, c := agentDataVolumeTestReconciler(t, scheme, volume, pvc)
	reconcileAgentDataVolumeForTest(t, reconciler, volume)

	updated := getAgentDataVolumeForTest(t, c, volume)
	if updated.Status.Phase == controlv1alpha1.AgentDataVolumePhaseBlocked {
		t.Fatalf("legacy PVC was blocked: %#v", updated.Status)
	}
	if updated.Status.StorageClassName != "observability-local" {
		t.Fatalf("storageClassName = %q, want adopted legacy class", updated.Status.StorageClassName)
	}
}

func TestAgentDataVolumeUsesConfiguredDefaultStorageClass(t *testing.T) {
	t.Parallel()

	volume, _, scheme := agentDataVolumeTestObjects(t)
	volume.Spec.StorageClassName = ""
	reconciler, c := agentDataVolumeTestReconcilerWithObjects(t, scheme, volume)
	reconciler.DefaultStorageClass = "fast"
	reconcileAgentDataVolumeForTest(t, reconciler, volume)

	pvc := &corev1.PersistentVolumeClaim{}
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: volume.Namespace, Name: volume.Spec.ClaimName}, pvc); err != nil {
		t.Fatalf("get PVC: %v", err)
	}
	if pvc.Spec.StorageClassName == nil || *pvc.Spec.StorageClassName != "fast" {
		t.Fatalf("storageClassName = %#v, want fast", pvc.Spec.StorageClassName)
	}
}

func TestAgentDataVolumeAppliesVolumeProfileDefaults(t *testing.T) {
	t.Parallel()

	_, _, scheme := agentDataVolumeTestObjects(t)
	profile := agentDataVolumeTestProfile("rust-home", "home", resource.MustParse("20Gi"))
	volume := &controlv1alpha1.AgentDataVolume{
		ObjectMeta: metav1.ObjectMeta{Name: "home", Namespace: "agents", UID: types.UID("profiled-home-volume-uid"), Generation: 1},
		Spec: controlv1alpha1.AgentDataVolumeSpec{
			ClaimName:         "agent-home",
			ProfileRef:        &corev1.LocalObjectReference{Name: profile.Name},
			ProfileVolumeName: "home",
		},
	}
	reconciler, c := agentDataVolumeTestReconcilerWithObjects(t, scheme, volume, profile)
	reconcileAgentDataVolumeForTest(t, reconciler, volume)

	updatedPVC := &corev1.PersistentVolumeClaim{}
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: volume.Namespace, Name: "agent-home"}, updatedPVC); err != nil {
		t.Fatalf("get created PVC: %v", err)
	}
	if updatedPVC.Spec.StorageClassName == nil || *updatedPVC.Spec.StorageClassName != "observability-local" {
		t.Fatalf("storageClassName = %#v, want observability-local", updatedPVC.Spec.StorageClassName)
	}
	request := updatedPVC.Spec.Resources.Requests[corev1.ResourceStorage]
	if request.Cmp(resource.MustParse("20Gi")) != 0 {
		t.Fatalf("PVC request = %s, want 20Gi", request.String())
	}
	updated := getAgentDataVolumeForTest(t, c, volume)
	if updated.Status.MountPath != "/agent-home" || updated.Status.SubPath != "state" {
		t.Fatalf("resolved paths = mount %q subPath %q, want profile defaults", updated.Status.MountPath, updated.Status.SubPath)
	}
	if updated.Status.NodeSelector["hazyforge.io/storage"] != "observability-local" {
		t.Fatalf("node selector = %#v, want inherited storage selector", updated.Status.NodeSelector)
	}
	if len(updated.Status.ExtraEnv) != 1 || updated.Status.ExtraEnv[0].Name != "CODEX_HOME" {
		t.Fatalf("extra env = %#v, want inherited CODEX_HOME", updated.Status.ExtraEnv)
	}
	if updated.Status.ExternalSync == nil || updated.Status.ExternalSync.Phase != controlv1alpha1.ExternalVolumeSyncPhaseStubOnly {
		t.Fatalf("external sync = %#v, want StubOnly inherited sync", updated.Status.ExternalSync)
	}
}

func TestAgentDataVolumeRequiresProfileVolumeNameWhenAmbiguous(t *testing.T) {
	t.Parallel()

	_, _, scheme := agentDataVolumeTestObjects(t)
	profile := agentDataVolumeTestProfile("rust-home", "home", resource.MustParse("20Gi"))
	profile.Spec.Volumes = append(profile.Spec.Volumes, controlv1alpha1.VolumeProfileVolumeSpec{Name: "cache", Size: controlv1alpha1.VolumeProfileVolumeSizeSpec{Request: resource.MustParse("12Gi")}})
	volume := &controlv1alpha1.AgentDataVolume{
		ObjectMeta: metav1.ObjectMeta{Name: "home", Namespace: "agents", UID: types.UID("ambiguous-home-volume-uid"), Generation: 1},
		Spec: controlv1alpha1.AgentDataVolumeSpec{
			ClaimName:  "agent-home",
			ProfileRef: &corev1.LocalObjectReference{Name: profile.Name},
		},
	}
	reconciler, c := agentDataVolumeTestReconcilerWithObjects(t, scheme, volume, profile)
	reconcileAgentDataVolumeForTest(t, reconciler, volume)

	updated := getAgentDataVolumeForTest(t, c, volume)
	if updated.Status.Phase != controlv1alpha1.AgentDataVolumePhaseBlocked || !strings.Contains(updated.Status.LastError, "profileVolumeName") {
		t.Fatalf("unexpected status: %#v", updated.Status)
	}
	claims := &corev1.PersistentVolumeClaimList{}
	if err := c.List(context.Background(), claims); err != nil {
		t.Fatalf("list PVCs: %v", err)
	}
	if len(claims.Items) != 0 {
		t.Fatalf("created PVC despite ambiguous profile volume: %#v", claims.Items)
	}
}

func TestAgentDataVolumeProfileSizeReductionStillBlocked(t *testing.T) {
	t.Parallel()

	volume, pvc, scheme := agentDataVolumeTestObjects(t)
	profile := agentDataVolumeTestProfile("small-home", "home", resource.MustParse("5Gi"))
	volume.Spec.ProfileRef = &corev1.LocalObjectReference{Name: profile.Name}
	volume.Spec.ProfileVolumeName = "home"
	pvc.Status.Phase = corev1.ClaimBound
	reconciler, c := agentDataVolumeTestReconcilerWithObjects(t, scheme, volume, pvc, profile)
	reconcileAgentDataVolumeForTest(t, reconciler, volume)

	updated := getAgentDataVolumeForTest(t, c, volume)
	if updated.Status.Phase != controlv1alpha1.AgentDataVolumePhaseBlocked || !strings.Contains(updated.Status.LastError, "may only increase") {
		t.Fatalf("unexpected status: %#v", updated.Status)
	}
}

func agentDataVolumeTestObjects(t *testing.T) (*controlv1alpha1.AgentDataVolume, *corev1.PersistentVolumeClaim, *runtime.Scheme) {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := controlv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add control scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	storageClass := "observability-local"
	volume := &controlv1alpha1.AgentDataVolume{
		ObjectMeta: metav1.ObjectMeta{Name: "home", Namespace: "agents", UID: types.UID("home-volume-uid"), Generation: 1},
		Spec: controlv1alpha1.AgentDataVolumeSpec{
			ClaimName:        "agent-home",
			StorageClassName: storageClass,
			AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
		},
		Status: controlv1alpha1.AgentDataVolumeStatus{
			ClaimRef: &controlv1alpha1.NamespacedObjectReference{Name: "agent-home", Namespace: "agents"},
		},
	}
	controller := true
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: volume.Spec.ClaimName, Namespace: volume.Namespace, UID: types.UID("agent-home-pvc-uid"), OwnerReferences: []metav1.OwnerReference{{
			APIVersion: controlv1alpha1.GroupVersion.String(), Kind: "AgentDataVolume", Name: volume.Name, UID: volume.UID, Controller: &controller,
		}}},
		Spec: corev1.PersistentVolumeClaimSpec{
			StorageClassName: &storageClass,
			AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{
				corev1.ResourceStorage: resource.MustParse("10Gi"),
			}},
		},
	}
	return volume, pvc, scheme
}

func agentDataVolumeTestReconciler(t *testing.T, scheme *runtime.Scheme, volume *controlv1alpha1.AgentDataVolume, pvc *corev1.PersistentVolumeClaim) (*AgentDataVolumeReconciler, client.Client) {
	t.Helper()
	return agentDataVolumeTestReconcilerWithObjects(t, scheme, volume, pvc)
}

func agentDataVolumeTestReconcilerWithObjects(t *testing.T, scheme *runtime.Scheme, objects ...client.Object) (*AgentDataVolumeReconciler, client.Client) {
	t.Helper()
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).WithStatusSubresource(&controlv1alpha1.AgentDataVolume{}, &controlv1alpha1.VolumeProfile{}, &corev1.PersistentVolumeClaim{}).Build()
	return &AgentDataVolumeReconciler{Client: c, Scheme: scheme}, c
}

func agentDataVolumeTestProfile(name, volumeName string, size resource.Quantity) *controlv1alpha1.VolumeProfile {
	return &controlv1alpha1.VolumeProfile{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "agents", UID: types.UID(name + "-uid"), Generation: 1},
		Spec: controlv1alpha1.VolumeProfileSpec{
			Volumes: []controlv1alpha1.VolumeProfileVolumeSpec{{
				Name:             volumeName,
				Purpose:          "agent-home",
				MountPath:        "/agent-home",
				SubPath:          "state",
				StorageClassName: "observability-local",
				Size:             controlv1alpha1.VolumeProfileVolumeSizeSpec{Request: size},
				AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				NodeSelector:     map[string]string{"hazyforge.io/storage": "observability-local"},
				ExtraEnv:         []controlv1alpha1.AgentDataVolumePathEnvVar{{Name: "CODEX_HOME", Value: "/agent-home/codex"}},
				ExternalSync: &controlv1alpha1.ExternalVolumeSyncSpec{
					Provider:     controlv1alpha1.ExternalVolumeSyncProviderS3,
					Direction:    controlv1alpha1.ExternalVolumeSyncDirectionBidirectional,
					SeedOnCreate: true,
					SyncBack:     true,
					S3:           &controlv1alpha1.ExternalVolumeSyncS3Spec{Bucket: "agent-homes", Prefix: "home/"},
				},
			}},
		},
	}
}

func reconcileAgentDataVolumeForTest(t *testing.T, reconciler *AgentDataVolumeReconciler, volume *controlv1alpha1.AgentDataVolume) {
	t.Helper()
	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: volume.Namespace, Name: volume.Name}}); err != nil {
		t.Fatalf("reconcile AgentDataVolume: %v", err)
	}
}

func getAgentDataVolumeForTest(t *testing.T, c client.Client, volume *controlv1alpha1.AgentDataVolume) *controlv1alpha1.AgentDataVolume {
	t.Helper()
	updated := &controlv1alpha1.AgentDataVolume{}
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: volume.Namespace, Name: volume.Name}, updated); err != nil {
		t.Fatalf("get AgentDataVolume: %v", err)
	}
	return updated
}

func findAgentDataVolumeCondition(conditions []metav1.Condition, conditionType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return &conditions[i]
		}
	}
	return nil
}
