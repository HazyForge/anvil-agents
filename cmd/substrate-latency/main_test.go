package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/hazyforge/anvil-agents/internal/substrate"
)

// evaluateProbeTarget must fail closed with an honest reason when the live
// gate is not fully configured, and resolve a dial config when it is. No
// test here needs a Substrate cluster.
func TestEvaluateProbeTargetGateOff(t *testing.T) {
	t.Parallel()

	cfg, errMsg, ok := evaluateProbeTarget(substrate.GateConfig{})
	if ok {
		t.Fatalf("gate-off probe target ok with cfg %+v", cfg)
	}
	if !strings.Contains(errMsg, substrate.GateEnabledEnvVar) || !strings.Contains(errMsg, substrate.GateEndpointEnvVar) {
		t.Fatalf("gate-off error = %q, want both gate env vars named", errMsg)
	}
}

func TestEvaluateProbeTargetNeedsTemplate(t *testing.T) {
	t.Parallel()

	gate := substrate.GateConfig{Enabled: true, Endpoint: "127.0.0.1:8443"}
	if _, errMsg, ok := evaluateProbeTarget(gate); ok || !strings.Contains(errMsg, substrate.GateTemplateEnvVar) {
		t.Fatalf("template-less probe = ok=%v err=%q, want template error", ok, errMsg)
	}
}

func TestEvaluateProbeTargetLiveGateResolves(t *testing.T) {
	t.Parallel()

	gate := substrate.GateConfig{Enabled: true, Endpoint: "127.0.0.1:8443", Template: "counter", Atespace: "ate-demo-counter"}
	cfg, errMsg, ok := evaluateProbeTarget(gate)
	if !ok {
		t.Fatalf("live probe target failed: %q", errMsg)
	}
	if cfg.Address != "127.0.0.1:8443" || cfg.Template != "counter" || cfg.Atespace != "ate-demo-counter" {
		t.Fatalf("probe dial config = %+v", cfg)
	}
}

// The probe report carries reachability only: it must never smuggle latency
// scenarios or fabricated timings into an availability answer.
func TestProbeReportCarriesNoTimings(t *testing.T) {
	t.Parallel()

	report := probeReport{Tool: "substrate-latency-probe", Backend: "ate-live", Endpoint: "127.0.0.1:8443", Reachable: false, Error: "live gate off (need x)"}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"scenarios", "p50Ms", "p95Ms", "comparisonMs", "iterations"} {
		if _, found := decoded[key]; found {
			t.Fatalf("probe report must not carry %q: %s", key, encoded)
		}
	}
	if decoded["reachable"] != false || decoded["tool"] != "substrate-latency-probe" {
		t.Fatalf("probe report = %s", encoded)
	}
}

func testMeasureConfig(iterations int, busyHold time.Duration) measureConfig {
	return measureConfig{
		iterations: iterations,
		namespace:  "agents",
		harness:    "openCode",
		actorClass: "standing-chat",
		pool:       "warm",
		busyHold:   busyHold,
	}
}

// TestMeasurePeerBusyWaitWarmFakeClient drives the wait+resume path on
// FakeClient: occupy a warm recipient, durable-wait a peer delivery, resume
// the same actor, no second Create. No cluster required.
func TestMeasurePeerBusyWaitWarmFakeClient(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fake := substrate.NewFakeClient()
	threadID := "latency-peer-busy-child-test-1"
	hold := 8 * time.Millisecond
	durations, warmOps, err := measurePeerBusyWaitWarm(ctx, fake, "agents", threadID, "openCode", "standing-chat", "warm", 3, hold)
	if err != nil {
		t.Fatalf("measurePeerBusyWaitWarm: %v", err)
	}
	if len(durations) != 3 || warmOps != 3 {
		t.Fatalf("samples = %d warmOps = %d, want 3/3", len(durations), warmOps)
	}
	for i, d := range durations {
		if d < hold/2 {
			t.Fatalf("sample %d = %s, want a durable wait on the order of busy-hold %s", i, d, hold)
		}
	}
	if got := fake.Created(); got != 1 {
		t.Fatalf("distinct actors = %d, want exactly one recipient actor", got)
	}
	for _, call := range fake.Calls {
		if strings.HasPrefix(call, "create-reuse:") {
			t.Fatalf("busy-wait path reused CreateActor (%v); want one cold Create then Resume", fake.Calls)
		}
	}
	stats := summarize(durations, warmOps)
	if stats.Count != 3 || stats.WarmOps != 3 || stats.P50Ms <= 0 || stats.P95Ms <= 0 {
		t.Fatalf("peerBusyWaitWarm stats = %+v, want count/warmOps 3 and positive percentiles", stats)
	}
}

func TestCollectReportIncludesPeerBusyWaitWarm(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fake := substrate.NewFakeClient()
	cfg := testMeasureConfig(2, 5*time.Millisecond)
	cfg.jobP50 = 12000
	cfg.jobP95 = 44000
	report, err := collectReport(ctx, fake, "fake", false, cfg)
	if err != nil {
		t.Fatalf("collectReport: %v", err)
	}
	if report.Backend != "fake" || report.GateEnabled {
		t.Fatalf("report backend/gate = %s/%v, want fake/false", report.Backend, report.GateEnabled)
	}
	want := []string{"directCold", "directWarm", "peerCold", "peerWarm", "peerBusyWaitWarm"}
	for _, name := range want {
		stats, ok := report.Scenarios[name]
		if !ok {
			t.Fatalf("missing scenario %q in %v", name, report.Scenarios)
		}
		if stats.Count != 2 {
			t.Fatalf("scenario %q count = %d, want 2", name, stats.Count)
		}
		if stats.P50Ms < 0 || stats.P95Ms < 0 {
			t.Fatalf("scenario %q has negative percentile: %+v", name, stats)
		}
	}
	busy := report.Scenarios["peerBusyWaitWarm"]
	if busy.WarmOps != 2 {
		t.Fatalf("peerBusyWaitWarm warmOps = %d, want 2", busy.WarmOps)
	}
	if _, ok := report.ComparisonMs["peerBusyWaitWarmSavedVsJobP50"]; !ok {
		t.Fatalf("comparisonMs missing peerBusyWaitWarmSavedVsJobP50: %v", report.ComparisonMs)
	}
	if _, ok := report.ComparisonMs["peerBusyWaitWarmSavedVsJobP95"]; !ok {
		t.Fatalf("comparisonMs missing peerBusyWaitWarmSavedVsJobP95: %v", report.ComparisonMs)
	}
	if report.BusyHoldMs != 5 {
		t.Fatalf("busyHoldMs = %v, want 5", report.BusyHoldMs)
	}
}
