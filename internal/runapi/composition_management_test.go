package runapi

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestEvaluateCompositionManagementConsoleWritable(t *testing.T) {
	meta := &metav1.ObjectMeta{
		Name: "skills",
		Labels: map[string]string{
			LabelManagedBy: ManagedByConsole,
		},
	}
	got := evaluateCompositionManagement(meta)
	if !got.Writable || got.Reason != managementReasonConsoleManaged {
		t.Fatalf("got %#v, want console-managed writable", got)
	}
}

func TestEvaluateCompositionManagementGitOpsWins(t *testing.T) {
	meta := &metav1.ObjectMeta{
		Name: "skills",
		Labels: map[string]string{
			LabelManagedBy:                 ManagedByConsole,
			"argocd.argoproj.io/instance": "hazy-trade",
		},
	}
	got := evaluateCompositionManagement(meta)
	if got.Writable || got.Reason != managementReasonGitOpsProtected || got.ManagedBy != "argocd" {
		t.Fatalf("got %#v, want gitops_protected", got)
	}
}

func TestEvaluateCompositionManagementUnlabeledNotWritable(t *testing.T) {
	meta := &metav1.ObjectMeta{Name: "skills"}
	got := evaluateCompositionManagement(meta)
	if got.Writable || got.Reason != managementReasonNotConsoleManaged {
		t.Fatalf("got %#v, want not_console_managed", got)
	}
}

func TestStampConsoleManagedStripsGitOpsMarkers(t *testing.T) {
	meta := &metav1.ObjectMeta{
		Name: "skills",
		Labels: map[string]string{
			"argocd.argoproj.io/instance": "app",
			"team":                        "platform",
		},
		Annotations: map[string]string{
			"argocd.argoproj.io/tracking-id": "app:AgentSkillSet",
			"note":                          "keep",
		},
	}
	stampConsoleManaged(meta)
	if meta.Labels[LabelManagedBy] != ManagedByConsole {
		t.Fatalf("managed-by = %q", meta.Labels[LabelManagedBy])
	}
	if _, ok := meta.Labels["argocd.argoproj.io/instance"]; ok {
		t.Fatal("expected argo label stripped")
	}
	if meta.Labels["team"] != "platform" {
		t.Fatalf("expected non-gitops label retained, got %#v", meta.Labels)
	}
	if _, ok := meta.Annotations["argocd.argoproj.io/tracking-id"]; ok {
		t.Fatal("expected argo annotation stripped")
	}
	if meta.Annotations["note"] != "keep" {
		t.Fatalf("expected non-gitops annotation retained, got %#v", meta.Annotations)
	}
}
