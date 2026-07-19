package v1alpha1

import (
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
)

func TestCompositionResourcesAreRegistered(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	if err := AddToScheme(scheme); err != nil {
		t.Fatalf("add agent scheme: %v", err)
	}
	for _, object := range []runtime.Object{
		&AgentHarnessProfile{}, &AgentHarnessProfileList{},
		&AgentSkillSet{}, &AgentSkillSetList{},
		&AgentToolSet{}, &AgentToolSetList{},
		&AdverseSignal{}, &AdverseSignalList{},
	} {
		gvks, _, err := scheme.ObjectKinds(object)
		if err != nil {
			t.Fatalf("resolve kind for %T: %v", object, err)
		}
		if len(gvks) != 1 || gvks[0].GroupVersion() != GroupVersion {
			t.Fatalf("kind for %T = %#v, want one %s kind", object, gvks, GroupVersion)
		}
	}
}

func TestCompositionDeepCopyDoesNotAliasOverrides(t *testing.T) {
	t.Parallel()

	run := &AgentRun{Spec: AgentRunSpec{
		HarnessProfileRef: &NamespacedObjectReference{Name: "codex-standard"},
		SkillSets: &AgentSkillCompositionSpec{
			Refs: []NamespacedObjectReference{{Name: "repository-review"}},
			Overrides: []AgentSkillOverrideSpec{{
				Name:       "evidence-backed-review",
				Operation:  AgentSkillOverrideAugment,
				Paths:      []string{"docs/architecture.md"},
				SourceRefs: []AgentRunSkillSourceRef{{GitHub: &AgentRunGitHubSkillSourceSpec{Repository: "example/skills", Path: "review.md"}}},
			}},
		},
		ToolSets: &AgentToolCompositionSpec{
			Refs: []NamespacedObjectReference{{Name: "knowledge-tools"}},
		},
	}}
	copy := run.DeepCopy()
	copy.Spec.HarnessProfileRef.Name = "pi-standard"
	copy.Spec.SkillSets.Refs[0].Name = "incident-review"
	copy.Spec.SkillSets.Overrides[0].Paths[0] = "docs/security.md"
	copy.Spec.SkillSets.Overrides[0].SourceRefs[0].GitHub.Path = "security.md"
	copy.Spec.ToolSets.Refs[0].Name = "issue-tools"

	if got := run.Spec.HarnessProfileRef.Name; got != "codex-standard" {
		t.Fatalf("source harness ref mutated to %q", got)
	}
	if got := run.Spec.SkillSets.Refs[0].Name; got != "repository-review" {
		t.Fatalf("source skill set ref mutated to %q", got)
	}
	if got := run.Spec.SkillSets.Overrides[0].Paths[0]; got != "docs/architecture.md" {
		t.Fatalf("source override path mutated to %q", got)
	}
	if got := run.Spec.SkillSets.Overrides[0].SourceRefs[0].GitHub.Path; got != "review.md" {
		t.Fatalf("source override ref mutated to %q", got)
	}
	if got := run.Spec.ToolSets.Refs[0].Name; got != "knowledge-tools" {
		t.Fatalf("source tool set ref mutated to %q", got)
	}
}
