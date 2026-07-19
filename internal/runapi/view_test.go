package runapi

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	agentsv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

func TestAgentRunViewIncludesSanitizedResolvedComposition(t *testing.T) {
	t.Parallel()

	run := &agentsv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{Name: "review", Namespace: "agents"},
		Status: agentsv1alpha1.AgentRunStatus{ResolvedComposition: &agentsv1alpha1.AgentRunResolvedCompositionStatus{
			HarnessProfileRef: &agentsv1alpha1.AgentRunResolvedObjectReferenceStatus{Name: "codex-standard", UID: "harness-uid", Generation: 2, Digest: "sha256:harness"},
			SkillSetRefs:      []agentsv1alpha1.AgentRunResolvedObjectReferenceStatus{{Name: "repository-review", UID: "skills-uid", Generation: 4, Digest: "sha256:skills"}},
			ToolSetRefs:       []agentsv1alpha1.AgentRunResolvedObjectReferenceStatus{{Name: "knowledge-tools", UID: "tools-uid", Generation: 3, Digest: "sha256:tools"}},
			EffectiveDigest:   "sha256:effective",
			PayloadDigest:     "sha256:payload",
			Scope: &agentsv1alpha1.AgentRunResolvedScopeStatus{
				Application:       "payments",
				ApplicationTarget: "production",
			},
		}},
	}
	view := NewAgentRunView(run, false)
	if view.ResolvedComposition == nil || view.ResolvedComposition.HarnessProfileRef == nil {
		t.Fatalf("resolved composition was omitted: %#v", view)
	}
	if got := view.ResolvedComposition.SkillSetRefs[0].Name; got != "repository-review" {
		t.Fatalf("skill set name = %q", got)
	}
	if got := view.ResolvedComposition.ToolSetRefs[0].Name; got != "knowledge-tools" {
		t.Fatalf("tool set name = %q", got)
	}
	if view.Application != "payments" || view.ApplicationTarget != "production" {
		t.Fatalf("resolved application scope omitted: %#v", view)
	}
	view.ResolvedComposition.SkillSetRefs[0].Name = "mutated"
	view.ResolvedComposition.ToolSetRefs[0].Name = "mutated"
	if got := run.Status.ResolvedComposition.SkillSetRefs[0].Name; got != "repository-review" {
		t.Fatalf("view aliased source status: %q", got)
	}
	if got := run.Status.ResolvedComposition.ToolSetRefs[0].Name; got != "knowledge-tools" {
		t.Fatalf("view aliased source tool status: %q", got)
	}
}
