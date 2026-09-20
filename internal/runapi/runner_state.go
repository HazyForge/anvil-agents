package runapi

import (
	"context"
	"time"

	agentsv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
)

// RunnerStateView exposes only canned, recoverable startup feedback. It never
// contains Kubernetes messages, resource names, or credential details.
type RunnerStateView struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type agentRunStateSource interface {
	RunnerState(context.Context, *agentsv1alpha1.AgentRun) (*RunnerStateView, error)
}

func (source KubernetesLogSource) RunnerState(ctx context.Context, run *agentsv1alpha1.AgentRun) (*RunnerStateView, error) {
	pod, err := source.verifiedRunnerPod(ctx, run)
	if err != nil {
		return nil, err
	}
	return runnerStateForPod(pod), nil
}

func runnerStateForPod(pod *corev1.Pod) *RunnerStateView {
	for _, status := range pod.Status.ContainerStatuses {
		if status.Name != agentContainer {
			continue
		}
		if status.State.Running != nil || status.State.Terminated != nil {
			return nil
		}
		if waiting := status.State.Waiting; waiting != nil {
			switch waiting.Reason {
			case "CreateContainerConfigError", "CreateContainerError":
				return &RunnerStateView{Code: "configuration_unavailable", Message: "Runner configuration is unavailable. Waiting for it to be corrected."}
			case "ErrImagePull", "ImagePullBackOff", "InvalidImageName":
				return &RunnerStateView{Code: "image_unavailable", Message: "The runner image is unavailable. Waiting for it to become available."}
			}
		}
	}
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodScheduled && condition.Status == corev1.ConditionFalse && condition.Reason == "Unschedulable" {
			return &RunnerStateView{Code: "capacity_wait", Message: "Waiting for a worker that can schedule this runner."}
		}
	}
	return nil
}

func (server *Server) readRunnerState(ctx context.Context, run *agentsv1alpha1.AgentRun) (*RunnerStateView, bool) {
	if run == nil || agentRunStreamComplete(run) || run.Status.RunnerPodRef == nil {
		return nil, true
	}
	source, ok := server.logs.(agentRunStateSource)
	if !ok {
		return nil, true
	}
	readCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	state, err := source.RunnerState(readCtx, run)
	return state, err == nil
}

func sameRunnerState(left, right *RunnerStateView) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}
