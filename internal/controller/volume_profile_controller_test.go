package controller

import (
	"context"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	controlv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

func TestVolumeProfileReportsExternalSyncStubOnly(t *testing.T) {
	t.Parallel()

	profile, scheme := volumeProfileTestObject(t)
	reconciler, c := volumeProfileTestReconciler(t, scheme, profile)
	reconcileVolumeProfileForTest(t, reconciler, profile)

	updated := getVolumeProfileForTest(t, c, profile)
	if updated.Status.Phase != controlv1alpha1.VolumeProfilePhaseReady {
		t.Fatalf("phase = %q, want Ready", updated.Status.Phase)
	}
	if updated.Status.TotalRequestedStorage != "12Gi" {
		t.Fatalf("total requested = %q, want 12Gi", updated.Status.TotalRequestedStorage)
	}
	if len(updated.Status.Volumes) != 1 || updated.Status.Volumes[0].ExternalSync == nil {
		t.Fatalf("volume statuses = %#v, want external sync status", updated.Status.Volumes)
	}
	sync := updated.Status.Volumes[0].ExternalSync
	if sync.Phase != controlv1alpha1.ExternalVolumeSyncPhaseStubOnly || sync.Provider != controlv1alpha1.ExternalVolumeSyncProviderS3 {
		t.Fatalf("external sync = %#v, want StubOnly/s3", sync)
	}
	if ready := findVolumeProfileCondition(sync.Conditions, externalVolumeSyncReady); ready == nil || ready.Status != metav1.ConditionFalse || ready.Reason != "StubOnly" {
		t.Fatalf("sync ready condition = %#v, want False/StubOnly", ready)
	}
}

func TestVolumeProfileBlocksDuplicateNames(t *testing.T) {
	t.Parallel()

	profile, scheme := volumeProfileTestObject(t)
	profile.Spec.Volumes = append(profile.Spec.Volumes, profile.Spec.Volumes[0])
	reconciler, c := volumeProfileTestReconciler(t, scheme, profile)
	reconcileVolumeProfileForTest(t, reconciler, profile)

	updated := getVolumeProfileForTest(t, c, profile)
	if updated.Status.Phase != controlv1alpha1.VolumeProfilePhaseBlocked || !strings.Contains(updated.Status.LastError, "duplicate") {
		t.Fatalf("unexpected status: %#v", updated.Status)
	}
}

func volumeProfileTestObject(t *testing.T) (*controlv1alpha1.VolumeProfile, *runtime.Scheme) {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := controlv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add control scheme: %v", err)
	}
	profile := &controlv1alpha1.VolumeProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "rust-cache", Namespace: "agents", UID: types.UID("profile-uid"), Generation: 1},
		Spec: controlv1alpha1.VolumeProfileSpec{
			Volumes: []controlv1alpha1.VolumeProfileVolumeSpec{{
				Name:             "cargo",
				Purpose:          "cargo-cache",
				StorageClassName: "observability-local",
				Size:             controlv1alpha1.VolumeProfileVolumeSizeSpec{Request: resource.MustParse("12Gi")},
				ExternalSync: &controlv1alpha1.ExternalVolumeSyncSpec{
					Provider:     controlv1alpha1.ExternalVolumeSyncProviderS3,
					Direction:    controlv1alpha1.ExternalVolumeSyncDirectionSeedOnly,
					SeedOnCreate: true,
					S3:           &controlv1alpha1.ExternalVolumeSyncS3Spec{Bucket: "agent-cache", Prefix: "rust/cargo/"},
				},
			}},
		},
	}
	return profile, scheme
}

func volumeProfileTestReconciler(t *testing.T, scheme *runtime.Scheme, objects ...client.Object) (*VolumeProfileReconciler, client.Client) {
	t.Helper()
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).WithStatusSubresource(&controlv1alpha1.VolumeProfile{}).Build()
	return &VolumeProfileReconciler{Client: c, Scheme: scheme}, c
}

func reconcileVolumeProfileForTest(t *testing.T, reconciler *VolumeProfileReconciler, profile *controlv1alpha1.VolumeProfile) {
	t.Helper()
	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: profile.Namespace, Name: profile.Name}}); err != nil {
		t.Fatalf("reconcile VolumeProfile: %v", err)
	}
}

func getVolumeProfileForTest(t *testing.T, c client.Client, profile *controlv1alpha1.VolumeProfile) *controlv1alpha1.VolumeProfile {
	t.Helper()
	updated := &controlv1alpha1.VolumeProfile{}
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: profile.Namespace, Name: profile.Name}, updated); err != nil {
		t.Fatalf("get VolumeProfile: %v", err)
	}
	return updated
}

func findVolumeProfileCondition(conditions []metav1.Condition, conditionType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return &conditions[i]
		}
	}
	return nil
}
