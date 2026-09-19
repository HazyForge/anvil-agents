package runapi

import (
	"context"
	"encoding/json"
	"testing"

	agentsv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
	"github.com/hazyforge/anvil-agents/internal/chat"
	"github.com/hazyforge/anvil-agents/internal/substrate"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// A standing-chat turn addresses its harness only through the thread's
// profile/harness selection. The Substrate spike adds no new chat endpoint:
// selecting a SubstrateActor harness profile must survive turn construction so
// composition resolves the actor runtime while the default Job path is
// untouched for every other harness.
func TestBuildChatRunPreservesSubstrateHarnessSelection(t *testing.T) {
	t.Parallel()

	metadata, err := json.Marshal(map[string]string{"harnessProfileName": "substrate-chat"})
	if err != nil {
		t.Fatalf("metadata: %v", err)
	}
	thread := chat.Thread{ID: "thread-1", Namespace: "agents", ProfileName: "desktop-assistant", Metadata: metadata}
	run, err := buildChatRun("agents", "chat-turn-1", "hello", thread)
	if err != nil {
		t.Fatalf("buildChatRun: %v", err)
	}
	if run.Spec.ProfileRef == nil || run.Spec.ProfileRef.Name != "desktop-assistant" {
		t.Fatalf("chat run profile ref = %+v, want desktop-assistant", run.Spec.ProfileRef)
	}
	if run.Spec.HarnessProfileRef == nil || run.Spec.HarnessProfileRef.Name != "substrate-chat" {
		t.Fatalf("chat run harness profile ref = %+v, want substrate-chat", run.Spec.HarnessProfileRef)
	}
	if run.Spec.HarnessProfileRef.Namespace != "" {
		t.Fatalf("chat run harness ref namespace = %q, want same-namespace default", run.Spec.HarnessProfileRef.Namespace)
	}

	harnessOnly, err := buildChatRun("agents", "chat-turn-2", "hello", chat.Thread{ID: "thread-2", Namespace: "agents", Metadata: metadata})
	if err != nil {
		t.Fatalf("harness-only buildChatRun: %v", err)
	}
	if harnessOnly.Spec.ProfileRef != nil {
		t.Fatalf("harness-only run must not gain a profile ref: %+v", harnessOnly.Spec.ProfileRef)
	}
	if harnessOnly.Spec.HarnessProfileRef == nil || harnessOnly.Spec.HarnessProfileRef.Name != "substrate-chat" {
		t.Fatalf("harness-only run harness ref = %+v, want substrate-chat", harnessOnly.Spec.HarnessProfileRef)
	}
}

// Peer child threads carry the recipient profile and defer the harness choice
// to that profile, so a peer is eligible for the actor plane exactly when its
// own configured harness selects it. The live backend then resumes the
// recipient's thread actor (see substrate.ActorNameForThread).
func TestBuildPeerChildRunDefersHarnessToRecipientProfile(t *testing.T) {
	t.Parallel()

	metadata, err := json.Marshal(map[string]string{
		"sourceTurnId":      "parent-turn",
		"sourceThreadId":    "parent-thread",
		"sourceProfileName": "desktop-manager",
	})
	if err != nil {
		t.Fatalf("metadata: %v", err)
	}
	child, err := buildChatRun("agents", "chat-turn-3", "review the proposal", chat.Thread{
		ID: "child-id", Namespace: "agents", ProfileName: "desktop-reviewer", Metadata: metadata,
	})
	if err != nil {
		t.Fatalf("peer child buildChatRun: %v", err)
	}
	if child.Spec.ProfileRef == nil || child.Spec.ProfileRef.Name != "desktop-reviewer" {
		t.Fatalf("peer child profile ref = %+v, want desktop-reviewer", child.Spec.ProfileRef)
	}
	if child.Spec.HarnessProfileRef != nil {
		t.Fatalf("peer child must not inherit the parent harness: %+v", child.Spec.HarnessProfileRef)
	}
}

// The resolved execution beside the selected harness profile is what opts a
// chat turn into the actor plane. A Job harness profile must keep resolving
// to the Job plane even after the Substrate fields exist.
func TestChatHarnessExecutionRuntimeSelection(t *testing.T) {
	t.Parallel()

	job := agentsv1alpha1.AgentRunHarnessExecutionSpec{}
	if job.UsesSubstrateActors() {
		t.Fatal("empty execution must stay on the Job plane")
	}
	substrateExec := agentsv1alpha1.AgentRunHarnessExecutionSpec{
		Runtime:   agentsv1alpha1.AgentRunExecutionRuntimeSubstrateActor,
		Substrate: &agentsv1alpha1.AgentRunSubstrateActorSpec{ActorClass: "standing-chat"},
	}
	if !substrateExec.UsesSubstrateActors() {
		t.Fatal("substrate harness profile execution must select the actor plane")
	}
	if reason, message := agentsv1alpha1.ValidateSubstrateExecution(&substrateExec); reason != "" {
		t.Fatalf("substrate chat execution invalid: %s %s", reason, message)
	}
}

// Peer deliveries resume the recipient thread actor through the same mapping
// as a direct turn: the child run carries the child thread as its ChatThread
// source, so substrate.ThreadIDForRun resolves the recipient thread and
// ActorNameForThread addresses exactly one stable actor. Retried deliveries
// keep their deterministic request ID (see dispatchChatCoordination), and the
// existing durable wait for busy recipients still owns queueing — the warm
// actor only skips the cold start and never drops a queued peer turn.
func TestPeerChildRunMapsToRecipientThreadActor(t *testing.T) {
	t.Parallel()

	metadata, err := json.Marshal(map[string]string{
		"sourceTurnId":      "parent-turn",
		"sourceThreadId":    "parent-thread",
		"sourceProfileName": "desktop-manager",
	})
	if err != nil {
		t.Fatalf("metadata: %v", err)
	}
	child, err := buildChatRun("agents", "chat-turn-3", "review the proposal", chat.Thread{
		ID: "child-id", Namespace: "agents", ProfileName: "desktop-reviewer", Metadata: metadata,
	})
	if err != nil {
		t.Fatalf("peer child buildChatRun: %v", err)
	}
	if child.Spec.SourceRef.Kind != "ChatThread" || child.Spec.SourceRef.Name != "child-id" {
		t.Fatalf("peer child source = %+v, want ChatThread/child-id", child.Spec.SourceRef)
	}
	threadID := substrate.ThreadIDForRun(child.Spec.SourceRef.Kind, child.Spec.SourceRef.Name, child.Name)
	if threadID != "child-id" {
		t.Fatalf("peer thread id = %q, want child-id", threadID)
	}
	actor := substrate.ActorNameForThread(threadID)
	if actor == "" || actor != substrate.ActorNameForThread("child-id") {
		t.Fatalf("peer actor = %q, want the stable recipient thread actor", actor)
	}
	if actor == substrate.ActorNameForThread("parent-thread") {
		t.Fatal("peer recipient actor must not collide with the parent thread actor")
	}
	direct, err := buildChatRun("agents", "chat-turn-4", "hello", chat.Thread{ID: "parent-thread", Namespace: "agents", ProfileName: "desktop-manager"})
	if err != nil {
		t.Fatalf("direct buildChatRun: %v", err)
	}
	directThread := substrate.ThreadIDForRun(direct.Spec.SourceRef.Kind, direct.Spec.SourceRef.Name, direct.Name)
	if directThread != "parent-thread" {
		t.Fatalf("direct thread id = %q, want parent-thread", directThread)
	}
}

// A busy SubstrateActor recipient must still durable-queue a peer delivery.
// The delivery waits on the existing durable wait while the recipient is
// busy, then runs after the busy turn completes and binds the same warm
// recipient actor through the FakeClient backend — a warm actor never
// silently drops a queued peer turn. No cluster is required.
func TestPeerSubstrateBusyRecipientDurableWait(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := chatTestServer(t, true)

	harness := &agentsv1alpha1.AgentHarnessProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "substrate-chat", Namespace: "agents"},
		Spec: agentsv1alpha1.AgentHarnessProfileSpec{
			Execution: agentsv1alpha1.AgentRunHarnessExecutionSpec{
				Runtime:   agentsv1alpha1.AgentRunExecutionRuntimeSubstrateActor,
				Substrate: &agentsv1alpha1.AgentRunSubstrateActorSpec{ActorClass: "standing-chat", Pool: "warm"},
			},
		},
	}
	if err := s.writes.Create(ctx, harness); err != nil {
		t.Fatal(err)
	}
	peer := &agentsv1alpha1.AgentRunProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "peer", Namespace: "agents"},
		Spec: agentsv1alpha1.AgentRunProfileSpec{
			HarnessProfileRef: &agentsv1alpha1.NamespacedObjectReference{Name: "substrate-chat"},
		},
	}
	if err := s.writes.Create(ctx, peer); err != nil {
		t.Fatal(err)
	}

	// Occupy the recipient profile with a direct turn so the peer delivery
	// below meets a busy recipient on the actor plane.
	busyThread, err := s.chatStore.CreateThread(ctx, chat.Thread{Namespace: "agents", ProfileName: "peer", Mode: "persona", CreatedBy: "user"})
	if err != nil {
		t.Fatal(err)
	}
	busy, err := s.queueChatTurn(ctx, "agents", busyThread.ID, AppendChatMessageRequest{Content: "Existing task"})
	if err != nil {
		t.Fatal(err)
	}

	manager := newExecutionThread(t, s, `{"coordination":{"enabled":true,"allowedProfiles":["peer"]}}`)
	parent, err := s.queueChatTurn(ctx, "agents", manager.ID, AppendChatMessageRequest{Content: "Ask peer for a review"})
	if err != nil {
		t.Fatal(err)
	}
	setReply(t, s, parent.Turn, `{"reply":"I have queued a review request for the peer.","messages":[{"profileName":"peer","content":"Please review the implementation on the warm actor."}]}`)
	const peerPayload = `{"reply":"I have queued a review request for the peer.","messages":[{"profileName":"peer","content":"Please review the implementation on the warm actor."}]}`
	// Retried dispatches converge on the same deterministic delivery.
	for i := 0; i < 2; i++ {
		if _, err = s.dispatchChatCoordination(ctx, &parent.Turn, peerPayload); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		if _, err = s.reconcileChatThread(ctx, "agents", manager.ID); err != nil {
			t.Fatal(err)
		}
	}
	turns, _ := s.chatStore.ListTurns(ctx, "agents", manager.ID)
	if turns[0].Status != "succeeded" || len(turns[0].Delegates) != 1 {
		t.Fatalf("parent=%#v", turns)
	}
	receipt := turns[0].Delegates[0]
	children, _ := s.chatStore.ListTurns(ctx, "agents", receipt.ThreadID)
	if len(children) != 1 || children[0].Status != "waiting" {
		t.Fatalf("busy-recipient peer delivery=%#v, want one durable waiting turn", children)
	}

	// The waiting delivery owns the recipient thread actor mapping already:
	// one stable actor, distinct from the parent thread actor.
	actors := substrate.NewFakeClient()
	childActor := substrate.ActorNameForThread(receipt.ThreadID)
	if childActor == "" || childActor == substrate.ActorNameForThread(manager.ID) {
		t.Fatalf("peer actor = %q, want the stable recipient thread actor", childActor)
	}
	childSpec := substrate.ActorSpecForRun("agents", childActor, "openCode", "standing-chat", "warm",
		map[string]string{"control.anvil.hazyforge.io/agent-run": "peer-turn"})
	cold, warm, err := substrate.EnsureTurnActor(ctx, actors, childSpec)
	if err != nil || warm {
		t.Fatalf("first peer ensure = warm=%v err=%v, want cold", warm, err)
	}
	if _, warm, err := substrate.EnsureTurnActor(ctx, actors, childSpec); err != nil || !warm {
		t.Fatalf("peer retry ensure = warm=%v err=%v, want warm resume", warm, err)
	}
	if got := actors.Created(); got != 1 {
		t.Fatalf("distinct peer actors = %d, want exactly one recipient actor", got)
	}

	// The waiting turn must not execute while the recipient is busy: no
	// AgentRun exists for the child thread yet (busy + parent only).
	runs := &agentsv1alpha1.AgentRunList{}
	if err = s.writes.List(ctx, runs); err != nil {
		t.Fatal(err)
	}
	for _, run := range runs.Items {
		if run.Spec.SourceRef.Kind == "ChatThread" && run.Spec.SourceRef.Name == receipt.ThreadID {
			t.Fatalf("waiting peer turn already has run %q while busy", run.Name)
		}
	}

	// After the busy turn completes, the queued delivery activates and runs.
	setReply(t, s, busy.Turn, "Existing task complete.")
	if _, err = s.reconcileChatThread(ctx, "agents", busyThread.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.reconcileChatThread(ctx, "agents", receipt.ThreadID); err != nil {
		t.Fatal(err)
	}
	children, _ = s.chatStore.ListTurns(ctx, "agents", receipt.ThreadID)
	if len(children) != 1 || children[0].Status == "waiting" {
		t.Fatalf("queued peer turn did not activate after busy completed: %#v", children)
	}
	childRun := &agentsv1alpha1.AgentRun{}
	if err = s.writes.Get(ctx, types.NamespacedName{Namespace: "agents", Name: children[0].RunName}, childRun); err != nil {
		t.Fatalf("activated peer turn has no AgentRun (silent drop): %v", err)
	}
	if got := substrate.ThreadIDForRun(childRun.Spec.SourceRef.Kind, childRun.Spec.SourceRef.Name, childRun.Name); got != receipt.ThreadID {
		t.Fatalf("peer run thread = %q, want %q", got, receipt.ThreadID)
	}
	setReply(t, s, children[0], "The peer's review from the warm actor.")
	if _, err = s.reconcileChatThread(ctx, "agents", receipt.ThreadID); err != nil {
		t.Fatal(err)
	}
	msgs, _ := s.chatStore.ListMessages(ctx, "agents", receipt.ThreadID)
	if len(msgs) != 2 || msgs[1].Content != "The peer's review from the warm actor." {
		t.Fatalf("peer messages=%#v, want the queued delivery to run to completion", msgs)
	}

	// The completed delivery still binds the same warm recipient actor: no
	// duplicate execution identity, no drop.
	resumed, warm, err := substrate.EnsureTurnActor(ctx, actors, childSpec)
	if err != nil || !warm {
		t.Fatalf("completed peer ensure = warm=%v err=%v, want warm resume", warm, err)
	}
	if resumed.ID != cold.ID {
		t.Fatalf("resumed actor = %q, want warm reuse of %q", resumed.ID, cold.ID)
	}
	if got := actors.Created(); got != 1 {
		t.Fatalf("distinct peer actors = %d, want exactly one recipient actor", got)
	}

	// Adjacent suspend-on-idle confirmation: the default suspends the idle
	// actor, and the next turn resumes it warm without a cold create.
	if suspended, err := substrate.SuspendIdleActor(ctx, actors, "agents", childActor, nil); err != nil || !suspended {
		t.Fatalf("default suspend = %v/%v, want suspended", suspended, err)
	}
	afterIdle, warm, err := substrate.EnsureTurnActor(ctx, actors, childSpec)
	if err != nil || !warm {
		t.Fatalf("post-idle ensure = warm=%v err=%v, want warm resume", warm, err)
	}
	if afterIdle.ID != cold.ID {
		t.Fatalf("post-idle actor = %q, want warm reuse of %q", afterIdle.ID, cold.ID)
	}
}
