package runapi

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/types"

	agentsv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
	"github.com/hazyforge/anvil-agents/internal/standing"
)

func TestStandingReplyDefersWork(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		reply string
		want  bool
	}{
		{
			name:  "live github preamble",
			reply: "I'll check what GitHub access is actually available in this session.",
			want:  true,
		},
		{
			name:  "let me look",
			reply: "Let me look at the configured tools.",
			want:  true,
		},
		{
			name:  "complete greeting",
			reply: "Doing well — still here if you want to actually start something.",
			want:  false,
		},
		{
			name:  "capability list",
			reply: "I can take on a pretty wide range of work. A few concrete examples:\n**Code**\n- Write or refactor a feature",
			want:  false,
		},
		{
			name:  "promise plus finding",
			reply: "I'll check the docs — GitHub CLI is not on PATH.",
			want:  false,
		},
		{
			name:  "direct access answer",
			reply: "This standing process has the grok CLI only; GitHub CLI is not on PATH.",
			want:  false,
		},
		{
			name:  "empty",
			reply: "   ",
			want:  false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := standingReplyDefersWork(tc.reply); got != tc.want {
				t.Fatalf("standingReplyDefersWork(%q) = %v, want %v", tc.reply, got, tc.want)
			}
		})
	}
}

// deferredThenAnswerRunner replays the live 15:17Z failure: first stdout is
// only the "I'll check" preamble, second is the actual session result. The
// second call blocks until releaseSecond so tests can observe that Done is
// withheld while the check is still open.
type deferredThenAnswerRunner struct {
	mu            sync.Mutex
	prompts       []string
	firstEmitted  chan struct{}
	releaseSecond chan struct{}
}

func newDeferredThenAnswerRunner() *deferredThenAnswerRunner {
	return &deferredThenAnswerRunner{
		firstEmitted:  make(chan struct{}),
		releaseSecond: make(chan struct{}),
	}
}

func (r *deferredThenAnswerRunner) Run(ctx context.Context, harnessKind, prompt string, emit func(chunk string) error) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	r.mu.Lock()
	r.prompts = append(r.prompts, prompt)
	call := len(r.prompts)
	r.mu.Unlock()
	if call == 1 {
		text := "I'll check what GitHub access is actually available in this session.\n"
		if emit != nil {
			if err := emit(text); err != nil {
				return "", err
			}
		}
		close(r.firstEmitted)
		return text, nil
	}
	select {
	case <-r.releaseSecond:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	text := "This standing process has the grok CLI only; GitHub CLI is not on PATH.\n"
	if emit != nil {
		if err := emit(text); err != nil {
			return "", err
		}
	}
	return text, nil
}

func TestStandingDeferredCheckCompletesSameTurn(t *testing.T) {
	ctx := context.Background()
	server := chatTestServer(t, true)
	standingHarness(t, server, "standing-harness", agentsv1alpha1.AgentRunHarnessBackendGrokBuild)
	runner := newDeferredThenAnswerRunner()
	backend := standing.NewProcessBackend(runner)
	enableStandingLive(t, server, backend)
	thread := standingThread(t, server, "standing-harness")

	events, unsubscribe := server.standingHub.subscribe("agents", thread.ID)
	defer unsubscribe()
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(runner.releaseSecond) }) }
	defer release()

	done := make(chan error, 1)
	go func() {
		_, err := server.queueChatTurn(ctx, "agents", thread.ID, AppendChatMessageRequest{
			Content: "do you have access to github and which ?",
		})
		done <- err
	}()

	select {
	case <-runner.firstEmitted:
	case <-time.After(5 * time.Second):
		t.Fatal("first standing process never emitted the preamble")
	}
	select {
	case event := <-events:
		if event.Done {
			t.Fatalf("Done published after preamble %+v, want the stream to stay open for continuation", event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("preamble token did not reach the hub")
	}
	select {
	case event := <-events:
		if event.Done {
			t.Fatalf("Done published before continuation %+v", event)
		}
	case <-time.After(200 * time.Millisecond):
	}
	release()

	var acceptedErr error
	select {
	case acceptedErr = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("standing turn did not finish after continuation")
	}
	if acceptedErr != nil {
		t.Fatal(acceptedErr)
	}

	turns, err := server.chatStore.ListTurns(ctx, "agents", thread.ID)
	if err != nil || len(turns) != 1 || turns[0].Status != "succeeded" {
		t.Fatalf("turns = %#v %v, want one succeeded turn", turns, err)
	}
	messages, err := server.chatStore.ListMessages(ctx, "agents", thread.ID)
	if err != nil || len(messages) != 2 {
		t.Fatalf("messages = %#v %v, want user + assistant", messages, err)
	}
	if !strings.Contains(messages[1].Content, "GitHub CLI is not on PATH") {
		t.Fatalf("assistant = %q, want the continuation result on the same turn", messages[1].Content)
	}
	if !strings.Contains(messages[1].Content, "I'll check") {
		t.Fatalf("assistant = %q, want the preamble kept with the result", messages[1].Content)
	}
	run := &agentsv1alpha1.AgentRun{}
	if err := server.writes.Get(ctx, types.NamespacedName{Namespace: "agents", Name: turns[0].RunName}, run); err != nil {
		t.Fatal(err)
	}
	if run.Status.Phase != agentsv1alpha1.AgentRunPhaseSucceeded {
		t.Fatalf("run phase = %q, want Succeeded once", run.Status.Phase)
	}
	if !strings.Contains(run.Status.Output, "GitHub CLI is not on PATH") {
		t.Fatalf("run output = %q, want the continuation persisted on the same AgentRun", run.Status.Output)
	}
	runner.mu.Lock()
	calls := len(runner.prompts)
	secondPrompt := ""
	if calls > 1 {
		secondPrompt = runner.prompts[1]
	}
	runner.mu.Unlock()
	if calls != 2 {
		t.Fatalf("process calls = %d, want preamble then continuation on the same session", calls)
	}
	if !strings.Contains(secondPrompt, "STANDING_TURN_CONTINUE") || !strings.Contains(secondPrompt, "I'll check") {
		t.Fatalf("continuation prompt = %q, want the deferred reply folded back in", secondPrompt)
	}
	if backend.Created() != 1 {
		t.Fatalf("sessions = %d, want one standing session", backend.Created())
	}

	sawDone := false
	deadline := time.Now().Add(2 * time.Second)
	for !sawDone {
		select {
		case event := <-events:
			if event.Done {
				sawDone = true
			}
		case <-time.After(time.Until(deadline)):
			t.Fatal("Done never published after the continuation")
		}
	}
}

func TestStandingCompleteReplyDoesNotContinue(t *testing.T) {
	ctx := context.Background()
	server := chatTestServer(t, true)
	standingHarness(t, server, "standing-harness", agentsv1alpha1.AgentRunHarnessBackendGrokBuild)
	var calls int
	runner := standing.RunnerFunc(func(ctx context.Context, _, prompt string, emit func(string) error) (string, error) {
		calls++
		text := "Doing well — still here if you want to actually start something.\n"
		if emit != nil {
			_ = emit(text)
		}
		return text, nil
	})
	enableStandingLive(t, server, standing.NewProcessBackend(runner))
	thread := standingThread(t, server, "standing-harness")
	accepted, err := server.queueChatTurn(ctx, "agents", thread.ID, AppendChatMessageRequest{Content: "how are you doing ?"})
	if err != nil {
		t.Fatal(err)
	}
	if accepted.Turn.Status != "succeeded" {
		t.Fatalf("status = %q, want succeeded", accepted.Turn.Status)
	}
	if calls != 1 {
		t.Fatalf("process calls = %d, want a single StreamTurn for a complete reply", calls)
	}
	messages, err := server.chatStore.ListMessages(ctx, "agents", thread.ID)
	if err != nil || len(messages) != 2 || !strings.Contains(messages[1].Content, "Doing well") {
		t.Fatalf("messages = %#v %v", messages, err)
	}
}

func TestStandingTurnPromptCarriesFinishNowContract(t *testing.T) {
	ctx := context.Background()
	server := chatTestServer(t, true)
	standingHarness(t, server, "standing-harness", agentsv1alpha1.AgentRunHarnessBackendGrokBuild)
	var prompt string
	runner := standing.RunnerFunc(func(ctx context.Context, _, frozen string, emit func(string) error) (string, error) {
		prompt = frozen
		text := "pong-standing\n"
		if emit != nil {
			_ = emit(text)
		}
		return text, nil
	})
	enableStandingLive(t, server, standing.NewProcessBackend(runner))
	thread := standingThread(t, server, "standing-harness")
	if _, err := server.queueChatTurn(ctx, "agents", thread.ID, AppendChatMessageRequest{Content: "ping"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "STANDING_TURN:") || !strings.Contains(prompt, "Never end with a promise") {
		t.Fatalf("frozen prompt = %q, want the standing finish-now contract", prompt)
	}
	if !strings.Contains(prompt, "CONVERSATION_JSON:") || !strings.Contains(prompt, `"ping"`) {
		t.Fatalf("frozen prompt = %q, want conversation JSON preserved", prompt)
	}
}
