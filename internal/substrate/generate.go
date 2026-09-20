package substrate

import (
	"context"
	"strings"
)

const (
	// DefaultGenerateActorClass is the overlay ActorTemplate that speaks
	// ACP-over-HTTP for generate-on-actor. Desktop standing-chat stays off
	// this list so ProcessBackend keeps owning desktop-standing-assistant.
	DefaultGenerateActorClass = "acp-spike"
)

// GenerateRequest is one frozen AgentRun prompt delivered through atenet
// after Resume/bind. SessionID is the chat thread (or run name) so an ACP
// actor can key the turn; it is not a secret.
type GenerateRequest struct {
	ActorName string
	Atespace  string
	SessionID string
	Prompt    string
}

// GenerateResult is the actor's public reply used as AgentRun status.output.
type GenerateResult struct {
	Text       string
	StopReason string
}

// TurnGenerator streams one prompt through atenet (ACP/HTTP) onto a resumed
// actor. Tests inject fakes; the live client is AtenetClient.
type TurnGenerator interface {
	Generate(ctx context.Context, req GenerateRequest) (GenerateResult, error)
}

// GenerateEnabled reports whether this actorClass should complete via
// atenet instead of standing ProcessBackend. Both the explicit generate
// gate and an exact class match are required: an empty allowlist never
// generates, so flipping actorsEnabled cannot hang Desktop chat.
// actorClass standing-chat is never generated even if listed, so
// desktop-standing-assistant stays on ProcessBackend.
func GenerateEnabled(generateOnActor bool, classes []string, actorClass string) bool {
	if !generateOnActor {
		return false
	}
	class := strings.TrimSpace(actorClass)
	if class == "" || class == "standing-chat" {
		return false
	}
	for _, allowed := range classes {
		if strings.TrimSpace(allowed) == class {
			return true
		}
	}
	return false
}

// ParseActorClassList splits a comma-separated actorClass allowlist.
func ParseActorClassList(raw string) []string {
	var out []string
	seen := map[string]struct{}{}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if _, ok := seen[part]; ok {
			continue
		}
		seen[part] = struct{}{}
		out = append(out, part)
	}
	return out
}
