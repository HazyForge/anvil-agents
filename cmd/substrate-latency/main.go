// Command substrate-latency measures warm actor resume versus cold actor
// create for standing-chat turns, covering both direct turns and peer
// deliveries (Substrate spike), plus the busy-recipient durable-wait peer
// path (peerBusyWaitWarm).
//
// Fake-backend by default: without the live gate it drives the in-memory
// FakeClient, so CI stays green with no cluster. With the live gate on
// (ANVIL_AGENTS_SUBSTRATE_ACTORS_ENABLED + ANVIL_AGENTS_SUBSTRATE_ENDPOINT)
// it dials ateapi over gRPC — verified TLS before any bearer token, token
// from ANVIL_AGENTS_SUBSTRATE_TOKEN_FILE (or the inline token env, or a
// minted ServiceAccount token), Kind-only skip-verify TLS behind
// ANVIL_AGENTS_SUBSTRATE_INSECURE for a loopback port-forward — and measures
// real Create/Resume timings. See docs/substrate-spike.md for the Kind
// port-forward setup. The busy-recipient scenario occupies one warm
// recipient, queues a peer delivery that waits, then resumes the same actor
// (never drop, no second Create) on FakeClient and on the live gate alike.
//
// The Job cold-start baseline cannot be measured from this process (it needs a
// real cluster scheduler), so pass the observed baseline explicitly with
// -job-baseline-p50-ms / -job-baseline-p95-ms (Desktop chat-latency JSONL or
// Job create-to-ready timings; Primaris measured ~8-44s). The report keeps
// p50/p95 fields for every scenario plus the comparison.
//
// Honest-unavailable probing: -probe-only dials ateapi once (GetActor for an
// absent probe name) and reports {"reachable":true/false} as JSON with no
// timings at all — NotFound for the absent probe counts as reachable because
// the server answered. Use it before any --live run so an unreachable gateway
// surfaces as reachable:false instead of fabricated numbers.
//
// Usage:
//
//	go run ./cmd/substrate-latency -n 20 -out /tmp/substrate-latency.json
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

	"github.com/hazyforge/anvil-agents/internal/substrate"
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
	Tool         string                   `json:"tool"`
	Backend      string                   `json:"backend"`
	GateEnabled  bool                     `json:"gateEnabled"`
	Iterations   int                      `json:"iterations"`
	Namespace    string                   `json:"namespace"`
	BusyHoldMs   float64                  `json:"busyHoldMs,omitempty"`
	GeneratedAt  string                   `json:"generatedAt"`
	Scenarios    map[string]scenarioStats `json:"scenarios"`
	JobBaseline  *scenarioStats           `json:"jobBaseline,omitempty"`
	ComparisonMs map[string]float64       `json:"comparisonMs,omitempty"`
}

type measureConfig struct {
	iterations int
	namespace  string
	harness    string
	actorClass string
	pool       string
	busyHold   time.Duration
	jobP50     float64
	jobP95     float64
}

// probeReport answers "is live Kind ATE reachable?" with no latency numbers
// at all. reachable=true means ateapi answered a GetActor probe (either with
// the actor or with NotFound for the absent probe name); reachable=false
// carries the dial/probe error so callers never mistake an unavailable
// gateway for fast warm resume. Token material never appears in the report —
// only the endpoint address does.
type probeReport struct {
	Tool        string `json:"tool"`
	Backend     string `json:"backend"`
	Endpoint    string `json:"endpoint"`
	Reachable   bool   `json:"reachable"`
	Error       string `json:"error,omitempty"`
	GeneratedAt string `json:"generatedAt"`
}

// evaluateProbeTarget resolves the live gate to a dial config, or returns the
// honest reason the gateway cannot be probed (gate off, missing template).
// Pure so unit tests can pin the gate-off messaging without a cluster.
func evaluateProbeTarget(gate substrate.GateConfig) (substrate.ATEClientConfig, string, bool) {
	if !gate.LiveEnabled() {
		missing := substrate.GateEnabledEnvVar + "=true"
		if strings.TrimSpace(gate.Endpoint) == "" {
			missing += " + " + substrate.GateEndpointEnvVar
		}
		return substrate.ATEClientConfig{}, "live gate off (need " + missing + ")", false
	}
	ateCfg := substrate.ATEClientConfigFromGate(gate)
	if err := ateCfg.Validate(); err != nil {
		return substrate.ATEClientConfig{}, err.Error(), false
	}
	return ateCfg, "", true
}

// runProbe dials ateapi per the live gate and issues one GetActor for a probe
// name that is expected to be absent. It always writes the JSON report (to
// -out when set, plus stdout) and exits 0 when ateapi answered, 1 otherwise.
func runProbe(ctx context.Context, namespace, outPath string) {
	gate := substrate.GateConfigFromEnv()
	ateCfg, gateErr, ok := evaluateProbeTarget(gate)
	report := probeReport{Tool: "substrate-latency-probe", Backend: "ate-live", Endpoint: ateCfg.Address, GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	exitCode := 1
	switch {
	case !ok:
		report.Error = gateErr
	default:
		probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		control, closeConn, err := substrate.DialATEControl(probeCtx, ateCfg, substrate.ATEDialOptions{})
		if err != nil {
			report.Error = err.Error()
		} else {
			defer closeConn()
			atespace := strings.TrimSpace(gate.Atespace)
			if atespace == "" {
				atespace = strings.TrimSpace(namespace)
			}
			_, probeErr := control.GetActor(probeCtx, substrate.ATEObjectRef{Atespace: atespace, Name: "anvil-substrate-probe"})
			if probeErr == nil || substrate.IsATENotFound(probeErr) {
				report.Reachable = true
				exitCode = 0
			} else {
				report.Error = probeErr.Error()
			}
		}
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: encode probe report: %v\n", err)
		os.Exit(1)
	}
	encoded = append(encoded, '\n')
	if trimmed := strings.TrimSpace(outPath); trimmed != "" {
		if err := os.WriteFile(trimmed, encoded, 0o600); err != nil {
			fmt.Fprintf(os.Stderr, "error: write probe report: %v\n", err)
			os.Exit(1)
		}
	}
	if _, err := os.Stdout.Write(encoded); err != nil {
		fmt.Fprintf(os.Stderr, "error: write stdout: %v\n", err)
		os.Exit(1)
	}
	os.Exit(exitCode)
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

func measureCold(ctx context.Context, client substrate.Client, namespace, prefix, harness, class, pool string, iterations int) ([]time.Duration, error) {
	durations := make([]time.Duration, 0, iterations)
	for i := 0; i < iterations; i++ {
		threadID := fmt.Sprintf("%s-measure-%d-%d", prefix, time.Now().UnixNano(), i)
		spec := substrate.ActorSpecForRun(namespace, substrate.ActorNameForThread(threadID), harness, class, pool, nil)
		start := time.Now()
		if _, err := client.CreateActor(ctx, spec); err != nil {
			return nil, err
		}
		durations = append(durations, time.Since(start))
		// Release the worker before the next iteration: Create returns while
		// the actor still occupies a worker, so without a suspend a small
		// WorkerPool exhausts (ResourceExhausted) before warm scenarios run.
		// The suspend is outside the timed sample, matching warm which times
		// Ensure/Resume only.
		if _, err := substrate.SuspendIdleActor(ctx, client, namespace, spec.Name, nil); err != nil {
			return nil, err
		}
	}
	return durations, nil
}

func measureWarm(ctx context.Context, client substrate.Client, namespace, threadID, harness, class, pool string, iterations int) ([]time.Duration, int, error) {
	spec := substrate.ActorSpecForRun(namespace, substrate.ActorNameForThread(threadID), harness, class, pool, nil)
	// Pre-bind once so every timed op is a warm resume, not the cold create.
	if _, _, err := substrate.EnsureTurnActor(ctx, client, spec); err != nil {
		return nil, 0, err
	}
	if _, err := substrate.SuspendIdleActor(ctx, client, namespace, spec.Name, nil); err != nil {
		return nil, 0, err
	}
	durations := make([]time.Duration, 0, iterations)
	warmOps := 0
	for i := 0; i < iterations; i++ {
		start := time.Now()
		_, warm, err := substrate.EnsureTurnActor(ctx, client, spec)
		if err != nil {
			return nil, 0, err
		}
		durations = append(durations, time.Since(start))
		if warm {
			warmOps++
		}
		if _, err := substrate.SuspendIdleActor(ctx, client, namespace, spec.Name, nil); err != nil {
			return nil, 0, err
		}
	}
	return durations, warmOps, nil
}

// measurePeerBusyWaitWarm occupies one recipient actor with a warm turn,
// then times a queued peer delivery that durable-waits until that turn
// completes and resumes the same actor. The path never drops the waiter and
// never issues a second Create (CountingClient stays at 1). Works against
// FakeClient (CI) and a live ATE Client (kind-substrate-spike --live).
func measurePeerBusyWaitWarm(ctx context.Context, client substrate.Client, namespace, threadID, harness, class, pool string, iterations int, busyHold time.Duration) ([]time.Duration, int, error) {
	if busyHold < 0 {
		return nil, 0, fmt.Errorf("busy hold must not be negative")
	}
	counter := &substrate.CountingClient{Client: client}
	spec := substrate.ActorSpecForRun(namespace, substrate.ActorNameForThread(threadID), harness, class, pool, nil)
	// Pre-bind once so Occupy is a warm turn, not a cold create.
	bound, warm, err := substrate.EnsureTurnActor(ctx, counter, spec)
	if err != nil {
		return nil, 0, err
	}
	if warm {
		return nil, 0, fmt.Errorf("busy-wait pre-bind resumed an existing actor; want a dedicated recipient")
	}
	if bound.ID == "" {
		return nil, 0, fmt.Errorf("busy-wait pre-bind returned an empty actor id")
	}

	occ := substrate.NewBusyRecipient(spec)
	durations := make([]time.Duration, 0, iterations)
	warmOps := 0
	for i := 0; i < iterations; i++ {
		if _, occupyWarm, err := occ.Occupy(ctx, counter); err != nil {
			return nil, 0, err
		} else if !occupyWarm {
			return nil, 0, fmt.Errorf("busy-wait occupy %d created a new actor instead of a warm turn", i)
		}
		released := make(chan error, 1)
		go func() {
			if busyHold > 0 {
				select {
				case <-ctx.Done():
					released <- ctx.Err()
					return
				case <-time.After(busyHold):
				}
			}
			released <- occ.Release(ctx, counter)
		}()
		start := time.Now()
		resumed, waitWarm, err := occ.WaitThenEnsure(ctx, counter)
		if err != nil {
			return nil, 0, err
		}
		if relErr := <-released; relErr != nil {
			return nil, 0, relErr
		}
		if !waitWarm {
			return nil, 0, fmt.Errorf("busy-wait peer delivery %d created a new actor instead of warm resume", i)
		}
		if resumed.ID != bound.ID {
			return nil, 0, fmt.Errorf("busy-wait resume identity %q != occupied %q", resumed.ID, bound.ID)
		}
		durations = append(durations, time.Since(start))
		warmOps++
	}
	if got := counter.Creates(); got != 1 {
		return nil, 0, fmt.Errorf("busy-wait path issued %d CreateActor calls, want exactly 1", got)
	}
	if _, err := substrate.SuspendIdleActor(ctx, counter, namespace, spec.Name, nil); err != nil {
		return nil, 0, err
	}
	return durations, warmOps, nil
}

func collectReport(ctx context.Context, client substrate.Client, backend string, gateEnabled bool, cfg measureConfig) (latencyReport, error) {
	directCold, err := measureCold(ctx, client, cfg.namespace, "direct", cfg.harness, cfg.actorClass, cfg.pool, cfg.iterations)
	if err != nil {
		return latencyReport{}, fmt.Errorf("direct cold: %w", err)
	}
	// Warm actors get a fresh thread ID per run: reusing a fixed thread after
	// a failed resume can rebind a CRASHED/stuck actor on a stale worker
	// (ateom.sock errors) instead of measuring a clean warm resume.
	runID := time.Now().UnixNano()
	directWarm, directWarmOps, err := measureWarm(ctx, client, cfg.namespace, fmt.Sprintf("latency-direct-thread-%d", runID), cfg.harness, cfg.actorClass, cfg.pool, cfg.iterations)
	if err != nil {
		return latencyReport{}, fmt.Errorf("direct warm: %w", err)
	}
	peerCold, err := measureCold(ctx, client, cfg.namespace, "peer-child", cfg.harness, cfg.actorClass, cfg.pool, cfg.iterations)
	if err != nil {
		return latencyReport{}, fmt.Errorf("peer cold: %w", err)
	}
	peerWarm, peerWarmOps, err := measureWarm(ctx, client, cfg.namespace, fmt.Sprintf("latency-peer-child-thread-%d", runID), cfg.harness, cfg.actorClass, cfg.pool, cfg.iterations)
	if err != nil {
		return latencyReport{}, fmt.Errorf("peer warm: %w", err)
	}
	peerBusy, peerBusyOps, err := measurePeerBusyWaitWarm(ctx, client, cfg.namespace, fmt.Sprintf("latency-peer-busy-child-thread-%d", runID), cfg.harness, cfg.actorClass, cfg.pool, cfg.iterations, cfg.busyHold)
	if err != nil {
		return latencyReport{}, fmt.Errorf("peer busy-wait warm: %w", err)
	}

	directColdStats := summarize(directCold, 0)
	directWarmStats := summarize(directWarm, directWarmOps)
	peerColdStats := summarize(peerCold, 0)
	peerWarmStats := summarize(peerWarm, peerWarmOps)
	peerBusyStats := summarize(peerBusy, peerBusyOps)

	report := latencyReport{
		Tool:        "substrate-latency",
		Backend:     backend,
		GateEnabled: gateEnabled,
		Iterations:  cfg.iterations,
		Namespace:   cfg.namespace,
		BusyHoldMs:  float64(cfg.busyHold.Nanoseconds()) / 1e6,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Scenarios: map[string]scenarioStats{
			"directCold":       directColdStats,
			"directWarm":       directWarmStats,
			"peerCold":         peerColdStats,
			"peerWarm":         peerWarmStats,
			"peerBusyWaitWarm": peerBusyStats,
		},
	}
	if cfg.jobP50 > 0 || cfg.jobP95 > 0 {
		report.JobBaseline = &scenarioStats{Count: cfg.iterations, P50Ms: cfg.jobP50, P95Ms: cfg.jobP95}
		report.ComparisonMs = map[string]float64{
			"directWarmSavedVsJobP50":       cfg.jobP50 - directWarmStats.P50Ms,
			"directWarmSavedVsJobP95":       cfg.jobP95 - directWarmStats.P95Ms,
			"peerWarmSavedVsJobP50":         cfg.jobP50 - peerWarmStats.P50Ms,
			"peerWarmSavedVsJobP95":         cfg.jobP95 - peerWarmStats.P95Ms,
			"peerBusyWaitWarmSavedVsJobP50": cfg.jobP50 - peerBusyStats.P50Ms,
			"peerBusyWaitWarmSavedVsJobP95": cfg.jobP95 - peerBusyStats.P95Ms,
		}
	}
	return report, nil
}

func writeReport(report latencyReport, outPath string) error {
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("encode report: %w", err)
	}
	encoded = append(encoded, '\n')
	if trimmed := strings.TrimSpace(outPath); trimmed != "" {
		if err := os.WriteFile(trimmed, encoded, 0o600); err != nil {
			return fmt.Errorf("write report: %w", err)
		}
	}
	if _, err := os.Stdout.Write(encoded); err != nil {
		return fmt.Errorf("write stdout: %w", err)
	}
	return nil
}

func main() {
	iterations := flag.Int("n", 20, "Timed operations per scenario.")
	namespace := flag.String("namespace", "agents", "Actor namespace for the measurement.")
	harness := flag.String("harness", "openCode", "Harness adapter kind carried on the actor spec.")
	actorClass := flag.String("actor-class", "standing-chat", "Substrate actor class for the measurement.")
	pool := flag.String("pool", "warm", "Substrate warm pool for the measurement.")
	outPath := flag.String("out", "", "Optional JSON report path (written with 0600 permissions).")
	jobP50 := flag.Float64("job-baseline-p50-ms", 0, "Observed Job cold-start p50 in ms for comparison (0 omits the baseline).")
	jobP95 := flag.Float64("job-baseline-p95-ms", 0, "Observed Job cold-start p95 in ms for comparison (0 omits the baseline).")
	busyHold := flag.Duration("busy-hold", 25*time.Millisecond, "How long the occupying warm turn holds the recipient before the queued peer delivery may resume (durable wait). Included in peerBusyWaitWarm samples.")
	probeOnly := flag.Bool("probe-only", false, "Dial ateapi once and report reachability as JSON (no timings, no iterations). Exits 0 when ateapi answered, 1 otherwise.")
	flag.Parse()

	if *probeOnly {
		runProbe(context.Background(), *namespace, *outPath)
	}

	if *iterations < 1 {
		fmt.Fprintln(os.Stderr, "error: -n must be at least 1")
		os.Exit(2)
	}
	ctx := context.Background()
	backendName := "fake"
	var client substrate.Client = substrate.NewFakeClient()
	gateEnabled := false
	if gate := substrate.GateConfigFromEnv(); gate.LiveEnabled() {
		ateCfg := substrate.ATEClientConfigFromGate(gate)
		live, closeConn, err := substrate.DialATEClient(ctx, ateCfg, substrate.ATEDialOptions{})
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: dial ateapi at %s: %v\n", ateCfg.Address, err)
			os.Exit(1)
		}
		defer closeConn()
		client = live
		backendName = "ate-live"
		gateEnabled = true
	}

	cfg := measureConfig{
		iterations: *iterations,
		namespace:  *namespace,
		harness:    *harness,
		actorClass: *actorClass,
		pool:       *pool,
		busyHold:   *busyHold,
		jobP50:     *jobP50,
		jobP95:     *jobP95,
	}
	report, err := collectReport(ctx, client, backendName, gateEnabled, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if err := writeReport(report, *outPath); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
