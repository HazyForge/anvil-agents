package controller

import (
	"context"
	"fmt"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	agents "github.com/hazyforge/anvil-agents/api/v1alpha1"
	"github.com/hazyforge/anvil-agents/internal/substrate"
)

// TestSubstrateLiveSuspendIdleMultiplexStress confirms suspend-on-idle
// multiplexing through the gate-on reconcile path without a cluster. A direct
// turn plus several peer child turns bind distinct warm actors on a shared
// backend, all suspend on terminal idle, and the next append-only turn per
// thread resumes warm with stable identity and zero Jobs. Wrong actor
// identity fails via binding mismatch, cold recreate fails via Created
// growth, and dropped turns fail via missing bindings.
func TestSubstrateLiveSuspendIdleMultiplexStress(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	backend := substrate.NewFakeClient()
	threads := []string{
		"multiplex-direct-1",
		"multiplex-peer-child-1",
		"multiplex-peer-child-2",
		"multiplex-peer-child-3",
	}

	firstIDs := make(map[string]string, len(threads))
	for i, thread := range threads {
		run := liveSubstrateRun(thread)
		run.Name = fmt.Sprintf("multiplex-turn-1-%d", i)
		r := liveSubstrateReconciler(t, run, backend)
		r.SubstrateClient = backend
		if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(run)}); err != nil {
			t.Fatalf("thread %q first reconcile: %v (silent drop)", thread, err)
		}
		updated := &agents.AgentRun{}
		if err := r.Get(ctx, types.NamespacedName{Namespace: run.Namespace, Name: run.Name}, updated); err != nil {
			t.Fatalf("thread %q get run: %v", thread, err)
		}
		if updated.Status.Phase != agents.AgentRunPhaseRunning {
			t.Fatalf("thread %q phase = %q, want Running", thread, updated.Status.Phase)
		}
		wantActor := substrate.ActorNameForThread(thread)
		if updated.Status.SubstrateActor == nil || updated.Status.SubstrateActor.ActorName != wantActor {
			t.Fatalf("thread %q actor = %+v, want actor %q", thread, updated.Status.SubstrateActor, wantActor)
		}
		if updated.Status.SubstrateActor.ActorID == "" {
			t.Fatalf("thread %q binding has no actor ID", thread)
		}
		firstIDs[thread] = updated.Status.SubstrateActor.ActorID
		jobs := &batchv1.JobList{}
		if err := r.List(ctx, jobs); err != nil {
			t.Fatalf("thread %q list jobs: %v", thread, err)
		}
		if len(jobs.Items) != 0 {
			t.Fatalf("thread %q reconcile created %d Jobs, want none", thread, len(jobs.Items))
		}
	}
	if got := backend.Created(); got != len(threads) {
		t.Fatalf("distinct actors after first turns = %d, want %d", got, len(threads))
	}
	for a, idA := range firstIDs {
		for b, idB := range firstIDs {
			if a != b && idA == idB {
				t.Fatalf("threads %q and %q share actor ID %q: identity churn", a, b, idA)
			}
		}
	}

	// All runs go terminal-idle: every actor suspends without losing identity.
	for _, thread := range threads {
		run := liveSubstrateRun(thread)
		r := liveSubstrateReconciler(t, run, backend)
		r.SubstrateClient = backend
		r.suspendSubstrateActorOnTerminal(ctx, run)
	}
	for _, thread := range threads {
		described, err := backend.DescribeActor(ctx, "agents", substrate.ActorNameForThread(thread))
		if err != nil {
			t.Fatalf("thread %q describe after suspend: %v", thread, err)
		}
		if described.State != substrate.ActorStateSuspended {
			t.Fatalf("thread %q idle state = %q, want Suspended", thread, described.State)
		}
		if described.ID != firstIDs[thread] {
			t.Fatalf("thread %q idle ID = %q, want stable %q", thread, described.ID, firstIDs[thread])
		}
	}

	// Next append-only turn per thread resumes its own warm actor.
	for i, thread := range threads {
		run := liveSubstrateRun(thread)
		run.Name = fmt.Sprintf("multiplex-turn-2-%d", i)
		r := liveSubstrateReconciler(t, run, backend)
		r.SubstrateClient = backend
		if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(run)}); err != nil {
			t.Fatalf("thread %q second reconcile: %v (silent drop)", thread, err)
		}
		updated := &agents.AgentRun{}
		if err := r.Get(ctx, types.NamespacedName{Namespace: run.Namespace, Name: run.Name}, updated); err != nil {
			t.Fatalf("thread %q get second run: %v", thread, err)
		}
		if updated.Status.SubstrateActor == nil || updated.Status.SubstrateActor.ActorID != firstIDs[thread] {
			t.Fatalf("thread %q second binding = %+v, want warm reuse of %q", thread, updated.Status.SubstrateActor, firstIDs[thread])
		}
		if updated.Status.SubstrateActor.State != string(substrate.ActorStateActive) {
			t.Fatalf("thread %q second state = %q, want Active", thread, updated.Status.SubstrateActor.State)
		}
	}
	if got := backend.Created(); got != len(threads) {
		t.Fatalf("distinct actors after multiplex resume = %d, want %d warm actors", got, len(threads))
	}
}
