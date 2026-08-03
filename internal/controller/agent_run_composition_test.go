package controller

import (
	"context"
	"encoding/json"
	"fmt"
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
			Name: "knowledge-search",
			ImageInitializer: &controlv1alpha1.AgentRunToolImageInitializerSpec{
				Image:   "registry.example/knowledge-search@sha256:" + strings.Repeat("c", 64),
				Command: []string{"/install-tool"},
				Args:    []string{"/opt/anvil/tools/knowledge-search"},
			},
			VerifyCommand: []string{"knowledge-search", "--help"},
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
	initializer := effective.Spec.Harness.Tools[1].ImageInitializer
	if initializer == nil || !strings.HasPrefix(initializer.Image, "registry.example/knowledge-search@sha256:") || len(initializer.Command) != 1 || initializer.Command[0] != "/install-tool" {
		t.Fatalf("resolved OCI tool initializer = %#v", initializer)
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

func TestAgentRunCompositionAttachesGlobalSkillAndToolSets(t *testing.T) {
	t.Parallel()

	profile := &controlv1alpha1.AgentRunProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "observer", Namespace: "agents", UID: "profile-uid", Generation: 1},
		Spec: controlv1alpha1.AgentRunProfileSpec{
			HarnessProfileRef: &controlv1alpha1.NamespacedObjectReference{Name: "codex-standard"},
			// No skillSets/toolSets refs — globals alone must attach.
			Harness: controlv1alpha1.AgentRunHarnessSpec{
				Intent:       controlv1alpha1.AgentRunIntentObserve,
				SystemPrompt: "Observe.",
			},
		},
	}
	codexHarness := testAgentHarnessProfile("codex-standard", controlv1alpha1.AgentRunHarnessBackendCodex, "codex-credentials")
	globalSkill := &controlv1alpha1.AgentSkillSet{
		ObjectMeta: metav1.ObjectMeta{Name: "namespace-knowledge-skill", Namespace: "agents", UID: "skill-uid", Generation: 1},
		Spec: controlv1alpha1.AgentSkillSetSpec{
			Global:      true,
			Description: "Shared knowledge usage",
			Skills: []controlv1alpha1.AgentRunSkillInjectionSpec{{
				Name:    "knowledge-base",
				Content: "Run knowledge-search before planning.",
			}},
		},
	}
	globalTool := &controlv1alpha1.AgentToolSet{
		ObjectMeta: metav1.ObjectMeta{Name: "namespace-knowledge-tool", Namespace: "agents", UID: "tool-uid", Generation: 1},
		Spec: controlv1alpha1.AgentToolSetSpec{
			Global:      true,
			Description: "Shared knowledge client",
			Tools: []controlv1alpha1.AgentRunToolSpec{{
				Name:          "knowledge-search",
				VerifyCommand: []string{"knowledge-search", "--help"},
			}},
		},
	}
	// Non-global set must not attach automatically.
	localOnly := &controlv1alpha1.AgentSkillSet{
		ObjectMeta: metav1.ObjectMeta{Name: "local-only", Namespace: "agents", UID: "local-uid", Generation: 1},
		Spec: controlv1alpha1.AgentSkillSetSpec{
			Skills: []controlv1alpha1.AgentRunSkillInjectionSpec{{Name: "local", Content: "Not global."}},
		},
	}
	run := &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{Name: "observe-1", Namespace: "agents"},
		Spec: controlv1alpha1.AgentRunSpec{
			ProfileRef: &controlv1alpha1.NamespacedObjectReference{Name: profile.Name},
		},
	}

	reconciler := testCompositionReconciler(t, profile, codexHarness, globalSkill, globalTool, localOnly)
	effective, resolution, phase, reason, message, err := reconciler.resolveAgentRunComposition(context.Background(), run)
	if err != nil {
		t.Fatalf("resolve composition: %v", err)
	}
	if phase != "" || reason != "" || message != "" {
		t.Fatalf("unexpected block phase=%q reason=%q message=%q", phase, reason, message)
	}
	if got := effective.Spec.Harness.SkillInjections; len(got) != 1 || got[0].Name != "knowledge-base" {
		t.Fatalf("skills = %#v, want knowledge-base only", got)
	}
	if got := effective.Spec.Harness.Tools; len(got) != 1 || got[0].Name != "knowledge-search" {
		t.Fatalf("tools = %#v, want knowledge-search only", got)
	}
	if len(resolution.SkillSetRefs) != 1 || resolution.SkillSetRefs[0].Name != globalSkill.Name {
		t.Fatalf("skillSetRefs = %#v", resolution.SkillSetRefs)
	}
	if len(resolution.ToolSetRefs) != 1 || resolution.ToolSetRefs[0].Name != globalTool.Name {
		t.Fatalf("toolSetRefs = %#v", resolution.ToolSetRefs)
	}
}

func TestAgentRunCompositionGlobalDedupesExplicitRef(t *testing.T) {
	t.Parallel()

	profile := &controlv1alpha1.AgentRunProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "observer", Namespace: "agents", UID: "profile-uid", Generation: 1},
		Spec: controlv1alpha1.AgentRunProfileSpec{
			HarnessProfileRef: &controlv1alpha1.NamespacedObjectReference{Name: "codex-standard"},
			SkillSets: &controlv1alpha1.AgentSkillCompositionSpec{
				Refs: []controlv1alpha1.NamespacedObjectReference{{Name: "namespace-knowledge-skill"}},
			},
			ToolSets: &controlv1alpha1.AgentToolCompositionSpec{
				Refs: []controlv1alpha1.NamespacedObjectReference{{Name: "namespace-knowledge-tool"}},
			},
			Harness: controlv1alpha1.AgentRunHarnessSpec{Intent: controlv1alpha1.AgentRunIntentObserve, SystemPrompt: "Observe."},
		},
	}
	codexHarness := testAgentHarnessProfile("codex-standard", controlv1alpha1.AgentRunHarnessBackendCodex, "codex-credentials")
	globalSkill := &controlv1alpha1.AgentSkillSet{
		ObjectMeta: metav1.ObjectMeta{Name: "namespace-knowledge-skill", Namespace: "agents", UID: "skill-uid", Generation: 1},
		Spec: controlv1alpha1.AgentSkillSetSpec{
			Global: true,
			Skills: []controlv1alpha1.AgentRunSkillInjectionSpec{{Name: "knowledge-base", Content: "Search first."}},
		},
	}
	globalTool := &controlv1alpha1.AgentToolSet{
		ObjectMeta: metav1.ObjectMeta{Name: "namespace-knowledge-tool", Namespace: "agents", UID: "tool-uid", Generation: 1},
		Spec: controlv1alpha1.AgentToolSetSpec{
			Global: true,
			Tools:  []controlv1alpha1.AgentRunToolSpec{{Name: "knowledge-search", VerifyCommand: []string{"knowledge-search", "--help"}}},
		},
	}
	run := &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{Name: "observe-2", Namespace: "agents"},
		Spec:       controlv1alpha1.AgentRunSpec{ProfileRef: &controlv1alpha1.NamespacedObjectReference{Name: profile.Name}},
	}
	reconciler := testCompositionReconciler(t, profile, codexHarness, globalSkill, globalTool)
	effective, resolution, phase, reason, message, err := reconciler.resolveAgentRunComposition(context.Background(), run)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if phase != "" || reason != "" || message != "" {
		t.Fatalf("block phase=%q reason=%q message=%q", phase, reason, message)
	}
	if len(resolution.SkillSetRefs) != 1 || len(resolution.ToolSetRefs) != 1 {
		t.Fatalf("expected single global refs, got skills=%#v tools=%#v", resolution.SkillSetRefs, resolution.ToolSetRefs)
	}
	if len(effective.Spec.Harness.SkillInjections) != 1 || len(effective.Spec.Harness.Tools) != 1 {
		t.Fatalf("duplicated capabilities skills=%#v tools=%#v", effective.Spec.Harness.SkillInjections, effective.Spec.Harness.Tools)
	}
}

func TestAgentRunCompositionExcludeGlobal(t *testing.T) {
	t.Parallel()

	profile := &controlv1alpha1.AgentRunProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "isolated", Namespace: "agents", UID: "profile-uid", Generation: 1},
		Spec: controlv1alpha1.AgentRunProfileSpec{
			HarnessProfileRef: &controlv1alpha1.NamespacedObjectReference{Name: "codex-standard"},
			SkillSets:         &controlv1alpha1.AgentSkillCompositionSpec{ExcludeGlobal: true},
			ToolSets:          &controlv1alpha1.AgentToolCompositionSpec{ExcludeGlobal: true},
			Harness:           controlv1alpha1.AgentRunHarnessSpec{Intent: controlv1alpha1.AgentRunIntentObserve, SystemPrompt: "Isolated."},
		},
	}
	codexHarness := testAgentHarnessProfile("codex-standard", controlv1alpha1.AgentRunHarnessBackendCodex, "codex-credentials")
	globalSkill := &controlv1alpha1.AgentSkillSet{
		ObjectMeta: metav1.ObjectMeta{Name: "namespace-knowledge-skill", Namespace: "agents", UID: "skill-uid", Generation: 1},
		Spec: controlv1alpha1.AgentSkillSetSpec{
			Global: true,
			Skills: []controlv1alpha1.AgentRunSkillInjectionSpec{{Name: "knowledge-base", Content: "Search first."}},
		},
	}
	globalTool := &controlv1alpha1.AgentToolSet{
		ObjectMeta: metav1.ObjectMeta{Name: "namespace-knowledge-tool", Namespace: "agents", UID: "tool-uid", Generation: 1},
		Spec: controlv1alpha1.AgentToolSetSpec{
			Global: true,
			Tools:  []controlv1alpha1.AgentRunToolSpec{{Name: "knowledge-search", VerifyCommand: []string{"knowledge-search", "--help"}}},
		},
	}
	run := &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{Name: "isolated-1", Namespace: "agents"},
		Spec:       controlv1alpha1.AgentRunSpec{ProfileRef: &controlv1alpha1.NamespacedObjectReference{Name: profile.Name}},
	}
	reconciler := testCompositionReconciler(t, profile, codexHarness, globalSkill, globalTool)
	effective, resolution, phase, reason, message, err := reconciler.resolveAgentRunComposition(context.Background(), run)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if phase != "" || reason != "" || message != "" {
		t.Fatalf("block phase=%q reason=%q message=%q", phase, reason, message)
	}
	if len(effective.Spec.Harness.SkillInjections) != 0 || len(effective.Spec.Harness.Tools) != 0 {
		t.Fatalf("globals should be excluded, skills=%#v tools=%#v", effective.Spec.Harness.SkillInjections, effective.Spec.Harness.Tools)
	}
	if len(resolution.SkillSetRefs) != 0 || len(resolution.ToolSetRefs) != 0 {
		t.Fatalf("resolution refs should be empty, skills=%#v tools=%#v", resolution.SkillSetRefs, resolution.ToolSetRefs)
	}
}

func TestAgentRunCompositionRejectsRunLocalCredentialBootstrapEnvironment(t *testing.T) {
	t.Parallel()

	for _, name := range []string{
		"ANVIL_GITHUB_HOST",
		"ANVIL_GITHUB_APP_REPOSITORY_ID",
		"ANVIL_GITHUB_APP_PERMISSIONS_JSON",
		"ANVIL_AGENT_RUN_TIMEOUT_SECONDS",
		"ANVIL_AGENT_RUN_GH_CONFIG_DIR",
		"GITHUB_APP_PRIVATE_KEY",
		"GH_TOKEN",
	} {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			run := &controlv1alpha1.AgentRun{
				ObjectMeta: metav1.ObjectMeta{Name: "reserved-env", Namespace: "agents"},
				Spec: controlv1alpha1.AgentRunSpec{Harness: controlv1alpha1.AgentRunHarnessSpec{Execution: controlv1alpha1.AgentRunHarnessExecutionSpec{
					ExtraEnv: []corev1.EnvVar{{Name: name, Value: "run-controlled"}},
				}}},
			}
			_, _, phase, reason, _, err := testCompositionReconciler(t).resolveAgentRunComposition(context.Background(), run)
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if phase != controlv1alpha1.AgentRunPhaseFailed || reason != "ReservedCredentialBootstrapEnvironment" {
				t.Fatalf("block = phase:%q reason:%q", phase, reason)
			}
		})
	}
}

func testAgentCouncil(name, prompt string, members ...controlv1alpha1.AgentCouncilMemberSpec) *controlv1alpha1.AgentCouncil {
	if len(members) == 0 {
		members = []controlv1alpha1.AgentCouncilMemberSpec{{Role: "worker", ProfileRef: controlv1alpha1.NamespacedObjectReference{Name: "worker-profile"}}}
	}
	return &controlv1alpha1.AgentCouncil{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "agents", UID: typesUID(name + "-uid"), Generation: 3, ResourceVersion: "17"},
		Spec: controlv1alpha1.AgentCouncilSpec{
			Description: "test council", Members: members, CouncilPrompt: prompt,
		},
	}
}

func testCouncilMemberProfile(name string) *controlv1alpha1.AgentRunProfile {
	return &controlv1alpha1.AgentRunProfile{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "agents", UID: typesUID(name + "-uid"), Generation: 1}}
}

func TestAgentRunCompositionCouncilPromptHasDeterministicProvenanceAndNoAuthorityEscalation(t *testing.T) {
	t.Parallel()

	executingHarness := testAgentHarnessProfile("executing-runtime", controlv1alpha1.AgentRunHarnessBackendCodex, "executing-credentials")
	memberHarness := testAgentHarnessProfile("member-runtime", controlv1alpha1.AgentRunHarnessBackendPiAgent, "member-credentials")
	member := testCouncilMemberProfile("worker-profile")
	member.Spec.HarnessProfileRef = &controlv1alpha1.NamespacedObjectReference{Name: memberHarness.Name}
	member.Spec.Harness.Execution.ServiceAccountName = "privileged-member"
	member.Spec.Harness.Execution.EnvSecretRefs = []controlv1alpha1.NamespacedObjectReference{{Name: "member-secret"}}
	council := testAgentCouncil("repo-council", "Manager coordinates; workers remain within their own authority.")
	profile := &controlv1alpha1.AgentRunProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "executing-profile", Namespace: "agents", UID: "executing-profile-uid", Generation: 2},
		Spec: controlv1alpha1.AgentRunProfileSpec{
			HarnessProfileRef: &controlv1alpha1.NamespacedObjectReference{Name: executingHarness.Name},
			CouncilRef:        &controlv1alpha1.NamespacedObjectReference{Name: council.Name},
		},
	}
	run := &controlv1alpha1.AgentRun{ObjectMeta: metav1.ObjectMeta{Name: "team-1", Namespace: "agents"}, Spec: controlv1alpha1.AgentRunSpec{ProfileRef: &controlv1alpha1.NamespacedObjectReference{Name: profile.Name}}}
	reconciler := testCompositionReconciler(t, profile, executingHarness, memberHarness, member, council)

	first, firstStatus, phase, reason, message, err := reconciler.resolveAgentRunComposition(context.Background(), run)
	if err != nil || phase != "" {
		t.Fatalf("first resolve: phase=%q reason=%q message=%q err=%v", phase, reason, message, err)
	}
	second, secondStatus, phase, reason, message, err := reconciler.resolveAgentRunComposition(context.Background(), run.DeepCopy())
	if err != nil || phase != "" {
		t.Fatalf("second resolve: phase=%q reason=%q message=%q err=%v", phase, reason, message, err)
	}
	if firstStatus.CouncilRef == nil {
		t.Fatal("resolved council provenance is missing")
	}
	if got, want := firstStatus.CouncilRef.Name, council.Name; got != want {
		t.Fatalf("council name = %q, want %q", got, want)
	}
	if got, want := firstStatus.CouncilRef.UID, string(council.UID); got != want {
		t.Fatalf("council UID = %q, want %q", got, want)
	}
	if got, want := firstStatus.CouncilRef.Generation, council.Generation; got != want {
		t.Fatalf("council generation = %d, want %d", got, want)
	}
	if got, want := firstStatus.CouncilRef.ResourceVersion, council.ResourceVersion; got != want {
		t.Fatalf("council resourceVersion = %q, want %q", got, want)
	}
	if got, want := firstStatus.CouncilRef.Digest, digestJSON(council.Spec); got != want {
		t.Fatalf("council digest = %q, want %q", got, want)
	}
	if firstStatus.EffectiveDigest != secondStatus.EffectiveDigest || firstStatus.CouncilRef.Digest != secondStatus.CouncilRef.Digest || !reflect.DeepEqual(first.Spec, second.Spec) {
		t.Fatalf("council resolution is not deterministic: first=%#v second=%#v", firstStatus, secondStatus)
	}
	if got := first.Spec.Harness.Execution.ServiceAccountName; got != executingHarness.Spec.Execution.ServiceAccountName {
		t.Fatalf("member ServiceAccount escalated execution to %q", got)
	}
	for _, ref := range first.Spec.Harness.Execution.EnvSecretRefs {
		if ref.Name == "member-secret" {
			t.Fatalf("member credential leaked into executing run: %#v", first.Spec.Harness.Execution.EnvSecretRefs)
		}
	}
	foundCouncilSkill := false
	for _, skill := range first.Spec.Harness.SkillInjections {
		if skill.Name == "council-repo-council" {
			foundCouncilSkill = true
			if skill.Content != strings.TrimSpace(council.Spec.CouncilPrompt) {
				t.Fatalf("council prompt = %q, want %q", skill.Content, council.Spec.CouncilPrompt)
			}
		}
	}
	if !foundCouncilSkill {
		t.Fatalf("council skill missing from %#v", first.Spec.Harness.SkillInjections)
	}
}

func TestAgentRunCompositionCouncilAssociationIsExplicitAndRunRefOverridesProfile(t *testing.T) {
	t.Parallel()

	harness := testAgentHarnessProfile("runtime", controlv1alpha1.AgentRunHarnessBackendCodex, "creds")
	member := testCouncilMemberProfile("worker-profile")
	profileCouncil := testAgentCouncil("profile-council", "Profile guidance.")
	runCouncil := testAgentCouncil("run-council", "Run guidance.")
	profile := &controlv1alpha1.AgentRunProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "worker", Namespace: "agents"},
		Spec:       controlv1alpha1.AgentRunProfileSpec{HarnessProfileRef: &controlv1alpha1.NamespacedObjectReference{Name: harness.Name}, CouncilRef: &controlv1alpha1.NamespacedObjectReference{Name: profileCouncil.Name}},
	}

	withoutAssociation := &controlv1alpha1.AgentRun{ObjectMeta: metav1.ObjectMeta{Name: "solo", Namespace: "agents"}, Spec: controlv1alpha1.AgentRunSpec{HarnessProfileRef: &controlv1alpha1.NamespacedObjectReference{Name: harness.Name}}}
	_, status, phase, reason, message, err := testCompositionReconciler(t, harness, member, profileCouncil).resolveAgentRunComposition(context.Background(), withoutAssociation)
	if err != nil || phase != "" {
		t.Fatalf("unassociated resolve: phase=%q reason=%q message=%q err=%v", phase, reason, message, err)
	}
	if status.CouncilRef != nil {
		t.Fatalf("object presence selected a council: %#v", status.CouncilRef)
	}

	override := &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{Name: "override", Namespace: "agents"},
		Spec:       controlv1alpha1.AgentRunSpec{ProfileRef: &controlv1alpha1.NamespacedObjectReference{Name: profile.Name}, CouncilRef: &controlv1alpha1.NamespacedObjectReference{Name: runCouncil.Name}},
	}
	effective, status, phase, reason, message, err := testCompositionReconciler(t, harness, member, profile, profileCouncil, runCouncil).resolveAgentRunComposition(context.Background(), override)
	if err != nil || phase != "" {
		t.Fatalf("override resolve: phase=%q reason=%q message=%q err=%v", phase, reason, message, err)
	}
	if status.CouncilRef == nil || status.CouncilRef.Name != runCouncil.Name {
		t.Fatalf("run council did not override profile: %#v", status.CouncilRef)
	}
	for _, skill := range effective.Spec.Harness.SkillInjections {
		if skill.Name == "council-profile-council" {
			t.Fatalf("profile council prompt leaked through run override: %#v", skill)
		}
	}
}

func TestAgentRunCompositionCouncilWithoutPromptRecordsAssociationOnly(t *testing.T) {
	t.Parallel()

	harness := testAgentHarnessProfile("runtime", controlv1alpha1.AgentRunHarnessBackendCodex, "creds")
	member := testCouncilMemberProfile("worker-profile")
	council := testAgentCouncil("inventory-only", " \n\t ")
	run := &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{Name: "association", Namespace: "agents"},
		Spec: controlv1alpha1.AgentRunSpec{
			HarnessProfileRef: &controlv1alpha1.NamespacedObjectReference{Name: harness.Name},
			CouncilRef:        &controlv1alpha1.NamespacedObjectReference{Name: council.Name},
		},
	}
	effective, status, phase, reason, message, err := testCompositionReconciler(t, harness, member, council).resolveAgentRunComposition(context.Background(), run)
	if err != nil || phase != "" {
		t.Fatalf("resolve: phase=%q reason=%q message=%q err=%v", phase, reason, message, err)
	}
	if status.CouncilRef == nil || status.CouncilRef.Name != council.Name {
		t.Fatalf("association evidence = %#v", status.CouncilRef)
	}
	for _, skill := range effective.Spec.Harness.SkillInjections {
		if strings.HasPrefix(skill.Name, "council-") {
			t.Fatalf("whitespace prompt injected a skill: %#v", skill)
		}
	}
}

func TestAgentRunCompositionCouncilValidationFailsClosed(t *testing.T) {
	t.Parallel()

	harness := testAgentHarnessProfile("runtime", controlv1alpha1.AgentRunHarnessBackendCodex, "creds")
	member := testCouncilMemberProfile("worker-profile")
	manyMembers := make([]controlv1alpha1.AgentCouncilMemberSpec, 33)
	for i := range manyMembers {
		manyMembers[i] = controlv1alpha1.AgentCouncilMemberSpec{Role: "worker", ProfileRef: controlv1alpha1.NamespacedObjectReference{Name: fmt.Sprintf("worker-%02d", i)}}
	}
	tests := []struct {
		name       string
		councilRef *controlv1alpha1.NamespacedObjectReference
		council    *controlv1alpha1.AgentCouncil
		wantPhase  controlv1alpha1.AgentRunPhase
		wantReason string
	}{
		{name: "empty council ref", councilRef: &controlv1alpha1.NamespacedObjectReference{Name: "  "}, wantPhase: controlv1alpha1.AgentRunPhaseFailed, wantReason: "InvalidCouncilRef"},
		{name: "cross namespace council", councilRef: &controlv1alpha1.NamespacedObjectReference{Name: "other", Namespace: "other"}, wantPhase: controlv1alpha1.AgentRunPhaseFailed, wantReason: "CrossNamespaceCouncilRef"},
		{name: "missing council", councilRef: &controlv1alpha1.NamespacedObjectReference{Name: "missing"}, wantPhase: controlv1alpha1.AgentRunPhaseNeedsHuman, wantReason: "CouncilNotFound"},
		{name: "zero members", council: &controlv1alpha1.AgentCouncil{ObjectMeta: metav1.ObjectMeta{Name: "zero", Namespace: "agents"}}, wantPhase: controlv1alpha1.AgentRunPhaseFailed, wantReason: "InvalidCouncilMembers"},
		{name: "too many members", council: testAgentCouncil("many", "prompt", manyMembers...), wantPhase: controlv1alpha1.AgentRunPhaseFailed, wantReason: "InvalidCouncilMembers"},
		{name: "empty role", council: testAgentCouncil("bad-role", "prompt", controlv1alpha1.AgentCouncilMemberSpec{Role: " ", ProfileRef: controlv1alpha1.NamespacedObjectReference{Name: member.Name}}), wantPhase: controlv1alpha1.AgentRunPhaseFailed, wantReason: "InvalidCouncilMemberRole"},
		{name: "empty member profile ref", council: testAgentCouncil("bad-ref", "prompt", controlv1alpha1.AgentCouncilMemberSpec{Role: "worker", ProfileRef: controlv1alpha1.NamespacedObjectReference{Name: " "}}), wantPhase: controlv1alpha1.AgentRunPhaseFailed, wantReason: "InvalidCouncilMemberProfileRef"},
		{name: "cross namespace member profile ref", council: testAgentCouncil("cross-member", "prompt", controlv1alpha1.AgentCouncilMemberSpec{Role: "worker", ProfileRef: controlv1alpha1.NamespacedObjectReference{Name: member.Name, Namespace: "other"}}), wantPhase: controlv1alpha1.AgentRunPhaseFailed, wantReason: "CrossNamespaceCouncilMemberProfileRef"},
		{name: "duplicate member profile", council: testAgentCouncil("duplicate", "prompt", controlv1alpha1.AgentCouncilMemberSpec{Role: "worker", ProfileRef: controlv1alpha1.NamespacedObjectReference{Name: member.Name}}, controlv1alpha1.AgentCouncilMemberSpec{Role: "auditor", ProfileRef: controlv1alpha1.NamespacedObjectReference{Name: member.Name}}), wantPhase: controlv1alpha1.AgentRunPhaseFailed, wantReason: "DuplicateCouncilMemberProfile"},
		{name: "missing member profile object", council: testAgentCouncil("missing-member", "prompt", controlv1alpha1.AgentCouncilMemberSpec{Role: "worker", ProfileRef: controlv1alpha1.NamespacedObjectReference{Name: "does-not-exist"}}), wantPhase: controlv1alpha1.AgentRunPhaseNeedsHuman, wantReason: "CouncilMemberProfileNotFound"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ref := tt.councilRef
			objects := []client.Object{harness, member}
			if tt.council != nil {
				ref = &controlv1alpha1.NamespacedObjectReference{Name: tt.council.Name}
				objects = append(objects, tt.council)
			}
			run := &controlv1alpha1.AgentRun{ObjectMeta: metav1.ObjectMeta{Name: "validate", Namespace: "agents"}, Spec: controlv1alpha1.AgentRunSpec{HarnessProfileRef: &controlv1alpha1.NamespacedObjectReference{Name: harness.Name}, CouncilRef: ref}}
			_, _, phase, reason, message, err := testCompositionReconciler(t, objects...).resolveAgentRunComposition(context.Background(), run)
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if phase != tt.wantPhase || reason != tt.wantReason {
				t.Fatalf("block = phase:%q reason:%q message:%q, want phase:%q reason:%q", phase, reason, message, tt.wantPhase, tt.wantReason)
			}
		})
	}
}

func TestAgentRunCompositionCouncilPromptCannotBeShadowed(t *testing.T) {
	t.Parallel()

	harness := testAgentHarnessProfile("runtime", controlv1alpha1.AgentRunHarnessBackendCodex, "creds")
	member := testCouncilMemberProfile("worker-profile")
	council := testAgentCouncil("repo-council", "Authoritative council guidance.")
	run := &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{Name: "collision", Namespace: "agents"},
		Spec: controlv1alpha1.AgentRunSpec{
			HarnessProfileRef: &controlv1alpha1.NamespacedObjectReference{Name: harness.Name},
			CouncilRef:        &controlv1alpha1.NamespacedObjectReference{Name: council.Name},
			Harness:           controlv1alpha1.AgentRunHarnessSpec{SkillInjections: []controlv1alpha1.AgentRunSkillInjectionSpec{{Name: "council-repo-council", Content: "Shadow text."}}},
		},
	}
	_, _, phase, reason, message, err := testCompositionReconciler(t, harness, member, council).resolveAgentRunComposition(context.Background(), run)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if phase != controlv1alpha1.AgentRunPhaseFailed || reason != "CouncilSkillNameConflict" {
		t.Fatalf("block = phase:%q reason:%q message:%q", phase, reason, message)
	}
}

func TestAgentCouncilPromptLimitMatchesOpenAPIRuneCount(t *testing.T) {
	t.Parallel()

	council := testAgentCouncil("unicode-boundary", strings.Repeat("界", 65536))
	if reason, message := validateAgentCouncilShape(council); reason != "" {
		t.Fatalf("valid multibyte prompt rejected: reason=%q message=%q", reason, message)
	}
	council.Spec.CouncilPrompt += "界"
	if reason, _ := validateAgentCouncilShape(council); reason != "CouncilPromptTooLarge" {
		t.Fatalf("oversized multibyte prompt reason = %q, want CouncilPromptTooLarge", reason)
	}
}
