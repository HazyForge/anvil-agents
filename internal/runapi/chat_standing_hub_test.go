package runapi

import (
	"context"
	"testing"
	"time"

	agentsv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
	"github.com/hazyforge/anvil-agents/internal/standing"
)

// Slice-3 token hub tests: namespace/thread scoping, fan-out, unsubscribe,
// and the non-blocking guarantee that keeps a slow reader from stalling a
// streaming turn.

func TestStandingTokenHubPublishesToThreadSubscribers(t *testing.T) {
	hub := newStandingTokenHub()
	first, unsubFirst := hub.subscribe("agents", "thread-a")
	defer unsubFirst()
	second, unsubSecond := hub.subscribe("agents", "thread-a")
	defer unsubSecond()
	other, unsubOther := hub.subscribe("agents", "thread-b")
	defer unsubOther()

	event := standing.TokenEvent{ThreadID: "thread-a", TurnID: "turn-1", RunName: "chat-turn-1", Seq: 0, Token: "hello "}
	hub.publish("agents", event)

	for _, events := range []<-chan standing.TokenEvent{first, second} {
		select {
		case got := <-events:
			if got != event {
				t.Fatalf("hub event = %+v, want %+v", got, event)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("thread subscriber did not receive the published token")
		}
	}
	select {
	case got := <-other:
		t.Fatalf("other thread received %+v, want isolation", got)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestStandingTokenHubIsolatesNamespaces(t *testing.T) {
	hub := newStandingTokenHub()
	events, unsubscribe := hub.subscribe("agents", "shared-thread")
	defer unsubscribe()

	hub.publish("other", standing.TokenEvent{ThreadID: "shared-thread", TurnID: "turn-1", Token: "leak"})
	select {
	case got := <-events:
		t.Fatalf("cross-namespace event = %+v, want isolation", got)
	case <-time.After(100 * time.Millisecond):
	}

	hub.publish("agents", standing.TokenEvent{ThreadID: "shared-thread", TurnID: "turn-1", Token: "hello"})
	select {
	case got := <-events:
		if got.Token != "hello" {
			t.Fatalf("hub event = %+v, want the same-namespace token", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("same-namespace subscriber did not receive the published token")
	}
}

func TestStandingTokenHubUnsubscribeStopsDelivery(t *testing.T) {
	hub := newStandingTokenHub()
	events, unsubscribe := hub.subscribe("agents", "thread-a")
	unsubscribe()
	if got := hub.subscriberCount("agents", "thread-a"); got != 0 {
		t.Fatalf("subscribers = %d after unsubscribe, want 0", got)
	}
	hub.publish("agents", standing.TokenEvent{ThreadID: "thread-a", TurnID: "turn-1", Token: "late"})
	select {
	case got := <-events:
		t.Fatalf("unsubscribed channel received %+v", got)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestStandingTokenHubNeverBlocksPublisher(t *testing.T) {
	hub := newStandingTokenHub()
	events, unsubscribe := hub.subscribe("agents", "thread-a")
	defer unsubscribe()
	// Fill the subscriber buffer without draining: publish must still return
	// so a slow stream reader can never stall a streaming turn.
	for i := 0; i < standingTokenHubBuffer+10; i++ {
		done := make(chan struct{})
		go func() {
			hub.publish("agents", standing.TokenEvent{ThreadID: "thread-a", TurnID: "turn-1", Seq: i, Token: "x"})
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatalf("publish %d blocked on a slow subscriber", i)
		}
	}
	// The durable reply still lands in the turn record, so drops are safe;
	// the subscriber keeps whatever fit.
	drained := 0
	for {
		select {
		case <-events:
			drained++
		default:
			if drained == 0 {
				t.Fatal("slow subscriber kept nothing")
			}
			return
		}
	}
}

func TestStandingTokenHubNilSafe(t *testing.T) {
	var hub *standingTokenHub
	events, unsubscribe := hub.subscribe("agents", "thread-a")
	unsubscribe()
	hub.publish("agents", standing.TokenEvent{ThreadID: "thread-a", TurnID: "turn-1"})
	if got := hub.subscriberCount("agents", "thread-a"); got != 0 {
		t.Fatalf("nil hub subscribers = %d, want 0", got)
	}
	select {
	case _, ok := <-events:
		if ok {
			t.Fatal("nil hub subscription must be closed")
		}
	default:
		// Either closed or momentarily empty is fine; the contract is only
		// that nil hubs never block and never deliver.
		_ = events
	}
}

func TestStandingTurnPublishesStampedTokensToHub(t *testing.T) {
	ctx := context.Background()
	server := chatTestServer(t, true)
	standingHarness(t, server, "standing-harness", agentsv1alpha1.AgentRunHarnessBackendOpenCode)
	fake := standing.NewFakeBackend()
	enableStandingLive(t, server, fake)
	thread := standingThread(t, server, "standing-harness")

	events, unsubscribe := server.standingHub.subscribe("agents", thread.ID)
	defer unsubscribe()

	accepted, err := server.queueChatTurn(ctx, "agents", thread.ID, AppendChatMessageRequest{Content: "hello live tokens"})
	if err != nil {
		t.Fatal(err)
	}
	if accepted.Turn.Status != "succeeded" {
		t.Fatalf("turn status = %q, want succeeded", accepted.Turn.Status)
	}
	deadline := time.Now().Add(5 * time.Second)
	var received []standing.TokenEvent
	for {
		select {
		case event := <-events:
			received = append(received, event)
			if event.Done {
				goto done
			}
		case <-time.After(time.Until(deadline)):
			t.Fatalf("hub received %d events without a Done marker", len(received))
		}
	}
done:
	if len(received) < 2 {
		t.Fatalf("hub events = %d, want word tokens plus a Done marker", len(received))
	}
	for i, event := range received {
		if event.ThreadID != thread.ID || event.TurnID != accepted.Turn.ID || event.RunName != accepted.Turn.RunName {
			t.Fatalf("hub event %d = %+v, want the full turn identity stamped", i, event)
		}
		if event.Seq != i {
			t.Fatalf("hub event %d seq = %d, want ordered delivery", i, event.Seq)
		}
	}
}
