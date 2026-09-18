package controller

import (
	"context"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	agents "github.com/hazyforge/anvil-agents/api/v1alpha1"
	"github.com/hazyforge/anvil-agents/internal/substrate"
)

// liveSubstrateReconciler returns a reconciler with the explicit opt-in gate on
// and an in-memory actor backend, so live-wiring tests never need a Substrate
// cluster.
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
			SubstrateEndpoint:      "http://substrate-gateway.substrate:8080",
		}},
	}
}

func liveSubstrateRun(threadID string) *agents.AgentRun {
	run := substrateSpikeRun()
	run.Name = "standing-chat-live"
	run.Spec.SourceRef = agents.AgentRunSourceRef{Kind: "ChatThread", Name: threadID}
	run.Spec.Harness.Execution.Runtime = agents.AgentRunExecutionRuntimeSubstrateActor
	run.Spec.Harness.Execution.Substrate = &agents.AgentRunSubstrateActorSpec{ActorClass: "standing-chat", Pool: "warm"}
	return run
}

func TestSubstrateLiveClientRequiresGateAndEndpoint(t *testing.T) {
	t.Parallel()

	run := liveSubstrateRun("thread-1")
	gated := &AgentRunReconciler{
		SubstrateClient:         substrate.NewFakeClient(),
		CommonReconcilerOptions: CommonReconcilerOptions{Options: &Options{SubstrateActorsEnabled: true, SubstrateEndpoint: "http://substrate-gateway.substrate:8080"}},
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
		{"gate on without backend", &Options{SubstrateActorsEnabled: true, SubstrateEndpoint: "http://substrate-gateway.substrate:8080"}, nil},
		{"nil options", nil, substrate.NewFakeClient()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := &AgentRunReconciler{SubstrateClient: tc.client, CommonReconcilerOptions: CommonReconcilerOptions{Options: tc.options}}
			if r.substrateLiveClient() != nil {
				t.Fatal("live client must stay nil without the full opt-in")
			}
			// Without the live backend the API-first hold still applies.
			if phase, reason, _ := r.agentRunBlockingValidation(run); phase != agents.AgentRunPhaseNeedsHuman || reason != "SubstrateActorNotWired" {
				t.Fatalf("hold = %q/%q, want NeedsHuman/SubstrateActorNotWired", phase, reason)
			}
		})
	}
}

func TestSubstrateGateOnLiftsHoldWithoutJob(t *testing.T) {
	t.Parallel()

	run := liveSubstrateRun("thread-1")
	r := &AgentRunReconciler{
		SubstrateClient:         substrate.NewFakeClient(),
		CommonReconcilerOptions: CommonReconcilerOptions{Options: &Options{SubstrateActorsEnabled: true, SubstrateEndpoint: "http://substrate-gateway.substrate:8080"}},
	}
	if phase, reason, message := r.agentRunBlockingValidation(run); phase != "" || reason != "" || message != "" {
		t.Fatalf("live validation = %q/%q/%q, want no blocking phase", phase, reason, message)
	}
}

func TestSubstrateLiveReconcileBindsActorWithoutJob(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	backend := substrate.NewFakeClient()
	run := liveSubstrateRun("thread-direct-1")
	r := liveSubstrateReconciler(t, run, backend)
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(run)}

	result, err := r.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if !result.Requeue && result.RequeueAfter == 0 {
		t.Fatal("live actor reconcile must requeue for warm-resume polling")
	}
	updated := &agents.AgentRun{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: run.Namespace, Name: run.Name}, updated); err != nil {
		t.Fatalf("get run: %v", err)
	}
	if updated.Status.Phase != agents.AgentRunPhaseRunning {
		t.Fatalf("phase = %q, want Running", updated.Status.Phase)
	}
	if updated.Status.ExecutionRuntime != string(agents.AgentRunExecutionRuntimeSubstrateActor) {
		t.Fatalf("execution runtime = %q, want SubstrateActor", updated.Status.ExecutionRuntime)
	}
	wantActor := substrate.ActorNameForThread("thread-direct-1")
	if updated.Status.SubstrateActor == nil || updated.Status.SubstrateActor.ActorName != wantActor {
		t.Fatalf("substrate actor = %+v, want actor %q", updated.Status.SubstrateActor, wantActor)
	}
	if updated.Status.SubstrateActor.ActorID == "" || updated.Status.SubstrateActor.State != string(substrate.ActorStateActive) {
		t.Fatalf("substrate actor binding = %+v, want active with an ID", updated.Status.SubstrateActor)
	}
	if updated.Status.JobRef != nil || updated.Status.PlannedJobRef != nil || updated.Status.JobCreateAttemptedAt != nil {
		t.Fatalf("live actor run must not carry Job-launch receipts: %+v", updated.Status)
	}
	jobs := &batchv1.JobList{}
	if err := r.List(ctx, jobs); err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(jobs.Items) != 0 {
		t.Fatalf("live actor reconcile created %d Jobs, want none", len(jobs.Items))
	}
	if updated.Status.CompletedAt != nil {
		t.Fatal("running actor binding must not set CompletedAt")
	}
}

func TestSubstrateLiveReconcileWarmsOnSecondTurn(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	backend := substrate.NewFakeClient()
	run := liveSubstrateRun("thread-direct-2")
	r := liveSubstrateReconciler(t, run, backend)
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(run)}

	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	if got := backend.Created(); got != 1 {
		t.Fatalf("distinct actors after first turn = %d, want 1", got)
	}
	// A second turn on the same thread (new append-only AgentRun) resumes the
	// warm actor instead of paying another cold create.
	second := liveSubstrateRun("thread-direct-2")
	second.Name = "standing-chat-live-2"
	r2 := liveSubstrateReconciler(t, second, backend)
	// Share the backend across both reconcilers like the live gateway would.
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
}

func TestSubstrateLivePeerChildResumesRecipientActor(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	backend := substrate.NewFakeClient()
	// Peer coordination creates one durable child thread per recipient; the
	// child run carries the child thread as its ChatThread source, so the live
	// path resumes the recipient actor with no new peer protocol.
	child := liveSubstrateRun("child-recipient-thread")
	child.Name = "peer-delivery-live"
	r := liveSubstrateReconciler(t, child, backend)
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
	if updated.Status.Phase != agents.AgentRunPhaseRunning {
		t.Fatalf("peer phase = %q, want Running", updated.Status.Phase)
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

	// With the gate on, a default-runtime run must not consult the actor plane
	// at all: no hold, no actor binding, no live client requirement.
	run := substrateSpikeRun()
	r := &AgentRunReconciler{
		SubstrateClient:         substrate.NewFakeClient(),
		CommonReconcilerOptions: CommonReconcilerOptions{Options: &Options{SubstrateActorsEnabled: true, SubstrateEndpoint: "http://substrate-gateway.substrate:8080"}},
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
	run := liveSubstrateRun("thread-terminal-1")
	r := liveSubstrateReconciler(t, run, backend)
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(run)}

	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	described, err := backend.DescribeActor(ctx, run.Namespace, substrate.ActorNameForThread("thread-terminal-1"))
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	if described.State != substrate.ActorStateActive {
		t.Fatalf("bound actor state = %q, want Active", described.State)
	}
	r.suspendSubstrateActorOnTerminal(ctx, run)
	suspended, err := backend.DescribeActor(ctx, run.Namespace, substrate.ActorNameForThread("thread-terminal-1"))
	if err != nil {
		t.Fatalf("describe after suspend: %v", err)
	}
	if suspended.State != substrate.ActorStateSuspended {
		t.Fatalf("idle actor state = %q, want Suspended", suspended.State)
	}
	// Suspend is best-effort: missing actors and Job runs never error.
	r.suspendSubstrateActorOnTerminal(ctx, substrateSpikeRun())
	missing := liveSubstrateRun("thread-never-bound")
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
