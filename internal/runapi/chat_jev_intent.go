package runapi

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/hazyforge/anvil-agents/internal/chat"
	"github.com/hazyforge/anvil-agents/internal/jev"
)

// Jev intent classification on the chat-append path.
//
// When the opt-in gate is on (chat.jevIntentEnabled config or the
// ANVIL_AGENTS_JEV_INTENT environment variable, plus an attached Jev
// backend), queueChatTurnAttempt classifies the incoming message before
// buildChatPrompt freezes the execution intent. Classification observes the
// message; it never rewrites the durable turn. Every failure mode — gate
// off, no backend (missing TYPESAFE_API_KEY), Jev error — falls back to
// today's behavior (chat_reply, unchanged prompt, no intent metadata) and
// never fails the turn. Jev never generates chat text; the harness chat
// model behind the standing session still generates exactly as today.
// create-agent stays Wrapper/manager-only: the create_agent_request hint
// names the concrete peer-safe request shape (requestPeer STATUS_JSON with
// request=create-agent, mirroring skills/create-agent/SKILL.md and the
// controller's injected skill content) and the turn carries a structured
// request flag (user-message jevNeedsManagerCreate plus the
// jev-needs-manager-create AgentRun annotation) for a Wrapper/manager to
// fulfill later — never peer creation. The peer_handoff hint mirrors the
// same conventions with the existing-peer requestPeer STATUS_JSON shape
// (action=requestPeer with peerProfileName/summary, mirroring
// web/desktop/src/wrapper/requestPeer.ts and the controller's
// requestPeer decision parsing) and the turn carries its own structured
// request flag (user-message jevNeedsPeerHandoff plus the
// jev-needs-peer-handoff AgentRun annotation) — still coordination only,
// never creation. See
// docs/jev-intent-routing.md.

// JevIntentEnvVar enables chat.jevIntentEnabled from the process
// environment. It can enable the config-file flag but never disables it,
// mirroring ANVIL_AGENTS_STANDING_LIVE.
const JevIntentEnvVar = "ANVIL_AGENTS_JEV_INTENT"

// JevModelEnvVar overrides chat.jevModel from the process environment when
// non-empty (after trimming). Empty leaves the config-file value untouched,
// so the default stays the jev-latest alias.
const JevModelEnvVar = "ANVIL_AGENTS_JEV_MODEL"

// Annotation keys recording the routing decision on the turn's AgentRun for
// kubectl-visible observability. The user message metadata carries the same
// decision plus confidence detail; see chatAuthorMetadataWithIntent.
// jevNeedsManagerCreateAnnotation marks the one actionable fulfillment case:
// a classified, non-unclear create_agent_request. It is a request flag for a
// Wrapper/manager to fulfill through the existing manager-authorization
// path — never a grant for this peer to create. Desktop Wrapper may watch
// it later; peers must still request via requestPeer STATUS_JSON.
// jevNeedsPeerHandoffAnnotation marks the actionable handoff case: a
// classified, non-unclear peer_handoff. It is a request flag for the
// harness to fulfill through the existing coordination contract
// (coordination JSON in chat, requestPeer STATUS_JSON on the runner path)
// — never a grant to create agents or to send outside existing paths.
const (
	jevIntentAnnotation             = "control.anvil.hazyforge.io/jev-intent"
	jevConfidenceAnnotation         = "control.anvil.hazyforge.io/jev-confidence"
	jevModelAnnotation              = "control.anvil.hazyforge.io/jev-model"
	jevNeedsManagerCreateAnnotation = "control.anvil.hazyforge.io/jev-needs-manager-create"
	jevNeedsPeerHandoffAnnotation   = "control.anvil.hazyforge.io/jev-needs-peer-handoff"
	jevCreateAgentStatusJSONExample = `ANVIL_AGENT_RUN_STATUS_JSON={"type":"decision","action":"requestPeer","request":"create-agent","name":"<dns-label>","description":"<why>","peerProfileName":"desktop-manager"}`
	jevPeerHandoffStatusJSONExample = `ANVIL_AGENT_RUN_STATUS_JSON={"type":"decision","action":"requestPeer","peerProfileName":"<existing-profile>","summary":"<why>"}`
)

// jevClassifyTimeout bounds one routing decision. System One answers in
// ~70-500ms; the cap only guards a wedged transport so a slow Jev call can
// never stall the chat-append path past falling back.
const jevClassifyTimeout = 10 * time.Second

// SetJevBackend attaches the Jev backend used for chat-turn intent
// classification. Nil (the default) keeps today's behavior even with the
// gate on. Tests drive jev.FakeBackend / jev.KeywordFakeBackend (no
// network); production attaches jev.ClientFromEnv when TYPESAFE_API_KEY is
// set.
func (server *Server) SetJevBackend(backend jev.Backend) {
	if server == nil {
		return
	}
	server.jev = backend
}

// jevIntentEnabled reports whether intent classification may run. Both the
// explicit opt-in flag and an attached backend are required; anything else
// keeps today's behavior unchanged.
func (server *Server) jevIntentEnabled() bool {
	return server != nil && server.config.Chat.JevIntentEnabled && server.jev != nil
}

// jevModel returns the configured Jev serving model for the chat turn
// path. Empty (the default) means the Router falls back to the jev-latest
// alias (jev.DefaultModel); a versioned ID (e.g. "jev-1.13.0") pins
// classification to that serving model.
func (server *Server) jevModel() string {
	if server == nil {
		return ""
	}
	return strings.TrimSpace(server.config.Chat.JevModel)
}

// classifyChatIntent routes one incoming message through the Jev intent
// router. It returns classified=false on every fallback path (gate off, no
// backend, Jev error) with a chat_reply decision carrying no confidence, so
// callers keep today's prompt and metadata byte-identical without
// branching on errors. Failures are logged, never returned.
func (server *Server) classifyChatIntent(ctx context.Context, content string, messages []chat.Message) (jev.Decision, bool) {
	fallback := jev.Decision{Intent: jev.IntentChatReply, RawChoice: jev.IntentChatReply}
	if !server.jevIntentEnabled() {
		return fallback, false
	}
	recent := make([]string, 0, 6)
	for _, message := range messages {
		if message.Role != chat.RoleUser && message.Role != chat.RoleAssistant {
			continue
		}
		trimmed := strings.TrimSpace(message.Content)
		if trimmed == "" {
			continue
		}
		recent = append(recent, trimmed)
	}
	if len(recent) > 6 {
		recent = recent[len(recent)-6:]
	}
	classifyCtx, cancel := context.WithTimeout(ctx, jevClassifyTimeout)
	defer cancel()
	router := &jev.Router{Backend: server.jev, Model: server.jevModel()}
	decision, err := router.ClassifyIntent(classifyCtx, jev.MessageContext{Message: content, Recent: recent})
	if err != nil {
		server.log.Info("jev intent classification failed; keeping today's chat behavior", "error", err.Error())
		return fallback, false
	}
	return decision, true
}

// jevNeedsManagerCreate reports the one actionable fulfillment case: a
// classified, non-unclear create_agent_request. Gated (unclear) turns,
// unclassified fallbacks, and every other intent return false, so unclear
// never produces a fulfillment signal. The flag is request-only — the
// existing manager-authorization path (controller create-agent skill
// injection/stripping plus Desktop isCreateAgentPrincipal) still owns the
// decision, and peers never gain create authority from it.
func jevNeedsManagerCreate(decision jev.Decision, classified bool) bool {
	return classified && !decision.Unclear && decision.Intent == jev.IntentCreateAgentRequest
}

// jevNeedsPeerHandoff reports the actionable handoff case: a classified,
// non-unclear peer_handoff. Gated (unclear) turns, unclassified fallbacks,
// and every other intent return false, so unclear never produces a
// fulfillment signal. The flag is request-only — fulfillment still goes
// through the existing coordination contract (coordination JSON in chat,
// requestPeer STATUS_JSON on the runner path), and it never authorizes
// agent creation or sends outside existing paths.
func jevNeedsPeerHandoff(decision jev.Decision, classified bool) bool {
	return classified && !decision.Unclear && decision.Intent == jev.IntentPeerHandoff
}

// jevIntentPromptHint renders the minimal per-intent routing hint. Empty
// means unchanged behavior: chat_reply takes today's prompt path, and the
// fallback (unclassified) path adds no hint either. Hints observe the
// existing contracts — coordination JSON, manager authorization, harness
// tools — and never grant new authority.
func jevIntentPromptHint(decision jev.Decision, classified bool) string {
	if !classified || decision.Intent == jev.IntentChatReply {
		return ""
	}
	provenance := fmt.Sprintf(" (Jev intent %s, confidence %.2f, model %s)", decision.Intent, decision.Confidence, decision.Model)
	switch decision.Intent {
	case jev.IntentCreateAgentRequest:
		return "\nROUTING_HINT" + provenance + ": the author may be asking for a NEW agent. Do NOT create, spawn, provision, or claim to have created an agent from this peer path — create-agent stays Wrapper/manager-only. Peer-safe fulfillment is request-only through the existing runner STATUS_JSON path so a Wrapper/manager can fulfill (the controller strips create-agent from peers; Desktop refuses peer creation): " + jevCreateAgentStatusJSONExample + " Fill name/description from the request; otherwise reply explaining that creating an agent requires a manager and ask what the new agent should do.\n"
	case jev.IntentPeerHandoff:
		return "\nROUTING_HINT" + provenance + ": the author may want handoff to another EXISTING agent or peer — never a new agent. Coordinate only through the existing coordination contract: in chat, the coordination JSON this thread's config allows (messages to an allowed profile); on the runner path, the existing requestPeer STATUS_JSON shape so delivery stays inside existing paths: " + jevPeerHandoffStatusJSONExample + " Fill peerProfileName with the existing target profile and summary from the request. Never create, spawn, or provision an agent for a handoff, and never send outside existing paths. If coordination is not enabled on this thread, answer directly or explain the handoff needs an enabled coordination target.\n"
	case jev.IntentToolRun:
		return "\nROUTING_HINT" + provenance + ": the reply may depend on a tool, command, lookup, query, build, or deploy result. Prefer tool-first behavior: if the harness offers a tool step, run it before finalizing the reply; otherwise note what lookup is needed instead of guessing the result.\n"
	default:
		return "\nROUTING_HINT" + provenance + ": the request is ambiguous or low-confidence (unclear). Ask a brief clarifying question before acting; do not take destructive, delegating, or agent-creating actions on this turn.\n"
	}
}

// buildChatPromptWithIntent freezes the execution intent with the Jev
// routing hint folded in. Unclassified turns and chat_reply are today's
// prompt byte-identical: the hint is empty and this delegates to
// buildChatPrompt untouched.
func buildChatPromptWithIntent(thread chat.Thread, messages []chat.Message, content string, decision jev.Decision, classified bool) (string, error) {
	prompt, err := buildChatPrompt(thread, messages, content)
	if err != nil {
		return "", err
	}
	hint := jevIntentPromptHint(decision, classified)
	if hint == "" {
		return prompt, nil
	}
	const marker = "CONVERSATION_JSON:\n"
	if before, after, ok := strings.Cut(prompt, marker); ok {
		return before + hint + marker + after, nil
	}
	return prompt + hint, nil
}

// chatAuthorMetadataWithIntent merges the routing decision into the queued
// user message metadata so intent + confidence + serving model persist
// alongside the turn for threshold tuning. Unclassified turns return
// today's metadata byte-identical. Classified, non-unclear
// create_agent_request turns additionally carry jevNeedsManagerCreate=true:
// the structured outbox hint a Wrapper/manager harness can act on later
// (Desktop Wrapper fulfillment via executeCreateAgent). Classified,
// non-unclear peer_handoff turns additionally carry
// jevNeedsPeerHandoff=true: the structured outbox hint a harness can act
// on later through the existing coordination contract. Both grant no
// authority — peers still request via requestPeer STATUS_JSON, and neither
// flag authorizes creation.
func chatAuthorMetadataWithIntent(thread chat.Thread, deferred bool, decision jev.Decision, classified bool) json.RawMessage {
	base := chatAuthorMetadata(thread, deferred)
	if !classified {
		return base
	}
	var metadata map[string]any
	if err := json.Unmarshal(base, &metadata); err != nil || metadata == nil {
		metadata = map[string]any{}
	}
	metadata["jevIntent"] = decision.Intent
	metadata["jevRawChoice"] = decision.RawChoice
	metadata["jevConfidence"] = decision.Confidence
	metadata["jevModel"] = decision.Model
	metadata["jevUnclear"] = decision.Unclear
	if jevNeedsManagerCreate(decision, classified) {
		metadata["jevNeedsManagerCreate"] = true
	}
	if jevNeedsPeerHandoff(decision, classified) {
		metadata["jevNeedsPeerHandoff"] = true
	}
	raw, err := json.Marshal(metadata)
	if err != nil {
		return base
	}
	return raw
}

// annotateChatRunWithIntent stamps the routing decision on the turn's
// AgentRun for kubectl-visible observability. Unclassified turns leave the
// run untouched. Classified, non-unclear create_agent_request turns
// additionally carry the jev-needs-manager-create=true annotation so a
// Wrapper/manager can find actionable turns with kubectl; it is a request
// flag, never a create grant. Classified, non-unclear peer_handoff turns
// additionally carry the jev-needs-peer-handoff=true annotation; it is a
// coordination-request flag, never a create grant and never a new fanout
// path.
func annotateChatRunWithIntent(annotations map[string]string, decision jev.Decision, classified bool) map[string]string {
	if !classified {
		return annotations
	}
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations[jevIntentAnnotation] = decision.Intent
	annotations[jevConfidenceAnnotation] = strconv.FormatFloat(decision.Confidence, 'f', 4, 64)
	if model := strings.TrimSpace(decision.Model); model != "" {
		annotations[jevModelAnnotation] = model
	}
	if jevNeedsManagerCreate(decision, classified) {
		annotations[jevNeedsManagerCreateAnnotation] = "true"
	}
	if jevNeedsPeerHandoff(decision, classified) {
		annotations[jevNeedsPeerHandoffAnnotation] = "true"
	}
	return annotations
}
