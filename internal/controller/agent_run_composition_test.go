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
			Tools:     []controlv1alpha1.AgentRunToolSpec{{Name: "knowledge-search", VerifyCommand: []string{"knowledge-search", "--help"}}},
			Subagents: []controlv1alpha1.AgentRunSubagentSpec{{Name: "correctness-reviewer", When: "Shared behavior changes."}},
		},
	}
	runSet := &controlv1alpha1.AgentSkillSet{
		ObjectMeta: metav1.ObjectMeta{Name: "incident-review", Namespace: "agents", UID: "incident-review-uid", Generation: 2},
		Spec: controlv1alpha1.AgentSkillSetSpec{
			Skills: []controlv1alpha1.AgentRunSkillInjectionSpec{
				{Name: "evidence-review", Content: "Review the incident evidence."},
				{Name: "timeline", Content: "Build an incident timeline."},
			},
			Tools:     append([]controlv1alpha1.AgentRunToolSpec(nil), baseSet.Spec.Tools...),
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

	reconciler := testCompositionReconciler(t, profile, codexHarness, piHarness, baseSet, runSet)
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
	if len(effective.Spec.Harness.Tools) != 1 || len(effective.Spec.Harness.Subagents) != 1 {
		t.Fatalf("resolved tools/subagents = %#v / %#v", effective.Spec.Harness.Tools, effective.Spec.Harness.Subagents)
	}
	if resolution == nil || resolution.ProfileRef == nil || resolution.HarnessProfileRef == nil || len(resolution.SkillSetRefs) != 2 {
		t.Fatalf("resolution status = %#v", resolution)
	}
	if resolution.HarnessProfileRef.Name != "pi-standard" || resolution.EffectiveDigest == "" || resolution.SkillSetRefs[0].Digest == "" {
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
