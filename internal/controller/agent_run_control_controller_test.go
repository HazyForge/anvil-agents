package controller

import (
	"context"
	"strings"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	controlv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

func TestResolveAgentScheduleApplicationNameSupportsExplicitAndLegacyProfiles(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := newAgentControlTestScheme(t)
	profile := &controlv1alpha1.AgentRunProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "hazy-trade-health", Namespace: "hazy-trade"},
		Spec: controlv1alpha1.AgentRunProfileSpec{Scope: controlv1alpha1.AgentRunScopeSpec{
			ApplicationRef: &controlv1alpha1.ApplicationReferenceSpec{Name: "hazy-trade"},
		}},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(profile).Build()
	legacy := &controlv1alpha1.AgentSchedule{
		ObjectMeta: metav1.ObjectMeta{Name: "legacy", Namespace: "hazy-trade"},
		Spec: controlv1alpha1.AgentScheduleSpec{RunTemplate: controlv1alpha1.AgentRunSpec{
			ProfileRef: &controlv1alpha1.NamespacedObjectReference{Name: profile.Name},
		}},
	}
	name, err := resolveAgentScheduleApplicationName(ctx, c, legacy)
	if err != nil {
		t.Fatalf("resolve legacy schedule application: %v", err)
	}
	if name != "hazy-trade" {
		t.Fatalf("legacy application = %q, want hazy-trade", name)
	}

	explicit := legacy.DeepCopy()
	explicit.Name = "explicit"
	explicit.Spec.ApplicationRef = &controlv1alpha1.ApplicationReferenceSpec{Name: "hazy-trade"}
	name, err = resolveAgentScheduleApplicationName(ctx, c, explicit)
	if err != nil {
		t.Fatalf("resolve explicit schedule application: %v", err)
	}
	if name != "hazy-trade" {
		t.Fatalf("explicit application = %q, want hazy-trade", name)
	}
}

func TestResolveAgentScheduleApplicationNameRejectsMismatchAndMixedTemplates(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := newAgentControlTestScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	schedule := &controlv1alpha1.AgentSchedule{
		ObjectMeta: metav1.ObjectMeta{Name: "mixed", Namespace: "hazy-trade"},
		Spec: controlv1alpha1.AgentScheduleSpec{
			ApplicationRef: &controlv1alpha1.ApplicationReferenceSpec{Name: "hazy-trade"},
			RunTemplates: []controlv1alpha1.AgentScheduleRunTemplateSpec{
				{Name: "hazy", Template: controlv1alpha1.AgentRunSpec{Scope: controlv1alpha1.AgentRunScopeSpec{ApplicationRef: &controlv1alpha1.ApplicationReferenceSpec{Name: "hazy-trade"}}}},
				{Name: "other", Template: controlv1alpha1.AgentRunSpec{Scope: controlv1alpha1.AgentRunScopeSpec{ApplicationRef: &controlv1alpha1.ApplicationReferenceSpec{Name: "other-app"}}}},
			},
		},
	}
	if _, err := resolveAgentScheduleApplicationName(ctx, c, schedule); err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("mixed application error = %v, want conflict", err)
	}
}

func TestCreateScheduledAgentRunCopiesExplicitApplicationRef(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := newAgentControlTestScheme(t)
	reconciler := &AgentScheduleReconciler{Client: fake.NewClientBuilder().WithScheme(scheme).Build(), Scheme: scheme}
	schedule := &controlv1alpha1.AgentSchedule{
		ObjectMeta: metav1.ObjectMeta{Name: "health", Namespace: "hazy-trade", UID: types.UID("health-uid")},
		Spec: controlv1alpha1.AgentScheduleSpec{
			ApplicationRef:  &controlv1alpha1.ApplicationReferenceSpec{Name: "hazy-trade"},
			IntervalSeconds: 3600,
		},
	}
	now := metav1.NewTime(time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC))
	request := agentScheduleIntervalRunRequest(schedule, now, "")
	request.ApplicationName = "hazy-trade"
	run, _, err := reconciler.createScheduledAgentRun(ctx, schedule, now, request)
	if err != nil {
		t.Fatalf("create scheduled AgentRun: %v", err)
	}
	if run.Spec.Scope.ApplicationRef == nil || run.Spec.Scope.ApplicationRef.Name != "hazy-trade" {
		t.Fatalf("child applicationRef = %#v, want hazy-trade", run.Spec.Scope.ApplicationRef)
	}
}

func TestCreateScheduledAgentRunCopiesLegacyProfileApplicationRef(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := newAgentControlTestScheme(t)
	profile := &controlv1alpha1.AgentRunProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "health-profile", Namespace: "hazy-trade"},
		Spec: controlv1alpha1.AgentRunProfileSpec{Scope: controlv1alpha1.AgentRunScopeSpec{
			ApplicationRef: &controlv1alpha1.ApplicationReferenceSpec{Name: "hazy-trade"},
		}},
	}
	reconciler := &AgentScheduleReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(profile).Build(),
		Scheme: scheme,
	}
	schedule := &controlv1alpha1.AgentSchedule{
		ObjectMeta: metav1.ObjectMeta{Name: "legacy-health", Namespace: "hazy-trade", UID: types.UID("legacy-health-uid")},
		Spec: controlv1alpha1.AgentScheduleSpec{
			IntervalSeconds: 3600,
			RunTemplate: controlv1alpha1.AgentRunSpec{
				ProfileRef: &controlv1alpha1.NamespacedObjectReference{Name: profile.Name},
			},
		},
	}
	now := metav1.NewTime(time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC))
	request := agentScheduleIntervalRunRequest(schedule, now, "")
	request.ApplicationName = "hazy-trade"
	run, _, err := reconciler.createScheduledAgentRun(ctx, schedule, now, request)
	if err != nil {
		t.Fatalf("create legacy scheduled AgentRun: %v", err)
	}
	if run.Spec.Scope.ApplicationRef == nil || run.Spec.Scope.ApplicationRef.Name != "hazy-trade" {
		t.Fatalf("child applicationRef = %#v, want profile-resolved hazy-trade", run.Spec.Scope.ApplicationRef)
	}
}

func TestPausedAgentRunControlSuspendsScheduleWithoutCreatingChild(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := newAgentControlTestScheme(t)
	schedule := &controlv1alpha1.AgentSchedule{
		ObjectMeta: metav1.ObjectMeta{Name: "health", Namespace: "hazy-trade", UID: types.UID("health-uid"), Generation: 1},
		Spec: controlv1alpha1.AgentScheduleSpec{
			ApplicationRef:  &controlv1alpha1.ApplicationReferenceSpec{Name: "hazy-trade"},
			IntervalSeconds: 3600,
		},
	}
	control := pausedAgentRunControl("hazy-trade-control", "hazy-trade", nil)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(schedule, control).WithStatusSubresource(schedule).Build()
	reconciler := &AgentScheduleReconciler{Client: c, Scheme: scheme}
	if _, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKey{Namespace: schedule.Namespace, Name: schedule.Name}}); err != nil {
		t.Fatalf("reconcile paused schedule: %v", err)
	}
	runs := &controlv1alpha1.AgentRunList{}
	if err := c.List(ctx, runs); err != nil {
		t.Fatalf("list AgentRuns: %v", err)
	}
	if len(runs.Items) != 0 {
		t.Fatalf("created %d AgentRuns while paused", len(runs.Items))
	}
	updated := &controlv1alpha1.AgentSchedule{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: schedule.Namespace, Name: schedule.Name}, updated); err != nil {
		t.Fatalf("get schedule: %v", err)
	}
	if updated.Status.Phase != controlv1alpha1.AgentSchedulePhaseSuspended {
		t.Fatalf("phase = %q, want Suspended", updated.Status.Phase)
	}
	ready := apimeta.FindStatusCondition(updated.Status.Conditions, agentScheduleReady)
	if ready == nil || ready.Reason != "ApplicationPaused" {
		t.Fatalf("ready condition = %#v, want ApplicationPaused", ready)
	}
}

func TestPausedControlAndSuspendedScheduleBlockPendingRunLaunch(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name           string
		control        *controlv1alpha1.AgentRunControl
		suspend        bool
		expectedReason string
	}{
		{name: "application control", control: pausedAgentRunControl("hazy-trade-control", "hazy-trade", nil), expectedReason: "ApplicationPaused"},
		{name: "suspended schedule", suspend: true, expectedReason: "ScheduleSuspended"},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			scheme := newAgentControlTestScheme(t)
			schedule := &controlv1alpha1.AgentSchedule{
				ObjectMeta: metav1.ObjectMeta{Name: "health", Namespace: "hazy-trade"},
				Spec:       controlv1alpha1.AgentScheduleSpec{Suspend: test.suspend, IntervalSeconds: 3600},
			}
			run := testPendingApplicationRun("health-run", schedule.Name)
			objects := []client.Object{schedule, run}
			if test.control != nil {
				objects = append(objects, test.control)
			}
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).WithStatusSubresource(run).Build()
			reconciler := &AgentRunReconciler{Client: c, Scheme: scheme}
			if _, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKey{Namespace: run.Namespace, Name: run.Name}}); err != nil {
				t.Fatalf("reconcile pending run: %v", err)
			}
			jobs := &batchv1.JobList{}
			if err := c.List(ctx, jobs); err != nil {
				t.Fatalf("list Jobs: %v", err)
			}
			if len(jobs.Items) != 0 {
				t.Fatalf("created %d Jobs while launch was paused", len(jobs.Items))
			}
			updated := &controlv1alpha1.AgentRun{}
			if err := c.Get(ctx, client.ObjectKey{Namespace: run.Namespace, Name: run.Name}, updated); err != nil {
				t.Fatalf("get run: %v", err)
			}
			ready := apimeta.FindStatusCondition(updated.Status.Conditions, agentRunReady)
			if updated.Status.Phase != controlv1alpha1.AgentRunPhasePending || ready == nil || ready.Reason != test.expectedReason {
				t.Fatalf("run status = phase %q condition %#v, want Pending/%s", updated.Status.Phase, ready, test.expectedReason)
			}
		})
	}
}

func TestPausedControlBlocksManualAndAdverseApplicationRuns(t *testing.T) {
	t.Parallel()

	for _, purpose := range []controlv1alpha1.AgentRunPurpose{
		controlv1alpha1.AgentRunPurposeManual,
		controlv1alpha1.AgentRunPurposeAdverseSituation,
	} {
		purpose := purpose
		t.Run(string(purpose), func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			scheme := newAgentControlTestScheme(t)
			run := testPendingApplicationRun("pending-"+string(purpose), "source")
			run.Spec.Purpose = purpose
			run.Spec.ScheduleRef = nil
			run.Spec.SourceRef.Kind = "AdverseSituation"
			if purpose == controlv1alpha1.AgentRunPurposeManual {
				run.Spec.SourceRef.Kind = "Operator"
			}
			control := pausedAgentRunControl("hazy-trade-control", "hazy-trade", nil)
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(run, control).WithStatusSubresource(run).Build()
			reconciler := &AgentRunReconciler{Client: c, Scheme: scheme}
			if _, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKey{Namespace: run.Namespace, Name: run.Name}}); err != nil {
				t.Fatalf("reconcile %s run: %v", purpose, err)
			}
			jobs := &batchv1.JobList{}
			if err := c.List(ctx, jobs); err != nil {
				t.Fatalf("list Jobs: %v", err)
			}
			if len(jobs.Items) != 0 {
				t.Fatalf("created %d Jobs for paused %s run", len(jobs.Items), purpose)
			}
			updated := &controlv1alpha1.AgentRun{}
			if err := c.Get(ctx, client.ObjectKey{Namespace: run.Namespace, Name: run.Name}, updated); err != nil {
				t.Fatalf("get run: %v", err)
			}
			ready := apimeta.FindStatusCondition(updated.Status.Conditions, agentRunReady)
			if updated.Status.Phase != controlv1alpha1.AgentRunPhasePending || ready == nil || ready.Reason != "ApplicationPaused" {
				t.Fatalf("run status = phase %q condition %#v, want Pending/ApplicationPaused", updated.Status.Phase, ready)
			}
		})
	}
}

func TestPausedControlDoesNotInterruptAlreadyCreatedJob(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := newAgentControlTestScheme(t)
	run := testPendingApplicationRun("health-run", "health")
	run.UID = types.UID("health-run-uid")
	run.Status = controlv1alpha1.AgentRunStatus{
		ObservedGeneration: 1,
		Phase:              controlv1alpha1.AgentRunPhaseRunning,
		PromptHash:         "existing",
		JobRef:             &controlv1alpha1.NamespacedObjectReference{Name: "health-run-harness-existing", Namespace: run.Namespace},
	}
	controller := true
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: run.Status.JobRef.Name, Namespace: run.Namespace, Labels: map[string]string{agentRunJobLabel: run.Status.JobRef.Name}, OwnerReferences: []metav1.OwnerReference{{APIVersion: controlv1alpha1.GroupVersion.String(), Kind: "AgentRun", Name: run.Name, UID: run.UID, Controller: &controller}}},
		Status:     batchv1.JobStatus{Active: 1},
	}
	control := pausedAgentRunControl("hazy-trade-control", "hazy-trade", nil)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(run, job, control).WithStatusSubresource(run).Build()
	reconciler := &AgentRunReconciler{Client: c, Scheme: scheme}
	if _, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKey{Namespace: run.Namespace, Name: run.Name}}); err != nil {
		t.Fatalf("reconcile active run: %v", err)
	}
	jobs := &batchv1.JobList{}
	if err := c.List(ctx, jobs); err != nil {
		t.Fatalf("list Jobs: %v", err)
	}
	if len(jobs.Items) != 1 || jobs.Items[0].Name != job.Name {
		t.Fatalf("Jobs = %#v, want existing Job %q", jobs.Items, job.Name)
	}
}

func TestPausedControlRejectsInjectedJobCreatedBeforeStatusPersistence(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := newAgentControlTestScheme(t)
	run := testPendingApplicationRun("crash-gap-run", "health")
	run.UID = types.UID("crash-gap-run-uid")
	controller := true
	jobName := agentRunChildName(run.Name, "harness", shortHash(buildAgentRunPrompt(run)))
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: run.Namespace,
			Labels:    agentRunLabels(run, jobName),
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: controlv1alpha1.GroupVersion.String(),
				Kind:       "AgentRun",
				Name:       run.Name,
				UID:        run.UID,
				Controller: &controller,
			}},
		},
		Spec: batchv1.JobSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{{
				Name:  "agent",
				Image: "ghcr.io/hazyforge/anvil-agent-run-codex:test",
				Env: []corev1.EnvVar{
					{Name: "ANVIL_AGENT_RUN_BACKEND", Value: "codex"},
					{Name: "ANVIL_AGENT_RUN_INTENT", Value: "observe"},
				},
			}},
		}}},
		Status: batchv1.JobStatus{Conditions: []batchv1.JobCondition{{Type: batchv1.JobComplete, Status: corev1.ConditionTrue}}},
	}
	control := pausedAgentRunControl("hazy-trade-control", "hazy-trade", nil)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(run, job, control).WithStatusSubresource(run).Build()
	reconciler := &AgentRunReconciler{Client: c, Scheme: scheme}
	if _, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKey{Namespace: run.Namespace, Name: run.Name}}); err == nil || !strings.Contains(err.Error(), "AgentRun Job") {
		t.Fatalf("reconcile injected crash-gap Job error = %v, want exact-spec rejection", err)
	}

	updated := &controlv1alpha1.AgentRun{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: run.Namespace, Name: run.Name}, updated); err != nil {
		t.Fatalf("get adopted run: %v", err)
	}
	if updated.Status.JobRef != nil {
		t.Fatalf("jobRef = %#v, want injected Job to remain untrusted", updated.Status.JobRef)
	}
	jobs := &batchv1.JobList{}
	if err := c.List(ctx, jobs); err != nil {
		t.Fatalf("list Jobs: %v", err)
	}
	if len(jobs.Items) != 1 || jobs.Items[0].Name != job.Name {
		t.Fatalf("Jobs = %#v, want only adopted Job %q", jobs.Items, job.Name)
	}
}

func TestPausedControlRecoversExactJobCreatedBeforeStatusPersistence(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := newAgentControlTestScheme(t)
	run := testPendingApplicationRun("exact-crash-gap-run", "health")
	run.UID = types.UID("exact-crash-gap-run-uid")
	seed := &AgentRunReconciler{Client: fake.NewClientBuilder().WithScheme(scheme).Build(), Scheme: scheme}
	effective, composition, phase, reason, message, err := seed.resolveAgentRunComposition(ctx, run)
	if err != nil || phase != "" {
		t.Fatalf("resolve composition = phase %q reason %q message %q err %v", phase, reason, message, err)
	}
	prompt := buildAgentRunPrompt(effective)
	promptHash := shortHash(prompt)
	effective.Status.ResolvedComposition = composition.DeepCopy()
	contextBody, err := seed.agentRunContextJSON(ctx, effective)
	if err != nil {
		t.Fatalf("render context: %v", err)
	}
	data, err := seed.agentRunConfigMapData(ctx, effective, prompt, string(contextBody))
	if err != nil {
		t.Fatalf("render payload: %v", err)
	}
	controller := true
	owner := metav1.OwnerReference{APIVersion: controlv1alpha1.GroupVersion.String(), Kind: "AgentRun", Name: run.Name, UID: run.UID, Controller: &controller}
	configMap := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
		Name: agentRunChildName(run.Name, "context", promptHash), Namespace: run.Namespace,
		Labels: agentRunLabels(effective, ""), OwnerReferences: []metav1.OwnerReference{owner},
	}, Data: data, Immutable: boolPtr(true)}
	composition.PayloadDigest = digestJSON(data)
	effective.Status.ResolvedComposition = composition.DeepCopy()
	jobName := agentRunChildName(run.Name, "harness", promptHash)
	job := seed.agentRunJob(effective, jobName, configMap.Name, nil)
	job.UID = "exact-job-uid"
	job.OwnerReferences = []metav1.OwnerReference{owner}
	job.Status.Conditions = []batchv1.JobCondition{{Type: batchv1.JobComplete, Status: corev1.ConditionTrue}}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(run, configMap, job).WithStatusSubresource(run).Build()
	reconciler := &AgentRunReconciler{Client: c, Scheme: scheme}
	if _, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(run)}); err != nil {
		t.Fatalf("reconcile exact crash-gap Job: %v", err)
	}
	updated := &controlv1alpha1.AgentRun{}
	if err := c.Get(ctx, client.ObjectKeyFromObject(run), updated); err != nil {
		t.Fatalf("get recovered run: %v", err)
	}
	if updated.Status.JobRef == nil || updated.Status.JobRef.Name != job.Name || updated.Status.Phase != controlv1alpha1.AgentRunPhaseSucceeded {
		t.Fatalf("recovered status = %#v, want exact Job succeeded", updated.Status)
	}
	if updated.Status.JobUID != string(job.UID) {
		t.Fatalf("recovered Job UID = %q, want %q", updated.Status.JobUID, job.UID)
	}
	if updated.Status.ResolvedComposition == nil || updated.Status.ResolvedComposition.ResolvedAt == nil {
		t.Fatalf("recovered composition = %#v, want status-only resolvedAt", updated.Status.ResolvedComposition)
	}
	if strings.Contains(job.Annotations[agentRunAnnotationComposition], "resolvedAt") {
		t.Fatal("Job composition annotation contains retry-unstable resolvedAt")
	}
}

func TestAgentRunPersistsLaunchReceiptBeforeCreatingJob(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := newAgentControlTestScheme(t)
	run := testPendingApplicationRun("planned-launch", "health")
	run.UID = types.UID("planned-launch-uid")
	run.Spec.Harness.Backend = controlv1alpha1.AgentRunHarnessBackendSpec{
		Kind:   controlv1alpha1.AgentRunHarnessBackendCustom,
		Image:  "busybox:1.37.0",
		Custom: &controlv1alpha1.AgentRunCustomBackendSpec{Command: []string{"/bin/true"}},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(run).
		WithStatusSubresource(run).
		WithInterceptorFuncs(interceptor.Funcs{Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if obj.GetUID() == "" {
				obj.SetUID(types.UID("fake-" + obj.GetName() + "-uid"))
			}
			return c.Create(ctx, obj, opts...)
		}}).
		Build()
	reconciler := &AgentRunReconciler{Client: c, Scheme: scheme}
	request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(run)}

	if _, err := reconciler.Reconcile(ctx, request); err != nil {
		t.Fatalf("plan AgentRun launch: %v", err)
	}
	jobs := &batchv1.JobList{}
	if err := c.List(ctx, jobs); err != nil {
		t.Fatalf("list Jobs after launch planning: %v", err)
	}
	if len(jobs.Items) != 0 {
		t.Fatalf("Jobs after launch planning = %#v, want none before receipt persistence", jobs.Items)
	}
	planned := &controlv1alpha1.AgentRun{}
	if err := c.Get(ctx, client.ObjectKeyFromObject(run), planned); err != nil {
		t.Fatalf("get planned AgentRun: %v", err)
	}
	if !agentRunLaunchReceiptComplete(&planned.Status) || planned.Status.JobUID != "" {
		t.Fatalf("planned launch receipt = %#v", planned.Status)
	}
	if planned.Status.JobRef != nil || planned.Status.PlannedJobRef == nil {
		t.Fatalf("planned/created Job references = %#v/%#v, want plan only", planned.Status.PlannedJobRef, planned.Status.JobRef)
	}
	if planned.Status.PayloadUID == "" {
		t.Fatal("planned launch receipt omitted payload UID")
	}

	if _, err := reconciler.Reconcile(ctx, request); err != nil {
		t.Fatalf("create AgentRun Job from launch receipt: %v", err)
	}
	if err := c.List(ctx, jobs); err != nil {
		t.Fatalf("list Jobs after launch: %v", err)
	}
	if len(jobs.Items) != 1 || jobs.Items[0].Name != planned.Status.PlannedJobRef.Name {
		t.Fatalf("Jobs after launch = %#v, want planned Job %q", jobs.Items, planned.Status.PlannedJobRef.Name)
	}
	launched := &controlv1alpha1.AgentRun{}
	if err := c.Get(ctx, client.ObjectKeyFromObject(run), launched); err != nil {
		t.Fatalf("get launched AgentRun: %v", err)
	}
	if launched.Status.JobCreateAttemptedAt == nil || launched.Status.JobRef == nil || launched.Status.JobUID == "" {
		t.Fatalf("launched execution receipt = %#v", launched.Status)
	}
}

func TestAgentRunCrashGapRecoveryUsesRecordedSnapshotAfterProfileDeletion(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := newAgentControlTestScheme(t)
	run, payload, job := plannedProfileAgentRunExecution(t, ctx, scheme)
	job.Status.Conditions = []batchv1.JobCondition{{Type: batchv1.JobComplete, Status: corev1.ConditionTrue}}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(run, payload, job).
		WithStatusSubresource(run).
		Build()
	reconciler := &AgentRunReconciler{Client: c, Scheme: scheme}

	if _, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(run)}); err != nil {
		t.Fatalf("recover exact Job after profile deletion: %v", err)
	}
	updated := &controlv1alpha1.AgentRun{}
	if err := c.Get(ctx, client.ObjectKeyFromObject(run), updated); err != nil {
		t.Fatalf("get recovered AgentRun: %v", err)
	}
	if updated.Status.JobUID != string(job.UID) || updated.Status.Phase != controlv1alpha1.AgentRunPhaseSucceeded {
		t.Fatalf("recovered status = %#v, want recorded Job succeeded", updated.Status)
	}
	if updated.Status.ResolvedComposition == nil || updated.Status.ResolvedComposition.ProfileRef == nil || updated.Status.ResolvedComposition.ProfileRef.Name != "launch-profile" {
		t.Fatalf("recovered composition = %#v, want launch-time profile receipt", updated.Status.ResolvedComposition)
	}
}

func TestAgentRunRejectsPlannedJobWithoutCreateAttemptReceipt(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := newAgentControlTestScheme(t)
	run, payload, job := plannedProfileAgentRunExecution(t, ctx, scheme)
	run.Status.JobCreateAttemptedAt = nil
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(run, payload, job).
		WithStatusSubresource(run).
		Build()
	reconciler := &AgentRunReconciler{Client: c, Scheme: scheme}

	_, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(run)})
	if err == nil || !strings.Contains(err.Error(), "without the required create-attempt receipt") {
		t.Fatalf("unattempted planned Job error = %v, want create-attempt rejection", err)
	}
}

func TestAgentRunCrashGapRecoveryRejectsJobTampering(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := newAgentControlTestScheme(t)
	run, payload, job := plannedProfileAgentRunExecution(t, ctx, scheme)
	job.Spec.Template.Spec.Containers[0].Image = "example.invalid/injected:latest"
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(run, payload, job).
		WithStatusSubresource(run).
		Build()
	reconciler := &AgentRunReconciler{Client: c, Scheme: scheme}

	_, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(run)})
	if err == nil || !strings.Contains(err.Error(), "recorded execution snapshot") {
		t.Fatalf("tampered recovery error = %v, want execution snapshot rejection", err)
	}
	updated := &controlv1alpha1.AgentRun{}
	if err := c.Get(ctx, client.ObjectKeyFromObject(run), updated); err != nil {
		t.Fatalf("get rejected AgentRun: %v", err)
	}
	if updated.Status.JobUID != "" {
		t.Fatalf("rejected Job UID = %q, want untrusted Job to remain unpinned", updated.Status.JobUID)
	}
}

func TestAgentRunCrashGapRecoveryRejectsPayloadReplacementUID(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := newAgentControlTestScheme(t)
	run, payload, job := plannedProfileAgentRunExecution(t, ctx, scheme)
	payload.UID = types.UID("replacement-payload-uid")
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(run, payload, job).
		WithStatusSubresource(run).
		Build()
	reconciler := &AgentRunReconciler{Client: c, Scheme: scheme}

	_, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(run)})
	if err == nil || !strings.Contains(err.Error(), "does not match recorded UID") {
		t.Fatalf("replacement payload error = %v, want UID rejection", err)
	}
	updated := &controlv1alpha1.AgentRun{}
	if err := c.Get(ctx, client.ObjectKeyFromObject(run), updated); err != nil {
		t.Fatalf("get rejected AgentRun: %v", err)
	}
	if updated.Status.JobUID != "" {
		t.Fatalf("rejected Job UID = %q, want Job to remain unpinned", updated.Status.JobUID)
	}
}

func TestAgentRunDoesNotRecreateMissingJobAfterCreateAttempt(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := newAgentControlTestScheme(t)
	run, payload, _ := plannedProfileAgentRunExecution(t, ctx, scheme)
	run.Generation++
	run.Status.ObservedGeneration = run.Generation - 1
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(run, payload).
		WithStatusSubresource(run).
		Build()
	reconciler := &AgentRunReconciler{Client: c, Scheme: scheme}

	if _, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(run)}); err != nil {
		t.Fatalf("reconcile missing post-attempt Job: %v", err)
	}
	jobs := &batchv1.JobList{}
	if err := c.List(ctx, jobs); err != nil {
		t.Fatalf("list Jobs: %v", err)
	}
	if len(jobs.Items) != 0 {
		t.Fatalf("recreated %d Jobs after an ambiguous create attempt", len(jobs.Items))
	}
	updated := &controlv1alpha1.AgentRun{}
	if err := c.Get(ctx, client.ObjectKeyFromObject(run), updated); err != nil {
		t.Fatalf("get failed AgentRun: %v", err)
	}
	ready := apimeta.FindStatusCondition(updated.Status.Conditions, agentRunReady)
	if updated.Status.Phase != controlv1alpha1.AgentRunPhaseFailed || ready == nil || ready.Reason != "HarnessJobMissing" {
		t.Fatalf("ambiguous create status = %#v, want Failed/HarnessJobMissing", updated.Status)
	}
	if updated.Status.ObservedGeneration != run.Generation || !agentRunLaunchReceiptComplete(&updated.Status) || updated.Status.JobCreateAttemptedAt == nil {
		t.Fatalf("generation change did not preserve the complete launch receipt: %#v", updated.Status)
	}
}

func plannedProfileAgentRunExecution(t *testing.T, ctx context.Context, scheme *runtime.Scheme) (*controlv1alpha1.AgentRun, *corev1.ConfigMap, *batchv1.Job) {
	t.Helper()

	run := testPendingApplicationRun("profile-crash-gap", "health")
	run.UID = types.UID("profile-crash-gap-uid")
	run.Spec.ProfileRef = &controlv1alpha1.NamespacedObjectReference{Name: "launch-profile", Namespace: run.Namespace}
	run.Spec.Harness.Backend = controlv1alpha1.AgentRunHarnessBackendSpec{
		Kind:   controlv1alpha1.AgentRunHarnessBackendCustom,
		Image:  "busybox:1.37.0",
		Custom: &controlv1alpha1.AgentRunCustomBackendSpec{Command: []string{"/bin/true"}},
	}
	profile := &controlv1alpha1.AgentRunProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "launch-profile", Namespace: run.Namespace, UID: types.UID("launch-profile-uid"), Generation: 1, ResourceVersion: "7"},
		Spec:       controlv1alpha1.AgentRunProfileSpec{Harness: controlv1alpha1.AgentRunHarnessSpec{SystemPrompt: "launch-time profile instructions"}},
	}
	seed := &AgentRunReconciler{Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(profile).Build(), Scheme: scheme}
	effective, composition, phase, reason, message, err := seed.resolveAgentRunComposition(ctx, run)
	if err != nil || phase != "" {
		t.Fatalf("resolve launch composition = phase %q reason %q message %q err %v", phase, reason, message, err)
	}
	prompt := buildAgentRunPrompt(effective)
	promptHash := shortHash(prompt)
	effective.Status.ResolvedComposition = composition.DeepCopy()
	contextBody, err := seed.agentRunContextJSON(ctx, effective)
	if err != nil {
		t.Fatalf("render launch context: %v", err)
	}
	data, err := seed.agentRunConfigMapData(ctx, effective, prompt, string(contextBody))
	if err != nil {
		t.Fatalf("render launch payload: %v", err)
	}
	controller := true
	owner := metav1.OwnerReference{APIVersion: controlv1alpha1.GroupVersion.String(), Kind: "AgentRun", Name: run.Name, UID: run.UID, Controller: &controller}
	payload := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
		Name: agentRunChildName(run.Name, "context", promptHash), Namespace: run.Namespace, UID: types.UID("payload-uid"),
		Labels: agentRunLabels(effective, ""), OwnerReferences: []metav1.OwnerReference{owner},
	}, Data: data, Immutable: boolPtr(true)}
	composition.PayloadDigest = digestJSON(data)
	effective.Status.ResolvedComposition = composition.DeepCopy()
	jobName := agentRunChildName(run.Name, "harness", promptHash)
	job := seed.agentRunJob(effective, jobName, payload.Name, nil)
	job.UID = types.UID("job-uid")
	job.OwnerReferences = []metav1.OwnerReference{owner}
	jobDigest, err := agentRunJobSnapshotDigest(job)
	if err != nil {
		t.Fatalf("digest launch Job: %v", err)
	}
	attemptedAt := metav1.Now()
	run.Status = controlv1alpha1.AgentRunStatus{
		ObservedGeneration:   run.Generation,
		Phase:                controlv1alpha1.AgentRunPhasePending,
		PromptHash:           promptHash,
		PlannedJobRef:        &controlv1alpha1.NamespacedObjectReference{Name: job.Name, Namespace: job.Namespace},
		JobCreateAttemptedAt: &attemptedAt,
		JobSpecDigest:        jobDigest,
		PayloadRef:           &controlv1alpha1.NamespacedObjectReference{Name: payload.Name, Namespace: payload.Namespace},
		PayloadUID:           string(payload.UID),
		ResolvedComposition:  composition.DeepCopy(),
	}
	return run, payload, job
}

func TestReferencedAgentRunJobRejectsSameNameWithDifferentOwner(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := newAgentControlTestScheme(t)
	run := testPendingApplicationRun("owned-run", "health")
	run.UID = types.UID("owned-run-uid")
	ref := &controlv1alpha1.NamespacedObjectReference{Name: "owned-run-harness", Namespace: run.Namespace}
	controller := true
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{
		Name:      ref.Name,
		Namespace: ref.Namespace,
		OwnerReferences: []metav1.OwnerReference{{
			APIVersion: controlv1alpha1.GroupVersion.String(),
			Kind:       "AgentRun",
			Name:       "different-run",
			UID:        types.UID("different-run-uid"),
			Controller: &controller,
		}},
	}}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(job).Build()
	reconciler := &AgentRunReconciler{Client: c, Scheme: scheme}
	resolved, message, err := reconciler.existingAgentRunJob(ctx, run, ref, "", false)
	if err != nil {
		t.Fatalf("resolve referenced Job: %v", err)
	}
	if resolved != nil || !strings.Contains(message, "not controller-owned") {
		t.Fatalf("resolved/message = %#v/%q, want ownership rejection", resolved, message)
	}
}

func TestReferencedAgentRunJobRejectsReplacementUID(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := newAgentControlTestScheme(t)
	run := testPendingApplicationRun("owned-run", "health")
	run.UID = types.UID("owned-run-uid")
	ref := &controlv1alpha1.NamespacedObjectReference{Name: "owned-run-harness", Namespace: run.Namespace}
	controller := true
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{
		Name:      ref.Name,
		Namespace: ref.Namespace,
		UID:       types.UID("replacement-job-uid"),
		OwnerReferences: []metav1.OwnerReference{{
			APIVersion: controlv1alpha1.GroupVersion.String(),
			Kind:       "AgentRun",
			Name:       run.Name,
			UID:        run.UID,
			Controller: &controller,
		}},
	}}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(job).Build()
	reconciler := &AgentRunReconciler{Client: c, Scheme: scheme}
	resolved, message, err := reconciler.existingAgentRunJob(ctx, run, ref, "original-job-uid", false)
	if err != nil {
		t.Fatalf("resolve referenced Job: %v", err)
	}
	if resolved != nil || !strings.Contains(message, "does not match recorded UID") {
		t.Fatalf("resolved/message = %#v/%q, want UID rejection", resolved, message)
	}
}

func TestExpiredPauseDoesNotBlockLaunch(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := newAgentControlTestScheme(t)
	expiresAt := metav1.NewTime(time.Now().Add(-time.Minute))
	control := pausedAgentRunControl("expired", "hazy-trade", &expiresAt)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(control).Build()
	pause, err := activeAgentRunPauseForApplication(ctx, c, "hazy-trade", time.Now())
	if err != nil {
		t.Fatalf("resolve active pause: %v", err)
	}
	if pause != nil {
		t.Fatalf("expired control still paused launches: %#v", pause)
	}
}

func TestAuthoritativeLaunchGateUsesUncachedReader(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := newAgentControlTestScheme(t)
	run := testPendingApplicationRun("authoritative-gate", "source")
	stale := fake.NewClientBuilder().WithScheme(scheme).Build()
	fresh := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pausedAgentRunControl("fresh-hold", "hazy-trade", nil)).Build()
	reconciler := &AgentRunReconciler{
		Client: stale,
		Scheme: scheme,
		CommonReconcilerOptions: CommonReconcilerOptions{
			APIReader: fresh,
		},
	}
	paused, reason, _, _, err := reconciler.agentRunLaunchPausedAuthoritative(ctx, run)
	if err != nil {
		t.Fatalf("authoritative launch gate: %v", err)
	}
	if !paused || reason != "ApplicationPaused" {
		t.Fatalf("paused/reason = %t/%q, want true/ApplicationPaused", paused, reason)
	}
}

func TestAllowedControlDoesNotOverrideActivePause(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := newAgentControlTestScheme(t)
	applicationName := "hazy-trade"
	paused := pausedAgentRunControl("pause", applicationName, nil)
	allowed := &controlv1alpha1.AgentRunControl{
		ObjectMeta: metav1.ObjectMeta{Name: "allow"},
		Spec: controlv1alpha1.AgentRunControlSpec{
			ApplicationRef: controlv1alpha1.ApplicationReferenceSpec{Name: applicationName},
			LaunchPolicy:   controlv1alpha1.AgentRunControlLaunchPolicyAllowed,
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(paused, allowed).Build()
	pause, err := activeAgentRunPauseForApplication(ctx, c, applicationName, time.Now())
	if err != nil {
		t.Fatalf("resolve active pause: %v", err)
	}
	if pause == nil || pause.ControlName != paused.Name {
		t.Fatalf("active pause = %#v, want %q", pause, paused.Name)
	}
}

func TestPausedControlWithoutReasonIsBlocked(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := newAgentControlTestScheme(t)
	applicationName := "hazy-trade"
	control := pausedAgentRunControl("invalid", applicationName, nil)
	control.Generation = 1
	control.Spec.Reason = "   "
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(control).WithStatusSubresource(control).Build()
	reconciler := &AgentRunControlReconciler{Client: c, Scheme: scheme}
	if _, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKey{Name: control.Name}}); err != nil {
		t.Fatalf("reconcile invalid AgentRunControl: %v", err)
	}
	updated := &controlv1alpha1.AgentRunControl{}
	if err := c.Get(ctx, client.ObjectKey{Name: control.Name}, updated); err != nil {
		t.Fatalf("get AgentRunControl: %v", err)
	}
	ready := apimeta.FindStatusCondition(updated.Status.Conditions, agentRunControlReady)
	if updated.Status.Phase != controlv1alpha1.AgentRunControlPhaseBlocked || ready == nil || ready.Reason != "PauseReasonRequired" {
		t.Fatalf("control status = %#v condition=%#v, want Blocked/PauseReasonRequired", updated.Status, ready)
	}
	pause, err := activeAgentRunPauseForApplication(ctx, c, applicationName, time.Now())
	if err != nil {
		t.Fatalf("resolve invalid pause: %v", err)
	}
	if pause != nil {
		t.Fatalf("invalid control paused launches: %#v", pause)
	}
}

func TestTerminalAgentRunSpecEditDoesNotRerun(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := newAgentControlTestScheme(t)
	run := testPendingApplicationRun("completed", "health")
	run.Generation = 2
	run.Spec.Prompt = "edited after completion"
	run.Status = controlv1alpha1.AgentRunStatus{
		ObservedGeneration: 1,
		Phase:              controlv1alpha1.AgentRunPhaseSucceeded,
		PromptHash:         "original",
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(run).WithStatusSubresource(run).Build()
	reconciler := &AgentRunReconciler{Client: c, Scheme: scheme}
	if _, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKey{Namespace: run.Namespace, Name: run.Name}}); err != nil {
		t.Fatalf("reconcile terminal AgentRun: %v", err)
	}
	jobs := &batchv1.JobList{}
	if err := c.List(ctx, jobs); err != nil {
		t.Fatalf("list Jobs: %v", err)
	}
	if len(jobs.Items) != 0 {
		t.Fatalf("terminal spec edit created %d Jobs", len(jobs.Items))
	}
	updated := &controlv1alpha1.AgentRun{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: run.Namespace, Name: run.Name}, updated); err != nil {
		t.Fatalf("get AgentRun: %v", err)
	}
	if updated.Status.ObservedGeneration != 1 || updated.Status.Phase != controlv1alpha1.AgentRunPhaseSucceeded || updated.Status.PromptHash != "original" {
		t.Fatalf("terminal status mutated: %#v", updated.Status)
	}
}

func TestAgentRunControlStatusCountsResolvedSubjects(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := newAgentControlTestScheme(t)
	control := pausedAgentRunControl("hazy-trade", "hazy-trade", nil)
	control.Generation = 1
	schedule := &controlv1alpha1.AgentSchedule{
		ObjectMeta: metav1.ObjectMeta{Name: "health", Namespace: "hazy-trade"},
		Spec:       controlv1alpha1.AgentScheduleSpec{ApplicationRef: &controlv1alpha1.ApplicationReferenceSpec{Name: "hazy-trade"}},
	}
	pending := testPendingApplicationRun("pending", "health")
	prepared := testPendingApplicationRun("prepared", "health")
	prepared.Status.PlannedJobRef = &controlv1alpha1.NamespacedObjectReference{Name: "prepared-job", Namespace: prepared.Namespace}
	active := testPendingApplicationRun("active", "health")
	active.Status.JobRef = &controlv1alpha1.NamespacedObjectReference{Name: "active-job", Namespace: active.Namespace}
	active.Status.Phase = controlv1alpha1.AgentRunPhaseRunning
	attempted := testPendingApplicationRun("attempted", "health")
	attempted.Status.PlannedJobRef = &controlv1alpha1.NamespacedObjectReference{Name: "attempted-job", Namespace: attempted.Namespace}
	attemptedAt := metav1.Now()
	attempted.Status.JobCreateAttemptedAt = &attemptedAt
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(control, schedule, pending, prepared, active, attempted).WithStatusSubresource(control).Build()
	reconciler := &AgentRunControlReconciler{Client: c, Scheme: scheme}
	if _, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKey{Name: control.Name}}); err != nil {
		t.Fatalf("reconcile AgentRunControl: %v", err)
	}
	updated := &controlv1alpha1.AgentRunControl{}
	if err := c.Get(ctx, client.ObjectKey{Name: control.Name}, updated); err != nil {
		t.Fatalf("get AgentRunControl: %v", err)
	}
	if updated.Status.Phase != controlv1alpha1.AgentRunControlPhasePaused || updated.Status.AffectedScheduleCount != 1 || updated.Status.PendingRunCount != 2 || updated.Status.ActiveRunCount != 2 {
		t.Fatalf("unexpected status: %#v", updated.Status)
	}
}

func newAgentControlTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := controlv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add control scheme: %v", err)
	}
	if err := batchv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add batch scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	return scheme
}

func pausedAgentRunControl(name, application string, expiresAt *metav1.Time) *controlv1alpha1.AgentRunControl {
	return &controlv1alpha1.AgentRunControl{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: controlv1alpha1.AgentRunControlSpec{
			ApplicationRef: controlv1alpha1.ApplicationReferenceSpec{Name: application},
			LaunchPolicy:   controlv1alpha1.AgentRunControlLaunchPolicyPaused,
			Reason:         "operator hold",
			ExpiresAt:      expiresAt,
		},
	}
}

func testPendingApplicationRun(name, scheduleName string) *controlv1alpha1.AgentRun {
	return &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "hazy-trade", Generation: 1},
		Spec: controlv1alpha1.AgentRunSpec{
			Purpose: controlv1alpha1.AgentRunPurposeScheduledHealthCheck,
			SourceRef: controlv1alpha1.AgentRunSourceRef{
				Kind: "AgentSchedule",
				Name: scheduleName,
			},
			ScheduleRef: &controlv1alpha1.NamespacedObjectReference{Name: scheduleName, Namespace: "hazy-trade"},
			Scope: controlv1alpha1.AgentRunScopeSpec{
				ApplicationRef: &controlv1alpha1.ApplicationReferenceSpec{Name: "hazy-trade"},
			},
			Harness: controlv1alpha1.AgentRunHarnessSpec{Intent: controlv1alpha1.AgentRunIntentObserve},
		},
	}
}
