package substrate

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"
)

// multiplexSpec builds the turn spec for one thread actor. Every turn on the
// same thread must address the same actor name so suspend-on-idle multiplexes
// workers without churning execution identity.
func multiplexSpec(threadID string, turn string) ActorSpec {
	return ActorSpecForRun("agents", ActorNameForThread(threadID), "openCode", "standing-chat", "warm",
		map[string]string{"control.anvil.hazyforge.io/agent-run": turn})
}

// TestSuspendIdleMultiplexManyActorsWarmResume stresses the many-actor shape
// of suspend-on-idle multiplexing: N distinct thread actors go idle and must
// each resume warm with stable identity. A regression that mixes up actor
// identity (wrong name/ID mapping) or cold-recreates suspended actors fails
// here via ID mismatch or Created growth.
func TestSuspendIdleMultiplexManyActorsWarmResume(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client := NewFakeClient()
	const actors = 8

	coldIDs := make(map[string]string, actors)
	for i := 0; i < actors; i++ {
		thread := fmt.Sprintf("multiplex-thread-%d", i)
		cold, warm, err := EnsureTurnActor(ctx, client, multiplexSpec(thread, fmt.Sprintf("cold-turn-%d", i)))
		if err != nil || warm {
			t.Fatalf("thread %q first ensure = warm=%v err=%v, want cold", thread, warm, err)
		}
		if cold.ID == "" || cold.State != ActorStateActive {
			t.Fatalf("thread %q cold handle = %+v, want active with an ID", thread, cold)
		}
		coldIDs[thread] = cold.ID
	}
	if got := client.Created(); got != actors {
		t.Fatalf("distinct actors after cold creates = %d, want %d", got, actors)
	}
	// Actor names must be pairwise distinct; identity churn across threads
	// would collapse the pool.
	seen := map[string]string{}
	for thread, id := range coldIDs {
		name := ActorNameForThread(thread)
		if prev, ok := seen[name]; ok {
			t.Fatalf("actor name %q shared by threads %q and %q", name, prev, thread)
		}
		seen[name] = thread
		_ = id
	}

	// All actors go idle at once, as a pool multiplex would.
	for thread := range coldIDs {
		if suspended, err := SuspendIdleActor(ctx, client, "agents", ActorNameForThread(thread), nil); err != nil || !suspended {
			t.Fatalf("thread %q suspend = %v/%v, want suspended", thread, suspended, err)
		}
	}
	for thread, wantID := range coldIDs {
		described, err := client.DescribeActor(ctx, "agents", ActorNameForThread(thread))
		if err != nil {
			t.Fatalf("thread %q describe: %v", thread, err)
		}
		if described.State != ActorStateSuspended {
			t.Fatalf("thread %q idle state = %q, want Suspended", thread, described.State)
		}
		if described.ID != wantID {
			t.Fatalf("thread %q idle ID = %q, want stable %q", thread, described.ID, wantID)
		}
	}

	// Every next turn resumes its own warm actor: no cold recreates, no
	// cross-thread identity swaps.
	for thread, wantID := range coldIDs {
		resumed, warm, err := EnsureTurnActor(ctx, client, multiplexSpec(thread, "warm-turn-"+thread))
		if err != nil || !warm {
			t.Fatalf("thread %q post-idle ensure = warm=%v err=%v, want warm resume", thread, warm, err)
		}
		if resumed.ID != wantID {
			t.Fatalf("thread %q resumed ID = %q, want warm reuse of %q", thread, resumed.ID, wantID)
		}
		if resumed.State != ActorStateActive {
			t.Fatalf("thread %q resumed state = %q, want Active", thread, resumed.State)
		}
	}
	if got := client.Created(); got != actors {
		t.Fatalf("distinct actors after multiplex resume = %d, want %d warm actors", got, actors)
	}
}

// TestSuspendIdleRapidCreateSuspendResumeCycles hammers one actor with rapid
// Create->Suspend->Resume cycles. Each cycle must resume warm with a stable
// ID; a regression that drops suspended actors (NotFound on resume) or pays
// a cold recreate per cycle fails via warm=false, ID churn, or Created > 1.
func TestSuspendIdleRapidCreateSuspendResumeCycles(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client := NewFakeClient()
	const thread = "rapid-cycle-thread"
	const cycles = 50

	cold, warm, err := EnsureTurnActor(ctx, client, multiplexSpec(thread, "cold-turn"))
	if err != nil || warm {
		t.Fatalf("first ensure = warm=%v err=%v, want cold", warm, err)
	}
	for i := 0; i < cycles; i++ {
		if suspended, err := SuspendIdleActor(ctx, client, "agents", ActorNameForThread(thread), nil); err != nil || !suspended {
			t.Fatalf("cycle %d suspend = %v/%v, want suspended", i, suspended, err)
		}
		described, err := client.DescribeActor(ctx, "agents", ActorNameForThread(thread))
		if err != nil {
			t.Fatalf("cycle %d describe: %v", i, err)
		}
		if described.State != ActorStateSuspended {
			t.Fatalf("cycle %d idle state = %q, want Suspended", i, described.State)
		}
		if described.ID != cold.ID {
			t.Fatalf("cycle %d idle ID = %q, want stable %q", i, described.ID, cold.ID)
		}
		resumed, warm, err := EnsureTurnActor(ctx, client, multiplexSpec(thread, fmt.Sprintf("turn-%d", i)))
		if err != nil {
			t.Fatalf("cycle %d resume: %v (silent drop)", i, err)
		}
		if !warm {
			t.Fatalf("cycle %d resume reported cold: suspended actor must resume warm", i)
		}
		if resumed.ID != cold.ID {
			t.Fatalf("cycle %d resumed ID = %q, want warm reuse of %q", i, resumed.ID, cold.ID)
		}
		if resumed.State != ActorStateActive {
			t.Fatalf("cycle %d resumed state = %q, want Active", i, resumed.State)
		}
	}
	if got := client.Created(); got != 1 {
		t.Fatalf("distinct actors after %d cycles = %d, want exactly one warm actor", cycles, got)
	}
	final, err := client.DescribeActor(ctx, "agents", ActorNameForThread(thread))
	if err != nil {
		t.Fatalf("final describe: %v", err)
	}
	if final.ID != cold.ID || final.Resumes != cycles {
		t.Fatalf("final handle = %+v, want stable ID with %d resumes", final, cycles)
	}
}

// TestSuspendIdleConcurrentPeerDirectResumeWarm stresses the concurrent
// peer+direct shape: a direct turn and several peer recipient turns share the
// pool, all suspend on idle, then a goroutine storm resumes them at once.
// Warm resume must hold per thread with no identity churn and no silent drops
// (every goroutine binds exactly its own thread actor).
func TestSuspendIdleConcurrentPeerDirectResumeWarm(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client := NewFakeClient()

	parentTurnID := uuid.NewString()
	directThread := "wrapper-thread-concurrent"
	peerThreads := []string{directThread}
	for _, recipient := range []string{"peer-alpha", "peer-beta", "peer-gamma"} {
		childID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("anvil-chat-child/"+parentTurnID+"/"+recipient)).String()
		peerThreads = append(peerThreads, childID)
	}
	// Direct and peer threads must address distinct actors or turns would
	// collapse onto one execution identity.
	for i := 0; i < len(peerThreads); i++ {
		for j := i + 1; j < len(peerThreads); j++ {
			if ActorNameForThread(peerThreads[i]) == ActorNameForThread(peerThreads[j]) {
				t.Fatalf("threads %q and %q collide on actor %q", peerThreads[i], peerThreads[j], ActorNameForThread(peerThreads[i]))
			}
		}
	}

	wantIDs := make(map[string]string, len(peerThreads))
	for _, thread := range peerThreads {
		cold, warm, err := EnsureTurnActor(ctx, client, multiplexSpec(thread, "cold-"+thread[:8]))
		if err != nil || warm {
			t.Fatalf("thread %q first ensure = warm=%v err=%v, want cold", thread, warm, err)
		}
		wantIDs[thread] = cold.ID
	}
	for _, thread := range peerThreads {
		if suspended, err := SuspendIdleActor(ctx, client, "agents", ActorNameForThread(thread), nil); err != nil || !suspended {
			t.Fatalf("thread %q suspend = %v/%v, want suspended", thread, suspended, err)
		}
	}

	// Concurrent resume storm: several workers per thread resume at once.
	const workersPerThread = 8
	var wg sync.WaitGroup
	errCh := make(chan error, len(peerThreads)*workersPerThread)
	for _, thread := range peerThreads {
		for w := 0; w < workersPerThread; w++ {
			wg.Add(1)
			go func(thread string, worker int) {
				defer wg.Done()
				resumed, warm, err := EnsureTurnActor(ctx, client, multiplexSpec(thread, fmt.Sprintf("storm-%d", worker)))
				if err != nil {
					errCh <- fmt.Errorf("thread %q worker %d resume: %w (silent drop)", thread, worker, err)
					return
				}
				if !warm {
					errCh <- fmt.Errorf("thread %q worker %d reported cold: suspended actor must resume warm", thread, worker)
					return
				}
				if resumed.ID != wantIDs[thread] {
					errCh <- fmt.Errorf("thread %q worker %d ID = %q, want warm reuse of %q", thread, worker, resumed.ID, wantIDs[thread])
					return
				}
				if resumed.State != ActorStateActive {
					errCh <- fmt.Errorf("thread %q worker %d state = %q, want Active", thread, worker, resumed.State)
				}
			}(thread, w)
		}
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
	if got := client.Created(); got != len(peerThreads) {
		t.Fatalf("distinct actors after concurrent resume = %d, want %d warm actors", got, len(peerThreads))
	}

	// Interleaved suspend->resume rounds per thread, still concurrent across
	// threads: no round may drop its turn or churn identity.
	const rounds = 10
	errCh = make(chan error, len(peerThreads)*rounds)
	for _, thread := range peerThreads {
		wg.Add(1)
		go func(thread string) {
			defer wg.Done()
			for r := 0; r < rounds; r++ {
				if _, err := client.SuspendActor(ctx, "agents", ActorNameForThread(thread)); err != nil {
					errCh <- fmt.Errorf("thread %q round %d suspend: %w", thread, r, err)
					return
				}
				resumed, warm, err := EnsureTurnActor(ctx, client, multiplexSpec(thread, fmt.Sprintf("round-%d", r)))
				if err != nil {
					errCh <- fmt.Errorf("thread %q round %d resume: %w (silent drop)", thread, r, err)
					return
				}
				if !warm {
					errCh <- fmt.Errorf("thread %q round %d reported cold after suspend", thread, r)
					return
				}
				if resumed.ID != wantIDs[thread] {
					errCh <- fmt.Errorf("thread %q round %d ID = %q, want %q", thread, r, resumed.ID, wantIDs[thread])
					return
				}
			}
		}(thread)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
	if got := client.Created(); got != len(peerThreads) {
		t.Fatalf("distinct actors after interleaved rounds = %d, want %d", got, len(peerThreads))
	}
}

// TestATEClientSuspendIdleMultiplexStress mirrors the multiplex contract
// against the live ATE mapping (in-memory ATEControl, no cluster): multiple
// actors suspend on idle and resume warm with stable server UIDs and
// preserved resume counts. A mapping regression that resets identity or
// resume accounting on the live path fails here.
func TestATEClientSuspendIdleMultiplexStress(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client, fake := mustLiveClient(t, newFakeATEControl())
	const actors = 4
	const cycles = 20

	wantUIDs := make(map[string]string, actors)
	for i := 0; i < actors; i++ {
		thread := fmt.Sprintf("ate-multiplex-thread-%d", i)
		cold, warm, err := EnsureTurnActor(ctx, client, multiplexSpec(thread, fmt.Sprintf("cold-%d", i)))
		if err != nil || warm {
			t.Fatalf("thread %q first ensure = warm=%v err=%v, want cold", thread, warm, err)
		}
		if cold.ID == "" {
			t.Fatalf("thread %q cold handle has no server UID", thread)
		}
		wantUIDs[thread] = cold.ID
	}
	if len(fake.actors) != actors {
		t.Fatalf("live actors after cold creates = %d, want %d", len(fake.actors), actors)
	}

	for round := 0; round < cycles; round++ {
		for thread, wantUID := range wantUIDs {
			if _, err := client.SuspendActor(ctx, "agents", ActorNameForThread(thread)); err != nil {
				t.Fatalf("round %d thread %q suspend: %v", round, thread, err)
			}
			resumed, warm, err := EnsureTurnActor(ctx, client, multiplexSpec(thread, fmt.Sprintf("round-%d", round)))
			if err != nil {
				t.Fatalf("round %d thread %q resume: %v (silent drop)", round, thread, err)
			}
			if !warm {
				t.Fatalf("round %d thread %q reported cold: suspended live actor must resume warm", round, thread)
			}
			if resumed.ID != wantUID {
				t.Fatalf("round %d thread %q UID = %q, want stable %q", round, thread, resumed.ID, wantUID)
			}
			if resumed.State != ActorStateActive {
				t.Fatalf("round %d thread %q state = %q, want Active", round, thread, resumed.State)
			}
		}
	}

	// Re-creating an existing live actor reuses the warm actor and preserves
	// the observed resume count instead of resetting execution identity.
	for thread, wantUID := range wantUIDs {
		reused, err := client.CreateActor(ctx, multiplexSpec(thread, "recreate"))
		if err != nil {
			t.Fatalf("thread %q re-create: %v", thread, err)
		}
		if reused.ID != wantUID {
			t.Fatalf("thread %q re-create UID = %q, want warm reuse of %q", thread, reused.ID, wantUID)
		}
		if reused.Resumes != cycles {
			t.Fatalf("thread %q re-create resumes = %d, want %d preserved", thread, reused.Resumes, cycles)
		}
	}
	if len(fake.actors) != actors {
		t.Fatalf("live actors after multiplex stress = %d, want %d", len(fake.actors), actors)
	}
}
