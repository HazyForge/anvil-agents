package controller

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	controlv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
	"github.com/hazyforge/anvil-agents/internal/archive"
)

const (
	agentRunPollInterval                            = 10 * time.Second
	agentRunExternalSecretRefreshPollInterval       = 2 * time.Second
	agentRunExternalSecretRefreshTimeout            = 2 * time.Minute
	agentRunReady                                   = "Ready"
	agentRunDefaultCodexImage                       = "ghcr.io/hazyforge/anvil-agent-run-codex:latest"
	agentRunDefaultHermesAgentImage                 = "ghcr.io/hazyforge/anvil-agent-run-hermes:latest"
	agentRunDefaultOpenClawImage                    = "ghcr.io/hazyforge/anvil-agent-run-openclaw:latest"
	agentRunDefaultGrokBuildImage                   = "ghcr.io/hazyforge/anvil-agent-run-grok-build:latest"
	agentRunDefaultPiAgentImage                     = "ghcr.io/hazyforge/anvil-agent-run-pi:latest"
	agentRunDefaultGitHubAPIBaseURL                 = "https://api.github.com"
	agentRunPodLogTailLines                   int64 = 10_000
	agentRunPodLogMaxBytes                    int64 = 4 * 1024 * 1024

	agentRunContainerName              = "agent"
	agentRunPayloadVolume              = "agent-run-payload"
	agentRunDataVolumePrefix           = "agent-data-"
	agentRunSpiffeWorkloadAPIVolume    = "spiffe-workload-api"
	agentRunSpiffeWorkloadAPIMountPath = "/spiffe-workload-api"
	agentRunSpiffeWorkloadAPISocket    = "/spiffe-workload-api/spire-agent.sock"
	agentRunSpiffeCSIDriver            = "csi.spiffe.io"
	agentRunPayloadMountPath           = "/var/run/anvil-agent-run"
	agentRunPromptFile                 = "prompt.md"
	agentRunContextFile                = "source.json"
	agentRunSkillFilePrefix            = "skill-"
	agentRunToolFilePrefix             = "tool-"
	agentRunStatusFile                 = "/tmp/anvil-agent-run-status/status.jsonl"
	agentRunStatusLinePrefix           = "ANVIL_AGENT_RUN_STATUS_JSON="
	agentRunPlatformRepository         = defaultPlatformRepository
	agentRunPlatformRepositoryURL      = defaultPlatformRepositoryURL
	agentRunLabel                      = "control.anvil.hazyforge.io/agent-run"
	agentRunJobLabel                   = "control.anvil.hazyforge.io/agent-run-job"
	agentRunLabelSourceKind            = "control.anvil.hazyforge.io/agent-run-source-kind"
	agentRunLabelSourceName            = "control.anvil.hazyforge.io/agent-run-source-name"
	agentRunLabelSpiffeWorkloadAPI     = "control.anvil.hazyforge.io/spiffe-workload-api"
	agentRunLabelServiceAccount        = "control.anvil.hazyforge.io/agent-run-service-account"
	agentRunAnnotationSourceUID        = "control.anvil.hazyforge.io/agent-run-source-uid"
	agentRunAnnotationSourceHash       = "control.anvil.hazyforge.io/agent-run-source-hash"
)

var agentRunSkillFileNameUnsafeChars = regexp.MustCompile(`[^A-Za-z0-9_.-]+`)

// +kubebuilder:rbac:groups="control.anvil.hazyforge.io",resources=agentruns,verbs=create;delete;get;list;patch;update;watch
// +kubebuilder:rbac:groups="control.anvil.hazyforge.io",resources=agentruns/status,verbs=get;patch;update
// +kubebuilder:rbac:groups="control.anvil.hazyforge.io",resources=agentruns/finalizers,verbs=update
// +kubebuilder:rbac:groups="control.anvil.hazyforge.io",resources=agentrunprofiles,verbs=get;list;watch
// +kubebuilder:rbac:groups="control.anvil.hazyforge.io",resources=adversesituations,verbs=get;list;watch
// +kubebuilder:rbac:groups="control.anvil.hazyforge.io",resources=agentdatavolumes,verbs=get;list;watch
// +kubebuilder:rbac:groups="batch",resources=jobs,verbs=create;delete;get;list;patch;update;watch
// +kubebuilder:rbac:groups="",resources=configmaps;pods,verbs=create;delete;get;list;patch;update;watch
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get
// +kubebuilder:rbac:groups="external-secrets.io",resources=externalsecrets,verbs=get;patch;update
// +kubebuilder:rbac:groups="",resources=pods/log,verbs=get
type AgentRunReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	CommonReconcilerOptions
	ReadPodLogs     func(ctx context.Context, namespace, pod string) (string, error)
	AgentRunArchive archive.AgentRunArchiveStore
}

func (r *AgentRunReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	obj := &controlv1alpha1.AgentRun{}
	if err := r.Get(ctx, req.NamespacedName, obj); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !obj.GetDeletionTimestamp().IsZero() {
		return ctrl.Result{}, nil
	}
	// AgentRuns are append-only execution records. A spec edit after terminal
	// completion must not clear status or create a second harness Job; callers
	// that want another execution create a new AgentRun instead.
	if agentRunPhaseTerminal(obj.Status.Phase) {
		return r.reconcileTerminalAgentRun(ctx, obj)
	}

	original := obj.DeepCopy()
	status := obj.Status
	if status.ObservedGeneration != 0 && status.ObservedGeneration != obj.Generation {
		// Spec changes bump generation and would otherwise wipe status. Preserve the
		// non-terminal harness Job identity so prompt/context edits cannot replace an
		// already-running execution (see status.jobRef single-execution contract).
		preservedJobRef := status.JobRef
		preservedStartedAt := status.StartedAt
		preservedPromptHash := status.PromptHash
		preservedPhase := status.Phase
		status = controlv1alpha1.AgentRunStatus{}
		if !agentRunPhaseTerminal(preservedPhase) && preservedJobRef != nil && strings.TrimSpace(preservedJobRef.Name) != "" {
			status.JobRef = preservedJobRef.DeepCopy()
			status.StartedAt = preservedStartedAt
			status.PromptHash = preservedPromptHash
			status.Phase = preservedPhase
		}
	}
	status.ObservedGeneration = obj.Generation

	// Recover the single-execution identity before resolving mutable profile or
	// spec inputs. The controller can crash after creating a Job but before
	// persisting status.jobRef; later profile deletion or drift must not
	// terminalize the record while that already-created execution runs unseen.
	now := metav1.Now()
	job, missingJobMessage, err := r.existingAgentRunJob(ctx, obj, status.JobRef)
	if err != nil {
		return ctrl.Result{}, err
	}
	if missingJobMessage != "" {
		status.Phase = controlv1alpha1.AgentRunPhaseFailed
		status.CompletedAt = &now
		status.Error = missingJobMessage
		status.Result = agentRunRawResult(status.Output, status.PullRequestURL, status.Decision, status.Reports)
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               agentRunReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: obj.Generation,
			LastTransitionTime: now,
			Reason:             "HarnessJobMissing",
			Message:            missingJobMessage,
		})
		obj.Status = status
		return r.patchAgentRunStatus(ctx, original, obj, false)
	}
	if job != nil && status.JobRef == nil {
		status.JobRef = &controlv1alpha1.NamespacedObjectReference{Name: job.Name, Namespace: job.Namespace}
	}

	effective := obj.DeepCopy()
	if job == nil {
		var phase controlv1alpha1.AgentRunPhase
		var reason, message string
		effective, phase, reason, message, err = r.resolveAgentRunProfile(ctx, obj)
		if err != nil {
			return ctrl.Result{}, err
		}
		status.Backend = string(agentRunBackendKind(effective))
		status.Intent = string(agentRunIntent(effective))
		status.Image = agentRunImage(effective)
		if phase != "" {
			status.Phase = phase
			if status.StartedAt == nil {
				status.StartedAt = &now
			}
			status.CompletedAt = &now
			status.Error = message
			apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
				Type:               agentRunReady,
				Status:             metav1.ConditionFalse,
				ObservedGeneration: obj.Generation,
				LastTransitionTime: now,
				Reason:             reason,
				Message:            message,
			})
			obj.Status = status
			return r.patchAgentRunStatus(ctx, original, obj, false)
		}

		if phase, reason, message := r.agentRunBlockingValidation(effective); phase != "" {
			status.Phase = phase
			if status.StartedAt == nil {
				status.StartedAt = &now
			}
			status.CompletedAt = &now
			status.Error = message
			apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
				Type:               agentRunReady,
				Status:             metav1.ConditionFalse,
				ObservedGeneration: obj.Generation,
				LastTransitionTime: now,
				Reason:             reason,
				Message:            message,
			})
			obj.Status = status
			return r.patchAgentRunStatus(ctx, original, obj, false)
		}
	} else {
		status.Backend = firstNonEmpty(status.Backend, agentRunJobEnvValue(job, "ANVIL_AGENT_RUN_BACKEND"))
		status.Intent = firstNonEmpty(status.Intent, agentRunJobEnvValue(job, "ANVIL_AGENT_RUN_INTENT"))
		status.Image = firstNonEmpty(agentRunJobContainerImage(job), status.Image)
	}

	if job == nil {
		paused, pauseReason, pauseMessage, pauseRequeueAfter, err := r.agentRunLaunchPaused(ctx, effective)
		if err != nil {
			return ctrl.Result{}, err
		}
		if paused {
			now := metav1.Now()
			status.Phase = controlv1alpha1.AgentRunPhasePending
			status.CompletedAt = nil
			status.Error = ""
			apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
				Type:               agentRunReady,
				Status:             metav1.ConditionFalse,
				ObservedGeneration: obj.Generation,
				LastTransitionTime: now,
				Reason:             pauseReason,
				Message:            pauseMessage,
			})
			obj.Status = status
			if err := r.Status().Patch(ctx, obj, client.MergeFrom(original)); err != nil {
				if apierrors.IsConflict(err) {
					return ctrl.Result{Requeue: true}, nil
				}
				return ctrl.Result{}, err
			}
			return ctrl.Result{RequeueAfter: pauseRequeueAfter}, nil
		}
		queuedBehind, queueReason, err := r.agentRunQueuedBehind(ctx, effective)
		if err != nil {
			return ctrl.Result{}, err
		}
		if queuedBehind != nil {
			now := metav1.Now()
			status.Phase = controlv1alpha1.AgentRunPhasePending
			status.CompletedAt = nil
			status.Error = ""
			message := fmt.Sprintf("Waiting behind scheduled AgentRun %s/%s.", queuedBehind.Namespace, queuedBehind.Name)
			if queueReason == "QueuedBehindApplicationRun" {
				message = fmt.Sprintf("Waiting behind application AgentRun %s/%s.", queuedBehind.Namespace, queuedBehind.Name)
			}
			apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
				Type:               agentRunReady,
				Status:             metav1.ConditionFalse,
				ObservedGeneration: obj.Generation,
				LastTransitionTime: now,
				Reason:             queueReason,
				Message:            message,
			})
			obj.Status = status
			return r.patchAgentRunStatus(ctx, original, obj, true)
		}
	}

	var dataVolumes []resolvedAgentRunDataVolume
	if job == nil {
		var phase controlv1alpha1.AgentRunPhase
		var reason, message string
		dataVolumes, phase, reason, message, err = r.resolveAgentRunDataVolumes(ctx, effective)
		if err != nil {
			return ctrl.Result{}, err
		}
		status.DataVolumes = agentRunDataVolumeStatuses(dataVolumes)
		if phase != "" {
			status.Phase = phase
			if phase == controlv1alpha1.AgentRunPhasePending {
				status.CompletedAt = nil
				status.Error = ""
			} else {
				if status.StartedAt == nil {
					status.StartedAt = &now
				}
				status.CompletedAt = &now
				status.Error = message
			}
			apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
				Type:               agentRunReady,
				Status:             metav1.ConditionFalse,
				ObservedGeneration: obj.Generation,
				LastTransitionTime: now,
				Reason:             reason,
				Message:            message,
			})
			obj.Status = status
			return r.patchAgentRunStatus(ctx, original, obj, phase == controlv1alpha1.AgentRunPhasePending)
		}
	}

	if job == nil {
		fresh, refreshPhase, refreshReason, refreshMessage, err := r.ensureAgentRunExternalSecretFreshness(ctx, effective, &status)
		if err != nil {
			return ctrl.Result{}, err
		}
		if refreshPhase != "" {
			status.Phase = refreshPhase
			if refreshPhase == controlv1alpha1.AgentRunPhasePending {
				status.CompletedAt = nil
			} else {
				status.CompletedAt = &now
			}
			status.Error = refreshMessage
			apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
				Type:               agentRunReady,
				Status:             metav1.ConditionFalse,
				ObservedGeneration: obj.Generation,
				LastTransitionTime: now,
				Reason:             refreshReason,
				Message:            refreshMessage,
			})
			obj.Status = status
			if refreshPhase == controlv1alpha1.AgentRunPhasePending {
				if err := r.Status().Patch(ctx, obj, client.MergeFrom(original)); err != nil {
					if apierrors.IsConflict(err) {
						return ctrl.Result{Requeue: true}, nil
					}
					return ctrl.Result{}, err
				}
				return ctrl.Result{RequeueAfter: agentRunExternalSecretRefreshPollInterval}, nil
			}
			return r.patchAgentRunStatus(ctx, original, obj, false)
		}
		if !fresh {
			return ctrl.Result{RequeueAfter: agentRunExternalSecretRefreshPollInterval}, nil
		}
		paused, pauseReason, pauseMessage, pauseRequeueAfter, err := r.agentRunLaunchPausedAuthoritative(ctx, effective)
		if err != nil {
			return ctrl.Result{}, err
		}
		if paused {
			return r.patchAgentRunLaunchPausedStatus(ctx, original, obj, &status, pauseReason, pauseMessage, pauseRequeueAfter)
		}
		prompt := buildAgentRunPrompt(effective)
		promptHash := shortHash(prompt)
		if _, err := r.ensureAgentRunConfigMap(ctx, effective, prompt, promptHash); err != nil {
			return ctrl.Result{}, err
		}
		job, err = r.ensureAgentRunJob(ctx, effective, promptHash, dataVolumes)
		if err != nil {
			return ctrl.Result{}, err
		}
		status.PromptHash = promptHash
		status.JobRef = &controlv1alpha1.NamespacedObjectReference{Name: job.Name, Namespace: job.Namespace}
	}

	if status.StartedAt == nil {
		status.StartedAt = &now
	}
	status.Image = firstNonEmpty(agentRunJobContainerImage(job), agentRunImage(effective))

	runnerRef, runnerPod, err := r.findAgentRunRunnerPod(ctx, obj.Namespace, job.Name)
	if err != nil {
		return ctrl.Result{}, err
	}
	status.RunnerPodRef = runnerRef
	if runnerPod != nil {
		status.RunnerNode = runnerPod.Spec.NodeName
		if logs, err := r.readAgentRunRunnerLogs(ctx, runnerPod.Namespace, runnerPod.Name); err == nil {
			agentRunApplyStatusReports(&status, agentRunStatusReportsFromOutput(logs))
			status.Output = agentRunTrimOutput(logs)
			status.Result = agentRunRawResult(status.Output, status.PullRequestURL, status.Decision, status.Reports)
		}
	}

	if message := agentRunPodLaunchFailureMessage(runnerPod); message != "" {
		if err := r.deleteAgentRunJobAfterLaunchFailure(ctx, job); err != nil {
			return ctrl.Result{}, err
		}
		status.Phase = controlv1alpha1.AgentRunPhaseFailed
		status.CompletedAt = &now
		status.Error = message
		status.Result = agentRunRawResult(status.Output, status.PullRequestURL, status.Decision, status.Reports)
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               agentRunReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: obj.Generation,
			LastTransitionTime: now,
			Reason:             "HarnessLaunchFailed",
			Message:            message,
		})
		obj.Status = status
		return r.patchAgentRunStatus(ctx, original, obj, false)
	}

	switch {
	case agentRunJobComplete(job):
		status.CompletedAt = &now
		status.Error = ""
		if status.Decision == nil {
			status.Decision = &controlv1alpha1.AgentRunDecisionStatus{
				Classification: "completed",
				Action:         firstNonEmpty(strings.TrimSpace(status.Intent), string(agentRunIntent(effective))),
				Summary:        agentRunOutputSummary(status.Output),
			}
		} else if strings.TrimSpace(status.Decision.Summary) == "" {
			status.Decision.Summary = agentRunOutputSummary(status.Output)
		}
		status.Result = agentRunRawResult(status.Output, status.PullRequestURL, status.Decision, status.Reports)
		if agentRunReportsNeedHuman(status.Reports) {
			status.Phase = controlv1alpha1.AgentRunPhaseNeedsHuman
			apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
				Type:               agentRunReady,
				Status:             metav1.ConditionFalse,
				ObservedGeneration: obj.Generation,
				LastTransitionTime: now,
				Reason:             "HarnessNeedsHuman",
				Message:            firstNonEmpty(status.Decision.Summary, agentRunLatestHumanFollowUp(status.Reports), "Agent run reported that human follow-up is required."),
			})
			obj.Status = status
			return r.patchAgentRunStatus(ctx, original, obj, false)
		}
		status.Phase = controlv1alpha1.AgentRunPhaseSucceeded
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               agentRunReady,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: obj.Generation,
			LastTransitionTime: now,
			Reason:             "HarnessSucceeded",
			Message:            firstNonEmpty(status.Decision.Summary, "Agent run harness completed successfully."),
		})
		obj.Status = status
		return r.patchAgentRunStatus(ctx, original, obj, false)
	case agentRunJobFailed(job):
		status.Phase = controlv1alpha1.AgentRunPhaseFailed
		status.CompletedAt = &now
		status.Error = firstNonEmpty(jobFailureMessage(job), podTerminationMessage(runnerPod), "Agent run harness failed.")
		status.Result = agentRunRawResult(status.Output, status.PullRequestURL, status.Decision, status.Reports)
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               agentRunReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: obj.Generation,
			LastTransitionTime: now,
			Reason:             "HarnessFailed",
			Message:            status.Error,
		})
		obj.Status = status
		return r.patchAgentRunStatus(ctx, original, obj, false)
	default:
		status.Phase = controlv1alpha1.AgentRunPhaseRunning
		status.CompletedAt = nil
		status.Error = ""
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               agentRunReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: obj.Generation,
			LastTransitionTime: now,
			Reason:             "HarnessRunning",
			Message:            fmt.Sprintf("Agent run job %s/%s is running or pending.", job.Namespace, job.Name),
		})
		obj.Status = status
		return r.patchAgentRunStatus(ctx, original, obj, true)
	}
}

func (r *AgentRunReconciler) ensureAgentRunConfigMap(ctx context.Context, obj *controlv1alpha1.AgentRun, prompt, promptHash string) (*corev1.ConfigMap, error) {
	contextBody, err := r.agentRunContextJSON(ctx, obj)
	if err != nil {
		return nil, err
	}
	name := agentRunChildName(obj.Name, "context", promptHash)
	configMap := &corev1.ConfigMap{}
	key := client.ObjectKey{Name: name, Namespace: obj.Namespace}
	if err := r.Get(ctx, key, configMap); err != nil {
		if client.IgnoreNotFound(err) != nil {
			return nil, err
		}
		data, err := r.agentRunConfigMapData(ctx, obj, prompt, string(contextBody))
		if err != nil {
			return nil, err
		}
		configMap = &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: obj.Namespace,
				Labels:    agentRunLabels(obj, ""),
			},
			Data: data,
		}
		if err := controllerutil.SetControllerReference(obj, configMap, r.Scheme); err != nil {
			return nil, err
		}
		if err := r.Create(ctx, configMap); err != nil {
			return nil, err
		}
	}
	return configMap, nil
}

func agentRunConfigMapData(obj *controlv1alpha1.AgentRun, prompt, contextBody string) map[string]string {
	data := map[string]string{
		agentRunPromptFile:  prompt,
		agentRunContextFile: contextBody,
	}
	for index, skill := range obj.Spec.Harness.SkillInjections {
		data[agentRunSkillFileName(index, skill)] = agentRunSkillFileContent(skill)
	}
	for index, tool := range obj.Spec.Harness.Tools {
		if strings.TrimSpace(tool.SetupScript) == "" {
			continue
		}
		data[agentRunToolSetupFileName(index, tool)] = agentRunToolSetupFileContent(tool)
	}
	return data
}

func (r *AgentRunReconciler) agentRunConfigMapData(ctx context.Context, obj *controlv1alpha1.AgentRun, prompt, contextBody string) (map[string]string, error) {
	data := map[string]string{
		agentRunPromptFile:  prompt,
		agentRunContextFile: contextBody,
	}
	for index, skill := range obj.Spec.Harness.SkillInjections {
		content, err := r.agentRunSkillFileContent(ctx, obj, skill)
		if err != nil {
			return nil, err
		}
		data[agentRunSkillFileName(index, skill)] = content
	}
	for index, tool := range obj.Spec.Harness.Tools {
		if strings.TrimSpace(tool.SetupScript) == "" {
			continue
		}
		data[agentRunToolSetupFileName(index, tool)] = agentRunToolSetupFileContent(tool)
	}
	return data, nil
}

func (r *AgentRunReconciler) ensureAgentRunJob(ctx context.Context, obj *controlv1alpha1.AgentRun, promptHash string, dataVolumes []resolvedAgentRunDataVolume) (*batchv1.Job, error) {
	jobName := agentRunChildName(obj.Name, "harness", promptHash)
	job := &batchv1.Job{}
	key := client.ObjectKey{Name: jobName, Namespace: obj.Namespace}
	if err := r.Get(ctx, key, job); err != nil {
		if client.IgnoreNotFound(err) != nil {
			return nil, err
		}
		job = r.agentRunJob(obj, jobName, agentRunChildName(obj.Name, "context", promptHash), dataVolumes)
		if err := controllerutil.SetControllerReference(obj, job, r.Scheme); err != nil {
			return nil, err
		}
		if err := r.Create(ctx, job); err != nil {
			return nil, err
		}
	} else if !job.DeletionTimestamp.IsZero() {
		return nil, fmt.Errorf("AgentRun Job %s/%s is still terminating; retry after deletion completes", job.Namespace, job.Name)
	}
	return job, nil
}

func (r *AgentRunReconciler) ensureAgentRunExternalSecretFreshness(ctx context.Context, obj *controlv1alpha1.AgentRun, status *controlv1alpha1.AgentRunStatus) (bool, controlv1alpha1.AgentRunPhase, string, string, error) {
	refs := obj.Spec.Harness.Execution.ExternalSecretRefreshRefs
	if len(refs) == 0 {
		return true, "", "", "", nil
	}
	reader := client.Reader(r.Client)
	if r.APIReader != nil {
		reader = r.APIReader
	}

	for _, ref := range refs {
		name := strings.TrimSpace(ref.Name)
		namespace := obj.Namespace
		targetSecret := strings.TrimSpace(ref.TargetSecretRef.Name)
		entry := agentRunExternalSecretRefreshStatus(status, name, namespace, targetSecret)
		externalSecret := &unstructured.Unstructured{}
		externalSecret.SetAPIVersion("external-secrets.io/v1")
		externalSecret.SetKind("ExternalSecret")
		key := client.ObjectKey{Name: name, Namespace: namespace}
		if err := reader.Get(ctx, key, externalSecret); err != nil {
			if apierrors.IsNotFound(err) {
				return false, controlv1alpha1.AgentRunPhaseFailed, "ExternalSecretNotFound", fmt.Sprintf("ExternalSecret %s/%s was not found for the AgentRun credential preflight.", namespace, name), nil
			}
			return false, "", "", "", fmt.Errorf("get ExternalSecret %s/%s: %w", namespace, name, err)
		}
		resolvedTargetSecret := agentRunExternalSecretTargetName(externalSecret, name)
		if resolvedTargetSecret != targetSecret {
			return false, controlv1alpha1.AgentRunPhaseFailed, "ExternalSecretTargetMismatch", fmt.Sprintf("ExternalSecret %s/%s targets Secret %q, but the AgentRun freshness ref declares %q.", namespace, name, resolvedTargetSecret, targetSecret), nil
		}

		if entry == nil {
			now := metav1.Now()
			previousRefreshTime, hasPreviousRefreshTime := agentRunExternalSecretRefreshTime(externalSecret)
			annotations := externalSecret.GetAnnotations()
			if annotations == nil {
				annotations = map[string]string{}
			}
			annotations["force-sync"] = fmt.Sprintf("agentrun-%s-%d", obj.UID, now.UnixNano())
			externalSecret.SetAnnotations(annotations)
			if err := r.Update(ctx, externalSecret); err != nil {
				return false, "", "", "", fmt.Errorf("request ExternalSecret refresh %s/%s: %w", namespace, name, err)
			}
			entry := controlv1alpha1.AgentRunExternalSecretRefreshStatus{Name: name, Namespace: namespace, TargetSecret: targetSecret, RequestedAt: &now}
			if hasPreviousRefreshTime {
				previous := metav1.NewTime(previousRefreshTime)
				entry.PreviousRefreshTime = &previous
			}
			status.ExternalSecretRefreshes = append(status.ExternalSecretRefreshes, entry)
			return false, controlv1alpha1.AgentRunPhasePending, "ExternalSecretRefreshRequested", fmt.Sprintf("Requested a fresh Key Vault reconciliation for ExternalSecret %s/%s before creating the AgentRun Job.", namespace, name), nil
		}
		if agentRunExternalSecretRefreshTimedOut(entry) {
			return false, controlv1alpha1.AgentRunPhaseFailed, "ExternalSecretRefreshTimedOut", fmt.Sprintf("ExternalSecret %s/%s did not report a fresh target Secret within %s.", namespace, name, agentRunExternalSecretRefreshTimeout), nil
		}

		ready, readyMessage := agentRunExternalSecretReady(externalSecret)
		if !ready {
			return false, controlv1alpha1.AgentRunPhasePending, "WaitingForExternalSecretRefresh", fmt.Sprintf("Waiting for ExternalSecret %s/%s to finish its Key Vault reconciliation%s.", namespace, name, agentRunExternalSecretRefreshDetail(readyMessage)), nil
		}
		refreshTime, ok := agentRunExternalSecretRefreshTime(externalSecret)
		if !ok || !agentRunExternalSecretRefreshChanged(entry, refreshTime) {
			return false, controlv1alpha1.AgentRunPhasePending, "WaitingForExternalSecretRefresh", fmt.Sprintf("Waiting for ExternalSecret %s/%s to report a refresh after the AgentRun request.", namespace, name), nil
		}
		secret := &corev1.Secret{}
		if err := reader.Get(ctx, client.ObjectKey{Name: targetSecret, Namespace: namespace}, secret); err != nil {
			if apierrors.IsNotFound(err) {
				return false, controlv1alpha1.AgentRunPhasePending, "WaitingForExternalSecretTarget", fmt.Sprintf("Waiting for ExternalSecret %s/%s to create target Secret %s/%s.", namespace, name, namespace, targetSecret), nil
			}
			return false, "", "", "", fmt.Errorf("get ExternalSecret target Secret %s/%s: %w", namespace, targetSecret, err)
		}
		observedAt := metav1.NewTime(refreshTime)
		entry.RefreshedAt = &observedAt
	}

	return true, "", "", "", nil
}

func agentRunExternalSecretRefreshStatus(status *controlv1alpha1.AgentRunStatus, name, namespace, targetSecret string) *controlv1alpha1.AgentRunExternalSecretRefreshStatus {
	for index := range status.ExternalSecretRefreshes {
		entry := &status.ExternalSecretRefreshes[index]
		if entry.Name == name && entry.Namespace == namespace && entry.TargetSecret == targetSecret {
			return entry
		}
	}
	return nil
}

func agentRunExternalSecretRefreshTimedOut(entry *controlv1alpha1.AgentRunExternalSecretRefreshStatus) bool {
	return entry != nil && entry.RequestedAt != nil && time.Since(entry.RequestedAt.Time) >= agentRunExternalSecretRefreshTimeout
}

func agentRunExternalSecretRefreshChanged(entry *controlv1alpha1.AgentRunExternalSecretRefreshStatus, refreshTime time.Time) bool {
	if entry == nil {
		return false
	}
	if entry.PreviousRefreshTime != nil {
		return refreshTime.After(entry.PreviousRefreshTime.Time)
	}
	return entry.RequestedAt != nil && !refreshTime.Before(entry.RequestedAt.Time.Truncate(time.Second))
}

func agentRunExternalSecretRefreshDetail(message string) string {
	if message = strings.TrimSpace(message); message != "" {
		return ": " + message
	}
	return ""
}

func agentRunExternalSecretReady(obj *unstructured.Unstructured) (bool, string) {
	conditions, found, err := unstructured.NestedSlice(obj.Object, "status", "conditions")
	if err != nil || !found {
		return false, ""
	}
	for _, item := range conditions {
		condition, ok := item.(map[string]any)
		if !ok || condition["type"] != "Ready" {
			continue
		}
		if condition["status"] == "True" {
			return true, ""
		}
		if message, ok := condition["message"].(string); ok {
			return false, strings.TrimSpace(message)
		}
		return false, "ExternalSecret Ready condition is not true"
	}
	return false, ""
}

func agentRunExternalSecretRefreshTime(obj *unstructured.Unstructured) (time.Time, bool) {
	value, found, err := unstructured.NestedString(obj.Object, "status", "refreshTime")
	if err != nil || !found || strings.TrimSpace(value) == "" {
		return time.Time{}, false
	}
	refreshTime, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, false
	}
	return refreshTime, true
}

func agentRunExternalSecretTargetName(obj *unstructured.Unstructured, fallback string) string {
	if name, found, err := unstructured.NestedString(obj.Object, "status", "binding", "name"); err == nil && found && strings.TrimSpace(name) != "" {
		return strings.TrimSpace(name)
	}
	if name, found, err := unstructured.NestedString(obj.Object, "spec", "target", "name"); err == nil && found && strings.TrimSpace(name) != "" {
		return strings.TrimSpace(name)
	}
	return fallback
}

func (r *AgentRunReconciler) existingAgentRunJob(ctx context.Context, obj *controlv1alpha1.AgentRun, ref *controlv1alpha1.NamespacedObjectReference) (*batchv1.Job, string, error) {
	reader := client.Reader(r.Client)
	if r.APIReader != nil {
		reader = r.APIReader
	}
	if ref == nil || strings.TrimSpace(ref.Name) == "" {
		jobs := &batchv1.JobList{}
		if err := reader.List(ctx, jobs, client.InNamespace(obj.Namespace), client.MatchingLabels{agentRunLabel: sanitizeLabelValue(obj.Name)}); err != nil {
			return nil, "", fmt.Errorf("list Jobs owned by AgentRun %s/%s: %w", obj.Namespace, obj.Name, err)
		}
		var owned *batchv1.Job
		for i := range jobs.Items {
			job := &jobs.Items[i]
			if !job.DeletionTimestamp.IsZero() {
				continue
			}
			owner := metav1.GetControllerOf(job)
			if owner == nil || owner.Kind != "AgentRun" || owner.Name != obj.Name {
				continue
			}
			if obj.UID != "" && owner.UID != obj.UID {
				continue
			}
			if owned != nil {
				return nil, "", fmt.Errorf("AgentRun %s/%s owns multiple harness Jobs (%s and %s); refusing to select one", obj.Namespace, obj.Name, owned.Name, job.Name)
			}
			owned = job.DeepCopy()
		}
		return owned, "", nil
	}
	namespace := firstNonEmpty(strings.TrimSpace(ref.Namespace), obj.Namespace)
	name := strings.TrimSpace(ref.Name)
	if namespace != obj.Namespace {
		return nil, fmt.Sprintf("Agent run job reference %s/%s is outside AgentRun namespace %s.", namespace, name, obj.Namespace), nil
	}
	job := &batchv1.Job{}
	if err := reader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, job); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Sprintf("Agent run job %s/%s no longer exists.", namespace, name), nil
		}
		return nil, "", fmt.Errorf("get agent run job %s/%s: %w", namespace, name, err)
	}
	owner := metav1.GetControllerOf(job)
	if owner == nil {
		if obj.UID != "" {
			return nil, fmt.Sprintf("Agent run job %s/%s is not controller-owned by AgentRun %s/%s.", namespace, name, obj.Namespace, obj.Name), nil
		}
	} else if owner.Kind != "AgentRun" || owner.Name != obj.Name || (obj.UID != "" && owner.UID != obj.UID) {
		return nil, fmt.Sprintf("Agent run job %s/%s is controlled by %s %s (UID %s), not AgentRun %s/%s (UID %s).", namespace, name, owner.Kind, owner.Name, owner.UID, obj.Namespace, obj.Name, obj.UID), nil
	}
	return job, "", nil
}

func (r *AgentRunReconciler) agentRunJob(obj *controlv1alpha1.AgentRun, jobName, configMapName string, dataVolumes []resolvedAgentRunDataVolume) *batchv1.Job {
	backoffLimit := int32(0)
	var activeDeadlineSeconds *int64
	if obj.Spec.Harness.Execution.TimeoutSeconds > 0 {
		value := int64(obj.Spec.Harness.Execution.TimeoutSeconds)
		activeDeadlineSeconds = &value
	}
	volumeMounts := []corev1.VolumeMount{{
		Name:      agentRunPayloadVolume,
		MountPath: agentRunPayloadMountPath,
		ReadOnly:  true,
	}}
	volumes := []corev1.Volume{{
		Name: agentRunPayloadVolume,
		VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: configMapName},
			},
		},
	}}
	for index, item := range dataVolumes {
		volumeName := agentRunDataVolumeName(index, item)
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      volumeName,
			MountPath: item.MountPath,
			SubPath:   item.SubPath,
			ReadOnly:  item.ReadOnly,
		})
		volumes = append(volumes, corev1.Volume{
			Name: volumeName,
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: item.ClaimName,
					ReadOnly:  item.ReadOnly,
				},
			},
		})
	}
	if obj.Spec.Harness.Execution.SpiffeWorkloadAPI.Enabled {
		readOnly := true
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      agentRunSpiffeWorkloadAPIVolume,
			MountPath: agentRunSpiffeWorkloadAPIMountPath,
			ReadOnly:  true,
		})
		volumes = append(volumes, corev1.Volume{
			Name: agentRunSpiffeWorkloadAPIVolume,
			VolumeSource: corev1.VolumeSource{
				CSI: &corev1.CSIVolumeSource{
					Driver:   agentRunSpiffeCSIDriver,
					ReadOnly: &readOnly,
				},
			},
		})
	}
	container := corev1.Container{
		Name:            agentRunContainerName,
		Image:           agentRunImage(obj),
		ImagePullPolicy: agentRunImagePullPolicy(obj),
		SecurityContext: agentRunContainerSecurityContext(obj),
		WorkingDir:      strings.TrimSpace(obj.Spec.Harness.Execution.Workdir),
		Env:             r.agentRunEnv(obj, dataVolumes),
		EnvFrom:         agentRunEnvFrom(obj),
		VolumeMounts:    volumeMounts,
		Resources:       obj.Spec.Harness.Execution.Resources,
	}
	if agentRunBackendKind(obj) == controlv1alpha1.AgentRunHarnessBackendCustom && obj.Spec.Harness.Backend.Custom != nil {
		container.Command = append([]string(nil), obj.Spec.Harness.Backend.Custom.Command...)
		container.Args = append([]string(nil), obj.Spec.Harness.Backend.Custom.Args...)
	}
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: obj.Namespace,
			Labels:    agentRunLabels(obj, jobName),
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            &backoffLimit,
			ActiveDeadlineSeconds:   activeDeadlineSeconds,
			TTLSecondsAfterFinished: obj.Spec.Harness.Execution.TTLSecondsAfterFinished,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: agentRunLabels(obj, jobName),
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: strings.TrimSpace(obj.Spec.Harness.Execution.ServiceAccountName),
					RestartPolicy:      corev1.RestartPolicyNever,
					NodeSelector:       agentRunNodeSelector(obj, dataVolumes),
					Affinity:           obj.Spec.Harness.Execution.Affinity,
					Tolerations:        obj.Spec.Harness.Execution.Tolerations,
					ImagePullSecrets:   obj.Spec.Harness.Execution.ImagePullSecrets,
					SecurityContext:    agentRunPodSecurityContext(obj),
					Containers:         []corev1.Container{container},
					Volumes:            volumes,
				},
			},
		},
	}
}

// agentRunJob keeps deterministic Job rendering available to focused tests and
// consumers that do not need a running manager. Production reconciliation uses
// the receiver form so configured platform context is injected.
func agentRunJob(obj *controlv1alpha1.AgentRun, jobName, configMapName string, dataVolumes []resolvedAgentRunDataVolume) *batchv1.Job {
	return (&AgentRunReconciler{CommonReconcilerOptions: CommonReconcilerOptions{Options: DefaultOptions()}}).
		agentRunJob(obj, jobName, configMapName, dataVolumes)
}

func agentRunJobContainerImage(job *batchv1.Job) string {
	if job == nil || len(job.Spec.Template.Spec.Containers) == 0 {
		return ""
	}
	return strings.TrimSpace(job.Spec.Template.Spec.Containers[0].Image)
}

func agentRunJobEnvValue(job *batchv1.Job, name string) string {
	if job == nil || len(job.Spec.Template.Spec.Containers) == 0 {
		return ""
	}
	for _, item := range job.Spec.Template.Spec.Containers[0].Env {
		if item.Name == name {
			return strings.TrimSpace(item.Value)
		}
	}
	return ""
}

func (r *AgentRunReconciler) agentRunEnv(obj *controlv1alpha1.AgentRun, dataVolumes []resolvedAgentRunDataVolume) []corev1.EnvVar {
	platform := r.platformContext()
	env := []corev1.EnvVar{
		{Name: "ANVIL_AGENT_RUN", Value: obj.Name},
		{Name: "ANVIL_AGENT_RUN_NAMESPACE", Value: obj.Namespace},
		{Name: "ANVIL_AGENT_RUN_BACKEND", Value: string(agentRunBackendKind(obj))},
		{Name: "ANVIL_AGENT_RUN_INTENT", Value: string(agentRunIntent(obj))},
		{Name: "ANVIL_AGENT_RUN_SOURCE_KIND", Value: strings.TrimSpace(obj.Spec.SourceRef.Kind)},
		{Name: "ANVIL_AGENT_RUN_SOURCE_NAME", Value: strings.TrimSpace(obj.Spec.SourceRef.Name)},
		{Name: "ANVIL_AGENT_RUN_PROMPT_FILE", Value: agentRunPayloadMountPath + "/" + agentRunPromptFile},
		{Name: "ANVIL_AGENT_RUN_CONTEXT_FILE", Value: agentRunPayloadMountPath + "/" + agentRunContextFile},
		{Name: "ANVIL_AGENT_RUN_STATUS_FILE", Value: agentRunStatusFile},
		{Name: "ANVIL_AGENT_RUN_STATUS_LOG_PREFIX", Value: agentRunStatusLinePrefix},
		{Name: "ANVIL_AGENT_RUN_STATUS_TOOL", Value: "anvil-agent-status"},
		{Name: "ANVIL_AGENT_FEEDBACK_TOOL", Value: "anvil-agent-feedback"},
		{Name: "ANVIL_AGENT_RUN_PLATFORM_REPOSITORY", Value: platform.Repository},
		{Name: "ANVIL_AGENT_RUN_PLATFORM_REPOSITORY_URL", Value: platform.RepositoryURL},
		{Name: "ANVIL_AGENT_RUN_PLATFORM_DOCS", Value: strings.Join(platform.DocsPaths, ",")},
	}
	if obj.Spec.Harness.Execution.TimeoutSeconds > 0 {
		env = append(env, corev1.EnvVar{Name: "ANVIL_AGENT_RUN_TIMEOUT_SECONDS", Value: strconv.Itoa(obj.Spec.Harness.Execution.TimeoutSeconds)})
	}
	if obj.Spec.Harness.Execution.SpiffeWorkloadAPI.Enabled {
		env = append(env,
			corev1.EnvVar{Name: "SPIFFE_ENDPOINT_SOCKET", Value: "unix://" + agentRunSpiffeWorkloadAPISocket},
			corev1.EnvVar{Name: "ANVIL_AGENT_RUN_SPIFFE_ID", Value: strings.TrimSpace(obj.Spec.Harness.Execution.SpiffeWorkloadAPI.SPIFFEID)},
		)
	}
	if provider := agentRunModelProvider(obj); provider != "" {
		env = append(env, corev1.EnvVar{Name: "ANVIL_AGENT_RUN_MODEL_PROVIDER", Value: string(provider)})
	}
	if authMode := agentRunProviderAuthMode(obj); authMode != "" {
		env = append(env, corev1.EnvVar{Name: "ANVIL_AGENT_RUN_PROVIDER_AUTH_MODE", Value: string(authMode)})
	}
	if obj.Spec.SituationRef != nil {
		env = append(env,
			corev1.EnvVar{Name: "ANVIL_ADVERSE_SITUATION_NAME", Value: strings.TrimSpace(obj.Spec.SituationRef.Name)},
			corev1.EnvVar{Name: "ANVIL_ADVERSE_SITUATION_NAMESPACE", Value: firstNonEmpty(strings.TrimSpace(obj.Spec.SituationRef.Namespace), obj.Namespace)},
		)
	}
	if obj.Spec.ScheduleRef != nil {
		env = append(env,
			corev1.EnvVar{Name: "ANVIL_AGENT_SCHEDULE_NAME", Value: strings.TrimSpace(obj.Spec.ScheduleRef.Name)},
			corev1.EnvVar{Name: "ANVIL_AGENT_SCHEDULE_NAMESPACE", Value: firstNonEmpty(strings.TrimSpace(obj.Spec.ScheduleRef.Namespace), obj.Namespace)},
		)
	}
	if obj.Spec.ProfileRef != nil {
		env = append(env,
			corev1.EnvVar{Name: "ANVIL_AGENT_RUN_PROFILE_NAME", Value: strings.TrimSpace(obj.Spec.ProfileRef.Name)},
			corev1.EnvVar{Name: "ANVIL_AGENT_RUN_PROFILE_NAMESPACE", Value: firstNonEmpty(strings.TrimSpace(obj.Spec.ProfileRef.Namespace), obj.Namespace)},
		)
	}
	if skillFiles := agentRunSkillFilesEnv(obj); skillFiles != "" {
		env = append(env, corev1.EnvVar{Name: "ANVIL_AGENT_RUN_SKILL_FILES", Value: skillFiles})
	}
	if setupFiles := agentRunToolSetupFilesEnv(obj); setupFiles != "" {
		env = append(env, corev1.EnvVar{Name: "ANVIL_AGENT_RUN_TOOL_SETUP_FILES", Value: setupFiles})
	}
	if toolsJSON := agentRunToolsJSONEnv(obj); toolsJSON != "" {
		env = append(env, corev1.EnvVar{Name: "ANVIL_AGENT_RUN_TOOLS_JSON", Value: toolsJSON})
	}
	if len(dataVolumes) > 0 {
		for _, item := range dataVolumes {
			env = append(env, item.ExtraEnv...)
		}
		if raw, err := json.Marshal(agentRunDataVolumeStatuses(dataVolumes)); err == nil {
			env = append(env, corev1.EnvVar{Name: "ANVIL_AGENT_RUN_DATA_VOLUMES_JSON", Value: string(raw)})
		}
	}
	if agentRunBackendKind(obj) == controlv1alpha1.AgentRunHarnessBackendCodex {
		codex := obj.Spec.Harness.Backend.Codex
		if codex == nil {
			codex = &controlv1alpha1.AgentRunCodexBackendSpec{}
		}
		env = append(env,
			corev1.EnvVar{Name: "ANVIL_CODEX_MODEL", Value: strings.TrimSpace(codex.Model)},
			corev1.EnvVar{Name: "ANVIL_CODEX_REASONING_EFFORT", Value: strings.TrimSpace(codex.ReasoningEffort)},
			corev1.EnvVar{Name: "ANVIL_CODEX_VERBOSITY", Value: strings.TrimSpace(codex.Verbosity)},
			corev1.EnvVar{Name: "ANVIL_CODEX_SERVICE_TIER", Value: strings.TrimSpace(codex.ServiceTier)},
			corev1.EnvVar{Name: "ANVIL_CODEX_GOAL_MODE", Value: strconv.FormatBool(codex.GoalMode)},
			corev1.EnvVar{Name: "ANVIL_CODEX_SANDBOX", Value: agentRunCodexSandbox(obj, codex)},
			corev1.EnvVar{Name: "ANVIL_CODEX_APPROVAL_POLICY", Value: "never"},
		)
		if strings.TrimSpace(codex.Goal) != "" {
			env = append(env, corev1.EnvVar{Name: "ANVIL_CODEX_GOAL", Value: strings.TrimSpace(codex.Goal)})
		}
		if len(codex.AdditionalArgs) > 0 {
			if raw, err := json.Marshal(codex.AdditionalArgs); err == nil {
				env = append(env, corev1.EnvVar{Name: "ANVIL_CODEX_ADDITIONAL_ARGS_JSON", Value: string(raw)})
			}
		}
	}
	if agentRunBackendKind(obj) == controlv1alpha1.AgentRunHarnessBackendHermesAgent {
		hermes := obj.Spec.Harness.Backend.HermesAgent
		if hermes == nil {
			hermes = &controlv1alpha1.AgentRunHermesBackendSpec{}
		}
		env = append(env,
			corev1.EnvVar{Name: "ANVIL_HERMES_MODEL_PROVIDER", Value: agentRunHermesModelProvider(obj)},
			corev1.EnvVar{Name: "ANVIL_HERMES_PROVIDER_AUTH_MODE", Value: string(agentRunProviderAuthMode(obj))},
			corev1.EnvVar{Name: "ANVIL_HERMES_MODEL", Value: strings.TrimSpace(hermes.Model)},
			corev1.EnvVar{Name: "ANVIL_HERMES_REASONING_EFFORT", Value: strings.TrimSpace(hermes.ReasoningEffort)},
			corev1.EnvVar{Name: "ANVIL_HERMES_SERVICE_TIER", Value: strings.TrimSpace(hermes.ServiceTier)},
			corev1.EnvVar{Name: "ANVIL_HERMES_PROFILE", Value: strings.TrimSpace(hermes.Profile)},
			corev1.EnvVar{Name: "ANVIL_HERMES_USE_CODEX_APP_SERVER", Value: strconv.FormatBool(hermes.UseCodexAppServer)},
		)
		if len(hermes.AdditionalArgs) > 0 {
			if raw, err := json.Marshal(hermes.AdditionalArgs); err == nil {
				env = append(env, corev1.EnvVar{Name: "ANVIL_HERMES_ADDITIONAL_ARGS_JSON", Value: string(raw)})
			}
		}
	}
	if agentRunBackendKind(obj) == controlv1alpha1.AgentRunHarnessBackendOpenClaw {
		openClaw := obj.Spec.Harness.Backend.OpenClaw
		if openClaw == nil {
			openClaw = &controlv1alpha1.AgentRunOpenClawBackendSpec{}
		}
		env = append(env,
			corev1.EnvVar{Name: "ANVIL_OPENCLAW_PROVIDER", Value: string(agentRunModelProvider(obj))},
			corev1.EnvVar{Name: "ANVIL_OPENCLAW_PROVIDER_AUTH_MODE", Value: string(agentRunProviderAuthMode(obj))},
			corev1.EnvVar{Name: "ANVIL_OPENCLAW_AGENT_ID", Value: strings.TrimSpace(openClaw.AgentID)},
			corev1.EnvVar{Name: "ANVIL_OPENCLAW_MODEL", Value: strings.TrimSpace(openClaw.Model)},
			corev1.EnvVar{Name: "ANVIL_OPENCLAW_THINKING", Value: strings.TrimSpace(openClaw.Thinking)},
			corev1.EnvVar{Name: "ANVIL_OPENCLAW_SERVICE_TIER", Value: strings.TrimSpace(openClaw.ServiceTier)},
		)
		if openClaw.Local != nil {
			env = append(env, corev1.EnvVar{Name: "ANVIL_OPENCLAW_LOCAL", Value: strconv.FormatBool(*openClaw.Local)})
		}
		if len(openClaw.AdditionalArgs) > 0 {
			if raw, err := json.Marshal(openClaw.AdditionalArgs); err == nil {
				env = append(env, corev1.EnvVar{Name: "ANVIL_OPENCLAW_ADDITIONAL_ARGS_JSON", Value: string(raw)})
			}
		}
	}
	if agentRunBackendKind(obj) == controlv1alpha1.AgentRunHarnessBackendGrokBuild {
		grokBuild := obj.Spec.Harness.Backend.GrokBuild
		if grokBuild == nil {
			grokBuild = &controlv1alpha1.AgentRunGrokBuildBackendSpec{}
		}
		env = append(env,
			corev1.EnvVar{Name: "ANVIL_GROK_BUILD_MODEL_PROVIDER", Value: string(agentRunModelProvider(obj))},
			corev1.EnvVar{Name: "ANVIL_GROK_BUILD_PROVIDER_AUTH_MODE", Value: string(agentRunProviderAuthMode(obj))},
			corev1.EnvVar{Name: "ANVIL_GROK_BUILD_MODEL", Value: strings.TrimSpace(grokBuild.Model)},
			corev1.EnvVar{Name: "ANVIL_GROK_BUILD_REASONING_EFFORT", Value: strings.TrimSpace(grokBuild.ReasoningEffort)},
			corev1.EnvVar{Name: "ANVIL_GROK_BUILD_SERVICE_TIER", Value: strings.TrimSpace(grokBuild.ServiceTier)},
			corev1.EnvVar{Name: "ANVIL_GROK_BUILD_PROFILE", Value: strings.TrimSpace(grokBuild.Profile)},
			corev1.EnvVar{Name: "ANVIL_GROK_BUILD_COMMAND", Value: strings.TrimSpace(grokBuild.Command)},
		)
		if len(grokBuild.AdditionalArgs) > 0 {
			if raw, err := json.Marshal(grokBuild.AdditionalArgs); err == nil {
				env = append(env, corev1.EnvVar{Name: "ANVIL_GROK_BUILD_ADDITIONAL_ARGS_JSON", Value: string(raw)})
			}
		}
	}
	if agentRunBackendKind(obj) == controlv1alpha1.AgentRunHarnessBackendPiAgent {
		piAgent := obj.Spec.Harness.Backend.PiAgent
		if piAgent == nil {
			piAgent = &controlv1alpha1.AgentRunPiBackendSpec{}
		}
		env = append(env,
			corev1.EnvVar{Name: "ANVIL_PI_MODEL_PROVIDER", Value: string(agentRunModelProvider(obj))},
			corev1.EnvVar{Name: "ANVIL_PI_PROVIDER_AUTH_MODE", Value: string(agentRunProviderAuthMode(obj))},
			corev1.EnvVar{Name: "ANVIL_PI_PROVIDER", Value: agentRunPiProvider(obj, piAgent)},
			corev1.EnvVar{Name: "ANVIL_PI_MODEL", Value: strings.TrimSpace(piAgent.Model)},
			corev1.EnvVar{Name: "ANVIL_PI_THINKING", Value: strings.TrimSpace(piAgent.Thinking)},
			corev1.EnvVar{Name: "ANVIL_PI_MODE", Value: strings.TrimSpace(piAgent.Mode)},
			corev1.EnvVar{Name: "ANVIL_PI_NO_SESSION", Value: strconv.FormatBool(piAgent.NoSession)},
		)
		if len(piAgent.AdditionalArgs) > 0 {
			if raw, err := json.Marshal(piAgent.AdditionalArgs); err == nil {
				env = append(env, corev1.EnvVar{Name: "ANVIL_PI_ADDITIONAL_ARGS_JSON", Value: string(raw)})
			}
		}
	}
	env = append(env, obj.Spec.Harness.Execution.ExtraEnv...)
	return env
}

func agentRunEnvFrom(obj *controlv1alpha1.AgentRun) []corev1.EnvFromSource {
	refs := obj.Spec.Harness.Execution.EnvSecretRefs
	out := make([]corev1.EnvFromSource, 0, len(refs))
	for _, ref := range refs {
		name := strings.TrimSpace(ref.Name)
		if name == "" {
			continue
		}
		out = append(out, corev1.EnvFromSource{
			SecretRef: &corev1.SecretEnvSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: name},
			},
		})
	}
	return out
}

func agentRunSkillFilesEnv(obj *controlv1alpha1.AgentRun) string {
	if len(obj.Spec.Harness.SkillInjections) == 0 {
		return ""
	}
	files := make([]string, 0, len(obj.Spec.Harness.SkillInjections))
	for index, skill := range obj.Spec.Harness.SkillInjections {
		files = append(files, agentRunPayloadMountPath+"/"+agentRunSkillFileName(index, skill))
	}
	return strings.Join(files, "\n")
}

func agentRunSkillFileName(index int, skill controlv1alpha1.AgentRunSkillInjectionSpec) string {
	name := strings.ToLower(strings.TrimSpace(skill.Name))
	name = agentRunSkillFileNameUnsafeChars.ReplaceAllString(name, "-")
	name = strings.Trim(name, "-_.")
	if name == "" {
		name = "unnamed"
	}
	if len(name) > 80 {
		name = strings.Trim(name[:80], "-_.")
		if name == "" {
			name = "unnamed"
		}
	}
	return fmt.Sprintf("%s%02d-%s.md", agentRunSkillFilePrefix, index+1, name)
}

func agentRunToolSetupFileName(index int, tool controlv1alpha1.AgentRunToolSpec) string {
	name := strings.ToLower(strings.TrimSpace(tool.Name))
	name = agentRunSkillFileNameUnsafeChars.ReplaceAllString(name, "-")
	name = strings.Trim(name, "-_.")
	if name == "" {
		name = "unnamed"
	}
	if len(name) > 80 {
		name = strings.Trim(name[:80], "-_.")
		if name == "" {
			name = "unnamed"
		}
	}
	return fmt.Sprintf("%s%02d-%s-setup.sh", agentRunToolFilePrefix, index+1, name)
}

type resolvedAgentRunSkillSource struct {
	Description string
	Content     string
}

func agentRunSkillFileContent(skill controlv1alpha1.AgentRunSkillInjectionSpec) string {
	return agentRunSkillFileContentWithSources(skill, nil)
}

func agentRunSkillFileContentWithSources(skill controlv1alpha1.AgentRunSkillInjectionSpec, sources []resolvedAgentRunSkillSource) string {
	parts := []string{
		"# Skill: " + strings.TrimSpace(skill.Name),
	}
	if description := strings.TrimSpace(skill.Description); description != "" {
		parts = append(parts, "", "## When To Apply", description)
	}
	if len(skill.Paths) > 0 {
		parts = append(parts, "", "## Required Context Paths")
		for _, path := range skill.Paths {
			if path = strings.TrimSpace(path); path != "" {
				parts = append(parts, "- "+path)
			}
		}
	}
	if content := strings.TrimSpace(skill.Content); content != "" {
		parts = append(parts, "", "## Instructions", content)
	}
	if len(sources) > 0 {
		parts = append(parts, "", "## Downloaded Source Content")
		for _, source := range sources {
			description := strings.TrimSpace(source.Description)
			if description == "" {
				description = "remote source"
			}
			parts = append(parts, "", "### "+description)
			if content := strings.TrimSpace(source.Content); content != "" {
				parts = append(parts, "", content)
			}
		}
	}
	return strings.Join(parts, "\n") + "\n"
}

func (r *AgentRunReconciler) agentRunSkillFileContent(ctx context.Context, obj *controlv1alpha1.AgentRun, skill controlv1alpha1.AgentRunSkillInjectionSpec) (string, error) {
	if len(skill.SourceRefs) == 0 {
		return agentRunSkillFileContent(skill), nil
	}
	sources := make([]resolvedAgentRunSkillSource, 0, len(skill.SourceRefs))
	for index, sourceRef := range skill.SourceRefs {
		source, err := r.resolveAgentRunSkillSource(ctx, obj, sourceRef)
		if err != nil {
			return "", fmt.Errorf("resolve skill %q sourceRefs[%d]: %w", skill.Name, index, err)
		}
		sources = append(sources, source)
	}
	return agentRunSkillFileContentWithSources(skill, sources), nil
}

func (r *AgentRunReconciler) resolveAgentRunSkillSource(ctx context.Context, obj *controlv1alpha1.AgentRun, sourceRef controlv1alpha1.AgentRunSkillSourceRef) (resolvedAgentRunSkillSource, error) {
	if sourceRef.GitHub != nil {
		return r.resolveAgentRunGitHubSkillSource(ctx, obj, *sourceRef.GitHub)
	}
	return resolvedAgentRunSkillSource{}, fmt.Errorf("sourceRefs entries must configure a provider")
}

func (r *AgentRunReconciler) resolveAgentRunGitHubSkillSource(ctx context.Context, obj *controlv1alpha1.AgentRun, spec controlv1alpha1.AgentRunGitHubSkillSourceSpec) (resolvedAgentRunSkillSource, error) {
	sourceURL, err := r.agentRunGitHubContentsURL(spec)
	if err != nil {
		return resolvedAgentRunSkillSource{}, err
	}

	token := ""
	if spec.TokenSecretRef != nil {
		token, err = r.agentRunReadSkillSourceToken(ctx, obj, *spec.TokenSecretRef)
		if err != nil {
			return resolvedAgentRunSkillSource{}, err
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return resolvedAgentRunSkillSource{}, err
	}
	req.Header.Set("Accept", "application/vnd.github.raw+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return resolvedAgentRunSkillSource{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return resolvedAgentRunSkillSource{}, fmt.Errorf("GitHub contents request failed for %s:%s with HTTP %d: %s", spec.Repository, spec.Path, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	content, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return resolvedAgentRunSkillSource{}, err
	}
	if strings.TrimSpace(string(content)) == "" {
		return resolvedAgentRunSkillSource{}, fmt.Errorf("GitHub contents request returned empty content for %s:%s", spec.Repository, spec.Path)
	}

	ref := strings.TrimSpace(spec.Ref)
	if ref == "" {
		ref = "default branch"
	}
	return resolvedAgentRunSkillSource{
		Description: fmt.Sprintf("GitHub %s:%s @ %s", strings.TrimSpace(spec.Repository), strings.TrimSpace(spec.Path), ref),
		Content:     string(content),
	}, nil
}

func (r *AgentRunReconciler) agentRunReadSkillSourceToken(ctx context.Context, obj *controlv1alpha1.AgentRun, ref controlv1alpha1.SecretKeyReference) (string, error) {
	namespace := strings.TrimSpace(ref.Namespace)
	if namespace == "" {
		namespace = obj.Namespace
	}
	if namespace != obj.Namespace {
		return "", fmt.Errorf("skill source token secret %s/%s must be in the AgentRun namespace %s", namespace, ref.Name, obj.Namespace)
	}
	secret := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{Name: strings.TrimSpace(ref.Name), Namespace: namespace}, secret); err != nil {
		return "", err
	}
	key := strings.TrimSpace(ref.Key)
	value, ok := secret.Data[key]
	if !ok {
		return "", fmt.Errorf("skill source token secret %s/%s is missing key %q", namespace, ref.Name, key)
	}
	token := strings.TrimSpace(string(value))
	if token == "" {
		return "", fmt.Errorf("skill source token secret %s/%s key %q is empty", namespace, ref.Name, key)
	}
	return token, nil
}

func (r *AgentRunReconciler) agentRunGitHubContentsURL(spec controlv1alpha1.AgentRunGitHubSkillSourceSpec) (string, error) {
	apiBase := strings.TrimSpace(spec.APIBaseURL)
	if apiBase == "" {
		apiBase = agentRunDefaultGitHubAPIBaseURL
	}
	baseURL, err := url.Parse(apiBase)
	if err != nil {
		return "", err
	}
	if baseURL.User != nil {
		return "", fmt.Errorf("github.apiBaseURL must not include credentials")
	}
	if baseURL.Host == "" {
		return "", fmt.Errorf("github.apiBaseURL must include a host")
	}
	options := r.Options
	if options == nil {
		options = DefaultOptions()
	}
	if baseURL.Scheme != "https" && !(options.AllowInsecureGitHubAPI && baseURL.Scheme == "http") {
		return "", fmt.Errorf("github.apiBaseURL must use https")
	}
	host := strings.ToLower(strings.TrimSpace(baseURL.Hostname()))
	allowed := false
	for _, configured := range options.GitHubAPIAllowedHosts {
		if host == strings.ToLower(strings.TrimSpace(configured)) {
			allowed = true
			break
		}
	}
	if !allowed {
		return "", fmt.Errorf("github.apiBaseURL host %q is not in the operator allowlist", host)
	}
	repository := strings.Split(strings.Trim(strings.TrimSpace(spec.Repository), "/"), "/")
	if len(repository) != 2 || strings.TrimSpace(repository[0]) == "" || strings.TrimSpace(repository[1]) == "" {
		return "", fmt.Errorf("github.repository must use owner/name form")
	}
	filePath := strings.TrimSpace(spec.Path)
	if err := agentRunValidateRemoteFilePath(filePath); err != nil {
		return "", err
	}

	segments := []string{strings.TrimRight(baseURL.Path, "/"), "repos", url.PathEscape(repository[0]), url.PathEscape(repository[1]), "contents"}
	for _, part := range strings.Split(filePath, "/") {
		segments = append(segments, url.PathEscape(part))
	}
	baseURL.Path = strings.Join(segments, "/")
	baseURL.RawPath = ""
	if ref := strings.TrimSpace(spec.Ref); ref != "" {
		query := baseURL.Query()
		query.Set("ref", ref)
		baseURL.RawQuery = query.Encode()
	}
	return baseURL.String(), nil
}

func agentRunValidateRemoteFilePath(filePath string) error {
	if strings.TrimSpace(filePath) == "" {
		return fmt.Errorf("github.path is required")
	}
	if strings.HasPrefix(filePath, "/") {
		return fmt.Errorf("github.path must be repository-relative")
	}
	for _, part := range strings.Split(filePath, "/") {
		part = strings.TrimSpace(part)
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("github.path must not contain empty, '.', or '..' segments")
		}
	}
	return nil
}

func agentRunToolSetupFileContent(tool controlv1alpha1.AgentRunToolSpec) string {
	script := strings.TrimSpace(tool.SetupScript)
	if strings.HasPrefix(script, "#!") {
		return script + "\n"
	}
	return "#!/usr/bin/env bash\nset -euo pipefail\n\n" + script + "\n"
}

func agentRunToolSetupFilesEnv(obj *controlv1alpha1.AgentRun) string {
	if len(obj.Spec.Harness.Tools) == 0 {
		return ""
	}
	files := []string{}
	for index, tool := range obj.Spec.Harness.Tools {
		if strings.TrimSpace(tool.SetupScript) == "" {
			continue
		}
		files = append(files, agentRunPayloadMountPath+"/"+agentRunToolSetupFileName(index, tool))
	}
	return strings.Join(files, "\n")
}

func agentRunToolsJSONEnv(obj *controlv1alpha1.AgentRun) string {
	if len(obj.Spec.Harness.Tools) == 0 {
		return ""
	}
	tools := make([]map[string]any, 0, len(obj.Spec.Harness.Tools))
	for index, tool := range obj.Spec.Harness.Tools {
		item := map[string]any{
			"name":        strings.TrimSpace(tool.Name),
			"description": strings.TrimSpace(tool.Description),
		}
		if strings.TrimSpace(tool.SetupScript) != "" {
			item["setupFile"] = agentRunPayloadMountPath + "/" + agentRunToolSetupFileName(index, tool)
		}
		if len(tool.VerifyCommand) > 0 {
			item["verifyCommand"] = append([]string(nil), tool.VerifyCommand...)
		}
		tools = append(tools, item)
	}
	raw, err := json.Marshal(tools)
	if err != nil {
		return ""
	}
	return string(raw)
}

type resolvedAgentRunDataVolume struct {
	Name         string
	Namespace    string
	ClaimName    string
	MountPath    string
	SubPath      string
	ReadOnly     bool
	ExtraEnv     []corev1.EnvVar
	NodeSelector map[string]string
}

func (r *AgentRunReconciler) resolveAgentRunDataVolumes(ctx context.Context, obj *controlv1alpha1.AgentRun) ([]resolvedAgentRunDataVolume, controlv1alpha1.AgentRunPhase, string, string, error) {
	refs := obj.Spec.Harness.Execution.DataVolumeRefs
	if len(refs) == 0 {
		return nil, "", "", "", nil
	}
	reader := client.Reader(r.Client)
	if r.APIReader != nil {
		reader = r.APIReader
	}
	applicationName, err := resolveAgentRunApplicationName(ctx, reader, obj)
	if err != nil {
		return nil, "", "", "", fmt.Errorf("resolve AgentRun application for data volumes: %w", err)
	}
	out := make([]resolvedAgentRunDataVolume, 0, len(refs))
	nodeSelector := map[string]string{}
	for key, value := range obj.Spec.Harness.Execution.NodeSelector {
		nodeSelector[key] = value
	}
	for _, ref := range refs {
		name := strings.TrimSpace(ref.Name)
		if name == "" {
			return nil, controlv1alpha1.AgentRunPhaseFailed, "InvalidDataVolumeRef", "spec.harness.execution.dataVolumeRefs entries must set name.", nil
		}
		namespace := firstNonEmpty(strings.TrimSpace(ref.Namespace), obj.Namespace)
		if namespace != obj.Namespace {
			return nil, controlv1alpha1.AgentRunPhaseFailed, "CrossNamespaceDataVolumeRef", "AgentRun dataVolumeRefs must reference AgentDataVolume objects in the AgentRun namespace.", nil
		}
		volume := &controlv1alpha1.AgentDataVolume{}
		if err := reader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, volume); err != nil {
			if apierrors.IsNotFound(err) {
				return nil, controlv1alpha1.AgentRunPhaseNeedsHuman, "DataVolumeNotFound", fmt.Sprintf("AgentDataVolume %s/%s was not found.", namespace, name), nil
			}
			return nil, "", "", "", err
		}
		if volume.Spec.ApplicationRef != nil {
			volumeApplication := strings.TrimSpace(volume.Spec.ApplicationRef.Name)
			if applicationName == "" || volumeApplication != applicationName {
				return nil, controlv1alpha1.AgentRunPhaseFailed, "DataVolumeApplicationMismatch", fmt.Sprintf("AgentDataVolume %s/%s is scoped to Application %q, but the AgentRun resolves to %q.", namespace, name, volumeApplication, applicationName), nil
			}
		}
		if volume.Status.ObservedGeneration == 0 || volume.Status.ObservedGeneration != volume.Generation {
			return nil, controlv1alpha1.AgentRunPhasePending, "DataVolumeStatusStale", fmt.Sprintf("Waiting for AgentDataVolume %s/%s status to observe generation %d.", namespace, name, volume.Generation), nil
		}
		if volume.Status.Phase == controlv1alpha1.AgentDataVolumePhasePending || volume.Status.Phase == "" {
			return nil, controlv1alpha1.AgentRunPhasePending, "DataVolumeNotReady", fmt.Sprintf("Waiting for AgentDataVolume %s/%s to become Ready.", namespace, name), nil
		}
		if volume.Status.Phase == controlv1alpha1.AgentDataVolumePhaseBlocked {
			message := firstNonEmpty(strings.TrimSpace(volume.Status.LastError), fmt.Sprintf("AgentDataVolume %s/%s is blocked.", namespace, name))
			return nil, controlv1alpha1.AgentRunPhaseNeedsHuman, "DataVolumeBlocked", message, nil
		}
		claimName := agentDataVolumeClaimName(volume)
		if volume.Status.ClaimRef != nil && strings.TrimSpace(volume.Status.ClaimRef.Name) != "" {
			claimNamespace := firstNonEmpty(strings.TrimSpace(volume.Status.ClaimRef.Namespace), namespace)
			if claimNamespace != namespace {
				return nil, controlv1alpha1.AgentRunPhaseFailed, "CrossNamespaceDataVolumeClaim", fmt.Sprintf("AgentDataVolume %s/%s resolved claim %s/%s outside the AgentRun namespace.", namespace, name, claimNamespace, strings.TrimSpace(volume.Status.ClaimRef.Name)), nil
			}
			// status.claimRef is the controller-accepted immutable identity. Never
			// consume a rejected spec.claimName drift when constructing a Job.
			claimName = strings.TrimSpace(volume.Status.ClaimRef.Name)
		}
		if strings.TrimSpace(claimName) == "" {
			return nil, controlv1alpha1.AgentRunPhaseNeedsHuman, "DataVolumeClaimNotConfigured", fmt.Sprintf("AgentDataVolume %s/%s does not resolve to a PersistentVolumeClaim.", namespace, name), nil
		}
		volumeNodeSelector := agentDataVolumeResolvedNodeSelector(volume)
		for key, value := range volumeNodeSelector {
			if existing, ok := nodeSelector[key]; ok && existing != value {
				return nil, controlv1alpha1.AgentRunPhaseFailed, "ConflictingNodeSelector", fmt.Sprintf("AgentDataVolume %s/%s requires nodeSelector %s=%s, but the AgentRun already sets %s=%s.", namespace, name, key, value, key, existing), nil
			}
			nodeSelector[key] = value
		}
		out = append(out, resolvedAgentRunDataVolume{
			Name:         name,
			Namespace:    namespace,
			ClaimName:    claimName,
			MountPath:    firstNonEmpty(strings.TrimSpace(ref.MountPath), agentDataVolumeResolvedMountPath(volume)),
			SubPath:      firstNonEmpty(strings.TrimSpace(ref.SubPath), agentDataVolumeResolvedSubPath(volume)),
			ReadOnly:     ref.ReadOnly,
			ExtraEnv:     agentDataVolumeResolvedExtraEnv(volume),
			NodeSelector: cloneStringMap(volumeNodeSelector),
		})
	}
	return out, "", "", "", nil
}

func agentRunDataVolumeStatuses(dataVolumes []resolvedAgentRunDataVolume) []controlv1alpha1.AgentRunDataVolumeStatus {
	if len(dataVolumes) == 0 {
		return nil
	}
	out := make([]controlv1alpha1.AgentRunDataVolumeStatus, 0, len(dataVolumes))
	for _, item := range dataVolumes {
		out = append(out, controlv1alpha1.AgentRunDataVolumeStatus{
			Name:      item.Name,
			Namespace: item.Namespace,
			ClaimName: item.ClaimName,
			MountPath: item.MountPath,
			ReadOnly:  item.ReadOnly,
		})
	}
	return out
}

func agentRunDataVolumeName(index int, item resolvedAgentRunDataVolume) string {
	return agentRunChildName(agentRunDataVolumePrefix, strconv.Itoa(index+1), item.Name)
}

func agentRunNodeSelector(obj *controlv1alpha1.AgentRun, dataVolumes []resolvedAgentRunDataVolume) map[string]string {
	out := map[string]string{}
	for _, item := range dataVolumes {
		for key, value := range item.NodeSelector {
			out[key] = value
		}
	}
	for key, value := range obj.Spec.Harness.Execution.NodeSelector {
		out[key] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func (r *AgentRunReconciler) patchAgentRunLaunchPausedStatus(ctx context.Context, original, obj *controlv1alpha1.AgentRun, status *controlv1alpha1.AgentRunStatus, reason, message string, requeueAfter time.Duration) (ctrl.Result, error) {
	now := metav1.Now()
	status.Phase = controlv1alpha1.AgentRunPhasePending
	status.CompletedAt = nil
	status.Error = ""
	apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
		Type:               agentRunReady,
		Status:             metav1.ConditionFalse,
		ObservedGeneration: obj.Generation,
		LastTransitionTime: now,
		Reason:             reason,
		Message:            message,
	})
	obj.Status = *status
	if err := r.Status().Patch(ctx, obj, client.MergeFrom(original)); err != nil {
		if apierrors.IsConflict(err) {
			return ctrl.Result{Requeue: true}, nil
		}
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

func (r *AgentRunReconciler) patchAgentRunStatus(ctx context.Context, original, obj *controlv1alpha1.AgentRun, requeue bool) (ctrl.Result, error) {
	if err := r.Status().Patch(ctx, obj, client.MergeFrom(original)); err != nil {
		if apierrors.IsConflict(err) {
			return ctrl.Result{Requeue: true}, nil
		}
		return ctrl.Result{}, err
	}
	if requeue {
		return ctrl.Result{RequeueAfter: agentRunPollInterval}, nil
	}
	return ctrl.Result{}, nil
}

func (r *AgentRunReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("agentrun").
		For(&controlv1alpha1.AgentRun{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&batchv1.Job{}).
		Complete(r)
}

func (r *AgentRunReconciler) resolveAgentRunProfile(ctx context.Context, obj *controlv1alpha1.AgentRun) (*controlv1alpha1.AgentRun, controlv1alpha1.AgentRunPhase, string, string, error) {
	if obj.Spec.ProfileRef == nil {
		return obj.DeepCopy(), "", "", "", nil
	}
	name := strings.TrimSpace(obj.Spec.ProfileRef.Name)
	if name == "" {
		return obj.DeepCopy(), controlv1alpha1.AgentRunPhaseFailed, "InvalidProfileRef", "spec.profileRef.name is required when profileRef is set.", nil
	}
	namespace := firstNonEmpty(strings.TrimSpace(obj.Spec.ProfileRef.Namespace), obj.Namespace)
	if namespace != obj.Namespace {
		return obj.DeepCopy(), controlv1alpha1.AgentRunPhaseFailed, "CrossNamespaceProfileRef", "AgentRun profileRef must reference an AgentRunProfile in the AgentRun namespace.", nil
	}
	profile := &controlv1alpha1.AgentRunProfile{}
	reader := client.Reader(r.Client)
	if r.APIReader != nil {
		reader = r.APIReader
	}
	if err := reader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, profile); err != nil {
		if apierrors.IsNotFound(err) {
			return obj.DeepCopy(), controlv1alpha1.AgentRunPhaseNeedsHuman, "ProfileNotFound", fmt.Sprintf("AgentRunProfile %s/%s was not found.", namespace, name), nil
		}
		return nil, "", "", "", err
	}
	return agentRunApplyProfile(obj, profile), "", "", "", nil
}

func agentRunApplyProfile(obj *controlv1alpha1.AgentRun, profile *controlv1alpha1.AgentRunProfile) *controlv1alpha1.AgentRun {
	out := obj.DeepCopy()
	out.Spec.Scope = agentRunMergeScope(profile.Spec.Scope, obj.Spec.Scope)
	out.Spec.Docs = agentRunMergeDocs(profile.Spec.Docs, obj.Spec.Docs)
	out.Spec.IssueTracking = agentRunMergeIssueTracking(profile.Spec.IssueTracking, obj.Spec.IssueTracking)
	out.Spec.Harness = agentRunMergeHarness(profile.Spec.Harness, obj.Spec.Harness)
	out.Spec.Notifications = agentRunMergeNotifications(profile.Spec.Notifications, obj.Spec.Notifications)
	return out
}

func agentRunMergeScope(profile, run controlv1alpha1.AgentRunScopeSpec) controlv1alpha1.AgentRunScopeSpec {
	out := *profile.DeepCopy()
	if strings.TrimSpace(run.Summary) != "" {
		out.Summary = run.Summary
	}
	if run.ApplicationRef != nil {
		out.ApplicationRef = run.ApplicationRef.DeepCopy()
	}
	if run.ApplicationTargetRef != nil {
		out.ApplicationTargetRef = run.ApplicationTargetRef.DeepCopy()
	}
	out.Namespaces = appendUniqueStrings(out.Namespaces, run.Namespaces...)
	out.ResourceKinds = appendUniqueStrings(out.ResourceKinds, run.ResourceKinds...)
	return out
}

func agentRunMergeDocs(profile, run *controlv1alpha1.AgentRunDocsSpec) *controlv1alpha1.AgentRunDocsSpec {
	if profile == nil && run == nil {
		return nil
	}
	out := &controlv1alpha1.AgentRunDocsSpec{}
	if profile != nil {
		out = profile.DeepCopy()
	}
	if run == nil {
		return out
	}
	if run.Policy != "" {
		out.Policy = run.Policy
	}
	out.Paths = appendUniqueStrings(out.Paths, run.Paths...)
	out.RuntimePaths = appendUniqueStrings(out.RuntimePaths, run.RuntimePaths...)
	out.Notes = mergePromptText(out.Notes, run.Notes)
	return out
}

func agentRunMergeIssueTracking(profile, run *controlv1alpha1.AgentRunIssueTrackingSpec) *controlv1alpha1.AgentRunIssueTrackingSpec {
	if profile == nil && run == nil {
		return nil
	}
	out := &controlv1alpha1.AgentRunIssueTrackingSpec{}
	if profile != nil {
		out = profile.DeepCopy()
	}
	if run == nil {
		return out
	}
	if run.Provider != "" {
		out.Provider = run.Provider
	}
	if strings.TrimSpace(run.Repository) != "" {
		out.Repository = run.Repository
	}
	out.Issues = append(out.Issues, run.Issues...)
	if strings.TrimSpace(run.SearchQuery) != "" {
		out.SearchQuery = run.SearchQuery
	}
	if run.UpdatePolicy != "" {
		out.UpdatePolicy = run.UpdatePolicy
	}
	return out
}

func agentRunMergeNotifications(profile, run *controlv1alpha1.AgentRunNotificationSpec) *controlv1alpha1.AgentRunNotificationSpec {
	if profile == nil && run == nil {
		return nil
	}
	out := &controlv1alpha1.AgentRunNotificationSpec{}
	if profile != nil {
		out = profile.DeepCopy()
	}
	if run == nil {
		return out
	}
	out.MobileOps = out.MobileOps || run.MobileOps
	if run.Telegram != nil {
		out.Telegram = run.Telegram.DeepCopy()
	}
	if run.Discord != nil {
		out.Discord = run.Discord.DeepCopy()
	}
	return out
}

func agentRunMergeHarness(profile, run controlv1alpha1.AgentRunHarnessSpec) controlv1alpha1.AgentRunHarnessSpec {
	out := *profile.DeepCopy()
	if run.Intent != "" {
		out.Intent = run.Intent
	}
	out.Backend = agentRunMergeBackend(profile.Backend, run.Backend)
	out.Execution = agentRunMergeExecution(profile.Execution, run.Execution)
	out.SkillInjections = append(out.SkillInjections, run.SkillInjections...)
	out.Subagents = append(out.Subagents, run.Subagents...)
	out.Tools = append(out.Tools, run.Tools...)
	out.SystemPrompt = mergePromptText(profile.SystemPrompt, run.SystemPrompt)
	return out
}

func agentRunMergeBackend(profile, run controlv1alpha1.AgentRunHarnessBackendSpec) controlv1alpha1.AgentRunHarnessBackendSpec {
	out := *profile.DeepCopy()
	if run.Kind != "" {
		out.Kind = run.Kind
	}
	if strings.TrimSpace(run.Image) != "" {
		out.Image = run.Image
	}
	if run.ModelProvider != "" {
		out.ModelProvider = run.ModelProvider
	}
	if run.ProviderAuthMode != "" {
		out.ProviderAuthMode = run.ProviderAuthMode
	}
	if run.ImagePullPolicy != "" {
		out.ImagePullPolicy = run.ImagePullPolicy
	}
	out.Codex = agentRunMergeCodexBackend(profile.Codex, run.Codex)
	out.HermesAgent = agentRunMergeHermesBackend(profile.HermesAgent, run.HermesAgent)
	out.OpenClaw = agentRunMergeOpenClawBackend(profile.OpenClaw, run.OpenClaw)
	out.GrokBuild = agentRunMergeGrokBuildBackend(profile.GrokBuild, run.GrokBuild)
	out.PiAgent = agentRunMergePiBackend(profile.PiAgent, run.PiAgent)
	out.Custom = agentRunMergeCustomBackend(profile.Custom, run.Custom)
	return out
}

func agentRunMergeCodexBackend(profile, run *controlv1alpha1.AgentRunCodexBackendSpec) *controlv1alpha1.AgentRunCodexBackendSpec {
	if profile == nil && run == nil {
		return nil
	}
	out := &controlv1alpha1.AgentRunCodexBackendSpec{}
	if profile != nil {
		out = profile.DeepCopy()
	}
	if run == nil {
		return out
	}
	if strings.TrimSpace(run.Model) != "" {
		out.Model = run.Model
	}
	if strings.TrimSpace(run.ReasoningEffort) != "" {
		out.ReasoningEffort = run.ReasoningEffort
	}
	if strings.TrimSpace(run.Verbosity) != "" {
		out.Verbosity = run.Verbosity
	}
	if strings.TrimSpace(run.ServiceTier) != "" {
		out.ServiceTier = run.ServiceTier
	}
	if run.GoalMode {
		out.GoalMode = true
	}
	if strings.TrimSpace(run.Goal) != "" {
		out.Goal = run.Goal
	}
	if strings.TrimSpace(run.Sandbox) != "" {
		out.Sandbox = run.Sandbox
	}
	out.AdditionalArgs = append(out.AdditionalArgs, run.AdditionalArgs...)
	return out
}

func agentRunMergeHermesBackend(profile, run *controlv1alpha1.AgentRunHermesBackendSpec) *controlv1alpha1.AgentRunHermesBackendSpec {
	if profile == nil && run == nil {
		return nil
	}
	out := &controlv1alpha1.AgentRunHermesBackendSpec{}
	if profile != nil {
		out = profile.DeepCopy()
	}
	if run == nil {
		return out
	}
	if strings.TrimSpace(run.Model) != "" {
		out.Model = run.Model
	}
	if strings.TrimSpace(run.ReasoningEffort) != "" {
		out.ReasoningEffort = run.ReasoningEffort
	}
	if strings.TrimSpace(run.ServiceTier) != "" {
		out.ServiceTier = run.ServiceTier
	}
	if strings.TrimSpace(run.Profile) != "" {
		out.Profile = run.Profile
	}
	if run.UseCodexAppServer {
		out.UseCodexAppServer = true
	}
	out.AdditionalArgs = append(out.AdditionalArgs, run.AdditionalArgs...)
	return out
}

func agentRunMergeOpenClawBackend(profile, run *controlv1alpha1.AgentRunOpenClawBackendSpec) *controlv1alpha1.AgentRunOpenClawBackendSpec {
	if profile == nil && run == nil {
		return nil
	}
	out := &controlv1alpha1.AgentRunOpenClawBackendSpec{}
	if profile != nil {
		out = profile.DeepCopy()
	}
	if run == nil {
		return out
	}
	if strings.TrimSpace(run.AgentID) != "" {
		out.AgentID = run.AgentID
	}
	if strings.TrimSpace(run.Model) != "" {
		out.Model = run.Model
	}
	if strings.TrimSpace(run.Thinking) != "" {
		out.Thinking = run.Thinking
	}
	if strings.TrimSpace(run.ServiceTier) != "" {
		out.ServiceTier = run.ServiceTier
	}
	if run.Local != nil {
		local := *run.Local
		out.Local = &local
	}
	out.AdditionalArgs = append(out.AdditionalArgs, run.AdditionalArgs...)
	return out
}

func agentRunMergeGrokBuildBackend(profile, run *controlv1alpha1.AgentRunGrokBuildBackendSpec) *controlv1alpha1.AgentRunGrokBuildBackendSpec {
	if profile == nil && run == nil {
		return nil
	}
	out := &controlv1alpha1.AgentRunGrokBuildBackendSpec{}
	if profile != nil {
		out = profile.DeepCopy()
	}
	if run == nil {
		return out
	}
	if strings.TrimSpace(run.Model) != "" {
		out.Model = run.Model
	}
	if strings.TrimSpace(run.ReasoningEffort) != "" {
		out.ReasoningEffort = run.ReasoningEffort
	}
	if strings.TrimSpace(run.ServiceTier) != "" {
		out.ServiceTier = run.ServiceTier
	}
	if strings.TrimSpace(run.Profile) != "" {
		out.Profile = run.Profile
	}
	if strings.TrimSpace(run.Command) != "" {
		out.Command = run.Command
	}
	out.AdditionalArgs = append(out.AdditionalArgs, run.AdditionalArgs...)
	return out
}

func agentRunMergePiBackend(profile, run *controlv1alpha1.AgentRunPiBackendSpec) *controlv1alpha1.AgentRunPiBackendSpec {
	if profile == nil && run == nil {
		return nil
	}
	out := &controlv1alpha1.AgentRunPiBackendSpec{}
	if profile != nil {
		out = profile.DeepCopy()
	}
	if run == nil {
		return out
	}
	if strings.TrimSpace(run.Provider) != "" {
		out.Provider = run.Provider
	}
	if strings.TrimSpace(run.Model) != "" {
		out.Model = run.Model
	}
	if strings.TrimSpace(run.Thinking) != "" {
		out.Thinking = run.Thinking
	}
	if strings.TrimSpace(run.Mode) != "" {
		out.Mode = run.Mode
	}
	if run.NoSession {
		out.NoSession = true
	}
	out.AdditionalArgs = append(out.AdditionalArgs, run.AdditionalArgs...)
	return out
}

func agentRunMergeCustomBackend(profile, run *controlv1alpha1.AgentRunCustomBackendSpec) *controlv1alpha1.AgentRunCustomBackendSpec {
	if profile == nil && run == nil {
		return nil
	}
	out := &controlv1alpha1.AgentRunCustomBackendSpec{}
	if profile != nil {
		out = profile.DeepCopy()
	}
	if run == nil {
		return out
	}
	if len(run.Command) > 0 {
		out.Command = append([]string(nil), run.Command...)
	}
	if len(run.Args) > 0 {
		out.Args = append([]string(nil), run.Args...)
	}
	return out
}

func agentRunMergeExecution(profile, run controlv1alpha1.AgentRunHarnessExecutionSpec) controlv1alpha1.AgentRunHarnessExecutionSpec {
	out := *profile.DeepCopy()
	if strings.TrimSpace(run.ServiceAccountName) != "" {
		out.ServiceAccountName = run.ServiceAccountName
	}
	out.EnvSecretRefs = mergeNamespacedRefs(out.EnvSecretRefs, run.EnvSecretRefs)
	out.ExternalSecretRefreshRefs = mergeAgentRunExternalSecretRefreshRefs(out.ExternalSecretRefreshRefs, run.ExternalSecretRefreshRefs)
	out.ExtraEnv = mergeEnvVars(out.ExtraEnv, run.ExtraEnv)
	out.DataVolumeRefs = mergeAgentRunDataVolumeRefs(out.DataVolumeRefs, run.DataVolumeRefs)
	if run.SpiffeWorkloadAPI.Enabled || strings.TrimSpace(run.SpiffeWorkloadAPI.SPIFFEID) != "" {
		out.SpiffeWorkloadAPI = run.SpiffeWorkloadAPI
	}
	if len(run.Resources.Requests) > 0 || len(run.Resources.Limits) > 0 || len(run.Resources.Claims) > 0 {
		out.Resources = *run.Resources.DeepCopy()
	}
	out.NodeSelector = mergeStringMap(out.NodeSelector, run.NodeSelector)
	if run.Affinity != nil {
		out.Affinity = run.Affinity.DeepCopy()
	}
	out.Tolerations = append(out.Tolerations, run.Tolerations...)
	out.ImagePullSecrets = mergeLocalObjectRefs(out.ImagePullSecrets, run.ImagePullSecrets)
	if run.PodSecurityContext != nil {
		out.PodSecurityContext = run.PodSecurityContext.DeepCopy()
	}
	if run.SecurityContext != nil {
		out.SecurityContext = run.SecurityContext.DeepCopy()
	}
	if strings.TrimSpace(run.Workdir) != "" {
		out.Workdir = run.Workdir
	}
	if run.TimeoutSeconds != 0 {
		out.TimeoutSeconds = run.TimeoutSeconds
	}
	if run.TTLSecondsAfterFinished != nil {
		value := *run.TTLSecondsAfterFinished
		out.TTLSecondsAfterFinished = &value
	}
	return out
}

func mergeAgentRunExternalSecretRefreshRefs(profile, run []controlv1alpha1.AgentRunExternalSecretRefreshRef) []controlv1alpha1.AgentRunExternalSecretRefreshRef {
	out := make([]controlv1alpha1.AgentRunExternalSecretRefreshRef, 0, len(profile)+len(run))
	seen := map[string]struct{}{}
	for _, ref := range append(append([]controlv1alpha1.AgentRunExternalSecretRefreshRef(nil), profile...), run...) {
		key := strings.TrimSpace(ref.Name) + "\x00" + strings.TrimSpace(ref.TargetSecretRef.Namespace) + "\x00" + strings.TrimSpace(ref.TargetSecretRef.Name)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, ref)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func appendUniqueStrings(base []string, additions ...string) []string {
	out := make([]string, 0, len(base)+len(additions))
	seen := map[string]struct{}{}
	for _, value := range append(append([]string(nil), base...), additions...) {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func mergePromptText(profile, run string) string {
	profile = strings.TrimSpace(profile)
	run = strings.TrimSpace(run)
	switch {
	case profile == "":
		return run
	case run == "":
		return profile
	default:
		return profile + "\n\n" + run
	}
}

func mergeNamespacedRefs(base, additions []controlv1alpha1.NamespacedObjectReference) []controlv1alpha1.NamespacedObjectReference {
	out := append([]controlv1alpha1.NamespacedObjectReference(nil), base...)
	positions := map[string]int{}
	for i, ref := range out {
		positions[namespacedRefKey(ref.Namespace, ref.Name)] = i
	}
	for _, ref := range additions {
		if strings.TrimSpace(ref.Name) == "" {
			out = append(out, ref)
			continue
		}
		key := namespacedRefKey(ref.Namespace, ref.Name)
		if index, ok := positions[key]; ok {
			out[index] = ref
			continue
		}
		positions[key] = len(out)
		out = append(out, ref)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func namespacedRefKey(namespace, name string) string {
	return strings.TrimSpace(namespace) + "/" + strings.TrimSpace(name)
}

func mergeEnvVars(base, additions []corev1.EnvVar) []corev1.EnvVar {
	out := append([]corev1.EnvVar(nil), base...)
	positions := map[string]int{}
	for i, env := range out {
		if strings.TrimSpace(env.Name) != "" {
			positions[env.Name] = i
		}
	}
	for _, env := range additions {
		if strings.TrimSpace(env.Name) == "" {
			out = append(out, env)
			continue
		}
		if index, ok := positions[env.Name]; ok {
			out[index] = env
			continue
		}
		positions[env.Name] = len(out)
		out = append(out, env)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func mergeAgentRunDataVolumeRefs(base, additions []controlv1alpha1.AgentRunDataVolumeRef) []controlv1alpha1.AgentRunDataVolumeRef {
	out := append([]controlv1alpha1.AgentRunDataVolumeRef(nil), base...)
	positions := map[string]int{}
	for i, ref := range out {
		positions[namespacedRefKey(ref.Namespace, ref.Name)] = i
	}
	for _, ref := range additions {
		if strings.TrimSpace(ref.Name) == "" {
			out = append(out, ref)
			continue
		}
		key := namespacedRefKey(ref.Namespace, ref.Name)
		if index, ok := positions[key]; ok {
			out[index] = ref
			continue
		}
		positions[key] = len(out)
		out = append(out, ref)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func mergeLocalObjectRefs(base, additions []corev1.LocalObjectReference) []corev1.LocalObjectReference {
	out := append([]corev1.LocalObjectReference(nil), base...)
	positions := map[string]int{}
	for i, ref := range out {
		if strings.TrimSpace(ref.Name) != "" {
			positions[ref.Name] = i
		}
	}
	for _, ref := range additions {
		if strings.TrimSpace(ref.Name) == "" {
			out = append(out, ref)
			continue
		}
		if index, ok := positions[ref.Name]; ok {
			out[index] = ref
			continue
		}
		positions[ref.Name] = len(out)
		out = append(out, ref)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func mergeStringMap(base, additions map[string]string) map[string]string {
	out := cloneStringMap(base)
	if len(additions) == 0 {
		return out
	}
	if out == nil {
		out = map[string]string{}
	}
	for key, value := range additions {
		out[key] = value
	}
	return out
}

func (r *AgentRunReconciler) agentRunQueuedBehind(ctx context.Context, obj *controlv1alpha1.AgentRun) (*controlv1alpha1.AgentRun, string, error) {
	blockedBy, err := r.agentRunQueuedBehindSchedule(ctx, obj)
	if err != nil {
		return nil, "", err
	}
	if blockedBy != nil {
		return blockedBy, "QueuedBehindScheduledRun", nil
	}
	blockedBy, err = r.agentRunQueuedBehindApplication(ctx, obj)
	if err != nil {
		return nil, "", err
	}
	if blockedBy != nil {
		return blockedBy, "QueuedBehindApplicationRun", nil
	}
	return nil, "", nil
}

func (r *AgentRunReconciler) agentRunLaunchPaused(ctx context.Context, obj *controlv1alpha1.AgentRun) (bool, string, string, time.Duration, error) {
	return r.agentRunLaunchPausedWithReader(ctx, r.Client, obj)
}

func (r *AgentRunReconciler) agentRunLaunchPausedAuthoritative(ctx context.Context, obj *controlv1alpha1.AgentRun) (bool, string, string, time.Duration, error) {
	reader := client.Reader(r.Client)
	if r.APIReader != nil {
		reader = r.APIReader
	}
	return r.agentRunLaunchPausedWithReader(ctx, reader, obj)
}

func (r *AgentRunReconciler) agentRunLaunchPausedWithReader(ctx context.Context, reader client.Reader, obj *controlv1alpha1.AgentRun) (bool, string, string, time.Duration, error) {
	if obj.Spec.ScheduleRef != nil && strings.TrimSpace(obj.Spec.ScheduleRef.Name) != "" {
		namespace := firstNonEmpty(strings.TrimSpace(obj.Spec.ScheduleRef.Namespace), obj.Namespace)
		schedule := &controlv1alpha1.AgentSchedule{}
		if err := reader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: strings.TrimSpace(obj.Spec.ScheduleRef.Name)}, schedule); err != nil {
			if !apierrors.IsNotFound(err) {
				return false, "", "", 0, fmt.Errorf("get AgentSchedule launch policy: %w", err)
			}
		} else if schedule.Spec.Suspend {
			return true, "ScheduleSuspended", fmt.Sprintf("Waiting because AgentSchedule %s/%s is suspended.", schedule.Namespace, schedule.Name), agentRunPollInterval, nil
		}
	}

	applicationName, err := resolveAgentRunApplicationName(ctx, reader, obj)
	if err != nil {
		return false, "", "", 0, err
	}
	pause, err := activeAgentRunPauseForApplication(ctx, reader, applicationName, time.Now())
	if err != nil {
		return false, "", "", 0, err
	}
	if pause == nil {
		return false, "", "", 0, nil
	}
	message := fmt.Sprintf("Waiting because Application %q is paused by AgentRunControl %q.", applicationName, pause.ControlName)
	if pause.Reason != "" {
		message = fmt.Sprintf("%s Reason: %s", message, pause.Reason)
	}
	requeueAfter := agentRunPollInterval
	if untilExpiry := agentRunPauseRequeueAfter(pause, time.Now()); untilExpiry < requeueAfter {
		requeueAfter = untilExpiry
	}
	return true, "ApplicationPaused", message, requeueAfter, nil
}

func (r *AgentRunReconciler) agentRunQueuedBehindSchedule(ctx context.Context, obj *controlv1alpha1.AgentRun) (*controlv1alpha1.AgentRun, error) {
	if obj.Spec.ScheduleRef == nil || strings.TrimSpace(obj.Spec.ScheduleRef.Name) == "" {
		return nil, nil
	}
	namespace := firstNonEmpty(strings.TrimSpace(obj.Spec.ScheduleRef.Namespace), obj.Namespace)
	schedule := &controlv1alpha1.AgentSchedule{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: strings.TrimSpace(obj.Spec.ScheduleRef.Name)}, schedule); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("get agent schedule for queue policy: %w", err)
	}
	policy := agentScheduleConcurrencyPolicy(schedule)
	if policy == controlv1alpha1.AgentScheduleConcurrencyAllow {
		return nil, nil
	}
	maxConcurrent := agentScheduleMaxConcurrentRuns(schedule)
	if maxConcurrent <= 0 {
		return nil, nil
	}

	list := &controlv1alpha1.AgentRunList{}
	if err := r.List(ctx, list, client.InNamespace(obj.Namespace), client.MatchingLabels{agentRunScheduleLabel: schedule.Name}); err != nil {
		return nil, fmt.Errorf("list queued scheduled agent runs: %w", err)
	}
	var blockedBy *controlv1alpha1.AgentRun
	precedingRuns := 0
	for i := range list.Items {
		run := &list.Items[i]
		if run.Name == obj.Name {
			continue
		}
		if agentRunPhaseTerminal(run.Status.Phase) {
			continue
		}
		if !agentRunPrecedes(run, obj) {
			continue
		}
		precedingRuns++
		if precedingRuns >= maxConcurrent {
			if blockedBy == nil || agentRunPrecedes(run, blockedBy) {
				blockedBy = run
			}
		}
	}
	return blockedBy, nil
}

func (r *AgentRunReconciler) agentRunQueuedBehindApplication(ctx context.Context, obj *controlv1alpha1.AgentRun) (*controlv1alpha1.AgentRun, error) {
	applicationName, err := r.agentRunApplicationName(ctx, obj)
	if err != nil {
		return nil, err
	}
	if applicationName == "" {
		return nil, nil
	}
	maxConcurrent, err := r.agentRunApplicationMaxConcurrentRuns(ctx, applicationName)
	if err != nil {
		return nil, err
	}
	if maxConcurrent <= 0 {
		return nil, nil
	}

	list := &controlv1alpha1.AgentRunList{}
	if err := r.List(ctx, list); err != nil {
		return nil, fmt.Errorf("list application-scoped agent runs: %w", err)
	}
	var blockedBy *controlv1alpha1.AgentRun
	precedingRuns := 0
	for i := range list.Items {
		run := &list.Items[i]
		if run.Namespace == obj.Namespace && run.Name == obj.Name {
			continue
		}
		if agentRunPhaseTerminal(run.Status.Phase) {
			continue
		}
		runApplicationName, err := r.agentRunApplicationName(ctx, run)
		if err != nil {
			return nil, err
		}
		if runApplicationName != applicationName {
			continue
		}
		if !agentRunPrecedes(run, obj) {
			continue
		}
		precedingRuns++
		if precedingRuns >= maxConcurrent {
			if blockedBy == nil || agentRunPrecedes(run, blockedBy) {
				blockedBy = run
			}
		}
	}
	return blockedBy, nil
}

func (r *AgentRunReconciler) agentRunApplicationName(ctx context.Context, obj *controlv1alpha1.AgentRun) (string, error) {
	return resolveAgentRunApplicationName(ctx, r.Client, obj)
}

func (r *AgentRunReconciler) agentRunApplicationMaxConcurrentRuns(ctx context.Context, applicationName string) (int, error) {
	if strings.TrimSpace(applicationName) == "" {
		return 0, nil
	}
	controls := &controlv1alpha1.AgentRunControlList{}
	if err := r.List(ctx, controls); err != nil {
		return 0, fmt.Errorf("list AgentRunControls for application concurrency: %w", err)
	}
	limit := 0
	for i := range controls.Items {
		control := &controls.Items[i]
		candidate := int(control.Spec.MaxConcurrentRuns)
		if strings.TrimSpace(control.Spec.ApplicationRef.Name) != strings.TrimSpace(applicationName) || candidate <= 0 {
			continue
		}
		if limit == 0 || candidate < limit {
			limit = candidate
		}
	}
	if limit > 0 {
		return limit, nil
	}
	if r.Options != nil && r.Options.ApplicationMaxConcurrentRuns > 0 {
		return r.Options.ApplicationMaxConcurrentRuns, nil
	}
	return defaultApplicationConcurrency, nil
}

func agentRunPrecedes(candidate, target *controlv1alpha1.AgentRun) bool {
	candidateTime := candidate.CreationTimestamp.Time
	targetTime := target.CreationTimestamp.Time
	if !candidateTime.IsZero() && !targetTime.IsZero() {
		if candidateTime.Before(targetTime) {
			return true
		}
		if candidateTime.After(targetTime) {
			return false
		}
	}
	return candidate.Name < target.Name
}

func (r *AgentRunReconciler) agentRunValidateGitHubSkillSource(obj *controlv1alpha1.AgentRun, source controlv1alpha1.AgentRunGitHubSkillSourceSpec) (controlv1alpha1.AgentRunPhase, string, string) {
	repository := strings.Split(strings.Trim(strings.TrimSpace(source.Repository), "/"), "/")
	if len(repository) != 2 || strings.TrimSpace(repository[0]) == "" || strings.TrimSpace(repository[1]) == "" {
		return controlv1alpha1.AgentRunPhaseFailed, "InvalidSkillSource", "spec.harness.skillInjections sourceRefs.github.repository must use owner/name form."
	}
	if err := agentRunValidateRemoteFilePath(strings.TrimSpace(source.Path)); err != nil {
		return controlv1alpha1.AgentRunPhaseFailed, "InvalidSkillSource", "spec.harness.skillInjections sourceRefs.github.path must be a repository-relative file path without '..' segments."
	}
	if _, err := r.agentRunGitHubContentsURL(source); err != nil {
		return controlv1alpha1.AgentRunPhaseFailed, "InvalidSkillSource", "spec.harness.skillInjections sourceRefs.github is invalid: " + err.Error()
	}
	if source.TokenSecretRef != nil {
		ref := source.TokenSecretRef
		if strings.TrimSpace(ref.Name) == "" {
			return controlv1alpha1.AgentRunPhaseFailed, "InvalidSkillSourceToken", "spec.harness.skillInjections sourceRefs.github.tokenSecretRef.name is required."
		}
		if strings.TrimSpace(ref.Key) == "" {
			return controlv1alpha1.AgentRunPhaseFailed, "InvalidSkillSourceToken", "spec.harness.skillInjections sourceRefs.github.tokenSecretRef.key is required."
		}
		if namespace := strings.TrimSpace(ref.Namespace); namespace != "" && namespace != obj.Namespace {
			return controlv1alpha1.AgentRunPhaseFailed, "CrossNamespaceSkillSourceToken", "Skill source token Secret refs must be in the agent run namespace."
		}
	}
	return "", "", ""
}

func (r *AgentRunReconciler) agentRunBlockingValidation(obj *controlv1alpha1.AgentRun) (controlv1alpha1.AgentRunPhase, string, string) {
	if strings.TrimSpace(obj.Spec.SourceRef.Kind) == "" {
		return controlv1alpha1.AgentRunPhaseFailed, "MissingSourceKind", "spec.sourceRef.kind is required."
	}
	if strings.TrimSpace(obj.Spec.SourceRef.Name) == "" {
		return controlv1alpha1.AgentRunPhaseFailed, "MissingSourceName", "spec.sourceRef.name is required."
	}
	if obj.Spec.Harness.Execution.TimeoutSeconds < 0 {
		return controlv1alpha1.AgentRunPhaseFailed, "InvalidTimeout", "spec.harness.execution.timeoutSeconds cannot be negative."
	}
	if obj.Spec.Harness.Execution.TTLSecondsAfterFinished != nil && *obj.Spec.Harness.Execution.TTLSecondsAfterFinished < 0 {
		return controlv1alpha1.AgentRunPhaseFailed, "InvalidTTL", "spec.harness.execution.ttlSecondsAfterFinished cannot be negative."
	}
	spiffeWorkloadAPI := obj.Spec.Harness.Execution.SpiffeWorkloadAPI
	if !spiffeWorkloadAPI.Enabled && strings.TrimSpace(spiffeWorkloadAPI.SPIFFEID) != "" {
		return controlv1alpha1.AgentRunPhaseFailed, "InvalidSPIFFEWorkloadAPI", "spec.harness.execution.spiffeWorkloadAPI.spiffeId requires enabled=true."
	}
	if spiffeWorkloadAPI.Enabled {
		if strings.TrimSpace(spiffeWorkloadAPI.SPIFFEID) == "" {
			return controlv1alpha1.AgentRunPhaseFailed, "InvalidSPIFFEWorkloadAPI", "spec.harness.execution.spiffeWorkloadAPI.spiffeId is required when enabled."
		}
		if _, err := spiffeid.FromString(strings.TrimSpace(spiffeWorkloadAPI.SPIFFEID)); err != nil {
			return controlv1alpha1.AgentRunPhaseFailed, "InvalidSPIFFEWorkloadAPI", "spec.harness.execution.spiffeWorkloadAPI.spiffeId must be a valid SPIFFE ID."
		}
	}
	for _, ref := range obj.Spec.Harness.Execution.EnvSecretRefs {
		if strings.TrimSpace(ref.Name) == "" {
			return controlv1alpha1.AgentRunPhaseFailed, "InvalidEnvSecretRef", "spec.harness.execution.envSecretRefs entries must set name."
		}
		if namespace := strings.TrimSpace(ref.Namespace); namespace != "" && namespace != obj.Namespace {
			return controlv1alpha1.AgentRunPhaseFailed, "CrossNamespaceEnvSecretRef", "Kubernetes envFrom secret refs must be in the agent run namespace."
		}
	}
	envSecretRefs := map[string]struct{}{}
	for _, ref := range obj.Spec.Harness.Execution.EnvSecretRefs {
		envSecretRefs[strings.TrimSpace(ref.Name)] = struct{}{}
	}
	for _, ref := range obj.Spec.Harness.Execution.ExternalSecretRefreshRefs {
		name := strings.TrimSpace(ref.Name)
		if name == "" {
			return controlv1alpha1.AgentRunPhaseFailed, "InvalidExternalSecretRefreshRef", "spec.harness.execution.externalSecretRefreshRefs entries must set name."
		}
		targetSecret := strings.TrimSpace(ref.TargetSecretRef.Name)
		if targetSecret == "" {
			return controlv1alpha1.AgentRunPhaseFailed, "InvalidExternalSecretRefreshTarget", "spec.harness.execution.externalSecretRefreshRefs entries must set targetSecretRef.name."
		}
		if namespace := strings.TrimSpace(ref.TargetSecretRef.Namespace); namespace != "" && namespace != obj.Namespace {
			return controlv1alpha1.AgentRunPhaseFailed, "CrossNamespaceExternalSecretRefreshTarget", "ExternalSecret refresh target Secret refs must be in the agent run namespace."
		}
		if _, ok := envSecretRefs[targetSecret]; !ok {
			return controlv1alpha1.AgentRunPhaseFailed, "ExternalSecretRefreshNotInjected", fmt.Sprintf("ExternalSecret refresh target Secret %q must also appear in spec.harness.execution.envSecretRefs.", targetSecret)
		}
	}
	for _, ref := range obj.Spec.Harness.Execution.DataVolumeRefs {
		if strings.TrimSpace(ref.Name) == "" {
			return controlv1alpha1.AgentRunPhaseFailed, "InvalidDataVolumeRef", "spec.harness.execution.dataVolumeRefs entries must set name."
		}
		if namespace := strings.TrimSpace(ref.Namespace); namespace != "" && namespace != obj.Namespace {
			return controlv1alpha1.AgentRunPhaseFailed, "CrossNamespaceDataVolumeRef", "AgentRun dataVolumeRefs must be in the agent run namespace."
		}
	}
	for _, skill := range obj.Spec.Harness.SkillInjections {
		if strings.TrimSpace(skill.Name) == "" {
			return controlv1alpha1.AgentRunPhaseFailed, "InvalidSkillInjection", "spec.harness.skillInjections entries must set name."
		}
		for _, sourceRef := range skill.SourceRefs {
			if sourceRef.GitHub == nil {
				return controlv1alpha1.AgentRunPhaseFailed, "InvalidSkillSource", "spec.harness.skillInjections sourceRefs entries must configure a supported provider."
			}
			if phase, reason, message := r.agentRunValidateGitHubSkillSource(obj, *sourceRef.GitHub); phase != "" {
				return phase, reason, message
			}
		}
	}
	for _, subagent := range obj.Spec.Harness.Subagents {
		if strings.TrimSpace(subagent.Name) == "" {
			return controlv1alpha1.AgentRunPhaseFailed, "InvalidSubagent", "spec.harness.subagents entries must set name."
		}
	}
	for _, tool := range obj.Spec.Harness.Tools {
		if strings.TrimSpace(tool.Name) == "" {
			return controlv1alpha1.AgentRunPhaseFailed, "InvalidTool", "spec.harness.tools entries must set name."
		}
		if len(tool.VerifyCommand) > 0 && strings.TrimSpace(tool.VerifyCommand[0]) == "" {
			return controlv1alpha1.AgentRunPhaseFailed, "InvalidToolVerifyCommand", "spec.harness.tools verifyCommand entries must start with a command."
		}
	}
	switch agentRunBackendKind(obj) {
	case controlv1alpha1.AgentRunHarnessBackendCodex:
		if strings.TrimSpace(agentRunImage(obj)) == "" {
			return controlv1alpha1.AgentRunPhaseNeedsHuman, "CodexImageNotConfigured", "A Codex agent run container image is required."
		}
		return "", "", ""
	case controlv1alpha1.AgentRunHarnessBackendHermesAgent:
		if strings.TrimSpace(agentRunImage(obj)) == "" {
			return controlv1alpha1.AgentRunPhaseNeedsHuman, "HermesAgentImageNotConfigured", "A Hermes AgentRun container image is required."
		}
		return "", "", ""
	case controlv1alpha1.AgentRunHarnessBackendOpenClaw:
		if strings.TrimSpace(agentRunImage(obj)) == "" {
			return controlv1alpha1.AgentRunPhaseNeedsHuman, "OpenClawImageNotConfigured", "An OpenClaw AgentRun container image is required."
		}
		return "", "", ""
	case controlv1alpha1.AgentRunHarnessBackendGrokBuild:
		if strings.TrimSpace(agentRunImage(obj)) == "" {
			return controlv1alpha1.AgentRunPhaseNeedsHuman, "GrokBuildImageNotConfigured", "A Grok Build AgentRun container image is required."
		}
		return "", "", ""
	case controlv1alpha1.AgentRunHarnessBackendPiAgent:
		if strings.TrimSpace(agentRunImage(obj)) == "" {
			return controlv1alpha1.AgentRunPhaseNeedsHuman, "PiAgentImageNotConfigured", "A Pi AgentRun container image is required."
		}
		return "", "", ""
	case controlv1alpha1.AgentRunHarnessBackendCustom:
		if strings.TrimSpace(obj.Spec.Harness.Backend.Image) == "" {
			return controlv1alpha1.AgentRunPhaseNeedsHuman, "CustomImageNotConfigured", "spec.harness.backend.image is required when backend.kind is custom."
		}
		return "", "", ""
	default:
		return controlv1alpha1.AgentRunPhaseFailed, "UnsupportedBackend", fmt.Sprintf("Unsupported agent run harness backend %q.", obj.Spec.Harness.Backend.Kind)
	}
}

func agentRunBlockingValidation(obj *controlv1alpha1.AgentRun) (controlv1alpha1.AgentRunPhase, string, string) {
	reconciler := &AgentRunReconciler{CommonReconcilerOptions: CommonReconcilerOptions{Options: DefaultOptions()}}
	return reconciler.agentRunBlockingValidation(obj)
}

func agentRunBackendKind(obj *controlv1alpha1.AgentRun) controlv1alpha1.AgentRunHarnessBackendKind {
	kind := obj.Spec.Harness.Backend.Kind
	if strings.TrimSpace(string(kind)) == "" {
		return controlv1alpha1.AgentRunHarnessBackendCodex
	}
	return kind
}

func agentRunModelProvider(obj *controlv1alpha1.AgentRun) controlv1alpha1.AgentRunModelProvider {
	provider := obj.Spec.Harness.Backend.ModelProvider
	if strings.TrimSpace(string(provider)) != "" {
		return provider
	}
	if agentRunBackendKind(obj) == controlv1alpha1.AgentRunHarnessBackendGrokBuild ||
		agentRunBackendKind(obj) == controlv1alpha1.AgentRunHarnessBackendPiAgent {
		return controlv1alpha1.AgentRunModelProviderXAI
	}
	return ""
}

func agentRunProviderAuthMode(obj *controlv1alpha1.AgentRun) controlv1alpha1.AgentRunProviderAuthMode {
	authMode := obj.Spec.Harness.Backend.ProviderAuthMode
	if strings.TrimSpace(string(authMode)) != "" {
		return authMode
	}
	return ""
}

func agentRunHermesModelProvider(obj *controlv1alpha1.AgentRun) string {
	provider := agentRunModelProvider(obj)
	if provider == controlv1alpha1.AgentRunModelProviderXAI && agentRunProviderAuthMode(obj) == controlv1alpha1.AgentRunProviderAuthModeOAuth {
		return "xai-oauth"
	}
	return string(provider)
}

func agentRunPiProvider(obj *controlv1alpha1.AgentRun, piAgent *controlv1alpha1.AgentRunPiBackendSpec) string {
	if piAgent != nil {
		if provider := strings.TrimSpace(piAgent.Provider); provider != "" {
			return provider
		}
	}
	provider := agentRunModelProvider(obj)
	if provider == controlv1alpha1.AgentRunModelProviderXAI && agentRunProviderAuthMode(obj) == controlv1alpha1.AgentRunProviderAuthModeOAuth {
		return "xai-auth"
	}
	return string(provider)
}

func agentRunIntent(obj *controlv1alpha1.AgentRun) controlv1alpha1.AgentRunIntent {
	intent := obj.Spec.Harness.Intent
	if strings.TrimSpace(string(intent)) == "" {
		return controlv1alpha1.AgentRunIntentObserve
	}
	return intent
}

func agentRunImage(obj *controlv1alpha1.AgentRun) string {
	if image := strings.TrimSpace(obj.Spec.Harness.Backend.Image); image != "" {
		return image
	}
	switch agentRunBackendKind(obj) {
	case controlv1alpha1.AgentRunHarnessBackendCodex:
		return agentRunDefaultCodexImage
	case controlv1alpha1.AgentRunHarnessBackendHermesAgent:
		return agentRunDefaultHermesAgentImage
	case controlv1alpha1.AgentRunHarnessBackendOpenClaw:
		return agentRunDefaultOpenClawImage
	case controlv1alpha1.AgentRunHarnessBackendGrokBuild:
		return agentRunDefaultGrokBuildImage
	case controlv1alpha1.AgentRunHarnessBackendPiAgent:
		return agentRunDefaultPiAgentImage
	default:
		return ""
	}
}

func agentRunImagePullPolicy(obj *controlv1alpha1.AgentRun) corev1.PullPolicy {
	if obj.Spec.Harness.Backend.ImagePullPolicy != "" {
		return obj.Spec.Harness.Backend.ImagePullPolicy
	}
	return corev1.PullIfNotPresent
}

func agentRunPodSecurityContext(obj *controlv1alpha1.AgentRun) *corev1.PodSecurityContext {
	out := &corev1.PodSecurityContext{}
	if obj.Spec.Harness.Execution.PodSecurityContext != nil {
		out = obj.Spec.Harness.Execution.PodSecurityContext.DeepCopy()
	}
	if out.RunAsNonRoot == nil {
		out.RunAsNonRoot = boolPtr(true)
	}
	if out.SeccompProfile == nil {
		out.SeccompProfile = &corev1.SeccompProfile{
			Type: corev1.SeccompProfileTypeRuntimeDefault,
		}
	}
	return out
}

func agentRunContainerSecurityContext(obj *controlv1alpha1.AgentRun) *corev1.SecurityContext {
	out := &corev1.SecurityContext{}
	if obj.Spec.Harness.Execution.SecurityContext != nil {
		out = obj.Spec.Harness.Execution.SecurityContext.DeepCopy()
	}
	if out.AllowPrivilegeEscalation == nil {
		out.AllowPrivilegeEscalation = boolPtr(false)
	}
	if out.RunAsNonRoot == nil {
		out.RunAsNonRoot = boolPtr(true)
	}
	if out.Capabilities == nil {
		out.Capabilities = &corev1.Capabilities{}
	}
	if !agentRunDropsCapability(out.Capabilities.Drop, "ALL") {
		out.Capabilities.Drop = append(out.Capabilities.Drop, "ALL")
	}
	return out
}

func agentRunDropsCapability(values []corev1.Capability, want corev1.Capability) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func agentRunCodexSandbox(obj *controlv1alpha1.AgentRun, codex *controlv1alpha1.AgentRunCodexBackendSpec) string {
	if sandbox := strings.TrimSpace(codex.Sandbox); sandbox != "" {
		return sandbox
	}
	if agentRunIntent(obj) == controlv1alpha1.AgentRunIntentObserve {
		return "read-only"
	}
	return "workspace-write"
}

func agentRunPhaseTerminal(phase controlv1alpha1.AgentRunPhase) bool {
	switch phase {
	case controlv1alpha1.AgentRunPhaseSucceeded, controlv1alpha1.AgentRunPhaseFailed, controlv1alpha1.AgentRunPhaseNeedsHuman:
		return true
	default:
		return false
	}
}

func buildAgentRunPrompt(obj *controlv1alpha1.AgentRun) string {
	source, _ := json.MarshalIndent(obj.Spec, "", "  ")
	parts := []string{
		agentRunSystemPrompt(),
		"",
		"AgentRun spec JSON:",
		string(source),
	}
	if strings.TrimSpace(obj.Spec.Prompt) != "" {
		parts = append(parts, "", "Run prompt:", obj.Spec.Prompt)
	}
	if strings.TrimSpace(obj.Spec.Harness.SystemPrompt) != "" {
		parts = append(parts, "", "Standing operator instructions:", obj.Spec.Harness.SystemPrompt)
	}
	return strings.Join(parts, "\n")
}

func (r *AgentRunReconciler) agentRunContextJSON(ctx context.Context, obj *controlv1alpha1.AgentRun) ([]byte, error) {
	platform := r.platformContext()
	payload := map[string]any{
		"apiVersion": controlv1alpha1.GroupVersion.String(),
		"kind":       "AgentRun",
		"metadata": map[string]any{
			"name":       obj.Name,
			"namespace":  obj.Namespace,
			"generation": obj.Generation,
			"uid":        obj.UID,
		},
		"spec": obj.Spec,
		"platformContext": map[string]any{
			"repository":    platform.Repository,
			"repositoryURL": platform.RepositoryURL,
			"docsPaths":     append([]string(nil), platform.DocsPaths...),
			"scopeRule":     "Use this anvil-agents context when evidence points at AgentRun, AgentSchedule, AgentRunProfile, AdverseSituation, backend adapters, harness prompts, data volumes, RBAC, images, or controller behavior. Product behavior remains owned by the opaque application scope.",
		},
	}
	if obj.Spec.SituationRef != nil && strings.TrimSpace(obj.Spec.SituationRef.Name) != "" {
		namespace := firstNonEmpty(strings.TrimSpace(obj.Spec.SituationRef.Namespace), obj.Namespace)
		situation := &controlv1alpha1.AdverseSituation{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: strings.TrimSpace(obj.Spec.SituationRef.Name)}, situation); err == nil {
			payload["adverseSituation"] = situation
		}
	}
	if obj.Spec.ScheduleRef != nil && strings.TrimSpace(obj.Spec.ScheduleRef.Name) != "" {
		namespace := firstNonEmpty(strings.TrimSpace(obj.Spec.ScheduleRef.Namespace), obj.Namespace)
		schedule := &controlv1alpha1.AgentSchedule{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: strings.TrimSpace(obj.Spec.ScheduleRef.Name)}, schedule); err == nil {
			payload["agentSchedule"] = schedule
		}
	}
	if obj.Spec.ProfileRef != nil && strings.TrimSpace(obj.Spec.ProfileRef.Name) != "" {
		namespace := firstNonEmpty(strings.TrimSpace(obj.Spec.ProfileRef.Namespace), obj.Namespace)
		profile := &controlv1alpha1.AgentRunProfile{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: strings.TrimSpace(obj.Spec.ProfileRef.Name)}, profile); err == nil {
			payload["agentRunProfile"] = profile
		}
	}
	return json.MarshalIndent(payload, "", "  ")
}

type agentRunPlatformContext struct {
	Repository    string
	RepositoryURL string
	DocsPaths     []string
}

func (r *AgentRunReconciler) platformContext() agentRunPlatformContext {
	options := r.Options
	if options == nil {
		options = DefaultOptions()
	}
	return agentRunPlatformContext{
		Repository:    strings.TrimSpace(options.PlatformRepository),
		RepositoryURL: strings.TrimSpace(options.PlatformRepositoryURL),
		DocsPaths:     append([]string(nil), options.PlatformDocs...),
	}
}

func agentRunTriggerForSource(obj *unstructured.Unstructured) (controlv1alpha1.AgentRunTriggerSnapshot, bool) {
	statusMap, err := extractStatusMap(obj)
	if err != nil {
		return controlv1alpha1.AgentRunTriggerSnapshot{}, false
	}
	observedGeneration := int64FromMap(statusMap, "observedGeneration")
	if observedGeneration == 0 || observedGeneration != obj.GetGeneration() {
		return controlv1alpha1.AgentRunTriggerSnapshot{}, false
	}

	phase, _ := statusMap["phase"].(string)
	conditions, _ := extractConditionsFromStatusMap(statusMap)
	now := metav1.Now()
	trigger := controlv1alpha1.AgentRunTriggerSnapshot{
		Phase:              strings.TrimSpace(phase),
		ObservedGeneration: observedGeneration,
		ResourceVersion:    obj.GetResourceVersion(),
		DetectedAt:         &now,
	}

	if condition := agentRunNegativeCondition(conditions, phase); condition != nil {
		trigger.ConditionType = condition.Type
		trigger.ConditionStatus = condition.Status
		trigger.Reason = condition.Reason
		trigger.Message = condition.Message
		trigger.ObservedGeneration = firstNonZero(condition.ObservedGeneration, observedGeneration)
		return trigger, true
	}
	if agentRunNegativePhase(phase) {
		trigger.Reason = obj.GetKind() + "NegativePhase"
		trigger.Message = fmt.Sprintf("%s %s/%s reported phase %s.", obj.GetKind(), obj.GetNamespace(), obj.GetName(), strings.TrimSpace(phase))
		return trigger, true
	}
	return controlv1alpha1.AgentRunTriggerSnapshot{}, false
}

func agentRunNegativeCondition(conditions []metav1.Condition, phase string) *metav1.Condition {
	for i := range conditions {
		condition := conditions[i]
		if condition.Status != metav1.ConditionTrue {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(condition.Type)) {
		case "failed", "needshuman", "actionrequired":
			return &condition
		}
	}
	if !agentRunNegativePhase(phase) {
		return nil
	}
	for i := range conditions {
		condition := conditions[i]
		if strings.EqualFold(condition.Type, "Ready") && condition.Status == metav1.ConditionFalse {
			return &condition
		}
	}
	return nil
}

func agentRunNegativePhase(phase string) bool {
	switch strings.ToLower(strings.TrimSpace(phase)) {
	case "failed", "error", "needshuman", "actionrequired":
		return true
	default:
		return false
	}
}

func (r *AgentRunReconciler) deleteAgentRunJobAfterLaunchFailure(ctx context.Context, job *batchv1.Job) error {
	if job == nil || !job.GetDeletionTimestamp().IsZero() {
		return nil
	}
	propagation := metav1.DeletePropagationBackground
	return client.IgnoreNotFound(r.Delete(ctx, job, client.PropagationPolicy(propagation)))
}

func (r *AgentRunReconciler) findAgentRunRunnerPod(ctx context.Context, namespace, jobName string) (*controlv1alpha1.NamespacedObjectReference, *corev1.Pod, error) {
	podList := &corev1.PodList{}
	if err := r.List(ctx, podList, client.InNamespace(namespace), client.MatchingLabels{agentRunJobLabel: jobName}); err != nil {
		return nil, nil, fmt.Errorf("list agent run pods: %w", err)
	}
	if len(podList.Items) == 0 {
		return nil, nil, nil
	}
	selected := &podList.Items[0]
	for i := 1; i < len(podList.Items); i++ {
		if podList.Items[i].CreationTimestamp.After(selected.CreationTimestamp.Time) {
			selected = &podList.Items[i]
		}
	}
	return &controlv1alpha1.NamespacedObjectReference{Name: selected.Name, Namespace: selected.Namespace}, selected, nil
}

func (r *AgentRunReconciler) readAgentRunRunnerLogs(ctx context.Context, namespace, pod string) (string, error) {
	if r.ReadPodLogs != nil {
		return r.ReadPodLogs(ctx, namespace, pod)
	}
	if r.RESTConfig == nil {
		return "", fmt.Errorf("kubernetes rest config is required to read pod logs")
	}
	clientset, err := kubernetes.NewForConfig(r.RESTConfig)
	if err != nil {
		return "", fmt.Errorf("build kubernetes clientset for pod logs: %w", err)
	}
	stream, err := clientset.CoreV1().Pods(namespace).GetLogs(pod, &corev1.PodLogOptions{
		Container:  agentRunContainerName,
		TailLines:  int64Ptr(agentRunPodLogTailLines),
		LimitBytes: int64Ptr(agentRunPodLogMaxBytes),
	}).Stream(ctx)
	if err != nil {
		return "", fmt.Errorf("stream pod logs %s/%s: %w", namespace, pod, err)
	}
	defer stream.Close()
	body, err := io.ReadAll(io.LimitReader(stream, agentRunPodLogMaxBytes))
	if err != nil {
		return "", fmt.Errorf("read pod logs %s/%s: %w", namespace, pod, err)
	}
	return string(body), nil
}

func int64Ptr(value int64) *int64 {
	return &value
}

func agentRunJobComplete(job *batchv1.Job) bool {
	if job == nil {
		return false
	}
	if job.Status.Succeeded > 0 {
		return true
	}
	for _, condition := range job.Status.Conditions {
		if condition.Type == batchv1.JobComplete && condition.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func agentRunJobFailed(job *batchv1.Job) bool {
	if job == nil {
		return false
	}
	for _, condition := range job.Status.Conditions {
		if condition.Type == batchv1.JobFailed && condition.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func agentRunPodLaunchFailureMessage(pod *corev1.Pod) string {
	if pod == nil {
		return ""
	}
	for _, status := range append(pod.Status.InitContainerStatuses, pod.Status.ContainerStatuses...) {
		if status.State.Waiting == nil || !agentRunContainerWaitingIsLaunchFailure(status.State.Waiting.Reason) {
			continue
		}
		detail := strings.TrimSpace(status.State.Waiting.Message)
		if detail == "" {
			detail = strings.TrimSpace(status.State.Waiting.Reason)
		}
		return fmt.Sprintf("Agent run harness pod %s/%s cannot start container %q: %s", pod.Namespace, pod.Name, status.Name, detail)
	}
	return ""
}

func agentRunContainerWaitingIsLaunchFailure(reason string) bool {
	switch strings.TrimSpace(reason) {
	case "ErrImagePull", "ImagePullBackOff", "InvalidImageName":
		return true
	default:
		return false
	}
}

func agentRunOutputSummary(output string) string {
	output = strings.TrimSpace(output)
	if output == "" {
		return "Agent run harness completed."
	}
	lines := strings.Split(output, "\n")
	compact := make([]string, 0, 4)
	for i := len(lines) - 1; i >= 0 && len(compact) < 4; i-- {
		line := strings.TrimSpace(lines[i])
		if line != "" {
			compact = append([]string{line}, compact...)
		}
	}
	summary := strings.Join(compact, " ")
	if len(summary) > 300 {
		return summary[:300]
	}
	return summary
}

func agentRunRawResult(output, pullRequestURL string, decision *controlv1alpha1.AgentRunDecisionStatus, reports []controlv1alpha1.AgentRunStatusReport) runtime.RawExtension {
	raw, err := json.Marshal(map[string]any{
		"output":         strings.TrimSpace(output),
		"pullRequestURL": strings.TrimSpace(pullRequestURL),
		"decision":       decision,
		"reports":        reports,
	})
	if err != nil {
		return runtime.RawExtension{}
	}
	return runtime.RawExtension{Raw: raw}
}

func agentRunStatusReportsFromOutput(output string) []controlv1alpha1.AgentRunStatusReport {
	scanner := bufio.NewScanner(strings.NewReader(output))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	reports := []controlv1alpha1.AgentRunStatusReport{}
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		index := strings.Index(line, agentRunStatusLinePrefix)
		if index < 0 {
			continue
		}
		raw := strings.TrimSpace(line[index+len(agentRunStatusLinePrefix):])
		if raw == "" {
			continue
		}
		var report controlv1alpha1.AgentRunStatusReport
		if err := json.Unmarshal([]byte(raw), &report); err != nil {
			continue
		}
		report = agentRunNormalizeStatusReport(report)
		if report == (controlv1alpha1.AgentRunStatusReport{}) {
			continue
		}
		reports = append(reports, report)
	}
	return agentRunTrimStatusReports(reports)
}

func agentRunApplyStatusReports(status *controlv1alpha1.AgentRunStatus, reports []controlv1alpha1.AgentRunStatusReport) {
	if status == nil || len(reports) == 0 {
		return
	}
	status.Reports = reports
	for _, report := range reports {
		if strings.TrimSpace(report.PullRequestURL) != "" {
			status.PullRequestURL = strings.TrimSpace(report.PullRequestURL)
		}
		if agentRunReportHasDecision(report) {
			status.Decision = &controlv1alpha1.AgentRunDecisionStatus{
				Classification: strings.TrimSpace(report.Classification),
				Action:         strings.TrimSpace(report.Action),
				Summary:        strings.TrimSpace(report.Summary),
				ResidualRisk:   strings.TrimSpace(report.ResidualRisk),
			}
		}
	}
}

func agentRunNormalizeStatusReport(report controlv1alpha1.AgentRunStatusReport) controlv1alpha1.AgentRunStatusReport {
	report.Type = agentRunLimitString(strings.TrimSpace(report.Type), 64)
	report.Level = agentRunLimitString(strings.TrimSpace(report.Level), 32)
	report.Stage = agentRunLimitString(strings.TrimSpace(report.Stage), 96)
	report.Classification = agentRunLimitString(strings.TrimSpace(report.Classification), 128)
	report.Action = agentRunLimitString(strings.TrimSpace(report.Action), 128)
	report.Summary = agentRunLimitString(strings.TrimSpace(report.Summary), 1000)
	report.Detail = agentRunLimitString(strings.TrimSpace(report.Detail), 4000)
	report.PullRequestURL = agentRunLimitString(strings.TrimSpace(report.PullRequestURL), 500)
	report.ResidualRisk = agentRunLimitString(strings.TrimSpace(report.ResidualRisk), 1000)
	report.HumanFollowUp = agentRunLimitString(strings.TrimSpace(report.HumanFollowUp), 1000)
	if report.Type == "" && report.Level == "" && report.Stage == "" && report.Classification == "" && report.Action == "" && report.Summary == "" && report.Detail == "" && report.PullRequestURL == "" && report.ResidualRisk == "" && report.HumanFollowUp == "" && !report.NeedsHuman {
		return controlv1alpha1.AgentRunStatusReport{}
	}
	return report
}

func agentRunReportHasDecision(report controlv1alpha1.AgentRunStatusReport) bool {
	if strings.EqualFold(strings.TrimSpace(report.Type), "decision") {
		return true
	}
	return strings.TrimSpace(report.Classification) != "" ||
		strings.TrimSpace(report.Action) != "" ||
		strings.TrimSpace(report.ResidualRisk) != "" ||
		report.NeedsHuman
}

func agentRunReportsNeedHuman(reports []controlv1alpha1.AgentRunStatusReport) bool {
	for _, report := range reports {
		if report.NeedsHuman {
			return true
		}
		if strings.EqualFold(strings.TrimSpace(report.Type), "needsHuman") {
			return true
		}
	}
	return false
}

func agentRunLatestHumanFollowUp(reports []controlv1alpha1.AgentRunStatusReport) string {
	for i := len(reports) - 1; i >= 0; i-- {
		if text := strings.TrimSpace(reports[i].HumanFollowUp); text != "" {
			return text
		}
	}
	return ""
}

func agentRunTrimStatusReports(reports []controlv1alpha1.AgentRunStatusReport) []controlv1alpha1.AgentRunStatusReport {
	const maxReports = 25
	if len(reports) <= maxReports {
		return reports
	}
	trimmed := append([]controlv1alpha1.AgentRunStatusReport(nil), reports[len(reports)-maxReports:]...)
	for _, report := range trimmed {
		if agentRunReportHasDecision(report) {
			return trimmed
		}
	}
	for i := len(reports) - maxReports - 1; i >= 0; i-- {
		if agentRunReportHasDecision(reports[i]) {
			trimmed[0] = reports[i]
			return trimmed
		}
	}
	return trimmed
}

func agentRunLimitString(value string, max int) string {
	if max <= 0 || len(value) <= max {
		return value
	}
	return value[:max]
}

func agentRunTrimOutput(output string) string {
	output = strings.TrimSpace(output)
	const maxOutputBytes = 32 * 1024
	if len(output) <= maxOutputBytes {
		return output
	}
	return output[len(output)-maxOutputBytes:]
}

func agentRunLabels(obj *controlv1alpha1.AgentRun, jobName string) map[string]string {
	labels := map[string]string{
		agentRunLabel:           sanitizeLabelValue(obj.Name),
		agentRunLabelSourceKind: sanitizeLabelValue(obj.Spec.SourceRef.Kind),
		agentRunLabelSourceName: sanitizeLabelValue(obj.Spec.SourceRef.Name),
	}
	if obj.Spec.SituationRef != nil {
		labels[adverseSituationLabel] = sanitizeLabelValue(obj.Spec.SituationRef.Name)
	}
	if obj.Spec.ScheduleRef != nil {
		labels[agentRunScheduleLabel] = sanitizeLabelValue(obj.Spec.ScheduleRef.Name)
	}
	if strings.TrimSpace(jobName) != "" {
		labels[agentRunJobLabel] = sanitizeLabelValue(jobName)
	}
	if obj.Spec.Harness.Execution.SpiffeWorkloadAPI.Enabled {
		labels[agentRunLabelSpiffeWorkloadAPI] = "true"
		if serviceAccountName := strings.TrimSpace(obj.Spec.Harness.Execution.ServiceAccountName); serviceAccountName != "" {
			labels[agentRunLabelServiceAccount] = sanitizeLabelValue(serviceAccountName)
		}
	}
	return labels
}

func int64FromMap(values map[string]any, key string) int64 {
	switch value := values[key].(type) {
	case int64:
		return value
	case int:
		return int64(value)
	case int32:
		return int64(value)
	case float64:
		return int64(value)
	case json.Number:
		out, _ := value.Int64()
		return out
	default:
		return 0
	}
}

func firstNonZero(values ...int64) int64 {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func agentRunChildName(parts ...string) string {
	tokens := make([]string, 0, len(parts))
	for _, part := range parts {
		token := sanitizeDNSLabel(part)
		if token != "" {
			tokens = append(tokens, token)
		}
	}
	name := strings.Join(tokens, "-")
	if len(name) <= 63 {
		return name
	}
	suffix := tokens[len(tokens)-1]
	prefixMax := 63 - len(suffix) - 1
	prefix := strings.Trim(strings.TrimRight(name[:prefixMax], "-"), "-")
	if prefix == "" {
		return suffix
	}
	return prefix + "-" + suffix
}

func sanitizeDNSLabel(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return ""
	}
	var builder strings.Builder
	lastDash := false
	for _, r := range value {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if ok {
			builder.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			builder.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(builder.String(), "-")
}

func sanitizeLabelValue(value string) string {
	value = sanitizeDNSLabel(value)
	if len(value) > 63 {
		return value[:63]
	}
	return value
}

func shortHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:12]
}
