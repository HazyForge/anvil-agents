// Slice-4 multi-replica claim for API-owned standing turns.
//
// The chart runs one API replica today, and the in-process singleflight guard
// (standingTurnGuard) only serializes queue/read-refresh/recovery races inside
// one process. When several API replicas reconcile the same standing turn,
// exactly one of them may drive it; the others must keep today's hold
// behavior. The claim is the cross-replica arbiter, and the controller
// respects it by yielding its InProcess hold (StandingClaimed) instead of
// racing a live stream with InProcessNotWired.
//
// Contract:
//
//   - The API stamps AgentRunStandingClaimAnnotation on the turn's
//     append-only AgentRun before streaming, binding the durable turn ID,
//     the driving replica, and a timestamp (structured JSON, no secrets:
//     turn UUID, hostname/pid owner, Unix seconds).
//   - The winner is decided by Kubernetes optimistic concurrency: the
//     replica whose metadata Update lands first owns the turn. A lost update
//     (conflict) or an already-live foreign claim means another replica owns
//     it — keep hold behavior, never stream.
//   - Claims expire after ClaimTTL so an owner crash cannot wedge the turn:
//     any replica may overwrite a stale claim and re-stream (at-least-once,
//     matching the existing crash-recovery semantics), and the controller
//     falls back to InProcessNotWired for stale or mismatched claims.
//   - Gate off stays byte-identical: no claim is ever written when
//     standing.liveEnabled is off, so the controller never observes one and
//     Job-plane/scout paths are untouched.
//
// This file composes with every Backend (FakeBackend in tests, ProcessBackend
// live): claiming happens around StreamTurn, never inside it.
package standing

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	agentsv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

// ClaimTTL bounds how long one replica's standing-turn claim stays live. It
// comfortably exceeds a turn stream (seconds) and the chat recovery interval
// while letting a crashed owner's turn be taken over promptly.
const ClaimTTL = 90 * time.Second

// Claim is one API replica's ownership of a standing turn.
type Claim struct {
	// TurnID is the durable chat turn the claim binds (chat-turn label).
	TurnID string `json:"turn"`
	// Owner identifies the driving API replica (hostname/pid). Observability
	// only: correctness comes from the optimistic-concurrency write, never
	// from comparing owners.
	Owner string `json:"owner"`
	// AtUnix is the claim timestamp in Unix seconds.
	AtUnix int64 `json:"at"`
}

// Time reports the claim timestamp in UTC.
func (claim Claim) Time() time.Time {
	return time.Unix(claim.AtUnix, 0).UTC()
}

// Fresh reports whether the claim is still live at now: its timestamp must
// be present and younger than ttl. Malformed timestamps and non-positive
// TTLs fail closed (never fresh).
func (claim Claim) Fresh(now time.Time, ttl time.Duration) bool {
	if now.IsZero() || ttl <= 0 || claim.AtUnix <= 0 {
		return false
	}
	elapsed := now.Sub(claim.Time())
	if elapsed < 0 {
		// Small cross-replica clock skew must not read as expired.
		elapsed = 0
	}
	return elapsed < ttl
}

// EncodeClaim marshals a claim with structured encoding only, so owner or
// turn values containing quotes can never break the framing.
func EncodeClaim(claim Claim) (string, error) {
	if strings.TrimSpace(claim.TurnID) == "" {
		return "", fmt.Errorf("standing claim turn ID is required")
	}
	if strings.TrimSpace(claim.Owner) == "" {
		return "", fmt.Errorf("standing claim owner is required")
	}
	if claim.AtUnix <= 0 {
		return "", fmt.Errorf("standing claim timestamp is required")
	}
	raw, err := json.Marshal(claim)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// ParseClaim decodes a claim annotation value. Unknown fields are ignored
// for forward compatibility; missing or blank turn/owner/timestamp fail.
func ParseClaim(raw string) (Claim, error) {
	var claim Claim
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &claim); err != nil {
		return Claim{}, err
	}
	claim.TurnID = strings.TrimSpace(claim.TurnID)
	claim.Owner = strings.TrimSpace(claim.Owner)
	if claim.TurnID == "" {
		return Claim{}, fmt.Errorf("standing claim turn ID is required")
	}
	if claim.Owner == "" {
		return Claim{}, fmt.Errorf("standing claim owner is required")
	}
	if claim.AtUnix <= 0 {
		return Claim{}, fmt.Errorf("standing claim timestamp is required")
	}
	return claim, nil
}

// ClaimForTurn returns the live claim binding turnID, or false when the
// annotations carry no fresh claim for that turn (absent, malformed,
// bound to another turn, or stale). Both the API (drive/takeover) and the
// controller (hold yield) key off this one predicate so they can never
// disagree about who owns a turn.
func ClaimForTurn(annotations map[string]string, turnID string, now time.Time, ttl time.Duration) (Claim, bool) {
	turnID = strings.TrimSpace(turnID)
	if turnID == "" || len(annotations) == 0 {
		return Claim{}, false
	}
	raw := strings.TrimSpace(annotations[agentsv1alpha1.AgentRunStandingClaimAnnotation])
	if raw == "" {
		return Claim{}, false
	}
	claim, err := ParseClaim(raw)
	if err != nil {
		return Claim{}, false
	}
	if claim.TurnID != turnID || !claim.Fresh(now, ttl) {
		return Claim{}, false
	}
	return claim, true
}

// NewOwnerID identifies one API process for claim ownership. Hostname plus
// pid distinguishes replicas for observability; restarts may reuse the value,
// which is safe because winning is decided by the annotation write, and a
// restarted process re-driving its own live claim is exactly what crash
// recovery wants.
func NewOwnerID() string {
	host, err := os.Hostname()
	if host = strings.TrimSpace(host); err != nil || host == "" {
		host = "unknown-host"
	}
	return fmt.Sprintf("%s/%d", host, os.Getpid())
}
