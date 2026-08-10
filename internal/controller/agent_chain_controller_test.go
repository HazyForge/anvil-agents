package controller

import (
	"context"
	"strings"
	"testing"
	"time"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
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

func TestAgentChainStartAndAdvance(t *testing.T) {
	scheme := newAgentChainScheme(t)
	chain := baseChain("lab-chain")
	chain.Annotations = map[string]string{
		controlv1alpha1.AgentChainStartNowAnnotation: "token-1",
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&controlv1alpha1.AgentChain{}).WithObjects(chain).Build()
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
