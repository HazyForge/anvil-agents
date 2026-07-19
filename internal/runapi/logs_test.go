package runapi

import (
	"context"
	"strings"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"

	agentsv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

func TestKubernetesLogSourceValidatesRunJobAndPodOwnership(t *testing.T) {
	isController := true
	run := testAgentRun(agentsv1alpha1.AgentRunPhaseRunning)
	run.Status.JobRef = &agentsv1alpha1.NamespacedObjectReference{Name: "run-job", Namespace: "agents"}
	run.Status.JobUID = "job-uid"
	run.Status.RunnerPodRef = &agentsv1alpha1.NamespacedObjectReference{Name: "run-pod", Namespace: "agents"}
	run.Status.RunnerPodUID = "pod-uid"
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{
		Name:      "run-job",
		Namespace: "agents",
		UID:       types.UID("job-uid"),
		Labels:    map[string]string{agentRunLabel: sanitizeLabelValue(run.Name)},
		OwnerReferences: []metav1.OwnerReference{{
			APIVersion: agentsv1alpha1.GroupVersion.String(),
			Kind:       "AgentRun",
			Name:       run.Name,
			UID:        run.UID,
			Controller: &isController,
		}},
	}}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name:      "run-pod",
		Namespace: "agents",
		UID:       types.UID("pod-uid"),
		Labels: map[string]string{
			agentRunLabel:    sanitizeLabelValue(run.Name),
			agentRunJobLabel: job.Name,
		},
		OwnerReferences: []metav1.OwnerReference{{
			APIVersion: "batch/v1",
			Kind:       "Job",
			Name:       job.Name,
			UID:        job.UID,
			Controller: &isController,
		}},
	}, Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: agentContainer}}}}
	source := KubernetesLogSource{Client: fake.NewClientset(job, pod)}
	if err := source.validateOwnership(context.Background(), run, pod); err != nil {
		t.Fatal(err)
	}

	tampered := pod.DeepCopy()
	tampered.Labels[agentRunLabel] = "different-run"
	if err := source.validateOwnership(context.Background(), run, tampered); err == nil || !strings.Contains(err.Error(), "not labeled") {
		t.Fatalf("expected tampered pod rejection, got %v", err)
	}

	replacedJob := run.DeepCopy()
	replacedJob.Status.JobUID = "different-job-uid"
	if err := source.validateOwnership(context.Background(), replacedJob, pod); err == nil || !strings.Contains(err.Error(), "recorded AgentRun Job UID") {
		t.Fatalf("expected replacement Job rejection, got %v", err)
	}
	replacedPod := run.DeepCopy()
	replacedPod.Status.RunnerPodUID = "different-pod-uid"
	if err := source.validateOwnership(context.Background(), replacedPod, pod); err == nil || !strings.Contains(err.Error(), "recorded UID") {
		t.Fatalf("expected replacement Pod rejection, got %v", err)
	}
}

func TestKubernetesLogSourceRejectsNonControllerAndWrongAPIVersionOwners(t *testing.T) {
	isController := true
	run := testAgentRun(agentsv1alpha1.AgentRunPhaseRunning)
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{
		Name: "run-job", UID: types.UID("job-uid"),
		OwnerReferences: []metav1.OwnerReference{{
			APIVersion: agentsv1alpha1.GroupVersion.String(), Kind: "AgentRun", Name: run.Name, UID: run.UID,
		}},
	}}
	if ownedByAgentRun(job, run) {
		t.Fatal("expected non-controller AgentRun owner reference to be rejected")
	}
	job.OwnerReferences[0].Controller = &isController
	job.OwnerReferences[0].APIVersion = "other.example/v1"
	if ownedByAgentRun(job, run) {
		t.Fatal("expected wrong AgentRun API version to be rejected")
	}

	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{OwnerReferences: []metav1.OwnerReference{{
		APIVersion: "batch/v1beta1", Kind: "Job", Name: job.Name, UID: job.UID, Controller: &isController,
	}}}}
	if ownedByJob(pod, job) {
		t.Fatal("expected wrong Job API version to be rejected")
	}
}

func TestPodHasRequiredAgentContainer(t *testing.T) {
	pod := &corev1.Pod{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "sidecar"}}}}
	if podHasContainer(pod, agentContainer) {
		t.Fatal("expected pod without agent container to be rejected")
	}
	pod.Spec.Containers = append(pod.Spec.Containers, corev1.Container{Name: agentContainer})
	if !podHasContainer(pod, agentContainer) {
		t.Fatal("expected pod with agent container to be accepted")
	}
}

func TestKubernetesLogSourceRejectsCrossNamespacePodReference(t *testing.T) {
	run := testAgentRun(agentsv1alpha1.AgentRunPhaseRunning)
	run.Status.RunnerPodRef = &agentsv1alpha1.NamespacedObjectReference{Name: "run-pod", Namespace: "other"}
	source := KubernetesLogSource{Client: fake.NewClientset()}
	if _, _, err := source.Open(context.Background(), run, corev1.PodLogOptions{}); err == nil || !strings.Contains(err.Error(), "crosses namespaces") {
		t.Fatalf("expected cross-namespace rejection, got %v", err)
	}
}
