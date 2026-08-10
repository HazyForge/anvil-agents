package controller

import (
	"context"
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
	agentChainReady        = "Ready"
	agentChainPollInterval = 30 * time.Second
	agentRunChainLabel     = "control.anvil.hazyforge.io/agent-chain"
	agentRunChainInstLabel = "control.anvil.hazyforge.io/agent-chain-instance"
	agentRunChainStepLabel = "control.anvil.hazyforge.io/agent-chain-step"
	agentChainHandoffMax   = 8192
)

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
		status.ActiveInstanceID = ""
		status.ActiveStep = ""
		status.ActiveRunRef = nil
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
		status.NextStartAt = nil
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
		status.NextStartAt = nil
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

	// Cancel instance if requested.
	cancelToken := strings.TrimSpace(obj.Annotations[controlv1alpha1.AgentChainCancelInstanceAnnotation])
	if cancelToken != "" && cancelToken != status.LastCancelToken {
		if status.ActiveInstanceID != "" && (cancelToken == status.ActiveInstanceID || cancelToken == "*") {
			status.LastInstanceID = status.ActiveInstanceID
			status.ActiveInstanceID = ""
			status.ActiveStep = ""
			status.ActiveRunRef = nil
			status.Phase = controlv1alpha1.AgentChainPhaseIdle
			status.LastError = ""
			status.LastCancelToken = cancelToken
			apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
				Type:               agentChainReady,
				Status:             metav1.ConditionTrue,
				ObservedGeneration: obj.Generation,
				LastTransitionTime: now,
				Reason:             "InstanceCancelled",
				Message:            fmt.Sprintf("Stopped advancing chain instance %q; active Jobs were not deleted.", cancelToken),
			})
			obj.Status = status
			return r.patchAgentChainStatus(ctx, original, obj, agentChainPollInterval)
		}
		status.LastCancelToken = cancelToken
	}

	// If an instance is active, sync step status and maybe advance.
	if status.ActiveInstanceID != "" {
		advanced, requeue, err := r.reconcileActiveInstance(ctx, obj, &status, runs, applicationName, now)
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
	nextStart := agentChainNextStartTime(obj, status, now.Time)
	status.NextStartAt = nil
	if nextStart != nil {
		status.NextStartAt = &metav1.Time{Time: *nextStart}
	}

	shouldStart := manualPending
	if !shouldStart && nextStart != nil && !nextStart.After(now.Time) {
		shouldStart = true
	}
	if !shouldStart {
		status.Phase = controlv1alpha1.AgentChainPhaseIdle
		status.LastError = ""
		message := "AgentChain is idle."
		if nextStart != nil {
			message = fmt.Sprintf("Next automatic instance start is scheduled for %s.", nextStart.UTC().Format(time.RFC3339))
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
		if nextStart != nil && nextStart.After(now.Time) {
			requeue = nextStart.Sub(now.Time)
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

	// Terminal backoff for automatic starts only.
	if !manualPending {
		if delayed, until := agentChainTerminalBackoffUntil(obj, status, now.Time); delayed {
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

	instanceID := now.UTC().Format("20060102T150405Z") + "-" + shortHash(strings.Join([]string{string(obj.UID), startToken, now.Format(time.RFC3339Nano)}, "|"))[:8]
	first := obj.Spec.Steps[0]
	run, err := r.createChainedAgentRun(ctx, obj, applicationName, instanceID, first, nil, now, "AgentChainStart",
		fmt.Sprintf("instance=%s step=%s", instanceID, first.Name))
	if err != nil {
		return ctrl.Result{}, err
	}

	status.Phase = controlv1alpha1.AgentChainPhaseRunning
	status.ActiveInstanceID = instanceID
	status.LastInstanceID = instanceID
	status.ActiveStep = first.Name
	status.ActiveRunRef = &controlv1alpha1.NamespacedObjectReference{Name: run.Name, Namespace: run.Namespace}
	status.StepRuns = []controlv1alpha1.AgentChainStepRunStatus{{
		InstanceID: instanceID,
		Step:       first.Name,
		RunRef:     &controlv1alpha1.NamespacedObjectReference{Name: run.Name, Namespace: run.Namespace},
		Phase:      run.Status.Phase,
	}}
	status.LastError = ""
	if manualPending {
		status.LastStartToken = startToken
	}
	// Advance next automatic start after an interval start.
	if obj.Spec.StartIntervalSeconds > 0 {
		next := now.Time.Add(time.Duration(obj.Spec.StartIntervalSeconds) * time.Second)
		status.NextStartAt = &metav1.Time{Time: next}
	} else {
		status.NextStartAt = nil
	}
	status.InstancesToday = instancesToday + 1
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
	runs []*controlv1alpha1.AgentRun,
	applicationName string,
	now metav1.Time,
) (advanced bool, requeueAfter time.Duration, err error) {
	instanceID := status.ActiveInstanceID
	stepIndex := agentChainStepIndex(chain, status.ActiveStep)
	if stepIndex < 0 {
		status.Phase = controlv1alpha1.AgentChainPhaseBlocked
		status.LastError = fmt.Sprintf("active step %q is not in the chain spec", status.ActiveStep)
		return false, 0, nil
	}

	// Find the run for active step/instance.
	var current *controlv1alpha1.AgentRun
	for _, run := range runs {
		if run.Labels[agentRunChainInstLabel] == sanitizeLabelValue(instanceID) &&
			run.Labels[agentRunChainStepLabel] == sanitizeLabelValue(status.ActiveStep) {
			if current == nil || run.CreationTimestamp.After(current.CreationTimestamp.Time) {
				current = run
			}
		}
	}
	if current == nil {
		status.Phase = controlv1alpha1.AgentChainPhaseBlocked
		status.LastError = fmt.Sprintf("missing AgentRun for active instance %s step %s", instanceID, status.ActiveStep)
		return false, agentChainPollInterval, nil
	}

	status.ActiveRunRef = &controlv1alpha1.NamespacedObjectReference{Name: current.Name, Namespace: current.Namespace}
	status.StepRuns = agentChainUpsertStepRun(status.StepRuns, controlv1alpha1.AgentChainStepRunStatus{
		InstanceID: instanceID,
		Step:       status.ActiveStep,
		RunRef:     &controlv1alpha1.NamespacedObjectReference{Name: current.Name, Namespace: current.Namespace},
		Phase:      current.Status.Phase,
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
		// Stop advancing; keep activeInstance for operator visibility, but clear active to allow new starts after backoff.
		status.LastInstanceID = instanceID
		status.ActiveInstanceID = ""
		status.ActiveStep = ""
		status.ActiveRunRef = nil
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
		// Chain complete.
		status.Phase = controlv1alpha1.AgentChainPhaseIdle
		status.LastInstanceID = instanceID
		status.ActiveInstanceID = ""
		status.ActiveStep = ""
		status.ActiveRunRef = nil
		status.LastError = ""
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               agentChainReady,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: chain.Generation,
			LastTransitionTime: now,
			Reason:             "InstanceCompleted",
			Message:            fmt.Sprintf("Instance %s completed at final step %q (%s).", instanceID, chain.Spec.Steps[stepIndex].Name, current.Status.Phase),
		})
		return true, agentChainPollInterval, nil
	}

	nextStep := chain.Spec.Steps[nextIndex]
	if !agentChainWhenMatches(nextStep.When, chain.Spec.Steps[stepIndex].Name, current) {
		status.Phase = controlv1alpha1.AgentChainPhaseIdle
		status.LastInstanceID = instanceID
		status.ActiveInstanceID = ""
		status.ActiveStep = ""
		status.ActiveRunRef = nil
		status.LastError = ""
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

	// Collect ancestor runs for handoff.
	priorByStep := map[string]*controlv1alpha1.AgentRun{}
	for _, run := range runs {
		if run.Labels[agentRunChainInstLabel] != sanitizeLabelValue(instanceID) {
			continue
		}
		stepName := run.Labels[agentRunChainStepLabel]
		if stepName == "" {
			continue
		}
		// Prefer latest terminal for each step.
		if existing, ok := priorByStep[stepName]; ok {
			if run.CreationTimestamp.After(existing.CreationTimestamp.Time) {
				priorByStep[stepName] = run
			}
			continue
		}
		priorByStep[stepName] = run
	}
	priorByStep[sanitizeLabelValue(chain.Spec.Steps[stepIndex].Name)] = current
	// Map sanitized label back: store also by original step names.
	for _, step := range chain.Spec.Steps {
		if run, ok := priorByStep[sanitizeLabelValue(step.Name)]; ok {
			priorByStep[step.Name] = run
		}
	}

	run, err := r.createChainedAgentRun(ctx, chain, applicationName, instanceID, nextStep, priorByStep, now, "AgentChainStep",
		fmt.Sprintf("instance=%s step=%s previousRun=%s/%s previousPhase=%s", instanceID, nextStep.Name, current.Namespace, current.Name, current.Status.Phase))
	if err != nil {
		return false, 0, err
	}

	status.Phase = controlv1alpha1.AgentChainPhaseRunning
	status.ActiveStep = nextStep.Name
	status.ActiveRunRef = &controlv1alpha1.NamespacedObjectReference{Name: run.Name, Namespace: run.Namespace}
	status.StepRuns = agentChainUpsertStepRun(status.StepRuns, controlv1alpha1.AgentChainStepRunStatus{
		InstanceID: instanceID,
		Step:       nextStep.Name,
		RunRef:     &controlv1alpha1.NamespacedObjectReference{Name: run.Name, Namespace: run.Namespace},
		Phase:      run.Status.Phase,
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

func (r *AgentChainReconciler) createChainedAgentRun(
	ctx context.Context,
	chain *controlv1alpha1.AgentChain,
	applicationName, instanceID string,
	step controlv1alpha1.AgentChainStepSpec,
	priorByStep map[string]*controlv1alpha1.AgentRun,
	now metav1.Time,
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
	spec.SourceGeneration = chain.Generation
	spec.Trigger.Reason = triggerReason
	spec.Trigger.Message = triggerMessage
	spec.Trigger.DetectedAt = &now
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
	labels := map[string]string{
		agentRunChainLabel:     sanitizeLabelValue(chain.Name),
		agentRunChainInstLabel: sanitizeLabelValue(instanceID),
		agentRunChainStepLabel: sanitizeLabelValue(step.Name),
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

	run := &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: chain.Namespace,
			Labels:    labels,
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
		_ = expected
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
		// Count unique instances that started today (step 0 create time).
		if !run.CreationTimestamp.Time.Before(dayStart) && run.CreationTimestamp.Time.Before(dayStart.Add(24*time.Hour)) {
			// Prefer counting first step only when label present; else count instance once.
			instancesToday[inst] = struct{}{}
		}
	}
	return runs, len(instancesToday), nil
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
		prev := strings.TrimSpace(step.When.PreviousStep)
		if prev != chain.Spec.Steps[i-1].Name {
			return fmt.Errorf("step %q when.previousStep must be the immediately previous step %q (got %q); v1 is linear only", name, chain.Spec.Steps[i-1].Name, prev)
		}
	}
	return nil
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
	_ = expectedPrev
	return true
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
		// Already due.
		t := now
		return &t
	}
	return &base
}

func agentChainTerminalBackoffUntil(chain *controlv1alpha1.AgentChain, status controlv1alpha1.AgentChainStatus, now time.Time) (bool, time.Time) {
	if chain.Spec.Backoff == nil || len(status.StepRuns) == 0 {
		return false, time.Time{}
	}
	// Look at last step of last instance.
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
	// Without per-step finishedAt on status, use nextStartAt or now+delay once.
	until := now.Add(time.Duration(delay) * time.Second)
	if status.NextStartAt != nil && status.NextStartAt.After(now) {
		return true, status.NextStartAt.Time
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
	if max <= 0 || len(value) <= max {
		return value
	}
	return value[:max] + "…"
}
