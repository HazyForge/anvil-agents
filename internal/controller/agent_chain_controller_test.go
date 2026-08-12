package controller

import (
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	controlv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

func newAgentChainScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := controlv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("scheme: %v", err)
	}
	return scheme
}

func baseChain(name string) *controlv1alpha1.AgentChain {
	return &controlv1alpha1.AgentChain{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         "anvilhub",
			UID:               types.UID("chain-uid-1"),
			CreationTimestamp: metav1.NewTime(time.Now().UTC().Add(-time.Hour)),
			Generation:        1,
			Finalizers:        []string{agentChainDrainFinalizer},
		},
		Spec: controlv1alpha1.AgentChainSpec{
			ApplicationRef: &controlv1alpha1.ApplicationReferenceSpec{Name: "demo-app"},
			Steps: []controlv1alpha1.AgentChainStepSpec{
				{
					Name: "exercise",
					RunTemplate: controlv1alpha1.AgentRunSpec{
						ProfileRef: &controlv1alpha1.NamespacedObjectReference{Name: "exerciser"},
						Prompt:     "exercise the delivery path",
					},
				},
				{
					Name: "monitor",
					When: &controlv1alpha1.AgentChainWhenSpec{
						PreviousStep: "exercise",
						OnPhases:     []controlv1alpha1.AgentRunPhase{controlv1alpha1.AgentRunPhaseSucceeded},
					},
					RunTemplate: controlv1alpha1.AgentRunSpec{
						ProfileRef: &controlv1alpha1.NamespacedObjectReference{Name: "monitor"},
						Prompt:     "audit evidence",
					},
					Handoff: &controlv1alpha1.AgentChainHandoffSpec{
						IncludeDecision: true,
					},
				},
			},
		},
	}
}

func freezeActiveChainForTest(chain *controlv1alpha1.AgentChain, status *controlv1alpha1.AgentChainStatus, run *controlv1alpha1.AgentRun) {
	workflowDigest := agentChainWorkflowDigest(chain)
	status.ActiveSourceGeneration = chain.Generation
	status.ActiveWorkflowDigest = workflowDigest
	status.ActiveRunRef = &controlv1alpha1.NamespacedObjectReference{Name: run.Name, Namespace: run.Namespace}
	status.ActiveRunUID = string(run.UID)
	run.Spec.SourceRef = controlv1alpha1.AgentRunSourceRef{APIVersion: controlv1alpha1.GroupVersion.String(), Kind: "AgentChain", Namespace: chain.Namespace, Name: chain.Name}
	run.Spec.SourceUID = string(chain.UID)
	run.Spec.SourceGeneration = chain.Generation
	run.Spec.SourceDigest = workflowDigest
	if run.Annotations == nil {
		run.Annotations = map[string]string{}
	}
	run.Annotations[agentRunChainDigestAnnotation] = workflowDigest
	applicationName, err := resolveAgentChainApplicationName(chain)
	if err != nil {
		panic(err)
	}
	if run.Labels == nil {
		run.Labels = map[string]string{}
	}
	for key, value := range agentChainChildLabels(chain, applicationName, status.ActiveInstanceID, status.ActiveStep) {
		run.Labels[key] = value
	}
	if run.Spec.Scope.ApplicationRef == nil {
		run.Spec.Scope.ApplicationRef = &controlv1alpha1.ApplicationReferenceSpec{Name: applicationName}
	}
}

func TestAgentChainStartAndAdvance(t *testing.T) {
	scheme := newAgentChainScheme(t)
	chain := baseChain("lab-chain")
	chain.Annotations = map[string]string{
		controlv1alpha1.AgentChainStartNowAnnotation: "token-1",
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&controlv1alpha1.AgentChain{}, &controlv1alpha1.AgentRun{}).WithObjects(chain).Build()
	r := &AgentChainReconciler{Client: c, Scheme: scheme}

	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: chain.Namespace, Name: chain.Name}}); err != nil {
		t.Fatalf("start reconcile: %v", err)
	}

	stored := &controlv1alpha1.AgentChain{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(chain), stored); err != nil {
		t.Fatalf("get chain: %v", err)
	}
	if stored.Status.Phase != controlv1alpha1.AgentChainPhaseRunning {
		t.Fatalf("phase = %s, want Running", stored.Status.Phase)
	}
	if stored.Status.ActiveInstanceID == "" || stored.Status.ActiveStep != "exercise" {
		t.Fatalf("active instance/step = %q/%q", stored.Status.ActiveInstanceID, stored.Status.ActiveStep)
	}
	if stored.Status.LastStartToken != "token-1" {
		t.Fatalf("lastStartToken = %q", stored.Status.LastStartToken)
	}

	runs := &controlv1alpha1.AgentRunList{}
	if err := c.List(context.Background(), runs); err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs.Items) != 1 {
		t.Fatalf("runs = %d, want 1", len(runs.Items))
	}
	first := runs.Items[0]
	if first.Spec.Purpose != controlv1alpha1.AgentRunPurposeChained {
		t.Fatalf("purpose = %s, want chained", first.Spec.Purpose)
	}
	if first.Spec.SourceRef.Kind != "AgentChain" {
		t.Fatalf("source kind = %s", first.Spec.SourceRef.Kind)
	}

	// Mark first terminal Succeeded and reconcile to advance.
	first.Status.Phase = controlv1alpha1.AgentRunPhaseSucceeded
	first.Status.Decision = &controlv1alpha1.AgentRunDecisionStatus{
		Classification: "completed",
		Action:         "proposeChange",
		Summary:        "built and promoted once",
	}
	if err := c.Status().Update(context.Background(), &first); err != nil {
		// fake client may need Update when status subresource not registered for AgentRun
		if err := c.Update(context.Background(), &first); err != nil {
			t.Fatalf("mark first succeeded: %v", err)
		}
	}

	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: chain.Namespace, Name: chain.Name}}); err != nil {
		t.Fatalf("advance reconcile: %v", err)
	}

	if err := c.List(context.Background(), runs); err != nil {
		t.Fatalf("list runs after advance: %v", err)
	}
	if len(runs.Items) != 2 {
		t.Fatalf("runs after advance = %d, want 2", len(runs.Items))
	}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(chain), stored); err != nil {
		t.Fatalf("get chain after advance: %v", err)
	}
	if stored.Status.ActiveStep != "monitor" {
		t.Fatalf("active step = %q, want monitor", stored.Status.ActiveStep)
	}

	var monitor *controlv1alpha1.AgentRun
	for i := range runs.Items {
		if runs.Items[i].Labels[agentRunChainStepLabel] == "monitor" {
			monitor = &runs.Items[i]
			break
		}
	}
	if monitor == nil {
		t.Fatal("missing monitor run")
	}
	if !strings.Contains(monitor.Spec.Prompt, "AgentChain handoff") {
		t.Fatalf("monitor prompt missing handoff: %q", monitor.Spec.Prompt)
	}
	if !strings.Contains(monitor.Spec.Prompt, "proposeChange") {
		t.Fatalf("monitor handoff missing decision action: %q", monitor.Spec.Prompt)
	}
}

func TestAgentChainStopsOnFailedWhen(t *testing.T) {
	scheme := newAgentChainScheme(t)
	chain := baseChain("lab-chain-fail")
	chain.Status = controlv1alpha1.AgentChainStatus{
		ObservedGeneration: 1,
		Phase:              controlv1alpha1.AgentChainPhaseRunning,
		ActiveInstanceID:   "inst-1",
		ActiveStep:         "exercise",
		LastStartToken:     "t",
	}
	run := &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "agentrun-lab-chain-fail-exercise",
			Namespace: "anvilhub",
			Labels: map[string]string{
				agentRunChainLabel:     sanitizeLabelValue(chain.Name),
				agentRunChainInstLabel: sanitizeLabelValue("inst-1"),
				agentRunChainStepLabel: sanitizeLabelValue("exercise"),
			},
		},
		Spec: controlv1alpha1.AgentRunSpec{
			Purpose:   controlv1alpha1.AgentRunPurposeChained,
			SourceUID: string(chain.UID),
		},
		Status: controlv1alpha1.AgentRunStatus{Phase: controlv1alpha1.AgentRunPhaseFailed},
	}
	freezeActiveChainForTest(chain, &chain.Status, run)
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&controlv1alpha1.AgentChain{}).WithObjects(chain, run).Build()
	r := &AgentChainReconciler{Client: c, Scheme: scheme}
	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: chain.Namespace, Name: chain.Name}}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	stored := &controlv1alpha1.AgentChain{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(chain), stored); err != nil {
		t.Fatalf("get: %v", err)
	}
	if stored.Status.ActiveInstanceID != "" {
		t.Fatalf("expected instance stopped, still active %q", stored.Status.ActiveInstanceID)
	}
	if stored.Status.Phase != controlv1alpha1.AgentChainPhaseIdle {
		t.Fatalf("phase = %s, want Idle", stored.Status.Phase)
	}
	runs := &controlv1alpha1.AgentRunList{}
	if err := c.List(context.Background(), runs); err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(runs.Items) != 1 {
		t.Fatalf("should not create next step; runs=%d", len(runs.Items))
	}
}

func TestValidateAgentChainSpecLinearOnly(t *testing.T) {
	chain := baseChain("x")
	chain.Spec.Steps[1].When.PreviousStep = "missing"
	if err := validateAgentChainSpec(chain); err == nil {
		t.Fatal("expected non-linear previousStep to fail")
	}
}

func TestAgentChainDeletionWaitsForExactActiveChildToDrain(t *testing.T) {
	scheme := newAgentChainScheme(t)
	chain := baseChain("delete-drains-active")
	chain.Annotations = map[string]string{controlv1alpha1.AgentChainStartNowAnnotation: "token-delete"}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&controlv1alpha1.AgentChain{}, &controlv1alpha1.AgentRun{}).WithObjects(chain).Build()
	r := &AgentChainReconciler{Client: c, Scheme: scheme}
	req := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: chain.Namespace, Name: chain.Name}}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("start: %v", err)
	}
	stored := &controlv1alpha1.AgentChain{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(chain), stored); err != nil {
		t.Fatalf("get: %v", err)
	}
	run := &controlv1alpha1.AgentRun{}
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: stored.Status.ActiveRunRef.Namespace, Name: stored.Status.ActiveRunRef.Name}, run); err != nil {
		t.Fatalf("get active child: %v", err)
	}
	if err := c.Delete(context.Background(), stored); err != nil {
		t.Fatalf("delete chain: %v", err)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("deletion drain reconcile: %v", err)
	}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(chain), stored); err != nil {
		t.Fatalf("chain deleted before active child drained: %v", err)
	}
	if !controllerutil.ContainsFinalizer(stored, agentChainDrainFinalizer) {
		t.Fatal("active-drain finalizer disappeared while child was nonterminal")
	}
	run.Status.Phase = controlv1alpha1.AgentRunPhaseSucceeded
	if err := c.Status().Update(context.Background(), run); err != nil {
		t.Fatalf("finish child: %v", err)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("terminal drain reconcile: %v", err)
	}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(chain), stored); !apierrors.IsNotFound(err) {
		t.Fatalf("chain should delete after active child drains, got %v", err)
	}
}

func TestValidateAgentChainSpecRejectsBlankDecisionActions(t *testing.T) {
	chain := baseChain("blank-action")
	chain.Spec.Steps[1].When.OnDecisionActions = []string{" "}
	if err := validateAgentChainSpec(chain); err == nil {
		t.Fatal("expected blank intermediate action to fail")
	}
	chain = baseChain("blank-completion")
	chain.Spec.Completion = &controlv1alpha1.AgentChainCompletionSpec{OnDecisionActions: []string{""}}
	if err := validateAgentChainSpec(chain); err == nil {
		t.Fatal("expected blank completion action to fail")
	}
}

func TestAgentChainWorkflowDigestBindsExecutionNotCadence(t *testing.T) {
	chain := baseChain("digest")
	base := agentChainWorkflowDigest(chain)
	operational := chain.DeepCopy()
	operational.Spec.Suspend = true
	operational.Spec.StartIntervalSeconds = 60
	operational.Spec.StartInitialDelaySeconds = 5
	operational.Spec.MaxInstancesPerDay = 2
	operational.Spec.Backoff = &controlv1alpha1.AgentChainBackoffSpec{FailedSeconds: 10}
	if got := agentChainWorkflowDigest(operational); got != base {
		t.Fatalf("operational edit changed workflow digest: %s != %s", got, base)
	}
	mutations := []func(*controlv1alpha1.AgentChain){
		func(value *controlv1alpha1.AgentChain) { value.Spec.ApplicationRef.Name = "other" },
		func(value *controlv1alpha1.AgentChain) { value.Spec.Steps[0].RunTemplate.Prompt = "new" },
		func(value *controlv1alpha1.AgentChain) {
			value.Spec.Steps[1].When.OnDecisionActions = []string{"passed"}
		},
		func(value *controlv1alpha1.AgentChain) {
			value.Spec.Steps[1].Handoff.IncludePullRequestURL = true
		},
		func(value *controlv1alpha1.AgentChain) {
			value.Spec.Completion = &controlv1alpha1.AgentChainCompletionSpec{OnDecisionActions: []string{"passed"}}
		},
		func(value *controlv1alpha1.AgentChain) {
			if value.Labels == nil {
				value.Labels = map[string]string{}
			}
			value.Labels[agentManagerRepositoryLabel] = "HazyForge/hazy-trade"
		},
	}
	for i, mutate := range mutations {
		changed := chain.DeepCopy()
		mutate(changed)
		if got := agentChainWorkflowDigest(changed); got == base {
			t.Fatalf("execution mutation %d did not change workflow digest", i)
		}
	}
}

func TestAgentChainSuspend(t *testing.T) {
	scheme := newAgentChainScheme(t)
	chain := baseChain("suspended")
	chain.Spec.Suspend = true
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&controlv1alpha1.AgentChain{}).WithObjects(chain).Build()
	r := &AgentChainReconciler{Client: c, Scheme: scheme}
	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: chain.Namespace, Name: chain.Name}}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	stored := &controlv1alpha1.AgentChain{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(chain), stored); err != nil {
		t.Fatalf("get: %v", err)
	}
	if stored.Status.Phase != controlv1alpha1.AgentChainPhaseSuspended {
		t.Fatalf("phase = %s", stored.Status.Phase)
	}
}

func TestAgentChainInstanceIDStableForManualToken(t *testing.T) {
	chain := baseChain("id-stable")
	token := "token-abc"
	a := agentChainInstanceID(chain, true, token, time.Unix(100, 0).UTC())
	b := agentChainInstanceID(chain, true, token, time.Unix(200, 0).UTC())
	if a != b {
		t.Fatalf("manual instance id should ignore wall clock: %q vs %q", a, b)
	}
	c := agentChainInstanceID(chain, true, "other", time.Unix(100, 0).UTC())
	if a == c {
		t.Fatal("different tokens must yield different instance ids")
	}
}

func TestAgentChainStartIdempotentOnRetry(t *testing.T) {
	scheme := newAgentChainScheme(t)
	chain := baseChain("lab-chain-idem")
	chain.Annotations = map[string]string{
		controlv1alpha1.AgentChainStartNowAnnotation: "same-token",
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&controlv1alpha1.AgentChain{}).WithObjects(chain).Build()
	r := &AgentChainReconciler{Client: c, Scheme: scheme}
	req := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: chain.Namespace, Name: chain.Name}}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	// Simulate lost status patch: clear status but keep the first-step run.
	stored := &controlv1alpha1.AgentChain{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(chain), stored); err != nil {
		t.Fatalf("get: %v", err)
	}
	stored.Status = controlv1alpha1.AgentChainStatus{ObservedGeneration: stored.Generation}
	if err := c.Status().Update(context.Background(), stored); err != nil {
		t.Fatalf("clear status: %v", err)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("retry reconcile: %v", err)
	}
	runs := &controlv1alpha1.AgentRunList{}
	if err := c.List(context.Background(), runs); err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(runs.Items) != 1 {
		t.Fatalf("expected single first-step run after retry, got %d", len(runs.Items))
	}
}

func TestAgentChainManualStartRetryAfterTimePassesKeepsOneChild(t *testing.T) {
	scheme := newAgentChainScheme(t)
	chain := baseChain("manual-delay-idem")
	chain.Annotations = map[string]string{controlv1alpha1.AgentChainStartNowAnnotation: "opaque-token"}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&controlv1alpha1.AgentChain{}).WithObjects(chain).Build()
	r := &AgentChainReconciler{Client: c, Scheme: scheme}
	req := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: chain.Namespace, Name: chain.Name}}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	stored := &controlv1alpha1.AgentChain{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(chain), stored); err != nil {
		t.Fatalf("get: %v", err)
	}
	stored.Status = controlv1alpha1.AgentChainStatus{ObservedGeneration: stored.Generation}
	if err := c.Status().Update(context.Background(), stored); err != nil {
		t.Fatalf("clear status: %v", err)
	}
	time.Sleep(1100 * time.Millisecond)
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("delayed retry reconcile: %v", err)
	}
	runs := &controlv1alpha1.AgentRunList{}
	if err := c.List(context.Background(), runs); err != nil || len(runs.Items) != 1 {
		t.Fatalf("expected one child after delayed retry: runs=%d err=%v", len(runs.Items), err)
	}
}

func TestAgentChainLostStatusAndNewManualTokenRecoversOneChild(t *testing.T) {
	scheme := newAgentChainScheme(t)
	chain := baseChain("manual-new-token-orphan")
	chain.Annotations = map[string]string{controlv1alpha1.AgentChainStartNowAnnotation: "token-one"}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&controlv1alpha1.AgentChain{}).WithObjects(chain).Build()
	r := &AgentChainReconciler{Client: c, Scheme: scheme}
	req := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: chain.Namespace, Name: chain.Name}}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	stored := &controlv1alpha1.AgentChain{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(chain), stored); err != nil {
		t.Fatalf("get: %v", err)
	}
	stored.Status = controlv1alpha1.AgentChainStatus{ObservedGeneration: stored.Generation}
	if err := c.Status().Update(context.Background(), stored); err != nil {
		t.Fatalf("clear status: %v", err)
	}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(chain), stored); err != nil {
		t.Fatalf("get for new token: %v", err)
	}
	stored.Annotations[controlv1alpha1.AgentChainStartNowAnnotation] = "token-two"
	if err := c.Update(context.Background(), stored); err != nil {
		t.Fatalf("change start token: %v", err)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("recover reconcile: %v", err)
	}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(chain), stored); err != nil {
		t.Fatalf("get recovered status: %v", err)
	}
	if stored.Status.ActiveInstanceID == "" || stored.Status.ActiveRunRef == nil {
		t.Fatalf("orphan ownership not recovered: %#v", stored.Status)
	}
	runs := &controlv1alpha1.AgentRunList{}
	if err := c.List(context.Background(), runs); err != nil || len(runs.Items) != 1 {
		t.Fatalf("new token overlapped orphan: runs=%d err=%v", len(runs.Items), err)
	}
}

func TestAgentChainRecoveredSameManualTokenIsConsumedAfterTerminal(t *testing.T) {
	scheme := newAgentChainScheme(t)
	chain := baseChain("manual-recovery-receipt")
	chain.Spec.Steps = chain.Spec.Steps[:1]
	chain.Annotations = map[string]string{controlv1alpha1.AgentChainStartNowAnnotation: "same-token"}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&controlv1alpha1.AgentChain{}, &controlv1alpha1.AgentRun{}).WithObjects(chain).Build()
	r := &AgentChainReconciler{Client: c, Scheme: scheme}
	req := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: chain.Namespace, Name: chain.Name}}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("start: %v", err)
	}
	stored := &controlv1alpha1.AgentChain{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(chain), stored); err != nil {
		t.Fatalf("get: %v", err)
	}
	stored.Status = controlv1alpha1.AgentChainStatus{ObservedGeneration: stored.Generation}
	if err := c.Status().Update(context.Background(), stored); err != nil {
		t.Fatalf("clear status: %v", err)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("recover: %v", err)
	}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(chain), stored); err != nil {
		t.Fatalf("get recovered: %v", err)
	}
	if stored.Status.LastStartToken != "same-token" {
		t.Fatalf("recovery did not consume same token: %#v", stored.Status)
	}
	run := &controlv1alpha1.AgentRun{}
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: stored.Status.ActiveRunRef.Namespace, Name: stored.Status.ActiveRunRef.Name}, run); err != nil {
		t.Fatalf("get run: %v", err)
	}
	run.Status.Phase = controlv1alpha1.AgentRunPhaseSucceeded
	if err := c.Status().Update(context.Background(), run); err != nil {
		t.Fatalf("finish run: %v", err)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("complete recovered: %v", err)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("post-completion reconcile: %v", err)
	}
	runs := &controlv1alpha1.AgentRunList{}
	if err := c.List(context.Background(), runs); err != nil || len(runs.Items) != 1 {
		t.Fatalf("same token replayed after recovery: runs=%d err=%v", len(runs.Items), err)
	}
}

func TestAgentChainRecoveredNewManualTokenStartsOnceAfterTerminal(t *testing.T) {
	scheme := newAgentChainScheme(t)
	chain := baseChain("manual-new-token-after-recovery")
	chain.Spec.Steps = chain.Spec.Steps[:1]
	chain.Annotations = map[string]string{controlv1alpha1.AgentChainStartNowAnnotation: "token-one"}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&controlv1alpha1.AgentChain{}, &controlv1alpha1.AgentRun{}).WithObjects(chain).Build()
	r := &AgentChainReconciler{Client: c, Scheme: scheme}
	req := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: chain.Namespace, Name: chain.Name}}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("start: %v", err)
	}
	stored := &controlv1alpha1.AgentChain{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(chain), stored); err != nil {
		t.Fatalf("get: %v", err)
	}
	stored.Status = controlv1alpha1.AgentChainStatus{ObservedGeneration: stored.Generation}
	if err := c.Status().Update(context.Background(), stored); err != nil {
		t.Fatalf("clear status: %v", err)
	}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(chain), stored); err != nil {
		t.Fatalf("get new token: %v", err)
	}
	stored.Annotations[controlv1alpha1.AgentChainStartNowAnnotation] = "token-two"
	if err := c.Update(context.Background(), stored); err != nil {
		t.Fatalf("set new token: %v", err)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("recover old child: %v", err)
	}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(chain), stored); err != nil {
		t.Fatalf("get recovered: %v", err)
	}
	run := &controlv1alpha1.AgentRun{}
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: stored.Status.ActiveRunRef.Namespace, Name: stored.Status.ActiveRunRef.Name}, run); err != nil {
		t.Fatalf("get run: %v", err)
	}
	run.Status.Phase = controlv1alpha1.AgentRunPhaseSucceeded
	if err := c.Status().Update(context.Background(), run); err != nil {
		t.Fatalf("finish old run: %v", err)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("finish recovered child: %v", err)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("start pending new token: %v", err)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("idempotent new token: %v", err)
	}
	runs := &controlv1alpha1.AgentRunList{}
	if err := c.List(context.Background(), runs); err != nil || len(runs.Items) != 2 {
		t.Fatalf("new token should start exactly once after terminal: runs=%d err=%v", len(runs.Items), err)
	}
}

func TestAgentChainLostStatusAndApplicationEditDoesNotOverlap(t *testing.T) {
	scheme := newAgentChainScheme(t)
	chain := baseChain("application-edit-orphan")
	chain.Annotations = map[string]string{controlv1alpha1.AgentChainStartNowAnnotation: "token-one"}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&controlv1alpha1.AgentChain{}).WithObjects(chain).Build()
	r := &AgentChainReconciler{Client: c, Scheme: scheme}
	req := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: chain.Namespace, Name: chain.Name}}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	stored := &controlv1alpha1.AgentChain{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(chain), stored); err != nil {
		t.Fatalf("get: %v", err)
	}
	stored.Status = controlv1alpha1.AgentChainStatus{ObservedGeneration: stored.Generation}
	if err := c.Status().Update(context.Background(), stored); err != nil {
		t.Fatalf("clear status: %v", err)
	}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(chain), stored); err != nil {
		t.Fatalf("get for application edit: %v", err)
	}
	stored.Spec.ApplicationRef.Name = "other-app"
	stored.Annotations[controlv1alpha1.AgentChainStartNowAnnotation] = "token-two"
	stored.Generation++
	if err := c.Update(context.Background(), stored); err != nil {
		t.Fatalf("change application: %v", err)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("recover old application child: %v", err)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("fence application drift: %v", err)
	}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(chain), stored); err != nil {
		t.Fatalf("get fenced chain: %v", err)
	}
	if stored.Status.Phase != controlv1alpha1.AgentChainPhaseBlocked || stored.Status.ActiveInstanceID == "" {
		t.Fatalf("application edit did not retain/fence orphan: %#v", stored.Status)
	}
	runs := &controlv1alpha1.AgentRunList{}
	if err := c.List(context.Background(), runs); err != nil || len(runs.Items) != 1 {
		t.Fatalf("application edit overlapped orphan: runs=%d err=%v", len(runs.Items), err)
	}
}

func TestAgentChainMalformedExactSourceOrphanBlocksNewStart(t *testing.T) {
	scheme := newAgentChainScheme(t)
	chain := baseChain("malformed-orphan")
	chain.Annotations = map[string]string{controlv1alpha1.AgentChainStartNowAnnotation: "token-one"}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&controlv1alpha1.AgentChain{}).WithObjects(chain).Build()
	r := &AgentChainReconciler{Client: c, Scheme: scheme}
	req := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: chain.Namespace, Name: chain.Name}}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	stored := &controlv1alpha1.AgentChain{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(chain), stored); err != nil {
		t.Fatalf("get: %v", err)
	}
	run := &controlv1alpha1.AgentRun{}
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: stored.Status.ActiveRunRef.Namespace, Name: stored.Status.ActiveRunRef.Name}, run); err != nil {
		t.Fatalf("get child: %v", err)
	}
	delete(run.Labels, agentRunChainLabel)
	if err := c.Update(context.Background(), run); err != nil {
		t.Fatalf("malform child labels: %v", err)
	}
	stored.Status = controlv1alpha1.AgentChainStatus{ObservedGeneration: stored.Generation}
	if err := c.Status().Update(context.Background(), stored); err != nil {
		t.Fatalf("clear status: %v", err)
	}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(chain), stored); err != nil {
		t.Fatalf("get for new token: %v", err)
	}
	stored.Annotations[controlv1alpha1.AgentChainStartNowAnnotation] = "token-two"
	if err := c.Update(context.Background(), stored); err != nil {
		t.Fatalf("change token: %v", err)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("reconcile malformed orphan: %v", err)
	}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(chain), stored); err != nil {
		t.Fatalf("get blocked chain: %v", err)
	}
	cond := apimeta.FindStatusCondition(stored.Status.Conditions, agentChainReady)
	if stored.Status.Phase != controlv1alpha1.AgentChainPhaseBlocked || cond == nil || cond.Reason != "UnrecoveredActiveRun" {
		t.Fatalf("malformed exact-source orphan did not fail closed: %#v", stored.Status)
	}
	runs := &controlv1alpha1.AgentRunList{}
	if err := c.List(context.Background(), runs); err != nil || len(runs.Items) != 1 {
		t.Fatalf("malformed orphan overlapped: runs=%d err=%v", len(runs.Items), err)
	}
}

func TestAgentChainLostStatusIntervalRetryKeepsOneChild(t *testing.T) {
	scheme := newAgentChainScheme(t)
	chain := baseChain("interval-orphan")
	chain.Spec.Steps = chain.Spec.Steps[:1]
	chain.Spec.StartIntervalSeconds = 60
	chain.CreationTimestamp = metav1.NewTime(time.Now().UTC().Add(-2 * time.Minute).Truncate(time.Second))
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&controlv1alpha1.AgentChain{}, &controlv1alpha1.AgentRun{}).WithObjects(chain).Build()
	r := &AgentChainReconciler{Client: c, Scheme: scheme}
	req := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: chain.Namespace, Name: chain.Name}}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("first interval reconcile: %v", err)
	}
	stored := &controlv1alpha1.AgentChain{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(chain), stored); err != nil {
		t.Fatalf("get: %v", err)
	}
	stored.Status = controlv1alpha1.AgentChainStatus{ObservedGeneration: stored.Generation}
	if err := c.Status().Update(context.Background(), stored); err != nil {
		t.Fatalf("clear status: %v", err)
	}
	time.Sleep(1100 * time.Millisecond)
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("interval retry reconcile: %v", err)
	}
	runs := &controlv1alpha1.AgentRunList{}
	if err := c.List(context.Background(), runs); err != nil || len(runs.Items) != 1 {
		t.Fatalf("interval retry overlapped orphan: runs=%d err=%v", len(runs.Items), err)
	}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(chain), stored); err != nil {
		t.Fatalf("get recovered interval: %v", err)
	}
	if stored.Status.NextStartAt == nil || !stored.Status.NextStartAt.After(chain.CreationTimestamp.Time) {
		t.Fatalf("recovery did not reconstruct interval receipt: %#v", stored.Status)
	}
	run := &controlv1alpha1.AgentRun{}
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: stored.Status.ActiveRunRef.Namespace, Name: stored.Status.ActiveRunRef.Name}, run); err != nil {
		t.Fatalf("get interval run: %v", err)
	}
	run.Status.Phase = controlv1alpha1.AgentRunPhaseSucceeded
	if err := c.Status().Update(context.Background(), run); err != nil {
		t.Fatalf("finish interval run: %v", err)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("finish recovered interval: %v", err)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("post-interval completion: %v", err)
	}
	if err := c.List(context.Background(), runs); err != nil || len(runs.Items) != 1 {
		t.Fatalf("recovered interval replayed terminal child: runs=%d err=%v", len(runs.Items), err)
	}
}

func TestAgentChainOldUnsuspendedCadenceStartsLatestSlotOnly(t *testing.T) {
	scheme := newAgentChainScheme(t)
	chain := baseChain("old-unsuspended-cadence")
	chain.Spec.Steps = chain.Spec.Steps[:1]
	chain.Spec.StartIntervalSeconds = 60
	chain.CreationTimestamp = metav1.NewTime(time.Now().UTC().Add(-7 * 24 * time.Hour).Truncate(time.Second))
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&controlv1alpha1.AgentChain{}, &controlv1alpha1.AgentRun{}).WithObjects(chain).Build()
	r := &AgentChainReconciler{Client: c, Scheme: scheme}
	req := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: chain.Namespace, Name: chain.Name}}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("first unsuspended reconcile: %v", err)
	}
	stored := &controlv1alpha1.AgentChain{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(chain), stored); err != nil {
		t.Fatalf("get running chain: %v", err)
	}
	if stored.Status.NextStartAt == nil || !stored.Status.NextStartAt.After(time.Now().UTC()) {
		t.Fatalf("old chain did not advance cadence to future deadline: %#v", stored.Status)
	}
	run := &controlv1alpha1.AgentRun{}
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: stored.Status.ActiveRunRef.Namespace, Name: stored.Status.ActiveRunRef.Name}, run); err != nil {
		t.Fatalf("get current-slot run: %v", err)
	}
	run.Status.Phase = controlv1alpha1.AgentRunPhaseSucceeded
	if err := c.Status().Update(context.Background(), run); err != nil {
		t.Fatalf("finish current-slot run: %v", err)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("finish chain: %v", err)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("post-finish reconcile: %v", err)
	}
	runs := &controlv1alpha1.AgentRunList{}
	if err := c.List(context.Background(), runs); err != nil || len(runs.Items) != 1 {
		t.Fatalf("old chain burst stale cadence slots: runs=%d err=%v", len(runs.Items), err)
	}
}

func TestAgentChainForeignLabelledRunWithWrongSourceUIDDoesNotBlockStart(t *testing.T) {
	scheme := newAgentChainScheme(t)
	chain := baseChain("foreign-wrong-source")
	chain.Annotations = map[string]string{controlv1alpha1.AgentChainStartNowAnnotation: "token-start"}
	foreign := &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{Name: "foreign", Namespace: chain.Namespace, Labels: map[string]string{
			agentRunChainLabel: sanitizeLabelValue(chain.Name), agentRunChainInstLabel: "foreign-instance", agentRunChainStepLabel: "exercise", agentManagedByLabel: "anvil-agents", agentApplicationLabel: "demo-app",
		}},
		Spec: controlv1alpha1.AgentRunSpec{Purpose: controlv1alpha1.AgentRunPurposeChained, SourceRef: controlv1alpha1.AgentRunSourceRef{APIVersion: controlv1alpha1.GroupVersion.String(), Kind: "AgentChain", Namespace: chain.Namespace, Name: chain.Name}, SourceUID: "wrong-uid", SourceGeneration: chain.Generation, SourceDigest: agentChainWorkflowDigest(chain)},
	}
	foreign.Annotations = map[string]string{agentRunChainDigestAnnotation: foreign.Spec.SourceDigest}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&controlv1alpha1.AgentChain{}).WithObjects(chain, foreign).Build()
	r := &AgentChainReconciler{Client: c, Scheme: scheme}
	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: chain.Namespace, Name: chain.Name}}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	stored := &controlv1alpha1.AgentChain{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(chain), stored); err != nil {
		t.Fatalf("get: %v", err)
	}
	if stored.Status.Phase != controlv1alpha1.AgentChainPhaseRunning || stored.Status.ActiveRunRef == nil || stored.Status.ActiveRunRef.Name == foreign.Name {
		t.Fatalf("foreign wrong-source run blocked or was adopted: %#v", stored.Status)
	}
}

func TestAgentChainFinalFailedNotCompleted(t *testing.T) {
	scheme := newAgentChainScheme(t)
	chain := baseChain("lab-final-fail")
	// Single-step chain so Failed is the final step.
	chain.Spec.Steps = chain.Spec.Steps[:1]
	chain.Status = controlv1alpha1.AgentChainStatus{
		ObservedGeneration: 1,
		Phase:              controlv1alpha1.AgentChainPhaseRunning,
		ActiveInstanceID:   "inst-f",
		ActiveStep:         "exercise",
	}
	run := &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "agentrun-final-fail",
			Namespace: "anvilhub",
			Labels: map[string]string{
				agentRunChainLabel:     sanitizeLabelValue(chain.Name),
				agentRunChainInstLabel: sanitizeLabelValue("inst-f"),
				agentRunChainStepLabel: sanitizeLabelValue("exercise"),
			},
		},
		Spec:   controlv1alpha1.AgentRunSpec{Purpose: controlv1alpha1.AgentRunPurposeChained, SourceUID: string(chain.UID)},
		Status: controlv1alpha1.AgentRunStatus{Phase: controlv1alpha1.AgentRunPhaseFailed},
	}
	freezeActiveChainForTest(chain, &chain.Status, run)
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&controlv1alpha1.AgentChain{}).WithObjects(chain, run).Build()
	r := &AgentChainReconciler{Client: c, Scheme: scheme}
	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: chain.Namespace, Name: chain.Name}}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	stored := &controlv1alpha1.AgentChain{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(chain), stored); err != nil {
		t.Fatalf("get: %v", err)
	}
	cond := apimeta.FindStatusCondition(stored.Status.Conditions, agentChainReady)
	if cond == nil || cond.Reason != "InstanceFailed" {
		t.Fatalf("expected InstanceFailed condition, got %#v", cond)
	}
	if stored.Status.ActiveInstanceID != "" {
		t.Fatalf("active instance should clear, got %q", stored.Status.ActiveInstanceID)
	}
}

func TestAgentChainBlocksActiveInstanceAcrossWorkflowEdit(t *testing.T) {
	scheme := newAgentChainScheme(t)
	chain := baseChain("workflow-drift")
	chain.Annotations = map[string]string{controlv1alpha1.AgentChainStartNowAnnotation: "token-drift"}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&controlv1alpha1.AgentChain{}, &controlv1alpha1.AgentRun{}).WithObjects(chain).Build()
	r := &AgentChainReconciler{Client: c, Scheme: scheme}
	req := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: chain.Namespace, Name: chain.Name}}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("start: %v", err)
	}
	stored := &controlv1alpha1.AgentChain{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(chain), stored); err != nil {
		t.Fatalf("get: %v", err)
	}
	oldDigest := stored.Status.ActiveWorkflowDigest
	stored.Spec.Steps[1].RunTemplate.Prompt = "changed verifier authority"
	stored.Generation++
	if err := c.Update(context.Background(), stored); err != nil {
		t.Fatalf("edit chain: %v", err)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("reconcile drift: %v", err)
	}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(chain), stored); err != nil {
		t.Fatalf("get after drift: %v", err)
	}
	if stored.Status.Phase != controlv1alpha1.AgentChainPhaseBlocked || stored.Status.ActiveWorkflowDigest != oldDigest {
		t.Fatalf("drift status = phase %s digest %q, want Blocked with frozen %q", stored.Status.Phase, stored.Status.ActiveWorkflowDigest, oldDigest)
	}
	cond := apimeta.FindStatusCondition(stored.Status.Conditions, agentChainReady)
	if cond == nil || cond.Reason != "InstanceNeedsRevalidation" {
		t.Fatalf("condition = %#v, want InstanceNeedsRevalidation", cond)
	}
	runs := &controlv1alpha1.AgentRunList{}
	if err := c.List(context.Background(), runs); err != nil || len(runs.Items) != 1 {
		t.Fatalf("workflow edit must not launch a successor: runs=%d err=%v", len(runs.Items), err)
	}
}

func TestAgentChainInvalidSpecRetainsActiveForbidOwnership(t *testing.T) {
	scheme := newAgentChainScheme(t)
	chain := baseChain("invalid-retains-active")
	chain.Annotations = map[string]string{controlv1alpha1.AgentChainStartNowAnnotation: "token-one"}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&controlv1alpha1.AgentChain{}, &controlv1alpha1.AgentRun{}).WithObjects(chain).Build()
	r := &AgentChainReconciler{Client: c, Scheme: scheme}
	req := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: chain.Namespace, Name: chain.Name}}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("start: %v", err)
	}
	stored := &controlv1alpha1.AgentChain{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(chain), stored); err != nil {
		t.Fatalf("get started chain: %v", err)
	}
	activeInstance := stored.Status.ActiveInstanceID
	activeRunName := stored.Status.ActiveRunRef.Name
	stored.Spec.Steps[1].Name = stored.Spec.Steps[0].Name
	stored.Generation++
	if err := c.Update(context.Background(), stored); err != nil {
		t.Fatalf("make spec invalid: %v", err)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("reconcile invalid spec: %v", err)
	}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(chain), stored); err != nil {
		t.Fatalf("get blocked chain: %v", err)
	}
	if stored.Status.Phase != controlv1alpha1.AgentChainPhaseBlocked || stored.Status.ActiveInstanceID != activeInstance || stored.Status.ActiveRunRef == nil || stored.Status.ActiveRunRef.Name != activeRunName {
		t.Fatalf("invalid spec released active ownership: %#v", stored.Status)
	}
	stored.Spec.Steps[1].Name = "monitor"
	stored.Spec.Steps[1].When.PreviousStep = "exercise"
	stored.Annotations[controlv1alpha1.AgentChainStartNowAnnotation] = "token-two"
	stored.Generation++
	if err := c.Update(context.Background(), stored); err != nil {
		t.Fatalf("repair spec: %v", err)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("reconcile repaired spec: %v", err)
	}
	runs := &controlv1alpha1.AgentRunList{}
	if err := c.List(context.Background(), runs); err != nil || len(runs.Items) != 1 {
		t.Fatalf("repaired spec started overlapping instance: runs=%d err=%v", len(runs.Items), err)
	}
}

func TestAgentChainIgnoresNewerForeignRunWithMatchingLabels(t *testing.T) {
	scheme := newAgentChainScheme(t)
	chain := baseChain("exact-active-ref")
	chain.Annotations = map[string]string{controlv1alpha1.AgentChainStartNowAnnotation: "token-exact"}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&controlv1alpha1.AgentChain{}, &controlv1alpha1.AgentRun{}).WithObjects(chain).Build()
	r := &AgentChainReconciler{Client: c, Scheme: scheme}
	req := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: chain.Namespace, Name: chain.Name}}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("start: %v", err)
	}
	stored := &controlv1alpha1.AgentChain{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(chain), stored); err != nil {
		t.Fatalf("get started chain: %v", err)
	}
	foreign := &controlv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "foreign-newer-run",
			Namespace:         chain.Namespace,
			UID:               types.UID("foreign-uid"),
			CreationTimestamp: metav1.NewTime(time.Now().UTC().Add(time.Minute)),
			Labels: map[string]string{
				agentRunChainLabel:     sanitizeLabelValue(chain.Name),
				agentRunChainInstLabel: sanitizeLabelValue(stored.Status.ActiveInstanceID),
				agentRunChainStepLabel: sanitizeLabelValue(stored.Status.ActiveStep),
			},
			Annotations: map[string]string{agentRunChainDigestAnnotation: stored.Status.ActiveWorkflowDigest},
		},
		Spec: controlv1alpha1.AgentRunSpec{
			Purpose:          controlv1alpha1.AgentRunPurposeChained,
			SourceRef:        controlv1alpha1.AgentRunSourceRef{APIVersion: controlv1alpha1.GroupVersion.String(), Kind: "AgentChain", Namespace: chain.Namespace, Name: chain.Name},
			SourceUID:        string(chain.UID),
			SourceGeneration: stored.Status.ActiveSourceGeneration,
			SourceDigest:     stored.Status.ActiveWorkflowDigest,
		},
		Status: controlv1alpha1.AgentRunStatus{Phase: controlv1alpha1.AgentRunPhaseSucceeded},
	}
	if err := c.Create(context.Background(), foreign); err != nil {
		t.Fatalf("create foreign run: %v", err)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("reconcile with foreign run: %v", err)
	}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(chain), stored); err != nil {
		t.Fatalf("get chain after foreign run: %v", err)
	}
	if stored.Status.Phase != controlv1alpha1.AgentChainPhaseRunning || stored.Status.ActiveStep != "exercise" || stored.Status.ActiveRunRef == nil || stored.Status.ActiveRunRef.Name == foreign.Name {
		t.Fatalf("foreign run displaced exact active ref: %#v", stored.Status)
	}
	stored.Annotations[controlv1alpha1.AgentChainCancelInstanceAnnotation] = stored.Status.ActiveInstanceID
	if err := c.Update(context.Background(), stored); err != nil {
		t.Fatalf("request cancellation: %v", err)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("request cancellation reconcile: %v", err)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("wait cancellation reconcile: %v", err)
	}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(chain), stored); err != nil {
		t.Fatalf("get cancelling chain: %v", err)
	}
	if stored.Status.Phase != controlv1alpha1.AgentChainPhaseCancelling || stored.Status.ActiveRunRef == nil || stored.Status.ActiveRunRef.Name == foreign.Name {
		t.Fatalf("foreign terminal run released cancellation ownership: %#v", stored.Status)
	}
	runs := &controlv1alpha1.AgentRunList{}
	if err := c.List(context.Background(), runs); err != nil || len(runs.Items) != 2 {
		t.Fatalf("foreign run triggered a successor: runs=%d err=%v", len(runs.Items), err)
	}
}

func TestAgentChainRejectsExistingChildWithDifferentImmutableSpec(t *testing.T) {
	scheme := newAgentChainScheme(t)
	chain := baseChain("child-collision")
	workflowDigest := agentChainWorkflowDigest(chain)
	instanceID := "instance-one"
	now := metav1.Now()
	r := &AgentChainReconciler{Scheme: scheme}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(chain).Build()
	r.Client = c
	if _, err := r.createChainedAgentRun(context.Background(), chain, "demo-app", instanceID, chain.Generation, workflowDigest, chain.Spec.Steps[0], nil, nil, "AgentChainStart", "stable"); err != nil {
		t.Fatalf("create expected child: %v", err)
	}
	changed := chain.Spec.Steps[0]
	changed.RunTemplate.Prompt = "different prompt"
	if _, err := r.createChainedAgentRun(context.Background(), chain, "demo-app", instanceID, chain.Generation, workflowDigest, changed, nil, &now, "AgentChainStart", "stable"); err == nil || !strings.Contains(err.Error(), "different immutable chain step spec") {
		t.Fatalf("expected immutable collision, got %v", err)
	}
}

func TestAgentChainRejectsExistingChildWithControllerLabelDrift(t *testing.T) {
	scheme := newAgentChainScheme(t)
	chain := baseChain("child-label-collision")
	workflowDigest := agentChainWorkflowDigest(chain)
	instanceID := "instance-one"
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(chain).Build()
	r := &AgentChainReconciler{Client: c, Scheme: scheme}
	run, err := r.createChainedAgentRun(context.Background(), chain, "demo-app", instanceID, chain.Generation, workflowDigest, chain.Spec.Steps[0], nil, nil, "AgentChainStart", "stable")
	if err != nil {
		t.Fatalf("create expected child: %v", err)
	}
	delete(run.Labels, agentApplicationLabel)
	if err := c.Update(context.Background(), run); err != nil {
		t.Fatalf("remove application label: %v", err)
	}
	if _, err := r.createChainedAgentRun(context.Background(), chain, "demo-app", instanceID, chain.Generation, workflowDigest, chain.Spec.Steps[0], nil, nil, "AgentChainStart", "stable"); err == nil || !strings.Contains(err.Error(), "different immutable chain step spec or provenance") {
		t.Fatalf("expected controller-label collision, got %v", err)
	}
}

func TestAgentChainRunSpecComparisonUsesCRDWireCanonicalization(t *testing.T) {
	left := controlv1alpha1.AgentRunSpec{}
	right := controlv1alpha1.AgentRunSpec{}
	right.Harness.Execution.Resources.Requests = corev1.ResourceList{}
	right.Harness.Execution.Resources.Limits = corev1.ResourceList{}
	if !agentChainRunSpecsEqual(left, right) {
		t.Fatal("nil and empty omitempty resource maps should compare as the same persisted CRD spec")
	}
	left.Harness.Execution.ExtraEnv = []corev1.EnvVar{{Name: "FROM_FILE", ValueFrom: &corev1.EnvVarSource{FileKeyRef: &corev1.FileKeySelector{Key: "value"}}}}
	optional := false
	right = *left.DeepCopy()
	right.Harness.Execution.ExtraEnv[0].ValueFrom.FileKeyRef.Optional = &optional
	if !agentChainRunSpecsEqual(left, right) {
		t.Fatal("nil and CRD-defaulted fileKeyRef.optional=false should compare equally")
	}
	right.Prompt = "authority-bearing difference"
	if agentChainRunSpecsEqual(left, right) {
		t.Fatal("wire canonicalization must retain material spec differences")
	}
}

func TestAgentChainCancellationWaitsForTerminalChild(t *testing.T) {
	scheme := newAgentChainScheme(t)
	chain := baseChain("cancel-waits")
	chain.Annotations = map[string]string{controlv1alpha1.AgentChainStartNowAnnotation: "token-cancel"}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&controlv1alpha1.AgentChain{}, &controlv1alpha1.AgentRun{}).WithObjects(chain).Build()
	r := &AgentChainReconciler{Client: c, Scheme: scheme}
	req := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: chain.Namespace, Name: chain.Name}}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("start: %v", err)
	}
	stored := &controlv1alpha1.AgentChain{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(chain), stored); err != nil {
		t.Fatalf("get: %v", err)
	}
	instanceID := stored.Status.ActiveInstanceID
	if stored.Annotations == nil {
		stored.Annotations = map[string]string{}
	}
	stored.Annotations[controlv1alpha1.AgentChainCancelInstanceAnnotation] = "*"
	if err := c.Update(context.Background(), stored); err != nil {
		t.Fatalf("request cancel: %v", err)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("cancel reconcile: %v", err)
	}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(chain), stored); err != nil {
		t.Fatalf("get cancelling: %v", err)
	}
	if stored.Status.Phase != controlv1alpha1.AgentChainPhaseCancelling || stored.Status.ActiveInstanceID != instanceID {
		t.Fatalf("cancel should retain ownership: phase=%s active=%q", stored.Status.Phase, stored.Status.ActiveInstanceID)
	}
	run := &controlv1alpha1.AgentRun{}
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: stored.Status.ActiveRunRef.Namespace, Name: stored.Status.ActiveRunRef.Name}, run); err != nil {
		t.Fatalf("get active run: %v", err)
	}
	run.Status.Phase = controlv1alpha1.AgentRunPhaseSucceeded
	if err := c.Status().Update(context.Background(), run); err != nil {
		t.Fatalf("finish active run: %v", err)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("finish cancellation: %v", err)
	}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(chain), stored); err != nil {
		t.Fatalf("get cancelled: %v", err)
	}
	if stored.Status.ActiveInstanceID != "" || stored.Status.CancelRequestedInstanceID != "" {
		t.Fatalf("cancelled instance still active: %#v", stored.Status)
	}
	runs := &controlv1alpha1.AgentRunList{}
	if err := c.List(context.Background(), runs); err != nil || len(runs.Items) != 1 {
		t.Fatalf("cancel must not create a successor: runs=%d err=%v", len(runs.Items), err)
	}
}

func TestAgentChainFinalCompletionRequiresDecisionAction(t *testing.T) {
	scheme := newAgentChainScheme(t)
	chain := baseChain("completion-action")
	chain.Spec.Steps = chain.Spec.Steps[:1]
	chain.Spec.Completion = &controlv1alpha1.AgentChainCompletionSpec{OnDecisionActions: []string{"browser-suite-passed"}}
	chain.Annotations = map[string]string{controlv1alpha1.AgentChainStartNowAnnotation: "token-complete"}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&controlv1alpha1.AgentChain{}, &controlv1alpha1.AgentRun{}).WithObjects(chain).Build()
	r := &AgentChainReconciler{Client: c, Scheme: scheme}
	req := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: chain.Namespace, Name: chain.Name}}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("start: %v", err)
	}
	stored := &controlv1alpha1.AgentChain{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(chain), stored); err != nil {
		t.Fatalf("get: %v", err)
	}
	run := &controlv1alpha1.AgentRun{}
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: stored.Status.ActiveRunRef.Namespace, Name: stored.Status.ActiveRunRef.Name}, run); err != nil {
		t.Fatalf("get run: %v", err)
	}
	run.Status.Phase = controlv1alpha1.AgentRunPhaseSucceeded
	run.Status.Decision = &controlv1alpha1.AgentRunDecisionStatus{Action: "no-exact-artifact"}
	if err := c.Status().Update(context.Background(), run); err != nil {
		t.Fatalf("finish run: %v", err)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("finish chain: %v", err)
	}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(chain), stored); err != nil {
		t.Fatalf("get finished: %v", err)
	}
	cond := apimeta.FindStatusCondition(stored.Status.Conditions, agentChainReady)
	if cond == nil || cond.Reason != "InstanceCompletionCriteriaUnmet" {
		t.Fatalf("condition = %#v, want InstanceCompletionCriteriaUnmet", cond)
	}
}

func TestAgentChainFinalCompletionAcceptsAllowedDecisionAction(t *testing.T) {
	scheme := newAgentChainScheme(t)
	chain := baseChain("completion-passes")
	chain.Spec.Steps = chain.Spec.Steps[:1]
	chain.Spec.Completion = &controlv1alpha1.AgentChainCompletionSpec{OnDecisionActions: []string{"browser-suite-passed"}}
	chain.Annotations = map[string]string{controlv1alpha1.AgentChainStartNowAnnotation: "token-pass"}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&controlv1alpha1.AgentChain{}, &controlv1alpha1.AgentRun{}).WithObjects(chain).Build()
	r := &AgentChainReconciler{Client: c, Scheme: scheme}
	req := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: chain.Namespace, Name: chain.Name}}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("start: %v", err)
	}
	stored := &controlv1alpha1.AgentChain{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(chain), stored); err != nil {
		t.Fatalf("get: %v", err)
	}
	run := &controlv1alpha1.AgentRun{}
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: stored.Status.ActiveRunRef.Namespace, Name: stored.Status.ActiveRunRef.Name}, run); err != nil {
		t.Fatalf("get run: %v", err)
	}
	run.Status.Phase = controlv1alpha1.AgentRunPhaseSucceeded
	run.Status.Decision = &controlv1alpha1.AgentRunDecisionStatus{Action: "Browser-Suite-Passed"}
	if err := c.Status().Update(context.Background(), run); err != nil {
		t.Fatalf("finish run: %v", err)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("finish chain: %v", err)
	}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(chain), stored); err != nil {
		t.Fatalf("get finished: %v", err)
	}
	cond := apimeta.FindStatusCondition(stored.Status.Conditions, agentChainReady)
	if cond == nil || cond.Reason != "InstanceCompleted" {
		t.Fatalf("condition = %#v, want InstanceCompleted", cond)
	}
}
