// Command substrate-latency measures warm actor resume versus cold actor
// create for standing-chat turns, covering both direct turns and peer
// deliveries (Substrate spike).
//
// Fake-backend by default: without the live gate it drives the in-memory
// FakeClient, so CI stays green with no cluster. With the live gate on
// (ANVIL_AGENTS_SUBSTRATE_ACTORS_ENABLED + ANVIL_AGENTS_SUBSTRATE_ENDPOINT)
// it dials ateapi over gRPC — verified TLS before any bearer token, token
// from ANVIL_AGENTS_SUBSTRATE_TOKEN_FILE (or the inline token env, or a
// minted ServiceAccount token), Kind-only skip-verify TLS behind
// ANVIL_AGENTS_SUBSTRATE_INSECURE for a loopback port-forward — and measures
// real Create/Resume timings. See docs/substrate-spike.md for the Kind
// port-forward setup.
//
// The Job cold-start baseline cannot be measured from this process (it needs a
// real cluster scheduler), so pass the observed baseline explicitly with
// -job-baseline-p50-ms / -job-baseline-p95-ms (Desktop chat-latency JSONL or
// Job create-to-ready timings; Primaris measured ~8-44s). The report keeps
// p50/p95 fields for every scenario plus the comparison.
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
	GeneratedAt  string                   `json:"generatedAt"`
	Scenarios    map[string]scenarioStats `json:"scenarios"`
	JobBaseline  *scenarioStats           `json:"jobBaseline,omitempty"`
	ComparisonMs map[string]float64       `json:"comparisonMs,omitempty"`
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

func main() {
	iterations := flag.Int("n", 20, "Timed operations per scenario.")
	namespace := flag.String("namespace", "agents", "Actor namespace for the measurement.")
	harness := flag.String("harness", "openCode", "Harness adapter kind carried on the actor spec.")
	actorClass := flag.String("actor-class", "standing-chat", "Substrate actor class for the measurement.")
	pool := flag.String("pool", "warm", "Substrate warm pool for the measurement.")
	outPath := flag.String("out", "", "Optional JSON report path (written with 0600 permissions).")
	jobP50 := flag.Float64("job-baseline-p50-ms", 0, "Observed Job cold-start p50 in ms for comparison (0 omits the baseline).")
	jobP95 := flag.Float64("job-baseline-p95-ms", 0, "Observed Job cold-start p95 in ms for comparison (0 omits the baseline).")
	flag.Parse()

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

	directCold, err := measureCold(ctx, client, *namespace, "direct", *harness, *actorClass, *pool, *iterations)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: direct cold: %v\n", err)
		os.Exit(1)
	}
	// Warm actors get a fresh thread ID per run: reusing a fixed thread after
	// a failed resume can rebind a CRASHED/stuck actor on a stale worker
	// (ateom.sock errors) instead of measuring a clean warm resume.
	runID := time.Now().UnixNano()
	directWarm, directWarmOps, err := measureWarm(ctx, client, *namespace, fmt.Sprintf("latency-direct-thread-%d", runID), *harness, *actorClass, *pool, *iterations)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: direct warm: %v\n", err)
		os.Exit(1)
	}
	peerCold, err := measureCold(ctx, client, *namespace, "peer-child", *harness, *actorClass, *pool, *iterations)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: peer cold: %v\n", err)
		os.Exit(1)
	}
	peerWarm, peerWarmOps, err := measureWarm(ctx, client, *namespace, fmt.Sprintf("latency-peer-child-thread-%d", runID), *harness, *actorClass, *pool, *iterations)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: peer warm: %v\n", err)
		os.Exit(1)
	}

	directColdStats := summarize(directCold, 0)
	directWarmStats := summarize(directWarm, directWarmOps)
	peerColdStats := summarize(peerCold, 0)
	peerWarmStats := summarize(peerWarm, peerWarmOps)

	report := latencyReport{
		Tool:        "substrate-latency",
		Backend:     backendName,
		GateEnabled: gateEnabled,
		Iterations:  *iterations,
		Namespace:   *namespace,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Scenarios: map[string]scenarioStats{
			"directCold": directColdStats,
			"directWarm": directWarmStats,
			"peerCold":   peerColdStats,
			"peerWarm":   peerWarmStats,
		},
	}
	if *jobP50 > 0 || *jobP95 > 0 {
		report.JobBaseline = &scenarioStats{Count: *iterations, P50Ms: *jobP50, P95Ms: *jobP95}
		report.ComparisonMs = map[string]float64{
			"directWarmSavedVsJobP50": *jobP50 - directWarmStats.P50Ms,
			"directWarmSavedVsJobP95": *jobP95 - directWarmStats.P95Ms,
			"peerWarmSavedVsJobP50":   *jobP50 - peerWarmStats.P50Ms,
			"peerWarmSavedVsJobP95":   *jobP95 - peerWarmStats.P95Ms,
		}
	}

	encoded, err := json.MarshalIndent(report, "", "  ")
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
}
