package controller

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	agents "github.com/hazyforge/anvil-agents/api/v1alpha1"
	"github.com/hazyforge/anvil-agents/internal/substrate"
)

func liveSubstrateReconciler(t *testing.T, run *agents.AgentRun, backend *substrate.FakeClient) *AgentRunReconciler {
	t.Helper()
	scheme := newAgentControlTestScheme(t)
	builder := fake.NewClientBuilder().WithScheme(scheme).WithObjects(run).WithStatusSubresource(run)
	builder = builder.WithInterceptorFuncs(interceptor.Funcs{Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
		if obj.GetUID() == "" {
			obj.SetUID(types.UID("fake-" + obj.GetName()))
		}
		return c.Create(ctx, obj, opts...)
	}})
	return &AgentRunReconciler{
		Client:          builder.Build(),
		Scheme:          scheme,
		SubstrateClient: backend,
		CommonReconcilerOptions: CommonReconcilerOptions{Options: &Options{
			SubstrateActorsEnabled: true,
			SubstrateEndpoint:      "ate-api-server.ate-system.svc:443",
		}},
	}
}

func liveGenerateReconciler(t *testing.T, run *agents.AgentRun, backend *substrate.FakeClient, gen *substrate.FakeGenerator) *AgentRunReconciler {
	t.Helper()
	r := liveSubstrateReconciler(t, run, backend)
	r.Options.SubstrateGenerateOnActor = true
	r.Options.SubstrateGenerateActorClasses = []string{substrate.DefaultGenerateActorClass}
	if gen == nil {
		gen = &substrate.FakeGenerator{}
	}
	r.SubstrateGenerate = gen
	return r
}

func liveSubstrateRun(threadID string) *agents.AgentRun {
	run := substrateSpikeRun()
	run.Name = "standing-chat-live"
	run.Spec.SourceRef = agents.AgentRunSourceRef{Kind: "ChatThread", Name: threadID}
	run.Spec.Harness.Execution.Runtime = agents.AgentRunExecutionRuntimeSubstrateActor
	run.Spec.Harness.Execution.Substrate = &agents.AgentRunSubstrateActorSpec{ActorClass: "standing-chat", Pool: "warm"}
	return run
}

func liveGenerateRun(threadID string) *agents.AgentRun {
	run := liveSubstrateRun(threadID)
	run.Spec.Prompt = "frozen actor prompt"
	run.Spec.Harness.Execution.Substrate = &agents.AgentRunSubstrateActorSpec{ActorClass: substrate.DefaultGenerateActorClass, Pool: "warm"}
	return run
}

func TestSubstrateLiveClientRequiresGateAndEndpoint(t *testing.T) {
	t.Parallel()

	run := liveSubstrateRun("thread-1")
	gated := &AgentRunReconciler{
		SubstrateClient:         substrate.NewFakeClient(),
		CommonReconcilerOptions: CommonReconcilerOptions{Options: &Options{SubstrateActorsEnabled: true, SubstrateEndpoint: "ate-api-server.ate-system.svc:443"}},
	}
	if gated.substrateLiveClient() == nil {
		t.Fatal("gate on with endpoint and backend must yield a live client")
	}
	for _, tc := range []struct {
		name    string
		options *Options
		client  substrate.Client
	}{
		{"gate off", &Options{}, substrate.NewFakeClient()},
		{"gate on without endpoint", &Options{SubstrateActorsEnabled: true}, substrate.NewFakeClient()},
		{"gate on without backend", &Options{SubstrateActorsEnabled: true, SubstrateEndpoint: "ate-api-server.ate-system.svc:443"}, nil},
		{"nil options", nil, substrate.NewFakeClient()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := &AgentRunReconciler{SubstrateClient: tc.client, CommonReconcilerOptions: CommonReconcilerOptions{Options: tc.options}}
			if r.substrateLiveClient() != nil {
				t.Fatal("live client must stay nil without the full opt-in")
			}
			if phase, reason, _ := r.agentRunBlockingValidation(run); phase != agents.AgentRunPhaseNeedsHuman || reason != "SubstrateActorNotWired" {
				t.Fatalf("hold = %q/%q, want NeedsHuman/SubstrateActorNotWired", phase, reason)
			}
		})
	}
}

func TestSubstrateGateOnWithoutGenerateKeepsHold(t *testing.T) {
	t.Parallel()

	run := liveSubstrateRun("thread-1")
	r := &AgentRunReconciler{
		SubstrateClient:         substrate.NewFakeClient(),
		CommonReconcilerOptions: CommonReconcilerOptions{Options: &Options{SubstrateActorsEnabled: true, SubstrateEndpoint: "ate-api-server.ate-system.svc:443"}},
	}
	if phase, reason, _ := r.agentRunBlockingValidation(run); phase != agents.AgentRunPhaseNeedsHuman || reason != "SubstrateActorNotWired" {
		t.Fatalf("actorsEnabled without generate = %q/%q, want SubstrateActorNotWired so Desktop standing-chat is unchanged", phase, reason)
	}
}

func TestSubstrateGenerateLiftsHoldForAllowedClassOnly(t *testing.T) {
	t.Parallel()

	gen := &substrate.FakeGenerator{}
	allowed := liveGenerateRun("thread-1")
	r := liveGenerateReconciler(t, allowed, substrate.NewFakeClient(), gen)
	if phase, reason, message := r.agentRunBlockingValidation(allowed); phase != "" || reason != "" || message != "" {
		t.Fatalf("acp-spike validation = %q/%q/%q, want no blocking phase", phase, reason, message)
	}
	denied := liveSubstrateRun("thread-desktop")
	if phase, reason, _ := r.agentRunBlockingValidation(denied); phase != agents.AgentRunPhaseNeedsHuman || reason != "SubstrateActorNotWired" {
		t.Fatalf("standing-chat hold = %q/%q, want NotWired", phase, reason)
	}
}

func TestSubstrateGenerateCompletesWithoutJob(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	backend := substrate.NewFakeClient()
	gen := &substrate.FakeGenerator{Reply: "actor reply"}
	run := liveGenerateRun("thread-direct-1")
	r := liveGenerateReconciler(t, run, backend, gen)
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(run)}

	result, err := r.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if result.Requeue || result.RequeueAfter != 0 {
		t.Fatalf("generate success must not requeue: %+v", result)
	}
	updated := &agents.AgentRun{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: run.Namespace, Name: run.Name}, updated); err != nil {
		t.Fatalf("get run: %v", err)
	}
	if updated.Status.Phase != agents.AgentRunPhaseSucceeded {
		t.Fatalf("phase = %q, want Succeeded", updated.Status.Phase)
	}
	cond := apimeta.FindStatusCondition(updated.Status.Conditions, agentRunReady)
	if cond == nil || cond.Reason != "SubstrateActorGenerated" {
		t.Fatalf("ready = %+v, want SubstrateActorGenerated", cond)
	}
	if strings.TrimSpace(updated.Status.Output) != "actor reply" {
		t.Fatalf("output = %q, want actor reply", updated.Status.Output)
	}
	wantActor := substrate.ActorNameForThread("thread-direct-1")
	if updated.Status.SubstrateActor == nil || updated.Status.SubstrateActor.ActorName != wantActor {
		t.Fatalf("substrate actor = %+v, want actor %q", updated.Status.SubstrateActor, wantActor)
	}
	if updated.Status.JobRef != nil || updated.Status.PlannedJobRef != nil || updated.Status.JobCreateAttemptedAt != nil {
		t.Fatalf("generate-on-actor must not carry Job-launch receipts: %+v", updated.Status)
	}
	jobs := &batchv1.JobList{}
	if err := r.List(ctx, jobs); err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(jobs.Items) != 0 {
		t.Fatalf("generate created %d Jobs, want none", len(jobs.Items))
	}
	if len(gen.Calls) != 1 || gen.Calls[0].Prompt != "frozen actor prompt" || gen.Calls[0].ActorName != wantActor {
		t.Fatalf("generate calls = %+v", gen.Calls)
	}
}

func TestSubstrateGenerateFailureDoesNotStayBound(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	backend := substrate.NewFakeClient()
	gen := &substrate.FakeGenerator{Err: errors.New("actor refused the prompt")}
	run := liveGenerateRun("thread-fail-1")
	r := liveGenerateReconciler(t, run, backend, gen)
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(run)}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	updated := &agents.AgentRun{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: run.Namespace, Name: run.Name}, updated); err != nil {
		t.Fatalf("get run: %v", err)
	}
	if updated.Status.Phase != agents.AgentRunPhaseFailed {
		t.Fatalf("phase = %q, want Failed", updated.Status.Phase)
	}
	cond := apimeta.FindStatusCondition(updated.Status.Conditions, agentRunReady)
	if cond == nil || cond.Reason != "SubstrateActorGenerateFailed" {
		t.Fatalf("ready = %+v, want SubstrateActorGenerateFailed", cond)
	}
	if strings.Contains(updated.Status.Error, "actor refused") {
		t.Fatal("status must not copy generate error text that might include secrets")
	}
}

func TestSubstrateGenerateTransientRequeuesWithoutFailing(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	backend := substrate.NewFakeClient()
	gen := &substrate.FakeGenerator{Err: fmt.Errorf("%w: HTTP 503", substrate.ErrGenerateTransient)}
	run := liveGenerateRun("thread-park-1")
	r := liveGenerateReconciler(t, run, backend, gen)
	_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(run)})
	if !errors.Is(err, substrate.ErrGenerateTransient) {
		t.Fatalf("reconcile err = %v, want ErrGenerateTransient", err)
	}
	updated := &agents.AgentRun{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: run.Namespace, Name: run.Name}, updated); err != nil {
		t.Fatalf("get run: %v", err)
	}
	if updated.Status.Phase != agents.AgentRunPhaseRunning {
		t.Fatalf("phase = %q, want Running", updated.Status.Phase)
	}
	cond := apimeta.FindStatusCondition(updated.Status.Conditions, agentRunReady)
	if cond == nil || cond.Reason != "SubstrateActorBound" {
		t.Fatalf("ready = %+v, want SubstrateActorBound while retrying", cond)
	}
}

func TestSubstrateStandingChatDoesNotGenerate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	backend := substrate.NewFakeClient()
	gen := &substrate.FakeGenerator{Reply: "stolen"}
	run := liveSubstrateRun("thread-desktop-1")
	run.Spec.Prompt = "hey"
	r := liveGenerateReconciler(t, run, backend, gen)
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(run)}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	updated := &agents.AgentRun{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: run.Namespace, Name: run.Name}, updated); err != nil {
		t.Fatalf("get run: %v", err)
	}
	if updated.Status.Phase != agents.AgentRunPhaseNeedsHuman {
		t.Fatalf("phase = %q, want NeedsHuman hold for standing-chat", updated.Status.Phase)
	}
	if len(gen.Calls) != 0 {
		t.Fatalf("ProcessBackend class must not hit atenet: %+v", gen.Calls)
	}
	if backend.Created() != 0 {
		t.Fatal("standing-chat must not bind an ATE actor")
	}
}

func TestSubstrateLiveReconcileWarmsOnSecondTurn(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	backend := substrate.NewFakeClient()
	gen := &substrate.FakeGenerator{}
	run := liveGenerateRun("thread-direct-2")
	r := liveGenerateReconciler(t, run, backend, gen)
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(run)}

	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	if got := backend.Created(); got != 1 {
		t.Fatalf("distinct actors after first turn = %d, want 1", got)
	}
	second := liveGenerateRun("thread-direct-2")
	second.Name = "standing-chat-live-2"
	r2 := liveGenerateReconciler(t, second, backend, gen)
	r2.SubstrateClient = backend
	req2 := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(second)}
	if _, err := r2.Reconcile(ctx, req2); err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	if got := backend.Created(); got != 1 {
		t.Fatalf("distinct actors after second turn = %d, want warm reuse of 1", got)
	}
	updated := &agents.AgentRun{}
	if err := r2.Get(ctx, types.NamespacedName{Namespace: second.Namespace, Name: second.Name}, updated); err != nil {
		t.Fatalf("get second run: %v", err)
	}
	if updated.Status.SubstrateActor == nil || updated.Status.SubstrateActor.ActorName != substrate.ActorNameForThread("thread-direct-2") {
		t.Fatalf("second turn actor = %+v, want the shared thread actor", updated.Status.SubstrateActor)
	}
	if updated.Status.Phase != agents.AgentRunPhaseSucceeded {
		t.Fatalf("second phase = %q, want Succeeded", updated.Status.Phase)
	}
}

func TestSubstrateLivePeerChildResumesRecipientActor(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	backend := substrate.NewFakeClient()
	child := liveGenerateRun("child-recipient-thread")
	child.Name = "peer-delivery-live"
	r := liveGenerateReconciler(t, child, backend, nil)
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(child)}

	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("peer reconcile: %v", err)
	}
	updated := &agents.AgentRun{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: child.Namespace, Name: child.Name}, updated); err != nil {
		t.Fatalf("get peer run: %v", err)
	}
	wantActor := substrate.ActorNameForThread("child-recipient-thread")
	if updated.Status.SubstrateActor == nil || updated.Status.SubstrateActor.ActorName != wantActor {
		t.Fatalf("peer actor = %+v, want recipient actor %q", updated.Status.SubstrateActor, wantActor)
	}
	if updated.Status.Phase != agents.AgentRunPhaseSucceeded {
		t.Fatalf("peer phase = %q, want Succeeded", updated.Status.Phase)
	}
	peerJobs := &batchv1.JobList{}
	if err := r.List(ctx, peerJobs); err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(peerJobs.Items) != 0 {
		t.Fatalf("peer delivery created %d Jobs, want none", len(peerJobs.Items))
	}
}

func TestSubstrateLiveDefaultJobPathUntouched(t *testing.T) {
	t.Parallel()

	run := substrateSpikeRun()
	r := &AgentRunReconciler{
		SubstrateClient:         substrate.NewFakeClient(),
		CommonReconcilerOptions: CommonReconcilerOptions{Options: &Options{SubstrateActorsEnabled: true, SubstrateEndpoint: "ate-api-server.ate-system.svc:443"}},
	}
	if phase, reason, message := r.agentRunBlockingValidation(run); phase != "" || reason != "" || message != "" {
		t.Fatalf("job validation = %q/%q/%q, want no blocking phase", phase, reason, message)
	}
	if got := run.Spec.Harness.Execution.EffectiveRuntime(); got != agents.AgentRunExecutionRuntimeJob {
		t.Fatalf("default runtime = %q, want Job", got)
	}
}

func TestSubstrateTerminalRunSuspendsActor(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	backend := substrate.NewFakeClient()
	run := liveGenerateRun("thread-terminal-1")
	r := liveGenerateReconciler(t, run, backend, nil)
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(run)}

	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	suspended, err := backend.DescribeActor(ctx, run.Namespace, substrate.ActorNameForThread("thread-terminal-1"))
	if err != nil {
		t.Fatalf("describe after generate: %v", err)
	}
	if suspended.State != substrate.ActorStateSuspended {
		t.Fatalf("idle actor state = %q, want Suspended after generate success", suspended.State)
	}
	r.suspendSubstrateActorOnTerminal(ctx, substrateSpikeRun())
	missing := liveGenerateRun("thread-never-bound")
	r.suspendSubstrateActorOnTerminal(ctx, missing)
}

func TestSubstrateActorSpecCarriesThreadBinding(t *testing.T) {
	t.Parallel()

	run := liveSubstrateRun("thread-bind-1")
	spec := substrateActorSpecForRun(run, substrate.ActorNameForThread("thread-bind-1"))
	if spec.Namespace != run.Namespace || spec.Name != substrate.ActorNameForThread("thread-bind-1") {
		t.Fatalf("actor spec identity = %+v, want the thread-bound actor", spec)
	}
	if spec.ActorClass != "standing-chat" || spec.Pool != "warm" {
		t.Fatalf("actor tuning = %+v, want the CRD class and pool", spec)
	}
	if err := substrate.ValidateSpec(spec); err != nil {
		t.Fatalf("actor spec invalid: %v", err)
	}
	if got := substrate.ThreadIDForRun(run.Spec.SourceRef.Kind, run.Spec.SourceRef.Name, run.Name); got != "thread-bind-1" {
		t.Fatalf("thread id = %q, want thread-bind-1", got)
	}
}
