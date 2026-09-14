package runapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	agentsv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func runnerStateFixture() (*agentsv1alpha1.AgentRun, *batchv1.Job, *corev1.Pod) {
	run := testAgentRun(agentsv1alpha1.AgentRunPhaseRunning)
	run.Status.JobRef = &agentsv1alpha1.NamespacedObjectReference{Name: "job", Namespace: run.Namespace}
	run.Status.JobUID = "job-uid"
	run.Status.RunnerPodRef = &agentsv1alpha1.NamespacedObjectReference{Name: "pod", Namespace: run.Namespace}
	run.Status.RunnerPodUID = "pod-uid"
	controller := true
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "job", Namespace: run.Namespace, UID: "job-uid", Labels: map[string]string{agentRunLabel: run.Name}, OwnerReferences: []metav1.OwnerReference{{APIVersion: agentsv1alpha1.GroupVersion.String(), Kind: "AgentRun", Name: run.Name, UID: run.UID, Controller: &controller}}}}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod", Namespace: run.Namespace, UID: "pod-uid", Labels: map[string]string{agentRunLabel: run.Name, agentRunJobLabel: job.Name}, OwnerReferences: []metav1.OwnerReference{{APIVersion: "batch/v1", Kind: "Job", Name: job.Name, UID: job.UID, Controller: &controller}}}, Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: agentContainer}}}}
	return run, job, pod
}

func TestVerifiedRunnerStateLabelsAndRecovery(t *testing.T) {
	for _, tc := range []struct{ reason, code string }{{"CreateContainerConfigError", "configuration_unavailable"}, {"CreateContainerError", "configuration_unavailable"}, {"ErrImagePull", "image_unavailable"}, {"ImagePullBackOff", "image_unavailable"}, {"InvalidImageName", "image_unavailable"}, {"ContainerCreating", ""}} {
		t.Run(tc.reason, func(t *testing.T) {
			run, job, pod := runnerStateFixture()
			pod.Status.ContainerStatuses = []corev1.ContainerStatus{{Name: agentContainer, State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: tc.reason, Message: "private-secret token=value"}}}}
			client := fake.NewClientset(job, pod)
			source := KubernetesLogSource{Client: client}
			state, err := source.RunnerState(context.Background(), run)
			if err != nil {
				t.Fatal(err)
			}
			if tc.code == "" {
				if state != nil {
					t.Fatal(state)
				}
			} else if state == nil || state.Code != tc.code {
				t.Fatalf("state %#v", state)
			}
			encoded, _ := json.Marshal(state)
			if strings.Contains(string(encoded), "private-secret") || strings.Contains(string(encoded), "token=") {
				t.Fatal(string(encoded))
			}
			pod.Status.ContainerStatuses[0].State = corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}
			if _, err = client.CoreV1().Pods(run.Namespace).UpdateStatus(context.Background(), pod, metav1.UpdateOptions{}); err != nil {
				t.Fatal(err)
			}
			state, err = source.RunnerState(context.Background(), run)
			if err != nil || state != nil {
				t.Fatalf("recovery: %#v %v", state, err)
			}
		})
	}
	run, job, pod := runnerStateFixture()
	pod.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodScheduled, Status: corev1.ConditionFalse, Reason: "Unschedulable", Message: "private-node-name"}}
	state, err := (KubernetesLogSource{Client: fake.NewClientset(job, pod)}).RunnerState(context.Background(), run)
	if err != nil || state == nil || state.Code != "capacity_wait" {
		t.Fatalf("capacity: %#v %v", state, err)
	}
}

func TestRunnerStateRequiresVerifiedOwnership(t *testing.T) {
	for _, poison := range []string{"pod-label", "job-label", "pod-owner", "job-owner", "pod-uid", "job-uid", "namespace", "container"} {
		t.Run(poison, func(t *testing.T) {
			run, job, pod := runnerStateFixture()
			pod.Status.ContainerStatuses = []corev1.ContainerStatus{{Name: agentContainer, State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CreateContainerConfigError"}}}}
			switch poison {
			case "pod-label":
				pod.Labels[agentRunLabel] = "other"
			case "job-label":
				job.Labels[agentRunLabel] = "other"
			case "pod-owner":
				pod.OwnerReferences[0].UID = "other"
			case "job-owner":
				job.OwnerReferences[0].UID = "other"
			case "pod-uid":
				run.Status.RunnerPodUID = "other"
			case "job-uid":
				run.Status.JobUID = "other"
			case "namespace":
				run.Status.RunnerPodRef.Namespace = "other"
			case "container":
				pod.Spec.Containers = nil
			}
			state, err := (KubernetesLogSource{Client: fake.NewClientset(job, pod)}).RunnerState(context.Background(), run)
			if err == nil || state != nil {
				t.Fatalf("unverified state %#v %v", state, err)
			}
		})
	}
}

type recoveringStateSource struct {
	staticLogSource
	reads atomic.Int32
}

func (s *recoveringStateSource) RunnerState(context.Context, *agentsv1alpha1.AgentRun) (*RunnerStateView, error) {
	switch s.reads.Add(1) {
	case 1:
		return &RunnerStateView{Code: "configuration_unavailable", Message: "Waiting for configuration"}, nil
	case 2:
		return nil, errors.New("transient private read error")
	default:
		return nil, nil
	}
}
func TestRunEventsClearsRecoveredPodWithoutRunVersionChange(t *testing.T) {
	run, _, _ := runnerStateFixture()
	source := &recoveringStateSource{staticLogSource: staticLogSource{err: ErrLogsPending}}
	server := testServer(t, run, staticAuthenticator{ready: true, principal: testPrincipal(time.Now().Add(time.Hour))}, source)
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/namespaces/agents/agent-runs/run-1/events", nil).WithContext(ctx)
	request.Header.Set("Authorization", "Bearer valid")
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	body := response.Body.String()
	if response.Code != 200 || !strings.Contains(body, `"runnerState":{"code":"configuration_unavailable"`) {
		t.Fatal(body)
	}
	if strings.Contains(body, "private read") {
		t.Fatal(body)
	}
	if strings.Count(body, "event: status") != 1 {
		t.Fatalf("expected one recovery, transient failure must retain blocker: %s", body)
	}
	parts := strings.Split(body, "event: status")
	if len(parts) != 2 || strings.Contains(parts[1], "runnerState") || !strings.Contains(parts[1], `"resourceVersion":"10"`) {
		t.Fatalf("recovery without CR change missing: %s", body)
	}
}
