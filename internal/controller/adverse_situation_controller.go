package controller

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	controlv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

const (
	adverseSituationReady                         = "Ready"
	adverseSituationDefaultName                   = "adverse-default"
	adverseSituationDefaultGroupKey               = "namespace/default"
	adverseSituationDefaultQuietPeriodSeconds     = 300
	adverseSituationDefaultDedupeWindowSeconds    = 120
	adverseSituationDefaultPullRequestHoldSeconds = 900
	adverseSituationDefaultMaxEvents              = 80
	adverseSituationHardMaxEvents                 = 200
	adverseSituationMaxReportIDsPerEvent          = 64
	adverseSituationPollInterval                  = 15 * time.Second

	adverseSituationLabel      = "control.anvil.hazyforge.io/adverse-situation"
	adverseSituationGroupLabel = "control.anvil.hazyforge.io/adverse-group"
)

// +kubebuilder:rbac:groups="control.anvil.hazyforge.io",resources=adversesituations,verbs=create;delete;get;list;patch;update;watch
// +kubebuilder:rbac:groups="control.anvil.hazyforge.io",resources=adversesituations/status,verbs=get;patch;update
// +kubebuilder:rbac:groups="control.anvil.hazyforge.io",resources=adversesituations/finalizers,verbs=update
// +kubebuilder:rbac:groups="control.anvil.hazyforge.io",resources=agentruns,verbs=create;get;list;patch;update;watch
type AdverseSituationReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

type AdverseSituationTriggerReconciler struct {
	client.Client
	Scheme  *runtime.Scheme
	GVK     schema.GroupVersionKind
	Sources []AdverseSourceConfig
}

func (r *AdverseSituationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	obj := &controlv1alpha1.AdverseSituation{}
	if err := r.Get(ctx, req.NamespacedName, obj); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !obj.GetDeletionTimestamp().IsZero() {
		return ctrl.Result{}, nil
	}
	if err := r.detachAdverseSituationAgentRunOwners(ctx, obj); err != nil {
		return ctrl.Result{}, err
	}

	original := obj.DeepCopy()
	status := obj.Status
	status.ObservedGeneration = obj.Generation
	now := metav1.Now()

	var run *controlv1alpha1.AgentRun
	var err error
	if len(status.Events) > 0 && status.Phase != controlv1alpha1.AdverseSituationPhaseResolved {
		run, err = r.ensureAdverseSituationAgentRun(ctx, obj, &status)
		if err != nil {
			return ctrl.Result{}, err
		}
	}
	if run != nil && strings.TrimSpace(run.Status.PullRequestURL) != "" {
		status.PullRequestURL = strings.TrimSpace(run.Status.PullRequestURL)
		if status.PullRequestObservedAt == nil {
			status.PullRequestObservedAt = &now
			holdUntil := metav1.NewTime(now.Add(time.Duration(adverseSituationPullRequestHoldSeconds(obj)) * time.Second))
			status.PullRequestQuietUntil = &holdUntil
		}
	}

	requeueAfter := adverseSituationPollInterval
	switch {
	case len(status.Events) == 0:
		status.Phase = controlv1alpha1.AdverseSituationPhasePending
		status.Summary = "No adverse events have been recorded."
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               adverseSituationReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: obj.Generation,
			LastTransitionTime: now,
			Reason:             "NoEvents",
			Message:            status.Summary,
		})
	case adverseSituationCanResolve(status, run, now.Time):
		status.Phase = controlv1alpha1.AdverseSituationPhaseResolved
		status.ResolvedAt = &now
		status.Summary = "Adverse stream stayed quiet for the configured resolution window."
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               adverseSituationReady,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: obj.Generation,
			LastTransitionTime: now,
			Reason:             "QuietWindowElapsed",
			Message:            status.Summary,
		})
		requeueAfter = 0
	default:
		status.Phase = controlv1alpha1.AdverseSituationPhaseQuieting
		status.Summary = adverseSituationOpenSummary(status)
		apimeta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               adverseSituationReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: obj.Generation,
			LastTransitionTime: now,
			Reason:             "AdverseStreamOpen",
			Message:            status.Summary,
		})
		requeueAfter = adverseSituationNextRequeue(status, now.Time)
	}

	obj.Status = status
	return r.patchAdverseSituationStatus(ctx, original, obj, requeueAfter)
}

func (r *AdverseSituationReconciler) ensureAdverseSituationAgentRun(ctx context.Context, situation *controlv1alpha1.AdverseSituation, status *controlv1alpha1.AdverseSituationStatus) (*controlv1alpha1.AgentRun, error) {
	if !adverseSituationAgentRunEnabled(situation) {
		return nil, nil
	}
	if status.Sequence <= 0 {
		status.Sequence = 1
	}
	name := agentRunChildName("agrun", situation.Name, fmt.Sprintf("%d", status.Sequence), shortHash(string(situation.UID)))
	hasResponderRef := status.ActiveResponderRef != nil && strings.TrimSpace(status.ActiveResponderRef.Name) != ""
	established := hasResponderRef && strings.TrimSpace(status.ActiveResponderUID) != "" && strings.TrimSpace(status.ActiveResponderDigest) != ""
	if hasResponderRef {
		name = strings.TrimSpace(status.ActiveResponderRef.Name)
	}
	run := &controlv1alpha1.AgentRun{}
	key := client.ObjectKey{Namespace: situation.Namespace, Name: name}
	if err := r.Get(ctx, key, run); err != nil {
		if client.IgnoreNotFound(err) != nil {
			return nil, err
		}
		if hasResponderRef {
			return nil, fmt.Errorf("recorded adverse responder AgentRun %s/%s no longer exists", key.Namespace, key.Name)
		}
		run = adverseSituationAgentRunFor(situation, name)
		if err := r.Create(ctx, run); err != nil {
			return nil, err
		}
	}
	if established {
		if !adverseSituationAgentRunStableIdentityMatches(run, situation) {
			return nil, fmt.Errorf("AgentRun %s/%s no longer matches the established adverse responder identity for AdverseSituation UID %s", run.Namespace, run.Name, situation.UID)
		}
		digest := digestJSON(run.Spec)
		if status.ActiveResponderUID != string(run.UID) {
			return nil, fmt.Errorf("AgentRun %s/%s UID %s does not match the established adverse responder UID %s", run.Namespace, run.Name, run.UID, status.ActiveResponderUID)
		}
		if status.ActiveResponderDigest != digest {
			return nil, fmt.Errorf("AgentRun %s/%s spec no longer matches the established adverse responder digest", run.Namespace, run.Name)
		}
		status.ActiveResponderUID = string(run.UID)
		status.ActiveResponderDigest = digest
	} else if hasResponderRef {
		if strings.TrimSpace(status.ActiveResponderUID) != "" || strings.TrimSpace(status.ActiveResponderDigest) != "" {
			return nil, fmt.Errorf("AdverseSituation %s/%s has an incomplete established responder receipt", situation.Namespace, situation.Name)
		}
		if !adverseSituationAgentRunMatches(run, situation) {
			return nil, fmt.Errorf("legacy adverse responder AgentRun %s/%s has no receipt and no longer exactly matches the current creation snapshot", run.Namespace, run.Name)
		}
	} else if !adverseSituationAgentRunMatches(run, situation) {
		return nil, fmt.Errorf("AgentRun %s/%s collides with adverse responder identity for AdverseSituation UID %s", run.Namespace, run.Name, situation.UID)
	}
	status.ActiveResponderRef = &controlv1alpha1.NamespacedObjectReference{Name: run.Name, Namespace: run.Namespace}
	status.ActiveResponderUID = string(run.UID)
	status.ActiveResponderDigest = digestJSON(run.Spec)
	return run, nil
}

func adverseSituationAgentRunMatches(run *controlv1alpha1.AgentRun, situation *controlv1alpha1.AdverseSituation) bool {
	if run == nil || situation == nil {
		return false
	}
	if !(run.Namespace == situation.Namespace &&
		run.Spec.Purpose == controlv1alpha1.AgentRunPurposeAdverseSituation &&
		run.Spec.SourceRef.APIVersion == controlv1alpha1.GroupVersion.String() &&
		run.Spec.SourceRef.Kind == "AdverseSituation" &&
		run.Spec.SourceRef.Namespace == situation.Namespace &&
		run.Spec.SourceRef.Name == situation.Name &&
		run.Spec.SourceUID == string(situation.UID) &&
		run.Spec.SituationRef != nil &&
		run.Spec.SituationRef.Name == situation.Name &&
		firstNonEmpty(run.Spec.SituationRef.Namespace, run.Namespace) == situation.Namespace) {
		return false
	}
	expected := adverseSituationAgentRunFor(situation, run.Name)
	if !apiequality.Semantic.DeepEqual(run.Spec, expected.Spec) {
		return false
	}
	for key, value := range expected.Labels {
		if run.Labels[key] != value {
			return false
		}
	}
	return true
}

func adverseSituationAgentRunStableIdentityMatches(run *controlv1alpha1.AgentRun, situation *controlv1alpha1.AdverseSituation) bool {
	if run == nil || situation == nil {
		return false
	}
	if !(run.Namespace == situation.Namespace &&
		run.Spec.Purpose == controlv1alpha1.AgentRunPurposeAdverseSituation &&
		run.Spec.SourceRef.APIVersion == controlv1alpha1.GroupVersion.String() &&
		run.Spec.SourceRef.Kind == "AdverseSituation" &&
		run.Spec.SourceRef.Namespace == situation.Namespace &&
		run.Spec.SourceRef.Name == situation.Name &&
		run.Spec.SourceUID == string(situation.UID) &&
		run.Spec.SituationRef != nil &&
		run.Spec.SituationRef.Name == situation.Name &&
		firstNonEmpty(run.Spec.SituationRef.Namespace, run.Namespace) == situation.Namespace) {
		return false
	}
	return run.Labels[adverseSituationLabel] == sanitizeLabelValue(situation.Name)
}

func (r *AdverseSituationReconciler) detachAdverseSituationAgentRunOwners(ctx context.Context, situation *controlv1alpha1.AdverseSituation) error {
	runs := &controlv1alpha1.AgentRunList{}
	if err := r.List(ctx, runs, client.InNamespace(situation.Namespace), client.MatchingLabels{adverseSituationLabel: sanitizeLabelValue(situation.Name)}); err != nil {
		return fmt.Errorf("list adverse responder AgentRuns for durable-history migration: %w", err)
	}
	for index := range runs.Items {
		run := &runs.Items[index]
		original := run.DeepCopy()
		if !detachAgentRunControllerOwner(run, controlv1alpha1.GroupVersion.String(), "AdverseSituation", situation.Name, situation.UID) {
			continue
		}
		if err := r.Patch(ctx, run, client.MergeFrom(original)); err != nil {
			return fmt.Errorf("detach durable AgentRun %s/%s from AdverseSituation garbage collection: %w", run.Namespace, run.Name, err)
		}
	}
	return nil
}

func (r *AdverseSituationReconciler) patchAdverseSituationStatus(ctx context.Context, original, obj *controlv1alpha1.AdverseSituation, requeueAfter time.Duration) (ctrl.Result, error) {
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

func (r *AdverseSituationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("adversesituation").
		For(&controlv1alpha1.AdverseSituation{}).
		Owns(&controlv1alpha1.AgentRun{}).
		Complete(r)
}

func (r *AdverseSituationTriggerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	source := &unstructured.Unstructured{}
	source.SetGroupVersionKind(r.GVK)
	if err := r.Get(ctx, req.NamespacedName, source); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !source.GetDeletionTimestamp().IsZero() {
		return ctrl.Result{}, nil
	}

	recordedRoutes := map[string]struct{}{}
	for _, integration := range r.Sources {
		if !adverseSourceConfigMatches(integration, source) {
			continue
		}
		trigger, ok := agentRunTriggerForSourceWithClassifier(source, integration.Classifier)
		if !ok {
			continue
		}
		namespace, name, _ := adverseSituationRouteForSource(source, integration)
		routeKey := namespace + "/" + name + "/" + adverseSituationEventID(source, trigger)
		if _, recorded := recordedRoutes[routeKey]; recorded {
			continue
		}
		recordedRoutes[routeKey] = struct{}{}
		if result, err := r.recordSource(ctx, source, integration, trigger); err != nil || result.Requeue || result.RequeueAfter > 0 {
			return result, err
		}
	}
	return ctrl.Result{}, nil
}

func (r *AdverseSituationTriggerReconciler) recordSource(ctx context.Context, source *unstructured.Unstructured, integration AdverseSourceConfig, trigger controlv1alpha1.AgentRunTriggerSnapshot) (ctrl.Result, error) {
	namespace, name, groupKey := adverseSituationRouteForSource(source, integration)
	situation := &controlv1alpha1.AdverseSituation{}
	key := client.ObjectKey{Namespace: namespace, Name: name}
	if err := r.Get(ctx, key, situation); err != nil {
		if client.IgnoreNotFound(err) != nil {
			return ctrl.Result{}, err
		}
		situation = defaultAdverseSituation(namespace, name)
		situation.Spec.GroupKey = groupKey
		situation.Labels[adverseSituationGroupLabel] = sanitizeLabelValue(groupKey)
		if err := r.Create(ctx, situation); err != nil && !apierrors.IsAlreadyExists(err) {
			return ctrl.Result{}, err
		}
		if err := r.Get(ctx, key, situation); err != nil {
			return ctrl.Result{}, err
		}
	}

	original := situation.DeepCopy()
	status := situation.Status
	if !adverseSituationRecordEvent(source, trigger, adverseSituationBuffer(situation), &status) {
		return ctrl.Result{RequeueAfter: adverseSituationPollInterval}, nil
	}
	status.ObservedGeneration = situation.Generation
	situation.Status = status
	if err := r.Status().Patch(ctx, situation, client.MergeFrom(original)); err != nil {
		if apierrors.IsConflict(err) {
			return ctrl.Result{Requeue: true}, nil
		}
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *AdverseSituationTriggerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	source := &unstructured.Unstructured{}
	source.SetGroupVersionKind(r.GVK)
	return ctrl.NewControllerManagedBy(mgr).
		Named("adverse-trigger-" + strings.ToLower(sanitizeLabelValue(r.GVK.Kind+"-"+shortHash(r.GVK.String())))).
		For(source).
		Complete(r)
}

func SetupAdverseSituationTriggerReconcilers(mgr ctrl.Manager, configured []string, integrations []AdverseSourceConfig) error {
	byGVK := map[string][]AdverseSourceConfig{}
	gvks := map[string]schema.GroupVersionKind{}
	structuredGVKs := map[string]struct{}{}
	for _, integration := range integrations {
		gvk, err := adverseSourceGVK(integration)
		if err != nil {
			return err
		}
		key := gvk.String()
		gvks[key] = gvk
		byGVK[key] = append(byGVK[key], integration)
		structuredGVKs[key] = struct{}{}
	}
	for _, value := range configured {
		gvk, err := parseAdverseSourceGVK(value)
		if err != nil {
			return err
		}
		key := gvk.String()
		if _, replaced := structuredGVKs[key]; replaced {
			continue
		}
		gvks[key] = gvk
		byGVK[key] = append(byGVK[key], AdverseSourceConfig{APIVersion: gvk.GroupVersion().String(), Kind: gvk.Kind})
	}
	keys := make([]string, 0, len(byGVK))
	for key := range byGVK {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		gvk := gvks[key]
		reconciler := &AdverseSituationTriggerReconciler{
			Client:  mgr.GetClient(),
			Scheme:  mgr.GetScheme(),
			GVK:     gvk,
			Sources: byGVK[key],
		}
		if err := reconciler.SetupWithManager(mgr); err != nil {
			return fmt.Errorf("setup AdverseSituation trigger for %s: %w", gvk.String(), err)
		}
	}
	return nil
}

func adverseSourceConfigMatches(integration AdverseSourceConfig, source *unstructured.Unstructured) bool {
	if source == nil {
		return false
	}
	if len(integration.Namespaces) > 0 {
		matched := false
		for _, namespace := range integration.Namespaces {
			if strings.TrimSpace(namespace) == source.GetNamespace() {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	if integration.ObjectSelector == nil {
		return true
	}
	selector, err := metav1.LabelSelectorAsSelector(integration.ObjectSelector)
	return err == nil && selector.Matches(labels.Set(source.GetLabels()))
}

func adverseSituationRouteForSource(source *unstructured.Unstructured, integration AdverseSourceConfig) (string, string, string) {
	namespace := strings.TrimSpace(source.GetNamespace())
	name := adverseSituationDefaultName
	groupKey := adverseSituationDefaultGroupKey
	if integration.SituationRef != nil {
		namespace = firstNonEmpty(strings.TrimSpace(integration.SituationRef.Namespace), namespace)
		name = firstNonEmpty(strings.TrimSpace(integration.SituationRef.Name), name)
		groupKey = "situation/" + namespace + "/" + name
	}
	namespace = firstNonEmpty(namespace, "default")
	if strings.TrimSpace(integration.GroupKey) != "" {
		groupKey = strings.TrimSpace(integration.GroupKey)
	}
	return namespace, name, groupKey
}

func parseAdverseSourceGVK(value string) (schema.GroupVersionKind, error) {
	value = strings.Trim(strings.TrimSpace(value), "/")
	separator := strings.LastIndex(value, "/")
	if separator <= 0 || separator == len(value)-1 {
		return schema.GroupVersionKind{}, fmt.Errorf("invalid adverse source %q: expected apiVersion/kind", value)
	}
	apiVersion := value[:separator]
	kind := value[separator+1:]
	gv, err := schema.ParseGroupVersion(apiVersion)
	if err != nil {
		return schema.GroupVersionKind{}, fmt.Errorf("parse adverse source %q: %w", value, err)
	}
	if strings.TrimSpace(kind) == "" {
		return schema.GroupVersionKind{}, fmt.Errorf("invalid adverse source %q: kind is required", value)
	}
	return gv.WithKind(kind), nil
}

func defaultAdverseSituation(namespace, name string) *controlv1alpha1.AdverseSituation {
	return &controlv1alpha1.AdverseSituation{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				adverseSituationGroupLabel: sanitizeLabelValue(adverseSituationDefaultGroupKey),
			},
		},
		Spec: controlv1alpha1.AdverseSituationSpec{
			GroupKey: adverseSituationDefaultGroupKey,
			Buffer: controlv1alpha1.AdverseSituationBufferSpec{
				QuietPeriodSeconds:            adverseSituationDefaultQuietPeriodSeconds,
				DedupeWindowSeconds:           adverseSituationDefaultDedupeWindowSeconds,
				PullRequestQuietPeriodSeconds: adverseSituationDefaultPullRequestHoldSeconds,
				MaxEvents:                     adverseSituationDefaultMaxEvents,
			},
		},
	}
}

func adverseSituationAgentRunFor(situation *controlv1alpha1.AdverseSituation, name string) *controlv1alpha1.AgentRun {
	responder := situation.Spec.Responders.AgentRun
	harness := controlv1alpha1.AgentRunHarnessSpec{
		Intent: controlv1alpha1.AgentRunIntentObserve,
	}
	var notifications *controlv1alpha1.AgentRunNotificationSpec
	var docs *controlv1alpha1.AgentRunDocsSpec
	var issueTracking *controlv1alpha1.AgentRunIssueTrackingSpec
	var profileRef *controlv1alpha1.NamespacedObjectReference
	var harnessProfileRef *controlv1alpha1.NamespacedObjectReference
	var skillSets *controlv1alpha1.AgentSkillCompositionSpec
	var toolSets *controlv1alpha1.AgentToolCompositionSpec
	prompt := ""
	scope := controlv1alpha1.AgentRunScopeSpec{}
	if responder != nil {
		harness = *responder.Harness.DeepCopy()
		if strings.TrimSpace(string(harness.Intent)) == "" {
			harness.Intent = controlv1alpha1.AgentRunIntentObserve
		}
		if responder.Docs != nil {
			docs = responder.Docs.DeepCopy()
		}
		if responder.IssueTracking != nil {
			issueTracking = responder.IssueTracking.DeepCopy()
		}
		if responder.Notifications != nil {
			notifications = responder.Notifications.DeepCopy()
		}
		profileRef = deepCopyNamespacedObjectReference(responder.ProfileRef)
		harnessProfileRef = deepCopyNamespacedObjectReference(responder.HarnessProfileRef)
		if responder.SkillSets != nil {
			skillSets = responder.SkillSets.DeepCopy()
		}
		if responder.ToolSets != nil {
			toolSets = responder.ToolSets.DeepCopy()
		}
		prompt = responder.Prompt
		scope = *responder.Scope.DeepCopy()
	}
	lastEvent := adverseSituationLastEvent(situation.Status.Events)
	sourceRef := controlv1alpha1.AgentRunSourceRef{
		APIVersion: controlv1alpha1.GroupVersion.String(),
		Kind:       "AdverseSituation",
		Namespace:  situation.Namespace,
		Name:       situation.Name,
	}
	return &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: situation.Namespace,
			Labels: map[string]string{
				adverseSituationLabel: sanitizeLabelValue(situation.Name),
			},
		},
		Spec: controlv1alpha1.AgentRunSpec{
			Purpose:          controlv1alpha1.AgentRunPurposeAdverseSituation,
			SourceRef:        sourceRef,
			SourceUID:        string(situation.UID),
			SourceGeneration: situation.Generation,
			Trigger: controlv1alpha1.AgentRunTriggerSnapshot{
				Phase:              string(situation.Status.Phase),
				Reason:             "AdverseSituationOpen",
				Message:            adverseSituationOpenSummary(situation.Status),
				ObservedGeneration: situation.Status.ObservedGeneration,
				ResourceVersion:    situation.ResourceVersion,
				DetectedAt:         lastEvent.LastSeenAt,
			},
			ProfileRef:        profileRef,
			HarnessProfileRef: harnessProfileRef,
			SkillSets:         skillSets,
			ToolSets:          toolSets,
			Prompt:            prompt,
			Scope:             scope,
			Docs:              docs,
			IssueTracking:     issueTracking,
			SituationRef:      &controlv1alpha1.NamespacedObjectReference{Name: situation.Name, Namespace: situation.Namespace},
			Harness:           harness,
			Notifications:     notifications,
		},
	}
}

func adverseSituationRecordEvent(source *unstructured.Unstructured, trigger controlv1alpha1.AgentRunTriggerSnapshot, buffer controlv1alpha1.AdverseSituationBufferSpec, status *controlv1alpha1.AdverseSituationStatus) bool {
	now := metav1.Now()
	if !adverseSituationPrepareSequence(status) {
		return false
	}
	eventID := adverseSituationEventID(source, trigger)
	dedupeWindow := time.Duration(adverseSituationDedupeWindowSeconds(buffer)) * time.Second
	for i := range status.Events {
		event := &status.Events[i]
		if event.ID != eventID {
			continue
		}
		if event.LastSeenAt != nil && dedupeWindow > 0 && now.Sub(event.LastSeenAt.Time) > dedupeWindow {
			continue
		}
		event.Count++
		event.LastSeenAt = &now
		event.Message = adverseSituationLimitString(trigger.Message, 8192)
		event.ResourceVersion = adverseSituationLimitString(trigger.ResourceVersion, 256)
		status.EventCount++
		status.DuplicateCount++
		status.LastEventAt = &now
		status.QuietUntil = adverseSituationQuietUntil(now, buffer)
		status.Phase = controlv1alpha1.AdverseSituationPhaseOpen
		status.ResolvedAt = nil
		return true
	}
	return adverseSituationAppendEvent(adverseSituationNormalizeEvent(controlv1alpha1.AdverseSituationEvent{
		ID: eventID,
		SourceRef: controlv1alpha1.AgentRunSourceRef{
			APIVersion: source.GroupVersionKind().GroupVersion().String(),
			Kind:       source.GetKind(),
			Namespace:  source.GetNamespace(),
			Name:       source.GetName(),
		},
		SourceUID:        string(source.GetUID()),
		SourceGeneration: source.GetGeneration(),
		Phase:            strings.TrimSpace(trigger.Phase),
		ConditionType:    strings.TrimSpace(trigger.ConditionType),
		ConditionStatus:  trigger.ConditionStatus,
		Reason:           strings.TrimSpace(trigger.Reason),
		Message:          strings.TrimSpace(trigger.Message),
		ResourceVersion:  strings.TrimSpace(trigger.ResourceVersion),
	}), now, buffer, status)
}

// adverseSituationRecordSignalEvent reports whether status changed and whether
// the delivery is durably represented. A false delivered result applies
// backpressure; callers must retry instead of accepting or dropping the signal.
func adverseSituationRecordSignalEvent(event controlv1alpha1.AdverseSituationEvent, reportID string, buffer controlv1alpha1.AdverseSituationBufferSpec, status *controlv1alpha1.AdverseSituationStatus) (changed bool, delivered bool) {
	if reportID == "" {
		return false, false
	}
	now := metav1.Now()
	for i := range status.Events {
		for _, recorded := range status.Events[i].ReportIDs {
			if recorded == reportID {
				return false, true
			}
		}
	}
	if !adverseSituationPrepareSequence(status) {
		return false, false
	}
	dedupeWindow := time.Duration(adverseSituationDedupeWindowSeconds(buffer)) * time.Second
	for i := range status.Events {
		recorded := &status.Events[i]
		if recorded.ID != event.ID {
			continue
		}
		if recorded.LastSeenAt != nil && dedupeWindow > 0 && now.Sub(recorded.LastSeenAt.Time) > dedupeWindow {
			continue
		}
		if len(recorded.ReportIDs) >= adverseSituationMaxReportIDsPerEvent {
			return false, false
		}
		recorded.ReportIDs = append(recorded.ReportIDs, reportID)
		recorded.Count++
		recorded.LastSeenAt = &now
		latest := adverseSituationNormalizeEvent(event)
		recorded.SignalRef = latest.SignalRef
		recorded.SourceRef = latest.SourceRef
		recorded.SourceUID = latest.SourceUID
		recorded.SourceURL = latest.SourceURL
		recorded.SourceGeneration = latest.SourceGeneration
		recorded.Phase = latest.Phase
		recorded.ConditionType = latest.ConditionType
		recorded.ConditionStatus = latest.ConditionStatus
		recorded.Reason = latest.Reason
		recorded.Message = latest.Message
		recorded.ResourceVersion = latest.ResourceVersion
		recorded.ObservedAt = latest.ObservedAt
		status.EventCount++
		status.DuplicateCount++
		status.LastEventAt = &now
		status.QuietUntil = adverseSituationQuietUntil(now, buffer)
		status.Phase = controlv1alpha1.AdverseSituationPhaseOpen
		status.ResolvedAt = nil
		return true, true
	}
	event.ReportIDs = []string{reportID}
	if event.ID == "" {
		// A missing grouping key must never collapse unrelated reports.
		event.ID = shortHash(reportID)
	}
	if !adverseSituationAppendEvent(adverseSituationNormalizeEvent(event), now, buffer, status) {
		return false, false
	}
	return true, true
}

func adverseSituationPrepareSequence(status *controlv1alpha1.AdverseSituationStatus) bool {
	if status.Sequence > 0 && status.Phase != controlv1alpha1.AdverseSituationPhaseResolved {
		return true
	}
	for i := range status.Events {
		if len(status.Events[i].ReportIDs) > 0 {
			return false
		}
	}
	status.Sequence++
	if status.Sequence <= 0 {
		status.Sequence = 1
	}
	status.Events = nil
	status.EventCount = 0
	status.DuplicateCount = 0
	status.ActiveResponderRef = nil
	status.ActiveResponderUID = ""
	status.ActiveResponderDigest = ""
	status.PullRequestURL = ""
	status.PullRequestObservedAt = nil
	status.PullRequestQuietUntil = nil
	status.ResolvedAt = nil
	return true
}

func adverseSituationAppendEvent(event controlv1alpha1.AdverseSituationEvent, now metav1.Time, buffer controlv1alpha1.AdverseSituationBufferSpec, status *controlv1alpha1.AdverseSituationStatus) bool {
	maxEvents := adverseSituationMaxEvents(buffer)
	evictCount := len(status.Events) + 1 - maxEvents
	for i := 0; i < evictCount; i++ {
		if len(status.Events[i].ReportIDs) > 0 {
			return false
		}
	}
	event.FirstSeenAt = &now
	event.LastSeenAt = &now
	event.Count = 1
	status.Events = append(status.Events, event)
	if len(status.Events) > maxEvents {
		status.Events = append([]controlv1alpha1.AdverseSituationEvent(nil), status.Events[len(status.Events)-maxEvents:]...)
	}
	status.EventCount++
	status.LastEventAt = &now
	status.QuietUntil = adverseSituationQuietUntil(now, buffer)
	status.Phase = controlv1alpha1.AdverseSituationPhaseOpen
	status.ResolvedAt = nil
	return true
}

func adverseSituationNormalizeEvent(event controlv1alpha1.AdverseSituationEvent) controlv1alpha1.AdverseSituationEvent {
	event.ID = adverseSituationLimitString(event.ID, 64)
	event.SourceRef.APIVersion = adverseSituationLimitString(event.SourceRef.APIVersion, 128)
	event.SourceRef.Kind = adverseSituationLimitString(event.SourceRef.Kind, 256)
	event.SourceRef.Namespace = adverseSituationLimitString(event.SourceRef.Namespace, 253)
	event.SourceRef.Name = adverseSituationLimitString(event.SourceRef.Name, 253)
	event.SourceUID = adverseSituationLimitString(event.SourceUID, 256)
	event.SourceURL = adverseSituationLimitString(event.SourceURL, 2048)
	event.Phase = adverseSituationLimitString(event.Phase, 128)
	event.ConditionType = adverseSituationLimitString(event.ConditionType, 256)
	event.Reason = adverseSituationLimitString(event.Reason, 256)
	event.Message = adverseSituationLimitString(event.Message, 8192)
	event.ResourceVersion = adverseSituationLimitString(event.ResourceVersion, 256)
	return event
}

func adverseSituationLimitString(value string, maxBytes int) string {
	value = strings.TrimSpace(value)
	if maxBytes <= 0 || len(value) <= maxBytes {
		return value
	}
	end := maxBytes
	for end > 0 && !utf8.RuneStart(value[end]) {
		end--
	}
	return strings.TrimSpace(value[:end])
}

func adverseSituationCanResolve(status controlv1alpha1.AdverseSituationStatus, run *controlv1alpha1.AgentRun, now time.Time) bool {
	if status.QuietUntil == nil || now.Before(status.QuietUntil.Time) {
		return false
	}
	if status.PullRequestQuietUntil != nil && now.Before(status.PullRequestQuietUntil.Time) {
		return false
	}
	if run != nil && !agentRunPhaseTerminal(run.Status.Phase) {
		return false
	}
	return true
}

func adverseSituationNextRequeue(status controlv1alpha1.AdverseSituationStatus, now time.Time) time.Duration {
	next := adverseSituationPollInterval
	for _, value := range []*metav1.Time{status.QuietUntil, status.PullRequestQuietUntil} {
		if value == nil || !value.After(now) {
			continue
		}
		duration := value.Sub(now)
		if duration > 0 && duration < next {
			next = duration
		}
	}
	return next
}

func adverseSituationOpenSummary(status controlv1alpha1.AdverseSituationStatus) string {
	if len(status.Events) == 0 {
		return "No adverse events have been recorded."
	}
	last := adverseSituationLastEvent(status.Events)
	return fmt.Sprintf("Adverse stream has %d reports across %d buffered events; latest %s/%s reason=%s.", status.EventCount, len(status.Events), last.SourceRef.Kind, last.SourceRef.Name, firstNonEmpty(last.Reason, last.Phase, "unknown"))
}

func adverseSituationNameForSource(_ *unstructured.Unstructured) string {
	return adverseSituationDefaultName
}

func adverseSituationEventID(source *unstructured.Unstructured, trigger controlv1alpha1.AgentRunTriggerSnapshot) string {
	return shortHash(fmt.Sprintf("%s/%s/%s/%s/%d/%s/%s/%s", source.GroupVersionKind().String(), source.GetNamespace(), source.GetName(), source.GetUID(), source.GetGeneration(), trigger.Phase, trigger.ConditionType, trigger.Reason))
}

func adverseSituationLastEvent(events []controlv1alpha1.AdverseSituationEvent) controlv1alpha1.AdverseSituationEvent {
	if len(events) == 0 {
		return controlv1alpha1.AdverseSituationEvent{}
	}
	return events[len(events)-1]
}

func adverseSituationQuietUntil(now metav1.Time, buffer controlv1alpha1.AdverseSituationBufferSpec) *metav1.Time {
	quietUntil := metav1.NewTime(now.Add(time.Duration(adverseSituationQuietPeriodSeconds(buffer)) * time.Second))
	return &quietUntil
}

func adverseSituationAgentRunEnabled(situation *controlv1alpha1.AdverseSituation) bool {
	responder := situation.Spec.Responders.AgentRun
	if responder == nil || responder.Enabled == nil {
		return false
	}
	return *responder.Enabled
}

func adverseSituationBuffer(situation *controlv1alpha1.AdverseSituation) controlv1alpha1.AdverseSituationBufferSpec {
	if situation == nil {
		return controlv1alpha1.AdverseSituationBufferSpec{}
	}
	return situation.Spec.Buffer
}

func adverseSituationQuietPeriodSeconds(buffer controlv1alpha1.AdverseSituationBufferSpec) int {
	if buffer.QuietPeriodSeconds > 0 {
		return buffer.QuietPeriodSeconds
	}
	return adverseSituationDefaultQuietPeriodSeconds
}

func adverseSituationDedupeWindowSeconds(buffer controlv1alpha1.AdverseSituationBufferSpec) int {
	if buffer.DedupeWindowSeconds > 0 {
		return buffer.DedupeWindowSeconds
	}
	return adverseSituationDefaultDedupeWindowSeconds
}

func adverseSituationPullRequestHoldSeconds(situation *controlv1alpha1.AdverseSituation) int {
	if situation != nil && situation.Spec.Buffer.PullRequestQuietPeriodSeconds > 0 {
		return situation.Spec.Buffer.PullRequestQuietPeriodSeconds
	}
	return adverseSituationDefaultPullRequestHoldSeconds
}

func adverseSituationMaxEvents(buffer controlv1alpha1.AdverseSituationBufferSpec) int {
	if buffer.MaxEvents > 0 {
		return min(buffer.MaxEvents, adverseSituationHardMaxEvents)
	}
	return adverseSituationDefaultMaxEvents
}
