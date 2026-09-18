package standing

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

const (
	// sessionNamePrefix scopes every standing session name so chat sessions
	// never collide with other in-process tenants sharing a process.
	sessionNamePrefix = "standing-"
	// maxSessionNameLen keeps derived names inside the DNS-subdomain bound
	// without forcing callers to truncate, mirroring the Substrate actor
	// naming convention.
	maxSessionNameLen = 253
)

// SessionNameForThread returns the stable standing session name for a chat
// thread.
//
// Peer cooperation maps onto session resume through this convention: the chat
// API already creates one durable child thread per recipient profile with a
// deterministic ID and queues each delivery through the same turn path as a
// direct message. A future live backend therefore resumes the recipient's
// thread session (ResumeSession) instead of creating new execution identity
// per message, and suspends it when the turn goes idle. Deterministic
// delivery request IDs keep retried peer messages idempotent end to end, and
// the existing durable-wait semantics for busy recipients carry over
// unchanged.
//
// The mapping is total over thread IDs: any Wrapper, manager, or peer thread
// resolves to exactly one session name, so every interactive role is eligible
// for the standing plane through its own harness profile. Thread IDs are
// usually UUIDs (already DNS-safe); the sanitizer keeps the function total
// for any future ID shape.
func SessionNameForThread(threadID string) string {
	sanitized := sanitizeSessionNameSegment(strings.ToLower(strings.TrimSpace(threadID)))
	if sanitized == "" {
		sanitized = "unknown"
	}
	name := sessionNamePrefix + sanitized
	if len(name) <= maxSessionNameLen {
		return name
	}
	digest := sha256.Sum256([]byte(threadID))
	suffix := hex.EncodeToString(digest[:])[:12]
	keep := maxSessionNameLen - len(sessionNamePrefix) - len(suffix) - 1
	return sessionNamePrefix + sanitized[:keep] + "-" + suffix
}

func sanitizeSessionNameSegment(raw string) string {
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
