package runapi

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/go-logr/logr"
	"github.com/google/uuid"
	agents "github.com/hazyforge/anvil-agents/api/v1alpha1"
	"github.com/hazyforge/anvil-agents/internal/chat"
	"github.com/hazyforge/anvil-agents/internal/standing"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSynthesisUsesParentHistoryBeforeQueuedFollowup(t *testing.T) {
	ctx := context.Background()
	s := chatTestServer(t, true)
	thread := newExecutionThread(t, s, `{"coordination":{"enabled":true,"returnReplies":true,"allowedProfiles":["inert-peer"]}}`)
	_, _, err := s.chatStore.AppendMessages(ctx, "agents", thread.ID, []chat.Message{
		{Role: chat.RoleUser, Content: "Earlier question"},
		{Role: chat.RoleAssistant, Content: "Earlier answer"},
	})
	if err != nil {
		t.Fatal(err)
	}
	parent, _, _, err := s.chatStore.QueueTurn(ctx, chat.Turn{Namespace: "agents", ThreadID: thread.ID, ProfileName: thread.ProfileName, RequestID: uuid.NewString(), RunName: "parent-run"}, chat.Message{Content: "Original task"})
	if err != nil {
		t.Fatal(err)
	}
	parent.Status = "succeeded"
	parent.Coordination = &chat.Coordination{Status: "waiting"}
	receipt, _ := json.Marshal(map[string]string{"turnId": parent.ID, "runName": parent.RunName})
	if err = s.chatStore.CompleteTurn(ctx, parent, chat.Message{Role: chat.RoleAssistant, Content: "Peer request queued", Metadata: receipt}); err != nil {
		t.Fatal(err)
	}
	followup, followupMessage, _, err := s.chatStore.QueueTurn(ctx, chat.Turn{Namespace: "agents", ThreadID: thread.ID, ProfileName: thread.ProfileName, RequestID: uuid.NewString(), RunName: "followup-run", QueueAfterActive: true}, chat.Message{Content: "A later, separate request"})
	if err != nil || followup.Status != "waiting" {
		t.Fatalf("manager follow-up was not queued: %#v, %v", followup, err)
	}

	const peerResults = "Peer results for the original task"
	resume, err := s.queueChatTurnWithOptions(ctx, "agents", thread.ID, AppendChatMessageRequest{Content: peerResults, RequestID: chat.SynthesisRequestID(parent.ID)}, chatTurnOptions{deferred: true, noDrive: true, resumption: true, parent: parent.ID, parentThread: thread.ID}, 0)
	if err != nil {
		t.Fatalf("synthesis should use the full snapshot sequence for admission: %v", err)
	}
	var run agents.AgentRun
	if err = json.Unmarshal(resume.Turn.RunJSON, &run); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(run.Spec.Prompt, "CURRENT_WORK_JSON") {
		t.Fatal("synthesis inherited current-work inventory")
	}
	_, raw, ok := strings.Cut(run.Spec.Prompt, "CONVERSATION_JSON:\n")
	if !ok {
		t.Fatal("synthesis prompt lacks conversation history")
	}
	var history []map[string]string
	if err = json.Unmarshal([]byte(raw), &history); err != nil {
		t.Fatalf("decode synthesis history: %v", err)
	}
	want := []map[string]string{
		{"role": chat.RoleUser, "content": "Earlier question"},
		{"role": chat.RoleAssistant, "content": "Earlier answer"},
		{"role": chat.RoleUser, "content": "Original task"},
		{"role": chat.RoleAssistant, "content": "Peer request queued"},
		{"role": chat.RoleUser, "content": peerResults},
	}
	if !reflect.DeepEqual(history, want) {
		t.Fatalf("synthesis history = %#v, want %#v", history, want)
	}
	messages, err := s.chatStore.ListMessages(ctx, "agents", thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 6 || messages[4].ID != followupMessage.ID || messages[4].Content != "A later, separate request" {
		t.Fatalf("queued follow-up was not preserved: %#v", messages)
	}
}

func TestSynthesisRejectsMissingOrForeignParentHistory(t *testing.T) {
	ctx := context.Background()
	s := chatTestServer(t, true)
	thread := newExecutionThread(t, s, `{}`)
	other := newExecutionThread(t, s, `{}`)
	parent, _, _, err := s.chatStore.QueueTurn(ctx, chat.Turn{Namespace: "agents", ThreadID: thread.ID, ProfileName: thread.ProfileName, RequestID: uuid.NewString(), RunName: "parent-run"}, chat.Message{Content: "Original task"})
	if err != nil {
		t.Fatal(err)
	}
	parent.Status = "succeeded"
	parent.Coordination = &chat.Coordination{Status: "waiting"}
	wrongReceipt, _ := json.Marshal(map[string]string{"turnId": parent.ID, "runName": "another-run"})
	if err = s.chatStore.CompleteTurn(ctx, parent, chat.Message{Role: chat.RoleAssistant, Content: "Unowned reply", Metadata: wrongReceipt}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, threadID, parentID, parentThread string
		want                                   error
	}{
		{"missing parent", thread.ID, uuid.NewString(), thread.ID, chat.ErrNotFound},
		{"foreign parent", other.ID, parent.ID, thread.ID, chat.ErrRequestConflict},
		{"unowned parent reply", thread.ID, parent.ID, thread.ID, chat.ErrNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.queueChatTurnWithOptions(ctx, "agents", tc.threadID, AppendChatMessageRequest{Content: "Peer results", RequestID: chat.SynthesisRequestID(tc.parentID)}, chatTurnOptions{deferred: true, noDrive: true, resumption: true, parent: tc.parentID, parentThread: tc.parentThread}, 0)
			if !errors.Is(err, tc.want) {
				t.Fatalf("synthesis admission error = %v, want %v", err, tc.want)
			}
		})
	}
	turns, err := s.chatStore.ListTurns(ctx, "agents", thread.ID)
	if err != nil || len(turns) != 1 {
		t.Fatalf("invalid synthesis created an outbox entry: %#v, %v", turns, err)
	}
}

func TestReplyBackBoundsEncodedFourPeerResults(t *testing.T) {
	ctx := context.Background()
	s := chatTestServer(t, true)
	profiles := []string{"inert-one", "inert-two", "inert-three", "inert-four"}
	for _, name := range profiles {
		if err := s.writes.Create(ctx, &agents.AgentRunProfile{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "agents"}}); err != nil {
			t.Fatal(err)
		}
	}
	metadata, _ := json.Marshal(map[string]any{"coordination": ChatCoordination{Enabled: true, ReturnReplies: true, AllowedProfiles: profiles}})
	thread, err := s.chatStore.CreateThread(ctx, chat.Thread{Namespace: "agents", ProfileName: "grok45", CreatedBy: "inert-user", Mode: chat.ModePersona, Metadata: metadata})
	if err != nil {
		t.Fatal(err)
	}
	parent, _, _, err := s.chatStore.QueueTurn(ctx, chat.Turn{Namespace: "agents", ThreadID: thread.ID, ProfileName: thread.ProfileName, RequestID: uuid.NewString(), RunName: "inert-parent"}, chat.Message{Content: "four escaped peer results"})
	if err != nil {
		t.Fatal(err)
	}
	for i, name := range profiles {
		source, _ := json.Marshal(map[string]string{"sourceThreadId": thread.ID, "sourceTurnId": parent.ID, "sourceProfileName": thread.ProfileName})
		childThread, err := s.chatStore.CreateThread(ctx, chat.Thread{Namespace: "agents", ProfileName: name, CreatedBy: "inert-user", Mode: chat.ModePersona, Metadata: source})
		if err != nil {
			t.Fatal(err)
		}
		child, _, _, err := s.chatStore.QueueTurn(ctx, chat.Turn{Namespace: "agents", ThreadID: childThread.ID, ProfileName: name, RequestID: uuid.NewString(), RunName: "inert-peer", ParentTurnID: parent.ID, ParentThreadID: thread.ID, Deferred: true}, chat.Message{Content: "review"})
		if err != nil {
			t.Fatal(err)
		}
		text := strings.Repeat([]string{"<", "\x00", "\\", "😀"}[i], 8192)
		child.Status = "succeeded"
		receipt, _ := json.Marshal(map[string]string{"turnId": child.ID, "runName": child.RunName})
		if err = s.chatStore.CompleteTurn(ctx, child, chat.Message{Role: chat.RoleAssistant, Content: text, Metadata: receipt}); err != nil {
			t.Fatal(err)
		}
		parent.Delegates = append(parent.Delegates, chat.Delegate{ProfileName: name, ThreadID: child.ThreadID, TurnID: child.ID, RunName: child.RunName, Status: child.Status})
	}
	parent.Status = "succeeded"
	parent.Coordination = &chat.Coordination{Status: "waiting"}
	parentReceipt, _ := json.Marshal(map[string]string{"turnId": parent.ID, "runName": parent.RunName})
	if err = s.chatStore.CompleteTurn(ctx, parent, chat.Message{Role: chat.RoleAssistant, Content: "requests sent", Metadata: parentReceipt}); err != nil {
		t.Fatal(err)
	}
	if err = s.reconcileChatReplyBack(ctx, &parent); err != nil {
		t.Fatal(err)
	}
	turns, _ := s.chatStore.ListTurns(ctx, "agents", thread.ID)
	if len(turns) != 2 || turns[0].Coordination.Status != "resuming" {
		t.Fatalf("bounded synthesis not admitted: %#v", turns)
	}
	messages, _ := s.chatStore.ListMessages(ctx, "agents", thread.ID)
	synthesis := messages[len(messages)-1]
	if len(synthesis.Content) > 64*1024 || !strings.Contains(synthesis.Content, "[Peer result truncated.]") {
		t.Fatalf("bad synthesis size: %d", len(synthesis.Content))
	}
}

type foreignBlockedBackend struct {
	*standing.FakeBackend
	entered chan struct{}
	release chan struct{}
}

func (b *foreignBlockedBackend) StreamTurn(_ context.Context, _ standing.SessionHandle, _, _ string, _ standing.Sink) (string, error) {
	close(b.entered)
	<-b.release // Simulate a different replica that cannot observe local cancel.
	return "late peer result", nil
}

func TestCoordinationCancelKeepsForeignHarnessLockUntilReceipt(t *testing.T) {
	ctx := context.Background()
	owner := chatTestServer(t, true)
	standingHarness(t, owner, "inert-shared-harness", agents.AgentRunHarnessBackendOpenCode)
	profile := &agents.AgentRunProfile{ObjectMeta: metav1.ObjectMeta{Name: "inert-peer", Namespace: "agents"}, Spec: agents.AgentRunProfileSpec{HarnessProfileRef: &agents.NamespacedObjectReference{Name: "inert-shared-harness"}}}
	if err := owner.writes.Create(ctx, profile); err != nil {
		t.Fatal(err)
	}
	blocked := &foreignBlockedBackend{FakeBackend: standing.NewFakeBackend(), entered: make(chan struct{}), release: make(chan struct{})}
	enableStandingLive(t, owner, blocked)
	observer, err := NewServer(owner.config, owner.authenticator, owner.writes, owner.logs, logr.Discard())
	if err != nil {
		t.Fatal(err)
	}
	observer.SetChatStore(owner.chatStore)
	observer.SetStandingBackend(standing.NewFakeBackend())
	observer.standingOwner = "foreign-observer"
	parentThread, err := owner.chatStore.CreateThread(ctx, chat.Thread{Namespace: "agents", ProfileName: "grok45", CreatedBy: "inert-user", Mode: chat.ModePersona})
	if err != nil {
		t.Fatal(err)
	}
	parent, _, _, err := owner.chatStore.QueueTurn(ctx, chat.Turn{Namespace: "agents", ThreadID: parentThread.ID, ProfileName: "grok45", RequestID: uuid.NewString(), RunName: "inert-parent"}, chat.Message{Content: "user task"})
	if err != nil {
		t.Fatal(err)
	}
	childThread, err := owner.chatStore.CreateThread(ctx, chat.Thread{Namespace: "agents", ProfileName: "inert-peer", CreatedBy: "inert-user", Mode: chat.ModePersona})
	if err != nil {
		t.Fatal(err)
	}
	child, err := owner.queueChatTurnWithOptions(ctx, "agents", childThread.ID, AppendChatMessageRequest{Content: "peer task"}, chatTurnOptions{deferred: true, noDrive: true, parent: parent.ID, parentThread: parent.ThreadID}, 0)
	if err != nil {
		t.Fatal(err)
	}
	parent.Delegates = []chat.Delegate{{ProfileName: "inert-peer", ThreadID: childThread.ID, TurnID: child.Turn.ID, RunName: child.Turn.RunName}}
	parent.Coordination = &chat.Coordination{Status: "waiting"}
	parent.Status = "succeeded"
	if err = owner.chatStore.CompleteTurn(ctx, parent, chat.Message{Role: chat.RoleAssistant, Content: "peer request sent"}); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { turn := child.Turn; finished <- owner.reconcileChatTurn(ctx, &turn) }()
	select {
	case <-blocked.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("foreign stream did not start")
	}
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.SetPathValue("namespace", "agents")
	req.SetPathValue("threadID", parentThread.ID)
	req.SetPathValue("turnID", parent.ID)
	req = req.WithContext(context.WithValue(req.Context(), principalContextKey{}, testPrincipal(time.Now().Add(time.Hour))))
	response := httptest.NewRecorder()
	observer.handleCancelChatCoordination(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("cancel response %d %s", response.Code, response.Body.String())
	}
	turn := child.Turn
	if err = observer.reconcileChatTurn(ctx, &turn); err != nil {
		t.Fatal(err)
	}
	otherThread, err := owner.chatStore.CreateThread(ctx, chat.Thread{Namespace: "agents", ProfileName: "inert-peer", CreatedBy: "inert-user", Mode: chat.ModePersona})
	if err != nil {
		t.Fatal(err)
	}
	next, err := observer.queueChatTurnWithOptions(ctx, "agents", otherThread.ID, AppendChatMessageRequest{Content: "another conversation"}, chatTurnOptions{deferred: true, noDrive: true}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if active, err := owner.chatStore.ActivateTurn(ctx, next.Turn); err != nil || active {
		t.Fatalf("cancel released live foreign harness: %v %v", active, err)
	}
	close(blocked.release)
	if err = <-finished; err != nil {
		t.Fatal(err)
	}
	if active, err := owner.chatStore.ActivateTurn(ctx, next.Turn); err != nil || !active {
		t.Fatalf("terminal receipt did not release harness: %v %v", active, err)
	}
	messages, _ := owner.chatStore.ListMessages(ctx, "agents", childThread.ID)
	for _, m := range messages {
		if m.Content == "late peer result" {
			t.Fatal("foreign late result persisted")
		}
	}
}
