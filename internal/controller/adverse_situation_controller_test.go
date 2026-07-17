package controller

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	controlv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

func TestDefaultAdverseSituationBuffersWithoutAgentRunResponder(t *testing.T) {
	t.Parallel()

	situation := defaultAdverseSituation("anvilhub", "adverse-default")
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
			Name:            "adverse-anvil",
			Namespace:       "anvil",
			UID:             types.UID("adverse-anvil-uid"),
			ResourceVersion: "42",
		},
		Spec: controlv1alpha1.AdverseSituationSpec{
			Responders: controlv1alpha1.AdverseSituationRespondersSpec{
				AgentRun: &controlv1alpha1.AdverseSituationAgentRunResponderSpec{
					ProfileRef: &controlv1alpha1.NamespacedObjectReference{
						Name: "hazy-trade-release-gate-responder",
					},
					Prompt: "Diagnose the adverse stream and repair or propose a release-gate fix.",
					Scope: controlv1alpha1.AgentRunScopeSpec{
						Summary:    "Hazy Trade production",
						Namespaces: []string{"hazy-trade"},
						ApplicationRef: &controlv1alpha1.ApplicationReferenceSpec{
							Name: "hazy-trade",
						},
						ApplicationTargetRef: &controlv1alpha1.ApplicationTargetReferenceSpec{
							Name: "hazy-trade-prod",
						},
					},
					Docs: &controlv1alpha1.AgentRunDocsSpec{
						Policy:       controlv1alpha1.AgentRunDocsPolicyRequired,
						Paths:        []string{"docs/agent-run.md"},
						RuntimePaths: []string{"api/control/v1alpha1/adverse_situation_types.go"},
					},
					IssueTracking: &controlv1alpha1.AgentRunIssueTrackingSpec{
						Provider:     controlv1alpha1.AgentRunIssueTrackingProviderGitHub,
						Repository:   "HazyForge/anvil-primaris",
						UpdatePolicy: controlv1alpha1.AgentRunIssueUpdatePolicyComment,
						SearchQuery:  `repo:HazyForge/anvil-primaris is:issue is:open "AdverseSituation"`,
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

	run := adverseSituationAgentRunFor(situation, "agrun-adverse-anvil")
	if run.Spec.ProfileRef == nil || run.Spec.ProfileRef.Name != "hazy-trade-release-gate-responder" {
		t.Fatalf("profile ref = %#v, want hazy-trade-release-gate-responder", run.Spec.ProfileRef)
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
	if got, want := run.Spec.IssueTracking.SearchQuery, `repo:HazyForge/anvil-primaris is:issue is:open "AdverseSituation"`; got != want {
		t.Fatalf("issue search query = %q, want %q", got, want)
	}
	if got, want := run.Spec.Scope.Summary, "Hazy Trade production"; got != want {
		t.Fatalf("scope summary = %q, want %q", got, want)
	}
	if run.Spec.Scope.ApplicationRef == nil || run.Spec.Scope.ApplicationRef.Name != "hazy-trade" {
		t.Fatalf("scope application ref = %#v, want hazy-trade", run.Spec.Scope.ApplicationRef)
	}
	if run.Spec.Scope.ApplicationTargetRef == nil || run.Spec.Scope.ApplicationTargetRef.Name != "hazy-trade-prod" {
		t.Fatalf("scope target ref = %#v, want hazy-trade-prod", run.Spec.Scope.ApplicationTargetRef)
	}
}
