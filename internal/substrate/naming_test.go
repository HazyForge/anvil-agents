package substrate

import (
	"strings"
	"testing"
)

func TestActorNameForThreadIsStableAndScoped(t *testing.T) {
	t.Parallel()

	first := ActorNameForThread("thread-1")
	second := ActorNameForThread("thread-1")
	if first != second {
		t.Fatalf("actor name unstable: %q vs %q", first, second)
	}
	if !strings.HasPrefix(first, "chat-") {
		t.Fatalf("actor name %q must carry the chat scope prefix", first)
	}
	if first == ActorNameForThread("thread-2") {
		t.Fatal("distinct threads must resolve to distinct actors")
	}
}

func TestActorNameForThreadSanitizesPeerIDs(t *testing.T) {
	t.Parallel()

	// UUID child-thread IDs (the peer delivery shape) already pass through.
	uuid := "123e4567-e89b-12d3-a456-426614174000"
	if got := ActorNameForThread(uuid); got != "chat-"+uuid {
		t.Fatalf("uuid thread actor = %q", got)
	}
	// Arbitrary future ID shapes still resolve to one DNS-safe actor.
	got := ActorNameForThread("  Peer/Thread:Desktop Manager_01  ")
	for _, r := range got {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '.') {
			t.Fatalf("actor name %q contains non-DNS rune %q", got, r)
		}
	}
	if got != ActorNameForThread("peer/thread:desktop manager_01") {
		t.Fatalf("actor naming must be case/whitespace-insensitive: %q", got)
	}
	if ActorNameForThread("") == "" || ActorNameForThread("!!!") == "" {
		t.Fatal("empty/hostile thread IDs must still resolve to an actor")
	}
	long := strings.Repeat("a", 400)
	if got := ActorNameForThread(long); len(got) > 253 || got == ActorNameForThread(long+"b") {
		t.Fatalf("long thread ID must truncate deterministically, len=%d", len(got))
	}
}
