package controller

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	controlv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

func TestValidateAgentDataVolumeCopySpec(t *testing.T) {
	obj := &controlv1alpha1.AgentDataVolumeCopy{
		ObjectMeta: metav1.ObjectMeta{Name: "copy-1", Namespace: "ns"},
		Spec: controlv1alpha1.AgentDataVolumeCopySpec{
			SourceRef: corev1.LocalObjectReference{Name: "src"},
			Destination: controlv1alpha1.AgentDataVolumeCopyDestination{
				Name:         "dst",
				NodeSelector: map[string]string{"kubernetes.io/hostname": "acer"},
			},
			Method: controlv1alpha1.AgentDataVolumeCopyMethodStream,
		},
	}
	if err := validateAgentDataVolumeCopySpec(obj); err != nil {
		t.Fatalf("expected valid spec, got %v", err)
	}
	obj.Spec.Destination.Name = "src"
	if err := validateAgentDataVolumeCopySpec(obj); err == nil {
		t.Fatal("expected error when source and destination match")
	}
}
