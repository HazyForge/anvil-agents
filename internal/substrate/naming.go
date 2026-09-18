package substrate

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

const (
	// actorNamePrefix scopes every Anvil chat actor name so chat actors never
	// collide with other Substrate tenants sharing a pool.
	actorNamePrefix = "chat-"
	// maxActorNameLen keeps derived names inside the DNS-subdomain bound
	// without forcing callers to truncate.
	maxActorNameLen = 253
)

// ActorNameForThread returns the stable Substrate actor name for a
// standing-chat thread.
//
// Peer messaging maps onto actor resume through this convention: the chat API
// already creates one durable child thread per recipient profile with a
// deterministic ID and queues each delivery through the same turn path as a
// direct message. The future live backend therefore resumes the recipient's
// thread actor (ResumeActor) instead of creating new execution identity per
// message, and suspends it when the turn goes idle. Deterministic delivery
// request IDs keep retried peer messages idempotent end to end, and the
// existing durable-wait semantics for busy recipients carry over unchanged.
//
// The mapping is total over thread IDs: any Wrapper, manager, or peer thread
// resolves to exactly one actor name, so every interactive role is eligible
// for the warm-actor plane through its own harness profile. Thread IDs are
// usually UUIDs (already DNS-safe); the sanitizer keeps the function total
// for any future ID shape.
func ActorNameForThread(threadID string) string {
	sanitized := sanitizeActorNameSegment(strings.ToLower(strings.TrimSpace(threadID)))
	if sanitized == "" {
		sanitized = "unknown"
	}
	name := actorNamePrefix + sanitized
	if len(name) <= maxActorNameLen {
		return name
	}
	digest := sha256.Sum256([]byte(threadID))
	suffix := hex.EncodeToString(digest[:])[:12]
	keep := maxActorNameLen - len(actorNamePrefix) - len(suffix) - 1
	return actorNamePrefix + sanitized[:keep] + "-" + suffix
}

func sanitizeActorNameSegment(raw string) string {
	var out strings.Builder
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '.':
			out.WriteRune(r)
		default:
			out.WriteRune('-')
		}
	}
	return strings.Trim(out.String(), "-.")
}
