package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// The fixture is synthetic by construction: fixed round values, synthetic
// thread ids, and a stub timestamp. It must never be mistaken for measured
// live latency (tonight's real n=5 is documented in
// docs/standing-inprocess-harness.md and stays gitignored on the WSL
// checkout — no live numbers are committed here).
func TestChatBarsSyntheticFixture(t *testing.T) {
	path := filepath.Join("testdata", "chat-latency-synthetic.jsonl")
	report, err := scoreChatJSONL(path, "desktop-chat-live-signed-in", "standing", 12000, 44000, 10)
	if err != nil {
		t.Fatalf("scoreChatJSONL: %v", err)
	}
	if report.Tool != "standing-latency" || report.Mode != "chat-bars" {
		t.Fatalf("tool/mode = %q/%q, want standing-latency/chat-bars", report.Tool, report.Mode)
	}
	if report.TotalLines != 9 {
		t.Fatalf("totalLines = %d, want 9", report.TotalLines)
	}
	if report.MatchedSamples != 6 {
		t.Fatalf("matchedSamples = %d, want 6 (other-source, job-path, and error lines filtered)", report.MatchedSamples)
	}
	if report.ErrorSamples != 1 {
		t.Fatalf("errorSamples = %d, want 1", report.ErrorSamples)
	}
	if report.SkippedLines != 0 {
		t.Fatalf("skippedLines = %d, want 0", report.SkippedLines)
	}
	// Sorted first-token: 310,340,365,390,420,510 → p50 idx 2 (365), p95 idx 5 (510).
	if report.SendToFirstToken.P50Ms != 365 || report.SendToFirstToken.P95Ms != 510 {
		t.Fatalf("sendToFirstToken p50/p95 = %.0f/%.0f, want 365/510", report.SendToFirstToken.P50Ms, report.SendToFirstToken.P95Ms)
	}
	if report.ReplyReady == nil {
		t.Fatal("replyReady section is missing")
	}
	// Sorted replyReady: 6100,6800,7200,7900,8300,9100 → p50 7200, p95 9100.
	if report.ReplyReady.P50Ms != 7200 || report.ReplyReady.P95Ms != 9100 {
		t.Fatalf("replyReady p50/p95 = %.0f/%.0f, want 7200/9100", report.ReplyReady.P50Ms, report.ReplyReady.P95Ms)
	}
	// n=6 below the default min-samples gate (10): first-token already sits
	// well under the Job baseline, but the verdict must stay
	// inconclusive-live — six turns cannot carry a promote call.
	if report.Verdict != "inconclusive-live" {
		t.Fatalf("verdict = %q, want inconclusive-live (n=6 < min 10)", report.Verdict)
	}
	if !strings.Contains(report.VerdictReason, "only 6") {
		t.Fatalf("reason should name the sample shortfall, got %q", report.VerdictReason)
	}
	for _, verdict := range []string{report.Verdict} {
		switch verdict {
		case "promote", "reshape", "retire", "inconclusive-live", "no-baseline":
		default:
			t.Fatalf("verdict %q is outside the Slice 5a vocabulary", verdict)
		}
	}
	if report.JobBaseline == nil || report.JobBaseline.P50Ms != 12000 || report.JobBaseline.P95Ms != 44000 {
		t.Fatalf("jobBaseline = %+v, want p50 12000 / p95 44000", report.JobBaseline)
	}
	if len(report.ComparisonMs) == 0 {
		t.Fatal("comparisonMs is empty")
	}
}

func TestJudgeChatBarsPromote(t *testing.T) {
	first := summarizeChatSamples([]float64{300, 320, 340, 360, 380, 400, 410, 420, 430, 440, 450, 460})
	reply := summarizeChatSamples([]float64{2100, 2300, 2500, 2700, 2900, 3100, 3200, 3300, 3400, 3500, 3600, 3800})
	verdict, reason := judgeChatBars(chatBarsInput{
		Matched: 12, FirstToken: first, ReplyReady: reply, HasReply: true,
		JobP50: 12000, JobP95: 44000, MinSamples: 10,
		Source: "desktop-chat-live-signed-in", Path: "standing",
	})
	if verdict != "promote" {
		t.Fatalf("verdict = %q (%s), want promote", verdict, reason)
	}
}

func TestJudgeChatBarsRetire(t *testing.T) {
	first := summarizeChatSamples([]float64{300, 320, 340, 360, 380, 400, 410, 420, 430, 440, 450, 460})
	reply := summarizeChatSamples([]float64{6100, 6800, 7200, 7900, 8300, 9100, 9500, 9900, 10100, 10500, 11000, 11500})
	verdict, _ := judgeChatBars(chatBarsInput{
		Matched: 12, FirstToken: first, ReplyReady: reply, HasReply: true,
		JobP50: 12000, JobP95: 44000, MinSamples: 10,
		Source: "desktop-chat-live-signed-in", Path: "standing",
	})
	if verdict != "retire" {
		t.Fatalf("verdict = %q, want retire (replyReady p50 within noise of job p50)", verdict)
	}
}

func TestJudgeChatBarsBetweenBarsIsInconclusive(t *testing.T) {
	// First-token passes but full-turn p95 misses the order-of-magnitude
	// bar: between the bars, so inconclusive-live.
	first := summarizeChatSamples([]float64{300, 320, 340, 360, 380, 400, 410, 420, 430, 440, 450, 460})
	reply := summarizeChatSamples([]float64{2100, 2300, 2500, 2700, 2900, 3100, 3200, 3300, 3400, 3500, 4600, 4800})
	verdict, _ := judgeChatBars(chatBarsInput{
		Matched: 12, FirstToken: first, ReplyReady: reply, HasReply: true,
		JobP50: 12000, JobP95: 44000, MinSamples: 10,
		Source: "desktop-chat-live-signed-in", Path: "standing",
	})
	if verdict != "inconclusive-live" {
		t.Fatalf("verdict = %q, want inconclusive-live", verdict)
	}
}

func TestJudgeChatBarsNoBaseline(t *testing.T) {
	first := summarizeChatSamples([]float64{300, 320})
	verdict, _ := judgeChatBars(chatBarsInput{
		Matched: 2, FirstToken: first, HasReply: false,
		JobP50: 0, JobP95: 0, Source: "desktop-chat-live-signed-in", Path: "standing",
	})
	if verdict != "no-baseline" {
		t.Fatalf("verdict = %q, want no-baseline", verdict)
	}
}

func TestJudgeChatBarsEmptyIsInconclusive(t *testing.T) {
	// Zero matching samples must not invent a new bar name: inconclusive.
	verdict, reason := judgeChatBars(chatBarsInput{
		JobP50: 12000, JobP95: 44000, Source: "desktop-chat-live-signed-in", Path: "standing",
	})
	if verdict != "inconclusive-live" {
		t.Fatalf("verdict = %q, want inconclusive-live", verdict)
	}
	if !strings.Contains(reason, "no delivered samples") {
		t.Fatalf("reason should say no samples collected, got %q", reason)
	}
}

func TestJudgeChatBarsMissingReplyReadyIsInconclusive(t *testing.T) {
	first := summarizeChatSamples([]float64{300, 320, 340, 360, 380, 400, 410, 420, 430, 440, 450, 460})
	verdict, reason := judgeChatBars(chatBarsInput{
		Matched: 12, FirstToken: first, HasReply: false,
		JobP50: 12000, JobP95: 44000, MinSamples: 10,
		Source: "desktop-chat-live-signed-in", Path: "standing",
	})
	if verdict != "inconclusive-live" {
		t.Fatalf("verdict = %q, want inconclusive-live", verdict)
	}
	if !strings.Contains(reason, "replyReadyMs") {
		t.Fatalf("reason should name the missing full-turn field, got %q", reason)
	}
}

func TestScoreChatJSONLMissingFileInventsNothing(t *testing.T) {
	if _, err := scoreChatJSONL(filepath.Join(t.TempDir(), "does-not-exist.jsonl"), "desktop-chat-live-signed-in", "standing", 12000, 44000, 10); err == nil {
		t.Fatal("expected an error for a missing JSONL path")
	}
}

func TestChatFirstTokenFallsBackToFirstTokenMs(t *testing.T) {
	line := chatLine{FirstTokenMs: ptrFloat(250)}
	if got, ok := chatFirstTokenMs(line); !ok || got != 250 {
		t.Fatalf("fallback firstTokenMs = %v,%v, want 250,true", got, ok)
	}
}

func ptrFloat(v float64) *float64 { return &v }
