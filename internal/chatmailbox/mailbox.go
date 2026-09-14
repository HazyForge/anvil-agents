// Package chatmailbox is the queue / steer / interrupt contract for live chat
// sessions. It does not talk to Postgres, Kubernetes, or Desktop.
//
// Teachings taken from Agent Substrate (identity independent of the worker,
// bounded parking, name the actor not the worker) without copying their
// suspend/resume dataplane. Fire-and-forget AgentRuns never participate.
package chatmailbox

import "strings"

const (
	// PurposeInteractive is the only purpose that may receive mailbox rows.
	// The CRD enum lives on a separate contract slice; this package compares
	// the string so fire-and-forget stays locked without that merge.
	PurposeInteractive = "interactive"

	ThreadLabel      = "control.anvil.hazyforge.io/chat-thread"
	SessionLabel     = "control.anvil.hazyforge.io/chat-session"
	CouncilLabel     = "control.anvil.hazyforge.io/council"
	CouncilRoleLabel = "control.anvil.hazyforge.io/council-role"

	ApplicationKeyPrefix = "chat:"

	EnvAgentRun    = "ANVIL_AGENT_RUN"
	EnvAgentRunUID = "ANVIL_AGENT_RUN_UID"
	EnvAgentRunNS  = "ANVIL_AGENT_RUN_NAMESPACE"
	EnvChatThread  = "ANVIL_CHAT_THREAD"
	EnvChatSession = "ANVIL_CHAT_SESSION"
	EnvChatCouncil = "ANVIL_CHAT_COUNCIL"
	EnvChatRole    = "ANVIL_CHAT_COUNCIL_ROLE"
)

// Kind is a mailbox row type. interrupt is stored; it does not stop a
// generation unless a harness adapter hook exists.
type Kind string

const (
	KindUtterance Kind = "utterance"
	KindInterrupt Kind = "interrupt"
	KindStatus    Kind = "status"
	KindDelivery  Kind = "delivery"
)

// Audience is who may ingest a row. Direct mail to another member is not in
// this member's inbox.
type Audience string

const (
	AudienceAll    Audience = "all"
	AudienceMember Audience = "member"
)

const (
	AuthorHuman  = "human"
	AuthorMember = "member"
	AuthorSystem = "system"
	RoleUser     = "user"
)

// MayReceiveMailbox is true only for live chat-session Jobs.
func MayReceiveMailbox(purpose string) bool {
	return strings.TrimSpace(purpose) == PurposeInteractive
}

// ApplicationKey is the dedicated AgentRunControl key so interactive sessions
// do not share concurrency with production work.
func ApplicationKey(namespace, councilName string) string {
	ns := strings.TrimSpace(namespace)
	council := strings.TrimSpace(councilName)
	if ns == "" || council == "" {
		return ""
	}
	return ApplicationKeyPrefix + ns + "/" + council
}

// IsApplicationKey reports whether name is reserved for chat sessions.
func IsApplicationKey(name string) bool {
	return strings.HasPrefix(strings.TrimSpace(name), ApplicationKeyPrefix)
}

// FlightKey collapses concurrent steers for the same member onto one
// session. It must not be used to spawn a second AgentRun.
func FlightKey(namespace, threadID, profileName string) string {
	return strings.TrimSpace(namespace) + "/" + strings.TrimSpace(threadID) + "/" + strings.TrimSpace(profileName)
}

// InInbox reports whether a member with profile selfProfile may ingest this
// addressed row. The operator room lists every message; this is not that.
func InInbox(audience Audience, audienceProfile, selfProfile string) bool {
	self := strings.TrimSpace(selfProfile)
	if self == "" {
		return false
	}
	switch audience {
	case AudienceAll, "":
		return true
	case AudienceMember:
		return strings.TrimSpace(audienceProfile) == self
	default:
		return false
	}
}

// AcceptHumanAppend is the HTTP spoof rule: humans may only write user
// utterance or interrupt. Member/system/assistant rows are not this path.
func AcceptHumanAppend(authorKind, role string, kind Kind) string {
	if authorKind != AuthorHuman {
		return "humans may only append author_kind=human"
	}
	if role != RoleUser {
		return "humans may only append role=user"
	}
	switch kind {
	case KindUtterance, KindInterrupt, "":
		return ""
	default:
		return "humans may only append kind utterance or interrupt"
	}
}

var reservedIdentityEnv = map[string]struct{}{
	EnvAgentRun:    {},
	EnvAgentRunUID: {},
	EnvAgentRunNS:  {},
	EnvChatThread:  {},
	EnvChatSession: {},
	EnvChatCouncil: {},
	EnvChatRole:    {},
}

// ReservedIdentityEnv reports names the controller writes and extraEnv must
// not spoof (Substrate: actors do not mint or rewrite their identity).
func ReservedIdentityEnv(name string) bool {
	_, ok := reservedIdentityEnv[strings.TrimSpace(name)]
	return ok
}
