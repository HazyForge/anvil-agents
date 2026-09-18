// Command standing-latency measures the standing InProcess warm path
// (session resume + streamed turn) for a direct turn AND a peer delivery,
// against the documented Job cold-start baseline.
//
// Fake-backend by default: it drives standing.FakeBackend (or a stubbed
// ProcessBackend runner), so the numbers are deterministic and CI-safe.
// They prove the harness and the warm-reuse contract only — never a
// promote/reshape/retire decision. The optional live path
// (-backend process-exec) runs one real harness subprocess per measured turn
// through standing.ProcessBackend with a PATH-resolved CLI, exactly like the
// slice-3b turn path; that path is local-only (needs the harness CLI and its
// own auth home) and never reads Secrets.
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
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"sort"
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
	JobBaseline   *scenarioStats           `json:"jobBaseline,omitempty"`
	ComparisonMs  map[string]float64       `json:"comparisonMs,omitempty"`
	Verdict       string                   `json:"verdict"`
	VerdictReason string                   `json:"verdictReason"`
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

// stubRunner is the deterministic ProcessBackend runner for the
// process-stub backend: it sleeps a fixed quantum and returns a canned
// native reply, so CI measures the real ProcessBackend session/streaming
// code with no PATH binary, no model call, and no credentials.
type stubRunner struct {
	latency time.Duration
	reply   string
}

func (s stubRunner) Run(ctx context.Context, harnessKind, prompt string, emit func(chunk string) error) (string, error) {
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

func newBackend(name, harness string) (standing.Backend, error) {
	switch name {
	case "fake", "":
		return standing.NewFakeBackend(), nil
	case "process-stub":
		return standing.NewProcessBackend(stubRunner{
			latency: 5 * time.Millisecond,
			reply:   "standing stub reply for the latency probe turn",
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
	directTurn := scenarios["directTurnWarm"]
	peerTurn := scenarios["peerTurnWarm"]
	directFirst := scenarios["directFirstToken"]
	peerFirst := scenarios["peerFirstToken"]
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

	directCold, err := measureEnsureCold(ctx, backend, cfg.namespace, cfg.harness, "direct", cfg.iterations)
	if err != nil {
		return nil, fmt.Errorf("direct ensure cold: %w", err)
	}
	runID := time.Now().UnixNano()
	directWarm, directWarmOps, err := measureEnsureWarm(ctx, backend, cfg.namespace, fmt.Sprintf("latency-direct-thread-%d", runID), cfg.harness, cfg.iterations)
	if err != nil {
		return nil, fmt.Errorf("direct ensure warm: %w", err)
	}
	directTurns, err := measureTurnWarm(ctx, backend, cfg.namespace, fmt.Sprintf("latency-direct-thread-%d", runID), cfg.harness, cfg.prompt, cfg.iterations, runID)
	if err != nil {
		return nil, fmt.Errorf("direct turn warm: %w", err)
	}
	peerCold, err := measureEnsureCold(ctx, backend, cfg.namespace, cfg.harness, "peer-child", cfg.iterations)
	if err != nil {
		return nil, fmt.Errorf("peer ensure cold: %w", err)
	}
	peerWarmEnsure, peerWarmEnsureOps, err := measureEnsureWarm(ctx, backend, cfg.namespace, fmt.Sprintf("latency-peer-child-thread-%d", runID), cfg.harness, cfg.iterations)
	if err != nil {
		return nil, fmt.Errorf("peer ensure warm: %w", err)
	}
	peerTurns, err := measureTurnWarm(ctx, backend, cfg.namespace, fmt.Sprintf("latency-peer-child-thread-%d", runID), cfg.harness, cfg.prompt, cfg.iterations, runID)
	if err != nil {
		return nil, fmt.Errorf("peer turn warm: %w", err)
	}

	scenarios := map[string]scenarioStats{
		"directEnsureCold": summarize(directCold, 0),
		"directEnsureWarm": summarize(directWarm, directWarmOps),
		"directTurnWarm":   summarize(fullDurations(directTurns), directWarmOps),
		"directFirstToken": summarize(firstTokenDurations(directTurns), 0),
		"peerEnsureCold":   summarize(peerCold, 0),
		"peerEnsureWarm":   summarize(peerWarmEnsure, peerWarmEnsureOps),
		"peerTurnWarm":     summarize(fullDurations(peerTurns), peerWarmEnsureOps),
		"peerFirstToken":   summarize(firstTokenDurations(peerTurns), 0),
	}

	report := &latencyReport{
		Tool:        "standing-latency",
		Backend:     backendName,
		Live:        backendName == "process-exec",
		Iterations:  cfg.iterations,
		Namespace:   cfg.namespace,
		Harness:     cfg.harness,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Scenarios:   scenarios,
	}
	if cfg.jobP50 > 0 || cfg.jobP95 > 0 {
		report.JobBaseline = &scenarioStats{Count: cfg.iterations, P50Ms: cfg.jobP50, P95Ms: cfg.jobP95}
		report.ComparisonMs = map[string]float64{
			"directTurnWarmSavedVsJobP50":   cfg.jobP50 - scenarios["directTurnWarm"].P50Ms,
			"directTurnWarmSavedVsJobP95":   cfg.jobP95 - scenarios["directTurnWarm"].P95Ms,
			"peerTurnWarmSavedVsJobP50":     cfg.jobP50 - scenarios["peerTurnWarm"].P50Ms,
			"peerTurnWarmSavedVsJobP95":     cfg.jobP95 - scenarios["peerTurnWarm"].P95Ms,
			"directFirstTokenSavedVsJobP50": cfg.jobP50 - scenarios["directFirstToken"].P50Ms,
			"directFirstTokenSavedVsJobP95": cfg.jobP95 - scenarios["directFirstToken"].P95Ms,
			"peerFirstTokenSavedVsJobP50":   cfg.jobP50 - scenarios["peerFirstToken"].P50Ms,
			"peerFirstTokenSavedVsJobP95":   cfg.jobP95 - scenarios["peerFirstToken"].P95Ms,
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
	flag.Parse()

	cfg = config{
		iterations: *iterations,
		namespace:  *namespace,
		harness:    *harness,
		prompt:     *prompt,
		backend:    *backend,
		jobP50:     *jobP50,
		jobP95:     *jobP95,
		outPath:    *outPath,
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
