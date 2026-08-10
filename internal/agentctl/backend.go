package agentctl

import (
	"context"
	"fmt"
	"io"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	agentsv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
	"github.com/hazyforge/anvil-agents/internal/runapi"
)

type KubeOptions struct {
	Kubeconfig string
	Context    string
}

type Backend interface {
	DefaultNamespace() string
	CreateRun(context.Context, *agentsv1alpha1.AgentRun) error
	ListRuns(context.Context, string, bool) (*agentsv1alpha1.AgentRunList, error)
	GetRun(context.Context, string, string) (*agentsv1alpha1.AgentRun, error)
	OpenLogs(context.Context, *agentsv1alpha1.AgentRun, corev1.PodLogOptions) (io.ReadCloser, error)
	GetJob(context.Context, string, string) (*batchv1.Job, error)
	GetPod(context.Context, string, string) (*corev1.Pod, error)
	ListEvents(context.Context, string, []types.UID) ([]corev1.Event, error)
	GetDataVolume(context.Context, string, string) (*agentsv1alpha1.AgentDataVolume, error)
	GetSecret(context.Context, string, string) (*corev1.Secret, error)
	CreateSecret(context.Context, *corev1.Secret) error
	DeleteSecret(context.Context, string, string) error
	CreateAuthSession(context.Context, *agentsv1alpha1.AgentAuthSession) error
	GetAuthSession(context.Context, string, string) (*agentsv1alpha1.AgentAuthSession, error)
	ListAuthSessions(context.Context, string) (*agentsv1alpha1.AgentAuthSessionList, error)
	CreateDataVolumeCopy(context.Context, *agentsv1alpha1.AgentDataVolumeCopy) error
	GetDataVolumeCopy(context.Context, string, string) (*agentsv1alpha1.AgentDataVolumeCopy, error)
	ListControls(context.Context) (*agentsv1alpha1.AgentRunControlList, error)
	GetControl(context.Context, string) (*agentsv1alpha1.AgentRunControl, error)
	CreateControl(context.Context, *agentsv1alpha1.AgentRunControl) error
	UpdateControl(context.Context, *agentsv1alpha1.AgentRunControl) error
	ListSchedules(context.Context, string, bool) (*agentsv1alpha1.AgentScheduleList, error)
	GetSchedule(context.Context, string, string) (*agentsv1alpha1.AgentSchedule, error)
	UpdateSchedule(context.Context, *agentsv1alpha1.AgentSchedule) error
}

type KubernetesBackend struct {
	runs             client.Client
	clientset        kubernetes.Interface
	defaultNamespace string
}

func NewKubernetesBackend(options KubeOptions) (Backend, error) {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if path := strings.TrimSpace(options.Kubeconfig); path != "" {
		loadingRules.ExplicitPath = path
	}
	overrides := &clientcmd.ConfigOverrides{CurrentContext: strings.TrimSpace(options.Context)}
	loader := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides)
	namespace, _, err := loader.Namespace()
	if err != nil {
		return nil, fmt.Errorf("resolve Kubernetes namespace: %w", err)
	}
	if strings.TrimSpace(namespace) == "" {
		namespace = "default"
	}
	restConfig, err := loader.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("load Kubernetes configuration: %w", err)
	}
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("add Kubernetes API scheme: %w", err)
	}
	if err := agentsv1alpha1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("add Anvil Agents API scheme: %w", err)
	}
	runClient, err := client.New(restConfig, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("create AgentRun client: %w", err)
	}
	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("create Kubernetes workload client: %w", err)
	}
	return &KubernetesBackend{runs: runClient, clientset: clientset, defaultNamespace: namespace}, nil
}

func (backend *KubernetesBackend) DefaultNamespace() string {
	return backend.defaultNamespace
}

func (backend *KubernetesBackend) CreateRun(ctx context.Context, run *agentsv1alpha1.AgentRun) error {
	if err := backend.runs.Create(ctx, run); err != nil {
		return fmt.Errorf("create AgentRun: %w", err)
	}
	return nil
}

func (backend *KubernetesBackend) ListRuns(ctx context.Context, namespace string, allNamespaces bool) (*agentsv1alpha1.AgentRunList, error) {
	list := &agentsv1alpha1.AgentRunList{}
	var options []client.ListOption
	if !allNamespaces {
		options = append(options, client.InNamespace(namespace))
	}
	if err := backend.runs.List(ctx, list, options...); err != nil {
		return nil, fmt.Errorf("list AgentRuns: %w", err)
	}
	return list, nil
}

func (backend *KubernetesBackend) GetRun(ctx context.Context, namespace, name string) (*agentsv1alpha1.AgentRun, error) {
	run := &agentsv1alpha1.AgentRun{}
	if err := backend.runs.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, run); err != nil {
		return nil, fmt.Errorf("get AgentRun %s/%s: %w", namespace, name, err)
	}
	return run, nil
}

func (backend *KubernetesBackend) OpenLogs(ctx context.Context, run *agentsv1alpha1.AgentRun, options corev1.PodLogOptions) (io.ReadCloser, error) {
	stream, _, err := (runapi.KubernetesLogSource{Client: backend.clientset}).Open(ctx, run, options)
	return stream, err
}

func (backend *KubernetesBackend) GetJob(ctx context.Context, namespace, name string) (*batchv1.Job, error) {
	job, err := backend.clientset.BatchV1().Jobs(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("get Job %s/%s: %w", namespace, name, err)
	}
	return job, nil
}

func (backend *KubernetesBackend) GetPod(ctx context.Context, namespace, name string) (*corev1.Pod, error) {
	pod, err := backend.clientset.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("get Pod %s/%s: %w", namespace, name, err)
	}
	return pod, nil
}

func (backend *KubernetesBackend) ListEvents(ctx context.Context, namespace string, uids []types.UID) ([]corev1.Event, error) {
	events := make([]corev1.Event, 0)
	seen := map[types.UID]struct{}{}
	for _, uid := range uids {
		if uid == "" {
			continue
		}
		selector := fields.OneTermEqualSelector("involvedObject.uid", string(uid)).String()
		list, err := backend.clientset.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{FieldSelector: selector})
		if err != nil {
			return nil, fmt.Errorf("list Events for UID %s: %w", uid, err)
		}
		for _, event := range list.Items {
			if _, ok := seen[event.UID]; ok && event.UID != "" {
				continue
			}
			seen[event.UID] = struct{}{}
			events = append(events, event)
		}
	}
	return events, nil
}

func (backend *KubernetesBackend) GetDataVolume(ctx context.Context, namespace, name string) (*agentsv1alpha1.AgentDataVolume, error) {
	volume := &agentsv1alpha1.AgentDataVolume{}
	if err := backend.runs.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, volume); err != nil {
		return nil, fmt.Errorf("get AgentDataVolume %s/%s: %w", namespace, name, err)
	}
	return volume, nil
}

func (backend *KubernetesBackend) GetSecret(ctx context.Context, namespace, name string) (*corev1.Secret, error) {
	secret, err := backend.clientset.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("get Secret %s/%s: %w", namespace, name, err)
	}
	return secret, nil
}

func (backend *KubernetesBackend) CreateSecret(ctx context.Context, secret *corev1.Secret) error {
	if _, err := backend.clientset.CoreV1().Secrets(secret.Namespace).Create(ctx, secret, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("create Secret %s/%s: %w", secret.Namespace, secret.Name, err)
	}
	return nil
}

func (backend *KubernetesBackend) DeleteSecret(ctx context.Context, namespace, name string) error {
	if err := backend.clientset.CoreV1().Secrets(namespace).Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
		return fmt.Errorf("delete Secret %s/%s: %w", namespace, name, err)
	}
	return nil
}

func (backend *KubernetesBackend) CreateAuthSession(ctx context.Context, session *agentsv1alpha1.AgentAuthSession) error {
	if err := backend.runs.Create(ctx, session); err != nil {
		return fmt.Errorf("create AgentAuthSession: %w", err)
	}
	return nil
}

func (backend *KubernetesBackend) GetAuthSession(ctx context.Context, namespace, name string) (*agentsv1alpha1.AgentAuthSession, error) {
	session := &agentsv1alpha1.AgentAuthSession{}
	if err := backend.runs.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, session); err != nil {
		return nil, fmt.Errorf("get AgentAuthSession %s/%s: %w", namespace, name, err)
	}
	return session, nil
}

func (backend *KubernetesBackend) ListAuthSessions(ctx context.Context, namespace string) (*agentsv1alpha1.AgentAuthSessionList, error) {
	list := &agentsv1alpha1.AgentAuthSessionList{}
	if err := backend.runs.List(ctx, list, client.InNamespace(namespace)); err != nil {
		return nil, fmt.Errorf("list AgentAuthSessions: %w", err)
	}
	return list, nil
}

func (backend *KubernetesBackend) CreateDataVolumeCopy(ctx context.Context, copyObj *agentsv1alpha1.AgentDataVolumeCopy) error {
	if err := backend.runs.Create(ctx, copyObj); err != nil {
		return fmt.Errorf("create AgentDataVolumeCopy: %w", err)
	}
	return nil
}

func (backend *KubernetesBackend) GetDataVolumeCopy(ctx context.Context, namespace, name string) (*agentsv1alpha1.AgentDataVolumeCopy, error) {
	copyObj := &agentsv1alpha1.AgentDataVolumeCopy{}
	if err := backend.runs.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, copyObj); err != nil {
		return nil, fmt.Errorf("get AgentDataVolumeCopy %s/%s: %w", namespace, name, err)
	}
	return copyObj, nil
}

func (backend *KubernetesBackend) ListControls(ctx context.Context) (*agentsv1alpha1.AgentRunControlList, error) {
	list := &agentsv1alpha1.AgentRunControlList{}
	if err := backend.runs.List(ctx, list); err != nil {
		return nil, fmt.Errorf("list AgentRunControls: %w", err)
	}
	return list, nil
}

func (backend *KubernetesBackend) GetControl(ctx context.Context, name string) (*agentsv1alpha1.AgentRunControl, error) {
	control := &agentsv1alpha1.AgentRunControl{}
	if err := backend.runs.Get(ctx, client.ObjectKey{Name: name}, control); err != nil {
		return nil, fmt.Errorf("get AgentRunControl %s: %w", name, err)
	}
	return control, nil
}

func (backend *KubernetesBackend) CreateControl(ctx context.Context, control *agentsv1alpha1.AgentRunControl) error {
	if err := backend.runs.Create(ctx, control); err != nil {
		return fmt.Errorf("create AgentRunControl %s: %w", control.Name, err)
	}
	return nil
}

func (backend *KubernetesBackend) UpdateControl(ctx context.Context, control *agentsv1alpha1.AgentRunControl) error {
	if err := backend.runs.Update(ctx, control); err != nil {
		return fmt.Errorf("update AgentRunControl %s: %w", control.Name, err)
	}
	return nil
}

func (backend *KubernetesBackend) ListSchedules(ctx context.Context, namespace string, allNamespaces bool) (*agentsv1alpha1.AgentScheduleList, error) {
	list := &agentsv1alpha1.AgentScheduleList{}
	var options []client.ListOption
	if !allNamespaces {
		options = append(options, client.InNamespace(namespace))
	}
	if err := backend.runs.List(ctx, list, options...); err != nil {
		return nil, fmt.Errorf("list AgentSchedules: %w", err)
	}
	return list, nil
}

func (backend *KubernetesBackend) GetSchedule(ctx context.Context, namespace, name string) (*agentsv1alpha1.AgentSchedule, error) {
	schedule := &agentsv1alpha1.AgentSchedule{}
	if err := backend.runs.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, schedule); err != nil {
		return nil, fmt.Errorf("get AgentSchedule %s/%s: %w", namespace, name, err)
	}
	return schedule, nil
}

func (backend *KubernetesBackend) UpdateSchedule(ctx context.Context, schedule *agentsv1alpha1.AgentSchedule) error {
	if err := backend.runs.Update(ctx, schedule); err != nil {
		return fmt.Errorf("update AgentSchedule %s/%s: %w", schedule.Namespace, schedule.Name, err)
	}
	return nil
}
