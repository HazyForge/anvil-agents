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
	agentRunControlReady        = "Ready"
	agentRunControlPollInterval = 30 * time.Second
)

// +kubebuilder:rbac:groups="control.anvil.hazyforge.io",resources=agentruncontrols,verbs=create;delete;get;list;patch;update;watch
// +kubebuilder:rbac:groups="control.anvil.hazyforge.io",resources=agentruncontrols/status,verbs=get;patch;update
// +kubebuilder:rbac:groups="control.anvil.hazyforge.io",resources=agentrunprofiles;agentruns;agentschedules,verbs=get;list;watch
type AgentRunControlReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

func (r *AgentRunControlReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	obj := &controlv1alpha1.AgentRunControl{}
	if err := r.Get(ctx, req.NamespacedName, obj); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !obj.GetDeletionTimestamp().IsZero() {
		return ctrl.Result{}, nil
	}

	original := obj.DeepCopy()
	status := obj.Status
	status.ObservedGeneration = obj.Generation
	status.AffectedScheduleCount = 0
	status.PendingRunCount = 0
	status.ActiveRunCount = 0
	now := time.Now()
	conditionTime := metav1.NewTime(now)
	applicationName := strings.TrimSpace(obj.Spec.ApplicationRef.Name)

	if applicationName == "" {
		status.Phase = controlv1alpha1.AgentRunControlPhaseBlocked
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               agentRunControlReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: obj.Generation,
			LastTransitionTime: conditionTime,
			Reason:             "ApplicationRequired",
			Message:            "spec.applicationRef.name is required.",
		})
		obj.Status = status
		return r.patchAgentRunControlStatus(ctx, original, obj, 0)
	}
	if err := r.countAgentRunControlSubjects(ctx, applicationName, &status); err != nil {
		return ctrl.Result{}, err
	}

	requeueAfter := agentRunControlPollInterval
	conditionStatus := metav1.ConditionTrue
	reason := "LaunchAllowed"
	message := fmt.Sprintf("AgentRun launches for Application %q are allowed.", applicationName)
	switch {
	case agentRunControlExpired(obj, now):
		status.Phase = controlv1alpha1.AgentRunControlPhaseExpired
		reason = "ControlExpired"
		message = fmt.Sprintf("AgentRunControl expired; launches for Application %q are allowed.", applicationName)
	case obj.Spec.LaunchPolicy == controlv1alpha1.AgentRunControlLaunchPolicyPaused && strings.TrimSpace(obj.Spec.Reason) == "":
		status.Phase = controlv1alpha1.AgentRunControlPhaseBlocked
		conditionStatus = metav1.ConditionFalse
		reason = "PauseReasonRequired"
		message = "spec.reason is required when launchPolicy is Paused."
	case obj.Spec.LaunchPolicy == controlv1alpha1.AgentRunControlLaunchPolicyPaused:
		status.Phase = controlv1alpha1.AgentRunControlPhasePaused
		reason = "LaunchPaused"
		message = fmt.Sprintf("AgentRun launches for Application %q are paused: %s", applicationName, strings.TrimSpace(obj.Spec.Reason))
		if obj.Spec.ExpiresAt != nil {
			untilExpiry := time.Until(obj.Spec.ExpiresAt.Time)
			if untilExpiry > 0 && untilExpiry < requeueAfter {
				requeueAfter = untilExpiry
			}
		}
	case obj.Spec.LaunchPolicy == controlv1alpha1.AgentRunControlLaunchPolicyAllowed:
		status.Phase = controlv1alpha1.AgentRunControlPhaseAllowed
	default:
		status.Phase = controlv1alpha1.AgentRunControlPhaseBlocked
		conditionStatus = metav1.ConditionFalse
		reason = "InvalidLaunchPolicy"
		message = fmt.Sprintf("Unsupported launchPolicy %q.", obj.Spec.LaunchPolicy)
	}
	apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
		Type:               agentRunControlReady,
		Status:             conditionStatus,
		ObservedGeneration: obj.Generation,
		LastTransitionTime: conditionTime,
		Reason:             reason,
		Message:            message,
	})
	obj.Status = status
	return r.patchAgentRunControlStatus(ctx, original, obj, requeueAfter)
}

func (r *AgentRunControlReconciler) countAgentRunControlSubjects(ctx context.Context, applicationName string, status *controlv1alpha1.AgentRunControlStatus) error {
	schedules := &controlv1alpha1.AgentScheduleList{}
	if err := r.List(ctx, schedules); err != nil {
		return fmt.Errorf("list AgentSchedules for AgentRunControl: %w", err)
	}
	for i := range schedules.Items {
		name, err := resolveAgentScheduleApplicationName(ctx, r.Client, &schedules.Items[i])
		if err == nil && name == applicationName {
			status.AffectedScheduleCount++
		}
	}

	runs := &controlv1alpha1.AgentRunList{}
	if err := r.List(ctx, runs); err != nil {
		return fmt.Errorf("list AgentRuns for AgentRunControl: %w", err)
	}
	for i := range runs.Items {
		run := &runs.Items[i]
		if agentRunPhaseTerminal(run.Status.Phase) {
			continue
		}
		name, err := resolveAgentRunApplicationName(ctx, r.Client, run)
		if err != nil || name != applicationName {
			continue
		}
		if run.Status.JobRef == nil {
			status.PendingRunCount++
		} else {
			status.ActiveRunCount++
		}
	}
	return nil
}

func (r *AgentRunControlReconciler) patchAgentRunControlStatus(ctx context.Context, original, obj *controlv1alpha1.AgentRunControl, requeueAfter time.Duration) (ctrl.Result, error) {
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

func (r *AgentRunControlReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("agentruncontrol").
		For(&controlv1alpha1.AgentRunControl{}).
		Complete(r)
}

func agentRunControlExpired(control *controlv1alpha1.AgentRunControl, now time.Time) bool {
	return control != nil && control.Spec.ExpiresAt != nil && !now.Before(control.Spec.ExpiresAt.Time)
}

type activeAgentRunPause struct {
	ControlName string
	Reason      string
	ExpiresAt   *metav1.Time
}

func activeAgentRunPauseForApplication(ctx context.Context, c client.Reader, applicationName string, now time.Time) (*activeAgentRunPause, error) {
	applicationName = strings.TrimSpace(applicationName)
	if applicationName == "" {
		return nil, nil
	}
	// ApplicationRef is an opaque stable scope key. No Application CRD is
	// required, and a missing external inventory object cannot fail open a hold.
	controls := &controlv1alpha1.AgentRunControlList{}
	if err := c.List(ctx, controls); err != nil {
		return nil, fmt.Errorf("list AgentRunControls: %w", err)
	}
	var selected *controlv1alpha1.AgentRunControl
	for i := range controls.Items {
		control := &controls.Items[i]
		if strings.TrimSpace(control.Spec.ApplicationRef.Name) != applicationName || control.Spec.LaunchPolicy != controlv1alpha1.AgentRunControlLaunchPolicyPaused || strings.TrimSpace(control.Spec.Reason) == "" || agentRunControlExpired(control, now) {
			continue
		}
		if selected == nil || control.Name < selected.Name {
			selected = control
		}
	}
	if selected == nil {
		return nil, nil
	}
	return &activeAgentRunPause{
		ControlName: selected.Name,
		Reason:      strings.TrimSpace(selected.Spec.Reason),
		ExpiresAt:   copyMetav1Time(selected.Spec.ExpiresAt),
	}, nil
}

func copyMetav1Time(value *metav1.Time) *metav1.Time {
	if value == nil {
		return nil
	}
	out := value.DeepCopy()
	return out
}

func agentRunPauseRequeueAfter(pause *activeAgentRunPause, now time.Time) time.Duration {
	requeueAfter := agentRunControlPollInterval
	if pause != nil && pause.ExpiresAt != nil {
		untilExpiry := pause.ExpiresAt.Sub(now)
		if untilExpiry > 0 && untilExpiry < requeueAfter {
			requeueAfter = untilExpiry
		}
	}
	return requeueAfter
}

func resolveAgentScheduleApplicationName(ctx context.Context, c client.Reader, schedule *controlv1alpha1.AgentSchedule) (string, error) {
	if schedule == nil {
		return "", nil
	}
	explicit := ""
	if schedule.Spec.ApplicationRef != nil {
		explicit = strings.TrimSpace(schedule.Spec.ApplicationRef.Name)
		if explicit == "" {
			return "", fmt.Errorf("spec.applicationRef.name is required when applicationRef is set")
		}
	}

	templates := []controlv1alpha1.AgentRunSpec{schedule.Spec.RunTemplate}
	if len(schedule.Spec.RunTemplates) > 0 {
		templates = make([]controlv1alpha1.AgentRunSpec, 0, len(schedule.Spec.RunTemplates))
		for i := range schedule.Spec.RunTemplates {
			templates = append(templates, schedule.Spec.RunTemplates[i].Template)
		}
	}
	resolved := ""
	for i := range templates {
		name, err := resolveAgentRunSpecApplicationNameStrict(ctx, c, schedule.Namespace, &templates[i])
		if err != nil {
			return "", fmt.Errorf("resolve run template %d application: %w", i, err)
		}
		if name == "" {
			continue
		}
		if explicit != "" && name != explicit {
			return "", fmt.Errorf("spec.applicationRef %q conflicts with run template applicationRef %q", explicit, name)
		}
		if resolved != "" && name != resolved {
			return "", fmt.Errorf("run templates resolve to multiple Applications %q and %q", resolved, name)
		}
		resolved = name
	}
	if explicit != "" {
		return explicit, nil
	}
	return resolved, nil
}

func resolveAgentRunSpecApplicationName(ctx context.Context, c client.Reader, namespace string, spec *controlv1alpha1.AgentRunSpec) (string, error) {
	return resolveAgentRunSpecApplicationNameWithProfilePolicy(ctx, c, namespace, spec, false)
}

func resolveAgentRunSpecApplicationNameStrict(ctx context.Context, c client.Reader, namespace string, spec *controlv1alpha1.AgentRunSpec) (string, error) {
	return resolveAgentRunSpecApplicationNameWithProfilePolicy(ctx, c, namespace, spec, true)
}

func resolveAgentRunSpecApplicationNameWithProfilePolicy(ctx context.Context, c client.Reader, namespace string, spec *controlv1alpha1.AgentRunSpec, requireProfile bool) (string, error) {
	if spec == nil {
		return "", nil
	}
	direct := ""
	if spec.Scope.ApplicationRef != nil {
		direct = strings.TrimSpace(spec.Scope.ApplicationRef.Name)
	}
	if spec.ProfileRef == nil || strings.TrimSpace(spec.ProfileRef.Name) == "" {
		return direct, nil
	}
	profileNamespace := firstNonEmpty(strings.TrimSpace(spec.ProfileRef.Namespace), namespace)
	if profileNamespace != namespace {
		if !requireProfile {
			return direct, nil
		}
		return "", fmt.Errorf("profileRef must reference an AgentRunProfile in namespace %q", namespace)
	}
	profile := &controlv1alpha1.AgentRunProfile{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: profileNamespace, Name: strings.TrimSpace(spec.ProfileRef.Name)}, profile); err != nil {
		if apierrors.IsNotFound(err) && !requireProfile {
			return direct, nil
		}
		return "", fmt.Errorf("get AgentRunProfile %s/%s: %w", profileNamespace, strings.TrimSpace(spec.ProfileRef.Name), err)
	}
	profileApplication := ""
	if profile.Spec.Scope.ApplicationRef != nil {
		profileApplication = strings.TrimSpace(profile.Spec.Scope.ApplicationRef.Name)
	}
	if direct != "" && profileApplication != "" && direct != profileApplication {
		return "", fmt.Errorf("run scope applicationRef %q conflicts with AgentRunProfile applicationRef %q", direct, profileApplication)
	}
	return firstNonEmpty(direct, profileApplication), nil
}

func resolveAgentRunApplicationName(ctx context.Context, c client.Reader, obj *controlv1alpha1.AgentRun) (string, error) {
	if obj == nil {
		return "", nil
	}
	return resolveAgentRunSpecApplicationName(ctx, c, obj.Namespace, &obj.Spec)
}
