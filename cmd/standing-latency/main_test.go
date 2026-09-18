package main

import (
	"context"
	"testing"
)

func testConfig(backend string, n int) config {
	return config{
		iterations: n,
		namespace:  "agents",
		harness:    "openCode",
		prompt:     "standing latency probe: reply briefly",
		backend:    backend,
		jobP50:     12000,
		jobP95:     44000,
	}
}

func requireScenarios(t *testing.T, cfg config) map[string]scenarioStats {
	t.Helper()
	report, err := run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if report.Tool != "standing-latency" {
		t.Fatalf("tool = %q, want standing-latency", report.Tool)
	}
	want := []string{
		"directEnsureCold", "directEnsureWarm", "directTurnWarm", "directFirstToken",
		"peerEnsureCold", "peerEnsureWarm", "peerTurnWarm", "peerFirstToken",
	}
	for _, name := range want {
		stats, ok := report.Scenarios[name]
		if !ok {
			t.Fatalf("missing scenario %q", name)
		}
		if stats.Count != cfg.iterations {
			t.Fatalf("scenario %q count = %d, want %d", name, stats.Count, cfg.iterations)
		}
		if stats.P50Ms < 0 || stats.P95Ms < 0 {
			t.Fatalf("scenario %q has negative percentile (p50=%v p95=%v)", name, stats.P50Ms, stats.P95Ms)
		}
	}
	for _, name := range []string{"directEnsureWarm", "peerEnsureWarm"} {
		if got := report.Scenarios[name].WarmOps; got != cfg.iterations {
			t.Fatalf("scenario %q warmOps = %d, want %d (every timed ensure must resume warm)", name, got, cfg.iterations)
		}
	}
	if report.JobBaseline == nil {
		t.Fatal("jobBaseline is missing")
	}
	if report.JobBaseline.P50Ms != 12000 || report.JobBaseline.P95Ms != 44000 {
		t.Fatalf("jobBaseline = p50 %v / p95 %v, want 12000 / 44000", report.JobBaseline.P50Ms, report.JobBaseline.P95Ms)
	}
	if len(report.ComparisonMs) == 0 {
		t.Fatal("comparisonMs is empty")
	}
	return report.Scenarios
}

// TestFakeReport pins the deterministic CI path: FakeBackend proves the
// harness and warm-reuse contract with no cluster, no subprocess, and no
// model call. The verdict must stay harness-ok so fake numbers can never
// promote the plane by themselves.
func TestFakeReport(t *testing.T) {
	scenarios := requireScenarios(t, testConfig("fake", 3))
	// Warm resume must not be slower than cold create on the same backend.
	if scenarios["directEnsureWarm"].P50Ms > scenarios["directEnsureCold"].P50Ms {
		t.Fatalf("direct warm ensure p50 %v exceeds cold create p50 %v",
			scenarios["directEnsureWarm"].P50Ms, scenarios["directEnsureCold"].P50Ms)
	}
	report, err := run(context.Background(), testConfig("fake", 3))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if report.Verdict != "harness-ok" {
		t.Fatalf("fake verdict = %q, want harness-ok", report.Verdict)
	}
	if report.Live {
		t.Fatal("fake backend must not report live")
	}
}

// TestProcessStubReport pins the stubbed ProcessBackend path: the real
// session/streaming code runs behind a fixed-latency stub runner, so the
// same warm-reuse assertions hold with no PATH binary.
func TestProcessStubReport(t *testing.T) {
	requireScenarios(t, testConfig("process-stub", 2))
	report, err := run(context.Background(), testConfig("process-stub", 2))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if report.Verdict != "harness-ok" {
		t.Fatalf("process-stub verdict = %q, want harness-ok", report.Verdict)
	}
	if report.Live {
		t.Fatal("process-stub backend must not report live")
	}
	if report.Backend != "process-stub" {
		t.Fatalf("backend = %q, want process-stub", report.Backend)
	}
}

// TestNoBaselineReport pins the no-baseline escape hatch used when the
// caller only wants raw warm-path numbers.
func TestNoBaselineReport(t *testing.T) {
	cfg := testConfig("fake", 2)
	cfg.jobP50, cfg.jobP95 = 0, 0
	report, err := run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if report.Verdict != "no-baseline" {
		t.Fatalf("verdict = %q, want no-baseline", report.Verdict)
	}
	if report.JobBaseline != nil {
		t.Fatal("jobBaseline must be omitted when both baseline flags are 0")
	}
}

// TestUnknownBackendFailsClosed pins fail-closed backend selection.
func TestUnknownBackendFailsClosed(t *testing.T) {
	if _, err := run(context.Background(), testConfig("bogus", 1)); err == nil {
		t.Fatal("expected an error for an unknown backend")
	}
	if _, err := run(context.Background(), testConfig("fake", 0)); err == nil {
		t.Fatal("expected an error for -n 0")
	}
}
