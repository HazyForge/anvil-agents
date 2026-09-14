package runapi

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	agentsv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
	"github.com/hazyforge/anvil-agents/internal/chat"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func newExecutionThread(t *testing.T, s *Server, metadata string) chat.Thread {
	t.Helper()
	thread, err := s.chatStore.CreateThread(context.Background(), chat.Thread{Namespace: "agents", ProfileName: "grok45", Mode: "persona", CreatedBy: "user", Metadata: json.RawMessage(metadata)})
	if err != nil {
		t.Fatal(err)
	}
	return thread
}
func TestChatTurnRecoveryIdempotencyAndTranscript(t *testing.T) {
	ctx := context.Background()
	s := chatTestServer(t, true)
	thread := newExecutionThread(t, s, `{}`)
	req := AppendChatMessageRequest{Content: "Remember the number 41", RequestID: uuid.NewString()}
	first, err := s.queueChatTurn(ctx, "agents", thread.ID, req)
	if err != nil {
		t.Fatal(err)
	}
	same, err := s.queueChatTurn(ctx, "agents", thread.ID, req)
	if err != nil || same.Turn.ID != first.Turn.ID {
		t.Fatalf("retry=%#v %v", same, err)
	}
	_, err = s.queueChatTurn(ctx, "agents", thread.ID, AppendChatMessageRequest{Content: "another"})
	if !errors.Is(err, chat.ErrTurnActive) {
		t.Fatalf("expected busy: %v", err)
	}
	req.Content = "changed"
	_, err = s.queueChatTurn(ctx, "agents", thread.ID, req)
	if !errors.Is(err, chat.ErrRequestConflict) {
		t.Fatalf("expected conflict: %v", err)
	}
	runs := &agentsv1alpha1.AgentRunList{}
	if err = s.writes.List(ctx, runs); err != nil || len(runs.Items) != 1 {
		t.Fatalf("runs=%v err=%v", len(runs.Items), err)
	}
	run := &runs.Items[0]
	run.Status.Phase = agentsv1alpha1.AgentRunPhaseSucceeded
	run.Status.Output = "I will remember 41."
	if err = s.writes.Status().Update(ctx, run); err != nil {
		t.Fatal(err)
	}
	// A new Server has no process-local state, only the persisted store and runs.
	restarted := *s
	for i := 0; i < 2; i++ {
		if _, err = restarted.reconcileChatThread(ctx, "agents", thread.ID); err != nil {
			t.Fatal(err)
		}
	}
	msgs, err := s.chatStore.ListMessages(ctx, "agents", thread.ID)
	if err != nil || len(msgs) != 2 || msgs[1].Content != run.Status.Output {
		t.Fatalf("messages=%#v err=%v", msgs, err)
	}
	second, err := s.queueChatTurn(ctx, "agents", thread.ID, AppendChatMessageRequest{Content: "What number?"})
	if err != nil {
		t.Fatal(err)
	}
	next := &agentsv1alpha1.AgentRun{}
	if err = s.writes.Get(ctx, types.NamespacedName{Namespace: "agents", Name: second.Turn.RunName}, next); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(next.Spec.Prompt, "I will remember 41.") || !strings.Contains(next.Spec.Prompt, "What number?") {
		t.Fatalf("missing transcript: %s", next.Spec.Prompt)
	}
}
func TestChatHarnessSelectionAndOutboxRecovery(t *testing.T) {
	ctx := context.Background()
	s := chatTestServer(t, true)
	h := &agentsv1alpha1.AgentHarnessProfile{ObjectMeta: metav1.ObjectMeta{Name: "chosen", Namespace: "agents"}}
	if err := s.writes.Create(ctx, h); err != nil {
		t.Fatal(err)
	}
	thread := newExecutionThread(t, s, `{"harnessProfileName":"chosen"}`)
	result, err := s.queueChatTurn(ctx, "agents", thread.ID, AppendChatMessageRequest{Content: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	run := &agentsv1alpha1.AgentRun{}
	if err = s.writes.Get(ctx, types.NamespacedName{Namespace: "agents", Name: result.Turn.RunName}, run); err != nil {
		t.Fatal(err)
	}
	if run.Spec.HarnessProfileRef == nil || run.Spec.HarnessProfileRef.Name != "chosen" || run.Spec.ProfileRef.Name != "grok45" {
		t.Fatalf("wrong target %#v", run.Spec)
	}
	if run.Spec.Harness.Backend.Kind != "" {
		t.Fatal("must retain selected profile's backend")
	}
	// Simulate accepted outbox before first Kubernetes create on another thread.
	other := newExecutionThread(t, s, `{}`)
	id := uuid.NewString()
	run.Name = "chat-recovery"
	run.ResourceVersion = ""
	run.UID = ""
	run.Labels[chatTurnLabel] = id
	run.Labels[chatThreadLabel] = other.ID
	run.Spec.SourceRef.Name = other.ID
	raw, _ := json.Marshal(run)
	queued, _, _, err := s.chatStore.QueueTurn(ctx, chat.Turn{ID: id, ThreadID: other.ID, Namespace: "agents", RequestID: uuid.NewString(), RunName: run.Name, RunJSON: raw}, chat.Message{Content: "recover"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.reconcileChatTurn(ctx, &queued); err != nil {
		t.Fatal(err)
	}
	if err = s.writes.Get(ctx, types.NamespacedName{Namespace: "agents", Name: "chat-recovery"}, &agentsv1alpha1.AgentRun{}); err != nil {
		t.Fatal(err)
	}
}
func TestChatConcurrentSendsCreateOneRun(t *testing.T) {
	s := chatTestServer(t, true)
	thread := newExecutionThread(t, s, `{}`)
	var wg sync.WaitGroup
	var mu sync.Mutex
	accepted := 0
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.queueChatTurn(context.Background(), "agents", thread.ID, AppendChatMessageRequest{Content: "concurrent"})
			if err == nil {
				mu.Lock()
				accepted++
				mu.Unlock()
			} else if !errors.Is(err, chat.ErrTurnActive) {
				t.Errorf("queue: %v", err)
			}
		}()
	}
	wg.Wait()
	if accepted != 1 {
		t.Fatalf("accepted %d", accepted)
	}
}

func setReply(t *testing.T, s *Server, turn chat.Turn, reply string) {
	t.Helper()
	run := &agentsv1alpha1.AgentRun{}
	if err := s.writes.Get(context.Background(), types.NamespacedName{Namespace: turn.Namespace, Name: turn.RunName}, run); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"type": "item.completed", "item": map[string]string{"type": "agent_message", "text": reply}})
	run.Status.Phase = agentsv1alpha1.AgentRunPhaseSucceeded
	run.Status.Backend = "codex"
	run.Status.Output = string(raw)
	if err := s.writes.Status().Update(context.Background(), run); err != nil {
		t.Fatal(err)
	}
}
func TestChatCoordinationDurableBusyDelivery(t *testing.T) {
	ctx := context.Background()
	s := chatTestServer(t, true)
	peer := &agentsv1alpha1.AgentRunProfile{ObjectMeta: metav1.ObjectMeta{Name: "peer", Namespace: "agents"}}
	if err := s.writes.Create(ctx, peer); err != nil {
		t.Fatal(err)
	}
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
	observed := &agentsv1alpha1.AgentRun{}
	if err = s.writes.Get(ctx, types.NamespacedName{Namespace: "agents", Name: parent.Turn.RunName}, observed); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(observed.Spec.Prompt, busy.Turn.RunName) || !strings.Contains(observed.Spec.Prompt, "Existing task") {
		t.Fatal("coordinator did not receive actual active work evidence")
	}
	setReply(t, s, parent.Turn, `{"reply":"I have queued a review request for the peer.","messages":[{"profileName":"peer","content":"Please review the implementation; do not duplicate the existing task."}]}`)
	// Simulate process failure after durable child enqueue but before the parent
	// completion transaction. Recovery must reuse the same delivery identifiers.
	for i := 0; i < 2; i++ {
		if _, err = s.dispatchChatCoordination(ctx, &parent.Turn, `{"reply":"I have queued a review request for the peer.","messages":[{"profileName":"peer","content":"Please review the implementation; do not duplicate the existing task."}]}`); err != nil {
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
		t.Fatalf("children=%#v", children)
	}
	childThread, _ := s.chatStore.GetThread(ctx, "agents", receipt.ThreadID)
	if coordinationConfig(childThread).Enabled {
		t.Fatal("child inherited coordination authority")
	}
	setReply(t, s, busy.Turn, "Existing task complete.")
	if _, err = s.reconcileChatThread(ctx, "agents", busyThread.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.reconcileChatThread(ctx, "agents", receipt.ThreadID); err != nil {
		t.Fatal(err)
	}
	setReply(t, s, children[0], "The peer's actual review.")
	if _, err = s.reconcileChatThread(ctx, "agents", receipt.ThreadID); err != nil {
		t.Fatal(err)
	}
	msgs, _ := s.chatStore.ListMessages(ctx, "agents", receipt.ThreadID)
	if len(msgs) != 2 || msgs[1].Content != "The peer's actual review." || !strings.Contains(string(msgs[0].Metadata), `"authorKind":"agent"`) {
		t.Fatalf("peer messages=%#v", msgs)
	}
}
func TestChatCoordinationRejectsUnauthorizedRecipient(t *testing.T) {
	ctx := context.Background()
	s := chatTestServer(t, true)
	if err := s.writes.Create(ctx, &agentsv1alpha1.AgentRunProfile{ObjectMeta: metav1.ObjectMeta{Name: "peer", Namespace: "agents"}}); err != nil {
		t.Fatal(err)
	}
	thread := newExecutionThread(t, s, `{"coordination":{"enabled":true,"allowedProfiles":["peer"]}}`)
	parent, err := s.queueChatTurn(ctx, "agents", thread.ID, AppendChatMessageRequest{Content: "coordinate"})
	if err != nil {
		t.Fatal(err)
	}
	setReply(t, s, parent.Turn, `{"reply":"Sent","messages":[{"profileName":"unauthorized","content":"do work"}]}`)
	turns, err := s.reconcileChatThread(ctx, "agents", thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if turns[0].Status != "failed" || len(turns[0].Delegates) != 0 {
		t.Fatalf("turns=%#v", turns)
	}
	runs := &agentsv1alpha1.AgentRunList{}
	_ = s.writes.List(ctx, runs)
	if len(runs.Items) != 1 {
		t.Fatal("unauthorized peer execution")
	}
}
func TestChatHarnessOnly(t *testing.T) {
	ctx := context.Background()
	s := chatTestServer(t, true)
	if err := s.writes.Create(ctx, &agentsv1alpha1.AgentHarnessProfile{ObjectMeta: metav1.ObjectMeta{Name: "standalone", Namespace: "agents"}}); err != nil {
		t.Fatal(err)
	}
	thread, err := s.chatStore.CreateThread(ctx, chat.Thread{Namespace: "agents", Mode: "persona", CreatedBy: "user", Metadata: json.RawMessage(`{"harnessProfileName":"standalone"}`)})
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.queueChatTurn(ctx, "agents", thread.ID, AppendChatMessageRequest{Content: "Hello harness"})
	if err != nil {
		t.Fatal(err)
	}
	run := &agentsv1alpha1.AgentRun{}
	_ = s.writes.Get(ctx, types.NamespacedName{Namespace: "agents", Name: result.Turn.RunName}, run)
	if run.Spec.ProfileRef != nil || run.Spec.HarnessProfileRef == nil || run.Spec.HarnessProfileRef.Name != "standalone" {
		t.Fatalf("run spec=%#v", run.Spec)
	}
	setReply(t, s, result.Turn, "Hello user")
	if _, err = s.reconcileChatThread(ctx, "agents", thread.ID); err != nil {
		t.Fatal(err)
	}
}

func TestChatCoordinationScopeAndSender(t *testing.T) {
	s := chatTestServer(t, true)
	ctx := context.Background()
	peer := &agentsv1alpha1.AgentRunProfile{ObjectMeta: metav1.ObjectMeta{Name: "other-app", Namespace: "agents"}, Spec: agentsv1alpha1.AgentRunProfileSpec{Scope: agentsv1alpha1.AgentRunScopeSpec{ApplicationRef: &agentsv1alpha1.ApplicationReferenceSpec{Name: "other"}}}}
	if err := s.writes.Create(ctx, peer); err != nil {
		t.Fatal(err)
	}
	thread := chat.Thread{Namespace: "agents", ProfileName: "grok45", Metadata: json.RawMessage(`{"coordination":{"enabled":true,"allowedProfiles":["other-app"]}}`)}
	if err := s.validateCoordination(ctx, thread); err == nil {
		t.Fatal("cross application target accepted")
	}
	thread.Metadata = json.RawMessage(`{"sourceTurnId":"turn","sourceProfileName":"peer"}`)
	if strings.Contains(string(chatAuthorMetadata(thread, false)), "peer") {
		t.Fatal("human followup impersonated originating peer")
	}
	if !strings.Contains(string(chatAuthorMetadata(thread, true)), "peer") {
		t.Fatal("peer provenance missing")
	}
}
func TestChatDoesNotReexecuteMissingRecordedRun(t *testing.T) {
	s := chatTestServer(t, true)
	thread := newExecutionThread(t, s, `{}`)
	ctx := context.Background()
	result, err := s.queueChatTurn(ctx, "agents", thread.ID, AppendChatMessageRequest{Content: "once"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.chatStore.RecordRun(ctx, result.Turn, "recorded-uid"); err != nil {
		t.Fatal(err)
	}
	if err = s.writes.Delete(ctx, &agentsv1alpha1.AgentRun{ObjectMeta: metav1.ObjectMeta{Name: result.Turn.RunName, Namespace: "agents"}}); err != nil {
		t.Fatal(err)
	}
	turns, err := s.reconcileChatThread(ctx, "agents", thread.ID)
	if err != nil || turns[0].Status != "failed" {
		t.Fatalf("turn=%#v err=%v", turns, err)
	}
	runs := &agentsv1alpha1.AgentRunList{}
	_ = s.writes.List(ctx, runs)
	if len(runs.Items) != 0 {
		t.Fatal("recorded run was executed again")
	}
}

func TestChatSharedHarnessSerializesDifferentProfilesAndDirectChat(t *testing.T) {
	ctx := context.Background()
	s := chatTestServer(t, true)
	h := &agentsv1alpha1.AgentHarnessProfile{ObjectMeta: metav1.ObjectMeta{Name: "shared", Namespace: "agents"}}
	if err := s.writes.Create(ctx, h); err != nil {
		t.Fatal(err)
	}
	p := &agentsv1alpha1.AgentRunProfile{}
	if err := s.writes.Get(ctx, types.NamespacedName{Namespace: "agents", Name: "grok45"}, p); err != nil {
		t.Fatal(err)
	}
	p.Spec.HarnessProfileRef = &agentsv1alpha1.NamespacedObjectReference{Name: "shared"}
	if err := s.writes.Update(ctx, p); err != nil {
		t.Fatal(err)
	}
	peer := &agentsv1alpha1.AgentRunProfile{ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: "agents"}, Spec: agentsv1alpha1.AgentRunProfileSpec{HarnessProfileRef: &agentsv1alpha1.NamespacedObjectReference{Name: "shared"}}}
	if err := s.writes.Create(ctx, peer); err != nil {
		t.Fatal(err)
	}
	first := newExecutionThread(t, s, `{}`)
	if _, err := s.queueChatTurn(ctx, "agents", first.ID, AppendChatMessageRequest{Content: "occupy home"}); err != nil {
		t.Fatal(err)
	}
	for _, profile := range []string{"other", ""} {
		thread, err := s.chatStore.CreateThread(ctx, chat.Thread{Namespace: "agents", ProfileName: profile, Mode: "persona", CreatedBy: "user", Metadata: json.RawMessage(`{"harnessProfileName":"shared"}`)})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.queueChatTurn(ctx, "agents", thread.ID, AppendChatMessageRequest{Content: "also use home"}); !errors.Is(err, chat.ErrTurnActive) {
			t.Fatalf("profile %q expected shared-home conflict: %v", profile, err)
		}
	}
}

type ambiguousCreateClient struct{ client.Client }

func (c ambiguousCreateClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	obj.SetUID("committed-run-uid")
	if err := c.Client.Create(ctx, obj, opts...); err != nil {
		return err
	}
	return errors.New("response lost after commit")
}
func TestChatRecoversAmbiguousCreateInSameRequest(t *testing.T) {
	ctx := context.Background()
	s := chatTestServer(t, true)
	thread := newExecutionThread(t, s, `{}`)
	s.writes = ambiguousCreateClient{s.writes}
	result, err := s.queueChatTurn(ctx, "agents", thread.ID, AppendChatMessageRequest{Content: "execute once"})
	if err != nil {
		t.Fatal(err)
	}
	stored, _ := s.chatStore.ListTurns(ctx, "agents", thread.ID)
	if result.Turn.RunUID != "committed-run-uid" || stored[0].RunUID != "committed-run-uid" {
		t.Fatalf("committed UID was not bound: %#v", stored)
	}
}
func TestChatNeedsHumanAndDisabledLaunchFinishExplicitly(t *testing.T) {
	ctx := context.Background()
	t.Run("needs human", func(t *testing.T) {
		s := chatTestServer(t, true)
		thread := newExecutionThread(t, s, `{}`)
		result, err := s.queueChatTurn(ctx, "agents", thread.ID, AppendChatMessageRequest{Content: "start"})
		if err != nil {
			t.Fatal(err)
		}
		run := &agentsv1alpha1.AgentRun{}
		_ = s.writes.Get(ctx, types.NamespacedName{Namespace: "agents", Name: result.Turn.RunName}, run)
		run.Status.Phase = agentsv1alpha1.AgentRunPhaseNeedsHuman
		run.Status.Error = "Authentication requires operator sign-in"
		if err = s.writes.Status().Update(ctx, run); err != nil {
			t.Fatal(err)
		}
		turns, err := s.reconcileChatThread(ctx, "agents", thread.ID)
		if err != nil || turns[0].Status != "failed" || turns[0].Error != run.Status.Error {
			t.Fatalf("needs-human=%#v %v", turns, err)
		}
	})
	t.Run("launch disabled", func(t *testing.T) {
		s := chatTestServer(t, true)
		thread := newExecutionThread(t, s, `{}`)
		result, err := s.queueChatTurn(ctx, "agents", thread.ID, AppendChatMessageRequest{Content: "queued"})
		if err != nil {
			t.Fatal(err)
		}
		// Fake API objects have no generated UID; remove it to model the accepted
		// outbox before Kubernetes creation, then disable future creation.
		if err = s.writes.Delete(ctx, &agentsv1alpha1.AgentRun{ObjectMeta: metav1.ObjectMeta{Name: result.Turn.RunName, Namespace: "agents"}}); err != nil {
			t.Fatal(err)
		}
		s.config.Runs.CreateEnabled = false
		turns, err := s.reconcileChatThread(ctx, "agents", thread.ID)
		if err != nil || turns[0].Status != "failed" || !strings.Contains(turns[0].Error, "disabled") {
			t.Fatalf("disabled=%#v %v", turns, err)
		}
	})
}

func TestChatWaitsForTransientCompletedRunLogs(t *testing.T) {
	ctx := context.Background()
	s := chatTestServer(t, true)
	thread := newExecutionThread(t, s, `{}`)
	result, err := s.queueChatTurn(ctx, "agents", thread.ID, AppendChatMessageRequest{Content: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	run := &agentsv1alpha1.AgentRun{}
	_ = s.writes.Get(ctx, types.NamespacedName{Namespace: "agents", Name: result.Turn.RunName}, run)
	run.Status.Phase = agentsv1alpha1.AgentRunPhaseSucceeded
	run.Status.Backend = "codex"
	completed := metav1.NewTime(time.Now())
	run.Status.CompletedAt = &completed
	run.Status.RunnerPodRef = &agentsv1alpha1.NamespacedObjectReference{Name: "runner", Namespace: "agents"}
	run.Status.Output = "truncated native output"
	if err = s.writes.Status().Update(ctx, run); err != nil {
		t.Fatal(err)
	}
	s.logs = staticLogSource{err: ErrLogsPending}
	turns, err := s.reconcileChatThread(ctx, "agents", thread.ID)
	if err != nil || !chat.Active(turns[0]) {
		t.Fatalf("transient logs should remain active: %#v %v", turns, err)
	}
	s.logs = staticLogSource{contents: `{"type":"item.completed","item":{"type":"agent_message","text":"Recovered real reply"}}`}
	turns, err = s.reconcileChatThread(ctx, "agents", thread.ID)
	if err != nil || turns[0].Status != "succeeded" {
		t.Fatalf("recovery=%#v %v", turns, err)
	}
	messages, _ := s.chatStore.ListMessages(ctx, "agents", thread.ID)
	if len(messages) != 2 || messages[1].Content != "Recovered real reply" {
		t.Fatalf("messages=%#v", messages)
	}
}
