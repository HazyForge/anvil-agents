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
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	controlv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

const (
	agentScheduleReady          = "Ready"
	agentSchedulePollInterval   = 30 * time.Second
	agentRunScheduleLabel       = "control.anvil.hazyforge.io/agent-schedule"
	agentRunTemplateLabel       = "control.anvil.hazyforge.io/agent-schedule-template"
	agentManagedByLabel         = "app.kubernetes.io/managed-by"
	agentApplicationLabel       = "control.anvil.hazyforge.io/application"
	agentDynamicLabel           = "control.anvil.hazyforge.io/agent-dynamic"
	agentManagerRepositoryLabel = "control.anvil.hazyforge.io/agent-manager-repository"
)

type agentScheduleRunRequest struct {
	ApplicationName string
	NameSegment     string
	NameTimestamp   string
	NameIdentityKey string
	TemplateName    string
	TriggerReason   string
	TriggerMessage  string
	ForceTrigger    bool
	Token           string
	NameTime        metav1.Time
}

// +kubebuilder:rbac:groups="control.anvil.hazyforge.io",resources=agentschedules,verbs=create;delete;get;list;patch;update;watch
// +kubebuilder:rbac:groups="control.anvil.hazyforge.io",resources=agentschedules/status,verbs=get;patch;update
// +kubebuilder:rbac:groups="control.anvil.hazyforge.io",resources=agentschedules/finalizers,verbs=update
// +kubebuilder:rbac:groups="control.anvil.hazyforge.io",resources=agentruns,verbs=create;get;list;patch;update;watch
type AgentScheduleReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

func (r *AgentScheduleReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	obj := &controlv1alpha1.AgentSchedule{}
	if err := r.Get(ctx, req.NamespacedName, obj); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !obj.GetDeletionTimestamp().IsZero() {
		return ctrl.Result{}, nil
	}

	original := obj.DeepCopy()
	status := obj.Status
	previousObservedGeneration := status.ObservedGeneration
	status.ObservedGeneration = obj.Generation
	generationChanged := previousObservedGeneration != 0 && previousObservedGeneration != obj.Generation
	now := metav1.Now()
	manualToken := agentScheduleManualRunToken(obj)
	manualPending := manualToken != "" && manualToken != status.LastManualRunToken
	manualTemplateName := agentScheduleManualRunTemplateName(obj)

	if obj.Spec.Suspend {
		status.Phase = controlv1alpha1.AgentSchedulePhaseSuspended
		status.NextRunAt = nil
		message := "AgentSchedule is suspended."
		if manualPending {
			message = fmt.Sprintf("AgentSchedule is suspended; manual run token %q remains pending.", manualToken)
		}
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               agentScheduleReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: obj.Generation,
			LastTransitionTime: now,
			Reason:             "Suspended",
			Message:            message,
		})
		obj.Status = status
		return r.patchAgentScheduleStatus(ctx, original, obj, 0)
	}
	if obj.Spec.IntervalSeconds <= 0 {
		status.Phase = controlv1alpha1.AgentSchedulePhaseBlocked
		status.LastError = "spec.intervalSeconds must be greater than zero."
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               agentScheduleReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: obj.Generation,
			LastTransitionTime: now,
			Reason:             "InvalidInterval",
			Message:            status.LastError,
		})
		obj.Status = status
		return r.patchAgentScheduleStatus(ctx, original, obj, 0)
	}
	if obj.Spec.MaxConcurrentRuns < 0 {
		status.Phase = controlv1alpha1.AgentSchedulePhaseBlocked
		status.LastError = "spec.maxConcurrentRuns cannot be negative."
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               agentScheduleReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: obj.Generation,
			LastTransitionTime: now,
			Reason:             "InvalidMaxConcurrentRuns",
			Message:            status.LastError,
		})
		obj.Status = status
		return r.patchAgentScheduleStatus(ctx, original, obj, 0)
	}
	if obj.Spec.Backoff != nil && (obj.Spec.Backoff.FailedSeconds < 0 || obj.Spec.Backoff.NeedsHumanSeconds < 0) {
		status.Phase = controlv1alpha1.AgentSchedulePhaseBlocked
		status.LastError = "spec.backoff failedSeconds and needsHumanSeconds cannot be negative."
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               agentScheduleReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: obj.Generation,
			LastTransitionTime: now,
			Reason:             "InvalidBackoff",
			Message:            status.LastError,
		})
		obj.Status = status
		return r.patchAgentScheduleStatus(ctx, original, obj, 0)
	}
	applicationName, err := resolveAgentScheduleApplicationName(ctx, r.Client, obj)
	if err != nil {
		status.Phase = controlv1alpha1.AgentSchedulePhaseBlocked
		status.NextRunAt = nil
		status.LastError = err.Error()
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               agentScheduleReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: obj.Generation,
			LastTransitionTime: now,
			Reason:             "InvalidApplicationIdentity",
			Message:            status.LastError,
		})
		obj.Status = status
		return r.patchAgentScheduleStatus(ctx, original, obj, agentSchedulePollInterval)
	}

	activeRuns, last, lastIntervalTemplate, err := r.agentScheduleRuns(ctx, obj)
	if err != nil {
		return ctrl.Result{}, err
	}
	policy := agentScheduleConcurrencyPolicy(obj)
	active := agentScheduleActiveRunForStatus(policy, activeRuns)
	status.ActiveRunRef = nil
	status.ActiveRunCount = len(activeRuns)
	if active != nil {
		status.ActiveRunRef = &controlv1alpha1.NamespacedObjectReference{Name: active.Name, Namespace: active.Namespace}
	}
	if last != nil {
		status.LastRunRef = &controlv1alpha1.NamespacedObjectReference{Name: last.Name, Namespace: last.Namespace}
		status.LastRunPhase = last.Status.Phase
		if last.Status.StartedAt != nil {
			status.LastRunAt = last.Status.StartedAt
		}
	}
	pause, err := activeAgentRunPauseForApplication(ctx, r.Client, applicationName, now.Time)
	if err != nil {
		return ctrl.Result{}, err
	}
	if pause != nil {
		status.Phase = controlv1alpha1.AgentSchedulePhaseSuspended
		status.NextRunAt = nil
		status.LastError = ""
		message := fmt.Sprintf("Application %q is paused by AgentRunControl %q.", applicationName, pause.ControlName)
		if pause.Reason != "" {
			message = fmt.Sprintf("%s Reason: %s", message, pause.Reason)
		}
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               agentScheduleReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: obj.Generation,
			LastTransitionTime: now,
			Reason:             "ApplicationPaused",
			Message:            message,
		})
		obj.Status = status
		return r.patchAgentScheduleStatus(ctx, original, obj, agentRunPauseRequeueAfter(pause, now.Time))
	}

	nextRunAt := agentScheduleNextRunTime(obj, status, now.Time, generationChanged)
	nextRunAt, terminalBackoffApplied := agentScheduleNextRunWithTerminalBackoff(obj, last, nextRunAt)
	status.NextRunAt = &nextRunAt
	if active != nil && policy == controlv1alpha1.AgentScheduleConcurrencyForbid {
		if !manualPending && !nextRunAt.Time.After(now.Time) {
			next := agentScheduleNextRunAfterCreation(obj, nextRunAt, now, false)
			status.NextRunAt = &next
			nextRunAt = next
		}
		status.Phase = controlv1alpha1.AgentSchedulePhaseRunning
		status.LastError = ""
		message := fmt.Sprintf("Waiting for active AgentRun %s/%s to finish.", active.Namespace, active.Name)
		if manualPending {
			message = fmt.Sprintf("Manual run token %q is pending; waiting for active AgentRun %s/%s to finish.", manualToken, active.Namespace, active.Name)
		}
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               agentScheduleReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: obj.Generation,
			LastTransitionTime: now,
			Reason:             "RunActive",
			Message:            message,
		})
		obj.Status = status
		return r.patchAgentScheduleStatus(ctx, original, obj, agentSchedulePollInterval)
	}
	if policy == controlv1alpha1.AgentScheduleConcurrencyAllow {
		maxConcurrent := agentScheduleMaxConcurrentRuns(obj)
		if maxConcurrent > 0 && len(activeRuns) >= maxConcurrent {
			status.Phase = controlv1alpha1.AgentSchedulePhaseRunning
			status.LastError = ""
			message := fmt.Sprintf("Waiting for %d active AgentRuns to drop below maxConcurrentRuns=%d.", len(activeRuns), maxConcurrent)
			if manualPending {
				message = fmt.Sprintf("Manual run token %q is pending; waiting for %d active AgentRuns to drop below maxConcurrentRuns=%d.", manualToken, len(activeRuns), maxConcurrent)
			}
			apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
				Type:               agentScheduleReady,
				Status:             metav1.ConditionFalse,
				ObservedGeneration: obj.Generation,
				LastTransitionTime: now,
				Reason:             "MaxConcurrentRunsReached",
				Message:            message,
			})
			obj.Status = status
			return r.patchAgentScheduleStatus(ctx, original, obj, agentSchedulePollInterval)
		}
	}
	if !manualPending && nextRunAt.Time.After(now.Time) {
		if active != nil && policy == controlv1alpha1.AgentScheduleConcurrencyQueue {
			status.Phase = controlv1alpha1.AgentSchedulePhaseRunning
			status.LastError = ""
			maxConcurrent := agentScheduleMaxConcurrentRuns(obj)
			message := fmt.Sprintf("Waiting for %d non-terminal AgentRuns to drain; queue head is %s/%s. Next AgentRun is scheduled for %s.", len(activeRuns), active.Namespace, active.Name, nextRunAt.Time.UTC().Format(time.RFC3339))
			if maxConcurrent > 1 {
				message = fmt.Sprintf("Waiting for %d non-terminal AgentRuns to drain with maxConcurrentRuns=%d; queue head is %s/%s. Next AgentRun is scheduled for %s.", len(activeRuns), maxConcurrent, active.Namespace, active.Name, nextRunAt.Time.UTC().Format(time.RFC3339))
			}
			apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
				Type:               agentScheduleReady,
				Status:             metav1.ConditionFalse,
				ObservedGeneration: obj.Generation,
				LastTransitionTime: now,
				Reason:             "QueueActive",
				Message:            message,
			})
			obj.Status = status
			return r.patchAgentScheduleStatus(ctx, original, obj, agentSchedulePollInterval)
		}
		status.Phase = controlv1alpha1.AgentSchedulePhaseScheduled
		status.LastError = ""
		reason := "NextRunScheduled"
		message := fmt.Sprintf("Next AgentRun is scheduled for %s.", nextRunAt.Time.UTC().Format(time.RFC3339))
		if terminalBackoffApplied {
			reason = "TerminalBackoff"
			message = fmt.Sprintf("Newest AgentRun %s/%s is %s; automatic runs are backed off until %s.", last.Namespace, last.Name, last.Status.Phase, nextRunAt.Time.UTC().Format(time.RFC3339))
		}
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               agentScheduleReady,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: obj.Generation,
			LastTransitionTime: now,
			Reason:             reason,
			Message:            message,
		})
		obj.Status = status
		return r.patchAgentScheduleStatus(ctx, original, obj, nextRunAt.Time.Sub(now.Time))
	}

	// Interval ticks are idempotent per dueAt. Queue may re-reconcile immediately
	// after Owns(AgentRun) fires for the first create while status.nextRunAt is
	// still the same due time; without this guard Sequential rotation would spawn
	// a second template for the same interval (see #328).
	if !manualPending {
		dueMessage := agentScheduleIntervalTriggerMessage(obj, nextRunAt)
		existingDue, err := r.agentScheduleIntervalRunForMessage(ctx, obj, dueMessage)
		if err != nil {
			return ctrl.Result{}, err
		}
		if existingDue != nil {
			status.Phase = controlv1alpha1.AgentSchedulePhaseRunning
			if active != nil {
				status.ActiveRunRef = &controlv1alpha1.NamespacedObjectReference{Name: active.Name, Namespace: active.Namespace}
			} else if !agentRunPhaseTerminal(existingDue.Status.Phase) {
				status.ActiveRunRef = &controlv1alpha1.NamespacedObjectReference{Name: existingDue.Name, Namespace: existingDue.Namespace}
			}
			status.LastRunRef = &controlv1alpha1.NamespacedObjectReference{Name: existingDue.Name, Namespace: existingDue.Namespace}
			status.LastRunPhase = existingDue.Status.Phase
			if existingDue.Status.StartedAt != nil {
				status.LastRunAt = existingDue.Status.StartedAt
			} else if status.LastRunAt == nil {
				status.LastRunAt = &now
			}
			if template := existingDue.Labels[agentRunTemplateLabel]; template != "" {
				status.LastRunTemplate = template
			}
			next := agentScheduleNextRunAfterCreation(obj, nextRunAt, now, false)
			status.NextRunAt = &next
			status.LastError = ""
			message := fmt.Sprintf("Interval due at %s already created AgentRun %s/%s; advanced next run to %s.", nextRunAt.UTC().Format(time.RFC3339), existingDue.Namespace, existingDue.Name, next.UTC().Format(time.RFC3339))
			if active != nil && policy == controlv1alpha1.AgentScheduleConcurrencyQueue {
				message = fmt.Sprintf("Interval due at %s already created AgentRun %s/%s; waiting for queue head %s/%s. Next AgentRun is scheduled for %s.", nextRunAt.UTC().Format(time.RFC3339), existingDue.Namespace, existingDue.Name, active.Namespace, active.Name, next.UTC().Format(time.RFC3339))
			}
			apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
				Type:               agentScheduleReady,
				Status:             metav1.ConditionFalse,
				ObservedGeneration: obj.Generation,
				LastTransitionTime: now,
				Reason:             "IntervalAlreadyCreated",
				Message:            message,
			})
			obj.Status = status
			return r.patchAgentScheduleStatus(ctx, original, obj, agentSchedulePollInterval)
		}
	}

	request := agentScheduleIntervalRunRequest(obj, nextRunAt, lastIntervalTemplate)
	if manualPending {
		request = agentScheduleManualRunRequest(obj, manualToken, manualTemplateName, now)
	}
	request.ApplicationName = applicationName
	run, selectedTemplateName, err := r.createScheduledAgentRun(ctx, obj, now, request)
	if err != nil {
		return ctrl.Result{}, err
	}
	status.Phase = controlv1alpha1.AgentSchedulePhaseRunning
	activeForStatus := run
	if policy == controlv1alpha1.AgentScheduleConcurrencyQueue && active != nil {
		activeForStatus = active
	}
	status.ActiveRunRef = &controlv1alpha1.NamespacedObjectReference{Name: activeForStatus.Name, Namespace: activeForStatus.Namespace}
	status.LastRunRef = &controlv1alpha1.NamespacedObjectReference{Name: run.Name, Namespace: run.Namespace}
	if !agentRunPhaseTerminal(run.Status.Phase) && !agentScheduleRunInList(activeRuns, run) {
		status.ActiveRunCount = len(activeRuns) + 1
	}
	status.LastRunAt = &now
	status.LastRunTemplate = selectedTemplateName
	next := agentScheduleNextRunAfterCreation(obj, nextRunAt, now, manualPending)
	status.NextRunAt = &next
	if manualPending {
		status.LastManualRunToken = manualToken
	}
	status.LastError = ""
	message := fmt.Sprintf("Created scheduled AgentRun %s/%s.", run.Namespace, run.Name)
	if selectedTemplateName != "" {
		message = fmt.Sprintf("Created scheduled AgentRun %s/%s from template %q.", run.Namespace, run.Name, selectedTemplateName)
	}
	if manualPending {
		message = fmt.Sprintf("Created manually nudged AgentRun %s/%s for token %q.", run.Namespace, run.Name, manualToken)
		if selectedTemplateName != "" {
			message = fmt.Sprintf("Created manually nudged AgentRun %s/%s from template %q for token %q.", run.Namespace, run.Name, selectedTemplateName, manualToken)
		}
	}
	apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
		Type:               agentScheduleReady,
		Status:             metav1.ConditionFalse,
		ObservedGeneration: obj.Generation,
		LastTransitionTime: now,
		Reason:             "RunCreated",
		Message:            message,
	})
	obj.Status = status
	return r.patchAgentScheduleStatus(ctx, original, obj, agentSchedulePollInterval)
}

func (r *AgentScheduleReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("agentschedule").
		For(&controlv1alpha1.AgentSchedule{}).
		Owns(&controlv1alpha1.AgentRun{}).
		Complete(r)
}

func (r *AgentScheduleReconciler) patchAgentScheduleStatus(ctx context.Context, original, obj *controlv1alpha1.AgentSchedule, requeueAfter time.Duration) (ctrl.Result, error) {
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

func (r *AgentScheduleReconciler) agentScheduleRuns(ctx context.Context, schedule *controlv1alpha1.AgentSchedule) ([]*controlv1alpha1.AgentRun, *controlv1alpha1.AgentRun, string, error) {
	list := &controlv1alpha1.AgentRunList{}
	if err := r.List(ctx, list, client.InNamespace(schedule.Namespace), client.MatchingLabels{agentRunScheduleLabel: sanitizeLabelValue(schedule.Name)}); err != nil {
		return nil, nil, "", fmt.Errorf("list scheduled agent runs: %w", err)
	}
	active := []*controlv1alpha1.AgentRun{}
	var last *controlv1alpha1.AgentRun
	var lastInterval *controlv1alpha1.AgentRun
	for i := range list.Items {
		run := &list.Items[i]
		if !agentRunBelongsToSchedule(run, schedule) {
			continue
		}
		original := run.DeepCopy()
		if detachAgentRunControllerOwner(run, controlv1alpha1.GroupVersion.String(), "AgentSchedule", schedule.Name, schedule.UID) {
			if err := r.Patch(ctx, run, client.MergeFrom(original)); err != nil {
				return nil, nil, "", fmt.Errorf("detach durable AgentRun %s/%s from AgentSchedule garbage collection: %w", run.Namespace, run.Name, err)
			}
		}
		if last == nil || run.CreationTimestamp.After(last.CreationTimestamp.Time) {
			last = run
		}
		if run.Spec.Trigger.Reason == "ScheduledAgentRun" && (lastInterval == nil || agentRunPrecedes(lastInterval, run)) {
			lastInterval = run
		}
		if agentRunPhaseTerminal(run.Status.Phase) {
			continue
		}
		active = append(active, run)
	}
	lastIntervalTemplate := ""
	if lastInterval != nil {
		lastIntervalTemplate = lastInterval.Labels[agentRunTemplateLabel]
	}
	return active, last, lastIntervalTemplate, nil
}

func (r *AgentScheduleReconciler) createScheduledAgentRun(ctx context.Context, schedule *controlv1alpha1.AgentSchedule, now metav1.Time, request agentScheduleRunRequest) (*controlv1alpha1.AgentRun, string, error) {
	spec, templateName, err := agentScheduleSelectedRunTemplate(schedule, request.TemplateName)
	if err != nil {
		return nil, "", err
	}
	request.TemplateName = templateName
	applicationName := strings.TrimSpace(request.ApplicationName)
	if applicationName == "" && schedule.Spec.ApplicationRef != nil {
		applicationName = strings.TrimSpace(schedule.Spec.ApplicationRef.Name)
	}
	if applicationName != "" {
		if spec.Scope.ApplicationRef == nil {
			spec.Scope.ApplicationRef = &controlv1alpha1.ApplicationReferenceSpec{Name: applicationName}
		} else if strings.TrimSpace(spec.Scope.ApplicationRef.Name) != applicationName {
			return nil, "", fmt.Errorf("resolved schedule applicationRef %q conflicts with selected run template applicationRef %q", applicationName, strings.TrimSpace(spec.Scope.ApplicationRef.Name))
		}
	}
	if strings.TrimSpace(string(spec.Purpose)) == "" {
		spec.Purpose = controlv1alpha1.AgentRunPurposeScheduledHealthCheck
	}
	if strings.TrimSpace(spec.SourceRef.Kind) == "" {
		spec.SourceRef = controlv1alpha1.AgentRunSourceRef{
			APIVersion: controlv1alpha1.GroupVersion.String(),
			Kind:       "AgentSchedule",
			Namespace:  schedule.Namespace,
			Name:       schedule.Name,
		}
	}
	if strings.TrimSpace(spec.SourceUID) == "" {
		spec.SourceUID = string(schedule.UID)
	} else if spec.SourceUID != string(schedule.UID) {
		return nil, "", fmt.Errorf("selected run template sourceUID does not match AgentSchedule UID")
	}
	if spec.SourceGeneration == 0 {
		spec.SourceGeneration = schedule.Generation
	} else if spec.SourceGeneration != schedule.Generation {
		return nil, "", fmt.Errorf("selected run template sourceGeneration does not match AgentSchedule generation")
	}
	if request.ForceTrigger || strings.TrimSpace(spec.Trigger.Reason) == "" {
		spec.Trigger.Reason = request.TriggerReason
		spec.Trigger.Message = request.TriggerMessage
		spec.Trigger.DetectedAt = &now
	}
	if spec.ScheduleRef == nil {
		spec.ScheduleRef = &controlv1alpha1.NamespacedObjectReference{Name: schedule.Name, Namespace: schedule.Namespace}
	}
	nameTime := now
	if !request.NameTime.IsZero() {
		nameTime = request.NameTime
	}
	nameTimestamp := nameTime.UTC().Format("20060102-150405")
	if strings.TrimSpace(request.NameTimestamp) != "" {
		nameTimestamp = strings.TrimSpace(request.NameTimestamp)
	}
	nameIdentityKey := nameTime.Format(time.RFC3339Nano)
	if strings.TrimSpace(request.NameIdentityKey) != "" {
		nameIdentityKey = strings.TrimSpace(request.NameIdentityKey)
	}
	// Interval ScheduledAgentRun identity is one-per-due token: do not vary the
	// child name/hash by Sequential template. Template still labels the object
	// and selects harness content, but AlreadyExists must collide across
	// grok/composer for the same dueAt (see #328).
	nameTemplatePart := request.TemplateName
	nameHashTemplatePart := request.TemplateName
	if request.TriggerReason == "ScheduledAgentRun" && strings.TrimSpace(request.NameSegment) == "" {
		nameTemplatePart = ""
		nameHashTemplatePart = ""
	}
	nameHashInput := strings.Join([]string{nameIdentityKey, request.Token, request.TriggerReason, nameHashTemplatePart}, "|")
	name := agentRunChildName("agentrun", schedule.Name, request.NameSegment, nameTemplatePart, nameTimestamp, shortHash(nameHashInput))
	labels := map[string]string{
		agentRunScheduleLabel: sanitizeLabelValue(schedule.Name),
	}
	provenance := agentScheduleProvenanceLabels(schedule)
	for key, value := range provenance {
		labels[key] = value
	}
	if request.TemplateName != "" {
		labels[agentRunTemplateLabel] = sanitizeLabelValue(request.TemplateName)
	}
	run := &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: schedule.Namespace,
			Labels:    labels,
		},
		Spec: spec,
	}
	if err := r.Create(ctx, run); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return nil, "", err
		}
		if err := r.Get(ctx, client.ObjectKey{Namespace: run.Namespace, Name: run.Name}, run); err != nil {
			return nil, "", err
		}
	}
	return run, templateName, nil
}

func detachAgentRunControllerOwner(run *controlv1alpha1.AgentRun, apiVersion, kind, name string, uid types.UID) bool {
	if run == nil {
		return false
	}
	owners := run.GetOwnerReferences()
	filtered := make([]metav1.OwnerReference, 0, len(owners))
	removed := false
	for _, owner := range owners {
		if owner.Controller != nil && *owner.Controller && owner.APIVersion == apiVersion && owner.Kind == kind && owner.Name == name && owner.UID == uid {
			removed = true
			continue
		}
		filtered = append(filtered, owner)
	}
	if removed {
		run.SetOwnerReferences(filtered)
	}
	return removed
}

func agentScheduleProvenanceLabels(schedule *controlv1alpha1.AgentSchedule) map[string]string {
	if schedule == nil {
		return nil
	}
	labels := map[string]string{}
	for _, key := range []string{agentManagedByLabel, agentApplicationLabel, agentDynamicLabel, agentManagerRepositoryLabel} {
		if value := strings.TrimSpace(schedule.Labels[key]); value != "" {
			labels[key] = value
		}
	}
	return labels
}

func agentScheduleIntervalTriggerMessage(schedule *controlv1alpha1.AgentSchedule, dueAt metav1.Time) string {
	return fmt.Sprintf("AgentSchedule %s/%s reached its interval due at %s.", schedule.Namespace, schedule.Name, dueAt.UTC().Format(time.RFC3339))
}

func agentScheduleIntervalRunRequest(schedule *controlv1alpha1.AgentSchedule, dueAt metav1.Time, lastTemplateName string) agentScheduleRunRequest {
	return agentScheduleRunRequest{
		TemplateName:   agentScheduleNextTemplateName(schedule, lastTemplateName),
		TriggerReason:  "ScheduledAgentRun",
		TriggerMessage: agentScheduleIntervalTriggerMessage(schedule, dueAt),
		Token:          dueAt.Format(time.RFC3339Nano),
		NameTime:       dueAt,
	}
}

// agentScheduleIntervalRunForMessage returns the newest ScheduledAgentRun whose
// trigger message matches a due interval. Used to keep one create per dueAt when
// Queue re-reconciles before status.nextRunAt advances.
func (r *AgentScheduleReconciler) agentScheduleIntervalRunForMessage(ctx context.Context, schedule *controlv1alpha1.AgentSchedule, message string) (*controlv1alpha1.AgentRun, error) {
	message = strings.TrimSpace(message)
	if message == "" {
		return nil, nil
	}
	list := &controlv1alpha1.AgentRunList{}
	if err := r.List(ctx, list, client.InNamespace(schedule.Namespace), client.MatchingLabels{agentRunScheduleLabel: sanitizeLabelValue(schedule.Name)}); err != nil {
		return nil, fmt.Errorf("list scheduled agent runs for due interval: %w", err)
	}
	var match *controlv1alpha1.AgentRun
	for i := range list.Items {
		run := &list.Items[i]
		if !agentRunBelongsToSchedule(run, schedule) {
			continue
		}
		if run.Spec.Trigger.Reason != "ScheduledAgentRun" {
			continue
		}
		if strings.TrimSpace(run.Spec.Trigger.Message) != message {
			continue
		}
		if match == nil || agentRunPrecedes(match, run) {
			match = run
		}
	}
	return match, nil
}

func agentRunBelongsToSchedule(run *controlv1alpha1.AgentRun, schedule *controlv1alpha1.AgentSchedule) bool {
	if run == nil || schedule == nil || run.Spec.ScheduleRef == nil {
		return false
	}
	namespace := firstNonEmpty(strings.TrimSpace(run.Spec.ScheduleRef.Namespace), run.Namespace)
	if namespace != schedule.Namespace || strings.TrimSpace(run.Spec.ScheduleRef.Name) != schedule.Name {
		return false
	}
	return strings.TrimSpace(run.Spec.SourceUID) == "" || run.Spec.SourceUID == string(schedule.UID)
}

func agentScheduleManualRunRequest(schedule *controlv1alpha1.AgentSchedule, token, templateName string, now metav1.Time) agentScheduleRunRequest {
	return agentScheduleRunRequest{
		NameSegment:     "manual",
		NameTimestamp:   agentScheduleManualNameTimestamp(token, now),
		NameIdentityKey: "manual|" + strings.TrimSpace(token),
		TemplateName:    strings.TrimSpace(templateName),
		TriggerReason:   "ManualAgentScheduleNudge",
		TriggerMessage:  fmt.Sprintf("AgentSchedule %s/%s was manually nudged with token %q.", schedule.Namespace, schedule.Name, token),
		ForceTrigger:    true,
		Token:           strings.TrimSpace(token),
	}
}

func agentScheduleManualNameTimestamp(token string, fallback metav1.Time) string {
	token = strings.TrimSpace(token)
	for _, layout := range []string{"20060102T150405Z", time.RFC3339Nano, time.RFC3339} {
		if parsed, err := time.Parse(layout, token); err == nil {
			return parsed.UTC().Format("20060102-150405")
		}
	}
	if sanitized := sanitizeDNSLabel(token); sanitized != "" {
		if len(sanitized) > 24 {
			return sanitized[:24]
		}
		return sanitized
	}
	return fallback.UTC().Format("20060102-150405")
}

func agentScheduleManualRunToken(schedule *controlv1alpha1.AgentSchedule) string {
	if schedule == nil || schedule.Annotations == nil {
		return ""
	}
	return strings.TrimSpace(schedule.Annotations[controlv1alpha1.AgentScheduleRunNowAnnotation])
}

func agentScheduleManualRunTemplateName(schedule *controlv1alpha1.AgentSchedule) string {
	if schedule == nil || schedule.Annotations == nil {
		return ""
	}
	return strings.TrimSpace(schedule.Annotations[controlv1alpha1.AgentScheduleRunTemplateAnnotation])
}

func agentScheduleNextTemplateName(schedule *controlv1alpha1.AgentSchedule, lastTemplateName string) string {
	if schedule == nil || len(schedule.Spec.RunTemplates) == 0 {
		return ""
	}
	if strings.TrimSpace(lastTemplateName) == "" {
		return schedule.Spec.RunTemplates[0].Name
	}
	for index, template := range schedule.Spec.RunTemplates {
		if template.Name == lastTemplateName {
			return schedule.Spec.RunTemplates[(index+1)%len(schedule.Spec.RunTemplates)].Name
		}
	}
	return schedule.Spec.RunTemplates[0].Name
}

func agentScheduleSelectedRunTemplate(schedule *controlv1alpha1.AgentSchedule, templateName string) (controlv1alpha1.AgentRunSpec, string, error) {
	templateName = strings.TrimSpace(templateName)
	if len(schedule.Spec.RunTemplates) == 0 {
		if templateName != "" {
			return controlv1alpha1.AgentRunSpec{}, "", fmt.Errorf("run template %q was requested, but spec.runTemplates is empty", templateName)
		}
		return schedule.Spec.RunTemplate, "", nil
	}
	if templateName == "" {
		templateName = schedule.Spec.RunTemplates[0].Name
	}
	for _, template := range schedule.Spec.RunTemplates {
		if template.Name == templateName {
			return template.Template, template.Name, nil
		}
	}
	return controlv1alpha1.AgentRunSpec{}, "", fmt.Errorf("run template %q was not found in AgentSchedule %s/%s", templateName, schedule.Namespace, schedule.Name)
}

func agentScheduleNextRunTime(schedule *controlv1alpha1.AgentSchedule, status controlv1alpha1.AgentScheduleStatus, now time.Time, generationChanged bool) metav1.Time {
	if !generationChanged && status.NextRunAt != nil {
		return *status.NextRunAt
	}
	if status.LastRunAt != nil {
		return metav1.NewTime(status.LastRunAt.Add(time.Duration(schedule.Spec.IntervalSeconds) * time.Second))
	}
	if schedule.Spec.InitialDelaySeconds > 0 {
		return metav1.NewTime(schedule.CreationTimestamp.Add(time.Duration(schedule.Spec.InitialDelaySeconds) * time.Second))
	}
	return metav1.NewTime(now)
}

func agentScheduleNextRunWithTerminalBackoff(schedule *controlv1alpha1.AgentSchedule, newest *controlv1alpha1.AgentRun, existing metav1.Time) (metav1.Time, bool) {
	if schedule == nil || schedule.Spec.Backoff == nil || newest == nil {
		return existing, false
	}

	seconds := 0
	switch newest.Status.Phase {
	case controlv1alpha1.AgentRunPhaseFailed:
		seconds = schedule.Spec.Backoff.FailedSeconds
	case controlv1alpha1.AgentRunPhaseNeedsHuman:
		seconds = schedule.Spec.Backoff.NeedsHumanSeconds
	default:
		return existing, false
	}
	if seconds <= 0 {
		return existing, false
	}

	terminalAt := newest.CreationTimestamp.Time
	if newest.Status.CompletedAt != nil && !newest.Status.CompletedAt.IsZero() {
		terminalAt = newest.Status.CompletedAt.Time
	}
	if terminalAt.IsZero() {
		return existing, false
	}
	backoffUntil := metav1.NewTime(terminalAt.Add(time.Duration(seconds) * time.Second))
	if backoffUntil.After(existing.Time) {
		return backoffUntil, true
	}
	return existing, backoffUntil.Equal(&existing)
}

func agentScheduleNextRunAfterCreation(schedule *controlv1alpha1.AgentSchedule, scheduledNext, now metav1.Time, manual bool) metav1.Time {
	if manual {
		return scheduledNext
	}
	interval := time.Duration(schedule.Spec.IntervalSeconds) * time.Second
	next := scheduledNext.Time
	for !next.After(now.Time) {
		next = next.Add(interval)
	}
	return metav1.NewTime(next)
}

func agentScheduleConcurrencyPolicy(schedule *controlv1alpha1.AgentSchedule) controlv1alpha1.AgentScheduleConcurrencyPolicy {
	if schedule.Spec.ConcurrencyPolicy == "" {
		return controlv1alpha1.AgentScheduleConcurrencyForbid
	}
	return schedule.Spec.ConcurrencyPolicy
}

func agentScheduleMaxConcurrentRuns(schedule *controlv1alpha1.AgentSchedule) int {
	if schedule == nil {
		return 0
	}
	policy := agentScheduleConcurrencyPolicy(schedule)
	if policy == controlv1alpha1.AgentScheduleConcurrencyForbid {
		return 1
	}
	if schedule.Spec.MaxConcurrentRuns <= 0 {
		if policy == controlv1alpha1.AgentScheduleConcurrencyQueue {
			return 1
		}
		return 0
	}
	return schedule.Spec.MaxConcurrentRuns
}

func agentScheduleActiveRunForStatus(policy controlv1alpha1.AgentScheduleConcurrencyPolicy, runs []*controlv1alpha1.AgentRun) *controlv1alpha1.AgentRun {
	if policy == controlv1alpha1.AgentScheduleConcurrencyQueue {
		return agentScheduleOldestRun(runs)
	}
	return agentScheduleNewestRun(runs)
}

func agentScheduleOldestRun(runs []*controlv1alpha1.AgentRun) *controlv1alpha1.AgentRun {
	var selected *controlv1alpha1.AgentRun
	for _, run := range runs {
		if run == nil {
			continue
		}
		// Use the same ordering as the AgentRun launch gate so same-second
		// creationTimestamp ties resolve by name, not list order.
		if selected == nil || agentRunPrecedes(run, selected) {
			selected = run
		}
	}
	return selected
}

func agentScheduleNewestRun(runs []*controlv1alpha1.AgentRun) *controlv1alpha1.AgentRun {
	var selected *controlv1alpha1.AgentRun
	for _, run := range runs {
		if run == nil {
			continue
		}
		if selected == nil || agentRunPrecedes(selected, run) {
			selected = run
		}
	}
	return selected
}

func agentScheduleRunInList(runs []*controlv1alpha1.AgentRun, candidate *controlv1alpha1.AgentRun) bool {
	if candidate == nil {
		return false
	}
	for _, run := range runs {
		if run == nil {
			continue
		}
		if run.Namespace == candidate.Namespace && run.Name == candidate.Name {
			return true
		}
	}
	return false
}
