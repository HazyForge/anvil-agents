package runapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	agents "github.com/hazyforge/anvil-agents/api/v1alpha1"
	"github.com/hazyforge/anvil-agents/internal/chat"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type unavailableChatReader struct {
	client.Reader
	sawBoundedContext bool
}

func (r *unavailableChatReader) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	deadline, ok := ctx.Deadline()
	r.sawBoundedContext = ok && time.Until(deadline) <= 3*time.Second
	return errors.New("private Kubernetes endpoint diagnostic")
}

func TestChatReadPreservesTranscriptAndLockWhenExecutionRefreshFails(t *testing.T) {
	s := chatTestServer(t, true)
	thread := newExecutionThread(t, s, `{}`)
	accepted, err := s.queueChatTurn(context.Background(), "agents", thread.ID, AppendChatMessageRequest{Content: "what do you do?", RequestID: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	original := s.runs
	unavailable := &unavailableChatReader{Reader: original}
	s.runs = unavailable
	req := httptest.NewRequest(http.MethodGet, "/api/v1/namespaces/agents/chat/threads/"+thread.ID, nil)
	req.Header.Set("Authorization", "Bearer valid")
	recorder := httptest.NewRecorder()
	s.routes().ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK || !unavailable.sawBoundedContext {
		t.Fatalf("response=%d bounded=%v: %s", recorder.Code, unavailable.sawBoundedContext, recorder.Body.String())
	}
	var detail ChatThreadDetailResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if !detail.RecoveryPending || detail.ActiveTurn == nil || detail.ActiveTurn.ID != accepted.Turn.ID || len(detail.Messages) != 1 || detail.Messages[0].Content != "what do you do?" {
		t.Fatalf("lost persisted state: %#v", detail)
	}
	if strings.Contains(recorder.Body.String(), "private Kubernetes") {
		t.Fatal("leaked raw diagnostic")
	}
	s.runs = original
	_, err = s.queueChatTurn(context.Background(), "agents", thread.ID, AppendChatMessageRequest{Content: "another", RequestID: uuid.NewString()})
	if !errors.Is(err, chat.ErrTurnActive) {
		t.Fatalf("must retain execution lock: %v", err)
	}
}

func TestConfirmedChatStartupExpiryPreservesInputAndSchedulesAutomaticRetry(t *testing.T) {
	s := chatTestServer(t, true)
	ctx := context.Background()
	thread := newExecutionThread(t, s, `{}`)
	accepted, err := s.queueChatTurn(ctx, "agents", thread.ID, AppendChatMessageRequest{Content: "what do you do?", RequestID: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	run := &agents.AgentRun{}
	if err := s.writes.Get(ctx, types.NamespacedName{Namespace: "agents", Name: accepted.Turn.RunName}, run); err != nil {
		t.Fatal(err)
	}
	run.Status.Phase = agents.AgentRunPhaseFailed
	run.Status.Error = "This chat turn did not start within 5 minutes. No runner Job was found. Send a new message to try again."
	run.Status.Conditions = []metav1.Condition{{Type: "Ready", Status: "False", Reason: "ChatStartupDeadlineExceeded", Message: run.Status.Error, LastTransitionTime: metav1.Now()}}
	if err := s.writes.Status().Update(ctx, run); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := s.reconcileChatThread(ctx, "agents", thread.ID); err != nil {
			t.Fatal(err)
		}
	}
	messages, err := s.chatStore.ListMessages(ctx, "agents", thread.ID)
	if err != nil || len(messages) != 1 || messages[0].ID != accepted.User.ID {
		t.Fatalf("automatic retry must preserve one user message: %#v %v", messages, err)
	}
	turns, err := s.chatStore.ListTurns(ctx, "agents", thread.ID)
	if err != nil || len(turns) != 1 || turns[0].RetryCount != 1 || turns[0].RunName == accepted.Turn.RunName || turns[0].RetryAt == nil || !turns[0].RetryAt.After(time.Now()) || !chat.Active(turns[0]) {
		t.Fatalf("durable automatic retry: %#v %v", turns, err)
	}
	runs := &agents.AgentRunList{}
	if err := s.writes.List(ctx, runs); err != nil || len(runs.Items) != 1 {
		t.Fatal("retry launched before its persisted backoff")
	}
	if _, err := s.queueChatTurn(ctx, "agents", thread.ID, AppendChatMessageRequest{Content: "competing work", RequestID: uuid.NewString()}); !errors.Is(err, chat.ErrTurnActive) {
		t.Fatalf("automatic retry must retain its execution lock: %v", err)
	}
	if err := s.writes.Get(ctx, types.NamespacedName{Namespace: "agents", Name: accepted.Turn.RunName}, run); err != nil || run.Status.Phase != agents.AgentRunPhaseFailed {
		t.Fatal("old terminal execution changed")
	}
}
