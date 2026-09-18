package v1alpha1

import "fmt"

// Chat session identity labels are set at AgentRun create. They are never
// patched onto a live Job. The controller does not watch the chat store.
const (
	AgentRunChatThreadLabel  = "control.anvil.hazyforge.io/chat-thread"
	AgentRunChatSessionLabel = "control.anvil.hazyforge.io/chat-session"
	AgentRunChatTurnLabel    = "control.anvil.hazyforge.io/chat-turn"
	AgentRunCouncilLabel     = "control.anvil.hazyforge.io/council"
	AgentRunCouncilRoleLabel = "control.anvil.hazyforge.io/council-role"
)

// AgentRunStandingClaimAnnotation marks an AgentRun whose standing
// in-process turn is owned by one API replica. The API stamps it (JSON
// turn/owner/timestamp, see internal/standing) before streaming a live
// standing turn; the controller respects it by yielding its InProcess hold
// with StandingClaimed instead of racing the stream with InProcessNotWired.
// Only one replica wins the claim per turn via optimistic-concurrency
// update; a stale claim (owner crash) may be taken over after ClaimTTL.
const AgentRunStandingClaimAnnotation = "control.anvil.hazyforge.io/standing-claim"

// ParticipatesInChatMailbox reports whether Jobs with this purpose may receive
// queued, steered, or interrupted chat lines. Fire-and-forget purposes never
// do. Built-in grok/codex/opencode CLIs still lack a generation interrupt hook.
func (p AgentRunPurpose) ParticipatesInChatMailbox() bool {
	return p == AgentRunPurposeInteractive
}

// PublicCreateError returns a rejection when this purpose must not be set by
// anvil-agentctl run create or POST /agent-runs. Empty means allowed.
func (p AgentRunPurpose) PublicCreateError() string {
	switch p {
	case AgentRunPurposeManual, AgentRunPurposeAdverseSituation, AgentRunPurposeScheduledHealthCheck:
		return ""
	case AgentRunPurposeChained:
		return `purpose "chained" is controller-only (AgentChain)`
	case AgentRunPurposeInteractive:
		return `purpose "interactive" is reserved for the chat session API`
	default:
		return fmt.Sprintf("invalid purpose %q", p)
	}
}
