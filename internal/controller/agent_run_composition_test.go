package controller

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	controlv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

func TestAgentRunCompositionSwapsHarnessAndAppliesSkillLayers(t *testing.T) {
	t.Parallel()

	profile := &controlv1alpha1.AgentRunProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "repository-reviewer", Namespace: "agents", UID: "profile-uid", Generation: 3},
		Spec: controlv1alpha1.AgentRunProfileSpec{
			Scope: controlv1alpha1.AgentRunScopeSpec{
				ApplicationRef:       &controlv1alpha1.ApplicationReferenceSpec{Name: "payments"},
				ApplicationTargetRef: &controlv1alpha1.ApplicationTargetReferenceSpec{Name: "production"},
			},
			HarnessProfileRef: &controlv1alpha1.NamespacedObjectReference{Name: "codex-standard"},
			SkillSets: &controlv1alpha1.AgentSkillCompositionSpec{
				Refs: []controlv1alpha1.NamespacedObjectReference{{Name: "repository-review"}},
				Overrides: []controlv1alpha1.AgentSkillOverrideSpec{{
					Name:      "evidence-review",
					Operation: controlv1alpha1.AgentSkillOverrideAugment,
					Content:   "Apply the profile review policy.",
				}},
			},
			Harness: controlv1alpha1.AgentRunHarnessSpec{
				Intent:       controlv1alpha1.AgentRunIntentObserve,
				SystemPrompt: "Role policy remains independent of the selected runtime.",
				Execution: controlv1alpha1.AgentRunHarnessExecutionSpec{
					EnvSecretRefs: []controlv1alpha1.NamespacedObjectReference{{Name: "legacy-codex-only"}},
				},
			},
		},
	}
	codexHarness := testAgentHarnessProfile("codex-standard", controlv1alpha1.AgentRunHarnessBackendCodex, "codex-credentials")
	piHarness := testAgentHarnessProfile("pi-standard", controlv1alpha1.AgentRunHarnessBackendPiAgent, "pi-credentials")
	baseSet := &controlv1alpha1.AgentSkillSet{
		ObjectMeta: metav1.ObjectMeta{Name: "repository-review", Namespace: "agents", UID: "repository-review-uid", Generation: 5},
		Spec: controlv1alpha1.AgentSkillSetSpec{
			Skills:    []controlv1alpha1.AgentRunSkillInjectionSpec{{Name: "evidence-review", Content: "Review current repository evidence."}},
			Tools:     []controlv1alpha1.AgentRunToolSpec{{Name: "legacy-repository-status", VerifyCommand: []string{"git", "status", "--short"}}},
			Subagents: []controlv1alpha1.AgentRunSubagentSpec{{Name: "correctness-reviewer", When: "Shared behavior changes."}},
		},
	}
	knowledgeTools := &controlv1alpha1.AgentToolSet{
		ObjectMeta: metav1.ObjectMeta{Name: "knowledge-tools", Namespace: "agents", UID: "knowledge-tools-uid", Generation: 4},
		Spec: controlv1alpha1.AgentToolSetSpec{Tools: []controlv1alpha1.AgentRunToolSpec{{
			Name: "knowledge-search", VerifyCommand: []string{"knowledge-search", "--help"},
		}}},
	}
	profile.Spec.ToolSets = &controlv1alpha1.AgentToolCompositionSpec{
		Refs: []controlv1alpha1.NamespacedObjectReference{{Name: knowledgeTools.Name}},
	}
	runSet := &controlv1alpha1.AgentSkillSet{
		ObjectMeta: metav1.ObjectMeta{Name: "incident-review", Namespace: "agents", UID: "incident-review-uid", Generation: 2},
		Spec: controlv1alpha1.AgentSkillSetSpec{
			Skills: []controlv1alpha1.AgentRunSkillInjectionSpec{
				{Name: "evidence-review", Content: "Review the incident evidence."},
				{Name: "timeline", Content: "Build an incident timeline."},
			},
			Subagents: append([]controlv1alpha1.AgentRunSubagentSpec(nil), baseSet.Spec.Subagents...),
		},
	}
	run := &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{Name: "incident-42", Namespace: "agents"},
		Spec: controlv1alpha1.AgentRunSpec{
			ProfileRef:        &controlv1alpha1.NamespacedObjectReference{Name: profile.Name},
			HarnessProfileRef: &controlv1alpha1.NamespacedObjectReference{Name: piHarness.Name},
			SkillSets: &controlv1alpha1.AgentSkillCompositionSpec{
				Refs: []controlv1alpha1.NamespacedObjectReference{{Name: runSet.Name}},
				Overrides: []controlv1alpha1.AgentSkillOverrideSpec{
					{Name: "evidence-review", Operation: controlv1alpha1.AgentSkillOverrideAugment, Content: "Only incident 42."},
					{Name: "timeline", Operation: controlv1alpha1.AgentSkillOverrideDisable},
					{Name: "final-check", Operation: controlv1alpha1.AgentSkillOverrideAdd, Content: "Check the final recommendation."},
				},
			},
			Harness: controlv1alpha1.AgentRunHarnessSpec{Execution: controlv1alpha1.AgentRunHarnessExecutionSpec{
				ExtraEnv: []corev1.EnvVar{{Name: "RUN_ID", Value: "42"}},
			}},
		},
	}

	reconciler := testCompositionReconciler(t, profile, codexHarness, piHarness, baseSet, runSet, knowledgeTools)
	effective, resolution, phase, reason, message, err := reconciler.resolveAgentRunComposition(context.Background(), run)
	if err != nil {
		t.Fatalf("resolve composition: %v", err)
	}
	if phase != "" || reason != "" || message != "" {
		t.Fatalf("unexpected block phase=%q reason=%q message=%q", phase, reason, message)
	}
	if got := effective.Spec.Harness.Backend.Kind; got != controlv1alpha1.AgentRunHarnessBackendPiAgent {
		t.Fatalf("backend = %q, want Pi", got)
	}
	if got := effective.Spec.Harness.Execution.EnvSecretRefs; len(got) != 1 || got[0].Name != "pi-credentials" {
		t.Fatalf("runtime secrets = %#v, want only Pi credentials", got)
	}
	if got := effective.Spec.Harness.Execution.ExtraEnv; len(got) != 1 || got[0].Value != "42" {
		t.Fatalf("run-local env = %#v", got)
	}
	if got := effective.Spec.Harness.SystemPrompt; !strings.Contains(got, "Role policy") {
		t.Fatalf("profile role prompt was lost: %q", got)
	}
	if got := effective.Spec.Harness.SkillInjections; len(got) != 2 || got[0].Name != "evidence-review" || got[1].Name != "final-check" {
		t.Fatalf("resolved skills = %#v", got)
	} else if !strings.Contains(got[0].Content, "Review the incident evidence.") || !strings.Contains(got[0].Content, "Local override:\nOnly incident 42.") || strings.Contains(got[0].Content, "profile review policy") {
		t.Fatalf("resolved evidence skill content = %q", got[0].Content)
	}
	if len(effective.Spec.Harness.Tools) != 2 || effective.Spec.Harness.Tools[0].Name != "legacy-repository-status" || effective.Spec.Harness.Tools[1].Name != "knowledge-search" || len(effective.Spec.Harness.Subagents) != 1 {
		t.Fatalf("resolved tools/subagents = %#v / %#v", effective.Spec.Harness.Tools, effective.Spec.Harness.Subagents)
	}
	if resolution == nil || resolution.ProfileRef == nil || resolution.HarnessProfileRef == nil || len(resolution.SkillSetRefs) != 2 || len(resolution.ToolSetRefs) != 1 {
		t.Fatalf("resolution status = %#v", resolution)
	}
	if resolution.HarnessProfileRef.Name != "pi-standard" || resolution.EffectiveDigest == "" || resolution.SkillSetRefs[0].Digest == "" || resolution.ToolSetRefs[0].Digest == "" {
		t.Fatalf("incomplete resolution status = %#v", resolution)
	}
	if resolution.Scope == nil || resolution.Scope.Application != "payments" || resolution.Scope.ApplicationTarget != "production" {
		t.Fatalf("resolved scope = %#v", resolution.Scope)
	}
}

func TestAgentRunCompositionAppliesInlineRuntimeOverlayToProfileSelectedHarness(t *testing.T) {
	t.Parallel()

	harness := testAgentHarnessProfile("codex-standard", controlv1alpha1.AgentRunHarnessBackendCodex, "base-credentials")
	profile := &controlv1alpha1.AgentRunProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "reviewer", Namespace: "agents"},
		Spec: controlv1alpha1.AgentRunProfileSpec{
			HarnessProfileRef: &controlv1alpha1.NamespacedObjectReference{Name: harness.Name},
			Harness: controlv1alpha1.AgentRunHarnessSpec{
				Backend: controlv1alpha1.AgentRunHarnessBackendSpec{Codex: &controlv1alpha1.AgentRunCodexBackendSpec{Model: "gpt-profile"}},
				Execution: controlv1alpha1.AgentRunHarnessExecutionSpec{
					ServiceAccountName: "profile-runner",
					EnvSecretRefs:      []controlv1alpha1.NamespacedObjectReference{{Name: "profile-overlay"}},
				},
			},
		},
	}
	run := &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{Name: "review", Namespace: "agents"},
		Spec: controlv1alpha1.AgentRunSpec{
			ProfileRef: &controlv1alpha1.NamespacedObjectReference{Name: profile.Name},
			Harness: controlv1alpha1.AgentRunHarnessSpec{
				Backend:   controlv1alpha1.AgentRunHarnessBackendSpec{Codex: &controlv1alpha1.AgentRunCodexBackendSpec{ReasoningEffort: "high"}},
				Execution: controlv1alpha1.AgentRunHarnessExecutionSpec{ExtraEnv: []corev1.EnvVar{{Name: "RUN_SCOPE", Value: "review"}}},
			},
		},
	}

	effective, resolution, phase, reason, message, err := testCompositionReconciler(t, profile, harness).
		resolveAgentRunComposition(context.Background(), run)
	if err != nil || phase != "" {
		t.Fatalf("resolve composition: phase=%q reason=%q message=%q err=%v", phase, reason, message, err)
	}
	if got := effective.Spec.Harness.Backend.Codex; got == nil || got.Model != "gpt-profile" || got.ReasoningEffort != "high" {
		t.Fatalf("merged backend = %#v", got)
	}
	if got := effective.Spec.Harness.Execution; got.ServiceAccountName != "profile-runner" || len(got.EnvSecretRefs) != 2 || len(got.ExtraEnv) != 1 {
		t.Fatalf("merged execution = %#v", got)
	}
	if resolution == nil || resolution.HarnessProfileRef == nil || resolution.HarnessProfileRef.Name != harness.Name {
		t.Fatalf("resolution = %#v", resolution)
	}
}

func TestAgentRunJobPreservesResolvedCompositionSnapshot(t *testing.T) {
	t.Parallel()

	resolvedAt := metav1.NewTime(metav1.Now().Time.Truncate(time.Second))
	want := &controlv1alpha1.AgentRunResolvedCompositionStatus{
		ResolvedAt: &resolvedAt,
		HarnessProfileRef: &controlv1alpha1.AgentRunResolvedObjectReferenceStatus{
			Name: "pi-standard", Namespace: "agents", UID: "harness-uid", Generation: 4,
			ResourceVersion: "17", Digest: "sha256:harness",
		},
		SkillSetRefs: []controlv1alpha1.AgentRunResolvedObjectReferenceStatus{{
			Name: "repository-review", Namespace: "agents", UID: "skills-uid", Generation: 3,
			ResourceVersion: "12", Digest: "sha256:skills",
		}},
		ToolSetRefs: []controlv1alpha1.AgentRunResolvedObjectReferenceStatus{{
			Name: "knowledge-tools", Namespace: "agents", UID: "tools-uid", Generation: 2,
			ResourceVersion: "9", Digest: "sha256:tools",
		}},
		EffectiveDigest: "sha256:effective",
		PayloadDigest:   "sha256:payload",
	}
	run := &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{Name: "review", Namespace: "agents"},
		Spec: controlv1alpha1.AgentRunSpec{Harness: controlv1alpha1.AgentRunHarnessSpec{
			Backend: controlv1alpha1.AgentRunHarnessBackendSpec{Kind: controlv1alpha1.AgentRunHarnessBackendPiAgent},
		}},
		Status: controlv1alpha1.AgentRunStatus{ResolvedComposition: want.DeepCopy()},
	}

	job := agentRunJob(run, "review-harness", "review-context", nil)
	got := agentRunResolvedCompositionFromJob(job)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resolved snapshot = %#v, want %#v", got, want)
	}
}

func TestAgentRunContextUsesEffectiveSpecWithoutRawProfileCopy(t *testing.T) {
	t.Parallel()

	profile := &controlv1alpha1.AgentRunProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "legacy", Namespace: "agents"},
		Spec: controlv1alpha1.AgentRunProfileSpec{Harness: controlv1alpha1.AgentRunHarnessSpec{
			Execution: controlv1alpha1.AgentRunHarnessExecutionSpec{
				EnvSecretRefs: []controlv1alpha1.NamespacedObjectReference{{Name: "legacy-provider-secret"}},
			},
		}},
	}
	effective := &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{Name: "review", Namespace: "agents"},
		Spec: controlv1alpha1.AgentRunSpec{
			ProfileRef:   &controlv1alpha1.NamespacedObjectReference{Name: profile.Name},
			ScheduleRef:  &controlv1alpha1.NamespacedObjectReference{Name: "nightly"},
			SituationRef: &controlv1alpha1.NamespacedObjectReference{Name: "incident"},
			Harness: controlv1alpha1.AgentRunHarnessSpec{
				Backend: controlv1alpha1.AgentRunHarnessBackendSpec{Kind: controlv1alpha1.AgentRunHarnessBackendCustom},
			},
		},
		Status: controlv1alpha1.AgentRunStatus{ResolvedComposition: &controlv1alpha1.AgentRunResolvedCompositionStatus{
			EffectiveDigest: "sha256:effective",
		}},
	}
	schedule := &controlv1alpha1.AgentSchedule{
		ObjectMeta: metav1.ObjectMeta{Name: "nightly", Namespace: "agents"},
		Spec: controlv1alpha1.AgentScheduleSpec{
			IntervalSeconds: 3600,
			RunTemplate: controlv1alpha1.AgentRunSpec{Harness: controlv1alpha1.AgentRunHarnessSpec{Execution: controlv1alpha1.AgentRunHarnessExecutionSpec{
				EnvSecretRefs: []controlv1alpha1.NamespacedObjectReference{{Name: "sibling-schedule-secret"}},
			}}},
			RunTemplates: []controlv1alpha1.AgentScheduleRunTemplateSpec{{
				Name: "other",
				Template: controlv1alpha1.AgentRunSpec{Harness: controlv1alpha1.AgentRunHarnessSpec{Execution: controlv1alpha1.AgentRunHarnessExecutionSpec{
					EnvSecretRefs: []controlv1alpha1.NamespacedObjectReference{{Name: "sibling-named-secret"}},
				}}},
			}},
		},
	}
	enabled := true
	situation := &controlv1alpha1.AdverseSituation{
		ObjectMeta: metav1.ObjectMeta{Name: "incident", Namespace: "agents"},
		Spec: controlv1alpha1.AdverseSituationSpec{Responders: controlv1alpha1.AdverseSituationRespondersSpec{
			AgentRun: &controlv1alpha1.AdverseSituationAgentRunResponderSpec{
				Enabled: &enabled,
				Harness: controlv1alpha1.AgentRunHarnessSpec{Execution: controlv1alpha1.AgentRunHarnessExecutionSpec{
					EnvSecretRefs: []controlv1alpha1.NamespacedObjectReference{{Name: "sibling-responder-secret"}},
				}},
			},
		}},
		Status: controlv1alpha1.AdverseSituationStatus{Events: []controlv1alpha1.AdverseSituationEvent{{
			ID: "provider-timeout", ReportIDs: []string{"internal-delivery-receipt"}, Message: "provider timed out",
		}}},
	}

	body, err := testCompositionReconciler(t, profile, schedule, situation).agentRunContextJSON(context.Background(), effective)
	if err != nil {
		t.Fatalf("build context: %v", err)
	}
	payload := map[string]any{}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode context: %v", err)
	}
	if _, exists := payload["agentRunProfile"]; exists {
		t.Fatalf("context must not embed a second mutable AgentRunProfile: %s", body)
	}
	if strings.Contains(string(body), "legacy-provider-secret") {
		t.Fatalf("context leaked profile-inline runtime metadata: %s", body)
	}
	for _, secretName := range []string{"sibling-schedule-secret", "sibling-named-secret", "sibling-responder-secret"} {
		if strings.Contains(string(body), secretName) {
			t.Fatalf("context leaked sibling runtime metadata %q: %s", secretName, body)
		}
	}
	if payload["agentSchedule"] == nil || payload["adverseSituation"] == nil {
		t.Fatalf("context omitted sanitized source objects: %s", body)
	}
	if strings.Contains(string(body), "internal-delivery-receipt") || strings.Contains(string(body), "reportIDs") {
		t.Fatalf("context leaked internal delivery receipts: %s", body)
	}
}

func TestAgentRunCompositionReplaceDiscardsProfileSkillComposition(t *testing.T) {
	t.Parallel()

	profileSet := &controlv1alpha1.AgentSkillSet{
		ObjectMeta: metav1.ObjectMeta{Name: "profile-skills", Namespace: "agents"},
		Spec:       controlv1alpha1.AgentSkillSetSpec{Skills: []controlv1alpha1.AgentRunSkillInjectionSpec{{Name: "profile-only", Content: "profile"}}},
	}
	runSet := &controlv1alpha1.AgentSkillSet{
		ObjectMeta: metav1.ObjectMeta{Name: "run-skills", Namespace: "agents"},
		Spec:       controlv1alpha1.AgentSkillSetSpec{Skills: []controlv1alpha1.AgentRunSkillInjectionSpec{{Name: "run-only", Content: "run"}}},
	}
	profile := &controlv1alpha1.AgentRunProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "reviewer", Namespace: "agents"},
		Spec: controlv1alpha1.AgentRunProfileSpec{SkillSets: &controlv1alpha1.AgentSkillCompositionSpec{
			Refs:      []controlv1alpha1.NamespacedObjectReference{{Name: profileSet.Name}},
			Overrides: []controlv1alpha1.AgentSkillOverrideSpec{{Name: "profile-added", Operation: controlv1alpha1.AgentSkillOverrideAdd, Content: "profile add"}},
		}},
	}
	run := &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{Name: "replace", Namespace: "agents"},
		Spec: controlv1alpha1.AgentRunSpec{
			ProfileRef: &controlv1alpha1.NamespacedObjectReference{Name: profile.Name},
			SkillSets: &controlv1alpha1.AgentSkillCompositionSpec{
				Mode: controlv1alpha1.AgentSkillCompositionReplace,
				Refs: []controlv1alpha1.NamespacedObjectReference{{Name: runSet.Name}},
			},
		},
	}
	reconciler := testCompositionReconciler(t, profile, profileSet, runSet)
	effective, resolution, phase, reason, message, err := reconciler.resolveAgentRunComposition(context.Background(), run)
	if err != nil || phase != "" {
		t.Fatalf("resolve replacement: phase=%q reason=%q message=%q err=%v", phase, reason, message, err)
	}
	if got := effective.Spec.Harness.SkillInjections; len(got) != 1 || got[0].Name != "run-only" {
		t.Fatalf("replacement skills = %#v", got)
	}
	if resolution == nil || len(resolution.SkillSetRefs) != 1 || resolution.SkillSetRefs[0].Name != runSet.Name {
		t.Fatalf("replacement resolution = %#v", resolution)
	}
	if effective.Spec.SkillSets == nil || len(effective.Spec.SkillSets.Refs) != 1 || effective.Spec.SkillSets.Refs[0].Name != runSet.Name {
		t.Fatalf("effective skill composition = %#v", effective.Spec.SkillSets)
	}
}

func TestAgentRunCompositionReplaceDoesNotResolveBrokenProfileSkillComposition(t *testing.T) {
	t.Parallel()

	runSet := &controlv1alpha1.AgentSkillSet{
		ObjectMeta: metav1.ObjectMeta{Name: "run-skills", Namespace: "agents"},
		Spec:       controlv1alpha1.AgentSkillSetSpec{Skills: []controlv1alpha1.AgentRunSkillInjectionSpec{{Name: "run-only", Content: "run"}}},
	}
	profile := &controlv1alpha1.AgentRunProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "broken-profile", Namespace: "agents"},
		Spec: controlv1alpha1.AgentRunProfileSpec{SkillSets: &controlv1alpha1.AgentSkillCompositionSpec{
			Refs: []controlv1alpha1.NamespacedObjectReference{{Name: "missing-profile-skills"}},
		}},
	}
	run := &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{Name: "replace", Namespace: "agents"},
		Spec: controlv1alpha1.AgentRunSpec{
			ProfileRef: &controlv1alpha1.NamespacedObjectReference{Name: profile.Name},
			SkillSets: &controlv1alpha1.AgentSkillCompositionSpec{
				Mode: controlv1alpha1.AgentSkillCompositionReplace,
				Refs: []controlv1alpha1.NamespacedObjectReference{{Name: runSet.Name}},
			},
		},
	}

	effective, resolution, phase, reason, message, err := testCompositionReconciler(t, profile, runSet).
		resolveAgentRunComposition(context.Background(), run)
	if err != nil || phase != "" {
		t.Fatalf("resolve replacement: phase=%q reason=%q message=%q err=%v", phase, reason, message, err)
	}
	if got := effective.Spec.Harness.SkillInjections; len(got) != 1 || got[0].Name != "run-only" {
		t.Fatalf("replacement skills = %#v", got)
	}
	if resolution == nil || len(resolution.SkillSetRefs) != 1 || resolution.SkillSetRefs[0].Name != runSet.Name {
		t.Fatalf("replacement resolution = %#v", resolution)
	}
}

func TestAgentRunCompositionComposesToolSetsAndAppliesInlineOverlay(t *testing.T) {
	t.Parallel()

	profileTools := &controlv1alpha1.AgentToolSet{
		ObjectMeta: metav1.ObjectMeta{Name: "knowledge-tools", Namespace: "agents", UID: "knowledge-uid", Generation: 7},
		Spec: controlv1alpha1.AgentToolSetSpec{Tools: []controlv1alpha1.AgentRunToolSpec{{
			Name: "knowledge-query", VerifyCommand: []string{"knowledge-query", "--help"},
		}}},
	}
	runTools := &controlv1alpha1.AgentToolSet{
		ObjectMeta: metav1.ObjectMeta{Name: "repository-tools", Namespace: "agents", UID: "repository-uid", Generation: 2},
		Spec: controlv1alpha1.AgentToolSetSpec{Tools: []controlv1alpha1.AgentRunToolSpec{{
			Name: "repository-status", VerifyCommand: []string{"git", "status", "--short"},
		}}},
	}
	profile := &controlv1alpha1.AgentRunProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "maintainer", Namespace: "agents"},
		Spec: controlv1alpha1.AgentRunProfileSpec{
			SkillSets: &controlv1alpha1.AgentSkillCompositionSpec{Refs: []controlv1alpha1.NamespacedObjectReference{{Name: "legacy-knowledge-tools"}}},
			ToolSets:  &controlv1alpha1.AgentToolCompositionSpec{Refs: []controlv1alpha1.NamespacedObjectReference{{Name: profileTools.Name}}},
		},
	}
	legacyTools := &controlv1alpha1.AgentSkillSet{
		ObjectMeta: metav1.ObjectMeta{Name: "legacy-knowledge-tools", Namespace: "agents"},
		Spec: controlv1alpha1.AgentSkillSetSpec{Tools: []controlv1alpha1.AgentRunToolSpec{{
			Name: "knowledge-query", VerifyCommand: []string{"knowledge-query", "--help"},
		}}},
	}
	run := &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{Name: "review", Namespace: "agents"},
		Spec: controlv1alpha1.AgentRunSpec{
			ProfileRef: &controlv1alpha1.NamespacedObjectReference{Name: profile.Name},
			ToolSets: &controlv1alpha1.AgentToolCompositionSpec{
				Refs: []controlv1alpha1.NamespacedObjectReference{{Name: runTools.Name}},
			},
			Harness: controlv1alpha1.AgentRunHarnessSpec{Tools: []controlv1alpha1.AgentRunToolSpec{{
				Name: "knowledge-query", VerifyCommand: []string{"knowledge-query", "--version"},
			}}},
		},
	}

	effective, resolution, phase, reason, message, err := testCompositionReconciler(t, profile, profileTools, runTools, legacyTools).
		resolveAgentRunComposition(context.Background(), run)
	if err != nil || phase != "" {
		t.Fatalf("resolve tool composition: phase=%q reason=%q message=%q err=%v", phase, reason, message, err)
	}
	if got := effective.Spec.Harness.Tools; len(got) != 2 || got[0].Name != "knowledge-query" || got[0].VerifyCommand[1] != "--version" || got[1].Name != "repository-status" {
		t.Fatalf("resolved tools = %#v", got)
	}
	if resolution == nil || len(resolution.ToolSetRefs) != 2 || resolution.ToolSetRefs[0].Name != profileTools.Name || resolution.ToolSetRefs[1].Name != runTools.Name {
		t.Fatalf("resolved tool refs = %#v", resolution)
	}
}

func TestAgentRunCompositionCanonicalReplaceAndInlinePrecedence(t *testing.T) {
	t.Parallel()

	legacySkills := &controlv1alpha1.AgentSkillSet{
		ObjectMeta: metav1.ObjectMeta{Name: "legacy-skills", Namespace: "agents", UID: "legacy-skills-uid", Generation: 2},
		Spec:       controlv1alpha1.AgentSkillSetSpec{Skills: []controlv1alpha1.AgentRunSkillInjectionSpec{{Name: "legacy", Content: "legacy"}}},
	}
	profileSkill := &controlv1alpha1.AgentSkill{
		ObjectMeta: metav1.ObjectMeta{Name: "profile-skill", Namespace: "agents", UID: "profile-skill-uid", Generation: 3, ResourceVersion: "11"},
		Spec:       controlv1alpha1.AgentSkillSpec{Inline: &controlv1alpha1.AgentSkillInlineSource{SkillMD: "profile"}},
	}
	runSkill := &controlv1alpha1.AgentSkill{
		ObjectMeta: metav1.ObjectMeta{Name: "run-skill", Namespace: "agents", UID: "run-skill-uid", Generation: 4, ResourceVersion: "12"},
		Spec:       controlv1alpha1.AgentSkillSpec{Inline: &controlv1alpha1.AgentSkillInlineSource{SkillMD: "run", References: []controlv1alpha1.AgentSkillMarkdownReference{{Path: "references/checks.md", Content: "checks"}}}},
	}
	profileTool := canonicalInlineTool("profile-tool", "profile-tool-uid")
	runTool := canonicalInlineTool("run-tool", "run-tool-uid")
	profileMCP := canonicalMCPServer("profile-mcp", "profile-mcp-uid")
	runMCP := canonicalMCPServer("run-mcp", "run-mcp-uid")
	profileMCPSet := &controlv1alpha1.AgentMCPSet{
		ObjectMeta: metav1.ObjectMeta{Name: "profile-mcps", Namespace: "agents", UID: "profile-mcps-uid", Generation: 2},
		Spec:       controlv1alpha1.AgentMCPSetSpec{ServerRefs: []controlv1alpha1.NamespacedObjectReference{{Name: profileMCP.Name}}},
	}
	profile := &controlv1alpha1.AgentRunProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "canonical", Namespace: "agents"},
		Spec: controlv1alpha1.AgentRunProfileSpec{
			SkillSets: &controlv1alpha1.AgentSkillCompositionSpec{Refs: []controlv1alpha1.NamespacedObjectReference{{Name: legacySkills.Name}}},
			Capabilities: &controlv1alpha1.AgentCapabilitiesSpec{
				Skills:     &controlv1alpha1.AgentSkillCapabilityComposition{Selections: []controlv1alpha1.AgentSkillSelection{{SkillRef: &controlv1alpha1.NamespacedObjectReference{Name: profileSkill.Name}}}},
				Tools:      &controlv1alpha1.AgentToolCapabilityComposition{Selections: []controlv1alpha1.AgentToolSelection{{ToolRef: &controlv1alpha1.NamespacedObjectReference{Name: profileTool.Name}}}},
				MCPServers: &controlv1alpha1.AgentMCPCapabilityComposition{Selections: []controlv1alpha1.AgentMCPSelection{{MCPSetRef: &controlv1alpha1.NamespacedObjectReference{Name: profileMCPSet.Name}}}},
			},
		},
	}
	run := &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{Name: "replace", Namespace: "agents"},
		Spec: controlv1alpha1.AgentRunSpec{
			ProfileRef: &controlv1alpha1.NamespacedObjectReference{Name: profile.Name},
			Capabilities: &controlv1alpha1.AgentCapabilitiesSpec{
				Skills:     &controlv1alpha1.AgentSkillCapabilityComposition{Mode: controlv1alpha1.AgentCapabilityCompositionReplace, Selections: []controlv1alpha1.AgentSkillSelection{{SkillRef: &controlv1alpha1.NamespacedObjectReference{Name: runSkill.Name}}}},
				Tools:      &controlv1alpha1.AgentToolCapabilityComposition{Mode: controlv1alpha1.AgentCapabilityCompositionReplace, Selections: []controlv1alpha1.AgentToolSelection{{ToolRef: &controlv1alpha1.NamespacedObjectReference{Name: runTool.Name}}}},
				MCPServers: &controlv1alpha1.AgentMCPCapabilityComposition{Mode: controlv1alpha1.AgentCapabilityCompositionReplace, Selections: []controlv1alpha1.AgentMCPSelection{{ServerRef: &controlv1alpha1.NamespacedObjectReference{Name: runMCP.Name}}}},
			},
			Harness: controlv1alpha1.AgentRunHarnessSpec{
				SkillInjections: []controlv1alpha1.AgentRunSkillInjectionSpec{{Name: "run-skill", Content: "inline wins"}},
				Tools:           []controlv1alpha1.AgentRunToolSpec{{Name: "inline-tool", VerifyCommand: []string{"inline-tool", "--help"}}},
				MCPServers:      []controlv1alpha1.AgentRunMCPServerSpec{{Name: "inline-mcp", Transport: *profileMCP.Spec.Transport.DeepCopy()}},
			},
		},
	}

	effective, resolution, phase, reason, message, err := testCompositionReconciler(t, legacySkills, profileSkill, runSkill, profileTool, runTool, profileMCP, runMCP, profileMCPSet, profile).
		resolveAgentRunComposition(context.Background(), run)
	if err != nil || phase != "" {
		t.Fatalf("resolve canonical composition: phase=%q reason=%q message=%q err=%v", phase, reason, message, err)
	}
	if got := effective.Spec.Harness.SkillInjections; len(got) != 1 || got[0].Name != runSkill.Name || got[0].Content != "inline wins" {
		t.Fatalf("canonical replacement and inline skill overlay = %#v", got)
	}
	if got := effective.Spec.Harness.Tools; len(got) != 2 || got[0].Name != runTool.Name || got[1].Name != "inline-tool" || got[0].SpecDigest == "" {
		t.Fatalf("canonical replacement and inline tools = %#v", got)
	}
	if got := effective.Spec.Harness.MCPServers; len(got) != 2 || got[0].Name != runMCP.Name || got[1].Name != "inline-mcp" {
		t.Fatalf("canonical replacement and inline MCP servers = %#v", got)
	}
	if resolution == nil || len(resolution.SkillSetRefs) != 0 || len(resolution.SkillRefs) != 1 || len(resolution.ToolRefs) != 1 || len(resolution.MCPServerRefs) != 1 || len(resolution.MCPSetRefs) != 0 {
		t.Fatalf("canonical resolution evidence = %#v", resolution)
	}
	if got := resolution.SkillRefs[0]; got.UID != "run-skill-uid" || got.Generation != 4 || got.ResourceVersion != "12" || got.Digest == "" {
		t.Fatalf("atomic skill provenance = %#v", got)
	}
}

func TestAgentRunCompositionRejectsDirectAndSetDuplicateAtomicRef(t *testing.T) {
	t.Parallel()
	skill := &controlv1alpha1.AgentSkill{ObjectMeta: metav1.ObjectMeta{Name: "review", Namespace: "agents"}, Spec: controlv1alpha1.AgentSkillSpec{Inline: &controlv1alpha1.AgentSkillInlineSource{SkillMD: "review"}}}
	set := &controlv1alpha1.AgentSkillSet{ObjectMeta: metav1.ObjectMeta{Name: "review-set", Namespace: "agents"}, Spec: controlv1alpha1.AgentSkillSetSpec{SkillRefs: []controlv1alpha1.NamespacedObjectReference{{Name: skill.Name}}}}
	run := testCompositionRun(&controlv1alpha1.AgentRunSpec{Capabilities: &controlv1alpha1.AgentCapabilitiesSpec{Skills: &controlv1alpha1.AgentSkillCapabilityComposition{Selections: []controlv1alpha1.AgentSkillSelection{
		{SkillRef: &controlv1alpha1.NamespacedObjectReference{Name: skill.Name}},
		{SkillSetRef: &controlv1alpha1.NamespacedObjectReference{Name: set.Name}},
	}}}})
	_, _, phase, reason, _, err := testCompositionReconciler(t, skill, set).resolveAgentRunComposition(context.Background(), run)
	if err != nil || phase != controlv1alpha1.AgentRunPhaseFailed || reason != "DuplicateSkillRef" {
		t.Fatalf("duplicate atomic ref block phase=%q reason=%q err=%v", phase, reason, err)
	}
}

func TestLegacySetSelectionsExpandCanonicalAtomicRefs(t *testing.T) {
	t.Parallel()
	skill := &controlv1alpha1.AgentSkill{ObjectMeta: metav1.ObjectMeta{Name: "review", Namespace: "agents", UID: "skill-uid"}, Spec: controlv1alpha1.AgentSkillSpec{Inline: &controlv1alpha1.AgentSkillInlineSource{SkillMD: "review"}}}
	tool := canonicalInlineTool("query", "tool-uid")
	skillSet := &controlv1alpha1.AgentSkillSet{ObjectMeta: metav1.ObjectMeta{Name: "reviews", Namespace: "agents", UID: "skill-set-uid"}, Spec: controlv1alpha1.AgentSkillSetSpec{SkillRefs: []controlv1alpha1.NamespacedObjectReference{{Name: skill.Name}}}}
	toolSet := &controlv1alpha1.AgentToolSet{ObjectMeta: metav1.ObjectMeta{Name: "queries", Namespace: "agents", UID: "tool-set-uid"}, Spec: controlv1alpha1.AgentToolSetSpec{ToolRefs: []controlv1alpha1.NamespacedObjectReference{{Name: tool.Name}}}}
	profile := &controlv1alpha1.AgentRunProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "legacy-selectors", Namespace: "agents"},
		Spec: controlv1alpha1.AgentRunProfileSpec{
			SkillSets: &controlv1alpha1.AgentSkillCompositionSpec{Refs: []controlv1alpha1.NamespacedObjectReference{{Name: skillSet.Name}}},
			ToolSets:  &controlv1alpha1.AgentToolCompositionSpec{Refs: []controlv1alpha1.NamespacedObjectReference{{Name: toolSet.Name}}},
		},
	}
	run := testCompositionRun(&controlv1alpha1.AgentRunSpec{ProfileRef: &controlv1alpha1.NamespacedObjectReference{Name: profile.Name}})
	effective, resolution, phase, reason, message, err := testCompositionReconciler(t, skill, tool, skillSet, toolSet, profile).resolveAgentRunComposition(context.Background(), run)
	if err != nil || phase != "" {
		t.Fatalf("resolve composition: phase=%q reason=%q message=%q err=%v", phase, reason, message, err)
	}
	if len(effective.Spec.Harness.SkillInjections) != 1 || effective.Spec.Harness.SkillInjections[0].Name != skill.Name || len(effective.Spec.Harness.Tools) != 1 || effective.Spec.Harness.Tools[0].Name != tool.Name {
		t.Fatalf("legacy selectors did not expand canonical refs: skills=%#v tools=%#v", effective.Spec.Harness.SkillInjections, effective.Spec.Harness.Tools)
	}
	if resolution == nil || len(resolution.SkillRefs) != 1 || len(resolution.ToolRefs) != 1 {
		t.Fatalf("atomic provenance missing: %#v", resolution)
	}
}

func TestInlineMCPOverlayReplacesCanonicalServerByName(t *testing.T) {
	t.Parallel()
	server := canonicalMCPServer("context", "mcp-uid")
	overlay := controlv1alpha1.AgentRunMCPServerSpec{Name: server.Name, Transport: controlv1alpha1.AgentMCPTransport{StreamableHTTP: &controlv1alpha1.AgentMCPStreamableHTTPTransport{Endpoint: "https://mcp.example.test/v1"}}}
	run := testCompositionRun(&controlv1alpha1.AgentRunSpec{
		Capabilities: &controlv1alpha1.AgentCapabilitiesSpec{MCPServers: &controlv1alpha1.AgentMCPCapabilityComposition{Selections: []controlv1alpha1.AgentMCPSelection{{ServerRef: &controlv1alpha1.NamespacedObjectReference{Name: server.Name}}}}},
		Harness:      controlv1alpha1.AgentRunHarnessSpec{MCPServers: []controlv1alpha1.AgentRunMCPServerSpec{overlay}},
	})
	effective, _, phase, reason, message, err := testCompositionReconciler(t, server).resolveAgentRunComposition(context.Background(), run)
	if err != nil || phase != "" {
		t.Fatalf("resolve composition: phase=%q reason=%q message=%q err=%v", phase, reason, message, err)
	}
	if got := effective.Spec.Harness.MCPServers; len(got) != 1 || got[0].Transport.StreamableHTTP == nil {
		t.Fatalf("inline MCP overlay did not replace canonical server: %#v", got)
	}
}

func TestCanonicalSkillReplaceRetainsLegacySetProvenanceForRemainingTool(t *testing.T) {
	t.Parallel()
	legacy := &controlv1alpha1.AgentSkillSet{
		ObjectMeta: metav1.ObjectMeta{Name: "legacy", Namespace: "agents", UID: "legacy-uid", Generation: 3},
		Spec: controlv1alpha1.AgentSkillSetSpec{
			Skills: []controlv1alpha1.AgentRunSkillInjectionSpec{{Name: "old-skill", Content: "old"}},
			Tools:  []controlv1alpha1.AgentRunToolSpec{{Name: "remaining-tool", VerifyCommand: []string{"remaining-tool", "--help"}}},
		},
	}
	canonical := &controlv1alpha1.AgentSkill{
		ObjectMeta: metav1.ObjectMeta{Name: "new-skill", Namespace: "agents"},
		Spec:       controlv1alpha1.AgentSkillSpec{Inline: &controlv1alpha1.AgentSkillInlineSource{SkillMD: "new"}},
	}
	profile := &controlv1alpha1.AgentRunProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "profile", Namespace: "agents"},
		Spec:       controlv1alpha1.AgentRunProfileSpec{SkillSets: &controlv1alpha1.AgentSkillCompositionSpec{Refs: []controlv1alpha1.NamespacedObjectReference{{Name: legacy.Name}}}},
	}
	run := testCompositionRun(&controlv1alpha1.AgentRunSpec{
		ProfileRef: &controlv1alpha1.NamespacedObjectReference{Name: profile.Name},
		Capabilities: &controlv1alpha1.AgentCapabilitiesSpec{Skills: &controlv1alpha1.AgentSkillCapabilityComposition{
			Mode:       controlv1alpha1.AgentCapabilityCompositionReplace,
			Selections: []controlv1alpha1.AgentSkillSelection{{SkillRef: &controlv1alpha1.NamespacedObjectReference{Name: canonical.Name}}},
		}},
	})
	effective, resolution, phase, reason, message, err := testCompositionReconciler(t, legacy, canonical, profile).resolveAgentRunComposition(context.Background(), run)
	if err != nil || phase != "" {
		t.Fatalf("resolve composition: phase=%q reason=%q message=%q err=%v", phase, reason, message, err)
	}
	if len(effective.Spec.Harness.Tools) != 1 || effective.Spec.Harness.Tools[0].Name != "remaining-tool" {
		t.Fatalf("legacy tool was not retained: %#v", effective.Spec.Harness.Tools)
	}
	if resolution == nil || len(resolution.SkillSetRefs) != 1 || resolution.SkillSetRefs[0].Name != legacy.Name {
		t.Fatalf("remaining legacy tool lost its set provenance: %#v", resolution)
	}
}

func canonicalInlineTool(name, uid string) *controlv1alpha1.AgentTool {
	return &controlv1alpha1.AgentTool{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "agents", UID: typesUID(uid), Generation: 2},
		Spec: controlv1alpha1.AgentToolSpec{
			Executable:    controlv1alpha1.AgentToolExecutable{Name: name, Path: name},
			Source:        &controlv1alpha1.AgentToolSource{InlineScript: &controlv1alpha1.AgentToolInlineScript{Interpreter: []string{"/usr/bin/env", "bash"}, Script: "#!/usr/bin/env bash\nexit 0"}},
			VerifyCommand: []string{name, "--help"},
		},
	}
}

func canonicalMCPServer(name, uid string) *controlv1alpha1.AgentMCPServer {
	return &controlv1alpha1.AgentMCPServer{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "agents", UID: typesUID(uid), Generation: 2},
		Spec:       controlv1alpha1.AgentMCPServerSpec{Transport: controlv1alpha1.AgentMCPTransport{Stdio: &controlv1alpha1.AgentMCPStdioTransport{Command: []string{"mcp-server", "--stdio"}}}},
	}
}

func TestAgentRunCompositionReplaceDoesNotResolveBrokenProfileToolComposition(t *testing.T) {
	t.Parallel()

	runTools := &controlv1alpha1.AgentToolSet{
		ObjectMeta: metav1.ObjectMeta{Name: "run-tools", Namespace: "agents"},
		Spec:       controlv1alpha1.AgentToolSetSpec{Tools: []controlv1alpha1.AgentRunToolSpec{{Name: "run-only"}}},
	}
	legacyTools := &controlv1alpha1.AgentSkillSet{
		ObjectMeta: metav1.ObjectMeta{Name: "legacy-tools", Namespace: "agents"},
		Spec:       controlv1alpha1.AgentSkillSetSpec{Tools: []controlv1alpha1.AgentRunToolSpec{{Name: "legacy-only"}}},
	}
	profile := &controlv1alpha1.AgentRunProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "broken-profile", Namespace: "agents"},
		Spec: controlv1alpha1.AgentRunProfileSpec{
			SkillSets: &controlv1alpha1.AgentSkillCompositionSpec{Refs: []controlv1alpha1.NamespacedObjectReference{{Name: legacyTools.Name}}},
			ToolSets:  &controlv1alpha1.AgentToolCompositionSpec{Refs: []controlv1alpha1.NamespacedObjectReference{{Name: "missing-profile-tools"}}},
		},
	}
	run := &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{Name: "replace", Namespace: "agents"},
		Spec: controlv1alpha1.AgentRunSpec{
			ProfileRef: &controlv1alpha1.NamespacedObjectReference{Name: profile.Name},
			ToolSets: &controlv1alpha1.AgentToolCompositionSpec{
				Mode: controlv1alpha1.AgentToolCompositionReplace,
				Refs: []controlv1alpha1.NamespacedObjectReference{{Name: runTools.Name}},
			},
		},
	}

	effective, resolution, phase, reason, message, err := testCompositionReconciler(t, profile, runTools, legacyTools).
		resolveAgentRunComposition(context.Background(), run)
	if err != nil || phase != "" {
		t.Fatalf("resolve tool replacement: phase=%q reason=%q message=%q err=%v", phase, reason, message, err)
	}
	if got := effective.Spec.Harness.Tools; len(got) != 2 || got[0].Name != "legacy-only" || got[1].Name != "run-only" {
		t.Fatalf("replacement tools = %#v", got)
	}
	if resolution == nil || len(resolution.ToolSetRefs) != 1 || resolution.ToolSetRefs[0].Name != runTools.Name {
		t.Fatalf("replacement resolution = %#v", resolution)
	}
}

func TestAgentRunCompositionRejectsInvalidReferencesAndOverrides(t *testing.T) {
	t.Parallel()

	conflictingA := &controlv1alpha1.AgentSkillSet{
		ObjectMeta: metav1.ObjectMeta{Name: "tools-a", Namespace: "agents"},
		Spec:       controlv1alpha1.AgentSkillSetSpec{Tools: []controlv1alpha1.AgentRunToolSpec{{Name: "query", VerifyCommand: []string{"query", "--help"}}}},
	}
	conflictingB := &controlv1alpha1.AgentSkillSet{
		ObjectMeta: metav1.ObjectMeta{Name: "tools-b", Namespace: "agents"},
		Spec:       controlv1alpha1.AgentSkillSetSpec{Tools: []controlv1alpha1.AgentRunToolSpec{{Name: "query", VerifyCommand: []string{"query", "--version"}}}},
	}
	toolSetA := &controlv1alpha1.AgentToolSet{
		ObjectMeta: metav1.ObjectMeta{Name: "external-a", Namespace: "agents"},
		Spec:       controlv1alpha1.AgentToolSetSpec{Tools: []controlv1alpha1.AgentRunToolSpec{{Name: "query", VerifyCommand: []string{"query", "--help"}}}},
	}
	toolSetB := &controlv1alpha1.AgentToolSet{
		ObjectMeta: metav1.ObjectMeta{Name: "external-b", Namespace: "agents"},
		Spec:       controlv1alpha1.AgentToolSetSpec{Tools: []controlv1alpha1.AgentRunToolSpec{{Name: "query", VerifyCommand: []string{"query", "--version"}}}},
	}
	tests := []struct {
		name       string
		run        *controlv1alpha1.AgentRun
		objects    []client.Object
		wantPhase  controlv1alpha1.AgentRunPhase
		wantReason string
	}{
		{
			name:      "cross namespace harness",
			run:       testCompositionRun(&controlv1alpha1.AgentRunSpec{HarnessProfileRef: &controlv1alpha1.NamespacedObjectReference{Name: "codex", Namespace: "other"}}),
			wantPhase: controlv1alpha1.AgentRunPhaseFailed, wantReason: "CrossNamespaceHarnessProfileRef",
		},
		{
			name:      "missing skill set",
			run:       testCompositionRun(&controlv1alpha1.AgentRunSpec{SkillSets: &controlv1alpha1.AgentSkillCompositionSpec{Refs: []controlv1alpha1.NamespacedObjectReference{{Name: "missing"}}}}),
			wantPhase: controlv1alpha1.AgentRunPhaseNeedsHuman, wantReason: "SkillSetNotFound",
		},
		{
			name:    "duplicate ref",
			run:     testCompositionRun(&controlv1alpha1.AgentRunSpec{SkillSets: &controlv1alpha1.AgentSkillCompositionSpec{Refs: []controlv1alpha1.NamespacedObjectReference{{Name: conflictingA.Name}, {Name: conflictingA.Name}}}}),
			objects: []client.Object{conflictingA}, wantPhase: controlv1alpha1.AgentRunPhaseFailed, wantReason: "DuplicateSkillSetRef",
		},
		{
			name:      "unknown override",
			run:       testCompositionRun(&controlv1alpha1.AgentRunSpec{SkillSets: &controlv1alpha1.AgentSkillCompositionSpec{Overrides: []controlv1alpha1.AgentSkillOverrideSpec{{Name: "unknown", Operation: controlv1alpha1.AgentSkillOverrideAugment, Content: "x"}}}}),
			wantPhase: controlv1alpha1.AgentRunPhaseFailed, wantReason: "UnknownSkillOverride",
		},
		{
			name:    "conflicting tool",
			run:     testCompositionRun(&controlv1alpha1.AgentRunSpec{SkillSets: &controlv1alpha1.AgentSkillCompositionSpec{Refs: []controlv1alpha1.NamespacedObjectReference{{Name: conflictingA.Name}, {Name: conflictingB.Name}}}}),
			objects: []client.Object{conflictingA, conflictingB}, wantPhase: controlv1alpha1.AgentRunPhaseFailed, wantReason: "ConflictingToolName",
		},
		{
			name:      "missing tool set",
			run:       testCompositionRun(&controlv1alpha1.AgentRunSpec{ToolSets: &controlv1alpha1.AgentToolCompositionSpec{Refs: []controlv1alpha1.NamespacedObjectReference{{Name: "missing"}}}}),
			wantPhase: controlv1alpha1.AgentRunPhaseNeedsHuman, wantReason: "ToolSetNotFound",
		},
		{
			name:      "cross namespace tool set",
			run:       testCompositionRun(&controlv1alpha1.AgentRunSpec{ToolSets: &controlv1alpha1.AgentToolCompositionSpec{Refs: []controlv1alpha1.NamespacedObjectReference{{Name: "external", Namespace: "other"}}}}),
			wantPhase: controlv1alpha1.AgentRunPhaseFailed, wantReason: "CrossNamespaceToolSetRef",
		},
		{
			name:    "duplicate tool set ref",
			run:     testCompositionRun(&controlv1alpha1.AgentRunSpec{ToolSets: &controlv1alpha1.AgentToolCompositionSpec{Refs: []controlv1alpha1.NamespacedObjectReference{{Name: toolSetA.Name}, {Name: toolSetA.Name}}}}),
			objects: []client.Object{toolSetA}, wantPhase: controlv1alpha1.AgentRunPhaseFailed, wantReason: "DuplicateToolSetRef",
		},
		{
			name:    "conflicting external tools",
			run:     testCompositionRun(&controlv1alpha1.AgentRunSpec{ToolSets: &controlv1alpha1.AgentToolCompositionSpec{Refs: []controlv1alpha1.NamespacedObjectReference{{Name: toolSetA.Name}, {Name: toolSetB.Name}}}}),
			objects: []client.Object{toolSetA, toolSetB}, wantPhase: controlv1alpha1.AgentRunPhaseFailed, wantReason: "ConflictingToolName",
		},
		{
			name: "conflicting skill and tool set contracts",
			run: testCompositionRun(&controlv1alpha1.AgentRunSpec{
				SkillSets: &controlv1alpha1.AgentSkillCompositionSpec{Refs: []controlv1alpha1.NamespacedObjectReference{{Name: conflictingA.Name}}},
				ToolSets:  &controlv1alpha1.AgentToolCompositionSpec{Refs: []controlv1alpha1.NamespacedObjectReference{{Name: toolSetB.Name}}},
			}),
			objects: []client.Object{conflictingA, toolSetB}, wantPhase: controlv1alpha1.AgentRunPhaseFailed, wantReason: "ConflictingToolName",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			reconciler := testCompositionReconciler(t, test.objects...)
			_, _, phase, reason, _, err := reconciler.resolveAgentRunComposition(context.Background(), test.run)
			if err != nil {
				t.Fatalf("resolve composition: %v", err)
			}
			if phase != test.wantPhase || reason != test.wantReason {
				t.Fatalf("block = phase:%q reason:%q, want phase:%q reason:%q", phase, reason, test.wantPhase, test.wantReason)
			}
		})
	}
}

func testAgentHarnessProfile(name string, backend controlv1alpha1.AgentRunHarnessBackendKind, secret string) *controlv1alpha1.AgentHarnessProfile {
	return &controlv1alpha1.AgentHarnessProfile{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "agents", UID: typesUID(name + "-uid"), Generation: 2},
		Spec: controlv1alpha1.AgentHarnessProfileSpec{
			Backend: controlv1alpha1.AgentRunHarnessBackendSpec{Kind: backend},
			Execution: controlv1alpha1.AgentRunHarnessExecutionSpec{
				EnvSecretRefs: []controlv1alpha1.NamespacedObjectReference{{Name: secret}},
			},
		},
	}
}

func testCompositionRun(spec *controlv1alpha1.AgentRunSpec) *controlv1alpha1.AgentRun {
	return &controlv1alpha1.AgentRun{ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "agents"}, Spec: *spec}
}

func testCompositionReconciler(t *testing.T, objects ...client.Object) *AgentRunReconciler {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := controlv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add agent scheme: %v", err)
	}
	copies := make([]client.Object, 0, len(objects))
	for _, object := range objects {
		copies = append(copies, object.DeepCopyObject().(client.Object))
	}
	return &AgentRunReconciler{Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(copies...).Build(), Scheme: scheme}
}

func typesUID(value string) types.UID {
	return types.UID(value)
}
