package controller

import (
	"context"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	controlv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

func TestCreateScheduledAgentRunDoesNotRequireImmediateCacheRead(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := controlv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add control scheme: %v", err)
	}

	baseClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	cacheLagClient := &agentScheduleCreateCacheLagClient{Client: baseClient}
	reconciler := &AgentScheduleReconciler{Client: cacheLagClient, Scheme: scheme}
	schedule := &controlv1alpha1.AgentSchedule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "status-tool-smoke",
			Namespace: "anvil",
			UID:       "status-tool-smoke-uid",
		},
		Spec: controlv1alpha1.AgentScheduleSpec{
			IntervalSeconds: 3600,
			RunTemplate: controlv1alpha1.AgentRunSpec{
				Harness: controlv1alpha1.AgentRunHarnessSpec{
					Intent: controlv1alpha1.AgentRunIntentObserve,
				},
			},
		},
	}
	now := metav1.NewTime(time.Date(2026, 7, 7, 22, 45, 0, 0, time.UTC))

	run, _, err := reconciler.createScheduledAgentRun(ctx, schedule, now, agentScheduleManualRunRequest(schedule, "manual-token", "", now))
	if err != nil {
		t.Fatalf("create scheduled agent run: %v", err)
	}
	if cacheLagClient.agentRunGetAttempts != 0 {
		t.Fatalf("agent run get attempts = %d, want 0", cacheLagClient.agentRunGetAttempts)
	}
	if run.Name == "" {
		t.Fatalf("run name is empty")
	}
	if got, want := run.Namespace, "anvil"; got != want {
		t.Fatalf("run namespace = %q, want %q", got, want)
	}
	if run.Spec.ScheduleRef == nil || run.Spec.ScheduleRef.Name != schedule.Name || run.Spec.ScheduleRef.Namespace != schedule.Namespace {
		t.Fatalf("schedule ref = %#v, want %s/%s", run.Spec.ScheduleRef, schedule.Namespace, schedule.Name)
	}
	if got, want := run.Spec.Trigger.Reason, "ManualAgentScheduleNudge"; got != want {
		t.Fatalf("trigger reason = %q, want %q", got, want)
	}

	stored := &controlv1alpha1.AgentRun{}
	if err := baseClient.Get(ctx, client.ObjectKey{Namespace: run.Namespace, Name: run.Name}, stored); err != nil {
		t.Fatalf("created agent run was not stored: %v", err)
	}
}

func TestCreateScheduledAgentRunPreservesProfileResolutionInputs(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := controlv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add control scheme: %v", err)
	}

	reconciler := &AgentScheduleReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).Build(),
		Scheme: scheme,
	}
	schedule := &controlv1alpha1.AgentSchedule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "anvil-openclaw-xai-review-1h",
			Namespace: "anvilhub",
			UID:       "anvil-openclaw-xai-review-1h-uid",
		},
		Spec: controlv1alpha1.AgentScheduleSpec{
			IntervalSeconds: 3600,
			RunTemplate: controlv1alpha1.AgentRunSpec{
				ProfileRef: &controlv1alpha1.NamespacedObjectReference{
					Name: "anvil-openclaw-xai-review",
				},
			},
		},
	}
	now := metav1.NewTime(time.Date(2026, 7, 10, 4, 30, 0, 0, time.UTC))

	run, _, err := reconciler.createScheduledAgentRun(ctx, schedule, now, agentScheduleIntervalRunRequest(schedule, now, ""))
	if err != nil {
		t.Fatalf("create scheduled agent run: %v", err)
	}
	if run.Spec.ProfileRef == nil || run.Spec.ProfileRef.Name != "anvil-openclaw-xai-review" {
		t.Fatalf("profile ref = %#v, want anvil-openclaw-xai-review", run.Spec.ProfileRef)
	}
	if got := run.Spec.Harness.Intent; got != "" {
		t.Fatalf("harness intent = %q, want empty so AgentRunProfile can supply it", got)
	}
	profile := &controlv1alpha1.AgentRunProfile{
		Spec: controlv1alpha1.AgentRunProfileSpec{
			Harness: controlv1alpha1.AgentRunHarnessSpec{
				Intent: controlv1alpha1.AgentRunIntentProposeChange,
			},
		},
	}
	if got := agentRunApplyProfile(run, profile).Spec.Harness.Intent; got != controlv1alpha1.AgentRunIntentProposeChange {
		t.Fatalf("effective harness intent = %q, want profile intent %q", got, controlv1alpha1.AgentRunIntentProposeChange)
	}
}

func TestIntervalScheduledAgentRunNameIsDeterministicForDueTick(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := controlv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add control scheme: %v", err)
	}

	reconciler := &AgentScheduleReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).Build(),
		Scheme: scheme,
	}
	schedule := &controlv1alpha1.AgentSchedule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "platform-health-30m",
			Namespace: "anvilhub",
			UID:       "platform-health-30m-uid",
		},
		Spec: controlv1alpha1.AgentScheduleSpec{
			IntervalSeconds: 1800,
			RunTemplate: controlv1alpha1.AgentRunSpec{
				Harness: controlv1alpha1.AgentRunHarnessSpec{
					Intent: controlv1alpha1.AgentRunIntentProposeChange,
				},
			},
		},
	}
	dueAt := metav1.NewTime(time.Date(2026, 7, 8, 15, 30, 0, 0, time.UTC))
	firstNow := metav1.NewTime(time.Date(2026, 7, 8, 15, 35, 0, 123, time.UTC))
	secondNow := metav1.NewTime(time.Date(2026, 7, 8, 15, 35, 2, 456, time.UTC))
	request := agentScheduleIntervalRunRequest(schedule, dueAt, "")

	first, _, err := reconciler.createScheduledAgentRun(ctx, schedule, firstNow, request)
	if err != nil {
		t.Fatalf("create first scheduled agent run: %v", err)
	}
	second, _, err := reconciler.createScheduledAgentRun(ctx, schedule, secondNow, request)
	if err != nil {
		t.Fatalf("create duplicate scheduled agent run: %v", err)
	}
	if first.Name != second.Name {
		t.Fatalf("duplicate run name = %q, want %q", second.Name, first.Name)
	}

	list := &controlv1alpha1.AgentRunList{}
	if err := reconciler.List(ctx, list, client.InNamespace(schedule.Namespace)); err != nil {
		t.Fatalf("list scheduled agent runs: %v", err)
	}
	if got, want := len(list.Items), 1; got != want {
		t.Fatalf("agent run count = %d, want %d", got, want)
	}
}

func TestManualScheduledAgentRunNameIsDeterministicForToken(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := controlv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add control scheme: %v", err)
	}

	reconciler := &AgentScheduleReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).Build(),
		Scheme: scheme,
	}
	schedule := &controlv1alpha1.AgentSchedule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "platform-health-30m",
			Namespace: "anvilhub",
			UID:       "platform-health-30m-uid",
		},
		Spec: controlv1alpha1.AgentScheduleSpec{
			IntervalSeconds: 1800,
			RunTemplate: controlv1alpha1.AgentRunSpec{
				Harness: controlv1alpha1.AgentRunHarnessSpec{
					Intent: controlv1alpha1.AgentRunIntentProposeChange,
				},
			},
		},
	}
	firstNow := metav1.NewTime(time.Date(2026, 7, 8, 15, 19, 33, 100, time.UTC))
	secondNow := metav1.NewTime(time.Date(2026, 7, 8, 15, 19, 34, 200, time.UTC))
	token := "20260708T151924Z"

	first, _, err := reconciler.createScheduledAgentRun(ctx, schedule, firstNow, agentScheduleManualRunRequest(schedule, token, "", firstNow))
	if err != nil {
		t.Fatalf("create first manual agent run: %v", err)
	}
	second, _, err := reconciler.createScheduledAgentRun(ctx, schedule, secondNow, agentScheduleManualRunRequest(schedule, token, "", secondNow))
	if err != nil {
		t.Fatalf("create duplicate manual agent run: %v", err)
	}
	if first.Name != second.Name {
		t.Fatalf("duplicate manual run name = %q, want %q", second.Name, first.Name)
	}
	if !strings.Contains(first.Name, "20260708-15192") {
		t.Fatalf("manual run name = %q, want token timestamp", first.Name)
	}

	list := &controlv1alpha1.AgentRunList{}
	if err := reconciler.List(ctx, list, client.InNamespace(schedule.Namespace)); err != nil {
		t.Fatalf("list manual agent runs: %v", err)
	}
	if got, want := len(list.Items), 1; got != want {
		t.Fatalf("agent run count = %d, want %d", got, want)
	}
}

func TestAgentRunQueuedBehindScheduleBlocksNewerRun(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := controlv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add control scheme: %v", err)
	}

	schedule := testAgentScheduleWithPolicy(controlv1alpha1.AgentScheduleConcurrencyQueue)
	older := testScheduledAgentRun("platform-health-20260708-120000", time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC))
	newer := testScheduledAgentRun("platform-health-20260708-123000", time.Date(2026, 7, 8, 12, 30, 0, 0, time.UTC))
	reconciler := &AgentRunReconciler{
		Client: fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(schedule, older, newer).
			Build(),
		Scheme: scheme,
	}

	blockedBy, err := reconciler.agentRunQueuedBehindSchedule(ctx, newer)
	if err != nil {
		t.Fatalf("check queue block: %v", err)
	}
	if blockedBy == nil {
		t.Fatal("newer run was not blocked")
	}
	if got, want := blockedBy.Name, older.Name; got != want {
		t.Fatalf("blocked by %q, want %q", got, want)
	}
}

func TestAgentRunQueueGateUsesSanitizedLabelAndExactScheduleIdentity(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := controlv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add control scheme: %v", err)
	}
	schedule := testAgentScheduleWithPolicy(controlv1alpha1.AgentScheduleConcurrencyQueue)
	schedule.Name = "platform.health"
	schedule.UID = "platform-health-uid"
	older := testScheduledAgentRun("platform-health-older", time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC))
	newer := testScheduledAgentRun("platform-health-newer", time.Date(2026, 7, 8, 12, 30, 0, 0, time.UTC))
	for _, run := range []*controlv1alpha1.AgentRun{older, newer} {
		run.Labels[agentRunScheduleLabel] = "platform-health"
		run.Spec.ScheduleRef.Name = "platform.health"
		run.Spec.SourceUID = "platform-health-uid"
	}
	collision := testScheduledAgentRun("collision", time.Date(2026, 7, 8, 11, 0, 0, 0, time.UTC))
	collision.Labels[agentRunScheduleLabel] = "platform-health"
	collision.Spec.ScheduleRef.Name = "platform-health"
	reconciler := &AgentRunReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(schedule, older, newer, collision).Build(),
		Scheme: scheme,
	}
	blockedBy, err := reconciler.agentRunQueuedBehindSchedule(ctx, newer)
	if err != nil {
		t.Fatalf("check queue block: %v", err)
	}
	if blockedBy == nil || blockedBy.Name != older.Name {
		t.Fatalf("blocked by %#v, want exact schedule run %q", blockedBy, older.Name)
	}
}

func TestAgentRunQueuePolicyAllowsOldestRun(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := controlv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add control scheme: %v", err)
	}

	schedule := testAgentScheduleWithPolicy(controlv1alpha1.AgentScheduleConcurrencyQueue)
	older := testScheduledAgentRun("platform-health-20260708-120000", time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC))
	newer := testScheduledAgentRun("platform-health-20260708-123000", time.Date(2026, 7, 8, 12, 30, 0, 0, time.UTC))
	reconciler := &AgentRunReconciler{
		Client: fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(schedule, older, newer).
			Build(),
		Scheme: scheme,
	}

	blockedBy, err := reconciler.agentRunQueuedBehindSchedule(ctx, older)
	if err != nil {
		t.Fatalf("check queue block: %v", err)
	}
	if blockedBy != nil {
		t.Fatalf("oldest run was blocked by %s/%s", blockedBy.Namespace, blockedBy.Name)
	}
}

func TestAgentRunQueuePolicyHonorsMaxConcurrentRuns(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := controlv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add control scheme: %v", err)
	}

	schedule := testAgentScheduleWithPolicy(controlv1alpha1.AgentScheduleConcurrencyQueue)
	schedule.Spec.MaxConcurrentRuns = 2
	first := testScheduledAgentRun("platform-health-20260708-120000", time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC))
	second := testScheduledAgentRun("platform-health-20260708-123000", time.Date(2026, 7, 8, 12, 30, 0, 0, time.UTC))
	third := testScheduledAgentRun("platform-health-20260708-130000", time.Date(2026, 7, 8, 13, 0, 0, 0, time.UTC))
	reconciler := &AgentRunReconciler{
		Client: fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(schedule, first, second, third).
			Build(),
		Scheme: scheme,
	}

	blockedBy, err := reconciler.agentRunQueuedBehindSchedule(ctx, second)
	if err != nil {
		t.Fatalf("check second queue block: %v", err)
	}
	if blockedBy != nil {
		t.Fatalf("second run was blocked by %s/%s, want two allowed runs", blockedBy.Namespace, blockedBy.Name)
	}

	blockedBy, err = reconciler.agentRunQueuedBehindSchedule(ctx, third)
	if err != nil {
		t.Fatalf("check third queue block: %v", err)
	}
	if blockedBy == nil {
		t.Fatal("third run was not blocked")
	}
	if got, want := blockedBy.Name, second.Name; got != want {
		t.Fatalf("third blocked by %q, want %q", got, want)
	}
}

func TestAgentRunForbidPolicyBlocksNewerExistingRun(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := controlv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add control scheme: %v", err)
	}

	schedule := testAgentScheduleWithPolicy(controlv1alpha1.AgentScheduleConcurrencyForbid)
	older := testScheduledAgentRun("platform-health-20260708-120000", time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC))
	newer := testScheduledAgentRun("platform-health-20260708-123000", time.Date(2026, 7, 8, 12, 30, 0, 0, time.UTC))
	reconciler := &AgentRunReconciler{
		Client: fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(schedule, older, newer).
			Build(),
		Scheme: scheme,
	}

	blockedBy, err := reconciler.agentRunQueuedBehindSchedule(ctx, newer)
	if err != nil {
		t.Fatalf("check forbid block: %v", err)
	}
	if blockedBy == nil {
		t.Fatal("newer run was not blocked")
	}
	if got, want := blockedBy.Name, older.Name; got != want {
		t.Fatalf("blocked by %q, want %q", got, want)
	}
}

func TestScheduledAgentRunNameIsIndependentOfTemplate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := controlv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add control scheme: %v", err)
	}

	dueAt := metav1.NewTime(time.Date(2026, 7, 10, 9, 0, 2, 0, time.UTC))
	schedule := &controlv1alpha1.AgentSchedule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "anvil-openclaw-xai-review-1h",
			Namespace: "anvilhub",
			UID:       "anvil-openclaw-xai-review-1h-uid",
		},
		Spec: controlv1alpha1.AgentScheduleSpec{
			IntervalSeconds:         3600,
			ConcurrencyPolicy:       controlv1alpha1.AgentScheduleConcurrencyQueue,
			MaxConcurrentRuns:       1,
			TemplateSelectionPolicy: controlv1alpha1.AgentScheduleTemplateSelectionSequential,
			RunTemplates: []controlv1alpha1.AgentScheduleRunTemplateSpec{
				{Name: "grok-4-5", Template: controlv1alpha1.AgentRunSpec{Prompt: "grok"}},
				{Name: "composer-2-5", Template: controlv1alpha1.AgentRunSpec{Prompt: "composer"}},
			},
		},
	}
	reconciler := &AgentScheduleReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(schedule).Build(),
		Scheme: scheme,
	}

	grokReq := agentScheduleIntervalRunRequest(schedule, dueAt, "")
	composerReq := agentScheduleIntervalRunRequest(schedule, dueAt, "grok-4-5")
	if grokReq.TemplateName == composerReq.TemplateName {
		t.Fatalf("templates unexpectedly equal: %q", grokReq.TemplateName)
	}

	grokRun, grokTemplate, err := reconciler.createScheduledAgentRun(ctx, schedule, dueAt, grokReq)
	if err != nil {
		t.Fatalf("create grok run: %v", err)
	}
	if grokTemplate != "grok-4-5" {
		t.Fatalf("grok template = %q, want grok-4-5", grokTemplate)
	}

	// Second create for the same dueAt with a different Sequential template must
	// collide on name and return the existing object via AlreadyExists.
	second, secondTemplate, err := reconciler.createScheduledAgentRun(ctx, schedule, dueAt, composerReq)
	if err != nil {
		t.Fatalf("create composer run (expect AlreadyExists reuse): %v", err)
	}
	if second.Name != grokRun.Name {
		t.Fatalf("second name = %q, want same as first %q", second.Name, grokRun.Name)
	}
	if secondTemplate != "composer-2-5" {
		t.Fatalf("selected template after collide = %q, want composer-2-5 (selection still rotates)", secondTemplate)
	}

	list := &controlv1alpha1.AgentRunList{}
	if err := reconciler.List(ctx, list, client.InNamespace(schedule.Namespace)); err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if got, want := len(list.Items), 1; got != want {
		t.Fatalf("agent runs = %d, want %d", got, want)
	}
	if got := list.Items[0].Labels[agentRunTemplateLabel]; got != "grok-4-5" {
		t.Fatalf("stored template label = %q, want grok-4-5 from first create", got)
	}
}

func TestAgentScheduleQueueDoesNotCreateSecondTemplateForSameDueAt(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := controlv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add control scheme: %v", err)
	}

	dueAt := time.Date(2026, 7, 10, 7, 0, 2, 0, time.UTC)
	now := dueAt.Add(5 * time.Second)
	schedule := &controlv1alpha1.AgentSchedule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "anvil-openclaw-xai-review-1h",
			Namespace: "anvilhub",
			UID:       "anvil-openclaw-xai-review-1h-uid",
		},
		Spec: controlv1alpha1.AgentScheduleSpec{
			IntervalSeconds:         3600,
			ConcurrencyPolicy:       controlv1alpha1.AgentScheduleConcurrencyQueue,
			MaxConcurrentRuns:       1,
			TemplateSelectionPolicy: controlv1alpha1.AgentScheduleTemplateSelectionSequential,
			RunTemplates: []controlv1alpha1.AgentScheduleRunTemplateSpec{
				{Name: "grok-4-5", Template: controlv1alpha1.AgentRunSpec{Prompt: "grok"}},
				{Name: "composer-2-5", Template: controlv1alpha1.AgentRunSpec{Prompt: "composer"}},
			},
		},
		Status: controlv1alpha1.AgentScheduleStatus{
			ObservedGeneration: 1,
			NextRunAt:          &metav1.Time{Time: dueAt},
		},
	}
	schedule.Generation = 1
	existing := &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "agentrun-anvil-openclaw-xai-review-1h-grok-4-5-202-f06a604ec4f4",
			Namespace:         "anvilhub",
			CreationTimestamp: metav1.NewTime(dueAt),
			Labels: map[string]string{
				agentRunScheduleLabel: "anvil-openclaw-xai-review-1h",
				agentRunTemplateLabel: "grok-4-5",
			},
		},
		Spec: controlv1alpha1.AgentRunSpec{
			Trigger: controlv1alpha1.AgentRunTriggerSnapshot{
				Reason:  "ScheduledAgentRun",
				Message: agentScheduleIntervalTriggerMessage(schedule, metav1.NewTime(dueAt)),
			},
			ScheduleRef: &controlv1alpha1.NamespacedObjectReference{
				Name:      schedule.Name,
				Namespace: schedule.Namespace,
			},
		},
		Status: controlv1alpha1.AgentRunStatus{Phase: controlv1alpha1.AgentRunPhaseRunning},
	}
	reconciler := &AgentScheduleReconciler{
		Client: fake.NewClientBuilder().
			WithScheme(scheme).
			WithStatusSubresource(&controlv1alpha1.AgentSchedule{}).
			WithObjects(schedule, existing).
			Build(),
		Scheme: scheme,
	}

	// Freeze "now" by patching is not available; Reconcile uses metav1.Now().
	// The dueAt is in the past relative to wall clock in CI after 2026-07-10, so
	// the schedule remains due and the idempotency path is exercised.
	_ = now
	_, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(schedule)})
	if err != nil {
		t.Fatalf("reconcile schedule: %v", err)
	}

	list := &controlv1alpha1.AgentRunList{}
	if err := reconciler.List(ctx, list, client.InNamespace(schedule.Namespace), client.MatchingLabels{agentRunScheduleLabel: schedule.Name}); err != nil {
		t.Fatalf("list agent runs: %v", err)
	}
	if got, want := len(list.Items), 1; got != want {
		names := make([]string, 0, len(list.Items))
		for _, item := range list.Items {
			names = append(names, item.Name)
		}
		t.Fatalf("agent runs = %d (%v), want %d (no second Sequential template for same dueAt)", got, names, want)
	}

	stored := &controlv1alpha1.AgentSchedule{}
	if err := reconciler.Get(ctx, client.ObjectKeyFromObject(schedule), stored); err != nil {
		t.Fatalf("get schedule: %v", err)
	}
	if stored.Status.NextRunAt == nil || !stored.Status.NextRunAt.After(dueAt) {
		t.Fatalf("nextRunAt = %#v, want after dueAt %s", stored.Status.NextRunAt, dueAt.Format(time.RFC3339))
	}
	var ready *metav1.Condition
	for i := range stored.Status.Conditions {
		if stored.Status.Conditions[i].Type == agentScheduleReady {
			ready = &stored.Status.Conditions[i]
			break
		}
	}
	if ready == nil {
		t.Fatal("Ready condition was not set")
	}
	if got, want := ready.Reason, "IntervalAlreadyCreated"; got != want {
		t.Fatalf("Ready reason = %q, want %q (message=%q)", got, want, ready.Message)
	}
}

func TestAgentScheduleQueueStatusReportsQueueHeadWhileDraining(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := controlv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add control scheme: %v", err)
	}

	now := time.Now().UTC()
	lastRunAt := metav1.NewTime(now.Add(-10 * time.Minute))
	nextRunAt := metav1.NewTime(now.Add(20 * time.Minute))
	schedule := testAgentScheduleWithPolicy(controlv1alpha1.AgentScheduleConcurrencyQueue)
	schedule.Generation = 1
	schedule.Status = controlv1alpha1.AgentScheduleStatus{
		ObservedGeneration: 1,
		LastRunAt:          &lastRunAt,
		NextRunAt:          &nextRunAt,
	}
	older := testScheduledAgentRun("platform-health-20260708-120000", now.Add(-1*time.Hour))
	older.Status.Phase = controlv1alpha1.AgentRunPhaseRunning
	newer := testScheduledAgentRun("platform-health-20260708-123000", now.Add(-30*time.Minute))
	newer.Status.Phase = controlv1alpha1.AgentRunPhasePending
	reconciler := &AgentScheduleReconciler{
		Client: fake.NewClientBuilder().
			WithScheme(scheme).
			WithStatusSubresource(&controlv1alpha1.AgentSchedule{}).
			WithObjects(schedule, older, newer).
			Build(),
		Scheme: scheme,
	}

	result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(schedule)})
	if err != nil {
		t.Fatalf("reconcile schedule: %v", err)
	}
	if result.RequeueAfter != agentSchedulePollInterval {
		t.Fatalf("requeueAfter = %s, want %s", result.RequeueAfter, agentSchedulePollInterval)
	}

	stored := &controlv1alpha1.AgentSchedule{}
	if err := reconciler.Get(ctx, client.ObjectKeyFromObject(schedule), stored); err != nil {
		t.Fatalf("get reconciled schedule: %v", err)
	}
	if got, want := stored.Status.Phase, controlv1alpha1.AgentSchedulePhaseRunning; got != want {
		t.Fatalf("phase = %q, want %q", got, want)
	}
	if stored.Status.ActiveRunRef == nil || stored.Status.ActiveRunRef.Name != older.Name {
		t.Fatalf("activeRunRef = %#v, want queue head %s", stored.Status.ActiveRunRef, older.Name)
	}
	if got, want := stored.Status.ActiveRunCount, 2; got != want {
		t.Fatalf("activeRunCount = %d, want %d", got, want)
	}
	var ready *metav1.Condition
	for i := range stored.Status.Conditions {
		if stored.Status.Conditions[i].Type == agentScheduleReady {
			ready = &stored.Status.Conditions[i]
			break
		}
	}
	if ready == nil {
		t.Fatal("Ready condition was not set")
	}
	if got, want := ready.Status, metav1.ConditionFalse; got != want {
		t.Fatalf("Ready status = %q, want %q", got, want)
	}
	if got, want := ready.Reason, "QueueActive"; got != want {
		t.Fatalf("Ready reason = %q, want %q", got, want)
	}
}

func TestAgentScheduleDailyRunBudgetIncludesTerminalRunsAndManualNudges(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := controlv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add control scheme: %v", err)
	}

	now := time.Now().UTC()
	due := metav1.NewTime(now.Add(-time.Minute))
	schedule := testAgentScheduleWithPolicy(controlv1alpha1.AgentScheduleConcurrencyAllow)
	schedule.Generation = 1
	schedule.Spec.MaxRunsPerDay = 1
	schedule.Annotations = map[string]string{controlv1alpha1.AgentScheduleRunNowAnnotation: "manual-over-budget"}
	schedule.Status = controlv1alpha1.AgentScheduleStatus{ObservedGeneration: 1, NextRunAt: &due}
	existing := testScheduledAgentRun("platform-health-today", now.Add(-time.Hour))
	existing.Status.Phase = controlv1alpha1.AgentRunPhaseSucceeded

	reconciler := &AgentScheduleReconciler{
		Client: fake.NewClientBuilder().
			WithScheme(scheme).
			WithStatusSubresource(&controlv1alpha1.AgentSchedule{}).
			WithObjects(schedule, existing).
			Build(),
		Scheme: scheme,
	}

	result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(schedule)})
	if err != nil {
		t.Fatalf("reconcile schedule: %v", err)
	}
	if result.RequeueAfter <= 0 || result.RequeueAfter > 24*time.Hour {
		t.Fatalf("requeueAfter = %s, want next UTC budget reset", result.RequeueAfter)
	}

	list := &controlv1alpha1.AgentRunList{}
	if err := reconciler.List(ctx, list, client.InNamespace(schedule.Namespace)); err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if got, want := len(list.Items), 1; got != want {
		t.Fatalf("runs = %d, want %d while daily budget is exhausted", got, want)
	}

	stored := &controlv1alpha1.AgentSchedule{}
	if err := reconciler.Get(ctx, client.ObjectKeyFromObject(schedule), stored); err != nil {
		t.Fatalf("get schedule: %v", err)
	}
	ready := apimeta.FindStatusCondition(stored.Status.Conditions, agentScheduleReady)
	if ready == nil || ready.Reason != "DailyRunBudgetReached" || !strings.Contains(ready.Message, "manual-over-budget") {
		t.Fatalf("Ready condition = %#v, want deferred manual daily-budget condition", ready)
	}
	if stored.Status.LastManualRunToken != "" {
		t.Fatalf("lastManualRunToken = %q, want pending token to remain unconsumed", stored.Status.LastManualRunToken)
	}
}

func TestAgentScheduleForbidAdvancesMissedIntervalWhileActive(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := controlv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add control scheme: %v", err)
	}

	now := time.Now().UTC()
	lastRunAt := metav1.NewTime(now.Add(-2 * time.Hour))
	staleNextRunAt := metav1.NewTime(now.Add(-1 * time.Hour))
	schedule := testAgentScheduleWithPolicy(controlv1alpha1.AgentScheduleConcurrencyForbid)
	schedule.Generation = 1
	schedule.Status = controlv1alpha1.AgentScheduleStatus{
		ObservedGeneration: 1,
		LastRunAt:          &lastRunAt,
		NextRunAt:          &staleNextRunAt,
	}
	active := testScheduledAgentRun("platform-health-20260708-120000", now.Add(-2*time.Hour))
	active.Status.Phase = controlv1alpha1.AgentRunPhaseRunning
	reconciler := &AgentScheduleReconciler{
		Client: fake.NewClientBuilder().
			WithScheme(scheme).
			WithStatusSubresource(&controlv1alpha1.AgentSchedule{}).
			WithObjects(schedule, active).
			Build(),
		Scheme: scheme,
	}

	result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(schedule)})
	if err != nil {
		t.Fatalf("reconcile schedule: %v", err)
	}
	if result.RequeueAfter != agentSchedulePollInterval {
		t.Fatalf("requeueAfter = %s, want %s", result.RequeueAfter, agentSchedulePollInterval)
	}

	stored := &controlv1alpha1.AgentSchedule{}
	if err := reconciler.Get(ctx, client.ObjectKeyFromObject(schedule), stored); err != nil {
		t.Fatalf("get reconciled schedule: %v", err)
	}
	if stored.Status.NextRunAt == nil {
		t.Fatal("nextRunAt was nil")
	}
	if !stored.Status.NextRunAt.Time.After(now) {
		t.Fatalf("nextRunAt = %s, want after %s", stored.Status.NextRunAt.Time.Format(time.RFC3339), now.Format(time.RFC3339))
	}
	if stored.Status.ActiveRunRef == nil || stored.Status.ActiveRunRef.Name != active.Name {
		t.Fatalf("activeRunRef = %#v, want active run %s", stored.Status.ActiveRunRef, active.Name)
	}
	var ready *metav1.Condition
	for i := range stored.Status.Conditions {
		if stored.Status.Conditions[i].Type == agentScheduleReady {
			ready = &stored.Status.Conditions[i]
			break
		}
	}
	if ready == nil {
		t.Fatal("Ready condition was not set")
	}
	if got, want := ready.Reason, "RunActive"; got != want {
		t.Fatalf("Ready reason = %q, want %q", got, want)
	}

	list := &controlv1alpha1.AgentRunList{}
	if err := reconciler.List(ctx, list, client.InNamespace(schedule.Namespace), client.MatchingLabels{agentRunScheduleLabel: schedule.Name}); err != nil {
		t.Fatalf("list agent runs: %v", err)
	}
	if got, want := len(list.Items), 1; got != want {
		t.Fatalf("agent runs = %d, want %d", got, want)
	}
}

func TestAgentScheduleTerminalBackoffDelaysAutomaticIntervalRun(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := controlv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add control scheme: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	completedAt := metav1.NewTime(now.Add(-5 * time.Minute))
	existingNextRunAt := metav1.NewTime(now.Add(-time.Minute))
	wantNextRunAt := metav1.NewTime(completedAt.Add(time.Hour))
	schedule := testAgentScheduleWithPolicy(controlv1alpha1.AgentScheduleConcurrencyQueue)
	schedule.Generation = 1
	schedule.Spec.Backoff = &controlv1alpha1.AgentScheduleBackoffSpec{FailedSeconds: 3600}
	schedule.Status = controlv1alpha1.AgentScheduleStatus{
		ObservedGeneration: 1,
		NextRunAt:          &existingNextRunAt,
	}
	failed := testScheduledAgentRun("platform-health-failed", now.Add(-10*time.Minute))
	failed.Status = controlv1alpha1.AgentRunStatus{
		Phase:       controlv1alpha1.AgentRunPhaseFailed,
		CompletedAt: &completedAt,
	}
	reconciler := &AgentScheduleReconciler{
		Client: fake.NewClientBuilder().
			WithScheme(scheme).
			WithStatusSubresource(&controlv1alpha1.AgentSchedule{}).
			WithObjects(schedule, failed).
			Build(),
		Scheme: scheme,
	}

	result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(schedule)})
	if err != nil {
		t.Fatalf("reconcile schedule: %v", err)
	}
	if result.RequeueAfter < 50*time.Minute {
		t.Fatalf("requeueAfter = %s, want terminal backoff near 55m", result.RequeueAfter)
	}

	stored := &controlv1alpha1.AgentSchedule{}
	if err := reconciler.Get(ctx, client.ObjectKeyFromObject(schedule), stored); err != nil {
		t.Fatalf("get reconciled schedule: %v", err)
	}
	if stored.Status.NextRunAt == nil || !stored.Status.NextRunAt.Equal(&wantNextRunAt) {
		t.Fatalf("nextRunAt = %#v, want %s", stored.Status.NextRunAt, wantNextRunAt.Format(time.RFC3339))
	}
	if got, want := stored.Status.Phase, controlv1alpha1.AgentSchedulePhaseScheduled; got != want {
		t.Fatalf("phase = %q, want %q", got, want)
	}
	ready := apimeta.FindStatusCondition(stored.Status.Conditions, agentScheduleReady)
	if ready == nil {
		t.Fatal("Ready condition was not set")
	}
	if got, want := ready.Status, metav1.ConditionTrue; got != want {
		t.Fatalf("Ready status = %q, want %q", got, want)
	}
	if got, want := ready.Reason, "TerminalBackoff"; got != want {
		t.Fatalf("Ready reason = %q, want %q (message=%q)", got, want, ready.Message)
	}
	if !strings.Contains(ready.Message, string(controlv1alpha1.AgentRunPhaseFailed)) {
		t.Fatalf("Ready message = %q, want failed phase", ready.Message)
	}

	list := &controlv1alpha1.AgentRunList{}
	if err := reconciler.List(ctx, list, client.InNamespace(schedule.Namespace), client.MatchingLabels{agentRunScheduleLabel: schedule.Name}); err != nil {
		t.Fatalf("list agent runs: %v", err)
	}
	if got, want := len(list.Items), 1; got != want {
		t.Fatalf("agent runs = %d, want %d (no automatic run during backoff)", got, want)
	}

	if _, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(schedule)}); err != nil {
		t.Fatalf("reconcile schedule again: %v", err)
	}
	if err := reconciler.Get(ctx, client.ObjectKeyFromObject(schedule), stored); err != nil {
		t.Fatalf("get schedule after second reconcile: %v", err)
	}
	ready = apimeta.FindStatusCondition(stored.Status.Conditions, agentScheduleReady)
	if ready == nil || ready.Reason != "TerminalBackoff" {
		t.Fatalf("Ready condition after second reconcile = %#v, want TerminalBackoff", ready)
	}
}

func TestAgentScheduleRunNowBypassesTerminalBackoff(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := controlv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add control scheme: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	completedAt := metav1.NewTime(now.Add(-5 * time.Minute))
	existingNextRunAt := metav1.NewTime(now.Add(-time.Minute))
	wantNextRunAt := metav1.NewTime(completedAt.Add(time.Hour))
	schedule := testAgentScheduleWithPolicy(controlv1alpha1.AgentScheduleConcurrencyQueue)
	schedule.Generation = 1
	schedule.Annotations = map[string]string{
		controlv1alpha1.AgentScheduleRunNowAnnotation: "retry-after-failure",
	}
	schedule.Spec.Backoff = &controlv1alpha1.AgentScheduleBackoffSpec{FailedSeconds: 3600}
	schedule.Status = controlv1alpha1.AgentScheduleStatus{
		ObservedGeneration: 1,
		NextRunAt:          &existingNextRunAt,
	}
	failed := testScheduledAgentRun("platform-health-failed", now.Add(-10*time.Minute))
	failed.Status = controlv1alpha1.AgentRunStatus{
		Phase:       controlv1alpha1.AgentRunPhaseFailed,
		CompletedAt: &completedAt,
	}
	reconciler := &AgentScheduleReconciler{
		Client: fake.NewClientBuilder().
			WithScheme(scheme).
			WithStatusSubresource(&controlv1alpha1.AgentSchedule{}).
			WithObjects(schedule, failed).
			Build(),
		Scheme: scheme,
	}

	_, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(schedule)})
	if err != nil {
		t.Fatalf("reconcile schedule: %v", err)
	}

	stored := &controlv1alpha1.AgentSchedule{}
	if err := reconciler.Get(ctx, client.ObjectKeyFromObject(schedule), stored); err != nil {
		t.Fatalf("get reconciled schedule: %v", err)
	}
	if got, want := stored.Status.LastManualRunToken, "retry-after-failure"; got != want {
		t.Fatalf("lastManualRunToken = %q, want %q", got, want)
	}
	if stored.Status.NextRunAt == nil || !stored.Status.NextRunAt.Equal(&wantNextRunAt) {
		t.Fatalf("nextRunAt = %#v, want preserved backoff deadline %s", stored.Status.NextRunAt, wantNextRunAt.Format(time.RFC3339))
	}
	ready := apimeta.FindStatusCondition(stored.Status.Conditions, agentScheduleReady)
	if ready == nil || ready.Reason != "RunCreated" {
		t.Fatalf("Ready condition = %#v, want RunCreated", ready)
	}

	list := &controlv1alpha1.AgentRunList{}
	if err := reconciler.List(ctx, list, client.InNamespace(schedule.Namespace), client.MatchingLabels{agentRunScheduleLabel: schedule.Name}); err != nil {
		t.Fatalf("list agent runs: %v", err)
	}
	if got, want := len(list.Items), 2; got != want {
		t.Fatalf("agent runs = %d, want %d (failed run plus manual bypass)", got, want)
	}
	manualRuns := 0
	for i := range list.Items {
		if list.Items[i].Spec.Trigger.Reason == "ManualAgentScheduleNudge" {
			manualRuns++
		}
	}
	if got, want := manualRuns, 1; got != want {
		t.Fatalf("manual runs = %d, want %d", got, want)
	}
}

func TestAgentRunApplicationQueueBlocksDifferentScheduleForSameApplication(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := controlv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add control scheme: %v", err)
	}

	older := testApplicationScopedAgentRun("hazy-trade-health-20260708-120000", "hazy-trade", "hazy-trade-health-30m", "hazy-trade", time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC))
	newer := testApplicationScopedAgentRun("hazy-trade-pr-feedback-20260708-121500", "hazy-trade", "hazy-trade-pr-feedback-1h", "hazy-trade", time.Date(2026, 7, 8, 12, 15, 0, 0, time.UTC))
	anvil := testApplicationScopedAgentRun("platform-health-20260708-121500", "anvilhub", "platform-health-30m", "anvil-primaris", time.Date(2026, 7, 8, 12, 15, 0, 0, time.UTC))
	reconciler := &AgentRunReconciler{
		Client: fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(older, newer, anvil).
			Build(),
		Scheme: scheme,
	}

	blockedBy, err := reconciler.agentRunQueuedBehindApplication(ctx, newer)
	if err != nil {
		t.Fatalf("check application queue block: %v", err)
	}
	if blockedBy == nil {
		t.Fatal("newer Hazy Trade run was not blocked")
	}
	if got, want := blockedBy.Name, older.Name; got != want {
		t.Fatalf("blocked by %q, want %q", got, want)
	}

	blockedBy, err = reconciler.agentRunQueuedBehindApplication(ctx, anvil)
	if err != nil {
		t.Fatalf("check cross-application queue block: %v", err)
	}
	if blockedBy != nil {
		t.Fatalf("Anvil run was blocked by %s/%s from a different application", blockedBy.Namespace, blockedBy.Name)
	}
}

func TestAgentRunApplicationQueueHonorsApplicationMaxConcurrentRuns(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := controlv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add control scheme: %v", err)
	}

	first := testApplicationScopedAgentRun("hazy-trade-health-20260708-120000", "hazy-trade", "hazy-trade-health-30m", "hazy-trade", time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC))
	second := testApplicationScopedAgentRun("hazy-trade-pr-feedback-20260708-121500", "hazy-trade", "hazy-trade-pr-feedback-1h", "hazy-trade", time.Date(2026, 7, 8, 12, 15, 0, 0, time.UTC))
	third := testApplicationScopedAgentRun("hazy-trade-event-audit-20260708-123000", "hazy-trade", "hazy-trade-event-audit-4h", "hazy-trade", time.Date(2026, 7, 8, 12, 30, 0, 0, time.UTC))
	reconciler := &AgentRunReconciler{
		Client: fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(first, second, third).
			Build(),
		Scheme: scheme,
		CommonReconcilerOptions: CommonReconcilerOptions{Options: &Options{
			ApplicationMaxConcurrentRuns: 2,
		}},
	}

	blockedBy, err := reconciler.agentRunQueuedBehindApplication(ctx, second)
	if err != nil {
		t.Fatalf("check second application queue block: %v", err)
	}
	if blockedBy != nil {
		t.Fatalf("second run was blocked by %s/%s, want two allowed application runs", blockedBy.Namespace, blockedBy.Name)
	}

	blockedBy, err = reconciler.agentRunQueuedBehindApplication(ctx, third)
	if err != nil {
		t.Fatalf("check third application queue block: %v", err)
	}
	if blockedBy == nil {
		t.Fatal("third run was not blocked")
	}
	if got, want := blockedBy.Name, second.Name; got != want {
		t.Fatalf("third blocked by %q, want %q", got, want)
	}
}

func TestAgentRunApplicationQueueUsesAgentRunControlLimit(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := controlv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add control scheme: %v", err)
	}

	first := testApplicationScopedAgentRun("manager-1", "anvilhub", "manager", "anvil-primaris-agent-manager", time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC))
	second := testApplicationScopedAgentRun("manager-2", "anvilhub", "manager", "anvil-primaris-agent-manager", time.Date(2026, 7, 8, 12, 5, 0, 0, time.UTC))
	control := &controlv1alpha1.AgentRunControl{
		ObjectMeta: metav1.ObjectMeta{Name: "manager-concurrency"},
		Spec: controlv1alpha1.AgentRunControlSpec{
			ApplicationRef:    controlv1alpha1.ApplicationReferenceSpec{Name: "anvil-primaris-agent-manager"},
			LaunchPolicy:      controlv1alpha1.AgentRunControlLaunchPolicyAllowed,
			MaxConcurrentRuns: 1,
		},
	}
	reconciler := &AgentRunReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(first, second, control).Build(),
		Scheme: scheme,
		CommonReconcilerOptions: CommonReconcilerOptions{Options: &Options{
			ApplicationMaxConcurrentRuns: 4,
		}},
	}

	blockedBy, err := reconciler.agentRunQueuedBehindApplication(ctx, second)
	if err != nil {
		t.Fatalf("check per-control queue block: %v", err)
	}
	if blockedBy == nil || blockedBy.Name != first.Name {
		t.Fatalf("blockedBy = %#v, want %s", blockedBy, first.Name)
	}
}

func TestAgentRunApplicationQueueUsesProfileApplicationRef(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := controlv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add control scheme: %v", err)
	}

	profile := &controlv1alpha1.AgentRunProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hazy-trade-health",
			Namespace: "hazy-trade",
		},
		Spec: controlv1alpha1.AgentRunProfileSpec{
			Scope: controlv1alpha1.AgentRunScopeSpec{
				ApplicationRef: &controlv1alpha1.ApplicationReferenceSpec{Name: "hazy-trade"},
			},
		},
	}
	older := testScheduledAgentRun("hazy-trade-health-20260708-120000", time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC))
	older.Namespace = "hazy-trade"
	older.Spec.ProfileRef = &controlv1alpha1.NamespacedObjectReference{Name: "hazy-trade-health"}
	newer := testApplicationScopedAgentRun("hazy-trade-pr-feedback-20260708-121500", "hazy-trade", "hazy-trade-pr-feedback-1h", "hazy-trade", time.Date(2026, 7, 8, 12, 15, 0, 0, time.UTC))
	reconciler := &AgentRunReconciler{
		Client: fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(profile, older, newer).
			Build(),
		Scheme: scheme,
	}

	blockedBy, err := reconciler.agentRunQueuedBehindApplication(ctx, newer)
	if err != nil {
		t.Fatalf("check profile-backed application queue block: %v", err)
	}
	if blockedBy == nil {
		t.Fatal("newer Hazy Trade run was not blocked by profile-backed older run")
	}
	if got, want := blockedBy.Name, older.Name; got != want {
		t.Fatalf("blocked by %q, want %q", got, want)
	}
}

func TestAgentScheduleNextRunTimeRecomputesAfterGenerationChange(t *testing.T) {
	t.Parallel()

	lastRunAt := metav1.NewTime(time.Date(2026, 7, 9, 3, 45, 9, 0, time.UTC))
	staleNextRunAt := metav1.NewTime(time.Date(2026, 7, 9, 4, 45, 9, 0, time.UTC))
	now := time.Date(2026, 7, 9, 4, 25, 0, 0, time.UTC)
	schedule := testAgentScheduleWithPolicy(controlv1alpha1.AgentScheduleConcurrencyQueue)
	schedule.Spec.IntervalSeconds = 900
	status := controlv1alpha1.AgentScheduleStatus{
		LastRunAt: &lastRunAt,
		NextRunAt: &staleNextRunAt,
	}

	unchanged := agentScheduleNextRunTime(schedule, status, now, false)
	if !unchanged.Equal(&staleNextRunAt) {
		t.Fatalf("unchanged generation next run = %s, want stale future %s", unchanged.Time, staleNextRunAt.Time)
	}

	recomputed := agentScheduleNextRunTime(schedule, status, now, true)
	want := metav1.NewTime(time.Date(2026, 7, 9, 4, 0, 9, 0, time.UTC))
	if !recomputed.Equal(&want) {
		t.Fatalf("generation changed next run = %s, want recomputed %s", recomputed.Time, want.Time)
	}
}

func TestAgentScheduleNextRunWithTerminalBackoff(t *testing.T) {
	t.Parallel()

	existing := metav1.NewTime(time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC))
	schedule := testAgentScheduleWithPolicy(controlv1alpha1.AgentScheduleConcurrencyQueue)
	schedule.Spec.Backoff = &controlv1alpha1.AgentScheduleBackoffSpec{
		FailedSeconds:     3600,
		NeedsHumanSeconds: 1800,
	}
	failedCompletedAt := metav1.NewTime(time.Date(2026, 7, 15, 9, 30, 0, 0, time.UTC))
	failed := testScheduledAgentRun("failed", time.Date(2026, 7, 15, 9, 0, 0, 0, time.UTC))
	failed.Status = controlv1alpha1.AgentRunStatus{
		Phase:       controlv1alpha1.AgentRunPhaseFailed,
		CompletedAt: &failedCompletedAt,
	}
	needsHuman := testScheduledAgentRun("needs-human", time.Date(2026, 7, 15, 9, 45, 0, 0, time.UTC))
	needsHuman.Status.Phase = controlv1alpha1.AgentRunPhaseNeedsHuman
	succeeded := testScheduledAgentRun("succeeded", time.Date(2026, 7, 15, 9, 45, 0, 0, time.UTC))
	succeeded.Status.Phase = controlv1alpha1.AgentRunPhaseSucceeded

	tests := []struct {
		name        string
		schedule    *controlv1alpha1.AgentSchedule
		newest      *controlv1alpha1.AgentRun
		existing    metav1.Time
		want        time.Time
		wantApplied bool
	}{
		{
			name:        "failed uses completion time",
			schedule:    schedule,
			newest:      failed,
			existing:    existing,
			want:        time.Date(2026, 7, 15, 10, 30, 0, 0, time.UTC),
			wantApplied: true,
		},
		{
			name:        "needs human falls back to creation time",
			schedule:    schedule,
			newest:      needsHuman,
			existing:    existing,
			want:        time.Date(2026, 7, 15, 10, 15, 0, 0, time.UTC),
			wantApplied: true,
		},
		{
			name:        "existing next run remains later",
			schedule:    schedule,
			newest:      failed,
			existing:    metav1.NewTime(time.Date(2026, 7, 15, 11, 0, 0, 0, time.UTC)),
			want:        time.Date(2026, 7, 15, 11, 0, 0, 0, time.UTC),
			wantApplied: false,
		},
		{
			name:        "existing backoff deadline remains reported",
			schedule:    schedule,
			newest:      failed,
			existing:    metav1.NewTime(time.Date(2026, 7, 15, 10, 30, 0, 0, time.UTC)),
			want:        time.Date(2026, 7, 15, 10, 30, 0, 0, time.UTC),
			wantApplied: true,
		},
		{
			name:     "non-backoff phase preserves cadence",
			schedule: schedule,
			newest:   succeeded,
			existing: existing,
			want:     existing.Time,
		},
		{
			name: "zero values preserve default behavior",
			schedule: &controlv1alpha1.AgentSchedule{Spec: controlv1alpha1.AgentScheduleSpec{
				Backoff: &controlv1alpha1.AgentScheduleBackoffSpec{},
			}},
			newest:   failed,
			existing: existing,
			want:     existing.Time,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, applied := agentScheduleNextRunWithTerminalBackoff(tt.schedule, tt.newest, tt.existing)
			if !got.Time.Equal(tt.want) {
				t.Fatalf("next run = %s, want %s", got.Time, tt.want)
			}
			if applied != tt.wantApplied {
				t.Fatalf("backoff applied = %t, want %t", applied, tt.wantApplied)
			}
		})
	}
}

func TestAgentScheduleNextRunAfterCreationPreservesCadenceForManualRun(t *testing.T) {
	t.Parallel()

	schedule := testAgentScheduleWithPolicy(controlv1alpha1.AgentScheduleConcurrencyQueue)
	schedule.Spec.IntervalSeconds = 3600
	now := metav1.NewTime(time.Date(2026, 7, 10, 6, 3, 26, 0, time.UTC))
	scheduledNext := metav1.NewTime(time.Date(2026, 7, 10, 6, 15, 0, 0, time.UTC))

	manualNext := agentScheduleNextRunAfterCreation(schedule, scheduledNext, now, true)
	if !manualNext.Equal(&scheduledNext) {
		t.Fatalf("manual next run = %s, want preserved cadence %s", manualNext.Time, scheduledNext.Time)
	}

	due := metav1.NewTime(time.Date(2026, 7, 10, 6, 0, 0, 0, time.UTC))
	intervalNext := agentScheduleNextRunAfterCreation(schedule, due, now, false)
	wantInterval := metav1.NewTime(time.Date(2026, 7, 10, 7, 0, 0, 0, time.UTC))
	if !intervalNext.Equal(&wantInterval) {
		t.Fatalf("interval next run = %s, want %s", intervalNext.Time, wantInterval.Time)
	}
}

func TestAgentScheduleNextRunTimeKeepsDueCadenceAfterManualRun(t *testing.T) {
	t.Parallel()

	schedule := testAgentScheduleWithPolicy(controlv1alpha1.AgentScheduleConcurrencyQueue)
	schedule.Spec.IntervalSeconds = 3600
	manualRunAt := metav1.NewTime(time.Date(2026, 7, 10, 6, 3, 26, 0, time.UTC))
	scheduledNext := metav1.NewTime(time.Date(2026, 7, 10, 6, 15, 0, 0, time.UTC))
	now := time.Date(2026, 7, 10, 6, 15, 1, 0, time.UTC)
	status := controlv1alpha1.AgentScheduleStatus{
		LastRunAt: &manualRunAt,
		NextRunAt: &scheduledNext,
	}

	got := agentScheduleNextRunTime(schedule, status, now, false)
	if !got.Equal(&scheduledNext) {
		t.Fatalf("due next run = %s, want preserved cadence %s", got.Time, scheduledNext.Time)
	}
}

func TestAgentScheduleNextRunAfterCreationPreservesDueManualCadence(t *testing.T) {
	t.Parallel()

	schedule := testAgentScheduleWithPolicy(controlv1alpha1.AgentScheduleConcurrencyQueue)
	schedule.Spec.IntervalSeconds = 3600
	now := metav1.NewTime(time.Date(2026, 7, 10, 6, 15, 0, 0, time.UTC))
	due := metav1.NewTime(time.Date(2026, 7, 10, 6, 15, 0, 0, time.UTC))

	got := agentScheduleNextRunAfterCreation(schedule, due, now, true)
	if !got.Equal(&due) {
		t.Fatalf("due manual next run = %s, want preserved due slot %s", got.Time, due.Time)
	}
}

func TestAgentScheduleRunsKeepsManualTemplateOutOfIntervalRotation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := controlv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add control scheme: %v", err)
	}

	schedule := testAgentScheduleWithPolicy(controlv1alpha1.AgentScheduleConcurrencyQueue)
	schedule.Spec.RunTemplates = []controlv1alpha1.AgentScheduleRunTemplateSpec{
		{Name: "grok-4-5"},
		{Name: "composer-2-5"},
	}
	interval := testScheduledAgentRun("platform-health-20260710-060000", time.Date(2026, 7, 10, 6, 0, 0, 0, time.UTC))
	interval.Labels[agentRunTemplateLabel] = "grok-4-5"
	interval.Spec.Trigger.Reason = "ScheduledAgentRun"
	interval.Status.Phase = controlv1alpha1.AgentRunPhaseSucceeded
	manual := testScheduledAgentRun("platform-health-manual-20260710-060300", time.Date(2026, 7, 10, 6, 3, 0, 0, time.UTC))
	manual.Labels[agentRunTemplateLabel] = "composer-2-5"
	manual.Spec.Trigger.Reason = "ManualAgentScheduleNudge"
	manual.Status.Phase = controlv1alpha1.AgentRunPhaseSucceeded

	reconciler := &AgentScheduleReconciler{
		Client: fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(schedule, interval, manual).
			Build(),
		Scheme: scheme,
	}
	_, last, lastIntervalTemplate, _, err := reconciler.agentScheduleRuns(ctx, schedule, time.Date(2026, 7, 10, 7, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("list schedule runs: %v", err)
	}
	if last == nil || last.Name != manual.Name {
		t.Fatalf("latest run = %#v, want manual run %q", last, manual.Name)
	}
	if got, want := lastIntervalTemplate, "grok-4-5"; got != want {
		t.Fatalf("last interval template = %q, want %q", got, want)
	}
	if got, want := agentScheduleNextTemplateName(schedule, lastIntervalTemplate), "composer-2-5"; got != want {
		t.Fatalf("next interval template = %q, want %q", got, want)
	}
}

func TestAgentScheduleRunsUsesSanitizedLabelAndExactScheduleIdentity(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := controlv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add control scheme: %v", err)
	}
	schedule := &controlv1alpha1.AgentSchedule{
		ObjectMeta: metav1.ObjectMeta{Name: "review.v1", Namespace: "agents", UID: "review-v1-uid"},
		Spec:       controlv1alpha1.AgentScheduleSpec{IntervalSeconds: 3600},
	}
	matching := &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{Name: "matching", Namespace: "agents", Labels: map[string]string{agentRunScheduleLabel: "review-v1"}},
		Spec: controlv1alpha1.AgentRunSpec{
			ScheduleRef: &controlv1alpha1.NamespacedObjectReference{Name: "review.v1", Namespace: "agents"},
			SourceUID:   "review-v1-uid",
		},
	}
	collision := &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{Name: "collision", Namespace: "agents", Labels: map[string]string{agentRunScheduleLabel: "review-v1"}},
		Spec: controlv1alpha1.AgentRunSpec{
			ScheduleRef: &controlv1alpha1.NamespacedObjectReference{Name: "review-v1", Namespace: "agents"},
			SourceUID:   "other-uid",
		},
	}
	reconciler := &AgentScheduleReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(schedule, matching, collision).Build(),
		Scheme: scheme,
	}
	active, last, _, _, err := reconciler.agentScheduleRuns(ctx, schedule, time.Now())
	if err != nil {
		t.Fatalf("list schedule runs: %v", err)
	}
	if len(active) != 1 || active[0].Name != matching.Name || last == nil || last.Name != matching.Name {
		t.Fatalf("schedule runs = active:%v last:%v, want only %s", active, last, matching.Name)
	}
}

func TestAgentRunQueueGateIgnoresAllowPolicy(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := controlv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add control scheme: %v", err)
	}

	schedule := testAgentScheduleWithPolicy(controlv1alpha1.AgentScheduleConcurrencyAllow)
	older := testScheduledAgentRun("platform-health-20260708-120000", time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC))
	newer := testScheduledAgentRun("platform-health-20260708-123000", time.Date(2026, 7, 8, 12, 30, 0, 0, time.UTC))
	reconciler := &AgentRunReconciler{
		Client: fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(schedule, older, newer).
			Build(),
		Scheme: scheme,
	}

	blockedBy, err := reconciler.agentRunQueuedBehindSchedule(ctx, newer)
	if err != nil {
		t.Fatalf("check queue block: %v", err)
	}
	if blockedBy != nil {
		t.Fatalf("Allow policy blocked newer run behind %s/%s", blockedBy.Namespace, blockedBy.Name)
	}
}

type agentScheduleCreateCacheLagClient struct {
	client.Client
	agentRunGetAttempts int
}

func (c *agentScheduleCreateCacheLagClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if _, ok := obj.(*controlv1alpha1.AgentRun); ok {
		c.agentRunGetAttempts++
		return apierrors.NewNotFound(schema.GroupResource{
			Group:    controlv1alpha1.GroupVersion.Group,
			Resource: "agentruns",
		}, key.Name)
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

func testAgentScheduleWithPolicy(policy controlv1alpha1.AgentScheduleConcurrencyPolicy) *controlv1alpha1.AgentSchedule {
	return &controlv1alpha1.AgentSchedule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "platform-health-30m",
			Namespace: "anvilhub",
		},
		Spec: controlv1alpha1.AgentScheduleSpec{
			IntervalSeconds:   1800,
			ConcurrencyPolicy: policy,
		},
	}
}

func testScheduledAgentRun(name string, created time.Time) *controlv1alpha1.AgentRun {
	return &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         "anvilhub",
			CreationTimestamp: metav1.NewTime(created),
			Labels: map[string]string{
				agentRunScheduleLabel: "platform-health-30m",
			},
		},
		Spec: controlv1alpha1.AgentRunSpec{
			SourceRef: controlv1alpha1.AgentRunSourceRef{
				APIVersion: controlv1alpha1.GroupVersion.String(),
				Kind:       "AgentSchedule",
				Namespace:  "anvilhub",
				Name:       "platform-health-30m",
			},
			ScheduleRef: &controlv1alpha1.NamespacedObjectReference{
				Name:      "platform-health-30m",
				Namespace: "anvilhub",
			},
			Harness: controlv1alpha1.AgentRunHarnessSpec{
				Intent: controlv1alpha1.AgentRunIntentProposeChange,
			},
		},
	}
}

func testApplicationScopedAgentRun(name, namespace, scheduleName, applicationName string, created time.Time) *controlv1alpha1.AgentRun {
	run := testScheduledAgentRun(name, created)
	run.Namespace = namespace
	run.Labels[agentRunScheduleLabel] = scheduleName
	run.Spec.SourceRef.Namespace = namespace
	run.Spec.SourceRef.Name = scheduleName
	run.Spec.ScheduleRef = &controlv1alpha1.NamespacedObjectReference{
		Name:      scheduleName,
		Namespace: namespace,
	}
	run.Spec.Scope.ApplicationRef = &controlv1alpha1.ApplicationReferenceSpec{Name: applicationName}
	return run
}

func TestAgentScheduleOldestRunUsesLaunchGateOrdering(t *testing.T) {
	t.Parallel()

	same := metav1.NewTime(time.Date(2026, 7, 9, 15, 0, 0, 0, time.UTC))
	alpha := &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{Name: "run-b", Namespace: "anvilhub", CreationTimestamp: same},
	}
	beta := &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{Name: "run-a", Namespace: "anvilhub", CreationTimestamp: same},
	}
	// List order puts run-b first; launch-gate ordering prefers name "run-a".
	got := agentScheduleOldestRun([]*controlv1alpha1.AgentRun{alpha, beta})
	if got == nil || got.Name != "run-a" {
		t.Fatalf("oldest = %#v, want run-a (name tie-break)", got)
	}
	got = agentScheduleActiveRunForStatus(controlv1alpha1.AgentScheduleConcurrencyQueue, []*controlv1alpha1.AgentRun{alpha, beta})
	if got == nil || got.Name != "run-a" {
		t.Fatalf("queue head = %#v, want run-a", got)
	}
}
