package runapi

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	agentsv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
	"github.com/hazyforge/anvil-agents/internal/chat"
	"github.com/hazyforge/anvil-agents/internal/standing"
)

// Slice-2 standing turn-path wiring.
//
// When the opt-in live gate is on (standing.liveEnabled config or the
// ANVIL_AGENTS_STANDING_LIVE environment variable, plus an attached
// standing.Backend), reconcileChatTurn executes InProcess threads through
// their standing session instead of leaving the run for the Job /
// NeedsHuman hold path. Every standing turn remains one durable AgentRun:
//
//   - The append-only AgentRun is still created from the outbox-frozen
//     turn.RunJSON exactly as today; the standing backend consumes that
//     frozen intent (run.Spec.Prompt) and never bypasses it.
//   - EnsureTurnSession binds the turn to its thread session, StreamTurn
//     delivers the reply bound to the turn identity, and the returned full
//     reply is persisted by marking the run Succeeded so the existing
//     Succeeded branch below completes the turn (64KiB truncation,
//     dispatchChatCoordination peer fanout, CompleteTurn).
//   - Peer child turns take the identical branch: dispatchChatCoordination
//     queues each delivery through queueChatTurnInternal, which reconciles
//     through the same hook keyed off the recipient child thread.
//   - Gate off, unresolvable harness, or any backend error falls back to
//     today's behavior: the run holds as InProcessNotWired and no Job is
//     ever created for it.
//
// Stubbed vs live in this slice: unit tests drive standing.FakeBackend
// (deterministic word-streamed prose, no model calls, no credentials, no
// harness subprocess). The envelope wrapper below is stub glue so the fake's
// plain-text reply parses through the existing per-harness reply extractors;
// a real harness process returns native-enveloped output directly and this
// wrapper goes away with it. Crash recovery between create and the Succeeded
// mark re-streams at least once; the singleflight guard only serializes one
// process (the chart runs one API replica), so a multi-replica claim and a
// controller-hold yield are the documented next steps, not this slice.

// standingTurnGuard serializes live standing execution per turn ID within one
// API process. Queue, read-refresh, and background recovery can reconcile the
// same turn concurrently; the loser skips streaming and observes the winner's
// Succeeded mark on a later pass.
type standingTurnGuard struct {
	mu       sync.Mutex
	inflight map[string]struct{}
}

func newStandingTurnGuard() *standingTurnGuard {
	return &standingTurnGuard{inflight: map[string]struct{}{}}
}

func (guard *standingTurnGuard) claim(id string) bool {
	if guard == nil {
		return true
	}
	guard.mu.Lock()
	defer guard.mu.Unlock()
	if _, ok := guard.inflight[id]; ok {
		return false
	}
	guard.inflight[id] = struct{}{}
	return true
}

func (guard *standingTurnGuard) release(id string) {
	if guard == nil {
		return
	}
	guard.mu.Lock()
	defer guard.mu.Unlock()
	delete(guard.inflight, id)
}

// standingTurnEnabled reports whether live standing execution may run. Both
// the explicit opt-in flag and an attached backend are required; gate-off
// keeps today's Job / NeedsHuman hold behavior unchanged.
func (server *Server) standingTurnEnabled() bool {
	return server != nil && server.standing != nil && server.config.Standing.LiveEnabled
}

// reconcileStandingTurn streams one standing turn when the gate is on and the
// thread's harness selects execution.runtime InProcess. It returns true when
// the in-memory run now carries the streamed Succeeded result and the caller
// should continue through the normal status switch; false means untouched
// (today's behavior owns the turn from here).
func (server *Server) reconcileStandingTurn(ctx context.Context, turn *chat.Turn, run *agentsv1alpha1.AgentRun) (bool, error) {
	if turn == nil || run == nil {
		return false, nil
	}
	phase := run.Status.Phase
	if phase == agentsv1alpha1.AgentRunPhaseSucceeded ||
		phase == agentsv1alpha1.AgentRunPhaseFailed ||
		phase == agentsv1alpha1.AgentRunPhaseNeedsHuman {
		return false, nil
	}
	if !server.standingTurnEnabled() {
		return false, nil
	}
	if turn.ID == "" {
		return false, nil
	}
	if !server.standingGuard.claim(turn.ID) {
		return false, nil
	}
	defer server.standingGuard.release(turn.ID)
	thread, err := server.chatStore.GetThread(ctx, turn.Namespace, turn.ThreadID)
	if err != nil {
		server.log.Error(err, "standing turn cannot load thread; keeping hold behavior", "namespace", turn.Namespace, "turn", turn.ID)
		return false, nil
	}
	spec, ok := server.standingSessionSpec(ctx, thread)
	if !ok {
		return false, nil
	}
	handle, _, err := standing.EnsureTurnSession(ctx, server.standing, spec)
	if err != nil {
		server.log.Error(err, "standing turn cannot ensure session; keeping hold behavior", "namespace", turn.Namespace, "turn", turn.ID)
		return false, nil
	}
	prompt := strings.TrimSpace(run.Spec.Prompt)
	if prompt == "" {
		server.log.Info("standing turn has no frozen prompt; keeping hold behavior", "namespace", turn.Namespace, "turn", turn.ID)
		return false, nil
	}
	sink := &standingRunSink{runName: turn.RunName}
	reply, err := server.standing.StreamTurn(ctx, handle, turn.ID, prompt, sink)
	if err != nil {
		server.log.Error(err, "standing turn stream failed; keeping hold behavior", "namespace", turn.Namespace, "turn", turn.ID)
		return false, nil
	}
	if strings.TrimSpace(reply) == "" {
		server.log.Info("standing turn streamed an empty reply; keeping hold behavior", "namespace", turn.Namespace, "turn", turn.ID)
		return false, nil
	}
	harnessKind := agentsv1alpha1.AgentRunHarnessBackendKind(handle.HarnessKind)
	run.Status.Phase = agentsv1alpha1.AgentRunPhaseSucceeded
	run.Status.Backend = string(harnessKind)
	run.Status.Output = standingRunOutput(harnessKind, strings.TrimSpace(reply))
	completed := metav1.NewTime(time.Now())
	run.Status.CompletedAt = &completed
	if err := server.writes.Status().Update(ctx, run); err != nil {
		// A racing writer (controller hold, another replica) may own the
		// stored status now. Re-read and continue with stored truth rather
		// than persisting a turn the run record contradicts.
		stored := &agentsv1alpha1.AgentRun{}
		if getErr := server.runs.Get(ctx, types.NamespacedName{Namespace: turn.Namespace, Name: turn.RunName}, stored); getErr != nil {
			server.log.Error(getErr, "standing turn cannot resolve raced status", "namespace", turn.Namespace, "turn", turn.ID)
			*run = *stored
			return false, nil
		}
		*run = *stored
		if stored.Status.Phase == agentsv1alpha1.AgentRunPhaseSucceeded ||
			stored.Status.Phase == agentsv1alpha1.AgentRunPhaseFailed ||
			stored.Status.Phase == agentsv1alpha1.AgentRunPhaseNeedsHuman {
			return false, nil
		}
		return false, nil
	}
	server.log.Info("standing turn streamed", "namespace", turn.Namespace, "turn", turn.ID, "session", handle.SessionName, "tokens", sink.tokens)
	return true, nil
}

// suspendStandingTurn releases session resources after a terminal turn
// reconciliation. Best-effort: suspend is a density optimization, never a
// correctness gate, so errors are logged and never fail the turn.
func (server *Server) suspendStandingTurn(ctx context.Context, namespace, threadID, turnID string) {
	if !server.standingTurnEnabled() {
		return
	}
	thread, err := server.chatStore.GetThread(ctx, namespace, threadID)
	if err != nil {
		return
	}
	spec, ok := server.standingSessionSpec(ctx, thread)
	if !ok {
		return
	}
	if _, err := standing.SuspendIdleSession(ctx, server.standing, spec.Namespace, spec.SessionName, nil); err != nil {
		server.log.Error(err, "suspend standing session after terminal turn", "namespace", namespace, "turn", turnID)
	}
}

// standingRunSink binds streamed token events to the turn identity: the
// backend stamps thread/turn, this wrapper adds the append-only run name.
// Slice 2 counts the events and drops them (the durable reply lands in the
// turn record, which is what the existing stream snapshot serves); an
// optional downstream receives the stamped events so tests can observe the
// binding, and slice 3 can multiplex live token frames into open streams.
type standingRunSink struct {
	runName    string
	tokens     int
	downstream standing.Sink
}

// OnToken implements standing.Sink.
func (sink *standingRunSink) OnToken(ctx context.Context, event standing.TokenEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	event.RunName = sink.runName
	sink.tokens++
	if sink.downstream != nil {
		return sink.downstream.OnToken(ctx, event)
	}
	return nil
}

// standingRunOutput wraps a stub backend's plain-text reply in the harness's
// native envelope so the existing per-harness reply extractor accepts it on
// the shared Succeeded path. Backends whose extractor already accepts plain
// text (openCode, piAgent, grokBuild, custom, unset) pass through unwrapped.
// Envelopes are built with structured marshaling only — never string
// concatenation — so a reply containing quotes cannot break the framing. The
// OpenClaw envelope keeps its native key order (payloads before meta) via an
// ordered struct because its prefix parser reads the public payloads first.
// A real harness process returns native-enveloped output directly and does
// not need this wrapper.
func standingRunOutput(backend agentsv1alpha1.AgentRunHarnessBackendKind, reply string) string {
	marshal := func(value any) string {
		raw, err := json.Marshal(value)
		if err != nil {
			return reply
		}
		return string(raw)
	}
	switch backend {
	case agentsv1alpha1.AgentRunHarnessBackendCodex:
		return marshal(map[string]any{
			"type": "item.completed",
			"item": map[string]any{"type": "agent_message", "text": reply},
		})
	case agentsv1alpha1.AgentRunHarnessBackendHermesAgent:
		return marshal(map[string]any{
			"type": "anvil.hermes.final", "version": 1, "role": "assistant", "text": reply,
		})
	case agentsv1alpha1.AgentRunHarnessBackendOpenClaw:
		type payload struct {
			Text string `json:"text"`
		}
		type agentMeta struct {
			Provider string `json:"provider"`
			Model    string `json:"model"`
		}
		type meta struct {
			AgentMeta agentMeta `json:"agentMeta"`
			Aborted   bool      `json:"aborted"`
		}
		type envelope struct {
			Payloads []payload `json:"payloads"`
			Meta     meta      `json:"meta"`
		}
		return marshal(envelope{
			Payloads: []payload{{Text: reply}},
			Meta:     meta{AgentMeta: agentMeta{Provider: "standing-stub", Model: "standing-stub"}},
		})
	case agentsv1alpha1.AgentRunHarnessBackendPrimeAgent:
		return marshal(map[string]any{
			"type": "message_end",
			"message": map[string]any{
				"role": "assistant", "stopReason": "stop",
				"content": []any{map[string]any{"type": "text", "text": reply}},
			},
		})
	case agentsv1alpha1.AgentRunHarnessBackendAgy:
		return marshal(map[string]any{
			"event":  "result",
			"result": map[string]any{"status": "SUCCESS", "response": reply},
		})
	default:
		return reply
	}
}
