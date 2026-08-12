package controller

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	controlv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

const (
	agentChainReady               = "Ready"
	agentChainPollInterval        = 30 * time.Second
	agentRunChainLabel            = "control.anvil.hazyforge.io/agent-chain"
	agentRunChainInstLabel        = "control.anvil.hazyforge.io/agent-chain-instance"
	agentRunChainStepLabel        = "control.anvil.hazyforge.io/agent-chain-step"
	agentRunChainDigestAnnotation = "control.anvil.hazyforge.io/agent-chain-workflow-digest"
	agentChainHandoffMax          = 8192
)

var errAgentChainRunProvenance = errors.New("AgentRun provenance mismatch")

func agentChainClearActive(status *controlv1alpha1.AgentChainStatus) {
	status.ActiveInstanceID = ""
	status.ActiveStep = ""
	status.ActiveRunRef = nil
	status.ActiveRunUID = ""
	status.ActiveSourceGeneration = 0
	status.ActiveWorkflowDigest = ""
	status.CancelRequestedInstanceID = ""
}

// +kubebuilder:rbac:groups="control.anvil.hazyforge.io",resources=agentchains,verbs=create;delete;get;list;patch;update;watch
// +kubebuilder:rbac:groups="control.anvil.hazyforge.io",resources=agentchains/status,verbs=get;patch;update
// +kubebuilder:rbac:groups="control.anvil.hazyforge.io",resources=agentchains/finalizers,verbs=update
// +kubebuilder:rbac:groups="control.anvil.hazyforge.io",resources=agentruns,verbs=create;get;list;patch;update;watch
type AgentChainReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

func (r *AgentChainReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	obj := &controlv1alpha1.AgentChain{}
	if err := r.Get(ctx, req.NamespacedName, obj); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !obj.GetDeletionTimestamp().IsZero() {
		return ctrl.Result{}, nil
	}

	original := obj.DeepCopy()
	status := obj.Status
	status.ObservedGeneration = obj.Generation
	now := metav1.Now()

	if err := validateAgentChainSpec(obj); err != nil {
		status.Phase = controlv1alpha1.AgentChainPhaseBlocked
		status.LastError = err.Error()
		// Invalid desired state must not release Forbid ownership while an
		// already-created child may still be running. Retain the frozen active
		// instance until the spec is valid again and the exact child is drained.
		status.NextStartAt = nil
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               agentChainReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: obj.Generation,
			LastTransitionTime: now,
			Reason:             "InvalidSpec",
			Message:            status.LastError,
		})
		obj.Status = status
		return r.patchAgentChainStatus(ctx, original, obj, 0)
	}

	if obj.Spec.Suspend {
		status.Phase = controlv1alpha1.AgentChainPhaseSuspended
		// Keep NextStartAt so resume does not immediately fire a due interval.
		status.LastError = ""
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               agentChainReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: obj.Generation,
			LastTransitionTime: now,
			Reason:             "Suspended",
			Message:            "AgentChain is suspended.",
		})
		obj.Status = status
		return r.patchAgentChainStatus(ctx, original, obj, 0)
	}

	applicationName, err := resolveAgentChainApplicationName(obj)
	if err != nil {
		status.Phase = controlv1alpha1.AgentChainPhaseBlocked
		status.LastError = err.Error()
		status.NextStartAt = nil
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               agentChainReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: obj.Generation,
			LastTransitionTime: now,
			Reason:             "InvalidApplicationIdentity",
			Message:            status.LastError,
		})
		obj.Status = status
		return r.patchAgentChainStatus(ctx, original, obj, agentChainPollInterval)
	}

	pause, err := activeAgentRunPauseForApplication(ctx, r.Client, applicationName, now.Time)
	if err != nil {
		return ctrl.Result{}, err
	}
	if pause != nil {
		status.Phase = controlv1alpha1.AgentChainPhaseSuspended
		// Keep NextStartAt across application pause (same as suspend).
		status.LastError = ""
		message := fmt.Sprintf("Application %q is paused by AgentRunControl %q.", applicationName, pause.ControlName)
		if pause.Reason != "" {
			message = fmt.Sprintf("%s Reason: %s", message, pause.Reason)
		}
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               agentChainReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: obj.Generation,
			LastTransitionTime: now,
			Reason:             "ApplicationPaused",
			Message:            message,
		})
		obj.Status = status
		return r.patchAgentChainStatus(ctx, original, obj, agentChainPollInterval)
	}

	runs, instancesToday, err := r.agentChainRuns(ctx, obj, now.Time)
	if err != nil {
		return ctrl.Result{}, err
	}
	status.InstancesToday = instancesToday

	// Recover Forbid ownership after a create-success/status-patch-loss window.
	// A new start token or later interval must never overlap a provenance-valid
	// nonterminal child merely because status was not persisted.
	if status.ActiveInstanceID == "" {
		activeRuns, activeListErr := r.agentChainOwnedNonterminalRuns(ctx, obj)
		if activeListErr != nil {
			return ctrl.Result{}, activeListErr
		}
		switch len(activeRuns) {
		case 0:
		case 1:
			active := activeRuns[0]
			if recoverErr := validateAgentChainRecoverableRun(obj, active); recoverErr != nil {
				status.Phase = controlv1alpha1.AgentChainPhaseBlocked
				status.LastError = fmt.Sprintf("provenance-valid nonterminal AgentRun %s/%s cannot be safely recovered: %v", active.Namespace, active.Name, recoverErr)
				apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{Type: agentChainReady, Status: metav1.ConditionFalse, ObservedGeneration: obj.Generation, LastTransitionTime: now, Reason: "UnrecoveredActiveRun", Message: status.LastError})
				obj.Status = status
				return r.patchAgentChainStatus(ctx, original, obj, agentChainPollInterval)
			}
			instanceID := active.Labels[agentRunChainInstLabel]
			step := active.Labels[agentRunChainStepLabel]
			status.Phase = controlv1alpha1.AgentChainPhaseRunning
			status.ActiveInstanceID = instanceID
			status.ActiveStep = step
			status.ActiveRunRef = &controlv1alpha1.NamespacedObjectReference{Name: active.Name, Namespace: active.Namespace}
			status.ActiveRunUID = string(active.UID)
			status.ActiveSourceGeneration = active.Spec.SourceGeneration
			status.ActiveWorkflowDigest = active.Spec.SourceDigest
			status.LastInstanceID = instanceID
			startToken := strings.TrimSpace(obj.Annotations[controlv1alpha1.AgentChainStartNowAnnotation])
			if strings.HasPrefix(instanceID, "m-") && startToken != "" && agentChainInstanceID(obj, true, startToken, now.Time) == instanceID {
				status.LastStartToken = startToken
			}
			if obj.Spec.StartIntervalSeconds > 0 {
				base := active.CreationTimestamp.Time
				if strings.HasPrefix(instanceID, "i-") && active.Spec.Trigger.DetectedAt != nil && !active.Spec.Trigger.DetectedAt.IsZero() {
					base = active.Spec.Trigger.DetectedAt.Time
				}
				next := agentChainNextCadenceAfter(base, time.Duration(obj.Spec.StartIntervalSeconds)*time.Second, now.Time)
				status.NextStartAt = &metav1.Time{Time: next}
			}
			status.StepRuns = []controlv1alpha1.AgentChainStepRunStatus{{
				InstanceID:       instanceID,
				Step:             step,
				RunRef:           &controlv1alpha1.NamespacedObjectReference{Name: active.Name, Namespace: active.Namespace},
				RunUID:           string(active.UID),
				Phase:            active.Status.Phase,
				SourceGeneration: active.Spec.SourceGeneration,
				WorkflowDigest:   active.Spec.SourceDigest,
			}}
			status.LastError = ""
			apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
				Type:               agentChainReady,
				Status:             metav1.ConditionFalse,
				ObservedGeneration: obj.Generation,
				LastTransitionTime: now,
				Reason:             "ActiveRunRecovered",
				Message:            fmt.Sprintf("Recovered Forbid ownership of nonterminal AgentRun %s/%s for instance %s step %s after missing status.", active.Namespace, active.Name, instanceID, step),
			})
			obj.Status = status
			return r.patchAgentChainStatus(ctx, original, obj, agentChainPollInterval)
		default:
			status.Phase = controlv1alpha1.AgentChainPhaseBlocked
			status.LastError = fmt.Sprintf("found %d provenance-valid nonterminal AgentRuns without one frozen active status reference", len(activeRuns))
			apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
				Type:               agentChainReady,
				Status:             metav1.ConditionFalse,
				ObservedGeneration: obj.Generation,
				LastTransitionTime: now,
				Reason:             "UnrecoveredActiveRun",
				Message:            status.LastError,
			})
			obj.Status = status
			return r.patchAgentChainStatus(ctx, original, obj, agentChainPollInterval)
		}
	}

	// Cancel advancement if requested, but retain Forbid ownership until the
	// current child is terminal. The controller never deletes an active Job.
	cancelToken := strings.TrimSpace(obj.Annotations[controlv1alpha1.AgentChainCancelInstanceAnnotation])
	if cancelToken != "" && cancelToken != status.LastCancelToken {
		if status.ActiveInstanceID != "" && (cancelToken == status.ActiveInstanceID || cancelToken == "*") {
			status.CancelRequestedInstanceID = status.ActiveInstanceID
			status.Phase = controlv1alpha1.AgentChainPhaseCancelling
			status.LastError = ""
			status.LastCancelToken = cancelToken
			apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
				Type:               agentChainReady,
				Status:             metav1.ConditionFalse,
				ObservedGeneration: obj.Generation,
				LastTransitionTime: now,
				Reason:             "CancellationPending",
				Message:            fmt.Sprintf("Chain instance %q will not advance; waiting for its active AgentRun to become terminal without deleting its Job.", status.ActiveInstanceID),
			})
			obj.Status = status
			return r.patchAgentChainStatus(ctx, original, obj, agentChainPollInterval)
		}
		status.LastCancelToken = cancelToken
	}
	if status.ActiveInstanceID != "" && status.CancelRequestedInstanceID == status.ActiveInstanceID {
		current, currentErr := r.agentChainActiveRun(ctx, obj, &status)
		if currentErr != nil {
			status.Phase = controlv1alpha1.AgentChainPhaseBlocked
			status.LastError = fmt.Sprintf("cannot resolve exact AgentRun while cancelling instance %s step %s: %v", status.ActiveInstanceID, status.ActiveStep, currentErr)
			apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{Type: agentChainReady, Status: metav1.ConditionFalse, ObservedGeneration: obj.Generation, LastTransitionTime: now, Reason: "MissingCancellationRun", Message: status.LastError})
			obj.Status = status
			return r.patchAgentChainStatus(ctx, original, obj, agentChainPollInterval)
		}
		if !agentRunPhaseTerminal(current.Status.Phase) {
			status.Phase = controlv1alpha1.AgentChainPhaseCancelling
			status.LastError = ""
			obj.Status = status
			return r.patchAgentChainStatus(ctx, original, obj, agentChainPollInterval)
		}
		instanceID := status.ActiveInstanceID
		status.LastInstanceID = instanceID
		agentChainClearActive(&status)
		status.Phase = controlv1alpha1.AgentChainPhaseIdle
		status.LastError = ""
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{Type: agentChainReady, Status: metav1.ConditionTrue, ObservedGeneration: obj.Generation, LastTransitionTime: now, Reason: "InstanceCancelled", Message: fmt.Sprintf("Stopped chain instance %q after its active AgentRun became terminal; no successor was created.", instanceID)})
		obj.Status = status
		return r.patchAgentChainStatus(ctx, original, obj, agentChainPollInterval)
	}

	// If an instance is active, sync step status and maybe advance.
	if status.ActiveInstanceID != "" {
		advanced, requeue, err := r.reconcileActiveInstance(ctx, obj, &status, applicationName, now)
		if err != nil {
			return ctrl.Result{}, err
		}
		obj.Status = status
		if advanced || requeue > 0 {
			return r.patchAgentChainStatus(ctx, original, obj, requeue)
		}
		// If still active after reconcile, wait for child terminal.
		if status.ActiveInstanceID != "" {
			obj.Status = status
			return r.patchAgentChainStatus(ctx, original, obj, agentChainPollInterval)
		}
	}

	// Start a new instance when manually nudged or interval-due.
	startToken := strings.TrimSpace(obj.Annotations[controlv1alpha1.AgentChainStartNowAnnotation])
	manualPending := startToken != "" && startToken != status.LastStartToken

	// Preserve any future NextStartAt already on status (suspend must not force
	// an immediate fire on resume when a deadline was already scheduled).
	preservedNext := status.NextStartAt
	nextStart := agentChainNextStartTime(obj, status, now.Time)
	if nextStart != nil {
		status.NextStartAt = &metav1.Time{Time: *nextStart}
	} else if preservedNext != nil {
		status.NextStartAt = preservedNext.DeepCopy()
	} else {
		status.NextStartAt = nil
	}

	shouldStart := manualPending
	if !shouldStart && nextStart != nil && !nextStart.After(now.Time) {
		shouldStart = true
	}
	if !shouldStart {
		status.Phase = controlv1alpha1.AgentChainPhaseIdle
		status.LastError = ""
		message := "AgentChain is idle."
		if status.NextStartAt != nil {
			message = fmt.Sprintf("Next automatic instance start is scheduled for %s.", status.NextStartAt.UTC().Format(time.RFC3339))
		}
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               agentChainReady,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: obj.Generation,
			LastTransitionTime: now,
			Reason:             "Idle",
			Message:            message,
		})
		obj.Status = status
		requeue := agentChainPollInterval
		if status.NextStartAt != nil && status.NextStartAt.After(now.Time) {
			requeue = status.NextStartAt.Time.Sub(now.Time)
		}
		return r.patchAgentChainStatus(ctx, original, obj, requeue)
	}

	if obj.Spec.MaxInstancesPerDay > 0 && instancesToday >= obj.Spec.MaxInstancesPerDay && !manualPending {
		status.Phase = controlv1alpha1.AgentChainPhaseIdle
		status.LastError = ""
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               agentChainReady,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: obj.Generation,
			LastTransitionTime: now,
			Reason:             "DailyBudgetExhausted",
			Message:            fmt.Sprintf("maxInstancesPerDay=%d already reached for the current UTC day.", obj.Spec.MaxInstancesPerDay),
		})
		obj.Status = status
		return r.patchAgentChainStatus(ctx, original, obj, agentChainPollInterval)
	}

	// Terminal backoff for automatic starts only (anchored on prior terminal run).
	if !manualPending {
		if delayed, until := agentChainTerminalBackoffUntil(obj, status, runs, now.Time); delayed {
			status.Phase = controlv1alpha1.AgentChainPhaseIdle
			status.NextStartAt = &metav1.Time{Time: until}
			status.LastError = ""
			apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
				Type:               agentChainReady,
				Status:             metav1.ConditionTrue,
				ObservedGeneration: obj.Generation,
				LastTransitionTime: now,
				Reason:             "TerminalBackoff",
				Message:            fmt.Sprintf("Automatic starts are backed off until %s.", until.UTC().Format(time.RFC3339)),
			})
			obj.Status = status
			return r.patchAgentChainStatus(ctx, original, obj, until.Sub(now.Time))
		}
	}

	// Stable instance ID per start intent so create+status-patch retries are idempotent.
	dueAt := agentChainStartDueAt(obj, manualPending, nextStart, now.Time)
	instanceID := agentChainInstanceID(obj, manualPending, startToken, dueAt)
	workflowDigest := agentChainWorkflowDigest(obj)
	if workflowDigest == "" {
		return ctrl.Result{}, fmt.Errorf("digest AgentChain workflow")
	}
	for _, existing := range runs {
		if existing.Labels[agentRunChainInstLabel] != sanitizeLabelValue(instanceID) {
			continue
		}
		if existing.Spec.SourceGeneration != obj.Generation || existing.Spec.SourceDigest != workflowDigest {
			status.Phase = controlv1alpha1.AgentChainPhaseBlocked
			status.LastError = fmt.Sprintf("existing AgentRun %s/%s for instance %s belongs to source generation %d digest %q, not generation %d digest %q", existing.Namespace, existing.Name, instanceID, existing.Spec.SourceGeneration, existing.Spec.SourceDigest, obj.Generation, workflowDigest)
			apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
				Type:               agentChainReady,
				Status:             metav1.ConditionFalse,
				ObservedGeneration: obj.Generation,
				LastTransitionTime: now,
				Reason:             "InstanceSnapshotCollision",
				Message:            status.LastError,
			})
			obj.Status = status
			return r.patchAgentChainStatus(ctx, original, obj, agentChainPollInterval)
		}
	}
	first := obj.Spec.Steps[0]
	var startDetectedAt *metav1.Time
	if !manualPending {
		value := metav1.NewTime(dueAt)
		startDetectedAt = &value
	}
	run, err := r.createChainedAgentRun(ctx, obj, applicationName, instanceID, obj.Generation, workflowDigest, first, nil, startDetectedAt, "AgentChainStart",
		fmt.Sprintf("instance=%s step=%s", instanceID, first.Name))
	if err != nil {
		return ctrl.Result{}, err
	}

	status.Phase = controlv1alpha1.AgentChainPhaseRunning
	status.ActiveInstanceID = instanceID
	status.ActiveSourceGeneration = obj.Generation
	status.ActiveWorkflowDigest = workflowDigest
	status.LastInstanceID = instanceID
	status.ActiveStep = first.Name
	status.ActiveRunRef = &controlv1alpha1.NamespacedObjectReference{Name: run.Name, Namespace: run.Namespace}
	status.ActiveRunUID = string(run.UID)
	status.StepRuns = []controlv1alpha1.AgentChainStepRunStatus{{
		InstanceID:       instanceID,
		Step:             first.Name,
		RunRef:           &controlv1alpha1.NamespacedObjectReference{Name: run.Name, Namespace: run.Namespace},
		RunUID:           string(run.UID),
		Phase:            run.Status.Phase,
		SourceGeneration: obj.Generation,
		WorkflowDigest:   workflowDigest,
	}}
	status.LastError = ""
	if manualPending {
		status.LastStartToken = startToken
	}
	// Advance next automatic start after an instance start (manual or interval).
	if obj.Spec.StartIntervalSeconds > 0 {
		// Anchor on dueAt for interval starts so retries keep the same cadence.
		base := dueAt
		if manualPending {
			base = now.Time
		}
		next := base.Add(time.Duration(obj.Spec.StartIntervalSeconds) * time.Second)
		status.NextStartAt = &metav1.Time{Time: next}
	} else if !manualPending {
		status.NextStartAt = nil
	}
	// Recompute instancesToday after create (idempotent retries must not double-count).
	_, instancesToday, err = r.agentChainRuns(ctx, obj, now.Time)
	if err != nil {
		return ctrl.Result{}, err
	}
	status.InstancesToday = instancesToday
	apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
		Type:               agentChainReady,
		Status:             metav1.ConditionFalse,
		ObservedGeneration: obj.Generation,
		LastTransitionTime: now,
		Reason:             "InstanceStarted",
		Message:            fmt.Sprintf("Started chain instance %s with step %q as AgentRun %s/%s.", instanceID, first.Name, run.Namespace, run.Name),
	})
	obj.Status = status
	return r.patchAgentChainStatus(ctx, original, obj, agentChainPollInterval)
}

func (r *AgentChainReconciler) reconcileActiveInstance(
	ctx context.Context,
	chain *controlv1alpha1.AgentChain,
	status *controlv1alpha1.AgentChainStatus,
	applicationName string,
	now metav1.Time,
) (advanced bool, requeueAfter time.Duration, err error) {
	instanceID := status.ActiveInstanceID
	workflowDigest := agentChainWorkflowDigest(chain)
	if status.ActiveSourceGeneration == 0 || status.ActiveWorkflowDigest == "" {
		status.Phase = controlv1alpha1.AgentChainPhaseBlocked
		status.LastError = fmt.Sprintf("active instance %s has no frozen source generation/workflow digest", instanceID)
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               agentChainReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: chain.Generation,
			LastTransitionTime: now,
			Reason:             "MissingInstanceSnapshot",
			Message:            status.LastError,
		})
		return false, 0, nil
	}
	if status.ActiveWorkflowDigest != workflowDigest {
		status.Phase = controlv1alpha1.AgentChainPhaseBlocked
		status.LastError = fmt.Sprintf("active instance %s is frozen at source generation %d digest %q; current AgentChain is generation %d digest %q", instanceID, status.ActiveSourceGeneration, status.ActiveWorkflowDigest, chain.Generation, workflowDigest)
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               agentChainReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: chain.Generation,
			LastTransitionTime: now,
			Reason:             "InstanceNeedsRevalidation",
			Message:            status.LastError,
		})
		return false, 0, nil
	}
	stepIndex := agentChainStepIndex(chain, status.ActiveStep)
	if stepIndex < 0 {
		status.Phase = controlv1alpha1.AgentChainPhaseBlocked
		status.LastError = fmt.Sprintf("active step %q is not in the chain spec", status.ActiveStep)
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               agentChainReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: chain.Generation,
			LastTransitionTime: now,
			Reason:             "InvalidActiveStep",
			Message:            status.LastError,
		})
		return false, 0, nil
	}

	// Resolve the exact status-owned run. Labels are indexes, not authority:
	// a newer foreign or mislabelled AgentRun must never replace this child.
	current, currentErr := r.agentChainActiveRun(ctx, chain, status)
	if currentErr != nil {
		status.Phase = controlv1alpha1.AgentChainPhaseBlocked
		status.LastError = fmt.Sprintf("cannot resolve exact AgentRun for active instance %s step %s: %v", instanceID, status.ActiveStep, currentErr)
		reason := "MissingStepRun"
		if errors.Is(currentErr, errAgentChainRunProvenance) {
			reason = "StepProvenanceMismatch"
		}
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               agentChainReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: chain.Generation,
			LastTransitionTime: now,
			Reason:             reason,
			Message:            status.LastError,
		})
		return false, agentChainPollInterval, nil
	}

	status.StepRuns = agentChainUpsertStepRun(status.StepRuns, controlv1alpha1.AgentChainStepRunStatus{
		InstanceID:       instanceID,
		Step:             status.ActiveStep,
		RunRef:           &controlv1alpha1.NamespacedObjectReference{Name: current.Name, Namespace: current.Namespace},
		RunUID:           string(current.UID),
		Phase:            current.Status.Phase,
		SourceGeneration: status.ActiveSourceGeneration,
		WorkflowDigest:   status.ActiveWorkflowDigest,
	})
	if !agentRunPhaseTerminal(current.Status.Phase) {
		status.Phase = controlv1alpha1.AgentChainPhaseRunning
		status.LastError = ""
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               agentChainReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: chain.Generation,
			LastTransitionTime: now,
			Reason:             "StepRunning",
			Message:            fmt.Sprintf("Instance %s step %q is %s on AgentRun %s/%s.", instanceID, status.ActiveStep, current.Status.Phase, current.Namespace, current.Name),
		})
		return false, agentChainPollInterval, nil
	}

	// Terminal current step.
	if current.Status.Phase == controlv1alpha1.AgentRunPhaseNeedsHuman {
		status.Phase = controlv1alpha1.AgentChainPhaseWaitingHuman
		status.LastError = ""
		// Stop advancing; clear active so a new instance can start after backoff.
		status.LastInstanceID = instanceID
		agentChainClearActive(status)
		agentChainApplyTerminalBackoffDeadline(chain, status, current, now.Time)
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               agentChainReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: chain.Generation,
			LastTransitionTime: now,
			Reason:             "WaitingHuman",
			Message:            fmt.Sprintf("Instance %s stopped at step %q with NeedsHuman on AgentRun %s/%s.", instanceID, current.Labels[agentRunChainStepLabel], current.Namespace, current.Name),
		})
		return true, agentChainPollInterval, nil
	}

	nextIndex := stepIndex + 1
	if nextIndex >= len(chain.Spec.Steps) {
		// Final step terminal — distinguish success from failure for operators.
		status.LastInstanceID = instanceID
		agentChainClearActive(status)
		status.LastError = ""
		status.Phase = controlv1alpha1.AgentChainPhaseIdle
		finalStep := chain.Spec.Steps[stepIndex].Name
		if agentChainCompletionMatches(chain.Spec.Completion, current) {
			apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
				Type:               agentChainReady,
				Status:             metav1.ConditionTrue,
				ObservedGeneration: chain.Generation,
				LastTransitionTime: now,
				Reason:             "InstanceCompleted",
				Message:            fmt.Sprintf("Instance %s completed successfully at final step %q.", instanceID, finalStep),
			})
		} else {
			// A terminal phase or decision that does not match completion is a
			// stopped instance, never a successful completion.
			agentChainApplyTerminalBackoffDeadline(chain, status, current, now.Time)
			reason := "InstanceCompletionCriteriaUnmet"
			if current.Status.Phase != controlv1alpha1.AgentRunPhaseSucceeded {
				reason = "InstanceFailed"
			}
			apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
				Type:               agentChainReady,
				Status:             metav1.ConditionTrue,
				ObservedGeneration: chain.Generation,
				LastTransitionTime: now,
				Reason:             reason,
				Message:            fmt.Sprintf("Instance %s stopped at final step %q with phase %s and decision action %q; completion criteria did not match.", instanceID, finalStep, current.Status.Phase, agentRunDecisionAction(current)),
			})
		}
		return true, agentChainPollInterval, nil
	}

	nextStep := chain.Spec.Steps[nextIndex]
	if !agentChainWhenMatches(nextStep.When, chain.Spec.Steps[stepIndex].Name, current) {
		status.Phase = controlv1alpha1.AgentChainPhaseIdle
		status.LastInstanceID = instanceID
		agentChainClearActive(status)
		status.LastError = ""
		agentChainApplyTerminalBackoffDeadline(chain, status, current, now.Time)
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               agentChainReady,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: chain.Generation,
			LastTransitionTime: now,
			Reason:             "InstanceStopped",
			Message:            fmt.Sprintf("Instance %s stopped after step %q phase %s; next step %q when-clause did not match.", instanceID, chain.Spec.Steps[stepIndex].Name, current.Status.Phase, nextStep.Name),
		})
		return true, agentChainPollInterval, nil
	}

	// Collect ancestor runs from exact, UID-bound status references. Never
	// select handoff authority from a label query.
	priorByStep := map[string]*controlv1alpha1.AgentRun{}
	for _, stepRun := range status.StepRuns {
		if stepRun.InstanceID != instanceID || stepRun.RunRef == nil {
			continue
		}
		run, getErr := r.agentChainRunByRef(ctx, chain, status, stepRun.Step, stepRun.RunRef, stepRun.RunUID)
		if getErr != nil {
			status.Phase = controlv1alpha1.AgentChainPhaseBlocked
			status.LastError = fmt.Sprintf("cannot resolve exact handoff AgentRun for instance %s step %s: %v", instanceID, stepRun.Step, getErr)
			apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{Type: agentChainReady, Status: metav1.ConditionFalse, ObservedGeneration: chain.Generation, LastTransitionTime: now, Reason: "HandoffProvenanceMismatch", Message: status.LastError})
			return false, 0, nil
		}
		priorByStep[stepRun.Step] = run
	}
	priorByStep[chain.Spec.Steps[stepIndex].Name] = current

	transitionDetectedAt := current.CreationTimestamp
	if current.Status.CompletedAt != nil && !current.Status.CompletedAt.IsZero() {
		transitionDetectedAt = *current.Status.CompletedAt
	}
	run, err := r.createChainedAgentRun(ctx, chain, applicationName, instanceID, status.ActiveSourceGeneration, status.ActiveWorkflowDigest, nextStep, priorByStep, &transitionDetectedAt, "AgentChainStep",
		fmt.Sprintf("instance=%s step=%s previousRun=%s/%s previousPhase=%s", instanceID, nextStep.Name, current.Namespace, current.Name, current.Status.Phase))
	if err != nil {
		return false, 0, err
	}

	status.Phase = controlv1alpha1.AgentChainPhaseRunning
	status.ActiveStep = nextStep.Name
	status.ActiveRunRef = &controlv1alpha1.NamespacedObjectReference{Name: run.Name, Namespace: run.Namespace}
	status.ActiveRunUID = string(run.UID)
	status.StepRuns = agentChainUpsertStepRun(status.StepRuns, controlv1alpha1.AgentChainStepRunStatus{
		InstanceID:       instanceID,
		Step:             nextStep.Name,
		RunRef:           &controlv1alpha1.NamespacedObjectReference{Name: run.Name, Namespace: run.Namespace},
		RunUID:           string(run.UID),
		Phase:            run.Status.Phase,
		SourceGeneration: status.ActiveSourceGeneration,
		WorkflowDigest:   status.ActiveWorkflowDigest,
	})
	status.LastError = ""
	apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
		Type:               agentChainReady,
		Status:             metav1.ConditionFalse,
		ObservedGeneration: chain.Generation,
		LastTransitionTime: now,
		Reason:             "StepAdvanced",
		Message:            fmt.Sprintf("Instance %s advanced to step %q as AgentRun %s/%s.", instanceID, nextStep.Name, run.Namespace, run.Name),
	})
	return true, agentChainPollInterval, nil
}

func (r *AgentChainReconciler) agentChainActiveRun(
	ctx context.Context,
	chain *controlv1alpha1.AgentChain,
	status *controlv1alpha1.AgentChainStatus,
) (*controlv1alpha1.AgentRun, error) {
	if status == nil || status.ActiveRunRef == nil {
		return nil, fmt.Errorf("status.activeRunRef is missing")
	}
	return r.agentChainRunByRef(ctx, chain, status, status.ActiveStep, status.ActiveRunRef, status.ActiveRunUID)
}

func (r *AgentChainReconciler) agentChainRunByRef(
	ctx context.Context,
	chain *controlv1alpha1.AgentChain,
	status *controlv1alpha1.AgentChainStatus,
	step string,
	ref *controlv1alpha1.NamespacedObjectReference,
	expectedUID string,
) (*controlv1alpha1.AgentRun, error) {
	if chain == nil || status == nil || ref == nil || strings.TrimSpace(ref.Name) == "" {
		return nil, fmt.Errorf("run reference is incomplete")
	}
	namespace := strings.TrimSpace(ref.Namespace)
	if namespace == "" {
		namespace = chain.Namespace
	}
	if namespace != chain.Namespace {
		return nil, fmt.Errorf("run reference namespace %q differs from chain namespace %q", namespace, chain.Namespace)
	}
	run := &controlv1alpha1.AgentRun{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: ref.Name}, run); err != nil {
		return nil, err
	}
	if expectedUID != "" && string(run.UID) != expectedUID {
		return nil, fmt.Errorf("%w: AgentRun %s/%s UID %q differs from frozen UID %q", errAgentChainRunProvenance, run.Namespace, run.Name, run.UID, expectedUID)
	}
	applicationName, err := resolveAgentChainApplicationName(chain)
	if err != nil {
		return nil, err
	}
	expectedLabels := agentChainChildLabels(chain, applicationName, status.ActiveInstanceID, step)
	if run.Spec.SourceRef.APIVersion != controlv1alpha1.GroupVersion.String() ||
		run.Spec.SourceRef.Kind != "AgentChain" ||
		run.Spec.SourceRef.Namespace != chain.Namespace ||
		run.Spec.SourceRef.Name != chain.Name ||
		run.Spec.SourceUID != string(chain.UID) ||
		run.Spec.SourceGeneration != status.ActiveSourceGeneration ||
		run.Spec.SourceDigest != status.ActiveWorkflowDigest ||
		run.Annotations[agentRunChainDigestAnnotation] != status.ActiveWorkflowDigest ||
		run.Labels[agentRunChainLabel] != sanitizeLabelValue(chain.Name) ||
		run.Labels[agentRunChainInstLabel] != sanitizeLabelValue(status.ActiveInstanceID) ||
		run.Labels[agentRunChainStepLabel] != sanitizeLabelValue(step) {
		return nil, fmt.Errorf("%w: AgentRun %s/%s identity does not match frozen instance %s step %s", errAgentChainRunProvenance, run.Namespace, run.Name, status.ActiveInstanceID, step)
	}
	for key, value := range expectedLabels {
		if run.Labels[key] != value {
			return nil, fmt.Errorf("%w: AgentRun %s/%s controller label %q does not match frozen instance", errAgentChainRunProvenance, run.Namespace, run.Name, key)
		}
	}
	return run, nil
}

func (r *AgentChainReconciler) createChainedAgentRun(
	ctx context.Context,
	chain *controlv1alpha1.AgentChain,
	applicationName, instanceID string,
	sourceGeneration int64,
	workflowDigest string,
	step controlv1alpha1.AgentChainStepSpec,
	priorByStep map[string]*controlv1alpha1.AgentRun,
	detectedAt *metav1.Time,
	triggerReason, triggerMessage string,
) (*controlv1alpha1.AgentRun, error) {
	specCopy := step.RunTemplate
	spec := &specCopy
	if applicationName != "" {
		if spec.Scope.ApplicationRef == nil {
			spec.Scope.ApplicationRef = &controlv1alpha1.ApplicationReferenceSpec{Name: applicationName}
		} else if strings.TrimSpace(spec.Scope.ApplicationRef.Name) != applicationName {
			return nil, fmt.Errorf("chain applicationRef %q conflicts with step %q applicationRef %q", applicationName, step.Name, strings.TrimSpace(spec.Scope.ApplicationRef.Name))
		}
	}
	spec.Purpose = controlv1alpha1.AgentRunPurposeChained
	spec.SourceRef = controlv1alpha1.AgentRunSourceRef{
		APIVersion: controlv1alpha1.GroupVersion.String(),
		Kind:       "AgentChain",
		Namespace:  chain.Namespace,
		Name:       chain.Name,
	}
	spec.SourceUID = string(chain.UID)
	spec.SourceGeneration = sourceGeneration
	spec.SourceDigest = workflowDigest
	spec.Trigger.Reason = triggerReason
	spec.Trigger.Message = triggerMessage
	spec.Trigger.DetectedAt = detectedAt
	spec.ScheduleRef = nil

	// Inject status-only handoff into the prompt.
	if priorByStep != nil && step.Handoff != nil {
		handoff := renderAgentChainHandoff(step, priorByStep)
		if handoff != "" {
			if strings.TrimSpace(spec.Prompt) == "" {
				spec.Prompt = handoff
			} else {
				spec.Prompt = strings.TrimSpace(spec.Prompt) + "\n\n" + handoff
			}
		}
	}

	nameHashInput := strings.Join([]string{string(chain.UID), instanceID, step.Name}, "|")
	name := agentRunChildName("agentrun", chain.Name, instanceID, step.Name, shortHash(nameHashInput))
	labels := agentChainChildLabels(chain, applicationName, instanceID, step.Name)

	run := &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: chain.Namespace,
			Labels:    labels,
			Annotations: map[string]string{
				agentRunChainDigestAnnotation: workflowDigest,
			},
		},
		Spec: *spec,
	}
	expected := run.DeepCopy()
	if err := r.Create(ctx, run); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return nil, err
		}
		if err := r.Get(ctx, client.ObjectKey{Namespace: run.Namespace, Name: run.Name}, run); err != nil {
			return nil, err
		}
		if run.Spec.SourceUID != string(chain.UID) || run.Labels[agentRunChainLabel] != sanitizeLabelValue(chain.Name) {
			return nil, fmt.Errorf("AgentRun %s/%s collides with the expected child for AgentChain %s/%s", run.Namespace, run.Name, chain.Namespace, chain.Name)
		}
		// Accept existing matching step child (idempotent retry).
		if run.Labels[agentRunChainInstLabel] != sanitizeLabelValue(instanceID) || run.Labels[agentRunChainStepLabel] != sanitizeLabelValue(step.Name) {
			return nil, fmt.Errorf("AgentRun %s/%s has mismatched chain instance/step labels", run.Namespace, run.Name)
		}
		if run.Annotations[agentRunChainDigestAnnotation] != workflowDigest ||
			run.Spec.SourceGeneration != sourceGeneration || run.Spec.SourceDigest != workflowDigest ||
			!agentChainRunSpecsEqual(run.Spec, expected.Spec) ||
			!agentChainExpectedMetadataMatches(run, expected) {
			return nil, fmt.Errorf("AgentRun %s/%s already exists with a different immutable chain step spec or provenance", run.Namespace, run.Name)
		}
	}
	return run, nil
}

func (r *AgentChainReconciler) agentChainRuns(ctx context.Context, chain *controlv1alpha1.AgentChain, now time.Time) ([]*controlv1alpha1.AgentRun, int, error) {
	list := &controlv1alpha1.AgentRunList{}
	if err := r.List(ctx, list, client.InNamespace(chain.Namespace), client.MatchingLabels{agentRunChainLabel: sanitizeLabelValue(chain.Name)}); err != nil {
		return nil, 0, fmt.Errorf("list chained agent runs: %w", err)
	}
	runs := make([]*controlv1alpha1.AgentRun, 0, len(list.Items))
	instancesToday := map[string]struct{}{}
	dayStart := now.UTC().Truncate(24 * time.Hour)
	for i := range list.Items {
		run := &list.Items[i]
		if run.Labels[agentRunChainLabel] != sanitizeLabelValue(chain.Name) {
			continue
		}
		original := run.DeepCopy()
		if detachAgentRunControllerOwner(run, controlv1alpha1.GroupVersion.String(), "AgentChain", chain.Name, chain.UID) {
			if err := r.Patch(ctx, run, client.MergeFrom(original)); err != nil {
				return nil, 0, fmt.Errorf("detach durable AgentRun %s/%s from AgentChain garbage collection: %w", run.Namespace, run.Name, err)
			}
		}
		runs = append(runs, run)
		inst := run.Labels[agentRunChainInstLabel]
		if inst == "" {
			continue
		}
		// Count unique instances whose first step was created in the UTC day.
		firstStep := ""
		if len(chain.Spec.Steps) > 0 {
			firstStep = sanitizeLabelValue(chain.Spec.Steps[0].Name)
		}
		if firstStep == "" || run.Labels[agentRunChainStepLabel] != firstStep {
			continue
		}
		if !run.CreationTimestamp.Time.Before(dayStart) && run.CreationTimestamp.Time.Before(dayStart.Add(24*time.Hour)) {
			instancesToday[inst] = struct{}{}
		}
	}
	return runs, len(instancesToday), nil
}

func (r *AgentChainReconciler) agentChainOwnedNonterminalRuns(ctx context.Context, chain *controlv1alpha1.AgentChain) ([]*controlv1alpha1.AgentRun, error) {
	if chain == nil {
		return nil, nil
	}
	list := &controlv1alpha1.AgentRunList{}
	// Intentionally do not use mutable labels for this safety scan. Exact
	// sourceRef/sourceUID are the overlap boundary after status loss.
	if err := r.List(ctx, list, client.InNamespace(chain.Namespace)); err != nil {
		return nil, fmt.Errorf("list AgentRuns for orphan ownership recovery: %w", err)
	}
	out := make([]*controlv1alpha1.AgentRun, 0, 1)
	for i := range list.Items {
		run := &list.Items[i]
		if run == nil || agentRunPhaseTerminal(run.Status.Phase) ||
			run.Spec.Purpose != controlv1alpha1.AgentRunPurposeChained ||
			run.Spec.SourceRef.APIVersion != controlv1alpha1.GroupVersion.String() ||
			run.Spec.SourceRef.Kind != "AgentChain" ||
			run.Spec.SourceRef.Namespace != chain.Namespace ||
			run.Spec.SourceRef.Name != chain.Name ||
			run.Spec.SourceUID != string(chain.UID) {
			continue
		}
		out = append(out, run)
	}
	return out, nil
}

func validateAgentChainRecoverableRun(chain *controlv1alpha1.AgentChain, run *controlv1alpha1.AgentRun) error {
	if chain == nil || run == nil {
		return fmt.Errorf("chain or run is nil")
	}
	instanceID := strings.TrimSpace(run.Labels[agentRunChainInstLabel])
	step := strings.TrimSpace(run.Labels[agentRunChainStepLabel])
	applicationName := ""
	if run.Spec.Scope.ApplicationRef != nil {
		applicationName = strings.TrimSpace(run.Spec.Scope.ApplicationRef.Name)
	}
	if run.Spec.SourceGeneration <= 0 || strings.TrimSpace(run.Spec.SourceDigest) == "" ||
		run.Annotations[agentRunChainDigestAnnotation] != run.Spec.SourceDigest ||
		run.Labels[agentRunChainLabel] != sanitizeLabelValue(chain.Name) ||
		instanceID == "" || step == "" ||
		run.Labels[agentManagedByLabel] != "anvil-agents" || applicationName == "" ||
		run.Labels[agentApplicationLabel] != sanitizeLabelValue(applicationName) {
		return fmt.Errorf("immutable source provenance or controller-owned identity metadata is incomplete or inconsistent")
	}
	return nil
}

func agentChainExpectedMetadataMatches(actual, expected *controlv1alpha1.AgentRun) bool {
	if actual == nil || expected == nil {
		return false
	}
	for key, value := range expected.Labels {
		if actual.Labels[key] != value {
			return false
		}
	}
	for key, value := range expected.Annotations {
		if actual.Annotations[key] != value {
			return false
		}
	}
	return true
}

// Compare the CRD wire representation instead of Go nil/empty implementation
// details. This matches what the API server persists for omitempty fields while
// retaining exact equality for every authority-bearing value.
func agentChainRunSpecsEqual(actual, expected controlv1alpha1.AgentRunSpec) bool {
	return digestJSON(agentChainCanonicalRunSpec(actual)) == digestJSON(agentChainCanonicalRunSpec(expected))
}

func agentChainCanonicalRunSpec(spec controlv1alpha1.AgentRunSpec) controlv1alpha1.AgentRunSpec {
	canonical := spec.DeepCopy()
	for i := range canonical.Harness.Execution.ExtraEnv {
		source := canonical.Harness.Execution.ExtraEnv[i].ValueFrom
		// The CRD inherits this Kubernetes core default. Apply it explicitly to
		// both the desired and API-read forms before retry comparison.
		if source != nil && source.FileKeyRef != nil && source.FileKeyRef.Optional == nil {
			value := false
			source.FileKeyRef.Optional = &value
		}
	}
	return *canonical
}

func agentChainChildLabels(chain *controlv1alpha1.AgentChain, applicationName, instanceID, step string) map[string]string {
	labels := map[string]string{
		agentRunChainLabel:     sanitizeLabelValue(chain.Name),
		agentRunChainInstLabel: sanitizeLabelValue(instanceID),
		agentRunChainStepLabel: sanitizeLabelValue(step),
		agentManagedByLabel:    "anvil-agents",
	}
	if applicationName != "" {
		labels[agentApplicationLabel] = sanitizeLabelValue(applicationName)
	}
	if chain.Labels != nil {
		if v := chain.Labels[agentDynamicLabel]; v != "" {
			labels[agentDynamicLabel] = v
		}
		if v := chain.Labels[agentManagerRepositoryLabel]; v != "" {
			labels[agentManagerRepositoryLabel] = v
		}
	}
	return labels
}

func (r *AgentChainReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("agentchain").
		For(&controlv1alpha1.AgentChain{}).
		Owns(&controlv1alpha1.AgentRun{}).
		Complete(r)
}

func (r *AgentChainReconciler) patchAgentChainStatus(ctx context.Context, original, obj *controlv1alpha1.AgentChain, requeueAfter time.Duration) (ctrl.Result, error) {
	if err := r.Status().Patch(ctx, obj, client.MergeFrom(original)); err != nil {
		if apierrors.IsConflict(err) {
			return ctrl.Result{Requeue: true}, nil
		}
		return ctrl.Result{}, err
	}
	if requeueAfter > 0 {
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}
	return ctrl.Result{}, nil
}

func validateAgentChainSpec(chain *controlv1alpha1.AgentChain) error {
	if chain == nil {
		return fmt.Errorf("AgentChain is nil")
	}
	if len(chain.Spec.Steps) == 0 {
		return fmt.Errorf("spec.steps must contain at least one step")
	}
	policy := chain.Spec.ConcurrencyPolicy
	if policy == "" {
		policy = controlv1alpha1.AgentChainConcurrencyForbid
	}
	if policy == controlv1alpha1.AgentChainConcurrencyQueue {
		return fmt.Errorf("spec.concurrencyPolicy=Queue is not implemented yet; use Forbid")
	}
	if policy != controlv1alpha1.AgentChainConcurrencyForbid {
		return fmt.Errorf("spec.concurrencyPolicy %q is not supported", policy)
	}
	if chain.Spec.StartIntervalSeconds < 0 || chain.Spec.StartInitialDelaySeconds < 0 {
		return fmt.Errorf("start interval/delay cannot be negative")
	}
	if chain.Spec.MaxInstancesPerDay < 0 {
		return fmt.Errorf("spec.maxInstancesPerDay cannot be negative")
	}
	if chain.Spec.Backoff != nil && (chain.Spec.Backoff.FailedSeconds < 0 || chain.Spec.Backoff.NeedsHumanSeconds < 0) {
		return fmt.Errorf("spec.backoff values cannot be negative")
	}
	if chain.Spec.Completion != nil {
		for i, action := range chain.Spec.Completion.OnDecisionActions {
			if strings.TrimSpace(action) == "" {
				return fmt.Errorf("spec.completion.onDecisionActions[%d] must not be blank", i)
			}
		}
	}
	seen := map[string]struct{}{}
	for i, step := range chain.Spec.Steps {
		name := strings.TrimSpace(step.Name)
		if name == "" {
			return fmt.Errorf("spec.steps[%d].name is required", i)
		}
		if _, ok := seen[name]; ok {
			return fmt.Errorf("duplicate step name %q", name)
		}
		seen[name] = struct{}{}
		if i == 0 {
			if step.When != nil && strings.TrimSpace(step.When.PreviousStep) != "" {
				return fmt.Errorf("first step %q must not set when.previousStep", name)
			}
			continue
		}
		if step.When == nil || strings.TrimSpace(step.When.PreviousStep) == "" {
			return fmt.Errorf("step %q requires when.previousStep", name)
		}
		for j, action := range step.When.OnDecisionActions {
			if strings.TrimSpace(action) == "" {
				return fmt.Errorf("step %q when.onDecisionActions[%d] must not be blank", name, j)
			}
		}
		for j, phase := range step.When.OnPhases {
			if phase != controlv1alpha1.AgentRunPhaseSucceeded && phase != controlv1alpha1.AgentRunPhaseFailed && phase != controlv1alpha1.AgentRunPhaseNeedsHuman {
				return fmt.Errorf("step %q when.onPhases[%d]=%q is not a terminal AgentRun phase", name, j, phase)
			}
		}
		prev := strings.TrimSpace(step.When.PreviousStep)
		if prev != chain.Spec.Steps[i-1].Name {
			return fmt.Errorf("step %q when.previousStep must be the immediately previous step %q (got %q); v1 is linear only", name, chain.Spec.Steps[i-1].Name, prev)
		}
	}
	return nil
}

// agentChainWorkflowDigest intentionally excludes cadence, backoff, budgets,
// and suspend. It binds the authority-bearing execution graph used by one
// instance: application identity plus ordered run templates, gates, and
// handoffs. Operational edits cannot silently reinterpret an active instance.
func agentChainWorkflowDigest(chain *controlv1alpha1.AgentChain) string {
	if chain == nil {
		return ""
	}
	snapshot := struct {
		ApplicationRef *controlv1alpha1.ApplicationReferenceSpec `json:"applicationRef,omitempty"`
		Completion     *controlv1alpha1.AgentChainCompletionSpec `json:"completion,omitempty"`
		PolicyLabels   map[string]string                         `json:"policyLabels,omitempty"`
		Steps          []controlv1alpha1.AgentChainStepSpec      `json:"steps"`
	}{
		ApplicationRef: chain.Spec.ApplicationRef,
		Completion:     chain.Spec.Completion,
		PolicyLabels: map[string]string{
			agentDynamicLabel:           chain.Labels[agentDynamicLabel],
			agentManagerRepositoryLabel: chain.Labels[agentManagerRepositoryLabel],
		},
		Steps: chain.Spec.Steps,
	}
	for key, value := range snapshot.PolicyLabels {
		if value == "" {
			delete(snapshot.PolicyLabels, key)
		}
	}
	return digestJSON(snapshot)
}

func resolveAgentChainApplicationName(chain *controlv1alpha1.AgentChain) (string, error) {
	if chain == nil {
		return "", fmt.Errorf("AgentChain is nil")
	}
	var names []string
	if chain.Spec.ApplicationRef != nil {
		if name := strings.TrimSpace(chain.Spec.ApplicationRef.Name); name != "" {
			names = append(names, name)
		}
	}
	for _, step := range chain.Spec.Steps {
		if step.RunTemplate.Scope.ApplicationRef != nil {
			if name := strings.TrimSpace(step.RunTemplate.Scope.ApplicationRef.Name); name != "" {
				names = append(names, name)
			}
		}
	}
	if len(names) == 0 {
		return "", fmt.Errorf("AgentChain requires applicationRef or step runTemplate.scope.applicationRef")
	}
	canonical := names[0]
	for _, name := range names[1:] {
		if name != canonical {
			return "", fmt.Errorf("AgentChain application identity conflicts: %q vs %q", canonical, name)
		}
	}
	return canonical, nil
}

func agentChainStepIndex(chain *controlv1alpha1.AgentChain, stepName string) int {
	stepName = strings.TrimSpace(stepName)
	for i, step := range chain.Spec.Steps {
		if step.Name == stepName {
			return i
		}
	}
	return -1
}

func agentChainWhenMatches(when *controlv1alpha1.AgentChainWhenSpec, expectedPrev string, prior *controlv1alpha1.AgentRun) bool {
	if prior == nil {
		return false
	}
	if expectedPrev != "" && prior.Labels[agentRunChainStepLabel] != sanitizeLabelValue(expectedPrev) {
		return false
	}
	phases := []controlv1alpha1.AgentRunPhase{controlv1alpha1.AgentRunPhaseSucceeded}
	if when != nil && len(when.OnPhases) > 0 {
		phases = when.OnPhases
	}
	matchedPhase := false
	for _, phase := range phases {
		if prior.Status.Phase == phase {
			matchedPhase = true
			break
		}
	}
	if !matchedPhase {
		return false
	}
	if when != nil && len(when.OnDecisionActions) > 0 {
		action := ""
		if prior.Status.Decision != nil {
			action = strings.TrimSpace(prior.Status.Decision.Action)
		}
		ok := false
		for _, allowed := range when.OnDecisionActions {
			if strings.EqualFold(strings.TrimSpace(allowed), action) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

func agentChainCompletionMatches(completion *controlv1alpha1.AgentChainCompletionSpec, run *controlv1alpha1.AgentRun) bool {
	if run == nil || run.Status.Phase != controlv1alpha1.AgentRunPhaseSucceeded {
		return false
	}
	if completion == nil || len(completion.OnDecisionActions) == 0 {
		return true
	}
	action := agentRunDecisionAction(run)
	for _, allowed := range completion.OnDecisionActions {
		if strings.EqualFold(strings.TrimSpace(allowed), action) {
			return true
		}
	}
	return false
}

func agentRunDecisionAction(run *controlv1alpha1.AgentRun) string {
	if run == nil || run.Status.Decision == nil {
		return ""
	}
	return strings.TrimSpace(run.Status.Decision.Action)
}

func agentChainInstanceID(chain *controlv1alpha1.AgentChain, manual bool, startToken string, dueAt time.Time) string {
	if chain == nil {
		return shortHash(dueAt.UTC().Format(time.RFC3339))[:12]
	}
	if manual {
		token := strings.TrimSpace(startToken)
		if token == "" {
			token = dueAt.UTC().Format(time.RFC3339Nano)
		}
		return "m-" + shortHash(strings.Join([]string{string(chain.UID), "manual", token}, "|"))[:12]
	}
	// Interval (or automatic) starts: stable per due wall time.
	return "i-" + shortHash(strings.Join([]string{string(chain.UID), "interval", dueAt.UTC().Format(time.RFC3339)}, "|"))[:12]
}

// agentChainStartDueAt picks a stable due timestamp for the instance id.
// Manual starts use the start annotation token's wall time via now; interval
// starts prefer the scheduled NextStartAt or the current interval period slot
// so create+status retries in the same period stay idempotent.
func agentChainStartDueAt(chain *controlv1alpha1.AgentChain, manual bool, nextStart *time.Time, now time.Time) time.Time {
	if manual {
		return now.UTC().Truncate(time.Second)
	}
	if nextStart != nil {
		return nextStart.UTC().Truncate(time.Second)
	}
	if chain != nil && chain.Spec.StartIntervalSeconds > 0 {
		interval := time.Duration(chain.Spec.StartIntervalSeconds) * time.Second
		base := chain.CreationTimestamp.Time.UTC()
		if chain.Spec.StartInitialDelaySeconds > 0 {
			base = base.Add(time.Duration(chain.Spec.StartInitialDelaySeconds) * time.Second)
		}
		if now.Before(base) {
			return base
		}
		elapsed := now.Sub(base)
		period := int64(elapsed / interval)
		return base.Add(time.Duration(period) * interval)
	}
	return now.UTC().Truncate(time.Second)
}

func agentChainNextCadenceAfter(base time.Time, interval time.Duration, now time.Time) time.Time {
	next := base.Add(interval)
	if interval <= 0 || next.After(now) {
		return next
	}
	missed := now.Sub(next)/interval + 1
	return next.Add(missed * interval)
}

// agentChainApplyTerminalBackoffDeadline sets NextStartAt to terminalAt+delay when
// backoff is configured for the terminal phase. Leaves an existing later deadline alone.
func agentChainApplyTerminalBackoffDeadline(chain *controlv1alpha1.AgentChain, status *controlv1alpha1.AgentChainStatus, terminal *controlv1alpha1.AgentRun, now time.Time) {
	if chain == nil || status == nil || chain.Spec.Backoff == nil || terminal == nil {
		return
	}
	var delay int
	switch terminal.Status.Phase {
	case controlv1alpha1.AgentRunPhaseFailed:
		delay = chain.Spec.Backoff.FailedSeconds
	case controlv1alpha1.AgentRunPhaseNeedsHuman:
		delay = chain.Spec.Backoff.NeedsHumanSeconds
	default:
		return
	}
	if delay <= 0 {
		return
	}
	terminalAt := terminal.CreationTimestamp.Time
	if terminal.Status.CompletedAt != nil && !terminal.Status.CompletedAt.IsZero() {
		terminalAt = terminal.Status.CompletedAt.Time
	}
	until := terminalAt.Add(time.Duration(delay) * time.Second)
	if until.Before(now) {
		// Already expired; do not invent a new deadline.
		return
	}
	if status.NextStartAt != nil && status.NextStartAt.After(until) {
		return
	}
	status.NextStartAt = &metav1.Time{Time: until}
}

func agentChainUpsertStepRun(existing []controlv1alpha1.AgentChainStepRunStatus, next controlv1alpha1.AgentChainStepRunStatus) []controlv1alpha1.AgentChainStepRunStatus {
	out := make([]controlv1alpha1.AgentChainStepRunStatus, 0, len(existing)+1)
	replaced := false
	for _, item := range existing {
		if item.InstanceID == next.InstanceID && item.Step == next.Step {
			out = append(out, next)
			replaced = true
			continue
		}
		// Drop steps from older instances to keep status bounded.
		if item.InstanceID != next.InstanceID {
			continue
		}
		out = append(out, item)
	}
	if !replaced {
		out = append(out, next)
	}
	return out
}

func agentChainNextStartTime(chain *controlv1alpha1.AgentChain, status controlv1alpha1.AgentChainStatus, now time.Time) *time.Time {
	if chain.Spec.StartIntervalSeconds <= 0 {
		return nil
	}
	if status.NextStartAt != nil {
		t := status.NextStartAt.Time
		return &t
	}
	// First automatic start: creation + initial delay.
	base := chain.CreationTimestamp.Time
	if chain.Spec.StartInitialDelaySeconds > 0 {
		base = base.Add(time.Duration(chain.Spec.StartInitialDelaySeconds) * time.Second)
	}
	if base.Before(now) {
		// Start at the latest stable cadence boundary, not the historical first
		// slot. A chain unsuspended after days must not burst through stale work.
		interval := time.Duration(chain.Spec.StartIntervalSeconds) * time.Second
		elapsed := now.Sub(base)
		period := time.Duration(int64(elapsed / interval))
		latest := base.Add(period * interval)
		return &latest
	}
	return &base
}

func agentChainTerminalBackoffUntil(chain *controlv1alpha1.AgentChain, status controlv1alpha1.AgentChainStatus, runs []*controlv1alpha1.AgentRun, now time.Time) (bool, time.Time) {
	if chain.Spec.Backoff == nil {
		return false, time.Time{}
	}
	// Prefer a deadline already computed and stored when the instance stopped.
	if status.NextStartAt != nil && status.NextStartAt.After(now) {
		// Only treat as backoff when the last step is a terminal failure/human hold.
		if len(status.StepRuns) > 0 {
			last := status.StepRuns[len(status.StepRuns)-1]
			if last.Phase == controlv1alpha1.AgentRunPhaseFailed || last.Phase == controlv1alpha1.AgentRunPhaseNeedsHuman {
				return true, status.NextStartAt.Time
			}
		}
	}
	if len(status.StepRuns) == 0 {
		return false, time.Time{}
	}
	last := status.StepRuns[len(status.StepRuns)-1]
	var delay int
	switch last.Phase {
	case controlv1alpha1.AgentRunPhaseFailed:
		delay = chain.Spec.Backoff.FailedSeconds
	case controlv1alpha1.AgentRunPhaseNeedsHuman:
		delay = chain.Spec.Backoff.NeedsHumanSeconds
	default:
		return false, time.Time{}
	}
	if delay <= 0 {
		return false, time.Time{}
	}
	// Anchor on the terminal run's CompletedAt (fallback CreationTimestamp).
	terminalAt := now
	for _, run := range runs {
		if last.RunRef != nil && run.Name == last.RunRef.Name && run.Namespace == last.RunRef.Namespace {
			terminalAt = run.CreationTimestamp.Time
			if run.Status.CompletedAt != nil && !run.Status.CompletedAt.IsZero() {
				terminalAt = run.Status.CompletedAt.Time
			}
			break
		}
	}
	until := terminalAt.Add(time.Duration(delay) * time.Second)
	if !until.After(now) {
		return false, time.Time{}
	}
	return true, until
}

func renderAgentChainHandoff(step controlv1alpha1.AgentChainStepSpec, priorByStep map[string]*controlv1alpha1.AgentRun) string {
	if step.Handoff == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("## AgentChain handoff (controller-injected, status only)\n")
	b.WriteString("This context is derived from prior AgentRun status. It is not release authority and does not grant credentials.\n")

	ancestors := step.Handoff.IncludeAncestorSteps
	if len(ancestors) == 0 && step.When != nil && strings.TrimSpace(step.When.PreviousStep) != "" {
		ancestors = []string{step.When.PreviousStep}
	}
	for _, name := range ancestors {
		run := priorByStep[name]
		if run == nil {
			run = priorByStep[sanitizeLabelValue(name)]
		}
		if run == nil {
			b.WriteString(fmt.Sprintf("\n### Step %q\n- missing prior AgentRun\n", name))
			continue
		}
		b.WriteString(fmt.Sprintf("\n### Step %q → AgentRun %s/%s\n", name, run.Namespace, run.Name))
		b.WriteString(fmt.Sprintf("- phase: %s\n", run.Status.Phase))
		if step.Handoff.IncludePullRequestURL && strings.TrimSpace(run.Status.PullRequestURL) != "" {
			b.WriteString(fmt.Sprintf("- pullRequestURL: %s\n", strings.TrimSpace(run.Status.PullRequestURL)))
		}
		if step.Handoff.IncludeDecision && run.Status.Decision != nil {
			b.WriteString(fmt.Sprintf("- decision.classification: %s\n", run.Status.Decision.Classification))
			b.WriteString(fmt.Sprintf("- decision.action: %s\n", run.Status.Decision.Action))
			if summary := strings.TrimSpace(run.Status.Decision.Summary); summary != "" {
				b.WriteString(fmt.Sprintf("- decision.summary: %s\n", truncateRunes(summary, 1024)))
			}
		}
		if step.Handoff.IncludeLatestReports {
			for _, report := range run.Status.Reports {
				b.WriteString(fmt.Sprintf("- report[%s]: %s\n", report.Type, truncateRunes(report.Summary, 512)))
			}
		}
		if step.Handoff.IncludeOutputExcerptBytes > 0 && strings.TrimSpace(run.Status.Output) != "" {
			limit := step.Handoff.IncludeOutputExcerptBytes
			if limit > agentChainHandoffMax {
				limit = agentChainHandoffMax
			}
			excerpt := run.Status.Output
			if len(excerpt) > limit {
				excerpt = excerpt[:limit] + "…"
			}
			b.WriteString("- outputExcerpt:\n```\n")
			b.WriteString(excerpt)
			b.WriteString("\n```\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func truncateRunes(value string, max int) string {
	if max <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max]) + "…"
}
