package runapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	agentsv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

const (
	agentRunLabel    = "control.anvil.hazyforge.io/agent-run"
	agentRunJobLabel = "control.anvil.hazyforge.io/agent-run-job"
	agentContainer   = "agent"
)

var ErrLogsPending = errors.New("runner pod is not available yet")

type AgentRunLogSource interface {
	Open(context.Context, *agentsv1alpha1.AgentRun, corev1.PodLogOptions) (io.ReadCloser, *corev1.Pod, error)
}

type KubernetesLogSource struct {
	Client kubernetes.Interface
}

func (source KubernetesLogSource) Open(ctx context.Context, run *agentsv1alpha1.AgentRun, options corev1.PodLogOptions) (io.ReadCloser, *corev1.Pod, error) {
	if source.Client == nil {
		return nil, nil, fmt.Errorf("Kubernetes client is not configured")
	}
	if run == nil || run.Status.RunnerPodRef == nil || strings.TrimSpace(run.Status.RunnerPodRef.Name) == "" {
		return nil, nil, ErrLogsPending
	}
	podNamespace := firstNonEmpty(run.Status.RunnerPodRef.Namespace, run.Namespace)
	if podNamespace != run.Namespace {
		return nil, nil, fmt.Errorf("runner pod reference crosses namespaces")
	}
	pod, err := source.Client.CoreV1().Pods(podNamespace).Get(ctx, run.Status.RunnerPodRef.Name, metav1.GetOptions{})
	if err != nil {
		return nil, nil, fmt.Errorf("get runner pod: %w", err)
	}
	if err := source.validateOwnership(ctx, run, pod); err != nil {
		return nil, nil, err
	}
	if !podHasContainer(pod, agentContainer) {
		return nil, nil, fmt.Errorf("runner pod does not contain required %q container", agentContainer)
	}
	options.Container = agentContainer
	stream, err := source.Client.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, &options).Stream(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("open runner logs: %w", err)
	}
	return stream, pod, nil
}

func (source KubernetesLogSource) validateOwnership(ctx context.Context, run *agentsv1alpha1.AgentRun, pod *corev1.Pod) error {
	if pod == nil || pod.Labels[agentRunLabel] != sanitizeLabelValue(run.Name) {
		return fmt.Errorf("runner pod is not labeled for AgentRun %s/%s", run.Namespace, run.Name)
	}
	if run.Status.JobRef == nil || strings.TrimSpace(run.Status.JobRef.Name) == "" {
		return fmt.Errorf("AgentRun has no controller-owned Job reference")
	}
	jobNamespace := firstNonEmpty(run.Status.JobRef.Namespace, run.Namespace)
	if jobNamespace != run.Namespace || pod.Labels[agentRunJobLabel] != run.Status.JobRef.Name {
		return fmt.Errorf("runner pod does not match the AgentRun Job")
	}
	job, err := source.Client.BatchV1().Jobs(jobNamespace).Get(ctx, run.Status.JobRef.Name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get AgentRun Job: %w", err)
	}
	if job.Labels[agentRunLabel] != sanitizeLabelValue(run.Name) || !ownedByAgentRun(job, run) {
		return fmt.Errorf("Job is not owned by AgentRun %s/%s", run.Namespace, run.Name)
	}
	if !ownedByJob(pod, job) {
		return fmt.Errorf("runner pod is not owned by the referenced Job")
	}
	return nil
}

func ownedByAgentRun(job *batchv1.Job, run *agentsv1alpha1.AgentRun) bool {
	owner := metav1.GetControllerOf(job)
	return owner != nil &&
		owner.APIVersion == agentsv1alpha1.GroupVersion.String() &&
		owner.Kind == "AgentRun" &&
		owner.Name == run.Name &&
		owner.UID == run.UID
}

func ownedByJob(pod *corev1.Pod, job *batchv1.Job) bool {
	owner := metav1.GetControllerOf(pod)
	return owner != nil &&
		owner.APIVersion == batchv1.SchemeGroupVersion.String() &&
		owner.Kind == "Job" &&
		owner.Name == job.Name &&
		owner.UID == job.UID
}

func podHasContainer(pod *corev1.Pod, name string) bool {
	for _, container := range pod.Spec.Containers {
		if container.Name == name {
			return true
		}
	}
	return false
}

func sanitizeLabelValue(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	var builder strings.Builder
	lastDash := false
	for _, character := range value {
		valid := (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9')
		if valid {
			builder.WriteRune(character)
			lastDash = false
		} else if !lastDash {
			builder.WriteByte('-')
			lastDash = true
		}
	}
	value = strings.Trim(builder.String(), "-")
	if len(value) > 63 {
		value = value[:63]
	}
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
