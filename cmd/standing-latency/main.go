// Command standing-latency measures the standing InProcess warm path
// (session resume + streamed turn) for a direct turn AND a peer delivery,
// against the documented Job cold-start baseline.
//
// What it measures (per thread plane: direct thread, peer child thread):
// session cold-create, warm resume, the full warm turn (resume + StreamTurn),
// time-to-first-token, plus — since slice 5c — cold FIRST turns (fresh thread,
// first StreamTurn, records the slice-5b native session id when the harness
// kind supports one) and resumed SECOND turns (same thread, second StreamTurn
// reusing the recorded native id). Sixteen scenarios
// (directEnsureCold/directEnsureWarm/directTurnWarm/directFirstToken/
// directTurnCold/directFirstTokenCold/directTurnResumed/directFirstTokenResumed
// plus the eight peer… peers), each with count/minMs/meanMs/p50Ms/p95Ms/maxMs,
// plus a nativeResume section ({supported, coldTurns, resumedTurns,
// nativeIDsObserved}) proving whether second turns actually resumed.
//
// Fake-backend by default: it drives standing.FakeBackend (or a stubbed
// ProcessBackend runner), so the numbers are deterministic and CI-safe.
// They prove the harness and the warm-reuse contract only — never a
// promote/reshape/retire decision. The optional live path
// (-backend process-exec) runs one real harness subprocess per measured turn
// through standing.ProcessBackend with a PATH-resolved CLI, exactly like the
// slice-3b turn path; that path is local-only (needs the harness CLI and its
// own auth home) and never reads Secrets. -only selects a scenario subset
// for cheap live probes (e.g. cold + resumed turns only).
//
// The Job cold-start baseline cannot be measured from this process (it needs
// a real cluster scheduler), so it defaults to the documented Primaris
// Pod-ready figures (~12s p50 / ~44s p95). Those figures cover Job create to
// Pod ready only — full turn time (model/harness time on top) is a separate
// measurement, see internal/desktop/chat_latency.go and the Desktop
// chat-latency JSONL. The report keeps p50/p95 fields for every scenario
// plus the comparison and a bars verdict.
//
// Usage:
//
//	go run ./cmd/standing-latency -n 20 -out .runtime/standing-latency-fake.json
//	go run ./cmd/standing-latency -backend process-stub -n 20 -out .runtime/standing-latency-stub.json
//	go run ./cmd/standing-latency -backend process-exec -harness codex -n 10 -out .runtime/standing-latency-live.json
//	go run ./cmd/standing-latency -backend process-exec -harness codex -n 5 -only directTurnCold,directTurnResumed -out .runtime/standing-latency-live-cold-resume.json
//
// Chat-bars mode scores Desktop signed-in standing JSONL against the same
// Slice 5a promote/reshape/retire vocabulary (see chat_bars.go for the
// honest single-plane mapping — reshape is never emitted there):
//
//	go run ./cmd/standing-latency -chat-jsonl .runtime/chat-latency.jsonl -out .runtime/standing-chat-latency-bars.json
//	hack/standing-chat-latency-bars.sh --jsonl .runtime/chat-latency.jsonl
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hazyforge/anvil-agents/internal/standing"
)

type scenarioStats struct {
	Count   int     `json:"count"`
	MinMs   float64 `json:"minMs"`
	MeanMs  float64 `json:"meanMs"`
	P50Ms   float64 `json:"p50Ms"`
	P95Ms   float64 `json:"p95Ms"`
	MaxMs   float64 `json:"maxMs"`
	WarmOps int     `json:"warmOps,omitempty"`
}

type latencyReport struct {
	Tool          string                   `json:"tool"`
	Backend       string                   `json:"backend"`
	Live          bool                     `json:"live"`
	Iterations    int                      `json:"iterations"`
	Namespace     string                   `json:"namespace"`
	Harness       string                   `json:"harness"`
	GeneratedAt   string                   `json:"generatedAt"`
	Scenarios     map[string]scenarioStats `json:"scenarios"`
	NativeResume  *nativeResumeReport      `json:"nativeResume,omitempty"`
	JobBaseline   *scenarioStats           `json:"jobBaseline,omitempty"`
	ComparisonMs  map[string]float64       `json:"comparisonMs,omitempty"`
	Verdict       string                   `json:"verdict"`
	VerdictReason string                   `json:"verdictReason"`
}

// nativeResumeReport makes the slice-5b cold-first vs resumed-second turn
// distinction explicit in the report: how many measured second turns reused
// a recorded native session id, and whether the harness kind supports native
// resume at all. Fake backends never record native ids; stub and live
// ProcessBackends record them exactly when the harness kind documents a
// resume surface (see standing.SupportsNativeResume).
type nativeResumeReport struct {
	Supported         bool   `json:"supported"`
	ColdTurns         int    `json:"coldTurns"`
	ResumedTurns      int    `json:"resumedTurns"`
	NativeIDsObserved int    `json:"nativeIDsObserved"`
	Note              string `json:"note,omitempty"`
}

type config struct {
	iterations int
	namespace  string
	harness    string
	prompt     string
	backend    string
	jobP50     float64
	jobP95     float64
	outPath    string
	only       map[string]bool
}

func percentile(sorted []float64, quantile float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	rank := int(math.Ceil(quantile*float64(len(sorted)))) - 1
	if rank < 0 {
		rank = 0
	}
	if rank >= len(sorted) {
		rank = len(sorted) - 1
	}
	return sorted[rank]
}

func summarize(durations []time.Duration, warmOps int) scenarioStats {
	samples := make([]float64, 0, len(durations))
	var total float64
	min := math.Inf(1)
	max := math.Inf(-1)
	for _, d := range durations {
		ms := float64(d.Nanoseconds()) / 1e6
		samples = append(samples, ms)
		total += ms
		if ms < min {
			min = ms
		}
		if ms > max {
			max = ms
		}
	}
	sort.Float64s(samples)
	stats := scenarioStats{Count: len(samples), MinMs: min, P50Ms: percentile(samples, 0.5), P95Ms: percentile(samples, 0.95), MaxMs: max}
	if len(samples) > 0 {
		stats.MeanMs = total / float64(len(samples))
	} else {
		stats.MinMs, stats.MaxMs = 0, 0
	}
	if warmOps > 0 {
		stats.WarmOps = warmOps
	}
	return stats
}

// resumeStubRunner is the deterministic ProcessBackend runner for the
// process-stub backend: it sleeps a fixed quantum and returns a canned
// native reply, so CI measures the real ProcessBackend session/streaming
// code with no PATH binary, no model call, and no credentials.
//
// Unlike a plain Runner it implements standing.SessionRunner, so harness
// kinds with a documented resume surface (codex, openCode, openClaw) exercise
// the same slice-5b record/resume plumbing the live ExecRunner uses: the
// first turn records a deterministic stub native id, the second turn is
// offered it back. The id is a fixed non-credential placeholder and must
// never be mistaken for a live measurement.
type resumeStubRunner struct {
	latency    time.Duration
	reply      string
	nativeID   string
	resumeSeen []string
}

func (s *resumeStubRunner) Run(ctx context.Context, harnessKind, prompt string, emit func(chunk string) error) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(s.latency):
	}
	if emit != nil {
		if err := emit(s.reply); err != nil {
			return "", err
		}
	}
	return s.reply, nil
}

func (s *resumeStubRunner) RunWithResume(ctx context.Context, handle standing.SessionHandle, turnID, prompt, resumeID string, emit func(chunk string) error) (reply, newSessionID string, err error) {
	s.resumeSeen = append(s.resumeSeen, resumeID)
	reply, err = s.Run(ctx, handle.HarnessKind, prompt, emit)
	if err != nil {
		return "", "", err
	}
	return reply, s.nativeID, nil
}

func newBackend(name, harness string) (standing.Backend, error) {
	switch name {
	case "fake", "":
		return standing.NewFakeBackend(), nil
	case "process-stub":
		return standing.NewProcessBackend(&resumeStubRunner{
			latency:  5 * time.Millisecond,
			reply:    "standing stub reply for the latency probe turn",
			nativeID: "stub-ses-latency-probe",
		}), nil
	case "process-exec":
		// Nil runner selects ExecRunner: PATH-resolved harness CLIs with the
		// desktop delegate's filtered env. Fails closed when the CLI for the
		// harness kind is missing — the first measured turn surfaces that.
		return standing.NewProcessBackend(nil), nil
	default:
		return nil, fmt.Errorf("unknown -backend %q (want fake, process-stub, or process-exec)", name)
	}
}

func turnSpec(namespace, threadID, harness string) standing.SessionSpec {
	return standing.SessionSpec{
		Namespace:   namespace,
		ThreadID:    threadID,
		SessionName: standing.SessionNameForThread(threadID),
		HarnessKind: harness,
	}
}

// measureEnsureCold times session creation: one fresh thread per iteration,
// so every EnsureTurnSession takes the cold-create path.
func measureEnsureCold(ctx context.Context, backend standing.Backend, namespace, harness, prefix string, iterations int) ([]time.Duration, error) {
	durations := make([]time.Duration, 0, iterations)
	for i := 0; i < iterations; i++ {
		threadID := fmt.Sprintf("%s-ensure-cold-%d-%d", prefix, time.Now().UnixNano(), i)
		start := time.Now()
		if _, _, err := standing.EnsureTurnSession(ctx, backend, turnSpec(namespace, threadID, harness)); err != nil {
			return nil, err
		}
		durations = append(durations, time.Since(start))
	}
	return durations, nil
}

// measureEnsureWarm times warm resume: the session is pre-bound once, so
// every timed EnsureTurnSession takes the resume path.
func measureEnsureWarm(ctx context.Context, backend standing.Backend, namespace, threadID, harness string, iterations int) ([]time.Duration, int, error) {
	if _, _, err := standing.EnsureTurnSession(ctx, backend, turnSpec(namespace, threadID, harness)); err != nil {
		return nil, 0, err
	}
	durations := make([]time.Duration, 0, iterations)
	warmOps := 0
	for i := 0; i < iterations; i++ {
		start := time.Now()
		_, warm, err := standing.EnsureTurnSession(ctx, backend, turnSpec(namespace, threadID, harness))
		if err != nil {
			return nil, 0, err
		}
		durations = append(durations, time.Since(start))
		if warm {
			warmOps++
		}
	}
	return durations, warmOps, nil
}

type turnSample struct {
	full       time.Duration
	firstToken time.Duration
}

type firstTokenSink struct {
	start     time.Time
	first     time.Time
	delivered int
}

func (s *firstTokenSink) OnToken(_ context.Context, event standing.TokenEvent) error {
	if event.Done {
		return nil
	}
	if s.delivered == 0 {
		s.first = time.Now()
	}
	s.delivered++
	return nil
}

// measureTurnWarm times the full InProcess warm path per turn: warm resume
// plus the streamed turn. The sink records time-to-first-token from
// StreamTurn start, mirroring the Desktop firstTokenMs vocabulary.
func measureTurnWarm(ctx context.Context, backend standing.Backend, namespace, threadID, harness, prompt string, iterations int, runID int64) ([]turnSample, error) {
	if _, _, err := standing.EnsureTurnSession(ctx, backend, turnSpec(namespace, threadID, harness)); err != nil {
		return nil, err
	}
	samples := make([]turnSample, 0, iterations)
	for i := 0; i < iterations; i++ {
		handle, _, err := standing.EnsureTurnSession(ctx, backend, turnSpec(namespace, threadID, harness))
		if err != nil {
			return nil, err
		}
		turnID := fmt.Sprintf("latency-turn-%d-%d", runID, i)
		sink := &firstTokenSink{start: time.Now()}
		start := time.Now()
		if _, err := backend.StreamTurn(ctx, handle, turnID, prompt, sink); err != nil {
			return nil, err
		}
		full := time.Since(start)
		first := full
		if !sink.first.IsZero() {
			first = sink.first.Sub(sink.start)
		}
		samples = append(samples, turnSample{full: full, firstToken: first})
	}
	return samples, nil
}

func fullDurations(samples []turnSample) []time.Duration {
	out := make([]time.Duration, 0, len(samples))
	for _, s := range samples {
		out = append(out, s.full)
	}
	return out
}

func firstTokenDurations(samples []turnSample) []time.Duration {
	out := make([]time.Duration, 0, len(samples))
	for _, s := range samples {
		out = append(out, s.firstToken)
	}
	return out
}

// nativeSessionObserver is the slice-5b introspection surface the latency
// harness uses to confirm a measured turn actually recorded (cold) or reused
// (resumed) a native session id. Only *standing.ProcessBackend implements it;
// FakeBackend has no native ids, so every assertion against it stays cold.
type nativeSessionObserver interface {
	NativeSessionID(namespace, name string) (string, bool)
}

func observedNativeID(backend standing.Backend, namespace, sessionName string) bool {
	observer, ok := backend.(nativeSessionObserver)
	if !ok {
		return false
	}
	_, found := observer.NativeSessionID(namespace, sessionName)
	return found
}

// measureTurnCold times cold first turns: one fresh thread per iteration, so
// every timed StreamTurn is the thread's first turn and the slice-5b native
// session id (when the harness kind supports one) is recorded by this turn,
// never resumed. It returns the timed samples plus how many cold turns left
// a native id behind for the next turn to resume.
func measureTurnCold(ctx context.Context, backend standing.Backend, namespace, harness, prompt, prefix string, iterations int, runID int64) ([]turnSample, int, error) {
	samples := make([]turnSample, 0, iterations)
	nativeIDs := 0
	for i := 0; i < iterations; i++ {
		threadID := fmt.Sprintf("%s-turn-cold-%d-%d", prefix, runID, i)
		spec := turnSpec(namespace, threadID, harness)
		handle, _, err := standing.EnsureTurnSession(ctx, backend, spec)
		if err != nil {
			return nil, 0, err
		}
		turnID := fmt.Sprintf("latency-turn-cold-%d-%d", runID, i)
		sink := &firstTokenSink{start: time.Now()}
		start := time.Now()
		if _, err := backend.StreamTurn(ctx, handle, turnID, prompt, sink); err != nil {
			return nil, 0, err
		}
		full := time.Since(start)
		first := full
		if !sink.first.IsZero() {
			first = sink.first.Sub(sink.start)
		}
		samples = append(samples, turnSample{full: full, firstToken: first})
		if observedNativeID(backend, namespace, handle.SessionName) {
			nativeIDs++
		}
	}
	return samples, nativeIDs, nil
}

// measureTurnResumed times resumed second turns: one fresh thread per
// iteration, an untimed cold first turn to record the slice-5b native session
// id, then a timed second turn that resumes it. Unsupported harness kinds
// (and FakeBackend) record nothing, so their "resumed" turn is honestly a
// second cold turn — the nativeResume section of the report says so instead
// of the timing pretending otherwise.
func measureTurnResumed(ctx context.Context, backend standing.Backend, namespace, harness, prompt, prefix string, iterations int, runID int64) ([]turnSample, int, error) {
	samples := make([]turnSample, 0, iterations)
	nativeIDs := 0
	for i := 0; i < iterations; i++ {
		threadID := fmt.Sprintf("%s-turn-resumed-%d-%d", prefix, runID, i)
		spec := turnSpec(namespace, threadID, harness)
		handle, _, err := standing.EnsureTurnSession(ctx, backend, spec)
		if err != nil {
			return nil, 0, err
		}
		if _, err := backend.StreamTurn(ctx, handle, fmt.Sprintf("latency-turn-resume-cold-%d-%d", runID, i), prompt, nil); err != nil {
			return nil, 0, err
		}
		handle, _, err = standing.EnsureTurnSession(ctx, backend, spec)
		if err != nil {
			return nil, 0, err
		}
		turnID := fmt.Sprintf("latency-turn-resumed-%d-%d", runID, i)
		sink := &firstTokenSink{start: time.Now()}
		start := time.Now()
		if _, err := backend.StreamTurn(ctx, handle, turnID, prompt, sink); err != nil {
			return nil, 0, err
		}
		full := time.Since(start)
		first := full
		if !sink.first.IsZero() {
			first = sink.first.Sub(sink.start)
		}
		samples = append(samples, turnSample{full: full, firstToken: first})
		if observedNativeID(backend, namespace, handle.SessionName) {
			nativeIDs++
		}
	}
	return samples, nativeIDs, nil
}

// scenarioGroups maps each report scenario to the measurement group that
// produces it, so -only can skip expensive live subprocess turns without
// changing the shape of the scenarios it does collect.
func scenarioGroups() map[string]string {
	return map[string]string{
		"directEnsureCold": "ensureCold", "peerEnsureCold": "ensureCold",
		"directEnsureWarm": "ensureWarm", "peerEnsureWarm": "ensureWarm",
		"directTurnWarm": "turnWarm", "directFirstToken": "turnWarm",
		"peerTurnWarm": "turnWarm", "peerFirstToken": "turnWarm",
		"directTurnCold": "turnCold", "directFirstTokenCold": "turnCold",
		"peerTurnCold": "turnCold", "peerFirstTokenCold": "turnCold",
		"directTurnResumed": "turnResumed", "directFirstTokenResumed": "turnResumed",
		"peerTurnResumed": "turnResumed", "peerFirstTokenResumed": "turnResumed",
	}
}

func wantedGroups(only map[string]bool) map[string]bool {
	groups := scenarioGroups()
	if len(only) == 0 {
		out := map[string]bool{}
		for _, group := range groups {
			out[group] = true
		}
		return out
	}
	out := map[string]bool{}
	for name := range only {
		if group, ok := groups[name]; ok {
			out[group] = true
		}
	}
	return out
}

func parseOnly(raw string) (map[string]bool, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}
	groups := scenarioGroups()
	out := map[string]bool{}
	for _, name := range strings.Split(trimmed, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := groups[name]; !ok {
			return nil, fmt.Errorf("unknown scenario %q (want one of directEnsureCold, directEnsureWarm, directTurnWarm, directFirstToken, directTurnCold, directFirstTokenCold, directTurnResumed, directFirstTokenResumed, and the four peer… peers)", name)
		}
		out[name] = true
	}
	return out, nil
}

// judge applies the standing promote/reshape/retire bars. Deterministic
// backends (fake, process-stub) only prove the harness and the warm-reuse
// contract, so they always report harness-ok: the bars need process-exec
// live numbers on the same cluster shape as the Job baseline.
func judge(backend string, scenarios map[string]scenarioStats, jobP50, jobP95 float64) (string, string) {
	if jobP50 <= 0 && jobP95 <= 0 {
		return "no-baseline", "no Job baseline flags were passed, so no comparison or bars verdict applies"
	}
	if backend != "process-exec" {
		return "harness-ok", "deterministic " + backend + " backend proves the harness and warm-reuse contract only; " +
			"promote/reshape/retire needs process-exec live numbers on the same cluster shape as the Job baseline"
	}
	directTurn, directOK := scenarios["directTurnWarm"]
	peerTurn, peerOK := scenarios["peerTurnWarm"]
	directFirst, directFirstOK := scenarios["directFirstToken"]
	peerFirst, peerFirstOK := scenarios["peerFirstToken"]
	if !directOK || !peerOK || !directFirstOK || !peerFirstOK {
		return "inconclusive-live", "subset measurement (-only): the promote/reshape/retire bars need the full directTurnWarm + peerTurnWarm scenarios, re-run without -only for a verdict"
	}
	// Promote: warm turn p95 an order of magnitude under the Job p95 for BOTH
	// direct and peer, with first-token p95 in the low single seconds.
	if jobP95 > 0 && directTurn.P95Ms <= jobP95/10 && peerTurn.P95Ms <= jobP95/10 &&
		directFirst.P95Ms <= 5000 && peerFirst.P95Ms <= 5000 {
		return "promote", fmt.Sprintf("warm turn p95 direct %.1fms / peer %.1fms <= job p95 %.0fms/10 with first-token p95 direct %.1fms / peer %.1fms <= 5000ms",
			directTurn.P95Ms, peerTurn.P95Ms, jobP95, directFirst.P95Ms, peerFirst.P95Ms)
	}
	// Reshape: direct wins clearly but peer does not — keep the standing plane
	// for direct turns and route peer fanout back to Jobs (or reshape the peer
	// path) before promoting peers.
	if jobP95 > 0 && directTurn.P95Ms <= jobP95/10 && peerTurn.P95Ms > jobP95/10 {
		return "reshape", fmt.Sprintf("direct turn p95 %.1fms wins vs job p95 %.0fms but peer turn p95 %.1fms does not; keep standing for direct, route peers to Jobs or reshape the peer path",
			directTurn.P95Ms, jobP95, peerTurn.P95Ms)
	}
	// Retire: warm turn p50 within noise (under a 2x win) of the Job p50.
	if jobP50 > 0 && directTurn.P50Ms >= jobP50/2 {
		return "retire", fmt.Sprintf("warm turn p50 %.1fms is within noise of job p50 %.0fms (under a 2x win); the standing plane does not pay for itself",
			directTurn.P50Ms, jobP50)
	}
	return "inconclusive-live", "live numbers land between the bars; collect more iterations on the same cluster shape before deciding"
}

func run(ctx context.Context, cfg config) (*latencyReport, error) {
	if cfg.iterations < 1 {
		return nil, fmt.Errorf("-n must be at least 1")
	}
	if strings.TrimSpace(cfg.namespace) == "" {
		return nil, fmt.Errorf("-namespace is required")
	}
	backend, err := newBackend(cfg.backend, cfg.harness)
	if err != nil {
		return nil, err
	}
	backendName := cfg.backend
	if backendName == "" {
		backendName = "fake"
	}
	groups := wantedGroups(cfg.only)
	runID := time.Now().UnixNano()
	scenarios := map[string]scenarioStats{}

	if groups["ensureCold"] {
		directCold, err := measureEnsureCold(ctx, backend, cfg.namespace, cfg.harness, "direct", cfg.iterations)
		if err != nil {
			return nil, fmt.Errorf("direct ensure cold: %w", err)
		}
		scenarios["directEnsureCold"] = summarize(directCold, 0)
		peerCold, err := measureEnsureCold(ctx, backend, cfg.namespace, cfg.harness, "peer-child", cfg.iterations)
		if err != nil {
			return nil, fmt.Errorf("peer ensure cold: %w", err)
		}
		scenarios["peerEnsureCold"] = summarize(peerCold, 0)
	}

	var directWarmOps, peerWarmEnsureOps int
	if groups["ensureWarm"] {
		directWarm, ops, err := measureEnsureWarm(ctx, backend, cfg.namespace, fmt.Sprintf("latency-direct-thread-%d", runID), cfg.harness, cfg.iterations)
		if err != nil {
			return nil, fmt.Errorf("direct ensure warm: %w", err)
		}
		directWarmOps = ops
		scenarios["directEnsureWarm"] = summarize(directWarm, ops)
		peerWarmEnsure, ops, err := measureEnsureWarm(ctx, backend, cfg.namespace, fmt.Sprintf("latency-peer-child-thread-%d", runID), cfg.harness, cfg.iterations)
		if err != nil {
			return nil, fmt.Errorf("peer ensure warm: %w", err)
		}
		peerWarmEnsureOps = ops
		scenarios["peerEnsureWarm"] = summarize(peerWarmEnsure, ops)
	}

	native := &nativeResumeReport{Supported: standing.SupportsNativeResume(cfg.harness) && backendName != "fake"}
	wants := func(names ...string) bool {
		if len(cfg.only) == 0 {
			return true
		}
		for _, name := range names {
			if cfg.only[name] {
				return true
			}
		}
		return false
	}
	if groups["turnWarm"] {
		if wants("directTurnWarm", "directFirstToken") {
			directTurns, err := measureTurnWarm(ctx, backend, cfg.namespace, fmt.Sprintf("latency-direct-thread-%d", runID), cfg.harness, cfg.prompt, cfg.iterations, runID)
			if err != nil {
				return nil, fmt.Errorf("direct turn warm: %w", err)
			}
			scenarios["directTurnWarm"] = summarize(fullDurations(directTurns), directWarmOps)
			scenarios["directFirstToken"] = summarize(firstTokenDurations(directTurns), 0)
		}
		if wants("peerTurnWarm", "peerFirstToken") {
			peerTurns, err := measureTurnWarm(ctx, backend, cfg.namespace, fmt.Sprintf("latency-peer-child-thread-%d", runID), cfg.harness, cfg.prompt, cfg.iterations, runID)
			if err != nil {
				return nil, fmt.Errorf("peer turn warm: %w", err)
			}
			scenarios["peerTurnWarm"] = summarize(fullDurations(peerTurns), peerWarmEnsureOps)
			scenarios["peerFirstToken"] = summarize(firstTokenDurations(peerTurns), 0)
		}
	}
	if groups["turnCold"] {
		if wants("directTurnCold", "directFirstTokenCold") {
			directColdTurns, directColdIDs, err := measureTurnCold(ctx, backend, cfg.namespace, cfg.harness, cfg.prompt, "direct", cfg.iterations, runID)
			if err != nil {
				return nil, fmt.Errorf("direct turn cold: %w", err)
			}
			scenarios["directTurnCold"] = summarize(fullDurations(directColdTurns), 0)
			scenarios["directFirstTokenCold"] = summarize(firstTokenDurations(directColdTurns), 0)
			native.ColdTurns += cfg.iterations
			native.NativeIDsObserved += directColdIDs
		}
		if wants("peerTurnCold", "peerFirstTokenCold") {
			peerColdTurns, peerColdIDs, err := measureTurnCold(ctx, backend, cfg.namespace, cfg.harness, cfg.prompt, "peer-child", cfg.iterations, runID)
			if err != nil {
				return nil, fmt.Errorf("peer turn cold: %w", err)
			}
			scenarios["peerTurnCold"] = summarize(fullDurations(peerColdTurns), 0)
			scenarios["peerFirstTokenCold"] = summarize(firstTokenDurations(peerColdTurns), 0)
			native.ColdTurns += cfg.iterations
			native.NativeIDsObserved += peerColdIDs
		}
	}

	if groups["turnResumed"] {
		if wants("directTurnResumed", "directFirstTokenResumed") {
			directResumed, directResumedIDs, err := measureTurnResumed(ctx, backend, cfg.namespace, cfg.harness, cfg.prompt, "direct", cfg.iterations, runID)
			if err != nil {
				return nil, fmt.Errorf("direct turn resumed: %w", err)
			}
			scenarios["directTurnResumed"] = summarize(fullDurations(directResumed), cfg.iterations)
			scenarios["directFirstTokenResumed"] = summarize(firstTokenDurations(directResumed), 0)
			native.ResumedTurns += cfg.iterations
			native.NativeIDsObserved += directResumedIDs
		}
		if wants("peerTurnResumed", "peerFirstTokenResumed") {
			peerResumed, peerResumedIDs, err := measureTurnResumed(ctx, backend, cfg.namespace, cfg.harness, cfg.prompt, "peer-child", cfg.iterations, runID)
			if err != nil {
				return nil, fmt.Errorf("peer turn resumed: %w", err)
			}
			scenarios["peerTurnResumed"] = summarize(fullDurations(peerResumed), cfg.iterations)
			scenarios["peerFirstTokenResumed"] = summarize(firstTokenDurations(peerResumed), 0)
			native.ResumedTurns += cfg.iterations
			native.NativeIDsObserved += peerResumedIDs
		}
	}
	switch {
	case backendName == "fake":
		native.Note = "fake backend records no native session ids: resumed second turns are second cold turns through the same warm process-local session"
	case !standing.SupportsNativeResume(cfg.harness):
		native.Note = "harness kind " + strconv.Quote(cfg.harness) + " has no documented resume surface (Fake-only or single-turn): resumed second turns run the cold path, only the process-local warm-reuse win applies"
	case backendName == "process-stub":
		native.Note = "deterministic stub native id (stub-ses-latency-probe): proves the record/resume plumbing only, never a live measurement"
	}

	// A filtered (-only) run keeps only the requested scenarios so an
	// expensive live CLI can probe cold+resume turns without paying for the
	// full matrix.
	if len(cfg.only) > 0 {
		filtered := map[string]scenarioStats{}
		for name := range cfg.only {
			if stats, ok := scenarios[name]; ok {
				filtered[name] = stats
			}
		}
		scenarios = filtered
	}

	report := &latencyReport{
		Tool:         "standing-latency",
		Backend:      backendName,
		Live:         backendName == "process-exec",
		Iterations:   cfg.iterations,
		Namespace:    cfg.namespace,
		Harness:      cfg.harness,
		GeneratedAt:  time.Now().UTC().Format(time.RFC3339Nano),
		Scenarios:    scenarios,
		NativeResume: native,
	}
	if cfg.jobP50 > 0 || cfg.jobP95 > 0 {
		report.JobBaseline = &scenarioStats{Count: cfg.iterations, P50Ms: cfg.jobP50, P95Ms: cfg.jobP95}
		report.ComparisonMs = map[string]float64{}
		if stats, ok := scenarios["directTurnWarm"]; ok {
			report.ComparisonMs["directTurnWarmSavedVsJobP50"] = cfg.jobP50 - stats.P50Ms
			report.ComparisonMs["directTurnWarmSavedVsJobP95"] = cfg.jobP95 - stats.P95Ms
		}
		if stats, ok := scenarios["peerTurnWarm"]; ok {
			report.ComparisonMs["peerTurnWarmSavedVsJobP50"] = cfg.jobP50 - stats.P50Ms
			report.ComparisonMs["peerTurnWarmSavedVsJobP95"] = cfg.jobP95 - stats.P95Ms
		}
		if stats, ok := scenarios["directFirstToken"]; ok {
			report.ComparisonMs["directFirstTokenSavedVsJobP50"] = cfg.jobP50 - stats.P50Ms
			report.ComparisonMs["directFirstTokenSavedVsJobP95"] = cfg.jobP95 - stats.P95Ms
		}
		if stats, ok := scenarios["peerFirstToken"]; ok {
			report.ComparisonMs["peerFirstTokenSavedVsJobP50"] = cfg.jobP50 - stats.P50Ms
			report.ComparisonMs["peerFirstTokenSavedVsJobP95"] = cfg.jobP95 - stats.P95Ms
		}
		if stats, ok := scenarios["directTurnCold"]; ok {
			report.ComparisonMs["directTurnColdSavedVsJobP50"] = cfg.jobP50 - stats.P50Ms
			report.ComparisonMs["directTurnColdSavedVsJobP95"] = cfg.jobP95 - stats.P95Ms
		}
		if stats, ok := scenarios["directTurnResumed"]; ok {
			report.ComparisonMs["directTurnResumedSavedVsJobP50"] = cfg.jobP50 - stats.P50Ms
			report.ComparisonMs["directTurnResumedSavedVsJobP95"] = cfg.jobP95 - stats.P95Ms
		}
		if stats, ok := scenarios["peerTurnCold"]; ok {
			report.ComparisonMs["peerTurnColdSavedVsJobP50"] = cfg.jobP50 - stats.P50Ms
			report.ComparisonMs["peerTurnColdSavedVsJobP95"] = cfg.jobP95 - stats.P95Ms
		}
		if stats, ok := scenarios["peerTurnResumed"]; ok {
			report.ComparisonMs["peerTurnResumedSavedVsJobP50"] = cfg.jobP50 - stats.P50Ms
			report.ComparisonMs["peerTurnResumedSavedVsJobP95"] = cfg.jobP95 - stats.P95Ms
		}
	}
	report.Verdict, report.VerdictReason = judge(backendName, scenarios, cfg.jobP50, cfg.jobP95)
	return report, nil
}

func main() {
	cfg := config{}
	backend := flag.String("backend", "fake", "Standing backend for the measurement: fake (deterministic, CI-safe), process-stub (real ProcessBackend code with a stub runner), or process-exec (real harness CLI on PATH, local-only).")
	iterations := flag.Int("n", 20, "Timed operations per scenario.")
	namespace := flag.String("namespace", "agents", "Session namespace for the measurement.")
	harness := flag.String("harness", "openCode", "Harness adapter kind carried on the session spec (must name a process recipe for process-exec).")
	prompt := flag.String("prompt", "standing latency probe: reply briefly", "Frozen prompt text streamed for each measured turn.")
	jobP50 := flag.Float64("job-baseline-p50-ms", 12000, "Documented Job create-to-Pod-ready p50 in ms for comparison (0 omits the baseline).")
	jobP95 := flag.Float64("job-baseline-p95-ms", 44000, "Documented Job create-to-Pod-ready p95 in ms for comparison (0 omits the baseline).")
	outPath := flag.String("out", "", "Optional JSON report path (written with 0600 permissions).")
	onlyRaw := flag.String("only", "", "Optional comma-separated scenario subset (e.g. directTurnCold,directTurnResumed) for cheap live probes; empty measures all sixteen scenarios.")
	chatJSONL := flag.String("chat-jsonl", "", "Score Desktop signed-in standing JSONL (e.g. .runtime/chat-latency.jsonl) against the Slice 5a bars instead of running the backend matrix. Filters to -chat-source on -chat-path and compares sendToFirstTokenMs/replyReadyMs to the Job baseline flags.")
	chatSource := flag.String("chat-source", "desktop-chat-live-signed-in", "JSONL source label to score in chat-bars mode.")
	chatPath := flag.String("chat-path", "standing", "JSONL path tag to score in chat-bars mode (empty scores every path).")
	minSamples := flag.Int("min-samples", defaultChatMinSamples, "Minimum delivered standing samples for a promote/retire call in chat-bars mode; fewer reports inconclusive-live.")
	flag.Parse()

	if strings.TrimSpace(*chatJSONL) != "" {
		if *minSamples < 0 {
			fmt.Fprintf(os.Stderr, "error: -min-samples must be at least 0\n")
			os.Exit(1)
		}
		chatReport, err := scoreChatJSONL(*chatJSONL, strings.TrimSpace(*chatSource), strings.TrimSpace(*chatPath), *jobP50, *jobP95, *minSamples)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		encoded, err := json.MarshalIndent(chatReport, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: encode report: %v\n", err)
			os.Exit(1)
		}
		encoded = append(encoded, '\n')
		if trimmed := strings.TrimSpace(*outPath); trimmed != "" {
			if err := os.WriteFile(trimmed, encoded, 0o600); err != nil {
				fmt.Fprintf(os.Stderr, "error: write report: %v\n", err)
				os.Exit(1)
			}
		}
		if _, err := os.Stdout.Write(encoded); err != nil {
			fmt.Fprintf(os.Stderr, "error: write stdout: %v\n", err)
			os.Exit(1)
		}
		return
	}

	only, err := parseOnly(*onlyRaw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	cfg = config{
		iterations: *iterations,
		namespace:  *namespace,
		harness:    *harness,
		prompt:     *prompt,
		backend:    *backend,
		jobP50:     *jobP50,
		jobP95:     *jobP95,
		outPath:    *outPath,
		only:       only,
	}

	report, err := run(context.Background(), cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: encode report: %v\n", err)
		os.Exit(1)
	}
	encoded = append(encoded, '\n')
	if trimmed := strings.TrimSpace(cfg.outPath); trimmed != "" {
		if err := os.WriteFile(trimmed, encoded, 0o600); err != nil {
			fmt.Fprintf(os.Stderr, "error: write report: %v\n", err)
			os.Exit(1)
		}
	}
	if _, err := os.Stdout.Write(encoded); err != nil {
		fmt.Fprintf(os.Stderr, "error: write stdout: %v\n", err)
		os.Exit(1)
	}
}
