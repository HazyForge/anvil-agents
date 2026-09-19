// Package main — chat JSONL bars scoring (Slice 5a vocabulary applied to
// Desktop signed-in standing samples).
//
// This file scores `.runtime/chat-latency.jsonl` lines with
// `source: "desktop-chat-live-signed-in"` on the `standing` path against the
// same Job Pod-ready baseline flags (`-job-baseline-p50-ms` /
// `-job-baseline-p95-ms`, defaults ~12s p50 / ~44s p95) and the SAME
// promote/reshape/retire vocabulary the Slice 5a standing-latency harness
// uses (see docs/standing-inprocess-harness.md, "Slice 5a").
//
// Closest honest mapping (documented here because the docs bars assume the
// sixteen-scenario standing-latency matrix, which the JSONL does not have).
// Recalibrated: per prior clarification the Job ~12s figure is Pod-ready /
// startup only, NOT a full turn — so the promote/retire/inconclusive gates
// key off sendToFirstTokenMs vs that baseline, and replyReadyMs never drives
// a verdict.
//
//   - `sendToFirstTokenMs` (falling back to `firstTokenMs` when the alias is
//     absent) is the promote/retire metric: time to first visible token,
//     which is exactly what the Job Pod-ready baseline (~12s p50 / ~44s p95
//     via `-job-baseline-p50-ms` / `-job-baseline-p95-ms`) delays. The bar
//     is p95 in the low single seconds (<= 5000ms) and at least an order of
//     magnitude under the Job p95 (<= jobP95/10).
//   - `replyReadyMs` is computed and reported in the JSON artifact as
//     model-inclusive context only — it must NOT drive retire/promote. A
//     slow full turn next to a fast first token reports promote (or
//     inconclusive, see below), never retire: the Job baseline covers
//     create-to-Pod-ready only while replyReady includes harness/model time
//     on top, so scoring replyReady against it miscalibrates the bars
//     (tonight's live n=10 retired on replyReady p50 7190ms vs Job 12s;
//     under this mapping the same first-token numbers lean promote).
//   - The JSONL carries a single standing plane (no direct-vs-peer split),
//     so `reshape` (direct wins, peer does not) is never emitted by this
//     scorer. A direct-win/peer-unknown outcome reports `promote` with a
//     reason naming the full `standing-latency` matrix as the peer check
//     that must also pass before promoting peers, or `inconclusive-live`
//     when the peer plane (or sample count) is the only open item.
//   - `promote`/`retire` additionally require at least `-min-samples`
//     delivered standing samples (default 10): tonight's n=5 already sits
//     well under the first-token bar, but five turns cannot carry a p95
//     promote call.
//   - `retire` fires when first-token p50 sits within noise of the Job
//     Pod-ready p50 (under a 2x win, i.e. first-token p50 >= jobP50/2). A
//     slow replyReadyMs alone never retires the plane.
//   - Zero matching samples, too few samples, or first-token numbers between
//     the bars all report `inconclusive-live` — never a new bar name. A
//     missing replyReadyMs no longer blocks a verdict (it is context only).
//     No baseline flags reports `no-baseline`. This scorer never reports
//     `harness-ok`: its input is live signed-in samples, not deterministic
//     backends.
//
// Lines carrying a non-empty `error` are counted (`errorSamples`) and
// excluded from the latency stats: a denied or failed turn is not a latency
// sample. Malformed lines are counted (`skippedLines`) and skipped.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"time"
)

// chatBarsVerdict vocabulary — a subset of the Slice 5a set. `reshape` is
// intentionally never emitted (single-plane JSONL has no peer dimension;
// see the mapping note above). `harness-ok` is never emitted either (this
// scorer only reads live signed-in samples).
const (
	chatBarsPromote      = "promote"
	chatBarsReshape      = "reshape"
	chatBarsRetire       = "retire"
	chatBarsInconclusive = "inconclusive-live"
	chatBarsNoBaseline   = "no-baseline"
)

// defaultChatMinSamples is the minimum delivered standing-sample count for a
// promote/retire call from the JSONL. Below it the scorer reports
// inconclusive-live: p95 over a handful of turns cannot carry a bars
// decision.
const defaultChatMinSamples = 10

// firstTokenBarMs is the Slice 5a "low single seconds" first-token ceiling.
const firstTokenBarMs = 5000

type chatBarsStats struct {
	Count  int     `json:"count"`
	MinMs  float64 `json:"minMs"`
	MeanMs float64 `json:"meanMs"`
	P50Ms  float64 `json:"p50Ms"`
	P95Ms  float64 `json:"p95Ms"`
	MaxMs  float64 `json:"maxMs"`
}

func summarizeChatSamples(samples []float64) chatBarsStats {
	stats := chatBarsStats{Count: len(samples)}
	if len(samples) == 0 {
		return stats
	}
	sorted := append([]float64(nil), samples...)
	sort.Float64s(sorted)
	var total float64
	stats.MinMs = sorted[0]
	stats.MaxMs = sorted[len(sorted)-1]
	for _, v := range sorted {
		total += v
	}
	stats.MeanMs = total / float64(len(sorted))
	stats.P50Ms = percentile(sorted, 0.5)
	stats.P95Ms = percentile(sorted, 0.95)
	// Guard against NaN input leaking into the report.
	if math.IsNaN(stats.MeanMs) || math.IsNaN(stats.P50Ms) || math.IsNaN(stats.P95Ms) {
		stats.MeanMs, stats.P50Ms, stats.P95Ms = 0, 0, 0
	}
	return stats
}

// chatBarsInput carries the parsed scoreable inputs for judgeChatBars.
// ReplyReady/HasReply are model-inclusive context only: they are reported in
// the JSON artifact and may be named in verdict reasons, but they never gate
// a verdict — the promote/retire/inconclusive gates key off FirstToken vs
// the Job Pod-ready baseline alone.
type chatBarsInput struct {
	Matched      int
	FirstToken   chatBarsStats
	ReplyReady   chatBarsStats
	HasReply     bool
	JobP50       float64
	JobP95       float64
	MinSamples   int
	Source       string
	Path         string
	TotalLines   int
	ErrorSamples int
}

// judgeChatBars applies the Slice 5a vocabulary to Desktop signed-in
// standing JSONL stats. It never invents a new bar name: the only verdicts
// are promote, retire, inconclusive-live, and no-baseline. reshape is
// documented as not emitted (peer dimension lives in the full
// standing-latency matrix, not in this single-plane JSONL).
//
// Recalibrated mapping: the Job baseline is Pod-ready/startup only (NOT a
// full turn), so the gates key off sendToFirstTokenMs (fallback
// firstTokenMs) vs that baseline. replyReadyMs is context only and never
// drives retire/promote — a slow replyReady next to a fast first token
// reports promote (or inconclusive on sample count / between-bars
// first-token numbers), never retire.
func judgeChatBars(in chatBarsInput) (string, string) {
	if in.JobP50 <= 0 && in.JobP95 <= 0 {
		return chatBarsNoBaseline, "no Job baseline flags were passed, so no comparison or bars verdict applies"
	}
	if in.Matched == 0 {
		return chatBarsInconclusive, fmt.Sprintf("no delivered samples with source %q on path %q: collect signed-in standing turns before applying the bars",
			in.Source, in.Path)
	}
	if in.MinSamples > 0 && in.Matched < in.MinSamples {
		return chatBarsInconclusive, fmt.Sprintf("only %d delivered standing samples (need >= %d for a bars call): first-token p50 %.1fms already sits well under the Job baseline, but more iterations are needed before any promote/retire decision",
			in.Matched, in.MinSamples, in.FirstToken.P50Ms)
	}
	// Retire: first-token p50 within noise of the Job Pod-ready p50 (under
	// a 2x win). replyReadyMs alone never retires the plane.
	if in.JobP50 > 0 && in.FirstToken.P50Ms >= in.JobP50/2 {
		return chatBarsRetire, fmt.Sprintf("standing sendToFirstToken p50 %.1fms is within noise of job Pod-ready p50 %.0fms (under a 2x win); the standing plane does not pay for itself%s",
			in.FirstToken.P50Ms, in.JobP50, replyReadyContextSuffix(in))
	}
	// Promote: first-token p95 an order of magnitude under the Job Pod-ready
	// p95 with first-token p95 in the low single seconds. The peer dimension
	// is not in this JSONL, so the reason names the standing-latency matrix
	// as the peer check that must also pass before promoting peers.
	firstTokenCeiling := firstTokenBarMs
	if in.JobP95 > 0 && in.JobP95/10 < float64(firstTokenCeiling) {
		firstTokenCeiling = int(in.JobP95 / 10)
	}
	if in.JobP95 > 0 && in.FirstToken.P95Ms <= float64(firstTokenCeiling) {
		return chatBarsPromote, fmt.Sprintf("standing sendToFirstToken p95 %.1fms <= min(5000ms, job Pod-ready p95 %.0fms/10) = %dms over %d samples (p50 %.1fms)%s; peer plane still needs the full standing-latency matrix (reshape bar) before promoting peers",
			in.FirstToken.P95Ms, in.JobP95, firstTokenCeiling, in.Matched, in.FirstToken.P50Ms, replyReadyContextSuffix(in))
	}
	return chatBarsInconclusive, fmt.Sprintf("live standing first-token lands between the bars (sendToFirstToken p50 %.1fms / p95 %.1fms over %d samples vs job Pod-ready p50 %.0fms / p95 %.0fms%s): collect more iterations on the same cluster shape before deciding",
		in.FirstToken.P50Ms, in.FirstToken.P95Ms, in.Matched, in.JobP50, in.JobP95, replyReadyContextSuffix(in))
}

// replyReadyContextSuffix renders the model-inclusive replyReady context for
// verdict reasons. It never gates a verdict: present numbers are reported as
// context, an absent replyReady is reported as absent context.
func replyReadyContextSuffix(in chatBarsInput) string {
	if !in.HasReply {
		return "; no replyReadyMs in these samples (context absent, verdict keys off first-token only)"
	}
	return fmt.Sprintf("; replyReady p50 %.1fms / p95 %.1fms is model-inclusive context only, not a gate",
		in.ReplyReady.P50Ms, in.ReplyReady.P95Ms)
}

// chatBarsReport is the JSON report for `-chat-jsonl` scoring mode. Verdict
// vocabulary matches Slice 5a exactly; `mode` distinguishes this report
// from the sixteen-scenario matrix report.
type chatBarsReport struct {
	Tool             string             `json:"tool"`
	Mode             string             `json:"mode"`
	Backend          string             `json:"backend"`
	Live             bool               `json:"live"`
	Source           string             `json:"source"`
	Path             string             `json:"path"`
	File             string             `json:"file"`
	GeneratedAt      string             `json:"generatedAt"`
	TotalLines       int                `json:"totalLines"`
	MatchedSamples   int                `json:"matchedSamples"`
	ErrorSamples     int                `json:"errorSamples"`
	SkippedLines     int                `json:"skippedLines"`
	MinSamples       int                `json:"minSamples"`
	SendToFirstToken chatBarsStats      `json:"sendToFirstTokenMs"`
	ReplyReady       *chatBarsStats     `json:"replyReadyMs,omitempty"`
	JobBaseline      *scenarioStats     `json:"jobBaseline,omitempty"`
	ComparisonMs     map[string]float64 `json:"comparisonMs,omitempty"`
	Verdict          string             `json:"verdict"`
	VerdictReason    string             `json:"verdictReason"`
	Note             string             `json:"note,omitempty"`
}

// chatLine is the scoreable subset of a `.runtime/chat-latency.jsonl` line.
type chatLine struct {
	Source             string   `json:"source"`
	Path               string   `json:"path"`
	SendToFirstTokenMs *float64 `json:"sendToFirstTokenMs"`
	FirstTokenMs       *float64 `json:"firstTokenMs"`
	ReplyReadyMs       *float64 `json:"replyReadyMs"`
	Error              string   `json:"error"`
}

func chatFirstTokenMs(line chatLine) (float64, bool) {
	if line.SendToFirstTokenMs != nil && !math.IsNaN(*line.SendToFirstTokenMs) && *line.SendToFirstTokenMs >= 0 {
		return *line.SendToFirstTokenMs, true
	}
	if line.FirstTokenMs != nil && !math.IsNaN(*line.FirstTokenMs) && *line.FirstTokenMs >= 0 {
		return *line.FirstTokenMs, true
	}
	return 0, false
}

// scoreChatJSONL reads path, filters to source/path, and scores the Slice
// 5a bars. It never invents samples: a missing file is an error, and empty
// or fully filtered input reports inconclusive-live with zero matched
// samples.
func scoreChatJSONL(path, source, wantPath string, jobP50, jobP95 float64, minSamples int) (*chatBarsReport, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return nil, fmt.Errorf("-chat-jsonl is required in chat-bars mode (point at .runtime/chat-latency.jsonl)")
	}
	file, err := os.Open(trimmed)
	if err != nil {
		return nil, fmt.Errorf("open chat JSONL %q: %w (no samples invented: fix the path and re-run)", trimmed, err)
	}
	defer file.Close()

	var firstTokens, replyReadies []float64
	total, matched, errorSamples, skipped := 0, 0, 0, 0
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		raw := strings.TrimSpace(scanner.Text())
		if raw == "" {
			continue
		}
		total++
		var line chatLine
		if err := json.Unmarshal([]byte(raw), &line); err != nil {
			skipped++
			continue
		}
		if strings.TrimSpace(line.Source) != source {
			continue
		}
		if wantPath != "" && strings.TrimSpace(line.Path) != wantPath {
			continue
		}
		if strings.TrimSpace(line.Error) != "" {
			errorSamples++
			continue
		}
		firstToken, ok := chatFirstTokenMs(line)
		if !ok {
			skipped++
			continue
		}
		matched++
		firstTokens = append(firstTokens, firstToken)
		if line.ReplyReadyMs != nil && !math.IsNaN(*line.ReplyReadyMs) && *line.ReplyReadyMs >= 0 {
			replyReadies = append(replyReadies, *line.ReplyReadyMs)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read chat JSONL %q: %w", trimmed, err)
	}

	firstStats := summarizeChatSamples(firstTokens)
	hasReply := len(replyReadies) > 0
	verdict, reason := judgeChatBars(chatBarsInput{
		Matched:    matched,
		FirstToken: firstStats,
		ReplyReady: summarizeChatSamples(replyReadies),
		HasReply:   hasReply,
		JobP50:     jobP50,
		JobP95:     jobP95,
		MinSamples: minSamples,
		Source:     source,
		Path:       wantPath,
	})

	report := &chatBarsReport{
		Tool:             "standing-latency",
		Mode:             "chat-bars",
		Backend:          "chat-jsonl",
		Live:             true,
		Source:           source,
		Path:             wantPath,
		File:             trimmed,
		GeneratedAt:      time.Now().UTC().Format(time.RFC3339Nano),
		TotalLines:       total,
		MatchedSamples:   matched,
		ErrorSamples:     errorSamples,
		SkippedLines:     skipped,
		MinSamples:       minSamples,
		SendToFirstToken: firstStats,
		Verdict:          verdict,
		VerdictReason:    reason,
		Note:             "verdict keys off sendToFirstTokenMs vs the Job Pod-ready baseline (replyReadyMs is model-inclusive context only); single-plane JSONL: reshape needs the full standing-latency direct+peer matrix and is never emitted here; promote additionally requires the peer plane from that matrix before promoting peers",
	}
	if hasReply {
		replyStats := summarizeChatSamples(replyReadies)
		report.ReplyReady = &replyStats
	}
	if jobP50 > 0 || jobP95 > 0 {
		report.JobBaseline = &scenarioStats{Count: matched, P50Ms: jobP50, P95Ms: jobP95}
		report.ComparisonMs = map[string]float64{
			"sendToFirstTokenSavedVsJobP50": jobP50 - firstStats.P50Ms,
			"sendToFirstTokenSavedVsJobP95": jobP95 - firstStats.P95Ms,
		}
		if hasReply {
			report.ComparisonMs["replyReadySavedVsJobP50"] = jobP50 - report.ReplyReady.P50Ms
			report.ComparisonMs["replyReadySavedVsJobP95"] = jobP95 - report.ReplyReady.P95Ms
		}
	}
	return report, nil
}
