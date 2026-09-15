package runapi

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	agents "github.com/hazyforge/anvil-agents/api/v1alpha1"
	"github.com/hazyforge/anvil-agents/internal/chat"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// Move only the persisted retry deadline into the past; all production store
// concurrency and reconciliation behavior still runs in restart/budget tests.
type elapsedRetryStore struct{ chat.Store }

func (s elapsedRetryStore) RetryTurn(ctx context.Context, turn chat.Turn, retry chat.TurnRetry) (chat.Turn, error) {
	retry.At = time.Now().Add(-time.Second)
	return s.Store.RetryTurn(ctx, turn, retry)
}

func failBeforeChatJob(t *testing.T, s *Server, turn chat.Turn) *agents.AgentRun {
	t.Helper()
	run := &agents.AgentRun{}
	ctx := context.Background()
	if err := s.writes.Get(ctx, types.NamespacedName{Namespace: turn.Namespace, Name: turn.RunName}, run); err != nil {
		t.Fatal(err)
	}
	run.Status.Phase = agents.AgentRunPhaseFailed
	run.Status.Error = "startup expired"
	run.Status.Conditions = []metav1.Condition{{Type: "Ready", Status: "False", Reason: "ChatStartupDeadlineExceeded", ObservedGeneration: run.Generation, LastTransitionTime: metav1.Now()}}
	if err := s.writes.Status().Update(ctx, run); err != nil {
		t.Fatal(err)
	}
	return run
}

func TestChatAutomaticRetrySurvivesRestartAndCompletesOnce(t *testing.T) {
	ctx := context.Background()
	s := chatTestServer(t, true)
	s.chatStore = elapsedRetryStore{s.chatStore}
	thread := newExecutionThread(t, s, `{}`)
	accepted, err := s.queueChatTurn(ctx, "agents", thread.ID, AppendChatMessageRequest{Content: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	firstRun := failBeforeChatJob(t, s, accepted.Turn)
	// Concurrent API recovery and GET refreshes must advance exactly once.
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			copy := accepted.Turn
			if err := s.reconcileChatTurn(ctx, &copy); err != nil {
				t.Errorf("concurrent retry: %v", err)
			}
		}()
	}
	wg.Wait()
	restarted := *s
	turns, err := restarted.reconcileChatThread(ctx, "agents", thread.ID)
	if err != nil || len(turns) != 1 || turns[0].RetryCount != 1 || turns[0].ID != accepted.Turn.ID {
		t.Fatalf("restart lost attempt: %#v %v", turns, err)
	}
	current := turns[0]
	runs := &agents.AgentRunList{}
	if err := s.writes.List(ctx, runs); err != nil || len(runs.Items) != 2 {
		t.Fatalf("expected exactly original and retry: %#v %v", runs, err)
	}
	secondRun := &agents.AgentRun{}
	if err := s.writes.Get(ctx, types.NamespacedName{Namespace: "agents", Name: current.RunName}, secondRun); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(firstRun.Spec, secondRun.Spec) || !reflect.DeepEqual(firstRun.Labels, secondRun.Labels) {
		t.Fatal("retry changed frozen intent or execution identity")
	}
	setReply(t, &restarted, current, "hello back")
	for range 2 {
		if _, err := restarted.reconcileChatThread(ctx, "agents", thread.ID); err != nil {
			t.Fatal(err)
		}
	}
	messages, err := s.chatStore.ListMessages(ctx, "agents", thread.ID)
	if err != nil || len(messages) != 2 || messages[0].ID != accepted.User.ID || messages[1].Content != "hello back" {
		t.Fatalf("retry transcript %#v %v", messages, err)
	}
	var meta map[string]string
	_ = json.Unmarshal(messages[1].Metadata, &meta)
	if meta["runName"] != current.RunName {
		t.Fatal("assistant reply attributed to the failed attempt")
	}
}

func TestChatAutomaticRetryBudgetEndsInOneFailure(t *testing.T) {
	ctx := context.Background()
	s := chatTestServer(t, true)
	s.chatStore = elapsedRetryStore{s.chatStore}
	thread := newExecutionThread(t, s, `{}`)
	accepted, err := s.queueChatTurn(ctx, "agents", thread.ID, AppendChatMessageRequest{Content: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	current := accepted.Turn
	for attempt := 0; attempt <= chat.MaxTurnRetries; attempt++ {
		failBeforeChatJob(t, s, current)
		turns, err := s.reconcileChatThread(ctx, "agents", thread.ID)
		if err != nil {
			t.Fatal(err)
		}
		current = turns[0]
		if attempt < chat.MaxTurnRetries {
			if !chat.Active(current) || current.RetryCount != attempt+1 {
				t.Fatalf("attempt %d: %#v", attempt, current)
			}
			if err := s.reconcileChatTurn(ctx, &current); err != nil {
				t.Fatal(err)
			}
		}
	}
	for range 2 {
		if _, err := s.reconcileChatThread(ctx, "agents", thread.ID); err != nil {
			t.Fatal(err)
		}
	}
	if current.Status != "failed" || current.RetryCount != 2 || len(current.Attempts) != 2 {
		t.Fatalf("retry budget not terminal: %#v", current)
	}
	runs := &agents.AgentRunList{}
	if err := s.writes.List(ctx, runs); err != nil || len(runs.Items) != 3 {
		t.Fatalf("expected 3 append-only attempts: %d %v", len(runs.Items), err)
	}
	messages, err := s.chatStore.ListMessages(ctx, "agents", thread.ID)
	if err != nil || len(messages) != 2 || messages[1].Role != chat.RoleSystem {
		t.Fatalf("budget exhaustion transcript: %#v %v", messages, err)
	}
}

func TestChatSafeStartupFailureRejectsAmbiguousExecution(t *testing.T) {
	base := &agents.AgentRun{ObjectMeta: metav1.ObjectMeta{Generation: 3}, Status: agents.AgentRunStatus{Phase: agents.AgentRunPhaseFailed, Conditions: []metav1.Condition{{Type: "Ready", Status: "False", Reason: "ChatStartupDeadlineExceeded", ObservedGeneration: 3}}}}
	if !chatSafeStartupFailure(base) {
		t.Fatal("expected fenced no-Job failure to be safe")
	}
	cases := map[string]func(*agents.AgentRun){
		"job receipt":      func(r *agents.AgentRun) { r.Status.JobRef = &agents.NamespacedObjectReference{Name: "job"} },
		"job UID":          func(r *agents.AgentRun) { r.Status.JobUID = "job-uid" },
		"create ambiguity": func(r *agents.AgentRun) { now := metav1.Now(); r.Status.JobCreateAttemptedAt = &now },
		"started":          func(r *agents.AgentRun) { now := metav1.Now(); r.Status.StartedAt = &now },
		"pod receipt":      func(r *agents.AgentRun) { r.Status.RunnerPodRef = &agents.NamespacedObjectReference{Name: "pod"} },
		"pod UID":          func(r *agents.AgentRun) { r.Status.RunnerPodUID = "pod-uid" },
		"stale condition":  func(r *agents.AgentRun) { r.Status.Conditions[0].ObservedGeneration = 2 },
		"missing reply":    func(r *agents.AgentRun) { r.Status.Phase = agents.AgentRunPhaseSucceeded },
		"provider error":   func(r *agents.AgentRun) { r.Status.Conditions[0].Reason = "HarnessFailed" },
		"log spoof":        func(r *agents.AgentRun) { r.Status.Conditions = nil; r.Status.Output = "ChatStartupDeadlineExceeded" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			run := base.DeepCopy()
			mutate(run)
			if chatSafeStartupFailure(run) {
				t.Fatal("ambiguous execution allowed to replay")
			}
		})
	}
}

func TestChatWritePolicyFailureIsDisplayOnly(t *testing.T) {
	s := chatTestServer(t, true)
	ctx := context.Background()
	thread := newExecutionThread(t, s, `{}`)
	accepted, err := s.queueChatTurn(ctx, "agents", thread.ID, AppendChatMessageRequest{Content: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	run := &agents.AgentRun{}
	if err := s.writes.Get(ctx, types.NamespacedName{Namespace: "agents", Name: accepted.Turn.RunName}, run); err != nil {
		t.Fatal(err)
	}
	run.Status.Phase = agents.AgentRunPhaseFailed
	run.Status.Output = "private URL secret-token\nHazy Trade agent credential bootstrap failed: the Hazy Trade default branch lacks the reviewed approval-integrity or restricted-update rules"
	if err := s.writes.Status().Update(ctx, run); err != nil {
		t.Fatal(err)
	}
	turns, err := s.reconcileChatThread(ctx, "agents", thread.ID)
	if err != nil || turns[0].Status != "failed" || turns[0].RetryCount != 0 || turns[0].Error != chatWritePolicySetupFailure || strings.Contains(turns[0].Error, "secret-token") {
		t.Fatalf("unsafe policy handling: %#v %v", turns, err)
	}
}
