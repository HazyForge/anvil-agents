package runapi

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	agentsv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
	"github.com/hazyforge/anvil-agents/internal/chat"
	"github.com/hazyforge/anvil-agents/internal/standing"
)

// Slice-3b standing process backend turn-path tests. The stub Runner returns
// native-enveloped output with no model call, no subprocess, and no
// credentials; the FakeBackend-only unit path stays covered by the slice-2
// tests in chat_standing_turn_test.go.

// stubProcessRunner replays one native harness reply through the Runner
// contract, emitting it in two chunks so token streaming is exercised.
func stubProcessRunner(t *testing.T, reply string) standing.Runner {
	t.Helper()
	return standing.RunnerFunc(func(ctx context.Context, harnessKind, prompt string, emit func(chunk string) error) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if strings.TrimSpace(prompt) == "" {
			t.Error("process runner received an empty frozen prompt")
			return "", context.Canceled
		}
		mid := len(reply) / 2
		for _, chunk := range []string{reply[:mid], reply[mid:]} {
			if err := emit(chunk); err != nil {
				return "", err
			}
		}
		return reply, nil
	})
}

func TestStandingProcessTurnStreamsNativeReply(t *testing.T) {
	ctx := context.Background()
	server := chatTestServer(t, true)
	standingHarness(t, server, "standing-harness", agentsv1alpha1.AgentRunHarnessBackendCodex)
	native := `{"type":"item.completed","item":{"type":"agent_message","text":"process native reply"}}`
	backend := standing.NewProcessBackend(stubProcessRunner(t, native))
	enableStandingLive(t, server, backend)
	thread := standingThread(t, server, "standing-harness")

	accepted, err := server.queueChatTurn(ctx, "agents", thread.ID, AppendChatMessageRequest{Content: "hello process"})
	if err != nil {
		t.Fatal(err)
	}
	if accepted.Turn.Status != "succeeded" {
		t.Fatalf("process turn status = %q, want succeeded", accepted.Turn.Status)
	}
	messages, err := server.chatStore.ListMessages(ctx, "agents", thread.ID)
	if err != nil || len(messages) != 2 || messages[1].Content != "process native reply" {
		t.Fatalf("messages = %#v %v, want the native reply extracted without the Fake wrapper", messages, err)
	}
	run := &agentsv1alpha1.AgentRun{}
	if err := server.writes.Get(ctx, types.NamespacedName{Namespace: "agents", Name: accepted.Turn.RunName}, run); err != nil {
		t.Fatal(err)
	}
	if run.Status.Phase != agentsv1alpha1.AgentRunPhaseSucceeded || run.Status.Backend != "codex" {
		t.Fatalf("run status = %+v, want Succeeded/codex", run.Status)
	}
	if run.Status.Output != native {
		t.Fatalf("run output = %q, want the native harness output verbatim (no double wrap)", run.Status.Output)
	}
	if got := strings.Count(run.Status.Output, "item.completed"); got != 1 {
		t.Fatalf("envelope count = %d, want exactly one native envelope", got)
	}
	if backend.Created() != 1 || backend.Turns() != 1 {
		t.Fatalf("sessions = %d turns = %d, want one warm session and one streamed turn", backend.Created(), backend.Turns())
	}

	// A second turn resumes the same warm session.
	second, err := server.queueChatTurn(ctx, "agents", thread.ID, AppendChatMessageRequest{Content: "second process turn"})
	if err != nil {
		t.Fatal(err)
	}
	if second.Turn.Status != "succeeded" {
		t.Fatalf("second turn status = %q, want succeeded", second.Turn.Status)
	}
	if backend.Created() != 1 || backend.Turns() != 2 {
		t.Fatalf("sessions = %d turns = %d, want warm reuse across turns", backend.Created(), backend.Turns())
	}
}

func TestStandingProcessTurnPeerChildStreamsOwnSession(t *testing.T) {
	ctx := context.Background()
	server := chatTestServer(t, true)
	standingHarness(t, server, "standing-peer-harness", agentsv1alpha1.AgentRunHarnessBackendCodex)
	native := `{"type":"item.completed","item":{"type":"agent_message","text":"peer process reply"}}`
	enableStandingLive(t, server, standing.NewProcessBackend(stubProcessRunner(t, native)))
	peer := &agentsv1alpha1.AgentRunProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "peer", Namespace: "agents"},
		Spec: agentsv1alpha1.AgentRunProfileSpec{
			HarnessProfileRef: &agentsv1alpha1.NamespacedObjectReference{Name: "standing-peer-harness"},
		},
	}
	if err := server.writes.Create(ctx, peer); err != nil {
		t.Fatal(err)
	}
	manager, err := server.chatStore.CreateThread(ctx, chat.Thread{
		Namespace: "agents", ProfileName: "grok45", Mode: "persona", CreatedBy: "user",
		Metadata: []byte(`{"coordination":{"enabled":true,"allowedProfiles":["peer"]}}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := server.queueChatTurn(ctx, "agents", manager.ID, AppendChatMessageRequest{Content: "Ask peer for a review"})
	if err != nil {
		t.Fatal(err)
	}
	setReply(t, server, parent.Turn, `{"reply":"I have queued a review request for the peer.","messages":[{"profileName":"peer","content":"Please review the implementation."}]}`)
	turns, err := server.reconcileChatThread(ctx, "agents", manager.ID)
	if err != nil || turns[0].Status != "succeeded" || len(turns[0].Delegates) != 1 {
		t.Fatalf("parent = %#v %v, want succeeded with one delegate", turns, err)
	}
	children, err := server.chatStore.ListTurns(ctx, "agents", turns[0].Delegates[0].ThreadID)
	if err != nil || len(children) != 1 || children[0].Status != "succeeded" {
		t.Fatalf("peer child = %#v %v, want one succeeded turn via the process backend", children, err)
	}
	childMessages, err := server.chatStore.ListMessages(ctx, "agents", turns[0].Delegates[0].ThreadID)
	if err != nil || len(childMessages) != 2 || childMessages[1].Content != "peer process reply" {
		t.Fatalf("child messages = %#v %v, want the native peer reply", childMessages, err)
	}
}

func TestStandingProcessTurnFailureKeepsHoldBehavior(t *testing.T) {
	ctx := context.Background()
	server := chatTestServer(t, true)
	standingHarness(t, server, "standing-harness", agentsv1alpha1.AgentRunHarnessBackendCodex)
	failing := standing.RunnerFunc(func(ctx context.Context, _, _ string, _ func(string) error) (string, error) {
		return "", context.DeadlineExceeded
	})
	enableStandingLive(t, server, standing.NewProcessBackend(failing))
	thread := standingThread(t, server, "standing-harness")

	accepted, err := server.queueChatTurn(ctx, "agents", thread.ID, AppendChatMessageRequest{Content: "hello process"})
	if err != nil {
		t.Fatal(err)
	}
	turns, _ := server.chatStore.ListTurns(ctx, "agents", thread.ID)
	if len(turns) != 1 || !chat.Active(turns[0]) {
		t.Fatalf("failing process turn = %#v, want active hold", turns)
	}
	run := &agentsv1alpha1.AgentRun{}
	if err := server.writes.Get(ctx, types.NamespacedName{Namespace: "agents", Name: accepted.Turn.RunName}, run); err != nil {
		t.Fatal(err)
	}
	if run.Status.Phase != "" {
		t.Fatalf("failing run phase = %q, want untouched", run.Status.Phase)
	}
}

func TestStandingProcessTurnFakeOnlyKindKeepsHoldBehavior(t *testing.T) {
	ctx := context.Background()
	server := chatTestServer(t, true)
	// custom has no local process recipe: the turn must stay on hold, with
	// no Job created, exactly like the gate-off path.
	standingHarness(t, server, "standing-harness", agentsv1alpha1.AgentRunHarnessBackendCustom)
	enableStandingLive(t, server, standing.NewProcessBackend(nil))
	thread := standingThread(t, server, "standing-harness")

	accepted, err := server.queueChatTurn(ctx, "agents", thread.ID, AppendChatMessageRequest{Content: "hello custom"})
	if err != nil {
		t.Fatal(err)
	}
	turns, _ := server.chatStore.ListTurns(ctx, "agents", thread.ID)
	if len(turns) != 1 || !chat.Active(turns[0]) {
		t.Fatalf("Fake-only kind turn = %#v, want active hold", turns)
	}
	run := &agentsv1alpha1.AgentRun{}
	if err := server.writes.Get(ctx, types.NamespacedName{Namespace: "agents", Name: accepted.Turn.RunName}, run); err != nil {
		t.Fatal(err)
	}
	if run.Status.Phase != "" {
		t.Fatalf("Fake-only run phase = %q, want untouched", run.Status.Phase)
	}
}

func TestStandingTurnOutputSkipsWrapperForNativeBackends(t *testing.T) {
	native := `{"type":"item.completed","item":{"type":"agent_message","text":"hi"}}`
	if got := standingTurnOutput(standing.NewFakeBackend(), agentsv1alpha1.AgentRunHarnessBackendCodex, "hi"); !strings.Contains(got, "agent_message") || got == "hi" {
		t.Fatalf("fake output = %q, want the envelope wrapper", got)
	}
	if got := standingTurnOutput(standing.NewProcessBackend(stubProcessRunner(t, native)), agentsv1alpha1.AgentRunHarnessBackendCodex, native); got != native {
		t.Fatalf("process output = %q, want verbatim passthrough", got)
	}
	// Backends that do not implement the native gate keep today's behavior.
	if got := standingTurnOutput(nil, agentsv1alpha1.AgentRunHarnessBackendCodex, "hi"); !strings.Contains(got, "agent_message") {
		t.Fatalf("nil-backend output = %q, want the envelope wrapper", got)
	}
}
