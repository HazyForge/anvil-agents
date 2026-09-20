package runapi

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
// standing.Backend), reconcileChatTurn executes InProcess and SubstrateActor
// threads through their standing session instead of leaving the run for the
// Job / NeedsHuman hold path. Every standing turn remains one durable AgentRun:
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
// Stubbed vs live: unit tests drive standing.FakeBackend (deterministic
// word-streamed prose, no model calls, no credentials, no harness
// subprocess). The envelope wrapper below is stub glue so the fake's
// plain-text reply parses through the existing per-harness reply extractors;
// a real harness process (standing.ProcessBackend, slice 3b) returns
// native-enveloped output directly and skips this wrapper via the
// nativeEnvelopeBackend gate. Crash recovery between create and the
// Succeeded mark re-streams at least once. The singleflight guard only
// serializes one process (the chart runs one API replica), so cross-replica
// races are won through the annotation claim below (claimStandingTurn, slice
// 4), and the controller yields its InProcess hold for claimed turns
// (StandingClaimed, never InProcessNotWired racing a live stream).

// standingTurnGuard serializes live standing execution per turn ID within one
// API process. Queue and background recovery can reconcile the same turn
// concurrently; GET thread refresh is observe-only. The loser skips streaming
// and observes the winner's Succeeded mark on a later pass.
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

// standingDriveMinBudget is the shortest remaining context deadline that
// may start StreamTurn. GET thread refresh uses 3s; grok standing turns
// need tens of seconds. Recovery uses standingDriveTimeout per turn.
const standingDriveMinBudget = 45 * time.Second

// standingDriveTimeout bounds one recovery-driven standing stream. It
// matches the process-backend default so a warm grok turn can finish
// after a short GET refresh skipped streaming.
const standingDriveTimeout = 2 * time.Minute

// chatRecoveryListTimeout bounds the pending-turn LIST, not StreamTurn.
const chatRecoveryListTimeout = 20 * time.Second

func standingCanDriveStream(ctx context.Context) bool {
	if ctx == nil || ctx.Err() != nil {
		return false
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return true
	}
	return time.Until(deadline) >= standingDriveMinBudget
}

// reconcileStandingTurn streams one standing turn when the gate is on and the
// thread's harness selects InProcess or SubstrateActor. driveStanding is true
// for POST/recovery and false for GET thread refresh. It returns true when
// the in-memory run now carries the streamed Succeeded result and the caller
// should continue through the normal status switch; false means untouched
// (today's behavior owns the turn from here).
func (server *Server) reconcileStandingTurn(ctx context.Context, turn *chat.Turn, run *agentsv1alpha1.AgentRun, driveStanding bool) (bool, error) {
	if turn == nil || run == nil {
		return false, nil
	}
	phase := run.Status.Phase
	if phase == agentsv1alpha1.AgentRunPhaseSucceeded ||
		phase == agentsv1alpha1.AgentRunPhaseFailed {
		return false, nil
	}
	if phase == agentsv1alpha1.AgentRunPhaseNeedsHuman && !server.standingTakeoverAllowed(run, turn) {
		return false, nil
	}
	if !server.standingTurnEnabled() {
		return false, nil
	}
	if turn.ID == "" {
		return false, nil
	}
	if !driveStanding {
		return false, nil
	}
	if !standingCanDriveStream(ctx) {
		server.log.Info("standing turn stream skipped; remaining deadline too short", "namespace", turn.Namespace, "turn", turn.ID)
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
	// Multi-replica claim: exactly one API replica may drive this turn. The
	// winner is decided by the annotation write below; losers keep today's
	// hold behavior and observe the winner's Succeeded mark on a later pass.
	if !server.claimStandingTurn(ctx, turn, run) {
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
	sink := &standingRunSink{runName: turn.RunName, downstream: server.standingTokenPublisher(turn.Namespace)}
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
	if spec.Runtime != "" {
		run.Status.ExecutionRuntime = spec.Runtime
	}
	run.Status.Output = standingTurnOutput(server.standing, harnessKind, strings.TrimSpace(reply))
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

// standingTakeoverAllowed reports whether a NeedsHuman run may be re-driven
// through this turn's standing claim. Fresh (empty-phase) runs never reach
// here; this only re-opens a yielded hold: the claim must bind this exact
// turn, and it must be ours or stale (owner crash). A live foreign claim
// means another replica is driving — keep hold behavior, never steal it.
func (server *Server) standingTakeoverAllowed(run *agentsv1alpha1.AgentRun, turn *chat.Turn) bool {
	if server == nil || run == nil || turn == nil {
		return false
	}
	if !server.standingTurnEnabled() {
		return false
	}
	turnID := strings.TrimSpace(turn.ID)
	if turnID == "" {
		return false
	}
	raw := strings.TrimSpace(run.Annotations[agentsv1alpha1.AgentRunStandingClaimAnnotation])
	if raw == "" {
		return false
	}
	claim, err := standing.ParseClaim(raw)
	if err != nil || claim.TurnID != turnID {
		return false
	}
	if _, live := standing.ClaimForTurn(run.Annotations, turnID, time.Now(), standing.ClaimTTL); live {
		return claim.Owner == server.standingOwnerID()
	}
	return true
}

// claimStandingTurn wins the cross-replica annotation claim for turn.ID on
// the turn's AgentRun. It fresh-reads the run, yields to terminal results
// and live foreign claims, and stamps this replica's claim with a single
// conditional metadata Update: exactly one replica's write lands first, so
// exactly one replica streams. On a win the in-memory run is refreshed to
// the winning revision so the Succeeded mark below applies cleanly; on a
// loss it is refreshed to stored truth so the caller can complete off the
// winner's result. Any failure keeps today's hold behavior — the turn stays
// active for the holder or a later recovery pass.
func (server *Server) claimStandingTurn(ctx context.Context, turn *chat.Turn, run *agentsv1alpha1.AgentRun) bool {
	if server == nil || server.writes == nil || turn == nil || run == nil {
		return false
	}
	namespace := strings.TrimSpace(turn.Namespace)
	if namespace == "" || strings.TrimSpace(turn.ID) == "" || strings.TrimSpace(turn.RunName) == "" {
		return false
	}
	owner := server.standingOwnerID()
	now := time.Now()
	for attempt := 0; attempt < 2; attempt++ {
		fresh := &agentsv1alpha1.AgentRun{}
		if err := server.writes.Get(ctx, types.NamespacedName{Namespace: namespace, Name: turn.RunName}, fresh); err != nil {
			server.log.Error(err, "standing turn cannot read run for claim; keeping hold behavior", "namespace", namespace, "turn", turn.ID)
			return false
		}
		switch fresh.Status.Phase {
		case agentsv1alpha1.AgentRunPhaseSucceeded, agentsv1alpha1.AgentRunPhaseFailed:
			*run = *fresh
			return false
		case agentsv1alpha1.AgentRunPhaseNeedsHuman:
			if !server.standingTakeoverAllowed(fresh, turn) {
				*run = *fresh
				return false
			}
		}
		if live, ok := standing.ClaimForTurn(fresh.Annotations, turn.ID, now, standing.ClaimTTL); ok && live.Owner != owner {
			server.log.Info("standing turn owned by another replica; keeping hold behavior", "namespace", namespace, "turn", turn.ID, "owner", live.Owner)
			*run = *fresh
			return false
		}
		value, err := standing.EncodeClaim(standing.Claim{TurnID: turn.ID, Owner: owner, AtUnix: now.Unix()})
		if err != nil {
			server.log.Error(err, "standing turn cannot encode claim; keeping hold behavior", "namespace", namespace, "turn", turn.ID)
			return false
		}
		if fresh.Annotations == nil {
			fresh.Annotations = map[string]string{}
		}
		fresh.Annotations[agentsv1alpha1.AgentRunStandingClaimAnnotation] = value
		if err := server.writes.Update(ctx, fresh); err != nil {
			if apierrors.IsConflict(err) {
				continue
			}
			server.log.Error(err, "standing turn cannot persist claim; keeping hold behavior", "namespace", namespace, "turn", turn.ID)
			return false
		}
		*run = *fresh
		return true
	}
	return false
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

// standingOwnerID identifies this API process in standing-turn claims.
// Tests override Server.standingOwner to simulate peer replicas.
func (server *Server) standingOwnerID() string {
	if server == nil || strings.TrimSpace(server.standingOwner) == "" {
		return standing.NewOwnerID()
	}
	return server.standingOwner
}

// standingTokenPublisher fans stamped token events out to open WebSocket
// stream subscribers for the turn's thread. The outer standingRunSink stamps
// the run name first, so subscribers observe the full turn identity
// (thread/turn/run). Publishing never fails the turn: the hub drops for slow
// readers and the durable reply still lands in the turn record. Nil when no
// hub is attached, which keeps the sink a pure counter exactly as before.
func (server *Server) standingTokenPublisher(namespace string) standing.Sink {
	if server == nil || server.standingHub == nil {
		return nil
	}
	return standing.SinkFunc(func(ctx context.Context, event standing.TokenEvent) error {
		server.standingHub.publish(namespace, event)
		return ctx.Err()
	})
}

// standingRunSink binds streamed token events to the turn identity: the
// backend stamps thread/turn, this wrapper adds the append-only run name.
// It counts the events while the durable reply lands in the turn record
// (which is what the stream snapshot serves); the downstream receives the
// stamped events so open WebSocket streams can multiplex live token frames
// and tests can observe the binding.
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

// nativeEnvelopeBackend is implemented by standing backends whose StreamTurn
// returns the harness's native output directly (a real harness process, slice
// 3b). Stub backends return plain text and need the envelope wrapper below;
// without it the shared Succeeded path's per-harness extractors would reject
// the reply and the turn would never complete.
type nativeEnvelopeBackend interface {
	ReturnsNativeEnvelope() bool
}

// standingTurnOutput persists a streamed standing reply as the run output.
// Stub backends are wrapped in the harness's native envelope so the existing
// extractors accept them; native backends pass through unwrapped so a reply
// containing quotes can never be double-wrapped.
func standingTurnOutput(backend standing.Backend, harnessKind agentsv1alpha1.AgentRunHarnessBackendKind, reply string) string {
	if native, ok := backend.(nativeEnvelopeBackend); ok && native.ReturnsNativeEnvelope() {
		return reply
	}
	return standingRunOutput(harnessKind, reply)
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
