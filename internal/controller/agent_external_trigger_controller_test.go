package controller

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	controlv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

func TestAgentExternalTriggerReconcilerAssignsReceiver(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := controlv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	obj := &controlv1alpha1.AgentExternalTrigger{
		ObjectMeta: metav1.ObjectMeta{Name: "gh", Namespace: "agents", Generation: 1},
		Spec: controlv1alpha1.AgentExternalTriggerSpec{
			Source: controlv1alpha1.AgentExternalTriggerSourceSpec{
				Kind: controlv1alpha1.AgentExternalTriggerSourceGitHubWebhook,
				GitHub: &controlv1alpha1.AgentExternalTriggerGitHubSpec{
					Repositories: []string{"HazyForge/anvil-agents"},
				},
			},
			SecretRef: controlv1alpha1.AgentExternalTriggerSecretRef{Name: "hook"},
			Targets: []controlv1alpha1.AgentExternalTriggerTargetSpec{{
				Kind: controlv1alpha1.AgentExternalTriggerTargetAgentRunProfile,
				Name: "operator",
			}},
		},
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(obj).WithObjects(obj).Build()
	reconciler := &AgentExternalTriggerReconciler{Client: kube, Scheme: scheme}
	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "agents", Name: "gh"}}); err != nil {
		t.Fatal(err)
	}
	fresh := &controlv1alpha1.AgentExternalTrigger{}
	if err := kube.Get(context.Background(), types.NamespacedName{Namespace: "agents", Name: "gh"}, fresh); err != nil {
		t.Fatal(err)
	}
	if fresh.Status.ReceiverID == "" || len(fresh.Status.ReceiverID) != 32 {
		t.Fatalf("receiverID = %q", fresh.Status.ReceiverID)
	}
	wantPath := "/api/v1/external-triggers/agents/gh/" + fresh.Status.ReceiverID
	if fresh.Status.WebhookPath != wantPath {
		t.Fatalf("webhookPath = %q want %q", fresh.Status.WebhookPath, wantPath)
	}
	if fresh.Status.Phase != controlv1alpha1.AgentExternalTriggerPhaseReady {
		t.Fatalf("phase = %s", fresh.Status.Phase)
	}

	fresh.Spec.Suspend = true
	if err := kube.Update(context.Background(), fresh); err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "agents", Name: "gh"}}); err != nil {
		t.Fatal(err)
	}
	if err := kube.Get(context.Background(), types.NamespacedName{Namespace: "agents", Name: "gh"}, fresh); err != nil {
		t.Fatal(err)
	}
	if fresh.Status.Phase != controlv1alpha1.AgentExternalTriggerPhaseSuspended {
		t.Fatalf("phase = %s", fresh.Status.Phase)
	}
	if !strings.Contains(fresh.Status.WebhookPath, fresh.Status.ReceiverID) {
		t.Fatalf("receiverID should remain stable across suspend")
	}
}

func TestValidateAgentExternalTriggerSpec(t *testing.T) {
	obj := &controlv1alpha1.AgentExternalTrigger{
		Spec: controlv1alpha1.AgentExternalTriggerSpec{
			Source: controlv1alpha1.AgentExternalTriggerSourceSpec{
				Kind: controlv1alpha1.AgentExternalTriggerSourceGitHubWebhook,
			},
		},
	}
	reason, _ := validateAgentExternalTriggerSpec(obj)
	if reason != "InvalidSource" {
		t.Fatalf("reason = %s", reason)
	}
}
