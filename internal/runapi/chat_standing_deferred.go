package runapi

import (
	"context"
	"os/exec"
	"regexp"
	"strings"

	agentsv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
	"github.com/hazyforge/anvil-agents/internal/chat"
	"github.com/hazyforge/anvil-agents/internal/standing"
)

// standingTurnContract is frozen into standing (InProcess / SubstrateActor)
// prompts. One process still owns the turn: if inspection is needed, it has
// to finish before the harness exits. Promising to check later is how
// Desktop showed Reply received / Harness finished with no GitHub result.
const standingTurnContract = "\nSTANDING_TURN: this is one in-process harness turn. Finish any inspection (PATH, tools, GitHub, files) before you stop and include the result in this same reply. If you cannot inspect, say what this session actually lacks. Never end with a promise to check, look, or inspect later.\n"

// standingContinueBudget keeps a continuation prompt inside the standing
// backend's maxPromptBytes cap (256KiB) with headroom for facts + wrapper.
const standingContinueBudget = 200 * 1024

var deferredStandingLead = regexp.MustCompile(`(?is)^\s*(?:(?:sure|okay|ok|alright)[,!.]?\s+)?(?:i['’]ll|i will|let me|i am going to|i['’]m going to|i['’]m|i am)\s+(?:just\s+)?(?:check|look|inspect|see|find out|verify|investigate|ask)\b`)

// standingReplyDefersWork reports a short assistant reply whose only speech
// act is promising a later inspection. Complete answers that happen to
// mention checking, or replies that already report a finding, return false.
func standingReplyDefersWork(reply string) bool {
	text := strings.TrimSpace(reply)
	if text == "" || len(text) > 480 {
		return false
	}
	lower := strings.ToLower(strings.ReplaceAll(text, "\u2019", "'"))
	if standingReplyLooksResolved(lower) {
		return false
	}
	return deferredStandingLead.MatchString(lower)
}

func standingReplyLooksResolved(lower string) bool {
	for _, marker := range []string{
		"not on path",
		"not installed",
		"not available",
		"don't have",
		"do not have",
		"no github",
		"no access",
		"without access",
		"authenticated",
		"logged in",
		"gh auth",
		"here is",
		"here's what",
		"result:",
		"i have access",
		"i don't have access",
		"i do not have access",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func standingPublicText(kind agentsv1alpha1.AgentRunHarnessBackendKind, native string) string {
	reply, err := extractChatReply(kind, native)
	if err != nil {
		return strings.TrimSpace(native)
	}
	return strings.TrimSpace(reply)
}

func joinStandingNativeReplies(first, second string) string {
	a := strings.TrimRight(first, "\n")
	b := strings.TrimSpace(second)
	if strings.TrimSpace(a) == "" {
		return second
	}
	if b == "" {
		return first
	}
	return a + "\n" + b
}

func standingProcessFacts() string {
	var b strings.Builder
	b.WriteString("Observed this API process PATH (not a GitHub API result):\n")
	for _, bin := range []string{"gh", "git", "kubectl", "grok"} {
		if path, err := exec.LookPath(bin); err == nil && strings.TrimSpace(path) != "" {
			b.WriteString("- ")
			b.WriteString(bin)
			b.WriteString(": present\n")
			continue
		}
		b.WriteString("- ")
		b.WriteString(bin)
		b.WriteString(": not found\n")
	}
	b.WriteString("Standing copies the grok CLI into the API process; Job runners may include gh. Do not invent org or repository access.\n")
	return b.String()
}

func standingContinuePrompt(original, firstPublic string) string {
	var b strings.Builder
	b.WriteString("STANDING_TURN_CONTINUE: the previous output promised to inspect or check something but did not include a result:\n\n")
	b.WriteString(strings.TrimSpace(firstPublic))
	b.WriteString("\n\nThis same turn is still open. Perform that inspection now, or report the observed session facts below. Do not promise later work. Do not invent GitHub org or repository access.\n\n")
	b.WriteString(standingProcessFacts())
	prefix := b.String()
	original = strings.TrimSpace(original)
	budget := standingContinueBudget - len(prefix) - len("\nOriginal frozen intent follows.\n\n")
	if budget < 1024 {
		return prefix
	}
	if len(original) > budget {
		original = original[len(original)-budget:]
	}
	if original == "" {
		return prefix
	}
	return prefix + "\nOriginal frozen intent follows.\n\n" + original
}

// holdDoneSink withholds the harness Done marker from live WebSocket
// subscribers until the standing turn has actually finished. StreamTurn emits
// Done when the subprocess exits; Desktop treats that (plus the stream
// terminal that follows) as the end of the turn. A deferred "I'll check"
// process exit must not close the stream before the continuation lands.
type holdDoneSink struct {
	inner standing.Sink
	done  *standing.TokenEvent
}

func (sink *holdDoneSink) OnToken(ctx context.Context, event standing.TokenEvent) error {
	if sink == nil {
		return ctx.Err()
	}
	if event.Done {
		copyEvent := event
		sink.done = &copyEvent
		return ctx.Err()
	}
	if sink.inner != nil {
		return sink.inner.OnToken(ctx, event)
	}
	return ctx.Err()
}

func (sink *holdDoneSink) flush(ctx context.Context) error {
	if sink == nil || sink.done == nil {
		return ctx.Err()
	}
	if sink.inner == nil {
		return ctx.Err()
	}
	return sink.inner.OnToken(ctx, *sink.done)
}

// completeStandingDeferredWork keeps the same AgentRun open when grok (or any
// standing harness) exits after a promise to inspect. One extra StreamTurn
// on the same session is allowed; true async Jobs remain a different plane.
func (server *Server) completeStandingDeferredWork(ctx context.Context, handle standing.SessionHandle, turn *chat.Turn, kind agentsv1alpha1.AgentRunHarnessBackendKind, originalPrompt, reply string, sink standing.Sink) string {
	if server == nil || turn == nil {
		return reply
	}
	public := standingPublicText(kind, reply)
	if !standingReplyDefersWork(public) {
		return reply
	}
	if ctx.Err() != nil || !standingCanDriveStream(ctx) {
		server.log.Info("standing deferred reply left as-is; remaining deadline too short for continuation", "namespace", turn.Namespace, "turn", turn.ID)
		return reply
	}
	contPrompt := standingContinuePrompt(originalPrompt, public)
	cont, err := server.standing.StreamTurn(ctx, handle, turn.ID, contPrompt, sink)
	if err != nil {
		server.log.Error(err, "standing deferred continuation failed; keeping the first reply", "namespace", turn.Namespace, "turn", turn.ID)
		return reply
	}
	if strings.TrimSpace(cont) == "" {
		return reply
	}
	joined := joinStandingNativeReplies(reply, cont)
	server.log.Info("standing deferred continuation streamed", "namespace", turn.Namespace, "turn", turn.ID)
	return joined
}
