package runapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	agentsv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
	"github.com/hazyforge/anvil-agents/internal/chat"
	"github.com/hazyforge/anvil-agents/internal/standing"
)

// Slice-2 standing turn-path wiring tests. Every test uses FakeBackend or a
// small in-memory stub: no live harness, no model calls, no credentials.

func standingHarness(t *testing.T, server *Server, name string, backend agentsv1alpha1.AgentRunHarnessBackendKind) {
	t.Helper()
	harness := &agentsv1alpha1.AgentHarnessProfile{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "agents"},
		Spec: agentsv1alpha1.AgentHarnessProfileSpec{
			Backend:   agentsv1alpha1.AgentRunHarnessBackendSpec{Kind: backend},
			Execution: agentsv1alpha1.AgentRunHarnessExecutionSpec{Runtime: agentsv1alpha1.AgentRunExecutionRuntimeInProcess},
		},
	}
	if err := server.writes.Create(context.Background(), harness); err != nil {
		t.Fatal(err)
	}
}

func enableStandingLive(t *testing.T, server *Server, backend standing.Backend) {
	t.Helper()
	server.config.Standing.LiveEnabled = true
	server.SetStandingBackend(backend)
}

func standingThread(t *testing.T, server *Server, harnessName string) chat.Thread {
	t.Helper()
	thread, err := server.chatStore.CreateThread(context.Background(), chat.Thread{
		Namespace: "agents",
		Mode:      "persona",
		CreatedBy: "user",
		Metadata:  []byte(`{"harnessProfileName":"` + harnessName + `"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	return thread
}

func TestStandingTurnGateOffKeepsHoldBehavior(t *testing.T) {
	ctx := context.Background()
	server := chatTestServer(t, true)
	standingHarness(t, server, "standing-harness", agentsv1alpha1.AgentRunHarnessBackendOpenCode)
	thread := standingThread(t, server, "standing-harness")

	// Gate off (default): no backend, no live flag.
	accepted, err := server.queueChatTurn(ctx, "agents", thread.ID, AppendChatMessageRequest{Content: "hello standing"})
	if err != nil {
		t.Fatal(err)
	}
	turns, err := server.chatStore.ListTurns(ctx, "agents", thread.ID)
	if err != nil || len(turns) != 1 || !chat.Active(turns[0]) {
		t.Fatalf("gate-off turn = %#v %v, want one active turn", turns, err)
	}
	run := &agentsv1alpha1.AgentRun{}
	if err := server.writes.Get(ctx, types.NamespacedName{Namespace: "agents", Name: accepted.Turn.RunName}, run); err != nil {
		t.Fatal(err)
	}
	if run.Status.Phase != "" {
		t.Fatalf("gate-off run phase = %q, want untouched for the hold path", run.Status.Phase)
	}
	messages, err := server.chatStore.ListMessages(ctx, "agents", thread.ID)
	if err != nil || len(messages) != 1 {
		t.Fatalf("gate-off messages = %#v %v, want only the user message", messages, err)
	}
	// Recovery keeps the hold: still active, still no reply.
	if _, err := server.reconcileChatThread(ctx, "agents", thread.ID); err != nil {
		t.Fatal(err)
	}
	turns, _ = server.chatStore.ListTurns(ctx, "agents", thread.ID)
	if len(turns) != 1 || !chat.Active(turns[0]) {
		t.Fatalf("gate-off recovery turn = %#v, want still active", turns)
	}
}

func TestStandingTurnStreamsDirectTurn(t *testing.T) {
	ctx := context.Background()
	server := chatTestServer(t, true)
	standingHarness(t, server, "standing-harness", agentsv1alpha1.AgentRunHarnessBackendOpenCode)
	fake := standing.NewFakeBackend()
	enableStandingLive(t, server, fake)
	thread := standingThread(t, server, "standing-harness")

	accepted, err := server.queueChatTurn(ctx, "agents", thread.ID, AppendChatMessageRequest{Content: "hello standing"})
	if err != nil {
		t.Fatal(err)
	}
	if accepted.Turn.Status != "succeeded" {
		t.Fatalf("standing turn status = %q, want succeeded", accepted.Turn.Status)
	}
	messages, err := server.chatStore.ListMessages(ctx, "agents", thread.ID)
	if err != nil || len(messages) != 2 {
		t.Fatalf("messages = %#v %v, want user plus standing reply", messages, err)
	}
	if !strings.HasPrefix(messages[1].Content, "standing reply to turn "+accepted.Turn.ID+":") {
		t.Fatalf("assistant reply = %q, want fake standing reply bound to the turn", messages[1].Content)
	}
	run := &agentsv1alpha1.AgentRun{}
	if err := server.writes.Get(ctx, types.NamespacedName{Namespace: "agents", Name: accepted.Turn.RunName}, run); err != nil {
		t.Fatal(err)
	}
	if run.Status.Phase != agentsv1alpha1.AgentRunPhaseSucceeded || run.Status.Backend != "openCode" || run.Status.CompletedAt == nil {
		t.Fatalf("run status = %+v, want Succeeded/openCode with completion", run.Status)
	}
	if !strings.Contains(run.Status.Output, "standing reply to turn "+accepted.Turn.ID) {
		t.Fatalf("run output = %q, want the streamed reply persisted", run.Status.Output)
	}
	if fake.Created() != 1 {
		t.Fatalf("sessions = %d, want exactly one thread session", fake.Created())
	}
	suspends := 0
	for _, call := range fake.Calls {
		if strings.HasPrefix(call, "suspend:") {
			suspends++
		}
	}
	if suspends == 0 {
		t.Fatalf("calls = %v, want best-effort suspend on terminal", fake.Calls)
	}

	// A second turn resumes the same warm session: no cold start.
	second, err := server.queueChatTurn(ctx, "agents", thread.ID, AppendChatMessageRequest{Content: "second turn"})
	if err != nil {
		t.Fatal(err)
	}
	if second.Turn.Status != "succeeded" {
		t.Fatalf("second turn status = %q, want succeeded", second.Turn.Status)
	}
	if fake.Created() != 1 {
		t.Fatalf("sessions = %d after second turn, want warm reuse", fake.Created())
	}
	resumes := 0
	for _, call := range fake.Calls {
		if strings.HasPrefix(call, "resume:") {
			resumes++
		}
	}
	if resumes == 0 {
		t.Fatalf("calls = %v, want at least one warm resume", fake.Calls)
	}
	if fake.Turns() != 2 {
		t.Fatalf("streamed turns = %d, want 2", fake.Turns())
	}
}

func TestStandingTurnPeerChildTakesIdenticalBranch(t *testing.T) {
	ctx := context.Background()
	server := chatTestServer(t, true)
	standingHarness(t, server, "standing-peer-harness", agentsv1alpha1.AgentRunHarnessBackendOpenCode)
	fake := standing.NewFakeBackend()
	enableStandingLive(t, server, fake)
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
	// The manager stays on the Job plane: its run holds until a Succeeded
	// status lands, exactly as today.
	parent, err := server.queueChatTurn(ctx, "agents", manager.ID, AppendChatMessageRequest{Content: "Ask peer for a review"})
	if err != nil {
		t.Fatal(err)
	}
	setReply(t, server, parent.Turn, `{"reply":"I have queued a review request for the peer.","messages":[{"profileName":"peer","content":"Please review the implementation."}]}`)
	turns, err := server.reconcileChatThread(ctx, "agents", manager.ID)
	if err != nil || turns[0].Status != "succeeded" || len(turns[0].Delegates) != 1 {
		t.Fatalf("parent = %#v %v, want succeeded with one delegate", turns, err)
	}
	receipt := turns[0].Delegates[0]
	children, err := server.chatStore.ListTurns(ctx, "agents", receipt.ThreadID)
	if err != nil || len(children) != 1 || children[0].Status != "succeeded" {
		t.Fatalf("peer child = %#v %v, want one succeeded turn via the identical standing branch", children, err)
	}
	childMessages, err := server.chatStore.ListMessages(ctx, "agents", receipt.ThreadID)
	if err != nil || len(childMessages) != 2 {
		t.Fatalf("child messages = %#v %v, want delivery plus standing reply", childMessages, err)
	}
	if !strings.HasPrefix(childMessages[1].Content, "standing reply to turn "+children[0].ID+":") {
		t.Fatalf("child reply = %q, want standing reply bound to the child turn", childMessages[1].Content)
	}
	childRun := &agentsv1alpha1.AgentRun{}
	if err := server.writes.Get(ctx, types.NamespacedName{Namespace: "agents", Name: children[0].RunName}, childRun); err != nil {
		t.Fatal(err)
	}
	if childRun.Status.Phase != agentsv1alpha1.AgentRunPhaseSucceeded {
		t.Fatalf("child run phase = %q, want Succeeded", childRun.Status.Phase)
	}
	if childRun.Spec.ProfileRef == nil || childRun.Spec.ProfileRef.Name != "peer" {
		t.Fatalf("child run profile ref = %+v, want the peer profile carried on the durable run", childRun.Spec.ProfileRef)
	}
	if fake.Created() != 1 {
		t.Fatalf("sessions = %d, want one recipient child-thread session", fake.Created())
	}
}

type errStandingBackend struct {
	streamErr error
}

func (backend errStandingBackend) CreateSession(ctx context.Context, spec standing.SessionSpec) (standing.SessionHandle, error) {
	return standing.SessionHandle{Namespace: spec.Namespace, ThreadID: spec.ThreadID, SessionName: spec.SessionName}, nil
}

func (backend errStandingBackend) ResumeSession(_ context.Context, namespace, name string) (standing.SessionHandle, error) {
	return standing.SessionHandle{Namespace: namespace, SessionName: name}, nil
}

func (backend errStandingBackend) DescribeSession(_ context.Context, namespace, name string) (standing.SessionHandle, error) {
	return standing.SessionHandle{Namespace: namespace, SessionName: name}, nil
}

func (backend errStandingBackend) SuspendSession(_ context.Context, namespace, name string) (standing.SessionHandle, error) {
	return standing.SessionHandle{Namespace: namespace, SessionName: name}, nil
}

func (backend errStandingBackend) StreamTurn(_ context.Context, _ standing.SessionHandle, _, _ string, _ standing.Sink) (string, error) {
	return "", backend.streamErr
}

func TestStandingTurnBackendErrorFallsBackToHold(t *testing.T) {
	ctx := context.Background()
	server := chatTestServer(t, true)
	standingHarness(t, server, "standing-harness", agentsv1alpha1.AgentRunHarnessBackendOpenCode)
	enableStandingLive(t, server, errStandingBackend{streamErr: context.DeadlineExceeded})
	thread := standingThread(t, server, "standing-harness")

	accepted, err := server.queueChatTurn(ctx, "agents", thread.ID, AppendChatMessageRequest{Content: "hello standing"})
	if err != nil {
		t.Fatal(err)
	}
	turns, _ := server.chatStore.ListTurns(ctx, "agents", thread.ID)
	if len(turns) != 1 || !chat.Active(turns[0]) {
		t.Fatalf("error turn = %#v, want active hold for the controller guidance", turns)
	}
	messages, _ := server.chatStore.ListMessages(ctx, "agents", thread.ID)
	if len(messages) != 1 {
		t.Fatalf("error messages = %d, want only the user message", len(messages))
	}
	run := &agentsv1alpha1.AgentRun{}
	if err := server.writes.Get(ctx, types.NamespacedName{Namespace: "agents", Name: accepted.Turn.RunName}, run); err != nil {
		t.Fatal(err)
	}
	if run.Status.Phase != "" {
		t.Fatalf("error run phase = %q, want untouched", run.Status.Phase)
	}
}

func TestStandingTurnUnresolvableHarnessFallsBackToHold(t *testing.T) {
	ctx := context.Background()
	server := chatTestServer(t, true)
	standingHarness(t, server, "standing-harness", agentsv1alpha1.AgentRunHarnessBackendOpenCode)
	thread := standingThread(t, server, "standing-harness")

	// Queue with the gate off so the run holds, then enable the gate while
	// the harness is gone: the turn must stay on today's hold behavior.
	accepted, err := server.queueChatTurn(ctx, "agents", thread.ID, AppendChatMessageRequest{Content: "hello standing"})
	if err != nil {
		t.Fatal(err)
	}
	if err := server.writes.Delete(ctx, &agentsv1alpha1.AgentHarnessProfile{ObjectMeta: metav1.ObjectMeta{Name: "standing-harness", Namespace: "agents"}}); err != nil {
		t.Fatal(err)
	}
	enableStandingLive(t, server, standing.NewFakeBackend())
	turns, err := server.reconcileChatThread(ctx, "agents", thread.ID)
	if err != nil || len(turns) != 1 || !chat.Active(turns[0]) {
		t.Fatalf("unresolvable turn = %#v %v, want still active", turns, err)
	}
	run := &agentsv1alpha1.AgentRun{}
	if err := server.writes.Get(ctx, types.NamespacedName{Namespace: "agents", Name: accepted.Turn.RunName}, run); err != nil {
		t.Fatal(err)
	}
	if run.Status.Phase != "" {
		t.Fatalf("unresolvable run phase = %q, want untouched", run.Status.Phase)
	}
}

// captureStandingBackend records the turn identity a StreamTurn call binds
// (thread, turn, frozen prompt) and returns a canned reply.
type captureStandingBackend struct {
	inner      *standing.FakeBackend
	reply      string
	lastThread string
	lastTurn   string
	lastPrompt string
}

func (backend *captureStandingBackend) CreateSession(ctx context.Context, spec standing.SessionSpec) (standing.SessionHandle, error) {
	return backend.inner.CreateSession(ctx, spec)
}

func (backend *captureStandingBackend) ResumeSession(ctx context.Context, namespace, name string) (standing.SessionHandle, error) {
	return backend.inner.ResumeSession(ctx, namespace, name)
}

func (backend *captureStandingBackend) DescribeSession(ctx context.Context, namespace, name string) (standing.SessionHandle, error) {
	return backend.inner.DescribeSession(ctx, namespace, name)
}

func (backend *captureStandingBackend) SuspendSession(ctx context.Context, namespace, name string) (standing.SessionHandle, error) {
	return backend.inner.SuspendSession(ctx, namespace, name)
}

func (backend *captureStandingBackend) StreamTurn(ctx context.Context, handle standing.SessionHandle, turnID, prompt string, sink standing.Sink) (string, error) {
	backend.lastThread = handle.ThreadID
	backend.lastTurn = turnID
	backend.lastPrompt = prompt
	if sink != nil {
		if err := sink.OnToken(ctx, standing.TokenEvent{ThreadID: handle.ThreadID, TurnID: turnID, Token: backend.reply}); err != nil {
			return "", err
		}
		if err := sink.OnToken(ctx, standing.TokenEvent{ThreadID: handle.ThreadID, TurnID: turnID, Seq: 1, Done: true}); err != nil {
			return "", err
		}
	}
	return backend.reply, nil
}

func TestStandingTurnBindsFrozenIntentAndTurnIdentity(t *testing.T) {
	ctx := context.Background()
	server := chatTestServer(t, true)
	standingHarness(t, server, "standing-harness", agentsv1alpha1.AgentRunHarnessBackendOpenCode)
	capture := &captureStandingBackend{inner: standing.NewFakeBackend(), reply: "canned standing reply"}
	server.config.Standing.LiveEnabled = true
	server.SetStandingBackend(capture)
	thread := standingThread(t, server, "standing-harness")

	accepted, err := server.queueChatTurn(ctx, "agents", thread.ID, AppendChatMessageRequest{Content: "frozen intent check"})
	if err != nil {
		t.Fatal(err)
	}
	if accepted.Turn.Status != "succeeded" {
		t.Fatalf("turn status = %q, want succeeded", accepted.Turn.Status)
	}
	if capture.lastThread != thread.ID || capture.lastTurn != accepted.Turn.ID {
		t.Fatalf("stream identity = %q/%q, want %q/%q", capture.lastThread, capture.lastTurn, thread.ID, accepted.Turn.ID)
	}
	if !strings.Contains(capture.lastPrompt, "CONVERSATION_JSON") || !strings.Contains(capture.lastPrompt, "frozen intent check") {
		t.Fatalf("stream prompt = %q, want the outbox-frozen intent", capture.lastPrompt)
	}
	messages, _ := server.chatStore.ListMessages(ctx, "agents", thread.ID)
	if len(messages) != 2 || messages[1].Content != "canned standing reply" {
		t.Fatalf("messages = %#v, want the canned reply persisted", messages)
	}
}

func TestStandingTurnSinkStampsRunName(t *testing.T) {
	recorder := &runNameRecorder{}
	sink := &standingRunSink{runName: "chat-turn-abc", downstream: recorder}
	if err := sink.OnToken(context.Background(), standing.TokenEvent{ThreadID: "thread", TurnID: "turn", Token: "hi"}); err != nil {
		t.Fatal(err)
	}
	if sink.tokens != 1 {
		t.Fatalf("tokens = %d, want 1", sink.tokens)
	}
	if len(recorder.events) != 1 || recorder.events[0].RunName != "chat-turn-abc" ||
		recorder.events[0].ThreadID != "thread" || recorder.events[0].TurnID != "turn" {
		t.Fatalf("downstream = %+v, want the full turn identity stamped", recorder.events)
	}
}

type runNameRecorder struct {
	events []standing.TokenEvent
}

func (recorder *runNameRecorder) OnToken(_ context.Context, event standing.TokenEvent) error {
	recorder.events = append(recorder.events, event)
	return nil
}

func TestStandingRunOutputRoundTripsExtractors(t *testing.T) {
	// Adversarial quoting: a reply containing quotes, backslashes, and
	// newlines must survive the envelope without breaking its framing.
	reply := "Standing \"stub\" reply, two lines.\nSecond line with \\ and \"quotes\"."
	for _, backend := range []agentsv1alpha1.AgentRunHarnessBackendKind{
		agentsv1alpha1.AgentRunHarnessBackendCodex,
		agentsv1alpha1.AgentRunHarnessBackendOpenCode,
		agentsv1alpha1.AgentRunHarnessBackendHermesAgent,
		agentsv1alpha1.AgentRunHarnessBackendOpenClaw,
		agentsv1alpha1.AgentRunHarnessBackendGrokBuild,
		agentsv1alpha1.AgentRunHarnessBackendPiAgent,
		agentsv1alpha1.AgentRunHarnessBackendPrimeAgent,
		agentsv1alpha1.AgentRunHarnessBackendAgy,
		agentsv1alpha1.AgentRunHarnessBackendCustom,
		"",
	} {
		output := standingRunOutput(backend, reply)
		got, err := extractChatReply(backend, output)
		if err != nil || got != reply {
			t.Fatalf("backend %q: reply = %q err = %v, want exact round-trip", backend, got, err)
		}
	}
}

func TestStandingTurnGuardSerializesOneProcess(t *testing.T) {
	guard := newStandingTurnGuard()
	id := uuid.NewString()
	if !guard.claim(id) {
		t.Fatal("first claim must win")
	}
	if guard.claim(id) {
		t.Fatal("second claim must lose while held")
	}
	guard.release(id)
	if !guard.claim(id) {
		t.Fatal("claim must win after release")
	}
	guard.release(id)
	var nilGuard *standingTurnGuard
	if !nilGuard.claim(id) {
		t.Fatal("nil guard must allow")
	}
	nilGuard.release(id)
}

func TestStandingTurnResultsVisibleOnStreamPath(t *testing.T) {
	server := chatTestServer(t, true)
	standingHarness(t, server, "standing-harness", agentsv1alpha1.AgentRunHarnessBackendOpenCode)
	enableStandingLive(t, server, standing.NewFakeBackend())
	thread := createStreamThread(t, server, `{"harnessProfileName":"standing-harness","mode":"persona"}`)
	appendStreamMessage(t, server, thread.ID, "hello standing stream")

	request := httptest.NewRequest(http.MethodGet, "/api/v1/namespaces/agents/chat/threads/"+thread.ID+"/stream", nil)
	request.Header.Set("Authorization", "Bearer valid")
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("stream: %d %s", response.Code, response.Body.String())
	}
	var snapshot struct {
		Type     string                  `json:"type"`
		Messages []chat.Message          `json:"messages"`
		Standing *chatStreamStandingView `json:"standing"`
	}
	if err := json.Unmarshal(sseStreamEvent(t, response.Body.String(), "snapshot"), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Standing == nil {
		t.Fatal("snapshot standing is nil, want the resumed thread session")
	}
	found := false
	for _, message := range snapshot.Messages {
		if message.Role == chat.RoleAssistant && strings.HasPrefix(message.Content, "standing reply to turn ") {
			found = true
		}
	}
	if !found {
		t.Fatalf("snapshot messages = %+v, want the streamed standing reply in the durable tail", snapshot.Messages)
	}
	var terminal chatStreamTerminal
	if err := json.Unmarshal(sseStreamEvent(t, response.Body.String(), "terminal"), &terminal); err != nil {
		t.Fatal(err)
	}
	if terminal.Code != "standing_ready" {
		t.Fatalf("terminal = %+v, want standing_ready", terminal)
	}
}
