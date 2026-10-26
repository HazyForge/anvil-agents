package desktop

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/hazyforge/anvil-agents/internal/subjectrouting"
)

type loopAnswerFixture struct {
	entered chan subjectrouting.AnswerInput
	release chan struct{}
	calls   atomic.Int32
}

func (f *loopAnswerFixture) Decide(context.Context, subjectrouting.Input) (subjectrouting.Decision, error) {
	return subjectrouting.Decision{}, subjectrouting.ErrUnavailable
}
func (f *loopAnswerFixture) ResolveAnswer(ctx context.Context, in subjectrouting.AnswerInput) (subjectrouting.AnswerResolution, error) {
	f.calls.Add(1)
	select {
	case f.entered <- in:
	case <-ctx.Done():
		return subjectrouting.AnswerResolution{}, ctx.Err()
	}
	select {
	case <-f.release:
	case <-ctx.Done():
		return subjectrouting.AnswerResolution{}, ctx.Err()
	}
	confidence := .98
	return subjectrouting.AnswerResolution{Choice: "retry", Confidence: &confidence, Probabilities: map[string]float64{"retry": .98, "pause": .01, "cancel": 0, "unclear": .01}, Model: "answer-fixture"}, nil
}

func answerHTTP(s *Server, path string, body any) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:1738/local/v1/manager/loop"+path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, req)
	return response
}

// Owns the HTTP answer lifecycle: another tab can answer while the model is
// deciding. The policy tests do not exercise durable versions or request replay.
func TestLocalLoopAnswerCannotOverwriteAnotherTab(t *testing.T) {
	provider := &loopAnswerFixture{entered: make(chan subjectrouting.AnswerInput, 1), release: make(chan struct{})}
	server, _, _ := localManagerFixture(t, true, provider)
	defer server.Close()
	observed := answerHTTP(server, "/observe", loopObserveRequest{RequestID: uuid.NewString(), Conversation: loopConversation{ID: "failed-chat", Name: "Failed chat", Harness: "prime", Target: "native", Workdir: t.TempDir(), Messages: []loopMessage{{Role: "user", Content: "Create the report"}}}, LastError: "The previous turn failed."})
	if observed.Code != http.StatusOK {
		t.Fatalf("observe: %d %s", observed.Code, observed.Body.String())
	}
	var snapshot loopSnapshot
	if err := json.Unmarshal(observed.Body.Bytes(), &snapshot); err != nil || len(snapshot.Decisions) != 1 {
		t.Fatalf("observation did not create a question: %s", observed.Body.String())
	}
	d := snapshot.Decisions[0]
	answerID := uuid.NewString()
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- answerHTTP(server, "/decisions/"+d.ID+"/answer", map[string]any{"requestId": answerID, "version": d.Version, "content": "Attempt that report once more"})
	}()
	<-provider.entered
	pauseID := uuid.NewString()
	paused := answerHTTP(server, "/decisions/"+d.ID, map[string]any{"requestId": pauseID, "version": d.Version, "choice": "pause"})
	if paused.Code != http.StatusOK {
		close(provider.release)
		t.Fatalf("pause: %d %s", paused.Code, paused.Body.String())
	}
	close(provider.release)
	stale := <-done
	if stale.Code != http.StatusConflict {
		t.Fatalf("late model answer overwrote current choice: %d %s", stale.Code, stale.Body.String())
	}
	loop, err := server.getLoop()
	if err != nil {
		t.Fatal(err)
	}
	snapshot = loop.snapshot()
	if len(snapshot.Tasks) != 1 || snapshot.Tasks[0].State != "paused" || snapshot.Tasks[0].Attempt != 0 {
		t.Fatalf("stale answer started work: %#v", snapshot.Tasks)
	}
	// Delivery replay recovers the already recorded fixed choice without asking
	// the model again, even though the original decision version is now stale.
	replay := answerHTTP(server, "/decisions/"+d.ID+"/answer", map[string]any{"requestId": pauseID, "version": d.Version, "content": "leave it"})
	var reply struct {
		Resolution subjectrouting.AnswerResolution `json:"resolution"`
	}
	if replay.Code != http.StatusOK || json.Unmarshal(replay.Body.Bytes(), &reply) != nil || reply.Resolution.Choice != "pause" || reply.Resolution.Source != "recorded" || provider.calls.Load() != 1 {
		t.Fatalf("recorded choice not replayed: %d %s", replay.Code, replay.Body.String())
	}
}
