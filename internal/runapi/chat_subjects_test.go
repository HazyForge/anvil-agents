package runapi

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	agentsv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
	"github.com/hazyforge/anvil-agents/internal/chat"
	"github.com/hazyforge/anvil-agents/internal/subjectrouting"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

type subjectDecisionFunc func(context.Context, subjectrouting.Input) (subjectrouting.Decision, error)

func (f subjectDecisionFunc) Decide(ctx context.Context, input subjectrouting.Input) (subjectrouting.Decision, error) {
	return f(ctx, input)
}

type subjectTestAuthenticator struct{ ownerPrefix string }

func (subjectTestAuthenticator) Ready() bool { return true }
func (auth subjectTestAuthenticator) Verify(_ context.Context, token string) (Principal, error) {
	if token != "alice" && token != "bob" {
		return Principal{}, ErrInvalidAccessToken
	}
	principal := testPrincipal(time.Now().Add(time.Hour))
	prefix := auth.ownerPrefix
	if prefix == "" {
		prefix = "owner-"
	}
	principal.Subject = prefix + token
	return principal, nil
}
func subjectTestServer(t *testing.T) *Server {
	t.Helper()
	server := chatTestServer(t, true)
	server.config.Chat.SubjectRoutingEnabled = true
	server.authenticator = subjectTestAuthenticator{}
	server.config.Stream.MaxDuration = NewDuration(25 * time.Millisecond)
	if err := server.writes.Create(context.Background(), &agentsv1alpha1.AgentRunProfile{ObjectMeta: metav1.ObjectMeta{Name: "manager", Namespace: "agents"}}); err != nil {
		t.Fatal(err)
	}
	return server
}
func subjectDecision(input subjectrouting.Input, choice, intent string) subjectrouting.Decision {
	probabilities := map[string]float64{subjectrouting.NewSubject: 0, subjectrouting.NoSubject: 0}
	for _, candidate := range input.Candidates {
		probabilities[candidate.ID] = 0
	}
	probabilities[choice] = 1
	intents := map[string]float64{subjectrouting.IntentQuestion: 0, subjectrouting.IntentAmendRunning: 0, subjectrouting.IntentNewWork: 0, subjectrouting.IntentStatus: 0, subjectrouting.IntentDecide: 0, subjectrouting.IntentUnclear: 0}
	intents[intent] = 1
	return subjectrouting.Decision{SubjectChoice: choice, SubjectConfidence: 1, SubjectProbabilities: probabilities, Intent: intent, IntentConfidence: 1, IntentProbabilities: intents, Model: "fixture-subject-decisions"}
}
func subjectHTTPRequest(t *testing.T, server *Server, token, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var raw []byte
	var err error
	if body != nil {
		raw, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(raw))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	return response
}
func subjectManagerRequest(t *testing.T, server *Server, token string, body ManagerMessageRequest) ManagerMessageResponse {
	t.Helper()
	response := subjectHTTPRequest(t, server, token, http.MethodPost, "/api/v1/namespaces/agents/chat/manager/messages", body)
	if response.Code != http.StatusOK && response.Code != http.StatusAccepted {
		t.Fatalf("manager HTTP %d: %s", response.Code, response.Body.String())
	}
	var result ManagerMessageResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}
func subjectRunCount(t *testing.T, server *Server) int {
	t.Helper()
	list := &agentsv1alpha1.AgentRunList{}
	if err := server.runs.List(context.Background(), list); err != nil {
		t.Fatal(err)
	}
	return len(list.Items)
}
func seedPrivateSubject(t *testing.T, server *Server, label string) chat.Subject {
	t.Helper()
	ctx := context.Background()
	metadata := json.RawMessage(`{"subjectOwner":"owner-alice"}`)
	thread, err := server.chatStore.CreateThread(ctx, chat.Thread{Namespace: "agents", CreatedBy: "owner-alice", ProfileName: "manager", Mode: chat.ModePersona, Title: label, Metadata: metadata})
	if err != nil {
		t.Fatal(err)
	}
	subject, err := server.chatStore.(chat.SubjectStore).PutSubject(ctx, chat.Subject{Namespace: "agents", Owner: "owner-alice", ThreadID: thread.ID, ProfileName: "manager", Kind: "project", Label: label, Summary: "Existing work on " + label})
	if err != nil {
		t.Fatal(err)
	}
	return subject
}

type subjectWireEvent struct {
	name string
	data json.RawMessage
	err  error
}

func subjectSSEEvents(body io.Reader) <-chan subjectWireEvent {
	events := make(chan subjectWireEvent, 32)
	go func() {
		defer close(events)
		scanner := bufio.NewScanner(body)
		scanner.Buffer(make([]byte, 4096), 512*1024)
		var event subjectWireEvent
		for scanner.Scan() {
			line := scanner.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				event.name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				event.data = append(event.data, strings.TrimPrefix(line, "data: ")...)
			case line == "":
				if event.name != "" {
					events <- event
				}
				event = subjectWireEvent{}
			}
		}
		if err := scanner.Err(); err != nil {
			events <- subjectWireEvent{err: err}
		}
	}()
	return events
}
func nextSubjectEvent(t *testing.T, events <-chan subjectWireEvent) subjectWireEvent {
	t.Helper()
	select {
	case event, ok := <-events:
		if !ok {
			t.Fatal("routing stream ended before result")
		}
		if event.err != nil {
			t.Fatal(event.err)
		}
		return event
	case <-time.After(3 * time.Second):
		t.Fatal("routing stage was not flushed to the client")
		return subjectWireEvent{}
	}
}

// This test owns the observable live stream and accepted outbox contract: stages
// must reach a network client before inference completes; replay cannot create a
// second execution or reclassify an accepted message.
func TestSubjectManagerStreamsThenDispatchesOnce(t *testing.T) {
	server := subjectTestServer(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	var calls atomic.Int32
	var enteredOnce sync.Once
	server.SetSubjectDecisionProvider(subjectDecisionFunc(func(ctx context.Context, input subjectrouting.Input) (subjectrouting.Decision, error) {
		calls.Add(1)
		enteredOnce.Do(func() { close(entered) })
		select {
		case <-release:
			return subjectDecision(input, subjectrouting.NewSubject, subjectrouting.IntentNewWork), nil
		case <-ctx.Done():
			return subjectrouting.Decision{}, ctx.Err()
		}
	}))
	httpServer := httptest.NewServer(server.routes())
	defer func() { unblock(); httpServer.Close() }()
	body := ManagerMessageRequest{RequestID: uuid.NewString(), ProfileName: "manager", Content: "Build the launch website"}
	raw, _ := json.Marshal(body)
	request, err := http.NewRequest(http.MethodPost, httpServer.URL+"/api/v1/namespaces/agents/chat/manager/messages", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer alice")
	request.Header.Set("Accept", "text/event-stream")
	request.Header.Set("Content-Type", "application/json")
	client := httpServer.Client()
	client.Timeout = 5 * time.Second
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("not an SSE response: %d %s", response.StatusCode, response.Header.Get("Content-Type"))
	}
	events := subjectSSEEvents(response.Body)
	for _, step := range []string{"received", "subjects", "classifying"} {
		event := nextSubjectEvent(t, events)
		var stage RoutingEvent
		if err = json.Unmarshal(event.data, &stage); err != nil {
			t.Fatal(err)
		}
		if event.name != "routing" || stage.Step != step {
			t.Fatalf("wanted live step %s, got %s %s", step, event.name, event.data)
		}
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("provider never started")
	}
	if count := subjectRunCount(t, server); count != 0 {
		t.Fatalf("%d runs created before classification", count)
	}
	unblock()
	var result ManagerMessageResponse
	for result.Status == "" {
		event := nextSubjectEvent(t, events)
		if event.name == "result" {
			if err = json.Unmarshal(event.data, &result); err != nil {
				t.Fatal(err)
			}
		}
	}
	if result.Status != "accepted" || result.Subject == nil || result.Thread == nil || result.User == nil || result.Turn == nil {
		t.Fatalf("missing actual handoff: %+v", result)
	}
	run := &agentsv1alpha1.AgentRun{}
	if err = server.runs.Get(context.Background(), types.NamespacedName{Namespace: "agents", Name: result.Turn.RunName}, run); err != nil {
		t.Fatal(err)
	}
	if run.Annotations[subjectOwnerAnnotation] != "owner-alice" || run.Annotations["control.anvil.hazyforge.io/subject-id"] != result.Subject.ID || run.Annotations["control.anvil.hazyforge.io/subject-intent"] != "new_work" {
		t.Fatalf("run lost routed identity: %+v", run.Annotations)
	}
	if !strings.Contains(run.Spec.Prompt, "SUBJECT_CONTEXT_JSON:") || !strings.Contains(run.Spec.Prompt, result.Subject.ID) || !strings.Contains(run.Spec.Prompt, `"intent":"new_work"`) || !strings.Contains(run.Spec.Prompt, body.Content) {
		t.Fatalf("frozen execution omitted subject context: %s", run.Spec.Prompt)
	}
	replay := subjectManagerRequest(t, server, "alice", body)
	if replay.Turn == nil || replay.Turn.ID != result.Turn.ID || replay.Thread == nil || replay.Thread.ID != result.Thread.ID || calls.Load() != 1 || subjectRunCount(t, server) != 1 {
		t.Fatalf("accepted replay changed execution: %+v, calls=%d", replay, calls.Load())
	}
	// Acceptance is immutable even if its selected profile is later retired.
	if err = server.writes.Delete(context.Background(), &agentsv1alpha1.AgentRunProfile{ObjectMeta: metav1.ObjectMeta{Name: "manager", Namespace: "agents"}}); err != nil {
		t.Fatal(err)
	}
	replayedAfterRetirement := subjectManagerRequest(t, server, "alice", body)
	if replayedAfterRetirement.Turn == nil || replayedAfterRetirement.Turn.ID != result.Turn.ID || calls.Load() != 1 || subjectRunCount(t, server) != 1 {
		t.Fatalf("retiring a profile changed accepted work: %+v", replayedAfterRetirement)
	}
	conflict := body
	conflict.Content = "Different work using the same request id"
	rejected := subjectHTTPRequest(t, server, "alice", http.MethodPost, "/api/v1/namespaces/agents/chat/manager/messages", conflict)
	if rejected.Code != 409 || calls.Load() != 1 || subjectRunCount(t, server) != 1 {
		t.Fatalf("conflicting request reuse: %d %s", rejected.Code, rejected.Body.String())
	}
}

func TestSubjectManagerClarificationDoesNotExecuteUntilUserChooses(t *testing.T) {
	for _, cause := range []string{"ambiguous", "unavailable"} {
		t.Run(cause, func(t *testing.T) {
			server := subjectTestServer(t)
			subject := seedPrivateSubject(t, server, "Launch")
			var calls atomic.Int32
			server.SetSubjectDecisionProvider(subjectDecisionFunc(func(_ context.Context, input subjectrouting.Input) (subjectrouting.Decision, error) {
				calls.Add(1)
				if cause == "unavailable" {
					return subjectrouting.Decision{}, errors.New("private-provider-detail")
				}
				decision := subjectDecision(input, subject.ID, subjectrouting.IntentQuestion)
				decision.SubjectConfidence = .7
				decision.SubjectProbabilities[subject.ID] = .53
				decision.SubjectProbabilities[subjectrouting.NewSubject] = .47
				return decision, nil
			}))
			body := ManagerMessageRequest{RequestID: uuid.NewString(), ProfileName: "manager", Content: "What about that other idea?"}
			result := subjectManagerRequest(t, server, "alice", body)
			if result.Status != "clarify" || result.Clarification == "" || result.Turn != nil || subjectRunCount(t, server) != 0 {
				t.Fatalf("uncertain message executed: %+v", result)
			}
			encoded, _ := json.Marshal(result)
			if bytes.Contains(encoded, []byte("private-provider-detail")) {
				t.Fatal("provider error exposed")
			}
			replay := subjectManagerRequest(t, server, "alice", body)
			if replay.Status != "clarify" || calls.Load() != 1 {
				t.Fatalf("clarification replay reclassified: %+v calls=%d", replay, calls.Load())
			}
			body.SubjectID = subject.ID
			resolved := subjectManagerRequest(t, server, "alice", body)
			if resolved.Status != "accepted" || resolved.Subject == nil || resolved.Subject.ID != subject.ID || resolved.Turn == nil || subjectRunCount(t, server) != 1 || calls.Load() != 1 {
				t.Fatalf("human correction did not dispatch once: %+v calls=%d", resolved, calls.Load())
			}
			replay = subjectManagerRequest(t, server, "alice", body)
			if replay.Turn == nil || replay.Turn.ID != resolved.Turn.ID || subjectRunCount(t, server) != 1 {
				t.Fatalf("corrected request replay duplicated: %+v", replay)
			}
		})
	}
}

func TestSubjectManagerPrivateSurfacesAndGates(t *testing.T) {
	server := subjectTestServer(t)
	body := ManagerMessageRequest{RequestID: uuid.NewString(), ProfileName: "manager", Content: "Private launch planning", SubjectID: subjectrouting.NewSubject}
	result := subjectManagerRequest(t, server, "alice", body)
	if result.Status != "accepted" || result.Thread == nil || result.Subject == nil || result.Turn == nil {
		t.Fatalf("setup handoff: %+v", result)
	}
	base := "/api/v1/namespaces/agents"
	threadPath := base + "/chat/threads/" + result.Thread.ID
	runPath := base + "/agent-runs/" + result.Turn.RunName
	for _, path := range []string{base + "/chat/subjects", base + "/chat/routing", base + "/chat/threads", base + "/agent-runs"} {
		own := subjectHTTPRequest(t, server, "alice", http.MethodGet, path, nil)
		if own.Code != 200 {
			t.Fatalf("owner list %s: %d %s", path, own.Code, own.Body.String())
		}
		other := subjectHTTPRequest(t, server, "bob", http.MethodGet, path, nil)
		var list struct {
			Items []json.RawMessage `json:"items"`
		}
		err := json.Unmarshal(other.Body.Bytes(), &list)
		if other.Code != 200 || err != nil || len(list.Items) != 0 {
			t.Fatalf("private list leaked through %s: %d %s", path, other.Code, other.Body.String())
		}
	}
	for _, path := range []string{base + "/chat/routing/" + body.RequestID, threadPath, threadPath + "/messages", threadPath + "/stream", runPath, runPath + "/events"} {
		own := subjectHTTPRequest(t, server, "alice", http.MethodGet, path, nil)
		if own.Code != 200 {
			t.Fatalf("owner access %s: %d %s", path, own.Code, own.Body.String())
		}
		other := subjectHTTPRequest(t, server, "bob", http.MethodGet, path, nil)
		if other.Code != 404 || strings.Contains(other.Body.String(), body.Content) {
			t.Fatalf("private detail leaked through %s: %d %s", path, other.Code, other.Body.String())
		}
	}
	for _, change := range []struct {
		path string
		body any
	}{{threadPath + "/messages", AppendChatMessageRequest{RequestID: uuid.NewString(), Content: "intrusion"}}, {base + "/chat/subjects/" + result.Subject.ID, map[string]string{"label": "intrusion"}}, {base + "/chat/subjects/" + result.Subject.ID + "/merge", map[string]string{"targetId": uuid.NewString()}}} {
		other := subjectHTTPRequest(t, server, "bob", http.MethodPost, change.path, change.body)
		if other.Code != 404 {
			t.Fatalf("foreign write through %s: %d %s", change.path, other.Code, other.Body.String())
		}
	}
	for _, path := range []string{"/api/v1/namespaces/other/chat/subjects", "/api/v1/namespaces/other/chat/routing"} {
		if got := subjectHTTPRequest(t, server, "alice", http.MethodGet, path, nil); got.Code != 404 {
			t.Fatalf("namespace gate: %d %s", got.Code, got.Body.String())
		}
	}
	if got := subjectHTTPRequest(t, server, "alice", http.MethodPost, "/api/v1/namespaces/other/chat/manager/messages", body); got.Code != 404 {
		t.Fatalf("namespace write gate: %d", got.Code)
	}
	server.config.Chat.SubjectRoutingEnabled = false
	for _, path := range []string{base + "/chat/subjects", base + "/chat/routing"} {
		if got := subjectHTTPRequest(t, server, "alice", http.MethodGet, path, nil); got.Code != 404 {
			t.Fatalf("disabled read gate: %d", got.Code)
		}
	}
	if got := subjectHTTPRequest(t, server, "alice", http.MethodPost, base+"/chat/manager/messages", body); got.Code != 404 {
		t.Fatalf("disabled write gate: %d", got.Code)
	}
	if subjectRunCount(t, server) != 1 {
		t.Fatal("denied requests changed execution state")
	}
	// A newer private record must not consume another reader's result limit.
	// Shared namespace history still belongs in that reader's first page.
	older := time.Now().Add(-time.Hour)
	sharedThread, err := server.chatStore.CreateThread(context.Background(), chat.Thread{Namespace: "agents", CreatedBy: "shared-author", ProfileName: "manager", Mode: chat.ModePersona, Title: "Shared planning", CreatedAt: older})
	if err != nil {
		t.Fatal(err)
	}
	for _, run := range []*agentsv1alpha1.AgentRun{
		{ObjectMeta: metav1.ObjectMeta{Name: "shared-history", Namespace: "agents", CreationTimestamp: metav1.NewTime(older)}},
		{ObjectMeta: metav1.ObjectMeta{Name: "newer-private-history", Namespace: "agents", CreationTimestamp: metav1.Now(), Annotations: map[string]string{subjectOwnerAnnotation: "owner-alice"}}},
	} {
		if err = server.writes.Create(context.Background(), run); err != nil {
			t.Fatal(err)
		}
	}
	for _, page := range []struct{ path, want string }{
		{base + "/chat/threads?limit=1", sharedThread.ID},
		{base + "/agent-runs?limit=1", "shared-history"},
	} {
		response := subjectHTTPRequest(t, server, "bob", http.MethodGet, page.path, nil)
		var list struct {
			Items []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"items"`
		}
		if err = json.Unmarshal(response.Body.Bytes(), &list); err != nil {
			t.Fatal(err)
		}
		if response.Code != http.StatusOK || len(list.Items) != 1 || (list.Items[0].ID != page.want && list.Items[0].Name != page.want) {
			t.Fatalf("private record consumed visible page through %s: status=%d items=%+v", page.path, response.Code, list.Items)
		}
	}
}

type failAcceptedReceiptStore struct {
	*chat.MemoryStore
	failed bool
}

func (s *failAcceptedReceiptStore) SaveRoutingRequest(ctx context.Context, r chat.RoutingRequest) (chat.RoutingRequest, error) {
	if r.Status == "accepted" && !s.failed {
		s.failed = true
		return chat.RoutingRequest{}, errors.New("injected failure after outbox commit")
	}
	return s.MemoryStore.SaveRoutingRequest(ctx, r)
}

// A failed receipt write is a real crash boundary after accepted execution. A
// later subject merge must not move that immutable request to a second outbox.
func TestSubjectManagerRecoversOriginalTurnAfterReceiptFailureAndMerge(t *testing.T) {
	server := subjectTestServer(t)
	store := &failAcceptedReceiptStore{MemoryStore: chat.NewMemoryStore()}
	server.SetChatStore(store)
	body := ManagerMessageRequest{RequestID: uuid.NewString(), ProfileName: "manager", Content: "Build launch page", SubjectID: subjectrouting.NewSubject}
	interrupted := subjectManagerRequest(t, server, "alice", body)
	if interrupted.Status != "failed" || interrupted.Subject == nil || interrupted.Turn == nil || interrupted.Thread == nil {
		t.Fatalf("failure boundary not reached: %+v", interrupted)
	}
	if subjectRunCount(t, server) != 1 {
		t.Fatal("failure occurred before accepted execution")
	}
	receipt, err := store.GetRoutingRequest(context.Background(), "agents", "owner-alice", body.RequestID)
	if err != nil || receipt.Status == "accepted" || receipt.ThreadID != interrupted.Thread.ID {
		t.Fatalf("receipt unexpectedly finalized: %+v %v", receipt, err)
	}
	target := seedPrivateSubject(t, server, "Main website")
	merged, err := store.MergeSubjects(context.Background(), "agents", "owner-alice", interrupted.Subject.ID, target.ID)
	if err != nil || merged.ID != target.ID {
		t.Fatal(err)
	}
	body.SubjectID = target.ID
	recovered := subjectManagerRequest(t, server, "alice", body)
	if recovered.Status != "accepted" || recovered.Thread == nil || recovered.Turn == nil || recovered.Thread.ID != interrupted.Thread.ID || recovered.Turn.ID != interrupted.Turn.ID || recovered.Turn.RunName != interrupted.Turn.RunName {
		t.Fatalf("recovery moved accepted work after merge: original thread=%s turn=%s; recovered thread=%+v turn=%+v", interrupted.Thread.ID, interrupted.Turn.ID, recovered.Thread, recovered.Turn)
	}
	targetTurns, err := store.ListTurns(context.Background(), "agents", target.ThreadID)
	if err != nil || len(targetTurns) != 0 || subjectRunCount(t, server) != 1 {
		t.Fatalf("merge replay duplicated execution: target turns=%+v err=%v", targetTurns, err)
	}
}

// The store owns FIFO activation. This boundary checks the manager actually
// admits ordinary follow-ups to a busy subject using that durable queue.
func TestSubjectManagerQueuesFollowupsOnActiveSubject(t *testing.T) {
	server := subjectTestServer(t)
	server.SetSubjectDecisionProvider(subjectDecisionFunc(func(_ context.Context, input subjectrouting.Input) (subjectrouting.Decision, error) {
		if len(input.Candidates) == 0 {
			return subjectDecision(input, subjectrouting.NewSubject, subjectrouting.IntentNewWork), nil
		}
		return subjectDecision(input, input.Candidates[0].ID, subjectrouting.IntentAmendRunning), nil
	}))
	var first *ManagerMessageResponse
	for index, content := range []string{"Build a launch website", "Add a contact page to it", "Also add a FAQ to it"} {
		result := subjectManagerRequest(t, server, "alice", ManagerMessageRequest{RequestID: uuid.NewString(), ProfileName: "manager", Content: content})
		if result.Status != "accepted" || result.Thread == nil || result.Turn == nil {
			t.Fatalf("message %d was not accepted: %+v", index, result)
		}
		if index == 0 {
			first = &result
			if result.Turn.Status != "queued" {
				t.Fatalf("first message status = %q", result.Turn.Status)
			}
		} else if result.Thread.ID != first.Thread.ID || result.Turn.Status != "waiting" {
			t.Fatalf("follow-up %d escaped its busy subject queue: %+v", index, result)
		}
	}
	turns, err := server.chatStore.ListTurns(context.Background(), "agents", first.Thread.ID)
	if err != nil || len(turns) != 3 || subjectRunCount(t, server) != 1 {
		t.Fatalf("follow-ups started competing executions: turns=%+v error=%v", turns, err)
	}
}

// Valid JSON can carry a full 64 KiB message without escaping HTML characters.
// Receipt encoding expands those characters; acceptance must remain durable and
// replayable after the outbox has already queued the permitted message.
func TestSubjectManagerPersistsEscapedMaximumMessage(t *testing.T) {
	server := subjectTestServer(t)
	subject := seedPrivateSubject(t, server, "Markup review")
	body := ManagerMessageRequest{RequestID: uuid.NewString(), ProfileName: "manager", SubjectID: subject.ID, Content: strings.Repeat("<", 64*1024)}
	var raw bytes.Buffer
	encoder := json.NewEncoder(&raw)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(body); err != nil {
		t.Fatal(err)
	}
	post := func() ManagerMessageResponse {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/namespaces/agents/chat/manager/messages", bytes.NewReader(raw.Bytes()))
		request.Header.Set("Authorization", "Bearer alice")
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		server.routes().ServeHTTP(response, request)
		if response.Code != http.StatusOK && response.Code != http.StatusAccepted {
			t.Fatalf("maximum message HTTP %d", response.Code)
		}
		var result ManagerMessageResponse
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	accepted := post()
	if accepted.Turn == nil || subjectRunCount(t, server) != 1 {
		t.Fatal("maximum message did not reach the execution outbox")
	}
	if accepted.Status != "accepted" {
		t.Fatalf("accepted execution lacked durable receipt: status=%s error=%s", accepted.Status, accepted.Error)
	}
	stored, err := server.chatStore.(chat.SubjectStore).GetRoutingRequest(context.Background(), "agents", "owner-alice", body.RequestID)
	if err != nil || stored.Status != "accepted" || len(stored.Result) <= 256*1024 {
		t.Fatalf("expanded receipt was not persisted: status=%s bytes=%d error=%v", stored.Status, len(stored.Result), err)
	}
	replay := post()
	if replay.Status != "accepted" || replay.Turn == nil || replay.Turn.ID != accepted.Turn.ID || replay.User == nil || replay.User.Content != body.Content || subjectRunCount(t, server) != 1 {
		t.Fatal("maximum message replay lost content or duplicated execution")
	}
}

// A browser may reconnect after the initial routing receipt is saved but before
// a dispatch target is frozen. The persisted manual selection remains binding.
func TestSubjectManagerRecoversInitialManualSelection(t *testing.T) {
	server := subjectTestServer(t)
	chosen := seedPrivateSubject(t, server, "Launch website")
	other := seedPrivateSubject(t, server, "Documentation")
	server.SetSubjectDecisionProvider(subjectDecisionFunc(func(_ context.Context, input subjectrouting.Input) (subjectrouting.Decision, error) {
		return subjectDecision(input, other.ID, subjectrouting.IntentStatus), nil
	}))
	body := ManagerMessageRequest{RequestID: uuid.NewString(), ProfileName: "manager", Content: "Please continue with this work"}
	hash := sha256.Sum256([]byte(body.ProfileName + "\x00" + body.Content))
	_, err := server.chatStore.(chat.SubjectStore).SaveRoutingRequest(context.Background(), chat.RoutingRequest{
		ID: body.RequestID, Namespace: "agents", Owner: "owner-alice", RequestHash: fmt.Sprintf("%x", hash),
		Content: body.Content, ProfileName: body.ProfileName, Status: "routing", RequestedSubjectID: chosen.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	// SubjectID is absent, as on recovery after a page reload.
	recovered := subjectManagerRequest(t, server, "alice", body)
	if recovered.Status != "accepted" || recovered.Subject == nil || recovered.Subject.ID != chosen.ID || recovered.Thread == nil || recovered.Thread.ID != chosen.ThreadID || recovered.Turn == nil {
		t.Fatalf("recovery forgot the initial manual selection: status=%s subject=%+v thread=%+v", recovered.Status, recovered.Subject, recovered.Thread)
	}
	otherTurns, err := server.chatStore.ListTurns(context.Background(), "agents", other.ThreadID)
	if err != nil || len(otherTurns) != 0 || subjectRunCount(t, server) != 1 {
		t.Fatalf("recovery routed into classifier alternative: turns=%+v error=%v", otherTurns, err)
	}
}
