package chatmailbox

import (
	"fmt"
	"strings"
)

const (
	ReasonMailboxOnFireAndForget  = "ChatMailboxOnFireAndForget"
	ReasonChatApplicationReserved = "ChatApplicationKeyReserved"
	ReasonChatApplicationRequired = "ChatApplicationKeyRequired"
	ReasonChatIdentityIncomplete  = "ChatSessionIdentityRequired"
	ReasonChatRoleWithoutCouncil  = "ChatCouncilRoleWithoutCouncil"
	ReasonReservedIdentityEnv     = "ReservedIdentityEnvironment"
)

// RunView is the slice of an AgentRun this package may see. No kube types.
type RunView struct {
	Purpose         string
	Namespace       string
	ApplicationName string
	Labels          map[string]string
}

// ValidateRun fails closed when fire-and-forget work tries to join chat, or
// when a live session is missing identity / the dedicated application key.
func ValidateRun(run RunView) (reason, message string) {
	labels := run.Labels
	thread := strings.TrimSpace(labels[ThreadLabel])
	session := strings.TrimSpace(labels[SessionLabel])
	council := strings.TrimSpace(labels[CouncilLabel])
	role := strings.TrimSpace(labels[CouncilRoleLabel])
	hasChatLabel := thread != "" || session != "" || council != "" || role != ""
	interactive := MayReceiveMailbox(run.Purpose)
	app := strings.TrimSpace(run.ApplicationName)

	if !interactive {
		if hasChatLabel {
			return ReasonMailboxOnFireAndForget, "chat mailbox labels are only valid on purpose=interactive AgentRuns; fire-and-forget Jobs never receive mailbox rows."
		}
		if IsApplicationKey(app) {
			return ReasonChatApplicationReserved, fmt.Sprintf("applicationRef %q is reserved for interactive chat sessions.", app)
		}
		return "", ""
	}

	if thread == "" || session == "" {
		return ReasonChatIdentityIncomplete, "purpose=interactive requires chat-thread and chat-session labels at create; they are never patched onto a live Job."
	}
	if role != "" && council == "" {
		return ReasonChatRoleWithoutCouncil, "council-role requires a council label."
	}
	if !IsApplicationKey(app) {
		return ReasonChatApplicationRequired, fmt.Sprintf("purpose=interactive must set applicationRef.name to a chat: key (want prefix %q), not production work.", ApplicationKeyPrefix)
	}
	if council != "" {
		want := ApplicationKey(run.Namespace, council)
		if want != "" && app != want {
			return ReasonChatApplicationRequired, fmt.Sprintf("purpose=interactive applicationRef.name %q must be %q for this council.", app, want)
		}
	}
	return "", ""
}

// ValidateExtraEnv rejects identity spoofing via harness extraEnv.
func ValidateExtraEnv(names []string) (reason, message string) {
	for _, name := range names {
		if ReservedIdentityEnv(name) {
			return ReasonReservedIdentityEnv, fmt.Sprintf("spec.harness.execution.extraEnv must not set reserved identity variable %q; the controller writes run identity from the AgentRun object.", strings.TrimSpace(name))
		}
	}
	return "", ""
}
