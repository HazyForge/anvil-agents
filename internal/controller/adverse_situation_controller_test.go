package controller

import (
	"context"
	"strings"
	"testing"
	"time"

	apiequality "k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	controlv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

func TestDefaultAdverseSituationBuffersWithoutAgentRunResponder(t *testing.T) {
	t.Parallel()

	situation := defaultAdverseSituation("operations", "adverse-default")
	if got, want := situation.Spec.GroupKey, adverseSituationDefaultGroupKey; got != want {
		t.Fatalf("group key = %q, want %q", got, want)
	}
	if situation.Spec.Responders.AgentRun != nil {
		t.Fatalf("default adverse situation should not configure an AgentRun responder: %#v", situation.Spec.Responders.AgentRun)
	}
	if adverseSituationAgentRunEnabled(situation) {
		t.Fatalf("default adverse situation should not create an AgentRun responder")
	}
}

func TestAdverseSituationAgentRunResponderRequiresExplicitEnable(t *testing.T) {
	t.Parallel()

	situation := &controlv1alpha1.AdverseSituation{}
	if adverseSituationAgentRunEnabled(situation) {
		t.Fatalf("missing responder should be disabled")
	}

	situation.Spec.Responders.AgentRun = &controlv1alpha1.AdverseSituationAgentRunResponderSpec{}
	if adverseSituationAgentRunEnabled(situation) {
		t.Fatalf("nil enabled should be disabled")
	}

	enabled := true
	situation.Spec.Responders.AgentRun.Enabled = &enabled
	if !adverseSituationAgentRunEnabled(situation) {
		t.Fatalf("explicit enabled responder should be enabled")
	}

	enabled = false
	if adverseSituationAgentRunEnabled(situation) {
		t.Fatalf("explicit disabled responder should be disabled")
	}
}

func TestAdverseSituationAgentRunCopiesDocsAndIssueTracking(t *testing.T) {
	t.Parallel()

	now := metav1.Now()
	situation := &controlv1alpha1.AdverseSituation{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "checkout-health",
			Namespace:       "store",
			UID:             types.UID("checkout-health-uid"),
			ResourceVersion: "42",
		},
		Spec: controlv1alpha1.AdverseSituationSpec{
			Responders: controlv1alpha1.AdverseSituationRespondersSpec{
				AgentRun: &controlv1alpha1.AdverseSituationAgentRunResponderSpec{
					ProfileRef: &controlv1alpha1.NamespacedObjectReference{
						Name: "checkout-release-responder",
					},
					HarnessProfileRef: &controlv1alpha1.NamespacedObjectReference{Name: "codex-standard"},
					SkillSets: &controlv1alpha1.AgentSkillCompositionSpec{
						Refs: []controlv1alpha1.NamespacedObjectReference{{Name: "release-response"}},
					},
					ToolSets: &controlv1alpha1.AgentToolCompositionSpec{
						Refs: []controlv1alpha1.NamespacedObjectReference{{Name: "release-tools"}},
					},
					Prompt: "Diagnose the adverse stream and repair or propose a release-gate fix.",
					Scope: controlv1alpha1.AgentRunScopeSpec{
						Summary:    "Checkout production",
						Namespaces: []string{"store"},
						ApplicationRef: &controlv1alpha1.ApplicationReferenceSpec{
							Name: "checkout",
						},
						ApplicationTargetRef: &controlv1alpha1.ApplicationTargetReferenceSpec{
							Name: "checkout-prod",
						},
					},
					Docs: &controlv1alpha1.AgentRunDocsSpec{
						Policy:       controlv1alpha1.AgentRunDocsPolicyRequired,
						Paths:        []string{"docs/agent-run.md"},
						RuntimePaths: []string{"api/control/v1alpha1/adverse_situation_types.go"},
					},
					IssueTracking: &controlv1alpha1.AgentRunIssueTrackingSpec{
						Provider:     controlv1alpha1.AgentRunIssueTrackingProviderGitHub,
						Repository:   "example/checkout",
						UpdatePolicy: controlv1alpha1.AgentRunIssueUpdatePolicyComment,
						SearchQuery:  `repo:example/checkout is:issue is:open "AdverseSituation"`,
					},
				},
			},
		},
		Status: controlv1alpha1.AdverseSituationStatus{
			Phase:              controlv1alpha1.AdverseSituationPhaseOpen,
			ObservedGeneration: 3,
			Events: []controlv1alpha1.AdverseSituationEvent{{
				ID:         "source-reason",
				Reason:     "Failed",
				Message:    "source failed",
				LastSeenAt: &now,
			}},
		},
	}

	run := adverseSituationAgentRunFor(situation, "agrun-checkout-health")
	if run.Spec.ProfileRef == nil || run.Spec.ProfileRef.Name != "checkout-release-responder" {
		t.Fatalf("profile ref = %#v, want checkout-release-responder", run.Spec.ProfileRef)
	}
	if run.Spec.HarnessProfileRef == nil || run.Spec.HarnessProfileRef.Name != "codex-standard" {
		t.Fatalf("harness profile ref = %#v, want codex-standard", run.Spec.HarnessProfileRef)
	}
	if run.Spec.Harness.Backend.Kind != "" {
		t.Fatalf("inline backend kind = %q, want selected harness profile to remain authoritative", run.Spec.Harness.Backend.Kind)
	}
	if run.Spec.SkillSets == nil || len(run.Spec.SkillSets.Refs) != 1 || run.Spec.SkillSets.Refs[0].Name != "release-response" {
		t.Fatalf("skill sets = %#v, want release-response", run.Spec.SkillSets)
	}
	if run.Spec.ToolSets == nil || len(run.Spec.ToolSets.Refs) != 1 || run.Spec.ToolSets.Refs[0].Name != "release-tools" {
		t.Fatalf("tool sets = %#v, want release-tools", run.Spec.ToolSets)
	}
	if got, want := run.Spec.Prompt, "Diagnose the adverse stream and repair or propose a release-gate fix."; got != want {
		t.Fatalf("prompt = %q, want %q", got, want)
	}
	if run.Spec.Docs == nil {
		t.Fatalf("docs policy is nil")
	}
	if got, want := run.Spec.Docs.Policy, controlv1alpha1.AgentRunDocsPolicyRequired; got != want {
		t.Fatalf("docs policy = %q, want %q", got, want)
	}
	if got, want := run.Spec.Docs.Paths[0], "docs/agent-run.md"; got != want {
		t.Fatalf("docs path = %q, want %q", got, want)
	}
	if run.Spec.IssueTracking == nil {
		t.Fatalf("issue tracking is nil")
	}
	if got, want := run.Spec.IssueTracking.Provider, controlv1alpha1.AgentRunIssueTrackingProviderGitHub; got != want {
		t.Fatalf("issue provider = %q, want %q", got, want)
	}
	if got, want := run.Spec.IssueTracking.UpdatePolicy, controlv1alpha1.AgentRunIssueUpdatePolicyComment; got != want {
		t.Fatalf("issue update policy = %q, want %q", got, want)
	}
	if got, want := run.Spec.IssueTracking.SearchQuery, `repo:example/checkout is:issue is:open "AdverseSituation"`; got != want {
		t.Fatalf("issue search query = %q, want %q", got, want)
	}
	if got, want := run.Spec.Scope.Summary, "Checkout production"; got != want {
		t.Fatalf("scope summary = %q, want %q", got, want)
	}
	if run.Spec.Scope.ApplicationRef == nil || run.Spec.Scope.ApplicationRef.Name != "checkout" {
		t.Fatalf("scope application ref = %#v, want checkout", run.Spec.Scope.ApplicationRef)
	}
	if run.Spec.Scope.ApplicationTargetRef == nil || run.Spec.Scope.ApplicationTargetRef.Name != "checkout-prod" {
		t.Fatalf("scope target ref = %#v, want checkout-prod", run.Spec.Scope.ApplicationTargetRef)
	}
	run.Spec.SkillSets.Refs[0].Name = "mutated-skills"
	run.Spec.ToolSets.Refs[0].Name = "mutated-tools"
	responder := situation.Spec.Responders.AgentRun
	if responder.SkillSets.Refs[0].Name != "release-response" || responder.ToolSets.Refs[0].Name != "release-tools" {
		t.Fatalf("created AgentRun aliases responder composition: skills=%#v tools=%#v", responder.SkillSets, responder.ToolSets)
	}
}

func TestAdverseSituationAgentRunRejectsRetainedRunFromRecreatedSituation(t *testing.T) {
	t.Parallel()

	situation := &controlv1alpha1.AdverseSituation{ObjectMeta: metav1.ObjectMeta{
		Name: "checkout-health", Namespace: "store", UID: types.UID("current-situation-uid"),
	}}
	retained := adverseSituationAgentRunFor(situation, "retained-run")
	retained.Spec.SourceUID = "deleted-situation-uid"
	if adverseSituationAgentRunMatches(retained, situation) {
		t.Fatalf("run from deleted situation UID must not be adopted")
	}
}

func TestAdverseSituationAgentRunUnestablishedMatchRejectsTriggerDrift(t *testing.T) {
	t.Parallel()

	detectedAt := metav1.Now()
	situation := &controlv1alpha1.AdverseSituation{
		ObjectMeta: metav1.ObjectMeta{Name: "checkout-health", Namespace: "store", UID: types.UID("situation-uid"), ResourceVersion: "12", Generation: 3},
		Status: controlv1alpha1.AdverseSituationStatus{
			Phase:              controlv1alpha1.AdverseSituationPhaseOpen,
			ObservedGeneration: 3,
			Events:             []controlv1alpha1.AdverseSituationEvent{{LastSeenAt: &detectedAt}},
		},
	}
	run := adverseSituationAgentRunFor(situation, "responder")
	otherDetectedAt := metav1.NewTime(detectedAt.Add(time.Second))
	run.Spec.Trigger.DetectedAt = &otherDetectedAt
	if adverseSituationAgentRunMatches(run, situation) {
		t.Fatal("unestablished responder with detectedAt drift was accepted")
	}
	run = adverseSituationAgentRunFor(situation, "responder")
	run.Spec.Trigger.Reason = "InjectedReason"
	if adverseSituationAgentRunMatches(run, situation) {
		t.Fatal("adverse responder with injected trigger reason was accepted")
	}
}

func TestAdverseSituationReconcileReusesEstablishedResponderAfterStatusDrift(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := controlv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add control scheme: %v", err)
	}
	enabled := true
	now := metav1.Now()
	situation := &controlv1alpha1.AdverseSituation{
		ObjectMeta: metav1.ObjectMeta{Name: "checkout-health", Namespace: "store", UID: types.UID("situation-uid"), Generation: 1},
		Spec: controlv1alpha1.AdverseSituationSpec{Responders: controlv1alpha1.AdverseSituationRespondersSpec{AgentRun: &controlv1alpha1.AdverseSituationAgentRunResponderSpec{
			Enabled: &enabled,
			Harness: controlv1alpha1.AgentRunHarnessSpec{Intent: controlv1alpha1.AgentRunIntentObserve},
		}}},
		Status: controlv1alpha1.AdverseSituationStatus{
			ObservedGeneration: 1,
			Phase:              controlv1alpha1.AdverseSituationPhaseOpen,
			Sequence:           1,
			Events:             []controlv1alpha1.AdverseSituationEvent{{ID: "checkout-failed", LastSeenAt: &now}},
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(situation).
		WithStatusSubresource(&controlv1alpha1.AdverseSituation{}, &controlv1alpha1.AgentRun{}).
		Build()
	storedSituation := &controlv1alpha1.AdverseSituation{}
	if err := c.Get(ctx, client.ObjectKeyFromObject(situation), storedSituation); err != nil {
		t.Fatalf("get stored situation: %v", err)
	}
	name := agentRunChildName("agrun", situation.Name, "1", shortHash(string(situation.UID)))
	run := adverseSituationAgentRunFor(storedSituation, name)
	run.UID = types.UID("responder-uid")
	wantSpec := run.Spec.DeepCopy()
	if err := c.Create(ctx, run); err != nil {
		t.Fatalf("create exact responder fixture: %v", err)
	}
	reconciler := &AdverseSituationReconciler{Client: c, Scheme: scheme}
	request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(situation)}

	if _, err := reconciler.Reconcile(ctx, request); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	updated := &controlv1alpha1.AdverseSituation{}
	if err := c.Get(ctx, client.ObjectKeyFromObject(situation), updated); err != nil {
		t.Fatalf("get situation after first reconcile: %v", err)
	}
	if updated.Status.Phase != controlv1alpha1.AdverseSituationPhaseQuieting {
		t.Fatalf("phase after first reconcile = %q, want Quieting", updated.Status.Phase)
	}
	if updated.Status.ActiveResponderRef == nil || updated.Status.ActiveResponderRef.Name != run.Name || updated.Status.ActiveResponderUID != string(run.UID) || updated.Status.ActiveResponderDigest == "" {
		t.Fatalf("responder receipt after first reconcile = %#v", updated.Status)
	}

	if _, err := reconciler.Reconcile(ctx, request); err != nil {
		t.Fatalf("second reconcile after parent status drift: %v", err)
	}
	runs := &controlv1alpha1.AgentRunList{}
	if err := c.List(ctx, runs, client.InNamespace(situation.Namespace)); err != nil {
		t.Fatalf("list responder AgentRuns: %v", err)
	}
	if len(runs.Items) != 1 || runs.Items[0].UID != run.UID || !apiequality.Semantic.DeepEqual(&runs.Items[0].Spec, wantSpec) {
		t.Fatalf("responder after second reconcile = %#v, want same immutable run", runs.Items)
	}
}

func TestAdverseSituationRejectsEstablishedResponderReplacementUID(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := controlv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add control scheme: %v", err)
	}
	enabled := true
	situation := &controlv1alpha1.AdverseSituation{
		ObjectMeta: metav1.ObjectMeta{Name: "checkout-health", Namespace: "store", UID: types.UID("situation-uid")},
		Spec: controlv1alpha1.AdverseSituationSpec{Responders: controlv1alpha1.AdverseSituationRespondersSpec{
			AgentRun: &controlv1alpha1.AdverseSituationAgentRunResponderSpec{Enabled: &enabled},
		}},
		Status: controlv1alpha1.AdverseSituationStatus{
			Sequence:           1,
			ActiveResponderRef: &controlv1alpha1.NamespacedObjectReference{Name: "responder", Namespace: "store"},
			ActiveResponderUID: "original-responder-uid",
		},
	}
	replacement := adverseSituationAgentRunFor(situation, "responder")
	replacement.UID = types.UID("replacement-responder-uid")
	situation.Status.ActiveResponderDigest = digestJSON(replacement.Spec)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(replacement).Build()
	reconciler := &AdverseSituationReconciler{Client: c, Scheme: scheme}
	status := situation.Status.DeepCopy()

	_, err := reconciler.ensureAdverseSituationAgentRun(ctx, situation, status)
	if err == nil || !strings.Contains(err.Error(), "does not match the established adverse responder UID") {
		t.Fatalf("replacement responder error = %v, want UID rejection", err)
	}
}

func TestAdverseSituationLegacyResponderMigrationRequiresExactSnapshot(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := controlv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add control scheme: %v", err)
	}
	enabled := true
	situation := &controlv1alpha1.AdverseSituation{
		ObjectMeta: metav1.ObjectMeta{Name: "checkout-health", Namespace: "store", UID: types.UID("situation-uid")},
		Spec: controlv1alpha1.AdverseSituationSpec{Responders: controlv1alpha1.AdverseSituationRespondersSpec{
			AgentRun: &controlv1alpha1.AdverseSituationAgentRunResponderSpec{Enabled: &enabled},
		}},
		Status: controlv1alpha1.AdverseSituationStatus{
			Sequence:           1,
			ActiveResponderRef: &controlv1alpha1.NamespacedObjectReference{Name: "responder", Namespace: "store"},
		},
	}

	t.Run("exact", func(t *testing.T) {
		run := adverseSituationAgentRunFor(situation, "responder")
		run.UID = types.UID("exact-responder-uid")
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(run).Build()
		status := situation.Status.DeepCopy()
		if _, err := (&AdverseSituationReconciler{Client: c, Scheme: scheme}).ensureAdverseSituationAgentRun(ctx, situation, status); err != nil {
			t.Fatalf("migrate exact legacy responder: %v", err)
		}
		if status.ActiveResponderUID != string(run.UID) || status.ActiveResponderDigest == "" {
			t.Fatalf("migrated receipt = %#v", status)
		}
	})

	t.Run("authority drift", func(t *testing.T) {
		run := adverseSituationAgentRunFor(situation, "responder")
		run.UID = types.UID("drifted-responder-uid")
		run.Spec.Prompt = "injected authority-bearing prompt"
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(run).Build()
		status := situation.Status.DeepCopy()
		_, err := (&AdverseSituationReconciler{Client: c, Scheme: scheme}).ensureAdverseSituationAgentRun(ctx, situation, status)
		if err == nil || !strings.Contains(err.Error(), "no receipt and no longer exactly matches") {
			t.Fatalf("legacy responder drift error = %v, want exact-snapshot rejection", err)
		}
	})
}

func TestAdverseSituationNewSequenceClearsResponderReceipt(t *testing.T) {
	t.Parallel()

	status := &controlv1alpha1.AdverseSituationStatus{
		Phase:                 controlv1alpha1.AdverseSituationPhaseResolved,
		Sequence:              4,
		ActiveResponderRef:    &controlv1alpha1.NamespacedObjectReference{Name: "old-responder", Namespace: "store"},
		ActiveResponderUID:    "old-responder-uid",
		ActiveResponderDigest: "sha256:old-responder-digest",
	}
	if !adverseSituationPrepareSequence(status) {
		t.Fatal("prepare new sequence unexpectedly blocked")
	}
	if status.Sequence != 5 || status.ActiveResponderRef != nil || status.ActiveResponderUID != "" || status.ActiveResponderDigest != "" {
		t.Fatalf("new sequence retained responder receipt: %#v", status)
	}
}

func TestAdverseSituationStatusBoundsEventsAndUTF8Text(t *testing.T) {
	t.Parallel()

	if got := adverseSituationMaxEvents(controlv1alpha1.AdverseSituationBufferSpec{MaxEvents: 10_000}); got != adverseSituationHardMaxEvents {
		t.Fatalf("effective max events = %d, want %d", got, adverseSituationHardMaxEvents)
	}
	message := adverseSituationLimitString("abc🙂def", 6)
	if message != "abc" {
		t.Fatalf("bounded UTF-8 message = %q, want %q", message, "abc")
	}
}

func TestPullEventCannotEvictUnacknowledgedSignalReceipt(t *testing.T) {
	t.Parallel()

	now := metav1.Now()
	status := controlv1alpha1.AdverseSituationStatus{
		Phase:      controlv1alpha1.AdverseSituationPhaseOpen,
		Sequence:   1,
		EventCount: 1,
		Events: []controlv1alpha1.AdverseSituationEvent{{
			ID:          "signal-event",
			ReportIDs:   []string{"pending-signal-receipt"},
			Count:       1,
			FirstSeenAt: &now,
			LastSeenAt:  &now,
		}},
	}
	source := &unstructured.Unstructured{}
	source.SetGroupVersionKind(schema.GroupVersionKind{Group: "delivery.example.io", Version: "v1", Kind: "Release"})
	source.SetNamespace("store")
	source.SetName("checkout")
	source.SetUID(types.UID("release-uid"))
	trigger := controlv1alpha1.AgentRunTriggerSnapshot{Phase: "Failed", Reason: "DeploymentFailed"}
	buffer := controlv1alpha1.AdverseSituationBufferSpec{MaxEvents: 1}

	if adverseSituationRecordEvent(source, trigger, buffer, &status) {
		t.Fatalf("pull event should backpressure before evicting a signal receipt")
	}
	if len(status.Events) != 1 || status.Events[0].ID != "signal-event" || status.EventCount != 1 {
		t.Fatalf("pull event evicted receipt-bearing event: %#v", status)
	}

	status.Events[0].ReportIDs = nil
	if !adverseSituationRecordEvent(source, trigger, buffer, &status) {
		t.Fatalf("pull event should record after signal receipt cleanup")
	}
	if len(status.Events) != 1 || status.Events[0].ID == "signal-event" || status.EventCount != 2 {
		t.Fatalf("pull event was not recorded after cleanup: %#v", status)
	}
}
