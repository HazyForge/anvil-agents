package controller

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

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
