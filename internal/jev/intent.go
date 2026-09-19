// Intent routing on top of the Jev client (see jev.go).
//
// This is the one concrete Anvil use case this spike pins: given a user
// message plus minimal thread context, a Choice question over a small fixed
// intent set decides how the standing turn path should route, with a
// confidence gate — low confidence falls through to unclear (the human /
// clarification path). Jev only decides; generation still belongs to the
// harness chat model behind the standing session, and dispatch still
// belongs to the existing chat coordination path
// (dispatchChatCoordination / requestPeer).
//
// Fixed intent set:
//
//   - chat_reply: ordinary conversation the current agent answers directly.
//   - create_agent_request: the user asks to create/spawn a new agent.
//     Classification only: fulfillment must request a Wrapper/manager.
//     Peers never create agents directly (create-agent stays
//     Wrapper/manager-only); this intent routes to requesting one, and the
//     existing manager-authorization path still owns the decision.
//   - peer_handoff: hand off to (or coordinate with) another existing
//     agent/peer via requestPeer — never agent creation.
//   - tool_run: the reply needs a tool/command/lookup result first.
//   - unclear: nothing fits, the message is ambiguous, or confidence is
//     below the gate. Route to a human/clarification path.
//
// The runapi/desktop standing path can call Router.ClassifyIntent later;
// the hook point is queueChatTurnInternal in
// internal/runapi/chat_execution.go, before buildChatPrompt freezes the
// execution intent — classification observes the message, it never rewrites
// the durable turn. See docs/jev-intent-routing.md.
package jev

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Fixed intent set for standing-turn routing.
const (
	IntentChatReply          = "chat_reply"
	IntentCreateAgentRequest = "create_agent_request"
	IntentPeerHandoff        = "peer_handoff"
	IntentToolRun            = "tool_run"
	IntentUnclear            = "unclear"
)

// IntentQuestionID is the question ID carrying the routing Choice.
const IntentQuestionID = "intent"

// DefaultConfidenceThreshold is the review floor: below it the decision
// gates to unclear. It mirrors the docs' example review floor (0.5);
// destructive actions (e.g. fulfilling create_agent_request) must apply a
// higher bar in code at the fulfillment site, not here.
const DefaultConfidenceThreshold = 0.5

// IntentCriteria is the fixed routing rubric. Descriptions name what
// belongs to each option and how neighbors differ, per the Choice guidance;
// unclear is the explicit escape bucket so the model never has to force a
// message into a handler that does not fit.
func IntentCriteria() map[string]*string {
	return map[string]*string{
		IntentChatReply:          Desc("Ordinary conversation the current agent can answer directly with its own reply: greetings, questions, explanations, status updates, follow-ups. No new agent, no peer delegation, no tool execution requested."),
		IntentCreateAgentRequest: Desc("The author asks to create, spawn, or provision a NEW agent (new teammate, helper, worker, bot). Classification only: fulfillment must request a Wrapper/manager, never create from a peer."),
		IntentPeerHandoff:        Desc("The author asks to hand off, delegate, or coordinate with ANOTHER EXISTING agent or peer (a review, a named agent, a specific skill or owner). Routes via requestPeer coordination, never agent creation."),
		IntentToolRun:            Desc("The author asks to run a tool, command, lookup, query, build, deploy, or other action whose result the reply depends on. The reply needs a tool result first."),
		IntentUnclear:            nil,
	}
}

// IntentInstructions asks the routing judgment. `message` is the latest
// user message; `recent` is prior thread context for disambiguation only.
// Question IDs are not sent to the model, so the full meaning lives here.
const IntentInstructions = "What does the author of `message` want next? Use `recent` only to disambiguate pronouns and references in `message`."

// MessageContext is the minimal thread context routing observes: the
// latest user message plus a small tail of prior messages (oldest first)
// for disambiguation. It never carries credentials or secrets.
type MessageContext struct {
	Message string
	Recent  []string
}

// state renders the context as the named-field object Jev reads best,
// bounded so one turn cannot blow the shared token budget.
func (msg MessageContext) state() map[string]any {
	const maxMessageChars = 12000
	const maxRecent = 6
	const maxRecentChars = 2000
	recent := make([]string, 0, len(msg.Recent))
	for _, r := range msg.Recent {
		trimmed := strings.TrimSpace(r)
		if trimmed == "" {
			continue
		}
		recent = append(recent, trimmed)
	}
	if len(recent) > maxRecent {
		recent = recent[len(recent)-maxRecent:]
	}
	for i, r := range recent {
		if len(r) > maxRecentChars {
			recent[i] = r[:maxRecentChars] + "…"
		}
	}
	message := strings.TrimSpace(msg.Message)
	if len(message) > maxMessageChars {
		message = message[:maxMessageChars] + "…"
	}
	return map[string]any{"message": message, "recent": recent}
}

// IntentRequest builds the single-question routing request for msg. It is
// exported so the future runapi hook and tests share one construction.
func IntentRequest(model string, msg MessageContext) (Request, error) {
	if strings.TrimSpace(model) == "" {
		model = DefaultModel
	}
	if strings.TrimSpace(msg.Message) == "" {
		return Request{}, fmt.Errorf("jev intent: message is required")
	}
	req := Request{
		Model: model,
		State: msg.state(),
		Questions: map[string]Question{
			IntentQuestionID: ChoiceQuestion(IntentInstructions, IntentCriteria()),
		},
	}
	if err := req.Validate(); err != nil {
		return Request{}, err
	}
	return req, nil
}

// Decision is the routing outcome. Intent is the gated intent: when the
// model is unsure it reads unclear even if RawChoice names another option.
// Unclear reports whether the gate fired.
type Decision struct {
	Intent        string             `json:"intent"`
	RawChoice     string             `json:"rawChoice"`
	Confidence    float64            `json:"confidence"`
	Probabilities map[string]float64 `json:"probabilities"`
	Model         string             `json:"model"`
	Unclear       bool               `json:"unclear"`
}

// Router classifies standing-turn messages with a Jev Backend. The zero
// value is usable with a Fake/test Backend; set Model/Threshold to
// override the jev-latest default and the 0.5 review floor.
type Router struct {
	Backend   Backend
	Model     string
	Threshold float64
}

func (r *Router) model() string {
	if r != nil && strings.TrimSpace(r.Model) != "" {
		return strings.TrimSpace(r.Model)
	}
	return DefaultModel
}

func (r *Router) threshold() float64 {
	if r != nil && r.Threshold > 0 {
		return r.Threshold
	}
	return DefaultConfidenceThreshold
}

// ClassifyIntent routes one message. Unknown model outputs (a choice
// outside the fixed set — structurally impossible per the API, but
// defended anyway) and sub-threshold confidence both gate to unclear.
// Backend/transport errors propagate so callers keep today's behavior
// (hold / existing path) instead of routing on a guess.
func (r *Router) ClassifyIntent(ctx context.Context, msg MessageContext) (Decision, error) {
	if r == nil || r.Backend == nil {
		return Decision{}, fmt.Errorf("jev intent router: backend is not configured")
	}
	req, err := IntentRequest(r.model(), msg)
	if err != nil {
		return Decision{}, err
	}
	resp, err := r.Backend.Evaluate(ctx, req)
	if err != nil {
		return Decision{}, err
	}
	answer, err := resp.Choice(IntentQuestionID)
	if err != nil {
		return Decision{}, err
	}
	decision := Decision{
		RawChoice:     answer.Choice,
		Confidence:    answer.Confidence,
		Probabilities: answer.Probabilities,
		Model:         resp.Model,
	}
	switch answer.Choice {
	case IntentChatReply, IntentCreateAgentRequest, IntentPeerHandoff, IntentToolRun, IntentUnclear:
		decision.Intent = answer.Choice
	default:
		decision.Intent = IntentUnclear
		decision.Unclear = true
		return decision, nil
	}
	if decision.Intent == IntentUnclear || answer.Confidence < r.threshold() {
		decision.Intent = IntentUnclear
		decision.Unclear = true
	}
	return decision, nil
}

// KeywordFakeBackend is a deterministic no-network stub for the probe's
// --fake mode and local exploration. It routes on plain keyword matching
// (not model judgment) and reports full confidence for the match, zero
// confidence (→ unclear) when nothing matches.
func KeywordFakeBackend() Backend {
	return FuncBackend(func(ctx context.Context, req Request) (Response, error) {
		if err := ctx.Err(); err != nil {
			return Response{}, err
		}
		if err := req.Validate(); err != nil {
			return Response{}, err
		}
		var message string
		if state, ok := req.State.(map[string]any); ok {
			if m, ok := state["message"].(string); ok {
				message = strings.ToLower(m)
			}
		}
		choice := IntentUnclear
		confidence := 0.0
		contains := func(words ...string) bool {
			for _, w := range words {
				if strings.Contains(message, w) {
					return true
				}
			}
			return false
		}
		switch {
		case contains("create", "spawn", "new agent", "provision"):
			choice, confidence = IntentCreateAgentRequest, 0.92
		case contains("delegate", "hand off", "handoff", "ask the reviewer", "peer"):
			choice, confidence = IntentPeerHandoff, 0.88
		case contains("run", "deploy", "build", "lookup", "look up", "query", "search", "tool"):
			choice, confidence = IntentToolRun, 0.85
		case strings.TrimSpace(message) != "":
			choice, confidence = IntentChatReply, 0.9
		}
		probs := map[string]float64{
			IntentChatReply:          0.0,
			IntentCreateAgentRequest: 0.0,
			IntentPeerHandoff:        0.0,
			IntentToolRun:            0.0,
			IntentUnclear:            0.0,
		}
		probs[choice] = 1.0
		answer := ChoiceAnswer{Type: TypeChoice, Choice: choice, Probabilities: probs, Confidence: confidence}
		raw, err := json.Marshal(answer)
		if err != nil {
			return Response{}, err
		}
		return Response{Model: "fake-keyword-router", Answers: map[string]json.RawMessage{IntentQuestionID: raw}}, nil
	})
}
