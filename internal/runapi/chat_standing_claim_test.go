package runapi

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	agentsv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
	"github.com/hazyforge/anvil-agents/internal/chat"
	"github.com/hazyforge/anvil-agents/internal/standing"
)

// Slice-4 multi-replica claim tests. Two API replicas share one Kubernetes
// fake client and one chat store (two processes, one cluster); each keeps its
// own FakeBackend (two processes, two session tables). No live harness, no
// model calls, no credentials.

// sharedStandingServers builds two live-gate API replicas over shared state.
func sharedStandingServers(t *testing.T) (*Server, *Server) {
	t.Helper()
	config := DefaultConfig()
	config.OIDC.Issuer = "https://issuer.example"
	config.OIDC.Audiences = []string{"anvil-agents-api"}
	config.Chat.Enabled = true
	config.Runs.CreateEnabled = true
	config.Authorization.Bindings = []AuthorizationBinding{{
		Roles:       []string{"viewer"},
		Permissions: []string{PermissionRunsRead, PermissionRunsStream, PermissionChatRead, PermissionChatWrite, PermissionRunsCreate},
		Namespaces:  []string{"agents"},
	}}
	scheme := runtime.NewScheme()
	if err := agentsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	shared := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&agentsv1alpha1.AgentRun{}).WithObjects(&agentsv1alpha1.AgentRunProfile{ObjectMeta: metav1.ObjectMeta{Name: "grok45", Namespace: "agents"}}).Build()
	store := chat.NewMemoryStore()
	replica := func(owner string) *Server {
		server, err := NewServer(config, staticAuthenticator{
			ready:     true,
			principal: testPrincipal(time.Now().Add(time.Hour)),
		}, shared, staticLogSource{}, logr.Discard())
		if err != nil {
			t.Fatal(err)
		}
		server.SetChatStore(store)
		server.SetStandingBackend(standing.NewFakeBackend())
		server.config.Standing.LiveEnabled = true
		server.standingOwner = owner
		return server
	}
	return replica("api-a/1"), replica("api-b/2")
}

func getTestRun(t *testing.T, server *Server, namespace, name string) *agentsv1alpha1.AgentRun {
	t.Helper()
	run := &agentsv1alpha1.AgentRun{}
	if err := server.writes.Get(context.Background(), types.NamespacedName{Namespace: namespace, Name: name}, run); err != nil {
		t.Fatal(err)
	}
	return run
}

func TestStandingClaimOnlyOneReplicaDrives(t *testing.T) {
	ctx := context.Background()
	a, b := sharedStandingServers(t)
	standingHarness(t, a, "standing-harness", agentsv1alpha1.AgentRunHarnessBackendOpenCode)
	thread := standingThread(t, a, "standing-harness")

	// A queues: it claims the turn and streams it to Succeeded synchronously.
	accepted, err := a.queueChatTurn(ctx, "agents", thread.ID, AppendChatMessageRequest{Content: "hello standing"})
	if err != nil {
		t.Fatal(err)
	}
	if accepted.Turn.Status != "succeeded" {
		t.Fatalf("turn status = %q, want succeeded", accepted.Turn.Status)
	}
	stored := getTestRun(t, a, "agents", accepted.Turn.RunName)
	claim, err := standing.ParseClaim(stored.Annotations[agentsv1alpha1.AgentRunStandingClaimAnnotation])
	if err != nil {
		t.Fatalf("claim annotation = %q: %v", stored.Annotations[agentsv1alpha1.AgentRunStandingClaimAnnotation], err)
	}
	if claim.TurnID != accepted.Turn.ID || claim.Owner != "api-a/1" {
		t.Fatalf("claim = %+v, want this turn owned by api-a/1", claim)
	}

	// B reconciling the same turn observes the winner's result instead of
	// streaming a second reply: the turn stays succeeded with one reply.
	turns, err := b.chatStore.ListTurns(ctx, "agents", thread.ID)
	if err != nil || len(turns) != 1 {
		t.Fatalf("turns = %#v %v, want one turn", turns, err)
	}
	if err := b.reconcileChatTurn(ctx, &turns[0]); err != nil {
		t.Fatal(err)
	}
	if turns[0].Status != "succeeded" {
		t.Fatalf("loser turn status = %q, want succeeded off the winner", turns[0].Status)
	}
	messages, err := b.chatStore.ListMessages(ctx, "agents", thread.ID)
	if err != nil || len(messages) != 2 {
		t.Fatalf("messages = %#v %v, want exactly user plus one winner reply", messages, err)
	}
}

func TestStandingClaimRaceLoserKeepsHold(t *testing.T) {
	ctx := context.Background()
	a, b := sharedStandingServers(t)
	standingHarness(t, a, "standing-harness", agentsv1alpha1.AgentRunHarnessBackendOpenCode)
	thread := standingThread(t, a, "standing-harness")

	// A fresh unstreamed run both replicas can see at the same revision.
	race := &agentsv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "race-run",
			Namespace: "agents",
			Labels:    map[string]string{chatTurnLabel: "turn-race", chatThreadLabel: thread.ID},
		},
		Spec: agentsv1alpha1.AgentRunSpec{Prompt: "race prompt"},
	}
	if err := a.writes.Create(ctx, race); err != nil {
		t.Fatal(err)
	}
	raceTurn := &chat.Turn{ID: "turn-race", Namespace: "agents", ThreadID: thread.ID, RunName: "race-run"}

	aCopy := getTestRun(t, a, "agents", "race-run")
	bCopy := getTestRun(t, b, "agents", "race-run")
	if !a.claimStandingTurn(ctx, raceTurn, aCopy) {
		t.Fatal("first claim lost, want the winner to drive")
	}
	if b.claimStandingTurn(ctx, raceTurn, bCopy) {
		t.Fatal("second claim won, want exactly one driver")
	}
	stored := getTestRun(t, a, "agents", "race-run")
	claim, err := standing.ParseClaim(stored.Annotations[agentsv1alpha1.AgentRunStandingClaimAnnotation])
	if err != nil || claim.Owner != "api-a/1" || claim.TurnID != "turn-race" {
		t.Fatalf("stored claim = %+v %v, want the winner's claim intact", claim, err)
	}
}

func TestStandingClaimStaleTakeoverRedrives(t *testing.T) {
	ctx := context.Background()
	server := chatTestServer(t, true)
	standingHarness(t, server, "standing-harness", agentsv1alpha1.AgentRunHarnessBackendOpenCode)
	thread := standingThread(t, server, "standing-harness")

	// Queue with the gate off so the run holds unclaimed, then simulate the
	// controller's StandingClaimed yield left behind by a crashed owner: a
	// stale claim plus a NeedsHuman hold.
	accepted, err := server.queueChatTurn(ctx, "agents", thread.ID, AppendChatMessageRequest{Content: "takeover check"})
	if err != nil {
		t.Fatal(err)
	}
	stale, err := standing.EncodeClaim(standing.Claim{TurnID: accepted.Turn.ID, Owner: "dead-replica/9", AtUnix: time.Now().Add(-10 * time.Minute).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	run := getTestRun(t, server, "agents", accepted.Turn.RunName)
	run.Annotations = map[string]string{agentsv1alpha1.AgentRunStandingClaimAnnotation: stale}
	if err := server.writes.Update(ctx, run); err != nil {
		t.Fatal(err)
	}
	run.Status.Phase = agentsv1alpha1.AgentRunPhaseNeedsHuman
	if err := server.writes.Status().Update(ctx, run); err != nil {
		t.Fatal(err)
	}
	fake := standing.NewFakeBackend()
	enableStandingLive(t, server, fake)

	turns, err := server.reconcileChatThread(ctx, "agents", thread.ID)
	if err != nil || len(turns) != 1 || turns[0].Status != "succeeded" {
		t.Fatalf("takeover turn = %#v %v, want one succeeded turn", turns, err)
	}
	if fake.Turns() != 1 {
		t.Fatalf("streamed turns = %d, want exactly one takeover stream", fake.Turns())
	}
	stored := getTestRun(t, server, "agents", accepted.Turn.RunName)
	if stored.Status.Phase != agentsv1alpha1.AgentRunPhaseSucceeded {
		t.Fatalf("run phase = %q, want Succeeded", stored.Status.Phase)
	}
	claim, err := standing.ParseClaim(stored.Annotations[agentsv1alpha1.AgentRunStandingClaimAnnotation])
	if err != nil || claim.TurnID != accepted.Turn.ID || claim.Owner != server.standingOwnerID() {
		t.Fatalf("stored claim = %+v %v, want this replica's takeover claim", claim, err)
	}
	messages, err := server.chatStore.ListMessages(ctx, "agents", thread.ID)
	if err != nil || len(messages) != 2 || !strings.HasPrefix(messages[1].Content, "standing reply to turn "+accepted.Turn.ID+":") {
		t.Fatalf("messages = %#v %v, want the takeover reply persisted", messages, err)
	}
}

func TestStandingYieldedHoldKeepsTurnActive(t *testing.T) {
	ctx := context.Background()
	server := chatTestServer(t, true)
	standingHarness(t, server, "standing-harness", agentsv1alpha1.AgentRunHarnessBackendOpenCode)
	thread := standingThread(t, server, "standing-harness")

	// Gate-off queue holds the run; the controller then yields to a live
	// foreign claim (another replica mid-stream). Recovery must not fail the
	// turn with hold guidance — the holder completes it.
	accepted, err := server.queueChatTurn(ctx, "agents", thread.ID, AppendChatMessageRequest{Content: "yield check"})
	if err != nil {
		t.Fatal(err)
	}
	live, err := standing.EncodeClaim(standing.Claim{TurnID: accepted.Turn.ID, Owner: "api-b/2", AtUnix: time.Now().Unix()})
	if err != nil {
		t.Fatal(err)
	}
	run := getTestRun(t, server, "agents", accepted.Turn.RunName)
	run.Annotations = map[string]string{agentsv1alpha1.AgentRunStandingClaimAnnotation: live}
	if err := server.writes.Update(ctx, run); err != nil {
		t.Fatal(err)
	}
	run.Status.Phase = agentsv1alpha1.AgentRunPhaseNeedsHuman
	if err := server.writes.Status().Update(ctx, run); err != nil {
		t.Fatal(err)
	}
	fake := standing.NewFakeBackend()
	enableStandingLive(t, server, fake)

	turns, err := server.reconcileChatThread(ctx, "agents", thread.ID)
	if err != nil || len(turns) != 1 || !chat.Active(turns[0]) {
		t.Fatalf("yielded turn = %#v %v, want still active", turns, err)
	}
	messages, err := server.chatStore.ListMessages(ctx, "agents", thread.ID)
	if err != nil || len(messages) != 1 {
		t.Fatalf("messages = %#v %v, want only the user message (no failure, no double reply)", messages, err)
	}
	if fake.Turns() != 0 {
		t.Fatalf("streamed turns = %d, want none while a peer holds the claim", fake.Turns())
	}
}

func TestStandingHoldWithoutClaimStillFails(t *testing.T) {
	ctx := context.Background()
	server := chatTestServer(t, true)
	standingHarness(t, server, "standing-harness", agentsv1alpha1.AgentRunHarnessBackendOpenCode)
	thread := standingThread(t, server, "standing-harness")

	// The pre-slice-4 shape (controller InProcessNotWired hold, no claim)
	// still fails the turn with guidance once the gate is on: the carve-out
	// only covers live-claimed turns.
	accepted, err := server.queueChatTurn(ctx, "agents", thread.ID, AppendChatMessageRequest{Content: "hold check"})
	if err != nil {
		t.Fatal(err)
	}
	run := getTestRun(t, server, "agents", accepted.Turn.RunName)
	run.Status.Phase = agentsv1alpha1.AgentRunPhaseNeedsHuman
	run.Status.Error = "execution.runtime InProcess is an API-first slice-1 surface"
	if err := server.writes.Status().Update(ctx, run); err != nil {
		t.Fatal(err)
	}
	enableStandingLive(t, server, standing.NewFakeBackend())

	turns, err := server.reconcileChatThread(ctx, "agents", thread.ID)
	if err != nil || len(turns) != 1 || turns[0].Status != "failed" {
		t.Fatalf("unclaimed hold turn = %#v %v, want failed", turns, err)
	}
}
