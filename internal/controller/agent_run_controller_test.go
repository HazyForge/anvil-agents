package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	controlv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
	"github.com/hazyforge/anvil-agents/internal/archive"
)

type recordingAgentRunArchiveStore struct {
	records []archive.AgentRunArchiveRecord
	err     error
}

func (s *recordingAgentRunArchiveStore) ArchiveAgentRun(ctx context.Context, record archive.AgentRunArchiveRecord) (archive.AgentRunArchiveResult, error) {
	if err := ctx.Err(); err != nil {
		return archive.AgentRunArchiveResult{}, err
	}
	if s.err != nil {
		return archive.AgentRunArchiveResult{}, s.err
	}
	s.records = append(s.records, record)
	return archive.AgentRunArchiveResult{Store: archive.AgentRunArchiveStorePostgres, ArchivedAt: record.ArchivedAt, Digest: record.Digest}, nil
}

func (s *recordingAgentRunArchiveStore) Close() {}

func TestAgentRunChildNameHandlesOversizedFinalToken(t *testing.T) {
	t.Parallel()

	longSessionName := "codex-reauth-hazy-trade-docs-code-steward-codex-home-1785363423"
	fullName := "auth-" + longSessionName
	got := agentRunChildName("auth", longSessionName)
	if len(got) > 63 {
		t.Fatalf("name length = %d, want at most 63: %q", len(got), got)
	}
	if !strings.HasSuffix(got, "-"+shortHash(fullName)) {
		t.Fatalf("name = %q, want deterministic hash suffix %q", got, shortHash(fullName))
	}
	if got != agentRunChildName("auth", longSessionName) {
		t.Fatalf("name is not deterministic: %q", got)
	}
	if problems := validation.IsDNS1123Label(got); len(problems) != 0 {
		t.Fatalf("name = %q is not a DNS-1123 label: %v", got, problems)
	}
}

func TestAgentRunChildNameFinalTokenBoundaries(t *testing.T) {
	t.Parallel()

	for _, suffixLength := range []int{61, 62, 63, 64} {
		suffixLength := suffixLength
		t.Run(fmt.Sprintf("suffix-%d", suffixLength), func(t *testing.T) {
			suffix := strings.Repeat("s", suffixLength)
			got := agentRunChildName("auth", suffix)
			if len(got) > 63 {
				t.Fatalf("name length = %d, want at most 63: %q", len(got), got)
			}
			if problems := validation.IsDNS1123Label(got); len(problems) != 0 {
				t.Fatalf("name = %q is not a DNS-1123 label: %v", got, problems)
			}
			if suffixLength == 62 && got != suffix {
				t.Fatalf("62-character legacy suffix = %q, want unchanged %q", got, suffix)
			}
			if suffixLength >= 63 && !strings.HasSuffix(got, "-"+shortHash("auth-"+suffix)) {
				t.Fatalf("name = %q, want deterministic hash suffix", got)
			}
		})
	}
}

func TestAgentRunChildNamePreservesShortFinalToken(t *testing.T) {
	t.Parallel()

	got := agentRunChildName(strings.Repeat("long-prefix-", 8), "prompt-hash")
	if len(got) > 63 {
		t.Fatalf("name length = %d, want at most 63: %q", len(got), got)
	}
	if !strings.HasSuffix(got, "-prompt-hash") {
		t.Fatalf("name = %q, want preserved final token", got)
	}
}

func TestAgentRunStatusReportsFromOutputAppliesDecision(t *testing.T) {
	t.Parallel()

	output := strings.Join([]string{
		"ordinary harness output",
		`ANVIL_AGENT_RUN_STATUS_JSON={"type":"progress","observedAt":"2026-07-07T21:00:00Z","stage":"inspect-source","summary":"Read the source object."}`,
		`ANVIL_AGENT_RUN_STATUS_JSON={"type":"decision","classification":"durable code or GitOps defect","action":"proposeChange","summary":"Opened a pull request.","pullRequestURL":"https://github.com/HazyForge/anvil-primaris/pull/271","residualRisk":"Needs review.","needsHuman":true,"humanFollowUp":"Review and merge the PR."}`,
	}, "\n")

	reports := agentRunStatusReportsFromOutput(output)
	if len(reports) != 2 {
		t.Fatalf("reports = %d, want 2: %#v", len(reports), reports)
	}
	if reports[0].ObservedAt == nil {
		t.Fatalf("first report observedAt is nil")
	}
	if got, want := reports[0].Stage, "inspect-source"; got != want {
		t.Fatalf("first report stage = %q, want %q", got, want)
	}

	status := controlv1alpha1.AgentRunStatus{}
	agentRunApplyStatusReports(&status, reports)
	if status.Decision == nil {
		t.Fatalf("status.Decision is nil")
	}
	if got, want := status.Decision.Classification, "durable code or GitOps defect"; got != want {
		t.Fatalf("decision classification = %q, want %q", got, want)
	}
	if got, want := status.Decision.Action, "proposeChange"; got != want {
		t.Fatalf("decision action = %q, want %q", got, want)
	}
	if got, want := status.PullRequestURL, "https://github.com/HazyForge/anvil-primaris/pull/271"; got != want {
		t.Fatalf("pull request url = %q, want %q", got, want)
	}
	if !agentRunReportsNeedHuman(status.Reports) {
		t.Fatalf("reports should require human follow-up")
	}
	if got, want := agentRunLatestHumanFollowUp(status.Reports), "Review and merge the PR."; got != want {
		t.Fatalf("latest human follow-up = %q, want %q", got, want)
	}

	raw := agentRunRawResult(output, status.PullRequestURL, status.Decision, status.Reports)
	var result struct {
		PullRequestURL string                                  `json:"pullRequestURL"`
		Decision       *controlv1alpha1.AgentRunDecisionStatus `json:"decision"`
		Reports        []controlv1alpha1.AgentRunStatusReport  `json:"reports"`
	}
	if err := json.Unmarshal(raw.Raw, &result); err != nil {
		t.Fatalf("unmarshal raw result: %v", err)
	}
	if result.Decision == nil || result.Decision.Action != "proposeChange" {
		t.Fatalf("raw result decision = %#v, want proposeChange", result.Decision)
	}
	if len(result.Reports) != 2 {
		t.Fatalf("raw result reports = %d, want 2", len(result.Reports))
	}
}

func TestAgentRunArchivesTerminalRun(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := controlv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add control scheme: %v", err)
	}
	completedAt := metav1.NewTime(time.Now().UTC().Add(-time.Hour))
	run := &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "archive-me",
			Namespace:         "anvilhub",
			UID:               types.UID("archive-me-uid"),
			ResourceVersion:   "12",
			CreationTimestamp: metav1.NewTime(completedAt.Add(-time.Hour)),
		},
		Spec: controlv1alpha1.AgentRunSpec{
			SourceRef: controlv1alpha1.AgentRunSourceRef{Kind: "AgentSchedule", Name: "manager"},
			IssueTracking: &controlv1alpha1.AgentRunIssueTrackingSpec{
				Provider:   controlv1alpha1.AgentRunIssueTrackingProviderGitHub,
				Repository: "HazyForge/anvil-primaris",
				Issues:     []controlv1alpha1.AgentRunIssueRef{{Number: 415}},
			},
		},
		Status: controlv1alpha1.AgentRunStatus{
			Phase:          controlv1alpha1.AgentRunPhaseNeedsHuman,
			Backend:        "codex",
			Intent:         "proposeChange",
			CompletedAt:    &completedAt,
			PullRequestURL: "https://github.com/HazyForge/anvil-primaris/pull/415",
			Decision:       &controlv1alpha1.AgentRunDecisionStatus{Classification: "missing human input", Summary: "needs approval"},
			Reports:        []controlv1alpha1.AgentRunStatusReport{{Type: "decision", Summary: "needs approval", NeedsHuman: true}},
			Output:         "bounded output",
		},
	}
	store := &recordingAgentRunArchiveStore{}
	reconciler := &AgentRunReconciler{
		Client:          fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&controlv1alpha1.AgentRun{}).WithObjects(run).Build(),
		Scheme:          scheme,
		AgentRunArchive: store,
	}

	result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: run.Namespace, Name: run.Name}})
	if err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}
	if result.Requeue {
		t.Fatalf("Reconcile requeue = true, want false")
	}
	if len(store.records) != 1 {
		t.Fatalf("archived records = %d, want 1", len(store.records))
	}
	if got := string(store.records[0].Spec); !strings.Contains(got, `"issueTracking"`) || !strings.Contains(got, `"number":415`) {
		t.Fatalf("archive spec missing issue context: %s", got)
	}
	updated := &controlv1alpha1.AgentRun{}
	if err := reconciler.Get(ctx, types.NamespacedName{Namespace: run.Namespace, Name: run.Name}, updated); err != nil {
		t.Fatalf("get updated run: %v", err)
	}
	if updated.Status.Archive == nil || updated.Status.Archive.ArchivedAt == nil || updated.Status.Archive.Digest == "" {
		t.Fatalf("archive status not recorded: %#v", updated.Status.Archive)
	}
	if updated.Status.Archive.Error != "" {
		t.Fatalf("archive status error = %q", updated.Status.Archive.Error)
	}
}

func TestAgentRunArchivesTerminalRunAndRequeuesUntilRetention(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := controlv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add control scheme: %v", err)
	}
	completedAt := metav1.NewTime(time.Now().UTC().Add(-time.Hour))
	run := &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "archive-then-prune",
			Namespace:         "anvilhub",
			UID:               types.UID("archive-then-prune-uid"),
			ResourceVersion:   "12",
			CreationTimestamp: metav1.NewTime(completedAt.Add(-time.Hour)),
		},
		Status: controlv1alpha1.AgentRunStatus{
			Phase:       controlv1alpha1.AgentRunPhaseSucceeded,
			CompletedAt: &completedAt,
		},
	}
	store := &recordingAgentRunArchiveStore{}
	reconciler := &AgentRunReconciler{
		Client:          fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&controlv1alpha1.AgentRun{}).WithObjects(run).Build(),
		Scheme:          scheme,
		AgentRunArchive: store,
		CommonReconcilerOptions: CommonReconcilerOptions{
			Options: &Options{AgentRunTerminalRetention: 24 * time.Hour},
		},
	}

	result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: run.Namespace, Name: run.Name}})
	if err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}
	if result.Requeue {
		t.Fatalf("Reconcile requeue = true, want timed requeue")
	}
	if result.RequeueAfter <= 22*time.Hour || result.RequeueAfter > 24*time.Hour {
		t.Fatalf("RequeueAfter = %s, want remaining retention window", result.RequeueAfter)
	}
}

func TestAgentRunPrunesOnlyArchivedTerminalRunAfterRetention(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := controlv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add control scheme: %v", err)
	}
	completedAt := metav1.NewTime(time.Now().UTC().Add(-49 * time.Hour))
	archivedAt := metav1.NewTime(time.Now().UTC().Add(-48 * time.Hour))
	run := &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "prune-me",
			Namespace:         "anvilhub",
			UID:               types.UID("prune-me-uid"),
			CreationTimestamp: metav1.NewTime(completedAt.Add(-time.Hour)),
		},
		Status: controlv1alpha1.AgentRunStatus{
			Phase:       controlv1alpha1.AgentRunPhaseSucceeded,
			CompletedAt: &completedAt,
			Archive: &controlv1alpha1.AgentRunArchiveStatus{
				Store:      archive.AgentRunArchiveStorePostgres,
				ArchivedAt: &archivedAt,
				Digest:     "sha256:abc",
			},
		},
	}
	reconciler := &AgentRunReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(run).Build(),
		Scheme: scheme,
		CommonReconcilerOptions: CommonReconcilerOptions{
			Options: &Options{AgentRunTerminalRetention: 24 * time.Hour},
		},
	}

	if _, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: run.Namespace, Name: run.Name}}); err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}
	current := &controlv1alpha1.AgentRun{}
	err := reconciler.Get(ctx, types.NamespacedName{Namespace: run.Namespace, Name: run.Name}, current)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("get pruned run error = %v, want not found", err)
	}
}

func TestAgentRunRequeuesArchivedTerminalRunUntilRetention(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := controlv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add control scheme: %v", err)
	}
	completedAt := metav1.NewTime(time.Now().UTC().Add(-time.Hour))
	archivedAt := metav1.NewTime(time.Now().UTC().Add(-30 * time.Minute))
	run := &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "requeue-me",
			Namespace:         "anvilhub",
			UID:               types.UID("requeue-me-uid"),
			CreationTimestamp: metav1.NewTime(completedAt.Add(-time.Hour)),
		},
		Status: controlv1alpha1.AgentRunStatus{
			Phase:       controlv1alpha1.AgentRunPhaseSucceeded,
			CompletedAt: &completedAt,
			Archive: &controlv1alpha1.AgentRunArchiveStatus{
				Store:      archive.AgentRunArchiveStorePostgres,
				ArchivedAt: &archivedAt,
				Digest:     "sha256:abc",
			},
		},
	}
	reconciler := &AgentRunReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(run).Build(),
		Scheme: scheme,
		CommonReconcilerOptions: CommonReconcilerOptions{
			Options: &Options{AgentRunTerminalRetention: 24 * time.Hour},
		},
	}

	result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: run.Namespace, Name: run.Name}})
	if err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}
	if result.Requeue {
		t.Fatalf("Reconcile requeue = true, want timed requeue")
	}
	if result.RequeueAfter <= 22*time.Hour || result.RequeueAfter > 24*time.Hour {
		t.Fatalf("RequeueAfter = %s, want remaining retention window", result.RequeueAfter)
	}
	current := &controlv1alpha1.AgentRun{}
	if err := reconciler.Get(ctx, types.NamespacedName{Namespace: run.Namespace, Name: run.Name}, current); err != nil {
		t.Fatalf("get retained run: %v", err)
	}
}

func TestBuildAgentRunPromptIncludesDocsAndIssueTrackingPolicy(t *testing.T) {
	t.Parallel()

	run := &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "docs-and-issues",
			Namespace: "anvil",
		},
		Spec: controlv1alpha1.AgentRunSpec{
			Prompt: "Inspect this run's target issue and apply the smallest safe fix.",
			Docs: &controlv1alpha1.AgentRunDocsSpec{
				Policy:       controlv1alpha1.AgentRunDocsPolicyRequired,
				Paths:        []string{"docs/agent-run.md"},
				RuntimePaths: []string{"api/v1alpha1/agent_run_types.go"},
			},
			IssueTracking: &controlv1alpha1.AgentRunIssueTrackingSpec{
				Provider:     controlv1alpha1.AgentRunIssueTrackingProviderGitHub,
				Repository:   "example/service",
				UpdatePolicy: controlv1alpha1.AgentRunIssueUpdatePolicyComment,
				Issues:       []controlv1alpha1.AgentRunIssueRef{{Number: 271}},
			},
			Harness: controlv1alpha1.AgentRunHarnessSpec{
				SkillInjections: []controlv1alpha1.AgentRunSkillInjectionSpec{{
					Name:        "system-rounds",
					Description: "Check the declared scope and observability.",
					Paths:       []string{"docs/operations.md"},
					Content:     "Inspect logs, metrics, traces, docs, and current resource status.",
				}},
			},
		},
	}

	prompt := buildAgentRunPrompt(run)
	expected := []string{
		"immutable image prompt is authoritative",
		"Treat application references as opaque scope metadata",
		"any external control plane or delivery API",
		"Honor `spec.docs.policy`",
		"current source, API objects, logs, metrics, traces",
		"When `spec.issueTracking` is present",
		"Follow `updatePolicy` exactly",
		"ANVIL_AGENT_RUN_PLATFORM_REPOSITORY",
		"Write structured status",
		"example/service",
		"Run prompt:",
		"Inspect this run's target issue and apply the smallest safe fix.",
		"\"docs\"",
		"\"issueTracking\"",
		"\"prompt\"",
		"\"skillInjections\"",
	}
	for _, want := range expected {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}

	emptyPrompt := buildAgentRunPrompt(&controlv1alpha1.AgentRun{})
	if strings.Contains(emptyPrompt, `"issueTracking"`) {
		t.Fatalf("empty prompt should not contain an issueTracking object:\n%s", emptyPrompt)
	}
	if strings.Contains(emptyPrompt, `"prompt"`) {
		t.Fatalf("empty prompt should not contain a prompt field:\n%s", emptyPrompt)
	}
}

func TestAgentRunProfileResolvesEffectiveSpec(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := controlv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add control scheme: %v", err)
	}

	profile := &controlv1alpha1.AgentRunProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hazy-trade-prod-health",
			Namespace: "hazy-trade",
		},
		Spec: controlv1alpha1.AgentRunProfileSpec{
			Scope: controlv1alpha1.AgentRunScopeSpec{
				Summary: "Hazy Trade production health",
				ApplicationRef: &controlv1alpha1.ApplicationReferenceSpec{
					Name: "hazy-trade",
				},
				ApplicationTargetRef: &controlv1alpha1.ApplicationTargetReferenceSpec{
					Name: "hazy-trade-prod",
				},
				Namespaces:    []string{"hazy-trade"},
				ResourceKinds: []string{"Deployment", "Pod"},
			},
			Docs: &controlv1alpha1.AgentRunDocsSpec{
				Policy: controlv1alpha1.AgentRunDocsPolicyReview,
				Paths:  []string{"docs/agent-run.md"},
			},
			IssueTracking: &controlv1alpha1.AgentRunIssueTrackingSpec{
				Provider:     controlv1alpha1.AgentRunIssueTrackingProviderGitHub,
				Repository:   "HazyForge/hazy-trade",
				UpdatePolicy: controlv1alpha1.AgentRunIssueUpdatePolicyReadOnly,
			},
			Harness: controlv1alpha1.AgentRunHarnessSpec{
				Intent: controlv1alpha1.AgentRunIntentObserve,
				Backend: controlv1alpha1.AgentRunHarnessBackendSpec{
					Kind: controlv1alpha1.AgentRunHarnessBackendCodex,
					Codex: &controlv1alpha1.AgentRunCodexBackendSpec{
						Model:           "gpt-5.4",
						ReasoningEffort: "high",
						Verbosity:       "high",
						ServiceTier:     "priority",
						Sandbox:         "read-only",
					},
				},
				SkillInjections: []controlv1alpha1.AgentRunSkillInjectionSpec{{
					Name:    "hazy-trade-runtime-health",
					Content: "Inspect logs, metrics, traces, and Hazy Trade runtime state.",
				}},
				Subagents: []controlv1alpha1.AgentRunSubagentSpec{{
					Name:         "github-issue-hygiene",
					ToolNames:    []string{"gh", "anvil-hotline"},
					SystemPrompt: "Close stale tickets only when current evidence proves they are resolved.",
				}},
				Tools: []controlv1alpha1.AgentRunToolSpec{{
					Name:          "hazytradectl",
					VerifyCommand: []string{"hazytradectl", "--version"},
				}},
				SystemPrompt: "Profile prompt.",
				Execution: controlv1alpha1.AgentRunHarnessExecutionSpec{
					ServiceAccountName: "hazy-trade-agent-run",
					EnvSecretRefs:      []controlv1alpha1.NamespacedObjectReference{{Name: "codex-credentials"}},
					ExtraEnv: []corev1.EnvVar{{
						Name:  "ANVIL_AGENT_RUN_REPOSITORY_REF",
						Value: "master",
					}},
					DataVolumeRefs: []controlv1alpha1.AgentRunDataVolumeRef{{Name: "hazy-trade-codex-home"}},
				},
			},
		},
	}
	run := &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "release-failure",
			Namespace: "hazy-trade",
		},
		Spec: controlv1alpha1.AgentRunSpec{
			ProfileRef: &controlv1alpha1.NamespacedObjectReference{Name: "hazy-trade-prod-health"},
			Prompt:     "Investigate the failed release gate and propose a bounded fix.",
			Scope: controlv1alpha1.AgentRunScopeSpec{
				Namespaces:    []string{"hazy-trade-workers"},
				ResourceKinds: []string{"Pipeline"},
			},
			Docs: &controlv1alpha1.AgentRunDocsSpec{
				Policy: controlv1alpha1.AgentRunDocsPolicyRequired,
				Paths:  []string{"docs/release.md"},
			},
			IssueTracking: &controlv1alpha1.AgentRunIssueTrackingSpec{
				UpdatePolicy: controlv1alpha1.AgentRunIssueUpdatePolicyComment,
				Issues:       []controlv1alpha1.AgentRunIssueRef{{Number: 42}},
			},
			Harness: controlv1alpha1.AgentRunHarnessSpec{
				Backend: controlv1alpha1.AgentRunHarnessBackendSpec{
					Codex: &controlv1alpha1.AgentRunCodexBackendSpec{
						Model:           "gpt-5.5",
						ReasoningEffort: "xhigh",
						Verbosity:       "low",
						ServiceTier:     "default",
					},
				},
				SkillInjections: []controlv1alpha1.AgentRunSkillInjectionSpec{{
					Name:    "release-gate-failure",
					Content: "Focus on the failed release gate.",
				}},
				SystemPrompt: "Run-local prompt.",
				Execution: controlv1alpha1.AgentRunHarnessExecutionSpec{
					ExtraEnv: []corev1.EnvVar{{
						Name:  "ANVIL_AGENT_RUN_REPOSITORY_REF",
						Value: "release-candidate",
					}},
				},
			},
		},
	}
	reconciler := &AgentRunReconciler{
		Client: fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(profile).
			Build(),
		Scheme: scheme,
	}

	effective, phase, reason, message, err := reconciler.resolveAgentRunProfile(ctx, run)
	if err != nil {
		t.Fatalf("resolve profile: %v", err)
	}
	if phase != "" || reason != "" || message != "" {
		t.Fatalf("resolve profile returned phase=%q reason=%q message=%q", phase, reason, message)
	}
	if got, want := effective.Spec.Scope.ApplicationRef.Name, "hazy-trade"; got != want {
		t.Fatalf("application ref = %q, want %q", got, want)
	}
	if got, want := effective.Spec.Scope.ApplicationTargetRef.Name, "hazy-trade-prod"; got != want {
		t.Fatalf("application target ref = %q, want %q", got, want)
	}
	if got, want := effective.Spec.Prompt, "Investigate the failed release gate and propose a bounded fix."; got != want {
		t.Fatalf("prompt = %q, want %q", got, want)
	}
	if got, want := strings.Join(effective.Spec.Scope.Namespaces, ","), "hazy-trade,hazy-trade-workers"; got != want {
		t.Fatalf("namespaces = %q, want %q", got, want)
	}
	if got, want := effective.Spec.Docs.Policy, controlv1alpha1.AgentRunDocsPolicyRequired; got != want {
		t.Fatalf("docs policy = %q, want %q", got, want)
	}
	if got, want := strings.Join(effective.Spec.Docs.Paths, ","), "docs/agent-run.md,docs/release.md"; got != want {
		t.Fatalf("docs paths = %q, want %q", got, want)
	}
	if got, want := effective.Spec.IssueTracking.UpdatePolicy, controlv1alpha1.AgentRunIssueUpdatePolicyComment; got != want {
		t.Fatalf("issue update policy = %q, want %q", got, want)
	}
	if got, want := effective.Spec.Harness.Execution.ServiceAccountName, "hazy-trade-agent-run"; got != want {
		t.Fatalf("service account = %q, want %q", got, want)
	}
	if got, want := len(effective.Spec.Harness.SkillInjections), 2; got != want {
		t.Fatalf("skill injections = %d, want %d", got, want)
	}
	if got, want := len(effective.Spec.Harness.Subagents), 1; got != want {
		t.Fatalf("subagents = %d, want %d", got, want)
	}
	if got, want := len(effective.Spec.Harness.Tools), 1; got != want {
		t.Fatalf("tools = %d, want %d", got, want)
	}
	if !strings.Contains(effective.Spec.Harness.SystemPrompt, "Profile prompt.\n\nRun-local prompt.") {
		t.Fatalf("system prompt was not merged:\n%s", effective.Spec.Harness.SystemPrompt)
	}
	if got, want := effective.Spec.Harness.Backend.Codex.Model, "gpt-5.5"; got != want {
		t.Fatalf("codex model = %q, want %q", got, want)
	}
	if got, want := effective.Spec.Harness.Backend.Codex.ReasoningEffort, "xhigh"; got != want {
		t.Fatalf("codex reasoning effort = %q, want %q", got, want)
	}
	if got, want := effective.Spec.Harness.Backend.Codex.Verbosity, "low"; got != want {
		t.Fatalf("codex verbosity = %q, want %q", got, want)
	}
	if got, want := effective.Spec.Harness.Backend.Codex.ServiceTier, "default"; got != want {
		t.Fatalf("codex service tier = %q, want %q", got, want)
	}

	job := agentRunJob(effective, "release-failure-harness", "release-failure-context", nil)
	env := map[string]string{}
	for _, item := range job.Spec.Template.Spec.Containers[0].Env {
		env[item.Name] = item.Value
	}
	if got, want := env["ANVIL_AGENT_RUN_PROFILE_NAME"], "hazy-trade-prod-health"; got != want {
		t.Fatalf("profile env = %q, want %q", got, want)
	}
	if got, want := env["ANVIL_AGENT_RUN_REPOSITORY_REF"], "release-candidate"; got != want {
		t.Fatalf("repository ref env = %q, want %q", got, want)
	}
	if got, want := env["ANVIL_AGENT_RUN_PLATFORM_REPOSITORY"], agentRunPlatformRepository; got != want {
		t.Fatalf("platform repository env = %q, want %q", got, want)
	}
	if got, want := env["ANVIL_AGENT_RUN_PLATFORM_REPOSITORY_URL"], agentRunPlatformRepositoryURL; got != want {
		t.Fatalf("platform repository url env = %q, want %q", got, want)
	}
	if got := env["ANVIL_AGENT_RUN_PLATFORM_DOCS"]; !strings.Contains(got, "docs/agent-run.md") || !strings.Contains(got, "agent_run_controller.go") {
		t.Fatalf("platform docs env = %q, want agent run docs/runtime paths", got)
	}
	if got, want := env["ANVIL_CODEX_MODEL"], "gpt-5.5"; got != want {
		t.Fatalf("codex model env = %q, want %q", got, want)
	}
	if got, want := env["ANVIL_CODEX_REASONING_EFFORT"], "xhigh"; got != want {
		t.Fatalf("codex reasoning effort env = %q, want %q", got, want)
	}
	if got, want := env["ANVIL_CODEX_VERBOSITY"], "low"; got != want {
		t.Fatalf("codex verbosity env = %q, want %q", got, want)
	}
	if got, want := env["ANVIL_CODEX_SERVICE_TIER"], "default"; got != want {
		t.Fatalf("codex service tier env = %q, want %q", got, want)
	}
}

func TestAgentRunProfileRefMustStayInRunNamespace(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := controlv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add control scheme: %v", err)
	}
	run := &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cross-namespace",
			Namespace: "hazy-trade",
		},
		Spec: controlv1alpha1.AgentRunSpec{
			ProfileRef: &controlv1alpha1.NamespacedObjectReference{
				Name:      "platform-health",
				Namespace: "anvilhub",
			},
		},
	}
	reconciler := &AgentRunReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).Build(),
		Scheme: scheme,
	}

	_, phase, reason, _, err := reconciler.resolveAgentRunProfile(ctx, run)
	if err != nil {
		t.Fatalf("resolve profile: %v", err)
	}
	if got, want := phase, controlv1alpha1.AgentRunPhaseFailed; got != want {
		t.Fatalf("phase = %q, want %q", got, want)
	}
	if got, want := reason, "CrossNamespaceProfileRef"; got != want {
		t.Fatalf("reason = %q, want %q", got, want)
	}
}

func TestAgentRunSkillInjectionsBecomeMountedFilesAndEnv(t *testing.T) {
	t.Parallel()

	run := &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "platform-health",
			Namespace: "anvil",
		},
		Spec: controlv1alpha1.AgentRunSpec{
			SourceRef: controlv1alpha1.AgentRunSourceRef{
				Kind: "AgentSchedule",
				Name: "platform-health-hourly",
			},
			Harness: controlv1alpha1.AgentRunHarnessSpec{
				SkillInjections: []controlv1alpha1.AgentRunSkillInjectionSpec{{
					Name:        "Anvil System Rounds",
					Description: "Scheduled control-plane rounds.",
					Paths:       []string{"docs/agent-run.md"},
					Content:     "Inventory all Anvil CRs and inspect observability.",
				}},
			},
		},
	}

	data := agentRunConfigMapData(run, "prompt", "{}")
	skill, ok := data["skill-01-anvil-system-rounds.md"]
	if !ok {
		t.Fatalf("expected generated skill file, got keys %#v", data)
	}
	for _, want := range []string{
		"# Skill: Anvil System Rounds",
		"docs/agent-run.md",
		"Inventory all Anvil CRs",
	} {
		if !strings.Contains(skill, want) {
			t.Fatalf("skill file missing %q:\n%s", want, skill)
		}
	}
	job := agentRunJob(run, "platform-health-harness", "platform-health-context", nil)
	env := map[string]string{}
	for _, item := range job.Spec.Template.Spec.Containers[0].Env {
		env[item.Name] = item.Value
	}
	wantSkillFile := agentRunPayloadMountPath + "/skill-01-anvil-system-rounds.md"
	if got := env["ANVIL_AGENT_RUN_SKILL_FILES"]; got != wantSkillFile {
		t.Fatalf("ANVIL_AGENT_RUN_SKILL_FILES = %q, want %q", got, wantSkillFile)
	}
}

func TestAgentRunGitHubSkillSourceBecomesMountedFile(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/repos/HazyForge/knowledge-based/contents/skills/knowledge-base/SKILL.md"; got != want {
			t.Errorf("github request path = %q, want %q", got, want)
			http.Error(w, "bad path", http.StatusBadRequest)
			return
		}
		if got, want := r.URL.Query().Get("ref"), strings.Repeat("a", 40); got != want {
			t.Errorf("github request ref = %q, want %q", got, want)
			http.Error(w, "bad ref", http.StatusBadRequest)
			return
		}
		if got, want := r.Header.Get("Authorization"), "Bearer test-token"; got != want {
			t.Errorf("github auth header = %q, want %q", got, want)
			http.Error(w, "bad auth", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "text/markdown")
		_, _ = w.Write([]byte("# Knowledge Base\n\nUse the remote service before writing shared notes.\n"))
	}))
	defer server.Close()

	scheme := runtime.NewScheme()
	if err := controlv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("control AddToScheme returned error: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("core AddToScheme returned error: %v", err)
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "anvil-application-github",
			Namespace: "anvilhub",
		},
		Data: map[string][]byte{
			"GITHUB_TOKEN": []byte("test-token"),
		},
	}
	run := &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "platform-health",
			Namespace: "anvilhub",
		},
		Spec: controlv1alpha1.AgentRunSpec{
			SourceRef: controlv1alpha1.AgentRunSourceRef{
				Kind: "AgentSchedule",
				Name: "platform-health-hourly",
			},
			Harness: controlv1alpha1.AgentRunHarnessSpec{
				Execution: controlv1alpha1.AgentRunHarnessExecutionSpec{SkillSourceCredentials: []controlv1alpha1.AgentRunGitHubSkillCredential{{
					APIHost:        strings.TrimPrefix(server.URL, "http://"),
					TokenSecretRef: controlv1alpha1.SecretKeyReference{Name: "anvil-application-github", Key: "GITHUB_TOKEN"},
				}}},
				SkillInjections: []controlv1alpha1.AgentRunSkillInjectionSpec{{
					Name:        "knowledge-base",
					Description: "Shared knowledge-base operations.",
					SourceRefs: []controlv1alpha1.AgentRunSkillSourceRef{{
						GitHub: &controlv1alpha1.AgentRunGitHubSkillSourceSpec{
							Repository: "HazyForge/knowledge-based",
							Ref:        strings.Repeat("a", 40),
							Path:       "skills/knowledge-base/SKILL.md",
							APIBaseURL: server.URL,
						},
					}},
				}},
			},
		},
	}
	reconciler := &AgentRunReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(secret).Build(),
		Scheme: scheme,
		CommonReconcilerOptions: CommonReconcilerOptions{Options: &Options{
			GitHubAPIAllowedHosts:  []string{mustURLHostname(t, server.URL)},
			AllowInsecureGitHubAPI: true,
		}},
	}

	data, err := reconciler.agentRunConfigMapData(ctx, run, "prompt", "{}")
	if err != nil {
		t.Fatalf("agentRunConfigMapData returned error: %v", err)
	}
	skill := data["skill-01-knowledge-base.md"]
	for _, want := range []string{
		"# Skill: knowledge-base",
		"## Downloaded Source Content",
		"GitHub HazyForge/knowledge-based:skills/knowledge-base/SKILL.md @ " + strings.Repeat("a", 40),
		"Use the remote service before writing shared notes.",
	} {
		if !strings.Contains(skill, want) {
			t.Fatalf("downloaded skill file missing %q:\n%s", want, skill)
		}
	}
}

func TestAgentRunSkillSourceRejectsUnallowlistedAPIHost(t *testing.T) {
	t.Parallel()

	run := &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{Name: "remote-skill", Namespace: "agents"},
		Spec: controlv1alpha1.AgentRunSpec{
			SourceRef: controlv1alpha1.AgentRunSourceRef{Kind: "AgentSchedule", Name: "remote-skill"},
			Harness: controlv1alpha1.AgentRunHarnessSpec{SkillInjections: []controlv1alpha1.AgentRunSkillInjectionSpec{{
				Name: "remote",
				SourceRefs: []controlv1alpha1.AgentRunSkillSourceRef{{GitHub: &controlv1alpha1.AgentRunGitHubSkillSourceSpec{
					Repository: "HazyForge/skills",
					Path:       "SKILL.md",
					APIBaseURL: "https://attacker.example",
				}}},
			}}},
		},
	}
	reconciler := &AgentRunReconciler{CommonReconcilerOptions: CommonReconcilerOptions{Options: DefaultOptions()}}
	phase, reason, message := reconciler.agentRunBlockingValidation(run)
	if phase != controlv1alpha1.AgentRunPhaseFailed || reason != "InvalidSkillSource" {
		t.Fatalf("validation = (%q, %q), want (Failed, InvalidSkillSource): %s", phase, reason, message)
	}
	if !strings.Contains(message, "not in the operator allowlist") {
		t.Fatalf("validation message = %q, want allowlist error", message)
	}
}

func TestAgentRunAuthenticatedSkillSourceDoesNotFollowRedirects(t *testing.T) {
	t.Parallel()

	redirectTargetCalled := false
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		redirectTargetCalled = true
	}))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer server.Close()

	scheme := runtime.NewScheme()
	if err := controlv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add control scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "github-token", Namespace: "agents"},
		Data:       map[string][]byte{"token": []byte("sensitive")},
	}
	run := &controlv1alpha1.AgentRun{ObjectMeta: metav1.ObjectMeta{Name: "remote-skill", Namespace: "agents"}, Spec: controlv1alpha1.AgentRunSpec{Harness: controlv1alpha1.AgentRunHarnessSpec{Execution: controlv1alpha1.AgentRunHarnessExecutionSpec{SkillSourceCredentials: []controlv1alpha1.AgentRunGitHubSkillCredential{{APIHost: strings.TrimPrefix(server.URL, "http://"), TokenSecretRef: controlv1alpha1.SecretKeyReference{Name: "github-token", Key: "token"}}}}}}}
	reconciler := &AgentRunReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build(),
		Scheme: scheme,
		CommonReconcilerOptions: CommonReconcilerOptions{Options: &Options{
			GitHubAPIAllowedHosts:  []string{mustURLHostname(t, server.URL)},
			AllowInsecureGitHubAPI: true,
		}},
	}
	_, err := reconciler.resolveAgentRunGitHubSkillSource(context.Background(), run, controlv1alpha1.AgentRunGitHubSkillSourceSpec{
		Repository: "HazyForge/skills",
		Ref:        strings.Repeat("b", 40),
		Path:       "SKILL.md",
		APIBaseURL: server.URL,
	})
	if err == nil || !strings.Contains(err.Error(), "HTTP 302") {
		t.Fatalf("resolve error = %v, want HTTP 302 without redirect", err)
	}
	if redirectTargetCalled {
		t.Fatal("authenticated skill request followed a redirect")
	}
}

func TestAgentRunSkillSourceDoesNotReflectUpstreamErrorBody(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("provider-debug-secret"))
	}))
	defer server.Close()
	reconciler := &AgentRunReconciler{CommonReconcilerOptions: CommonReconcilerOptions{Options: &Options{
		GitHubAPIAllowedHosts:  []string{mustURLHostname(t, server.URL)},
		AllowInsecureGitHubAPI: true,
	}}}
	_, err := reconciler.resolveAgentRunGitHubSkillSource(context.Background(), &controlv1alpha1.AgentRun{}, controlv1alpha1.AgentRunGitHubSkillSourceSpec{
		Repository: "example/skills",
		Ref:        strings.Repeat("a", 40),
		Path:       "SKILL.md",
		APIBaseURL: server.URL,
	})
	if err == nil || !strings.Contains(err.Error(), "HTTP 502") {
		t.Fatalf("resolve error = %v, want HTTP status", err)
	}
	if strings.Contains(err.Error(), "provider-debug-secret") {
		t.Fatalf("resolve error reflected upstream response body: %v", err)
	}
}

func TestAgentRunGitHubSkillSourceRejectsOversizedContent(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("x"), agentRunRemoteSkillMaxBytes+1))
	}))
	defer server.Close()
	reconciler := &AgentRunReconciler{CommonReconcilerOptions: CommonReconcilerOptions{Options: &Options{
		GitHubAPIAllowedHosts:  []string{mustURLHostname(t, server.URL)},
		AllowInsecureGitHubAPI: true,
	}}}
	_, err := reconciler.resolveAgentRunGitHubSkillSource(context.Background(), &controlv1alpha1.AgentRun{}, controlv1alpha1.AgentRunGitHubSkillSourceSpec{
		Repository: "example/skills",
		Path:       "SKILL.md",
		APIBaseURL: server.URL,
	})
	if err == nil || !strings.Contains(err.Error(), "remote skill limit") {
		t.Fatalf("oversized skill error = %v, want explicit size limit", err)
	}
}

func TestAgentRunConfigMapDataRejectsOversizedPayload(t *testing.T) {
	t.Parallel()

	reconciler := &AgentRunReconciler{}
	_, err := reconciler.agentRunConfigMapData(context.Background(), &controlv1alpha1.AgentRun{}, strings.Repeat("p", agentRunPayloadConfigMapMaxBytes), "{}")
	if err == nil || !strings.Contains(err.Error(), "maximum supported ConfigMap payload") {
		t.Fatalf("oversized payload error = %v, want explicit ConfigMap limit", err)
	}
}

func mustURLHostname(t *testing.T, raw string) string {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse URL %q: %v", raw, err)
	}
	return parsed.Hostname()
}

func TestAgentRunSkillSourceRejectsCrossNamespaceTokenSecret(t *testing.T) {
	t.Parallel()

	run := &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "platform-health",
			Namespace: "anvilhub",
		},
		Spec: controlv1alpha1.AgentRunSpec{
			SourceRef: controlv1alpha1.AgentRunSourceRef{
				Kind: "AgentSchedule",
				Name: "platform-health-hourly",
			},
			Harness: controlv1alpha1.AgentRunHarnessSpec{
				Execution: controlv1alpha1.AgentRunHarnessExecutionSpec{SkillSourceCredentials: []controlv1alpha1.AgentRunGitHubSkillCredential{{
					APIHost:        "api.github.com",
					TokenSecretRef: controlv1alpha1.SecretKeyReference{Name: "github-token", Namespace: "other", Key: "GITHUB_TOKEN"},
				}}},
				SkillInjections: []controlv1alpha1.AgentRunSkillInjectionSpec{{
					Name: "knowledge-base",
					SourceRefs: []controlv1alpha1.AgentRunSkillSourceRef{{
						GitHub: &controlv1alpha1.AgentRunGitHubSkillSourceSpec{
							Repository: "HazyForge/knowledge-based",
							Ref:        strings.Repeat("c", 40),
							Path:       "skills/knowledge-base/SKILL.md",
						},
					}},
				}},
			},
		},
	}

	phase, reason, _ := agentRunBlockingValidation(run)
	if got, want := phase, controlv1alpha1.AgentRunPhaseFailed; got != want {
		t.Fatalf("phase = %q, want %q", got, want)
	}
	if got, want := reason, "CrossNamespaceSkillSourceToken"; got != want {
		t.Fatalf("reason = %q, want %q", got, want)
	}
}

func TestAgentRunSkillSourceRequiresImmutableCommit(t *testing.T) {
	t.Parallel()

	run := &controlv1alpha1.AgentRun{ObjectMeta: metav1.ObjectMeta{Name: "run", Namespace: "agents"}, Spec: controlv1alpha1.AgentRunSpec{SourceRef: controlv1alpha1.AgentRunSourceRef{Kind: "Manual", Name: "operator"}}}
	source := controlv1alpha1.AgentRunGitHubSkillSourceSpec{Repository: "example/skills", Ref: "main", Path: "review/SKILL.md"}
	run.Spec.Harness.SkillInjections = []controlv1alpha1.AgentRunSkillInjectionSpec{{Name: "review", SourceRefs: []controlv1alpha1.AgentRunSkillSourceRef{{GitHub: &source}}}}
	phase, reason, _ := agentRunBlockingValidation(run)
	if phase != controlv1alpha1.AgentRunPhaseFailed || reason != "MutableSkillSourceRef" {
		t.Fatalf("validation = (%q, %q), want Failed/MutableSkillSourceRef", phase, reason)
	}
	source.Ref = strings.Repeat("a", 40)
	run.Spec.Harness.SkillInjections[0].SourceRefs[0].GitHub = &source
	phase, reason, _ = agentRunBlockingValidation(run)
	if phase != "" || reason != "" {
		t.Fatalf("immutable commit validation = (%q, %q), want success", phase, reason)
	}
}

func TestAgentRunRejectsCrossNamespaceContextReferences(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		set  func(*controlv1alpha1.AgentRun)
		want string
	}{
		{name: "situation", set: func(run *controlv1alpha1.AgentRun) {
			run.Spec.SituationRef = &controlv1alpha1.NamespacedObjectReference{Name: "incident", Namespace: "other"}
		}, want: "CrossNamespaceSituationRef"},
		{name: "schedule", set: func(run *controlv1alpha1.AgentRun) {
			run.Spec.ScheduleRef = &controlv1alpha1.NamespacedObjectReference{Name: "hourly", Namespace: "other"}
		}, want: "CrossNamespaceScheduleRef"},
	} {
		t.Run(test.name, func(t *testing.T) {
			run := &controlv1alpha1.AgentRun{ObjectMeta: metav1.ObjectMeta{Name: "run", Namespace: "agents"}, Spec: controlv1alpha1.AgentRunSpec{SourceRef: controlv1alpha1.AgentRunSourceRef{Kind: "Manual", Name: "operator"}}}
			test.set(run)
			phase, reason, _ := agentRunBlockingValidation(run)
			if phase != controlv1alpha1.AgentRunPhaseFailed || reason != test.want {
				t.Fatalf("validation = (%q, %q), want Failed/%s", phase, reason, test.want)
			}
		})
	}
}

func TestAgentRunChildValidationRejectsInjectedPayloadAndJob(t *testing.T) {
	t.Parallel()

	run := &controlv1alpha1.AgentRun{ObjectMeta: metav1.ObjectMeta{Name: "run", Namespace: "agents", UID: "run-uid"}, Spec: controlv1alpha1.AgentRunSpec{SourceRef: controlv1alpha1.AgentRunSourceRef{Kind: "Manual", Name: "operator"}}}
	controller := true
	owner := metav1.OwnerReference{APIVersion: controlv1alpha1.GroupVersion.String(), Kind: "AgentRun", Name: run.Name, UID: run.UID, Controller: &controller}
	data := map[string]string{agentRunPromptFile: "trusted"}
	configMap := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "payload", Namespace: run.Namespace, Labels: agentRunLabels(run, ""), OwnerReferences: []metav1.OwnerReference{owner}}, Data: data, Immutable: boolPtr(true)}
	if err := validateAgentRunConfigMap(configMap, run, data); err != nil {
		t.Fatalf("valid ConfigMap rejected: %v", err)
	}
	configMap.Data[agentRunPromptFile] = "injected"
	if err := validateAgentRunConfigMap(configMap, run, map[string]string{agentRunPromptFile: "trusted"}); err == nil {
		t.Fatal("injected ConfigMap payload was accepted")
	}

	desired := agentRunJob(run, "run-harness", "payload", nil)
	desired.OwnerReferences = []metav1.OwnerReference{owner}
	actual := desired.DeepCopy()
	if err := validateAgentRunJob(actual, desired, run); err != nil {
		t.Fatalf("valid Job rejected: %v", err)
	}
	actual.Spec.Template.Spec.Containers[0].Image = "attacker.invalid/runner:latest"
	if err := validateAgentRunJob(actual, desired, run); err == nil {
		t.Fatal("injected Job image was accepted")
	}
	for _, test := range []struct {
		name   string
		mutate func(*batchv1.Job)
	}{
		{name: "environment", mutate: func(job *batchv1.Job) {
			job.Spec.Template.Spec.Containers[0].Env = append(job.Spec.Template.Spec.Containers[0].Env, corev1.EnvVar{Name: "INJECTED", Value: "true"})
		}},
		{name: "secret environment", mutate: func(job *batchv1.Job) {
			job.Spec.Template.Spec.Containers[0].EnvFrom = append(job.Spec.Template.Spec.Containers[0].EnvFrom, corev1.EnvFromSource{SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "foreign-secret"}}})
		}},
		{name: "secret volume", mutate: func(job *batchv1.Job) {
			job.Spec.Template.Spec.Volumes = append(job.Spec.Template.Spec.Volumes, corev1.Volume{Name: "foreign", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: "foreign-secret"}}})
		}},
		{name: "added capability", mutate: func(job *batchv1.Job) {
			job.Spec.Template.Spec.Containers[0].SecurityContext.Capabilities.Add = []corev1.Capability{"SYS_ADMIN"}
		}},
		{name: "service account", mutate: func(job *batchv1.Job) {
			job.Spec.Template.Spec.ServiceAccountName = "foreign-service-account"
		}},
		{name: "annotation", mutate: func(job *batchv1.Job) {
			job.Annotations = cloneStringMap(job.Annotations)
			if job.Annotations == nil {
				job.Annotations = map[string]string{}
			}
			job.Annotations["admission.example/inject"] = "true"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			injected := desired.DeepCopy()
			test.mutate(injected)
			if err := validateAgentRunJob(injected, desired, run); err == nil {
				t.Fatalf("injected Job %s was accepted", test.name)
			}
		})
	}
}

func TestAgentRunJobValidationAcceptsKubernetesDefaults(t *testing.T) {
	t.Parallel()

	run := &controlv1alpha1.AgentRun{ObjectMeta: metav1.ObjectMeta{Name: "run", Namespace: "agents", UID: "run-uid"}, Spec: controlv1alpha1.AgentRunSpec{SourceRef: controlv1alpha1.AgentRunSourceRef{Kind: "Manual", Name: "operator"}}}
	controller := true
	owner := metav1.OwnerReference{APIVersion: controlv1alpha1.GroupVersion.String(), Kind: "AgentRun", Name: run.Name, UID: run.UID, Controller: &controller}
	desired := agentRunJob(run, "run-harness", "payload", nil)
	desired.OwnerReferences = []metav1.OwnerReference{owner}
	actual := desired.DeepCopy()
	actual.UID = "job-uid"
	one := int32(1)
	falseValue := false
	completionMode := batchv1.NonIndexedCompletion
	replacementPolicy := batchv1.TerminatingOrFailed
	thirty := int64(30)
	defaultMode := int32(0o644)
	actual.Spec.Parallelism = &one
	actual.Spec.Completions = &one
	actual.Spec.ManualSelector = &falseValue
	actual.Spec.CompletionMode = &completionMode
	actual.Spec.Suspend = &falseValue
	actual.Spec.PodReplacementPolicy = &replacementPolicy
	actual.Spec.Selector = &metav1.LabelSelector{MatchLabels: map[string]string{"batch.kubernetes.io/controller-uid": string(actual.UID)}}
	actual.Spec.Template.Labels["batch.kubernetes.io/controller-uid"] = string(actual.UID)
	actual.Spec.Template.Labels["batch.kubernetes.io/job-name"] = actual.Name
	actual.Spec.Template.Labels["controller-uid"] = string(actual.UID)
	actual.Spec.Template.Labels["job-name"] = actual.Name
	pod := &actual.Spec.Template.Spec
	pod.DeprecatedServiceAccount = pod.ServiceAccountName
	pod.EnableServiceLinks = boolPtr(true)
	preemptLowerPriority := corev1.PreemptLowerPriority
	pod.PreemptionPolicy = &preemptLowerPriority
	pod.DNSPolicy = corev1.DNSClusterFirst
	pod.SchedulerName = corev1.DefaultSchedulerName
	pod.TerminationGracePeriodSeconds = &thirty
	pod.Containers[0].TerminationMessagePath = corev1.TerminationMessagePathDefault
	pod.Containers[0].TerminationMessagePolicy = corev1.TerminationMessageReadFile
	pod.Volumes[0].ConfigMap.DefaultMode = &defaultMode

	if err := validateAgentRunJob(actual, desired, run); err != nil {
		t.Fatalf("Kubernetes-defaulted Job rejected: %v", err)
	}
	desiredDigest, err := agentRunJobSnapshotDigest(desired)
	if err != nil {
		t.Fatalf("digest desired Job: %v", err)
	}
	actualDigest, err := agentRunJobSnapshotDigest(actual)
	if err != nil {
		t.Fatalf("digest defaulted Job: %v", err)
	}
	if actualDigest != desiredDigest {
		t.Fatalf("defaulted Job digest = %q, want %q", actualDigest, desiredDigest)
	}
}

func TestAgentRunJobValidationAndReceiptIgnoreTelemetryOnlyLabelRollout(t *testing.T) {
	t.Parallel()

	run := &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{Name: "legacy-run", Namespace: "agents", UID: "run-uid"},
		Spec: controlv1alpha1.AgentRunSpec{
			SourceRef: controlv1alpha1.AgentRunSourceRef{Kind: "Manual", Name: "operator"},
			Harness: controlv1alpha1.AgentRunHarnessSpec{
				Intent:  controlv1alpha1.AgentRunIntentProposeChange,
				Backend: controlv1alpha1.AgentRunHarnessBackendSpec{Kind: controlv1alpha1.AgentRunHarnessBackendOpenCode},
			},
		},
	}
	controller := true
	owner := metav1.OwnerReference{APIVersion: controlv1alpha1.GroupVersion.String(), Kind: "AgentRun", Name: run.Name, UID: run.UID, Controller: &controller}
	desired := agentRunJob(run, "legacy-run-harness", "legacy-run-context", nil)
	desired.OwnerReferences = []metav1.OwnerReference{owner}

	legacy := desired.DeepCopy()
	delete(legacy.Labels, agentRunLabelBackend)
	delete(legacy.Labels, agentRunLabelIntent)
	delete(legacy.Spec.Template.Labels, agentRunLabelBackend)
	delete(legacy.Spec.Template.Labels, agentRunLabelIntent)
	if err := validateAgentRunJob(legacy, desired, run); err != nil {
		t.Fatalf("pre-telemetry Job rejected during rollout: %v", err)
	}

	desiredDigest, err := agentRunJobSnapshotDigest(desired)
	if err != nil {
		t.Fatalf("digest telemetry-labeled Job: %v", err)
	}
	legacyDigest, err := agentRunJobSnapshotDigest(legacy)
	if err != nil {
		t.Fatalf("digest pre-telemetry Job: %v", err)
	}
	if legacyDigest != desiredDigest {
		t.Fatalf("pre-telemetry Job digest = %q, want %q", legacyDigest, desiredDigest)
	}

	tampered := legacy.DeepCopy()
	tampered.Labels[agentRunLabelSourceName] = "different-source"
	if err := validateAgentRunJob(tampered, desired, run); err == nil {
		t.Fatal("non-telemetry Job label mutation was accepted")
	}
}

func TestAgentRunConfigMapMigrationMakesExactLegacyPayloadImmutable(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := controlv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	run := &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{Name: "legacy-run", Namespace: "agents", UID: "run-uid"},
		Spec:       controlv1alpha1.AgentRunSpec{SourceRef: controlv1alpha1.AgentRunSourceRef{Kind: "Manual", Name: "operator"}},
	}
	reconciler := &AgentRunReconciler{Scheme: scheme}
	contextBody, err := reconciler.agentRunContextJSON(ctx, run)
	if err != nil {
		t.Fatal(err)
	}
	data, err := reconciler.agentRunConfigMapData(ctx, run, "trusted prompt", string(contextBody))
	if err != nil {
		t.Fatal(err)
	}
	controller := true
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      agentRunChildName(run.Name, "context", "prompt-hash"),
			Namespace: run.Namespace,
			Labels:    agentRunLabels(run, ""),
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: controlv1alpha1.GroupVersion.String(), Kind: "AgentRun", Name: run.Name, UID: run.UID, Controller: &controller,
			}},
		},
		Data: data,
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(configMap).Build()
	reconciler.Client = c

	resolved, _, err := reconciler.ensureAgentRunConfigMap(ctx, run, "trusted prompt", "prompt-hash")
	if err != nil {
		t.Fatalf("migrate legacy ConfigMap: %v", err)
	}
	if resolved.Immutable == nil || !*resolved.Immutable {
		t.Fatalf("resolved ConfigMap immutable = %#v, want true", resolved.Immutable)
	}
	stored := &corev1.ConfigMap{}
	if err := c.Get(ctx, client.ObjectKeyFromObject(configMap), stored); err != nil {
		t.Fatal(err)
	}
	if stored.Immutable == nil || !*stored.Immutable {
		t.Fatalf("stored ConfigMap immutable = %#v, want true", stored.Immutable)
	}
}

func TestAgentRunRunnerPodSelectionRequiresJobOwnerUID(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "run-harness", Namespace: "agents", UID: "job-uid"}}
	controller := true
	legitimate := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "legitimate", Namespace: "agents", CreationTimestamp: metav1.NewTime(time.Unix(1, 0)), Labels: map[string]string{agentRunJobLabel: job.Name}, OwnerReferences: []metav1.OwnerReference{{APIVersion: batchv1.SchemeGroupVersion.String(), Kind: "Job", Name: job.Name, UID: job.UID, Controller: &controller}}}}
	forged := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "forged", Namespace: "agents", CreationTimestamp: metav1.NewTime(time.Unix(2, 0)), Labels: map[string]string{agentRunJobLabel: job.Name}}}
	reconciler := &AgentRunReconciler{Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(legitimate, forged).Build()}
	ref, pod, err := reconciler.findAgentRunRunnerPod(context.Background(), job)
	if err != nil {
		t.Fatal(err)
	}
	if pod == nil || pod.Name != legitimate.Name || ref == nil || ref.Name != legitimate.Name {
		t.Fatalf("selected pod = %#v ref=%#v, want legitimate owner", pod, ref)
	}
}

func TestAgentRunToolsBecomeSetupFilesAndEnv(t *testing.T) {
	t.Parallel()

	run := &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hazy-trade-health",
			Namespace: "hazy-trade",
		},
		Spec: controlv1alpha1.AgentRunSpec{
			SourceRef: controlv1alpha1.AgentRunSourceRef{
				Kind: "AgentSchedule",
				Name: "hazy-trade-health-30m",
			},
			Harness: controlv1alpha1.AgentRunHarnessSpec{
				Tools: []controlv1alpha1.AgentRunToolSpec{{
					Name:          "hazytradectl",
					Description:   "Hazy Trade REST CLI.",
					SetupScript:   "export PATH=\"/codex-home/cargo/bin:$PATH\"\n",
					VerifyCommand: []string{"hazytradectl", "--version"},
				}},
			},
		},
	}

	data := agentRunConfigMapData(run, "prompt", "{}")
	setup, ok := data["tool-01-hazytradectl-setup.sh"]
	if !ok {
		t.Fatalf("expected generated tool setup file, got keys %#v", data)
	}
	for _, want := range []string{
		"#!/usr/bin/env bash",
		"set -euo pipefail",
		"export PATH=",
	} {
		if !strings.Contains(setup, want) {
			t.Fatalf("setup file missing %q:\n%s", want, setup)
		}
	}
	if manifest := data[agentRunToolManifestFile]; !strings.Contains(manifest, `"name":"hazytradectl"`) || !strings.Contains(manifest, `"setupFile"`) || strings.Contains(manifest, `"setupScript"`) {
		t.Fatalf("tool manifest = %q, want immutable file-backed tool plan", manifest)
	}

	job := agentRunJob(run, "hazy-trade-health-harness", "hazy-trade-health-context", nil)
	env := map[string]string{}
	for _, item := range job.Spec.Template.Spec.Containers[0].Env {
		env[item.Name] = item.Value
	}
	wantSetupFile := agentRunPayloadMountPath + "/tool-01-hazytradectl-setup.sh"
	if got := env["ANVIL_AGENT_RUN_TOOL_SETUP_FILES"]; got != wantSetupFile {
		t.Fatalf("ANVIL_AGENT_RUN_TOOL_SETUP_FILES = %q, want %q", got, wantSetupFile)
	}
	if got := env["ANVIL_AGENT_RUN_TOOLS_JSON"]; !strings.Contains(got, `"hazytradectl"`) || !strings.Contains(got, `"verifyCommand"`) {
		t.Fatalf("ANVIL_AGENT_RUN_TOOLS_JSON = %q, want tool metadata", got)
	}
	if strings.Contains(env["ANVIL_AGENT_RUN_TOOLS_JSON"], "SetupScript") || strings.Contains(env["ANVIL_AGENT_RUN_TOOLS_JSON"], "setupScript") {
		t.Fatalf("ANVIL_AGENT_RUN_TOOLS_JSON should not inline setup scripts: %s", env["ANVIL_AGENT_RUN_TOOLS_JSON"])
	}
	if got := env["ANVIL_AGENT_RUN_TOOL_MANIFEST_FILE"]; got != agentRunPayloadMountPath+"/"+agentRunToolManifestFile {
		t.Fatalf("ANVIL_AGENT_RUN_TOOL_MANIFEST_FILE = %q", got)
	}
	if env["ANVIL_AGENT_TOOL_CACHE_ROOT"] != agentRunToolCacheMountPath || env["ANVIL_AGENT_TOOL_BIN_DIR"] != agentRunCapabilityRuntimeMountPath+"/bin" {
		t.Fatalf("capability tool paths = %#v", env)
	}
}

func TestAgentRunMCPServersBecomeImmutableManifestAndEnv(t *testing.T) {
	t.Parallel()
	run := &controlv1alpha1.AgentRun{Spec: controlv1alpha1.AgentRunSpec{Harness: controlv1alpha1.AgentRunHarnessSpec{MCPServers: []controlv1alpha1.AgentRunMCPServerSpec{{
		Name: "runbooks",
		Transport: controlv1alpha1.AgentMCPTransport{StreamableHTTP: &controlv1alpha1.AgentMCPStreamableHTTPTransport{
			Endpoint: "https://mcp.example.test/v1",
			Headers:  []controlv1alpha1.AgentMCPHTTPHeader{{Name: "Authorization", EnvVar: "RUNBOOKS_TOKEN"}},
		}},
		ToolAllowlist: []string{"search"},
	}}}}}
	data := agentRunConfigMapData(run, "prompt", "{}")
	manifest := data[agentRunMCPManifestFile]
	if !strings.Contains(manifest, `"name":"runbooks"`) || !strings.Contains(manifest, `"envVar":"RUNBOOKS_TOKEN"`) || strings.Contains(manifest, "token-value") {
		t.Fatalf("MCP manifest = %q", manifest)
	}
	job := agentRunJob(run, "mcp-harness", "mcp-context", nil)
	if got := agentRunJobEnvValue(job, "ANVIL_AGENT_RUN_MCP_MANIFEST_FILE"); got != agentRunPayloadMountPath+"/"+agentRunMCPManifestFile {
		t.Fatalf("MCP manifest env = %q", got)
	}
}

func TestAgentRunJobUsesPersistentToolCacheWithoutAuthenticationEnv(t *testing.T) {
	t.Parallel()
	run := &controlv1alpha1.AgentRun{Spec: controlv1alpha1.AgentRunSpec{Harness: controlv1alpha1.AgentRunHarnessSpec{Tools: []controlv1alpha1.AgentRunToolSpec{{Name: "query"}}}}}
	cache := resolvedAgentRunDataVolume{
		Name:      "shared-tools",
		Namespace: "agents",
		ClaimName: "agent-data-shared-tools",
		MountPath: agentRunToolCacheMountPath,
		ToolCache: true,
	}
	job := agentRunJob(run, "cache-harness", "cache-context", []resolvedAgentRunDataVolume{cache})
	pod := job.Spec.Template.Spec
	cacheVolumes := 0
	for _, volume := range pod.Volumes {
		if volume.PersistentVolumeClaim != nil && volume.PersistentVolumeClaim.ClaimName == cache.ClaimName {
			cacheVolumes++
		}
		if volume.Name == agentRunToolCacheVolume {
			t.Fatalf("ephemeral cache rendered with persistent cache: %#v", pod.Volumes)
		}
	}
	if cacheVolumes != 1 {
		t.Fatalf("persistent cache volumes = %d, want 1", cacheVolumes)
	}
}

func TestAgentRunJobInjectsStatusToolEnv(t *testing.T) {
	t.Parallel()

	run := &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "platform-health",
			Namespace: "anvil-system",
		},
		Spec: controlv1alpha1.AgentRunSpec{
			SourceRef: controlv1alpha1.AgentRunSourceRef{
				Kind: "AgentSchedule",
				Name: "platform-health-hourly",
			},
			Harness: controlv1alpha1.AgentRunHarnessSpec{
				Intent: controlv1alpha1.AgentRunIntentProposeChange,
				Backend: controlv1alpha1.AgentRunHarnessBackendSpec{
					Kind: controlv1alpha1.AgentRunHarnessBackendCodex,
				},
				Execution: controlv1alpha1.AgentRunHarnessExecutionSpec{
					ExtraEnv: []corev1.EnvVar{{
						Name:  "CUSTOM_SETTING",
						Value: "enabled",
					}},
				},
			},
		},
	}

	job := agentRunJob(run, "platform-health-harness", "platform-health-context", nil)
	env := map[string]string{}
	for _, item := range job.Spec.Template.Spec.Containers[0].Env {
		env[item.Name] = item.Value
	}

	expected := map[string]string{
		"ANVIL_AGENT_RUN_STATUS_FILE":             agentRunStatusFile,
		"ANVIL_AGENT_RUN_STATUS_LOG_PREFIX":       agentRunStatusLinePrefix,
		"ANVIL_AGENT_RUN_STATUS_TOOL":             "anvil-agent-status",
		"ANVIL_AGENT_FEEDBACK_TOOL":               "anvil-hotline",
		"ANVIL_HOTLINE_TOOL":                      "anvil-hotline",
		"ANVIL_AGENT_RUN_PLATFORM_REPOSITORY":     agentRunPlatformRepository,
		"ANVIL_AGENT_RUN_PLATFORM_REPOSITORY_URL": agentRunPlatformRepositoryURL,
		"CUSTOM_SETTING":                          "enabled",
	}
	for key, want := range expected {
		if got := env[key]; got != want {
			t.Fatalf("env[%s] = %q, want %q", key, got, want)
		}
	}
	podSecurityContext := job.Spec.Template.Spec.SecurityContext
	if podSecurityContext == nil || podSecurityContext.RunAsNonRoot == nil || !*podSecurityContext.RunAsNonRoot {
		t.Fatalf("pod RunAsNonRoot = %#v, want true", podSecurityContext)
	}
	if podSecurityContext.SeccompProfile == nil || podSecurityContext.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Fatalf("pod seccomp profile = %#v, want RuntimeDefault", podSecurityContext.SeccompProfile)
	}
	containerSecurityContext := job.Spec.Template.Spec.Containers[0].SecurityContext
	if containerSecurityContext == nil || containerSecurityContext.AllowPrivilegeEscalation == nil || *containerSecurityContext.AllowPrivilegeEscalation {
		t.Fatalf("container allowPrivilegeEscalation = %#v, want false", containerSecurityContext)
	}
	if containerSecurityContext.Capabilities == nil || len(containerSecurityContext.Capabilities.Drop) != 1 || containerSecurityContext.Capabilities.Drop[0] != "ALL" {
		t.Fatalf("container dropped capabilities = %#v, want ALL", containerSecurityContext.Capabilities)
	}
}

func TestAgentRunJobLabelsExposeCollectorNeutralTelemetryDimensions(t *testing.T) {
	t.Parallel()

	run := &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{Name: "repository-review", Namespace: "agents"},
		Spec: controlv1alpha1.AgentRunSpec{
			SourceRef: controlv1alpha1.AgentRunSourceRef{Kind: "AgentSchedule", Name: "repository-review-hourly"},
			Harness: controlv1alpha1.AgentRunHarnessSpec{
				Intent: controlv1alpha1.AgentRunIntentProposeChange,
				Backend: controlv1alpha1.AgentRunHarnessBackendSpec{
					Kind: controlv1alpha1.AgentRunHarnessBackendOpenCode,
				},
			},
		},
	}

	job := agentRunJob(run, "repository-review-harness", "repository-review-context", nil)
	for name, want := range map[string]string{
		agentRunLabel:           "repository-review",
		agentRunJobLabel:        "repository-review-harness",
		agentRunLabelBackend:    "opencode",
		agentRunLabelIntent:     "proposechange",
		agentRunLabelSourceKind: "agentschedule",
		agentRunLabelSourceName: "repository-review-hourly",
	} {
		if got := job.Spec.Template.Labels[name]; got != want {
			t.Fatalf("pod label %s = %q, want %q", name, got, want)
		}
		if got := job.Labels[name]; got != want {
			t.Fatalf("job label %s = %q, want %q", name, got, want)
		}
	}
	for _, name := range []string{agentRunLabelBackend, agentRunLabelIntent} {
		if _, exists := agentRunLabels(run, "")[name]; exists {
			t.Fatalf("payload label set unexpectedly contains workload telemetry label %s", name)
		}
	}

	defaultJob := agentRunJob(&controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{Name: "default-run", Namespace: "agents"},
		Spec:       controlv1alpha1.AgentRunSpec{SourceRef: controlv1alpha1.AgentRunSourceRef{Kind: "Manual", Name: "operator"}},
	}, "default-run-harness", "default-run-context", nil)
	for name, want := range map[string]string{
		agentRunLabelBackend: "codex",
		agentRunLabelIntent:  "observe",
	} {
		if got := defaultJob.Spec.Template.Labels[name]; got != want {
			t.Fatalf("default pod label %s = %q, want %q", name, got, want)
		}
		if got := defaultJob.Labels[name]; got != want {
			t.Fatalf("default job label %s = %q, want %q", name, got, want)
		}
	}
}

func TestAgentRunBackendProviderAndGrokBuildEnv(t *testing.T) {
	t.Parallel()

	t.Run("opencode backend", func(t *testing.T) {
		t.Parallel()

		pure := false
		auto := true
		run := &controlv1alpha1.AgentRun{
			ObjectMeta: metav1.ObjectMeta{Name: "opencode-review", Namespace: "agents"},
			Spec: controlv1alpha1.AgentRunSpec{
				SourceRef: controlv1alpha1.AgentRunSourceRef{Kind: "AgentSchedule", Name: "opencode-hourly"},
				Harness: controlv1alpha1.AgentRunHarnessSpec{
					Backend: controlv1alpha1.AgentRunHarnessBackendSpec{
						Kind: controlv1alpha1.AgentRunHarnessBackendOpenCode,
						OpenCode: &controlv1alpha1.AgentRunOpenCodeBackendSpec{
							Model:          "openai/gpt-5.4",
							Agent:          "build",
							Variant:        "high",
							Format:         "json",
							Auto:           &auto,
							Pure:           &pure,
							AdditionalArgs: []string{"--title=scheduled review"},
						},
					},
				},
			},
		}

		if got, want := agentRunImage(run), agentRunDefaultOpenCodeImage; got != want {
			t.Fatalf("OpenCode image = %q, want %q", got, want)
		}
		job := agentRunJob(run, "opencode-harness", "opencode-context", nil)
		env := map[string]string{}
		for _, item := range job.Spec.Template.Spec.Containers[0].Env {
			env[item.Name] = item.Value
		}
		expected := map[string]string{
			"ANVIL_OPENCODE_MODEL":                "openai/gpt-5.4",
			"ANVIL_OPENCODE_AGENT":                "build",
			"ANVIL_OPENCODE_VARIANT":              "high",
			"ANVIL_OPENCODE_FORMAT":               "json",
			"ANVIL_OPENCODE_AUTO":                 "true",
			"ANVIL_OPENCODE_PURE":                 "false",
			"ANVIL_OPENCODE_ADDITIONAL_ARGS_JSON": `["--title=scheduled review"]`,
		}
		for key, want := range expected {
			if got := env[key]; got != want {
				t.Fatalf("env[%s] = %q, want %q", key, got, want)
			}
		}

		defaultRun := run.DeepCopy()
		defaultRun.Name = "opencode-defaults"
		defaultRun.Spec.Harness.Backend.OpenCode = nil
		defaultJob := agentRunJob(defaultRun, "opencode-default-harness", "opencode-default-context", nil)
		defaultEnv := map[string]string{}
		for _, item := range defaultJob.Spec.Template.Spec.Containers[0].Env {
			defaultEnv[item.Name] = item.Value
		}
		if got, want := defaultEnv["ANVIL_OPENCODE_AUTO"], "false"; got != want {
			t.Fatalf("default OpenCode auto = %q, want %q", got, want)
		}
		if got, want := defaultEnv["ANVIL_OPENCODE_PURE"], "true"; got != want {
			t.Fatalf("default OpenCode pure = %q, want %q", got, want)
		}
	})

	t.Run("hermes xai oauth provider", func(t *testing.T) {
		t.Parallel()

		run := &controlv1alpha1.AgentRun{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "hermes-grok",
				Namespace: "agents",
			},
			Spec: controlv1alpha1.AgentRunSpec{
				SourceRef: controlv1alpha1.AgentRunSourceRef{
					Kind: "AgentSchedule",
					Name: "hermes-grok-hourly",
				},
				Harness: controlv1alpha1.AgentRunHarnessSpec{
					Backend: controlv1alpha1.AgentRunHarnessBackendSpec{
						Kind:             controlv1alpha1.AgentRunHarnessBackendHermesAgent,
						ModelProvider:    controlv1alpha1.AgentRunModelProviderXAI,
						ProviderAuthMode: controlv1alpha1.AgentRunProviderAuthModeOAuth,
						HermesAgent: &controlv1alpha1.AgentRunHermesBackendSpec{
							Model: "grok-4.5",
						},
					},
				},
			},
		}

		job := agentRunJob(run, "hermes-grok-harness", "hermes-grok-context", nil)
		env := map[string]string{}
		for _, item := range job.Spec.Template.Spec.Containers[0].Env {
			env[item.Name] = item.Value
		}
		if got, want := env["ANVIL_AGENT_RUN_MODEL_PROVIDER"], "xai"; got != want {
			t.Fatalf("model provider env = %q, want %q", got, want)
		}
		if got, want := env["ANVIL_AGENT_RUN_PROVIDER_AUTH_MODE"], "oauth"; got != want {
			t.Fatalf("provider auth env = %q, want %q", got, want)
		}
		if got, want := env["ANVIL_HERMES_MODEL_PROVIDER"], "xai-oauth"; got != want {
			t.Fatalf("Hermes provider env = %q, want %q", got, want)
		}
		if got, want := env["ANVIL_HERMES_PROVIDER_AUTH_MODE"], "oauth"; got != want {
			t.Fatalf("Hermes auth mode env = %q, want %q", got, want)
		}
		if got, want := env["ANVIL_HERMES_MODEL"], "grok-4.5"; got != want {
			t.Fatalf("Hermes model env = %q, want %q", got, want)
		}
	})

	t.Run("openclaw xai oauth provider", func(t *testing.T) {
		t.Parallel()

		run := &controlv1alpha1.AgentRun{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "openclaw-grok",
				Namespace: "agents",
			},
			Spec: controlv1alpha1.AgentRunSpec{
				SourceRef: controlv1alpha1.AgentRunSourceRef{
					Kind: "AgentSchedule",
					Name: "openclaw-grok-hourly",
				},
				Harness: controlv1alpha1.AgentRunHarnessSpec{
					Backend: controlv1alpha1.AgentRunHarnessBackendSpec{
						Kind:             controlv1alpha1.AgentRunHarnessBackendOpenClaw,
						ModelProvider:    controlv1alpha1.AgentRunModelProviderXAI,
						ProviderAuthMode: controlv1alpha1.AgentRunProviderAuthModeOAuth,
						OpenClaw: &controlv1alpha1.AgentRunOpenClawBackendSpec{
							AgentID:     "xai-reviewer",
							Model:       "grok-4.5",
							Thinking:    "high",
							ServiceTier: "priority",
						},
					},
				},
			},
		}

		job := agentRunJob(run, "openclaw-grok-harness", "openclaw-grok-context", nil)
		env := map[string]string{}
		for _, item := range job.Spec.Template.Spec.Containers[0].Env {
			env[item.Name] = item.Value
		}
		expected := map[string]string{
			"ANVIL_AGENT_RUN_MODEL_PROVIDER":     "xai",
			"ANVIL_AGENT_RUN_PROVIDER_AUTH_MODE": "oauth",
			"ANVIL_OPENCLAW_PROVIDER":            "xai",
			"ANVIL_OPENCLAW_PROVIDER_AUTH_MODE":  "oauth",
			"ANVIL_OPENCLAW_AGENT_ID":            "xai-reviewer",
			"ANVIL_OPENCLAW_MODEL":               "grok-4.5",
			"ANVIL_OPENCLAW_THINKING":            "high",
			"ANVIL_OPENCLAW_SERVICE_TIER":        "priority",
		}
		for key, want := range expected {
			if got := env[key]; got != want {
				t.Fatalf("env[%s] = %q, want %q", key, got, want)
			}
		}
	})

	t.Run("grok build backend", func(t *testing.T) {
		t.Parallel()

		run := &controlv1alpha1.AgentRun{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "grok-build",
				Namespace: "agents",
			},
			Spec: controlv1alpha1.AgentRunSpec{
				SourceRef: controlv1alpha1.AgentRunSourceRef{
					Kind: "AgentSchedule",
					Name: "grok-build-hourly",
				},
				Harness: controlv1alpha1.AgentRunHarnessSpec{
					Backend: controlv1alpha1.AgentRunHarnessBackendSpec{
						Kind:             controlv1alpha1.AgentRunHarnessBackendGrokBuild,
						ModelProvider:    controlv1alpha1.AgentRunModelProviderXAI,
						ProviderAuthMode: controlv1alpha1.AgentRunProviderAuthModeOAuth,
						GrokBuild: &controlv1alpha1.AgentRunGrokBuildBackendSpec{
							Model:           "grok-4.5",
							ReasoningEffort: "high",
							ServiceTier:     "priority",
							Profile:         "platform-health",
							Command:         "grok",
							AdditionalArgs:  []string{"--output-format", "streaming-json"},
						},
					},
				},
			},
		}

		if got, want := agentRunImage(run), agentRunDefaultGrokBuildImage; got != want {
			t.Fatalf("Grok Build image = %q, want %q", got, want)
		}
		job := agentRunJob(run, "grok-build-harness", "grok-build-context", nil)
		env := map[string]string{}
		for _, item := range job.Spec.Template.Spec.Containers[0].Env {
			env[item.Name] = item.Value
		}
		expected := map[string]string{
			"ANVIL_AGENT_RUN_MODEL_PROVIDER":        "xai",
			"ANVIL_AGENT_RUN_PROVIDER_AUTH_MODE":    "oauth",
			"ANVIL_GROK_BUILD_MODEL_PROVIDER":       "xai",
			"ANVIL_GROK_BUILD_PROVIDER_AUTH_MODE":   "oauth",
			"ANVIL_GROK_BUILD_MODEL":                "grok-4.5",
			"ANVIL_GROK_BUILD_REASONING_EFFORT":     "high",
			"ANVIL_GROK_BUILD_SERVICE_TIER":         "priority",
			"ANVIL_GROK_BUILD_PROFILE":              "platform-health",
			"ANVIL_GROK_BUILD_COMMAND":              "grok",
			"ANVIL_GROK_BUILD_ADDITIONAL_ARGS_JSON": `["--output-format","streaming-json"]`,
		}
		for key, want := range expected {
			if got := env[key]; got != want {
				t.Fatalf("env[%s] = %q, want %q", key, got, want)
			}
		}
	})

	t.Run("pi agent xai oauth provider", func(t *testing.T) {
		t.Parallel()

		run := &controlv1alpha1.AgentRun{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "pi-grok",
				Namespace: "agents",
			},
			Spec: controlv1alpha1.AgentRunSpec{
				SourceRef: controlv1alpha1.AgentRunSourceRef{
					Kind: "AgentSchedule",
					Name: "pi-grok-hourly",
				},
				Harness: controlv1alpha1.AgentRunHarnessSpec{
					Backend: controlv1alpha1.AgentRunHarnessBackendSpec{
						Kind:             controlv1alpha1.AgentRunHarnessBackendPiAgent,
						ModelProvider:    controlv1alpha1.AgentRunModelProviderXAI,
						ProviderAuthMode: controlv1alpha1.AgentRunProviderAuthModeOAuth,
						PiAgent: &controlv1alpha1.AgentRunPiBackendSpec{
							Model:          "grok-composer-2.5-fast",
							Thinking:       "medium",
							Mode:           "json",
							AdditionalArgs: []string{"--exclude-tools", "write"},
						},
					},
				},
			},
		}

		if got, want := agentRunImage(run), agentRunDefaultPiAgentImage; got != want {
			t.Fatalf("Pi image = %q, want %q", got, want)
		}
		job := agentRunJob(run, "pi-grok-harness", "pi-grok-context", nil)
		env := map[string]string{}
		for _, item := range job.Spec.Template.Spec.Containers[0].Env {
			env[item.Name] = item.Value
		}
		expected := map[string]string{
			"ANVIL_AGENT_RUN_MODEL_PROVIDER":     "xai",
			"ANVIL_AGENT_RUN_PROVIDER_AUTH_MODE": "oauth",
			"ANVIL_PI_MODEL_PROVIDER":            "xai",
			"ANVIL_PI_PROVIDER_AUTH_MODE":        "oauth",
			"ANVIL_PI_PROVIDER":                  "xai-auth",
			"ANVIL_PI_MODEL":                     "grok-composer-2.5-fast",
			"ANVIL_PI_THINKING":                  "medium",
			"ANVIL_PI_MODE":                      "json",
			"ANVIL_PI_NO_SESSION":                "false",
			"ANVIL_PI_ADDITIONAL_ARGS_JSON":      `["--exclude-tools","write"]`,
		}
		for key, want := range expected {
			if got := env[key]; got != want {
				t.Fatalf("env[%s] = %q, want %q", key, got, want)
			}
		}
	})
}

func TestAgentRunImageUsesConfiguredBackendDefault(t *testing.T) {
	t.Parallel()

	run := &controlv1alpha1.AgentRun{Spec: controlv1alpha1.AgentRunSpec{
		Harness: controlv1alpha1.AgentRunHarnessSpec{
			Backend: controlv1alpha1.AgentRunHarnessBackendSpec{Kind: controlv1alpha1.AgentRunHarnessBackendCodex},
		},
	}}
	reconciler := &AgentRunReconciler{CommonReconcilerOptions: CommonReconcilerOptions{Options: &Options{
		CodexRunnerImage: "registry.example/agents/codex@sha256:configured",
	}}}
	if got, want := reconciler.agentRunImage(run), "registry.example/agents/codex@sha256:configured"; got != want {
		t.Fatalf("configured Codex image = %q, want %q", got, want)
	}
	run.Spec.Harness.Backend.Image = "registry.example/agents/codex@sha256:run-specific"
	if got, want := reconciler.agentRunImage(run), "registry.example/agents/codex@sha256:run-specific"; got != want {
		t.Fatalf("run-specific Codex image = %q, want %q", got, want)
	}
}

func TestAgentRunMergeBackendMergesModelProviderAndGrokBuild(t *testing.T) {
	t.Parallel()

	profile := controlv1alpha1.AgentRunHarnessBackendSpec{
		Kind:             controlv1alpha1.AgentRunHarnessBackendGrokBuild,
		ModelProvider:    controlv1alpha1.AgentRunModelProviderOpenAI,
		ProviderAuthMode: controlv1alpha1.AgentRunProviderAuthModeAPIKey,
		GrokBuild: &controlv1alpha1.AgentRunGrokBuildBackendSpec{
			Model:          "grok-build-default",
			ServiceTier:    "default",
			AdditionalArgs: []string{"--output-format", "json"},
		},
	}
	run := controlv1alpha1.AgentRunHarnessBackendSpec{
		ModelProvider:    controlv1alpha1.AgentRunModelProviderXAI,
		ProviderAuthMode: controlv1alpha1.AgentRunProviderAuthModeOAuth,
		GrokBuild: &controlv1alpha1.AgentRunGrokBuildBackendSpec{
			Model:           "grok-4.5",
			ReasoningEffort: "high",
			Profile:         "reviewer",
			Command:         "grok",
			AdditionalArgs:  []string{"--debug"},
		},
	}

	merged := agentRunMergeBackend(profile, run)
	if got, want := merged.ModelProvider, controlv1alpha1.AgentRunModelProviderXAI; got != want {
		t.Fatalf("model provider = %q, want %q", got, want)
	}
	if got, want := merged.ProviderAuthMode, controlv1alpha1.AgentRunProviderAuthModeOAuth; got != want {
		t.Fatalf("provider auth mode = %q, want %q", got, want)
	}
	if merged.GrokBuild == nil {
		t.Fatal("GrokBuild backend was not merged")
	}
	if got, want := merged.GrokBuild.Model, "grok-4.5"; got != want {
		t.Fatalf("GrokBuild model = %q, want %q", got, want)
	}
	if got, want := merged.GrokBuild.ReasoningEffort, "high"; got != want {
		t.Fatalf("GrokBuild reasoning effort = %q, want %q", got, want)
	}
	if got, want := merged.GrokBuild.ServiceTier, "default"; got != want {
		t.Fatalf("GrokBuild service tier = %q, want %q", got, want)
	}
	if got, want := merged.GrokBuild.Profile, "reviewer"; got != want {
		t.Fatalf("GrokBuild profile = %q, want %q", got, want)
	}
	if got, want := merged.GrokBuild.Command, "grok"; got != want {
		t.Fatalf("GrokBuild command = %q, want %q", got, want)
	}
	if got, want := strings.Join(merged.GrokBuild.AdditionalArgs, ","), "--output-format,json,--debug"; got != want {
		t.Fatalf("GrokBuild additional args = %q, want %q", got, want)
	}
}

func TestAgentRunMergeBackendMergesOpenCode(t *testing.T) {
	t.Parallel()

	profilePure := true
	runPure := false
	profileAuto := true
	runAuto := false
	profile := controlv1alpha1.AgentRunHarnessBackendSpec{
		Kind: controlv1alpha1.AgentRunHarnessBackendOpenCode,
		OpenCode: &controlv1alpha1.AgentRunOpenCodeBackendSpec{
			Model:          "openai/gpt-5.4",
			Format:         "json",
			Auto:           &profileAuto,
			Pure:           &profilePure,
			AdditionalArgs: []string{"--thinking"},
		},
	}
	run := controlv1alpha1.AgentRunHarnessBackendSpec{
		OpenCode: &controlv1alpha1.AgentRunOpenCodeBackendSpec{
			Agent:          "review",
			Variant:        "high",
			Auto:           &runAuto,
			Pure:           &runPure,
			AdditionalArgs: []string{"--title=review"},
		},
	}

	merged := agentRunMergeBackend(profile, run)
	if merged.OpenCode == nil {
		t.Fatal("OpenCode backend was not merged")
	}
	if got, want := merged.OpenCode.Model, "openai/gpt-5.4"; got != want {
		t.Fatalf("OpenCode model = %q, want %q", got, want)
	}
	if got, want := merged.OpenCode.Agent, "review"; got != want {
		t.Fatalf("OpenCode agent = %q, want %q", got, want)
	}
	if got, want := merged.OpenCode.Variant, "high"; got != want {
		t.Fatalf("OpenCode variant = %q, want %q", got, want)
	}
	if merged.OpenCode.Auto == nil || *merged.OpenCode.Auto {
		t.Fatalf("OpenCode auto = %#v, want false", merged.OpenCode.Auto)
	}
	if merged.OpenCode.Pure == nil || *merged.OpenCode.Pure {
		t.Fatalf("OpenCode pure = %#v, want false", merged.OpenCode.Pure)
	}
	if got, want := strings.Join(merged.OpenCode.AdditionalArgs, ","), "--thinking,--title=review"; got != want {
		t.Fatalf("OpenCode additional args = %q, want %q", got, want)
	}
}

func TestAgentRunMergeBackendMergesPiAgent(t *testing.T) {
	t.Parallel()

	profile := controlv1alpha1.AgentRunHarnessBackendSpec{
		Kind:             controlv1alpha1.AgentRunHarnessBackendPiAgent,
		ModelProvider:    controlv1alpha1.AgentRunModelProviderXAI,
		ProviderAuthMode: controlv1alpha1.AgentRunProviderAuthModeOAuth,
		PiAgent: &controlv1alpha1.AgentRunPiBackendSpec{
			Provider:       "xai-auth",
			Model:          "grok-4.5",
			Thinking:       "high",
			Mode:           "text",
			AdditionalArgs: []string{"--tools", "read,grep,find,ls"},
		},
	}
	run := controlv1alpha1.AgentRunHarnessBackendSpec{
		PiAgent: &controlv1alpha1.AgentRunPiBackendSpec{
			Model:          "grok-composer-2.5-fast",
			Thinking:       "medium",
			NoSession:      true,
			AdditionalArgs: []string{"--exclude-tools", "write"},
		},
	}

	merged := agentRunMergeBackend(profile, run)
	if got, want := merged.Kind, controlv1alpha1.AgentRunHarnessBackendPiAgent; got != want {
		t.Fatalf("kind = %q, want %q", got, want)
	}
	if merged.PiAgent == nil {
		t.Fatal("PiAgent backend was not merged")
	}
	if got, want := merged.PiAgent.Provider, "xai-auth"; got != want {
		t.Fatalf("Pi provider = %q, want %q", got, want)
	}
	if got, want := merged.PiAgent.Model, "grok-composer-2.5-fast"; got != want {
		t.Fatalf("Pi model = %q, want %q", got, want)
	}
	if got, want := merged.PiAgent.Thinking, "medium"; got != want {
		t.Fatalf("Pi thinking = %q, want %q", got, want)
	}
	if got, want := merged.PiAgent.Mode, "text"; got != want {
		t.Fatalf("Pi mode = %q, want %q", got, want)
	}
	if !merged.PiAgent.NoSession {
		t.Fatal("Pi noSession = false, want true")
	}
	if got, want := strings.Join(merged.PiAgent.AdditionalArgs, ","), "--tools,read,grep,find,ls,--exclude-tools,write"; got != want {
		t.Fatalf("Pi additional args = %q, want %q", got, want)
	}
}

func TestAgentDataVolumeMountPathDefaultsForAgentBackends(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		backend controlv1alpha1.AgentRunHarnessBackendKind
		want    string
	}{
		{name: "grok build", backend: controlv1alpha1.AgentRunHarnessBackendGrokBuild, want: "/opt/anvil/grok-build"},
		{name: "opencode", backend: controlv1alpha1.AgentRunHarnessBackendOpenCode, want: "/opt/anvil/opencode"},
		{name: "pi agent", backend: controlv1alpha1.AgentRunHarnessBackendPiAgent, want: "/opt/anvil/pi"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			volume := &controlv1alpha1.AgentDataVolume{
				Spec: controlv1alpha1.AgentDataVolumeSpec{
					Backend: tc.backend,
				},
			}
			if got := agentDataVolumeMountPath(volume); got != tc.want {
				t.Fatalf("data volume mount path = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAgentRunJobDoesNotInjectUndeclaredFeedbackSecret(t *testing.T) {
	t.Parallel()

	run := &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "platform-health",
			Namespace: "anvil",
		},
	}

	job := agentRunJob(run, "platform-health-harness", "platform-health-context", nil)
	envFrom := job.Spec.Template.Spec.Containers[0].EnvFrom
	if len(envFrom) != 0 {
		t.Fatalf("envFrom = %#v, want no undeclared Secret injection", envFrom)
	}
}

func TestAgentRunJobUsesOnlyExplicitFeedbackSecret(t *testing.T) {
	t.Parallel()

	run := &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "platform-health",
			Namespace: "anvil",
		},
		Spec: controlv1alpha1.AgentRunSpec{
			Harness: controlv1alpha1.AgentRunHarnessSpec{
				Execution: controlv1alpha1.AgentRunHarnessExecutionSpec{
					EnvSecretRefs: []controlv1alpha1.NamespacedObjectReference{
						{Name: "codex-credentials"},
						{Name: "agent-feedback-discord"},
					},
				},
			},
		},
	}

	job := agentRunJob(run, "platform-health-harness", "platform-health-context", nil)
	envFrom := job.Spec.Template.Spec.Containers[0].EnvFrom
	if len(envFrom) != 2 {
		t.Fatalf("envFrom = %#v, want explicit secrets only", envFrom)
	}
	if got, want := envFrom[0].SecretRef.Name, "codex-credentials"; got != want {
		t.Fatalf("first envFrom secret = %q, want %q", got, want)
	}
	feedback := envFrom[1].SecretRef
	if feedback == nil || feedback.Name != "agent-feedback-discord" {
		t.Fatalf("feedback envFrom secret = %#v, want agent-feedback-discord", feedback)
	}
	if feedback.Optional != nil {
		t.Fatalf("explicit feedback secret optional = %#v, want nil", feedback.Optional)
	}
}

func TestAgentRunPodLaunchFailureMessageDetectsImagePull(t *testing.T) {
	t.Parallel()

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "profile-backed-run-harness",
			Namespace: "hazy-trade",
		},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{{
				Name: "agent",
				State: corev1.ContainerState{
					Waiting: &corev1.ContainerStateWaiting{
						Reason:  "ErrImagePull",
						Message: "failed to authorize: 401 Unauthorized",
					},
				},
			}},
		},
	}

	message := agentRunPodLaunchFailureMessage(pod)
	for _, want := range []string{
		"hazy-trade/profile-backed-run-harness",
		`container "agent"`,
		"401 Unauthorized",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("launch failure message missing %q: %q", want, message)
		}
	}
}

func TestAgentRunDeletesJobAfterLaunchFailure(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := batchv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add batch scheme: %v", err)
	}
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "failed-harness",
			Namespace: "anvilhub",
		},
	}
	reconciler := &AgentRunReconciler{
		Client: fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(job).
			Build(),
		Scheme: scheme,
	}

	if err := reconciler.deleteAgentRunJobAfterLaunchFailure(ctx, job); err != nil {
		t.Fatalf("delete launch-failed job: %v", err)
	}
	if err := reconciler.Get(ctx, types.NamespacedName{Name: job.Name, Namespace: job.Namespace}, &batchv1.Job{}); !apierrors.IsNotFound(err) {
		t.Fatalf("job lookup error = %v, want NotFound", err)
	}
	if err := reconciler.deleteAgentRunJobAfterLaunchFailure(ctx, job); err != nil {
		t.Fatalf("second delete should ignore missing job: %v", err)
	}
}

func TestAgentRunActivatesJobTTLOnlyAfterTerminalStatus(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := batchv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add batch scheme: %v", err)
	}
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "completed-harness",
			Namespace: "agents",
			Annotations: map[string]string{
				agentRunAnnotationRequestedTTL: "300",
			},
		},
	}
	reconciler := &AgentRunReconciler{Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(job).Build(), Scheme: scheme}
	run := &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{Name: "completed", Namespace: "agents"},
		Status: controlv1alpha1.AgentRunStatus{
			Phase:  controlv1alpha1.AgentRunPhaseSucceeded,
			JobRef: &controlv1alpha1.NamespacedObjectReference{Name: job.Name},
		},
	}

	if err := reconciler.ensureTerminalAgentRunJobTTL(ctx, run); err != nil {
		t.Fatalf("activate terminal job TTL: %v", err)
	}
	updated := &batchv1.Job{}
	if err := reconciler.Get(ctx, types.NamespacedName{Name: job.Name, Namespace: job.Namespace}, updated); err != nil {
		t.Fatalf("get updated job: %v", err)
	}
	if updated.Spec.TTLSecondsAfterFinished == nil || *updated.Spec.TTLSecondsAfterFinished != 300 {
		t.Fatalf("job TTL = %#v, want 300", updated.Spec.TTLSecondsAfterFinished)
	}
}

func TestAgentRunReconcileUsesExistingStatusJobRef(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
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

	run := &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "platform-health",
			Namespace:  "anvilhub",
			UID:        "platform-health-uid",
			Generation: 1,
		},
		Spec: controlv1alpha1.AgentRunSpec{
			SourceRef: controlv1alpha1.AgentRunSourceRef{
				Kind: "AgentSchedule",
				Name: "platform-health-30m",
			},
			Harness: controlv1alpha1.AgentRunHarnessSpec{
				Intent: controlv1alpha1.AgentRunIntentProposeChange,
			},
		},
		Status: controlv1alpha1.AgentRunStatus{
			ObservedGeneration: 1,
			Phase:              controlv1alpha1.AgentRunPhaseRunning,
			PromptHash:         "oldhash",
			JobRef: &controlv1alpha1.NamespacedObjectReference{
				Name:      "platform-health-harness-oldhash",
				Namespace: "anvilhub",
			},
		},
	}
	controller := true
	existingJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "platform-health-harness-oldhash",
			Namespace: "anvilhub",
			UID:       types.UID("platform-health-job-uid"),
			Labels: map[string]string{
				agentRunJobLabel: "platform-health-harness-oldhash",
			},
			OwnerReferences: []metav1.OwnerReference{{APIVersion: controlv1alpha1.GroupVersion.String(), Kind: "AgentRun", Name: run.Name, UID: run.UID, Controller: &controller}},
		},
		Status: batchv1.JobStatus{
			Active: 1,
		},
		Spec: batchv1.JobSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{
			Name: "agent",
			Env: []corev1.EnvVar{{
				Name:  "ANVIL_AGENT_RUN_DATA_VOLUMES_JSON",
				Value: `[{"name":"home","namespace":"anvilhub","claimName":"agent-home","mountPath":"/home/agent"}]`,
			}},
		}}, RestartPolicy: corev1.RestartPolicyNever}}},
	}
	reconciler := &AgentRunReconciler{
		Client: fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(run, existingJob).
			WithStatusSubresource(run).
			Build(),
		Scheme: scheme,
	}

	if _, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: run.Name, Namespace: run.Namespace}}); err != nil {
		t.Fatalf("reconcile agent run: %v", err)
	}

	jobs := &batchv1.JobList{}
	if err := reconciler.List(ctx, jobs); err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if got, want := len(jobs.Items), 1; got != want {
		t.Fatalf("jobs = %d, want %d: %#v", got, want, jobs.Items)
	}
	if got, want := jobs.Items[0].Name, existingJob.Name; got != want {
		t.Fatalf("job name = %q, want %q", got, want)
	}
	updated := &controlv1alpha1.AgentRun{}
	if err := reconciler.Get(ctx, types.NamespacedName{Name: run.Name, Namespace: run.Namespace}, updated); err != nil {
		t.Fatalf("get updated run: %v", err)
	}
	if updated.Status.JobRef == nil || updated.Status.JobRef.Name != existingJob.Name {
		t.Fatalf("updated job ref = %#v, want %s", updated.Status.JobRef, existingJob.Name)
	}
	if updated.Status.JobUID != string(existingJob.UID) {
		t.Fatalf("updated Job UID = %q, want %q", updated.Status.JobUID, existingJob.UID)
	}
	if got, want := updated.Status.PromptHash, "oldhash"; got != want {
		t.Fatalf("prompt hash = %q, want %q", got, want)
	}
	if len(updated.Status.DataVolumes) != 1 || updated.Status.DataVolumes[0].ClaimName != "agent-home" {
		t.Fatalf("recovered data volumes = %#v", updated.Status.DataVolumes)
	}
}

func TestAgentRunReconcilePreservesJobRefAcrossGenerationChange(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
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

	run := &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "platform-health",
			Namespace:  "anvilhub",
			UID:        "platform-health-uid",
			Generation: 2,
		},
		Spec: controlv1alpha1.AgentRunSpec{
			SourceRef: controlv1alpha1.AgentRunSourceRef{
				Kind: "AgentSchedule",
				Name: "platform-health-30m",
			},
			Prompt: "updated prompt text that bumps generation",
			Harness: controlv1alpha1.AgentRunHarnessSpec{
				Intent: controlv1alpha1.AgentRunIntentProposeChange,
			},
		},
		Status: controlv1alpha1.AgentRunStatus{
			ObservedGeneration: 1,
			Phase:              controlv1alpha1.AgentRunPhaseRunning,
			PromptHash:         "oldhash",
			JobUID:             "platform-health-job-uid",
			JobRef: &controlv1alpha1.NamespacedObjectReference{
				Name:      "platform-health-harness-oldhash",
				Namespace: "anvilhub",
			},
		},
	}
	controller := true
	existingJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "platform-health-harness-oldhash",
			Namespace: "anvilhub",
			UID:       types.UID("platform-health-job-uid"),
			Labels: map[string]string{
				agentRunJobLabel: "platform-health-harness-oldhash",
			},
			OwnerReferences: []metav1.OwnerReference{{APIVersion: controlv1alpha1.GroupVersion.String(), Kind: "AgentRun", Name: run.Name, UID: run.UID, Controller: &controller}},
		},
		Status: batchv1.JobStatus{
			Active: 1,
		},
	}
	reconciler := &AgentRunReconciler{
		Client: fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(run, existingJob).
			WithStatusSubresource(run).
			Build(),
		Scheme: scheme,
	}

	if _, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: run.Name, Namespace: run.Namespace}}); err != nil {
		t.Fatalf("reconcile agent run: %v", err)
	}

	jobs := &batchv1.JobList{}
	if err := reconciler.List(ctx, jobs); err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if got, want := len(jobs.Items), 1; got != want {
		t.Fatalf("jobs = %d, want %d: %#v", got, want, jobs.Items)
	}
	if got, want := jobs.Items[0].Name, existingJob.Name; got != want {
		t.Fatalf("job name = %q, want %q", got, want)
	}
	updated := &controlv1alpha1.AgentRun{}
	if err := reconciler.Get(ctx, types.NamespacedName{Name: run.Name, Namespace: run.Namespace}, updated); err != nil {
		t.Fatalf("get updated run: %v", err)
	}
	if updated.Status.JobRef == nil || updated.Status.JobRef.Name != existingJob.Name {
		t.Fatalf("updated job ref = %#v, want %s", updated.Status.JobRef, existingJob.Name)
	}
	if updated.Status.ObservedGeneration != 2 {
		t.Fatalf("observed generation = %d, want 2", updated.Status.ObservedGeneration)
	}
}

func TestAgentRunJobMountsDataVolumesAndEnv(t *testing.T) {
	t.Parallel()

	ttlSecondsAfterFinished := int32(600)
	run := &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "platform-health",
			Namespace: "anvil-system",
		},
		Spec: controlv1alpha1.AgentRunSpec{
			SourceRef: controlv1alpha1.AgentRunSourceRef{
				Kind: "AgentSchedule",
				Name: "platform-health-hourly",
			},
			Harness: controlv1alpha1.AgentRunHarnessSpec{
				Backend: controlv1alpha1.AgentRunHarnessBackendSpec{
					Kind: controlv1alpha1.AgentRunHarnessBackendCodex,
				},
				Execution: controlv1alpha1.AgentRunHarnessExecutionSpec{
					NodeSelector:            map[string]string{"kubernetes.io/arch": "amd64"},
					TTLSecondsAfterFinished: &ttlSecondsAfterFinished,
				},
			},
		},
	}
	dataVolumes := []resolvedAgentRunDataVolume{{
		Name:      "anvil-codex-home",
		Namespace: "anvil-system",
		ClaimName: "agent-data-anvil-codex-home",
		MountPath: "/codex-home",
		ExtraEnv: []corev1.EnvVar{{
			Name:  "CODEX_HOME",
			Value: "/codex-home",
		}},
		NodeSelector: map[string]string{
			"hazyforge.io/home-lab": "true",
			"hazyforge.io/storage":  "observability-local",
		},
	}}

	job := agentRunJob(run, "platform-health-harness", "platform-health-context", dataVolumes)
	if job.Spec.TTLSecondsAfterFinished != nil {
		t.Fatalf("job TTL must remain disabled until terminal status is durable: %#v", job.Spec.TTLSecondsAfterFinished)
	}
	if got := job.Annotations[agentRunAnnotationRequestedTTL]; got != "600" {
		t.Fatalf("requested TTL annotation = %q, want 600", got)
	}
	podSpec := job.Spec.Template.Spec
	if got, want := podSpec.NodeSelector["hazyforge.io/home-lab"], "true"; got != want {
		t.Fatalf("home-lab node selector = %q, want %q", got, want)
	}
	if got, want := podSpec.NodeSelector["kubernetes.io/arch"], "amd64"; got != want {
		t.Fatalf("arch node selector = %q, want %q", got, want)
	}
	if len(podSpec.Volumes) != 4 {
		t.Fatalf("volumes = %d, want payload, capability runtime, data volume, and ephemeral tool cache", len(podSpec.Volumes))
	}
	dataVolume := podSpec.Volumes[2]
	if dataVolume.PersistentVolumeClaim == nil || dataVolume.PersistentVolumeClaim.ClaimName != "agent-data-anvil-codex-home" {
		t.Fatalf("data volume PVC = %#v, want agent-data-anvil-codex-home", dataVolume.PersistentVolumeClaim)
	}
	mounts := podSpec.Containers[0].VolumeMounts
	if len(mounts) != 4 || mounts[2].MountPath != "/codex-home" || mounts[3].MountPath != agentRunToolCacheMountPath {
		t.Fatalf("volume mounts = %#v, want data volume at /codex-home", mounts)
	}
	env := map[string]string{}
	for _, item := range podSpec.Containers[0].Env {
		env[item.Name] = item.Value
	}
	if got, want := env["CODEX_HOME"], "/codex-home"; got != want {
		t.Fatalf("CODEX_HOME = %q, want %q", got, want)
	}
	if got := env["ANVIL_AGENT_RUN_DATA_VOLUMES_JSON"]; !strings.Contains(got, "agent-data-anvil-codex-home") {
		t.Fatalf("ANVIL_AGENT_RUN_DATA_VOLUMES_JSON = %q, want claim summary", got)
	}
}

func TestResolveAgentRunDataVolumesRejectsBlockedDriftAndUsesResolvedClaim(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name       string
		generation int64
		observed   int64
		phase      controlv1alpha1.AgentDataVolumePhase
		wantPhase  controlv1alpha1.AgentRunPhase
		wantReason string
		wantClaim  string
	}{
		{name: "blocked drift", generation: 1, observed: 1, phase: controlv1alpha1.AgentDataVolumePhaseBlocked, wantPhase: controlv1alpha1.AgentRunPhaseNeedsHuman, wantReason: "DataVolumeBlocked"},
		{name: "resolved identity", generation: 1, observed: 1, phase: controlv1alpha1.AgentDataVolumePhaseReady, wantClaim: "accepted-claim"},
		{name: "stale block waits", generation: 2, observed: 1, phase: controlv1alpha1.AgentDataVolumePhaseBlocked, wantPhase: controlv1alpha1.AgentRunPhasePending, wantReason: "DataVolumeStatusStale"},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			scheme := runtime.NewScheme()
			if err := controlv1alpha1.AddToScheme(scheme); err != nil {
				t.Fatalf("add control scheme: %v", err)
			}
			if err := corev1.AddToScheme(scheme); err != nil {
				t.Fatalf("add core scheme: %v", err)
			}
			controller := true
			volume := &controlv1alpha1.AgentDataVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "agent-home", Namespace: "anvilhub", UID: "agent-home-uid", Generation: test.generation},
				Spec:       controlv1alpha1.AgentDataVolumeSpec{ClaimName: "rejected-spec-claim", MountPath: "/agent-home"},
				Status: controlv1alpha1.AgentDataVolumeStatus{
					ObservedGeneration: test.observed,
					Phase:              test.phase,
					LastError:          "immutable claim drift",
					ClaimRef:           &controlv1alpha1.NamespacedObjectReference{Name: "accepted-claim", Namespace: "anvilhub"},
					ClaimUID:           "accepted-claim-uid",
				},
			}
			pvc := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{
				Name: "accepted-claim", Namespace: "anvilhub", UID: "accepted-claim-uid",
				OwnerReferences: []metav1.OwnerReference{{APIVersion: controlv1alpha1.GroupVersion.String(), Kind: "AgentDataVolume", Name: volume.Name, UID: volume.UID, Controller: &controller}},
			}}
			run := &controlv1alpha1.AgentRun{
				ObjectMeta: metav1.ObjectMeta{Name: "run", Namespace: "anvilhub"},
				Spec: controlv1alpha1.AgentRunSpec{Harness: controlv1alpha1.AgentRunHarnessSpec{Execution: controlv1alpha1.AgentRunHarnessExecutionSpec{
					DataVolumeRefs: []controlv1alpha1.AgentRunDataVolumeRef{{Name: volume.Name}},
				}}},
			}
			reconciler := &AgentRunReconciler{Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(volume, pvc).Build(), Scheme: scheme}
			resolved, phase, reason, _, err := reconciler.resolveAgentRunDataVolumes(ctx, run)
			if err != nil {
				t.Fatalf("resolve data volume: %v", err)
			}
			if phase != test.wantPhase || reason != test.wantReason {
				t.Fatalf("phase/reason = %q/%q, want %q/%q", phase, reason, test.wantPhase, test.wantReason)
			}
			if test.wantClaim != "" && (len(resolved) != 1 || resolved[0].ClaimName != test.wantClaim) {
				t.Fatalf("resolved volumes = %#v, want claim %q", resolved, test.wantClaim)
			}
		})
	}
}

func TestResolveAgentRunDataVolumesAllowsWaitForFirstConsumerClaim(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := controlv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add control scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	if err := storagev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add storage scheme: %v", err)
	}
	storageClassName := "local-path"
	bindingMode := storagev1.VolumeBindingWaitForFirstConsumer
	controller := true
	volume := &controlv1alpha1.AgentDataVolume{
		ObjectMeta: metav1.ObjectMeta{Name: "agent-home", Namespace: "agents", UID: "agent-home-uid", Generation: 1},
		Spec:       controlv1alpha1.AgentDataVolumeSpec{MountPath: "/agent-home"},
		Status: controlv1alpha1.AgentDataVolumeStatus{
			ObservedGeneration: 1,
			Phase:              controlv1alpha1.AgentDataVolumePhasePending,
			ClaimRef:           &controlv1alpha1.NamespacedObjectReference{Name: "agent-data-agent-home", Namespace: "agents"},
			ClaimUID:           "agent-home-pvc-uid",
			Conditions: []metav1.Condition{{
				Type: "Ready", Status: metav1.ConditionFalse, Reason: "ClaimPending",
			}},
		},
	}
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "agent-data-agent-home", Namespace: "agents", UID: "agent-home-pvc-uid", OwnerReferences: []metav1.OwnerReference{{APIVersion: controlv1alpha1.GroupVersion.String(), Kind: "AgentDataVolume", Name: volume.Name, UID: volume.UID, Controller: &controller}}},
		Spec:       corev1.PersistentVolumeClaimSpec{StorageClassName: &storageClassName},
		Status:     corev1.PersistentVolumeClaimStatus{Phase: corev1.ClaimPending},
	}
	storageClass := &storagev1.StorageClass{
		ObjectMeta:        metav1.ObjectMeta{Name: storageClassName},
		VolumeBindingMode: &bindingMode,
	}
	run := &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{Name: "run", Namespace: "agents"},
		Spec: controlv1alpha1.AgentRunSpec{Harness: controlv1alpha1.AgentRunHarnessSpec{Execution: controlv1alpha1.AgentRunHarnessExecutionSpec{
			DataVolumeRefs: []controlv1alpha1.AgentRunDataVolumeRef{{Name: volume.Name}},
		}}},
	}
	reconciler := &AgentRunReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(volume, pvc, storageClass).Build(),
		Scheme: scheme,
	}

	resolved, phase, reason, _, err := reconciler.resolveAgentRunDataVolumes(ctx, run)
	if err != nil {
		t.Fatalf("resolve WFFC data volume: %v", err)
	}
	if phase != "" || reason != "" || len(resolved) != 1 || resolved[0].ClaimName != pvc.Name {
		t.Fatalf("resolved=%#v phase/reason=%q/%q, want pending WFFC claim accepted", resolved, phase, reason)
	}
}

func TestResolveAgentRunDataVolumesUsesResolvedProfileStatus(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := controlv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add control scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	controller := true
	volume := &controlv1alpha1.AgentDataVolume{
		ObjectMeta: metav1.ObjectMeta{Name: "agent-home", Namespace: "anvilhub", UID: "agent-home-uid", Generation: 1},
		Spec: controlv1alpha1.AgentDataVolumeSpec{
			ClaimName: "agent-home",
			MountPath: "/legacy-home",
		},
		Status: controlv1alpha1.AgentDataVolumeStatus{
			ObservedGeneration: 1,
			Phase:              controlv1alpha1.AgentDataVolumePhaseReady,
			ClaimRef:           &controlv1alpha1.NamespacedObjectReference{Name: "agent-home", Namespace: "anvilhub"},
			ClaimUID:           "agent-home-pvc-uid",
			MountPath:          "/profile-home",
			SubPath:            "state",
			NodeSelector:       map[string]string{"hazyforge.io/storage": "observability-local"},
			ExtraEnv:           []controlv1alpha1.AgentDataVolumePathEnvVar{{Name: "CODEX_HOME", Value: "/profile-home/codex"}},
		},
	}
	pvc := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{
		Name: "agent-home", Namespace: "anvilhub", UID: "agent-home-pvc-uid",
		OwnerReferences: []metav1.OwnerReference{{APIVersion: controlv1alpha1.GroupVersion.String(), Kind: "AgentDataVolume", Name: volume.Name, UID: volume.UID, Controller: &controller}},
	}}
	run := &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{Name: "run", Namespace: "anvilhub"},
		Spec: controlv1alpha1.AgentRunSpec{Harness: controlv1alpha1.AgentRunHarnessSpec{Execution: controlv1alpha1.AgentRunHarnessExecutionSpec{
			DataVolumeRefs: []controlv1alpha1.AgentRunDataVolumeRef{{Name: volume.Name}},
		}}},
	}
	reconciler := &AgentRunReconciler{Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(volume, pvc).Build(), Scheme: scheme}
	resolved, phase, reason, _, err := reconciler.resolveAgentRunDataVolumes(ctx, run)
	if err != nil {
		t.Fatalf("resolve data volume: %v", err)
	}
	if phase != "" || reason != "" || len(resolved) != 1 {
		t.Fatalf("resolved=%#v phase/reason=%q/%q, want one ready volume", resolved, phase, reason)
	}
	if resolved[0].MountPath != "/profile-home" || resolved[0].SubPath != "state" {
		t.Fatalf("resolved paths = %q/%q, want profile status paths", resolved[0].MountPath, resolved[0].SubPath)
	}
	if resolved[0].NodeSelector["hazyforge.io/storage"] != "observability-local" {
		t.Fatalf("node selector = %#v, want resolved profile selector", resolved[0].NodeSelector)
	}
	if len(resolved[0].ExtraEnv) != 1 || resolved[0].ExtraEnv[0].Value != "/profile-home/codex" {
		t.Fatalf("extra env = %#v, want resolved profile env", resolved[0].ExtraEnv)
	}
}

func TestResolveAgentRunDataVolumesRejectsReplacementClaim(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := controlv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add control scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	controller := true
	volume := &controlv1alpha1.AgentDataVolume{
		ObjectMeta: metav1.ObjectMeta{Name: "agent-home", Namespace: "agents", UID: "volume-uid", Generation: 1},
		Spec:       controlv1alpha1.AgentDataVolumeSpec{ClaimName: "agent-home", MountPath: "/agent-home"},
		Status: controlv1alpha1.AgentDataVolumeStatus{
			ObservedGeneration: 1, Phase: controlv1alpha1.AgentDataVolumePhaseReady,
			ClaimRef: &controlv1alpha1.NamespacedObjectReference{Name: "agent-home", Namespace: "agents"}, ClaimUID: "original-claim-uid",
		},
	}
	pvc := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{
		Name: "agent-home", Namespace: "agents", UID: "replacement-claim-uid",
		OwnerReferences: []metav1.OwnerReference{{APIVersion: controlv1alpha1.GroupVersion.String(), Kind: "AgentDataVolume", Name: volume.Name, UID: volume.UID, Controller: &controller}},
	}}
	run := &controlv1alpha1.AgentRun{ObjectMeta: metav1.ObjectMeta{Name: "run", Namespace: "agents"}, Spec: controlv1alpha1.AgentRunSpec{Harness: controlv1alpha1.AgentRunHarnessSpec{Execution: controlv1alpha1.AgentRunHarnessExecutionSpec{DataVolumeRefs: []controlv1alpha1.AgentRunDataVolumeRef{{Name: volume.Name}}}}}}
	reconciler := &AgentRunReconciler{Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(volume, pvc).Build(), Scheme: scheme}
	_, phase, reason, _, err := reconciler.resolveAgentRunDataVolumes(ctx, run)
	if err != nil {
		t.Fatalf("resolve replacement claim: %v", err)
	}
	if phase != controlv1alpha1.AgentRunPhaseFailed || reason != "DataVolumeClaimReplaced" {
		t.Fatalf("phase/reason = %q/%q, want Failed/DataVolumeClaimReplaced", phase, reason)
	}
}

func TestResolveAgentRunDataVolumesRejectsCrossApplicationVolume(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := controlv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add control scheme: %v", err)
	}
	volume := &controlv1alpha1.AgentDataVolume{
		ObjectMeta: metav1.ObjectMeta{Name: "other-home", Namespace: "hazy-trade", Generation: 1},
		Spec: controlv1alpha1.AgentDataVolumeSpec{
			ApplicationRef: &controlv1alpha1.ApplicationReferenceSpec{Name: "other-app"},
			ClaimName:      "other-home",
		},
		Status: controlv1alpha1.AgentDataVolumeStatus{
			ObservedGeneration: 1,
			Phase:              controlv1alpha1.AgentDataVolumePhaseReady,
			ClaimRef:           &controlv1alpha1.NamespacedObjectReference{Name: "other-home", Namespace: "hazy-trade"},
		},
	}
	run := &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{Name: "run", Namespace: "hazy-trade"},
		Spec: controlv1alpha1.AgentRunSpec{
			Scope: controlv1alpha1.AgentRunScopeSpec{ApplicationRef: &controlv1alpha1.ApplicationReferenceSpec{Name: "hazy-trade"}},
			Harness: controlv1alpha1.AgentRunHarnessSpec{Execution: controlv1alpha1.AgentRunHarnessExecutionSpec{
				DataVolumeRefs: []controlv1alpha1.AgentRunDataVolumeRef{{Name: volume.Name}},
			}},
		},
	}
	reconciler := &AgentRunReconciler{Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(volume).Build(), Scheme: scheme}
	_, phase, reason, _, err := reconciler.resolveAgentRunDataVolumes(ctx, run)
	if err != nil {
		t.Fatalf("resolve data volume: %v", err)
	}
	if phase != controlv1alpha1.AgentRunPhaseFailed || reason != "DataVolumeApplicationMismatch" {
		t.Fatalf("phase/reason = %q/%q, want Failed/DataVolumeApplicationMismatch", phase, reason)
	}
}

func TestAgentRunJobMountsSPIFFEWorkloadAPI(t *testing.T) {
	t.Parallel()

	const expectedID = "spiffe://anvil.hazyforge.io/workload/hazy-trade/agent-run"
	run := &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hazy-trade-tests",
			Namespace: "hazy-trade",
		},
		Spec: controlv1alpha1.AgentRunSpec{
			SourceRef: controlv1alpha1.AgentRunSourceRef{Kind: "AgentSchedule", Name: "hazy-trade-tests"},
			Harness: controlv1alpha1.AgentRunHarnessSpec{
				Execution: controlv1alpha1.AgentRunHarnessExecutionSpec{
					ServiceAccountName: "hazy-trade-agent-run",
					SpiffeWorkloadAPI: controlv1alpha1.AgentRunSpiffeWorkloadAPISpec{
						Enabled:  true,
						SPIFFEID: expectedID,
					},
				},
			},
		},
	}

	job := agentRunJob(run, "hazy-trade-tests-harness", "hazy-trade-tests-context", nil)
	pod := job.Spec.Template.Spec
	if got, want := len(pod.Volumes), 4; got != want {
		t.Fatalf("volumes = %d, want %d", got, want)
	}
	spiffeVolume := pod.Volumes[3]
	if spiffeVolume.Name != agentRunSpiffeWorkloadAPIVolume || spiffeVolume.CSI == nil {
		t.Fatalf("SPIFFE volume = %#v, want CSI volume", spiffeVolume)
	}
	if got, want := spiffeVolume.CSI.Driver, agentRunSpiffeCSIDriver; got != want {
		t.Fatalf("SPIFFE CSI driver = %q, want %q", got, want)
	}
	if spiffeVolume.CSI.ReadOnly == nil || !*spiffeVolume.CSI.ReadOnly {
		t.Fatalf("SPIFFE CSI readOnly = %#v, want true", spiffeVolume.CSI.ReadOnly)
	}
	mounts := pod.Containers[0].VolumeMounts
	if got, want := mounts[len(mounts)-1].MountPath, agentRunSpiffeWorkloadAPIMountPath; got != want {
		t.Fatalf("SPIFFE mount path = %q, want %q", got, want)
	}
	env := map[string]string{}
	for _, item := range pod.Containers[0].Env {
		env[item.Name] = item.Value
	}
	if got, want := env["SPIFFE_ENDPOINT_SOCKET"], "unix://"+agentRunSpiffeWorkloadAPISocket; got != want {
		t.Fatalf("SPIFFE_ENDPOINT_SOCKET = %q, want %q", got, want)
	}
	if got := env["ANVIL_AGENT_RUN_SPIFFE_ID"]; got != expectedID {
		t.Fatalf("ANVIL_AGENT_RUN_SPIFFE_ID = %q, want %q", got, expectedID)
	}
	labels := job.Spec.Template.Labels
	if labels[agentRunLabelSpiffeWorkloadAPI] != "true" {
		t.Fatalf("SPIFFE workload label = %q, want true", labels[agentRunLabelSpiffeWorkloadAPI])
	}
	if got, want := labels[agentRunLabelServiceAccount], "hazy-trade-agent-run"; got != want {
		t.Fatalf("service account label = %q, want %q", got, want)
	}
}

func TestAgentRunSPIFFEWorkloadAPIValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		spec   controlv1alpha1.AgentRunSpiffeWorkloadAPISpec
		reason string
	}{
		{name: "id without enablement", spec: controlv1alpha1.AgentRunSpiffeWorkloadAPISpec{SPIFFEID: "spiffe://anvil.hazyforge.io/workload/hazy-trade/agent-run"}, reason: "InvalidSPIFFEWorkloadAPI"},
		{name: "missing id", spec: controlv1alpha1.AgentRunSpiffeWorkloadAPISpec{Enabled: true}, reason: "InvalidSPIFFEWorkloadAPI"},
		{name: "malformed id", spec: controlv1alpha1.AgentRunSpiffeWorkloadAPISpec{Enabled: true, SPIFFEID: "not-a-spiffe-id"}, reason: "InvalidSPIFFEWorkloadAPI"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			run := &controlv1alpha1.AgentRun{Spec: controlv1alpha1.AgentRunSpec{
				SourceRef: controlv1alpha1.AgentRunSourceRef{Kind: "AgentSchedule", Name: "tests"},
				Harness:   controlv1alpha1.AgentRunHarnessSpec{Execution: controlv1alpha1.AgentRunHarnessExecutionSpec{SpiffeWorkloadAPI: test.spec}},
			}}
			phase, reason, _ := agentRunBlockingValidation(run)
			if phase != controlv1alpha1.AgentRunPhaseFailed || reason != test.reason {
				t.Fatalf("validation = phase %q reason %q, want Failed/%s", phase, reason, test.reason)
			}
		})
	}
}

func TestAgentRunMergeExecutionOverridesSPIFFEWorkloadAPI(t *testing.T) {
	t.Parallel()

	profile := controlv1alpha1.AgentRunHarnessExecutionSpec{
		SpiffeWorkloadAPI: controlv1alpha1.AgentRunSpiffeWorkloadAPISpec{
			Enabled:  true,
			SPIFFEID: "spiffe://anvil.hazyforge.io/workload/profile/agent-run",
		},
	}
	run := controlv1alpha1.AgentRunHarnessExecutionSpec{
		SpiffeWorkloadAPI: controlv1alpha1.AgentRunSpiffeWorkloadAPISpec{
			Enabled:  true,
			SPIFFEID: "spiffe://anvil.hazyforge.io/workload/hazy-trade/agent-run",
		},
	}
	merged := agentRunMergeExecution(profile, run)
	if got, want := merged.SpiffeWorkloadAPI.SPIFFEID, run.SpiffeWorkloadAPI.SPIFFEID; got != want {
		t.Fatalf("merged SPIFFE ID = %q, want %q", got, want)
	}
}

func TestAgentRunMergeExecutionOverridesResourcesAndJobUsesThem(t *testing.T) {
	t.Parallel()

	profile := controlv1alpha1.AgentRunHarnessExecutionSpec{Resources: corev1.ResourceRequirements{
		Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m"), corev1.ResourceMemory: resource.MustParse("128Mi")},
		Limits:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("500m"), corev1.ResourceMemory: resource.MustParse("512Mi")},
	}}
	run := controlv1alpha1.AgentRunHarnessExecutionSpec{Resources: corev1.ResourceRequirements{
		Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("250m"), corev1.ResourceMemory: resource.MustParse("512Mi"), corev1.ResourceEphemeralStorage: resource.MustParse("1Gi")},
		Limits:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2"), corev1.ResourceMemory: resource.MustParse("2Gi"), corev1.ResourceEphemeralStorage: resource.MustParse("5Gi")},
	}}
	merged := agentRunMergeExecution(profile, run)
	run.Resources.Requests[corev1.ResourceCPU] = resource.MustParse("3")
	if got := merged.Resources.Requests.Cpu(); got == nil || got.Cmp(resource.MustParse("250m")) != 0 {
		t.Fatalf("merged CPU request = %v, want deep-copied 250m", got)
	}

	jobRun := &controlv1alpha1.AgentRun{Spec: controlv1alpha1.AgentRunSpec{Harness: controlv1alpha1.AgentRunHarnessSpec{Execution: merged}}}
	job := agentRunJob(jobRun, "resource-harness", "resource-context", nil)
	if got := job.Spec.Template.Spec.Containers[0].Resources.Limits.Memory(); got == nil || got.Cmp(resource.MustParse("2Gi")) != 0 {
		t.Fatalf("Job memory limit = %v, want 2Gi", got)
	}
	if got := job.Spec.Template.Spec.Containers[0].Resources.Limits.StorageEphemeral(); got == nil || got.Cmp(resource.MustParse("5Gi")) != 0 {
		t.Fatalf("Job ephemeral-storage limit = %v, want 5Gi", got)
	}
}

func TestAgentRunJobMergesRestrictedSecurityDefaults(t *testing.T) {
	t.Parallel()

	runAsUser := int64(10001)
	runAsGroup := int64(10001)
	fsGroup := int64(10001)
	run := &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "platform-health",
			Namespace: "anvil-system",
		},
		Spec: controlv1alpha1.AgentRunSpec{
			SourceRef: controlv1alpha1.AgentRunSourceRef{
				Kind: "AgentSchedule",
				Name: "platform-health-hourly",
			},
			Harness: controlv1alpha1.AgentRunHarnessSpec{
				Execution: controlv1alpha1.AgentRunHarnessExecutionSpec{
					PodSecurityContext: &corev1.PodSecurityContext{
						RunAsUser:  &runAsUser,
						RunAsGroup: &runAsGroup,
						FSGroup:    &fsGroup,
					},
					SecurityContext: &corev1.SecurityContext{
						RunAsUser:  &runAsUser,
						RunAsGroup: &runAsGroup,
					},
				},
			},
		},
	}

	job := agentRunJob(run, "platform-health-harness", "platform-health-context", nil)
	podSecurityContext := job.Spec.Template.Spec.SecurityContext
	if podSecurityContext.RunAsUser == nil || *podSecurityContext.RunAsUser != runAsUser {
		t.Fatalf("pod RunAsUser = %#v, want %d", podSecurityContext.RunAsUser, runAsUser)
	}
	if podSecurityContext.RunAsNonRoot == nil || !*podSecurityContext.RunAsNonRoot {
		t.Fatalf("pod RunAsNonRoot = %#v, want true", podSecurityContext.RunAsNonRoot)
	}
	if podSecurityContext.SeccompProfile == nil || podSecurityContext.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Fatalf("pod seccomp profile = %#v, want RuntimeDefault", podSecurityContext.SeccompProfile)
	}
	containerSecurityContext := job.Spec.Template.Spec.Containers[0].SecurityContext
	if containerSecurityContext.RunAsUser == nil || *containerSecurityContext.RunAsUser != runAsUser {
		t.Fatalf("container RunAsUser = %#v, want %d", containerSecurityContext.RunAsUser, runAsUser)
	}
	if containerSecurityContext.AllowPrivilegeEscalation == nil || *containerSecurityContext.AllowPrivilegeEscalation {
		t.Fatalf("container allowPrivilegeEscalation = %#v, want false", containerSecurityContext.AllowPrivilegeEscalation)
	}
	if containerSecurityContext.Capabilities == nil || !agentRunDropsCapability(containerSecurityContext.Capabilities.Drop, "ALL") {
		t.Fatalf("container dropped capabilities = %#v, want ALL", containerSecurityContext.Capabilities)
	}
}

func TestAgentRunStatusReportsIgnoreUnstructuredPullRequestURLs(t *testing.T) {
	t.Parallel()

	output := strings.Join([]string{
		"kubectl output includes https://github.com/HazyForge/hazy-trade/pull/123",
		`{"pullRequestURL":"https://github.com/HazyForge/anvil-primaris/pull/459"}`,
		`ANVIL_AGENT_RUN_STATUS_JSON={"type":"progress","stage":"inspect-source","summary":"Read ordinary command output."}`,
	}, "\n")

	status := controlv1alpha1.AgentRunStatus{}
	agentRunApplyStatusReports(&status, agentRunStatusReportsFromOutput(output))
	if got := status.PullRequestURL; got != "" {
		t.Fatalf("pull request url = %q, want empty", got)
	}
}

func TestAgentRunStatusReportsPreserveDecisionWhenTrimmed(t *testing.T) {
	t.Parallel()

	lines := []string{
		`ANVIL_AGENT_RUN_STATUS_JSON={"type":"decision","classification":"missing human input","action":"observe","summary":"Needs credentials.","needsHuman":true,"humanFollowUp":"Attach GitHub credentials."}`,
	}
	for i := 0; i < 40; i++ {
		lines = append(lines, `ANVIL_AGENT_RUN_STATUS_JSON={"type":"progress","stage":"poll","summary":"Still watching."}`)
	}

	reports := agentRunStatusReportsFromOutput(strings.Join(lines, "\n"))
	if len(reports) != 25 {
		t.Fatalf("reports = %d, want 25", len(reports))
	}
	if reports[0].Type != "decision" {
		t.Fatalf("first trimmed report = %#v, want preserved decision", reports[0])
	}
	if !agentRunReportsNeedHuman(reports) {
		t.Fatalf("trimmed reports should retain needsHuman decision")
	}
}

func TestAgentRunBlockingValidationRequiresFreshExternalSecretToBeInjected(t *testing.T) {
	t.Parallel()

	run := &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{Namespace: "hazy-trade"},
		Spec: controlv1alpha1.AgentRunSpec{
			SourceRef: controlv1alpha1.AgentRunSourceRef{Kind: "AgentSchedule", Name: "prod-health"},
			Harness: controlv1alpha1.AgentRunHarnessSpec{
				Backend: controlv1alpha1.AgentRunHarnessBackendSpec{Kind: controlv1alpha1.AgentRunHarnessBackendCodex},
				Execution: controlv1alpha1.AgentRunHarnessExecutionSpec{
					EnvSecretRefs: []controlv1alpha1.NamespacedObjectReference{{Name: "codex-credentials"}},
					ExternalSecretRefreshRefs: []controlv1alpha1.AgentRunExternalSecretRefreshRef{{
						Name: "hazy-trade-agent-hazytradectl-auth",
						TargetSecretRef: controlv1alpha1.NamespacedObjectReference{
							Name: "hazy-trade-agent-hazytradectl-auth",
						},
					}},
				},
			},
		},
	}

	if phase, reason, _ := agentRunBlockingValidation(run); phase != controlv1alpha1.AgentRunPhaseFailed || reason != "ExternalSecretRefreshNotInjected" {
		t.Fatalf("validation = (%q, %q), want ExternalSecretRefreshNotInjected failure", phase, reason)
	}

	run.Spec.Harness.Execution.EnvSecretRefs = append(run.Spec.Harness.Execution.EnvSecretRefs, controlv1alpha1.NamespacedObjectReference{Name: "hazy-trade-agent-hazytradectl-auth"})
	if phase, reason, message := agentRunBlockingValidation(run); phase != "" || reason != "" || message != "" {
		t.Fatalf("validation = (%q, %q, %q), want success", phase, reason, message)
	}
}

func TestAgentRunExternalSecretStatusHelpersReadReadyFreshTarget(t *testing.T) {
	t.Parallel()

	externalSecret := &unstructured.Unstructured{Object: map[string]any{
		"status": map[string]any{
			"refreshTime": "2026-07-10T15:05:03Z",
			"binding":     map[string]any{"name": "codex-runtime-credentials"},
			"conditions":  []any{map[string]any{"type": "Ready", "status": "True"}},
		},
	}}
	ready, message := agentRunExternalSecretReady(externalSecret)
	if !ready || message != "" {
		t.Fatalf("ready = (%t, %q), want true with no message", ready, message)
	}
	refreshTime, ok := agentRunExternalSecretRefreshTime(externalSecret)
	if !ok {
		t.Fatal("refresh time was not parsed")
	}
	if want := time.Date(2026, time.July, 10, 15, 5, 3, 0, time.UTC); !refreshTime.Equal(want) {
		t.Fatalf("refresh time = %s, want %s", refreshTime, want)
	}
	if got, want := agentRunExternalSecretTargetName(externalSecret, "fallback"), "codex-runtime-credentials"; got != want {
		t.Fatalf("target = %q, want %q", got, want)
	}
}

func TestAgentRunExternalSecretFreshnessRequiresAChangedRefreshTime(t *testing.T) {
	t.Parallel()

	requested := metav1.NewTime(time.Date(2026, time.July, 10, 15, 5, 3, 900_000_000, time.UTC))
	previous := metav1.NewTime(time.Date(2026, time.July, 10, 15, 5, 3, 0, time.UTC))
	entry := &controlv1alpha1.AgentRunExternalSecretRefreshStatus{
		RequestedAt:         &requested,
		PreviousRefreshTime: &previous,
	}
	if agentRunExternalSecretRefreshChanged(entry, previous.Time) {
		t.Fatal("pre-request refresh time must not satisfy the freshness gate")
	}
	updated := previous.Time.Add(time.Second)
	if !agentRunExternalSecretRefreshChanged(entry, updated) {
		t.Fatal("newer refresh time must satisfy the freshness gate")
	}
}

func TestAgentRunExternalSecretAcceptsHealthyStableAfterGrace(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 26, 12, 0, 30, 0, time.UTC)
	requested := metav1.NewTime(now.Add(-agentRunExternalSecretHealthyAcceptAfter - time.Second))
	previous := metav1.NewTime(now.Add(-2 * time.Minute))
	entry := &controlv1alpha1.AgentRunExternalSecretRefreshStatus{
		RequestedAt:         &requested,
		PreviousRefreshTime: &previous,
	}
	if agentRunExternalSecretRefreshChanged(entry, previous.Time) {
		t.Fatal("unchanged refresh time must not count as a post-request advance")
	}
	if !agentRunExternalSecretAcceptHealthyStable(entry, previous.Time, now) {
		t.Fatal("Ready secrets with recent unchanged refreshTime must pass after the force-sync grace")
	}
	if agentRunExternalSecretAcceptHealthyStable(entry, previous.Time, requested.Time.Add(agentRunExternalSecretHealthyAcceptAfter/2)) {
		t.Fatal("healthy stable acceptance must wait for the force-sync grace")
	}
	stale := now.Add(-agentRunExternalSecretHealthyMaxAge - time.Minute)
	if agentRunExternalSecretAcceptHealthyStable(entry, stale, now) {
		t.Fatal("stale refreshTime must not satisfy the healthy stable gate")
	}
	if !agentRunExternalSecretRefreshStale(stale, now) {
		t.Fatal("refresh older than the healthy max age must be stale")
	}
}

func TestAgentRunExternalSecretRefreshTimeoutIgnoresCompletedEntries(t *testing.T) {
	t.Parallel()

	requested := metav1.NewTime(time.Now().Add(-agentRunExternalSecretRefreshTimeout - time.Minute))
	entry := &controlv1alpha1.AgentRunExternalSecretRefreshStatus{RequestedAt: &requested}
	if !agentRunExternalSecretRefreshTimedOut(entry) {
		t.Fatal("an overdue incomplete refresh must time out")
	}

	refreshed := metav1.NewTime(requested.Add(time.Second))
	entry.RefreshedAt = &refreshed
	if agentRunExternalSecretRefreshTimedOut(entry) {
		t.Fatal("a completed refresh must stay complete while later sequential refreshes run")
	}
}

func TestAgentRunExternalSecretTargetMismatchFailsBeforeForceSync(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := runtime.NewScheme()
	externalSecretGVK := schema.GroupVersionKind{Group: "external-secrets.io", Version: "v1", Kind: "ExternalSecret"}
	scheme.AddKnownTypeWithName(externalSecretGVK, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(externalSecretGVK.GroupVersion().WithKind("ExternalSecretList"), &unstructured.UnstructuredList{})
	externalSecret := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": externalSecretGVK.GroupVersion().String(),
		"kind":       externalSecretGVK.Kind,
		"metadata":   map[string]any{"name": "database-credential", "namespace": "hazy-trade"},
		"spec":       map[string]any{"target": map[string]any{"name": "database-runtime-secret"}},
	}}
	reconciler := &AgentRunReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(externalSecret).Build(),
		Scheme: scheme,
	}
	run := &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{Name: "refresh-confused-deputy", Namespace: "hazy-trade", UID: "run-uid"},
		Spec: controlv1alpha1.AgentRunSpec{Harness: controlv1alpha1.AgentRunHarnessSpec{Execution: controlv1alpha1.AgentRunHarnessExecutionSpec{
			ExternalSecretRefreshRefs: []controlv1alpha1.AgentRunExternalSecretRefreshRef{{
				Name:            "database-credential",
				TargetSecretRef: controlv1alpha1.NamespacedObjectReference{Name: "allowed-agent-secret"},
			}},
		}}},
	}
	status := controlv1alpha1.AgentRunStatus{}
	fresh, phase, reason, _, err := reconciler.ensureAgentRunExternalSecretFreshness(ctx, run, &status)
	if err != nil || fresh || phase != controlv1alpha1.AgentRunPhaseFailed || reason != "ExternalSecretTargetMismatch" {
		t.Fatalf("mismatch preflight = fresh:%t phase:%q reason:%q err:%v", fresh, phase, reason, err)
	}
	stored := &unstructured.Unstructured{}
	stored.SetGroupVersionKind(externalSecretGVK)
	if err := reconciler.Get(ctx, types.NamespacedName{Name: "database-credential", Namespace: "hazy-trade"}, stored); err != nil {
		t.Fatalf("get ExternalSecret after rejected preflight: %v", err)
	}
	if stored.GetAnnotations()["force-sync"] != "" {
		t.Fatalf("target-mismatched ExternalSecret was mutated: %#v", stored.GetAnnotations())
	}
	if len(status.ExternalSecretRefreshes) != 0 {
		t.Fatalf("target-mismatched refresh status was recorded: %#v", status.ExternalSecretRefreshes)
	}
}

func TestAgentRunExternalSecretFreshnessGatesJobCreationOnNewTargetRefresh(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	externalSecretGVK := schema.GroupVersionKind{Group: "external-secrets.io", Version: "v1", Kind: "ExternalSecret"}
	scheme.AddKnownTypeWithName(externalSecretGVK, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(externalSecretGVK.GroupVersion().WithKind("ExternalSecretList"), &unstructured.UnstructuredList{})

	previousRefresh := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	externalSecret := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": externalSecretGVK.GroupVersion().String(),
		"kind":       externalSecretGVK.Kind,
		"metadata":   map[string]any{"name": "vault-credential", "namespace": "hazy-trade"},
		"spec":       map[string]any{"target": map[string]any{"name": "runtime-credential"}},
		"status": map[string]any{
			"refreshTime": previousRefresh,
			"binding":     map[string]any{"name": "runtime-credential"},
			"conditions":  []any{map[string]any{"type": "Ready", "status": "True"}},
		},
	}}
	reconciler := &AgentRunReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(
			externalSecret,
			&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "runtime-credential", Namespace: "hazy-trade"}},
		).Build(),
		Scheme: scheme,
	}
	run := &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{Name: "fresh-credentials", Namespace: "hazy-trade", UID: "run-uid"},
		Spec: controlv1alpha1.AgentRunSpec{Harness: controlv1alpha1.AgentRunHarnessSpec{Execution: controlv1alpha1.AgentRunHarnessExecutionSpec{
			EnvSecretRefs: []controlv1alpha1.NamespacedObjectReference{{Name: "runtime-credential"}},
			ExternalSecretRefreshRefs: []controlv1alpha1.AgentRunExternalSecretRefreshRef{{
				Name:            "vault-credential",
				TargetSecretRef: controlv1alpha1.NamespacedObjectReference{Name: "runtime-credential"},
			}},
		}}},
	}
	status := controlv1alpha1.AgentRunStatus{}
	fresh, phase, reason, _, err := reconciler.ensureAgentRunExternalSecretFreshness(ctx, run, &status)
	if err != nil || fresh || phase != controlv1alpha1.AgentRunPhasePending || reason != "ExternalSecretRefreshRequested" {
		t.Fatalf("first preflight = fresh:%t phase:%q reason:%q err:%v", fresh, phase, reason, err)
	}
	if len(status.ExternalSecretRefreshes) != 1 || status.ExternalSecretRefreshes[0].PreviousRefreshTime == nil {
		t.Fatalf("refresh status = %#v, want baseline observation", status.ExternalSecretRefreshes)
	}
	updated := &unstructured.Unstructured{}
	updated.SetGroupVersionKind(externalSecretGVK)
	if err := reconciler.Get(ctx, types.NamespacedName{Name: "vault-credential", Namespace: "hazy-trade"}, updated); err != nil {
		t.Fatalf("get refreshed ExternalSecret: %v", err)
	}
	if updated.GetAnnotations()["force-sync"] == "" {
		t.Fatalf("force-sync annotation was not requested: %#v", updated.GetAnnotations())
	}
	if err := unstructured.SetNestedSlice(updated.Object, []any{map[string]any{"type": "Ready", "status": "False", "message": "provider temporarily unavailable"}}, "status", "conditions"); err != nil {
		t.Fatalf("set transient failed condition: %v", err)
	}
	if err := reconciler.Update(ctx, updated); err != nil {
		t.Fatalf("update transient ExternalSecret: %v", err)
	}
	fresh, phase, reason, _, err = reconciler.ensureAgentRunExternalSecretFreshness(ctx, run, &status)
	if err != nil || fresh || phase != controlv1alpha1.AgentRunPhasePending || reason != "WaitingForExternalSecretRefresh" {
		t.Fatalf("transient preflight = fresh:%t phase:%q reason:%q err:%v", fresh, phase, reason, err)
	}
	if err := unstructured.SetNestedSlice(updated.Object, []any{map[string]any{"type": "Ready", "status": "True"}}, "status", "conditions"); err != nil {
		t.Fatalf("set ready condition: %v", err)
	}
	if err := unstructured.SetNestedField(updated.Object, time.Now().UTC().Add(time.Second).Format(time.RFC3339), "status", "refreshTime"); err != nil {
		t.Fatalf("set refreshed time: %v", err)
	}
	if err := reconciler.Update(ctx, updated); err != nil {
		t.Fatalf("update refreshed ExternalSecret: %v", err)
	}
	fresh, phase, reason, message, err := reconciler.ensureAgentRunExternalSecretFreshness(ctx, run, &status)
	if err != nil || !fresh || phase != "" || reason != "" || message != "" {
		t.Fatalf("fresh preflight = fresh:%t phase:%q reason:%q message:%q err:%v", fresh, phase, reason, message, err)
	}
	if got, want := status.ExternalSecretRefreshes[0].TargetSecret, "runtime-credential"; got != want {
		t.Fatalf("target secret = %q, want %q", got, want)
	}
}

func TestAgentRunExternalSecretFreshnessAcceptsHealthyUnchangedRefreshTime(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	externalSecretGVK := schema.GroupVersionKind{Group: "external-secrets.io", Version: "v1", Kind: "ExternalSecret"}
	scheme.AddKnownTypeWithName(externalSecretGVK, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(externalSecretGVK.GroupVersion().WithKind("ExternalSecretList"), &unstructured.UnstructuredList{})

	// Unchanged refreshTime within the healthy max age. force-sync often no-ops
	// status for static vault properties (e.g. production auditor tokens).
	previousRefresh := time.Now().UTC().Add(-2 * time.Minute).Truncate(time.Second)
	externalSecret := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": externalSecretGVK.GroupVersion().String(),
		"kind":       externalSecretGVK.Kind,
		"metadata":   map[string]any{"name": "auditor-auth", "namespace": "hazy-trade"},
		"spec":       map[string]any{"target": map[string]any{"name": "auditor-auth"}},
		"status": map[string]any{
			"refreshTime": previousRefresh.Format(time.RFC3339),
			"binding":     map[string]any{"name": "auditor-auth"},
			"conditions":  []any{map[string]any{"type": "Ready", "status": "True"}},
		},
	}}
	reconciler := &AgentRunReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(
			externalSecret,
			&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "auditor-auth", Namespace: "hazy-trade"}},
		).Build(),
		Scheme: scheme,
	}
	run := &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{Name: "stable-auditor", Namespace: "hazy-trade", UID: "run-uid"},
		Spec: controlv1alpha1.AgentRunSpec{Harness: controlv1alpha1.AgentRunHarnessSpec{Execution: controlv1alpha1.AgentRunHarnessExecutionSpec{
			EnvSecretRefs: []controlv1alpha1.NamespacedObjectReference{{Name: "auditor-auth"}},
			ExternalSecretRefreshRefs: []controlv1alpha1.AgentRunExternalSecretRefreshRef{{
				Name:            "auditor-auth",
				TargetSecretRef: controlv1alpha1.NamespacedObjectReference{Name: "auditor-auth"},
			}},
		}}},
	}
	status := controlv1alpha1.AgentRunStatus{}
	fresh, phase, reason, _, err := reconciler.ensureAgentRunExternalSecretFreshness(ctx, run, &status)
	if err != nil || fresh || phase != controlv1alpha1.AgentRunPhasePending || reason != "ExternalSecretRefreshRequested" {
		t.Fatalf("first preflight = fresh:%t phase:%q reason:%q err:%v", fresh, phase, reason, err)
	}
	if len(status.ExternalSecretRefreshes) != 1 {
		t.Fatalf("refresh status = %#v, want one request entry", status.ExternalSecretRefreshes)
	}

	// Inside the force-sync grace: still Ready + same refreshTime → keep waiting.
	fresh, phase, reason, _, err = reconciler.ensureAgentRunExternalSecretFreshness(ctx, run, &status)
	if err != nil || fresh || phase != controlv1alpha1.AgentRunPhasePending || reason != "WaitingForExternalSecretRefresh" {
		t.Fatalf("grace preflight = fresh:%t phase:%q reason:%q err:%v", fresh, phase, reason, err)
	}

	// Past grace with unchanged but recent refreshTime → accept healthy stable.
	pastGrace := metav1.NewTime(time.Now().Add(-agentRunExternalSecretHealthyAcceptAfter - time.Second))
	status.ExternalSecretRefreshes[0].RequestedAt = &pastGrace
	fresh, phase, reason, message, err := reconciler.ensureAgentRunExternalSecretFreshness(ctx, run, &status)
	if err != nil || !fresh || phase != "" || reason != "" || message != "" {
		t.Fatalf("healthy stable preflight = fresh:%t phase:%q reason:%q message:%q err:%v", fresh, phase, reason, message, err)
	}
	if status.ExternalSecretRefreshes[0].RefreshedAt == nil {
		t.Fatal("healthy stable acceptance must record RefreshedAt")
	}
}

func TestAgentRunExternalSecretFreshnessStillTimesOutWhenStaleAndUnchanged(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	externalSecretGVK := schema.GroupVersionKind{Group: "external-secrets.io", Version: "v1", Kind: "ExternalSecret"}
	scheme.AddKnownTypeWithName(externalSecretGVK, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(externalSecretGVK.GroupVersion().WithKind("ExternalSecretList"), &unstructured.UnstructuredList{})

	staleRefresh := time.Now().UTC().Add(-agentRunExternalSecretHealthyMaxAge - 5*time.Minute).Truncate(time.Second)
	externalSecret := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": externalSecretGVK.GroupVersion().String(),
		"kind":       externalSecretGVK.Kind,
		"metadata":   map[string]any{"name": "stale-auth", "namespace": "hazy-trade"},
		"spec":       map[string]any{"target": map[string]any{"name": "stale-auth"}},
		"status": map[string]any{
			"refreshTime": staleRefresh.Format(time.RFC3339),
			"binding":     map[string]any{"name": "stale-auth"},
			"conditions":  []any{map[string]any{"type": "Ready", "status": "True"}},
		},
	}}
	reconciler := &AgentRunReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(
			externalSecret,
			&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "stale-auth", Namespace: "hazy-trade"}},
		).Build(),
		Scheme: scheme,
	}
	run := &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{Name: "stale-credentials", Namespace: "hazy-trade", UID: "run-uid"},
		Spec: controlv1alpha1.AgentRunSpec{Harness: controlv1alpha1.AgentRunHarnessSpec{Execution: controlv1alpha1.AgentRunHarnessExecutionSpec{
			EnvSecretRefs: []controlv1alpha1.NamespacedObjectReference{{Name: "stale-auth"}},
			ExternalSecretRefreshRefs: []controlv1alpha1.AgentRunExternalSecretRefreshRef{{
				Name:            "stale-auth",
				TargetSecretRef: controlv1alpha1.NamespacedObjectReference{Name: "stale-auth"},
			}},
		}}},
	}
	status := controlv1alpha1.AgentRunStatus{}
	fresh, phase, reason, _, err := reconciler.ensureAgentRunExternalSecretFreshness(ctx, run, &status)
	if err != nil || fresh || phase != controlv1alpha1.AgentRunPhasePending || reason != "ExternalSecretRefreshRequested" {
		t.Fatalf("first preflight = fresh:%t phase:%q reason:%q err:%v", fresh, phase, reason, err)
	}
	overdue := metav1.NewTime(time.Now().Add(-agentRunExternalSecretRefreshTimeout - time.Second))
	status.ExternalSecretRefreshes[0].RequestedAt = &overdue
	fresh, phase, reason, _, err = reconciler.ensureAgentRunExternalSecretFreshness(ctx, run, &status)
	if err != nil || fresh || phase != controlv1alpha1.AgentRunPhaseFailed || reason != "ExternalSecretRefreshTimedOut" {
		t.Fatalf("stale preflight = fresh:%t phase:%q reason:%q err:%v", fresh, phase, reason, err)
	}
}

func TestAgentRunJobInjectsRepositoryBranchScope(t *testing.T) {
	t.Parallel()

	run := &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{Name: "docs-steward", Namespace: "hazy-trade"},
		Spec: controlv1alpha1.AgentRunSpec{
			SourceRef: controlv1alpha1.AgentRunSourceRef{Kind: "AgentSchedule", Name: "docs-steward-daily"},
			Scope: controlv1alpha1.AgentRunScopeSpec{
				ApplicationRef: &controlv1alpha1.ApplicationReferenceSpec{Name: "hazy-trade"},
				Repository: &controlv1alpha1.AgentRunRepositorySpec{
					Name:              "HazyForge/hazy-trade",
					DestinationBranch: "master",
					AllowedBranches:   []string{"master", "release/stable"},
				},
			},
			Harness: controlv1alpha1.AgentRunHarnessSpec{
				Intent: controlv1alpha1.AgentRunIntentProposeChange,
				Backend: controlv1alpha1.AgentRunHarnessBackendSpec{
					Kind: controlv1alpha1.AgentRunHarnessBackendCodex,
				},
			},
		},
	}

	job := agentRunJob(run, "docs-steward-harness", "docs-steward-context", nil)
	env := map[string]string{}
	for _, item := range job.Spec.Template.Spec.Containers[0].Env {
		env[item.Name] = item.Value
	}
	if got, want := env["ANVIL_AGENT_RUN_REPOSITORY"], "HazyForge/hazy-trade"; got != want {
		t.Fatalf("repository = %q, want %q", got, want)
	}
	if got, want := env["ANVIL_AGENT_RUN_REPOSITORY_URL"], "https://github.com/HazyForge/hazy-trade.git"; got != want {
		t.Fatalf("repository url = %q, want %q", got, want)
	}
	if got, want := env["ANVIL_AGENT_RUN_REPOSITORY_REF"], "master"; got != want {
		t.Fatalf("repository ref = %q, want %q", got, want)
	}
	if got, want := env["ANVIL_AGENT_RUN_DESTINATION_BRANCH"], "master"; got != want {
		t.Fatalf("destination branch = %q, want %q", got, want)
	}
	if got, want := env["ANVIL_AGENT_RUN_ALLOWED_BRANCHES"], "master,release/stable"; got != want {
		t.Fatalf("allowed branches = %q, want %q", got, want)
	}
}

func TestAgentRunMergeRepositoryRunOverridesProfile(t *testing.T) {
	t.Parallel()

	merged := agentRunMergeRepository(
		&controlv1alpha1.AgentRunRepositorySpec{
			Name:              "HazyForge/hazy-trade",
			DestinationBranch: "master",
			AllowedBranches:   []string{"master"},
		},
		&controlv1alpha1.AgentRunRepositorySpec{
			Ref:               "feature/docs-align",
			AllowedBranches:   []string{"feature/docs-align", "master"},
			DestinationBranch: "master",
		},
	)
	if merged == nil {
		t.Fatal("expected merged repository")
	}
	if got, want := merged.Name, "HazyForge/hazy-trade"; got != want {
		t.Fatalf("name = %q, want %q", got, want)
	}
	if got, want := merged.Ref, "feature/docs-align"; got != want {
		t.Fatalf("ref = %q, want %q", got, want)
	}
	if got, want := strings.Join(merged.AllowedBranches, ","), "feature/docs-align,master"; got != want {
		t.Fatalf("allowed = %q, want %q", got, want)
	}
}
