package main

import (
	"encoding/json"
	"strings"
	"testing"

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
