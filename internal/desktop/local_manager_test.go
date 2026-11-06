package desktop

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hazyforge/anvil-agents/internal/localrouting"
	"github.com/hazyforge/anvil-agents/internal/subjectrouting"
)

type localDecisionFunc func(context.Context, subjectrouting.Input) (subjectrouting.Decision, error)

func (f localDecisionFunc) Decide(ctx context.Context, input subjectrouting.Input) (subjectrouting.Decision, error) {
	return f(ctx, input)
}

func localManagerFixture(t *testing.T, enabled bool, provider subjectrouting.Provider) (*Server, localrouting.Request, string) {
	t.Helper()
	binDir := t.TempDir()
	marker := filepath.Join(binDir, "executed")
	writeExec(t, filepath.Join(binDir, "prime-agent"), "#!/bin/sh\nprintf 'unexpected execution' > '"+marker+"'\n")
	server, err := NewServer(Options{
		Listen: "127.0.0.1:0", ConfigDir: t.TempDir(), HarnessTarget: HarnessTargetNative,
		SubjectRoutingEnabled: enabled, SubjectDecisionProvider: provider,
		Discoverer: Discoverer{Path: binDir, InsideWSL: func() bool { return false }, WSL: WSLRunner{LookExe: func() (string, error) { return "", os.ErrNotExist }}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return server, localrouting.Request{
		Content: "Explain the parser in Compiler work", DefaultHarness: "prime",
		Conversations: []localrouting.Conversation{{ID: "compiler", Label: "Compiler work", Summary: "Implement the parser", Harness: "prime", Target: "native", Status: localrouting.StatusReady}},
	}, marker
}

func localManagerDecision(input subjectrouting.Input) subjectrouting.Decision {
	subjects := map[string]float64{subjectrouting.NewSubject: 0, subjectrouting.NoSubject: 0}
	for _, candidate := range input.Candidates {
		subjects[candidate.ID] = 0
	}
	subjects["compiler"] = 1
	intents := map[string]float64{subjectrouting.IntentQuestion: 1, subjectrouting.IntentAmendRunning: 0, subjectrouting.IntentNewWork: 0, subjectrouting.IntentStatus: 0, subjectrouting.IntentDecide: 0, subjectrouting.IntentUnclear: 0}
	return subjectrouting.Decision{SubjectChoice: "compiler", SubjectConfidence: 1, SubjectProbabilities: subjects, Intent: subjectrouting.IntentQuestion, IntentConfidence: 1, IntentProbabilities: intents, Model: "desktop-fixture"}
}

func readLocalManagerEvent(t *testing.T, scanner *bufio.Scanner) (string, json.RawMessage) {
	t.Helper()
	var kind string
	var data json.RawMessage
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" && kind != "" {
			return kind, data
		}
		if strings.HasPrefix(line, "event: ") {
			kind = strings.TrimPrefix(line, "event: ")
		}
		if strings.HasPrefix(line, "data: ") {
			data = json.RawMessage(strings.TrimPrefix(line, "data: "))
		}
	}
	t.Fatalf("stream ended before an event: %v", scanner.Err())
	return "", nil
}

// The transport owns early observable progress and guarantees a route does not
// execute the selected harness. Core policy tests cannot detect buffered SSE,
// accidental version execution, or credentials admitted by this HTTP adapter.
func TestLocalManagerStreamsBeforeClassificationWithoutExecuting(t *testing.T) {
	entered := make(chan subjectrouting.Input, 1)
	release := make(chan struct{})
	provider := localDecisionFunc(func(ctx context.Context, input subjectrouting.Input) (subjectrouting.Decision, error) {
		entered <- input
		select {
		case <-release:
			return localManagerDecision(input), nil
		case <-ctx.Done():
			return subjectrouting.Decision{}, ctx.Err()
		}
	})
	server, body, marker := localManagerFixture(t, true, provider)
	body.Content += "; API_TOKEN=classified-secret"
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()
	raw, _ := json.Marshal(body)
	request, _ := http.NewRequest(http.MethodPost, ts.URL+"/local/v1/manager/route", bytes.NewReader(raw))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", ts.URL)
	request.Header.Set("Authorization", "Bearer never-forward-this-header")
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("routing response %d %s", response.StatusCode, response.Header.Get("Content-Type"))
	}
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(nil, 1<<20)
	for _, expected := range []string{"inventory", "classifying"} {
		if kind, _ := readLocalManagerEvent(t, scanner); kind != expected {
			t.Fatalf("got stage %q, want %q before provider release", kind, expected)
		}
	}
	select {
	case input := <-entered:
		state, _ := json.Marshal(input)
		if strings.Contains(string(state), "classified-secret") || strings.Contains(string(state), "never-forward-this-header") || !strings.Contains(string(state), "Compiler work") {
			t.Fatalf("provider state did not preserve bounded inventory and redact credentials: %s", state)
		}
	case <-time.After(time.Second):
		t.Fatal("provider was not called")
	}
	close(release)
	for _, expected := range []string{"decision", "action", "result"} {
		kind, payload := readLocalManagerEvent(t, scanner)
		if kind != expected {
			t.Fatalf("stage %q, want %q", kind, expected)
		}
		if kind == "result" {
			var result struct {
				localrouting.Result
				RequestID string               `json:"requestId"`
				Snapshot  localrouting.Request `json:"snapshot"`
				Runtime   localrouting.Runtime `json:"runtime"`
			}
			if json.Unmarshal(payload, &result) != nil || result.RequestID == "" || result.Action.Kind != localrouting.ActionContinue || result.Action.TargetID != "compiler" {
				t.Fatalf("unexpected routing result: %s", payload)
			}
			if result.Runtime.Target != HarnessTargetNative || result.Runtime.Now.IsZero() || len(result.Runtime.AvailableHarnesses) != 1 || result.Runtime.AvailableHarnesses[0] != "prime" || len(result.Snapshot.Conversations) != 1 || result.Snapshot.Conversations[0].ID != "compiler" || strings.Contains(string(payload), "classified-secret") {
				t.Fatalf("feedback result omitted host runtime or redacted inventory: %s", payload)
			}
		}
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("routing executed a harness: %v", err)
	}
}

func localManagerRequest(t *testing.T, server *Server, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:1738/local/v1/manager/route", bytes.NewReader(raw))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	return response
}

func TestLocalManagerRequestBoundaryAndManualFallback(t *testing.T) {
	var calls atomic.Int32
	provider := localDecisionFunc(func(context.Context, subjectrouting.Input) (subjectrouting.Decision, error) {
		calls.Add(1)
		return subjectrouting.Decision{}, errors.New("private-provider-response")
	})
	server, body, marker := localManagerFixture(t, true, provider)
	for _, tc := range []struct {
		name, raw, host, origin string
		want                    int
	}{
		{"cross origin", `{"content":"hello","conversations":[]}`, "127.0.0.1:1738", "https://foreign.example", http.StatusForbidden},
		{"rebound host", `{"content":"hello","conversations":[]}`, "foreign.example", "", http.StatusForbidden},
		{"workdir field", `{"content":"hello","conversations":[],"workdir":"/private/project"}`, "127.0.0.1:1738", "", http.StatusBadRequest},
		{"trailing JSON", `{"content":"hello","conversations":[]} {}`, "127.0.0.1:1738", "", http.StatusBadRequest},
		{"oversized JSON", strings.Repeat(" ", maxLocalManagerJSON+1), "127.0.0.1:1738", "", http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "http://"+tc.host+"/local/v1/manager/route", strings.NewReader(tc.raw))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Origin", tc.origin)
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)
			if response.Code != tc.want {
				t.Fatalf("HTTP%d, want%d: %s", response.Code, tc.want, response.Body.String())
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatal("rejected requests reached the provider")
	}
	response := localManagerRequest(t, server, body)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"kind":"clarify"`) || strings.Contains(response.Body.String(), "private-provider-response") {
		t.Fatalf("provider failure was not a safe clarification: %s", response.Body.String())
	}
	body.TargetID = "compiler"
	response = localManagerRequest(t, server, body)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"kind":"continue"`) || calls.Load() != 1 {
		t.Fatalf("manual choice should route without another provider request: %s (calls %d)", response.Body.String(), calls.Load())
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("manual route executed a harness: %v", err)
	}

	server.opts.SubjectRoutingEnabled = false
	body.TargetID = ""
	response = localManagerRequest(t, server, body)
	if !strings.Contains(response.Body.String(), `"kind":"clarify"`) || calls.Load() != 1 {
		t.Fatalf("disabled classifier reached provider or routed automatically: %s", response.Body.String())
	}
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:1738/local/v1/snapshot", nil)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	var snapshot Snapshot
	if err := json.NewDecoder(io.Reader(recorder.Body)).Decode(&snapshot); err != nil || snapshot.LocalManager.Enabled || snapshot.LocalManager.Available || snapshot.LocalManager.Reason != "disabled" {
		t.Fatalf("snapshot availability: %+v (%v)", snapshot.LocalManager, err)
	}
}

// The HTTP mode switch must never turn a fixture replay into a paid provider
// call or harness execution. Scenario judgments themselves belong to the shared
// policy runner's tests.
func TestLocalManagerBenchModes(t *testing.T) {
	var calls atomic.Int32
	server, _, marker := localManagerFixture(t, true, localDecisionFunc(func(context.Context, subjectrouting.Input) (subjectrouting.Decision, error) {
		calls.Add(1)
		return subjectrouting.Decision{}, errors.New("fixture mode reached live provider")
	}))
	get := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:1738/local/v1/manager/bench", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, get)
	var catalog struct {
		Scenarios []localrouting.Scenario `json:"scenarios"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &catalog) != nil || len(catalog.Scenarios) == 0 {
		t.Fatalf("scenario inventory unavailable: %s", response.Body.String())
	}
	for _, mode := range []string{"fixture", "live"} {
		if mode == "live" {
			server.opts.SubjectRoutingEnabled = false
		}
		request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:1738/local/v1/manager/bench", strings.NewReader(`{"mode":"`+mode+`"}`))
		request.Header.Set("Content-Type", "application/json")
		response = httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if mode == "fixture" {
			var result localrouting.BenchResult
			if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.Mode != "fixture" || result.Totals.Turns == 0 || result.Provenance == "" {
				t.Fatalf("fixture replay response: HTTP%d %s", response.Code, response.Body.String())
			}
		} else if response.Code != http.StatusServiceUnavailable {
			t.Fatalf("disabled live replay HTTP%d, want503", response.Code)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("fixture replay or disabled live evaluation called the provider")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("benchmark executed a local harness: %v", err)
	}
}

func TestLocalManagerBenchPreservesInterruptedResults(t *testing.T) {
	var calls atomic.Int32
	server, _, _ := localManagerFixture(t, true, localDecisionFunc(func(ctx context.Context, input subjectrouting.Input) (subjectrouting.Decision, error) {
		if calls.Add(1) == 1 {
			decision := localManagerDecision(input)
			delete(decision.SubjectProbabilities, "compiler")
			decision.SubjectChoice = input.Candidates[0].ID
			decision.SubjectProbabilities[decision.SubjectChoice] = 1
			return decision, nil
		}
		<-ctx.Done()
		return subjectrouting.Decision{}, ctx.Err()
	}))
	// Model a host request deadline while leaving the client connected, so the
	// actual HTTP response can preserve the interrupted evaluation's evidence.
	ts := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		ctx, cancel := context.WithTimeout(request.Context(), 20*time.Millisecond)
		defer cancel()
		server.Handler().ServeHTTP(writer, request.WithContext(ctx))
	}))
	defer ts.Close()
	request, _ := http.NewRequest(http.MethodPost, ts.URL+"/local/v1/manager/bench", strings.NewReader(`{"mode":"live","scenarioIds":["switch-harnesses"]}`))
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: 2 * time.Second}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var result localrouting.BenchResult
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || result.Complete || result.ErrorReason == "" || result.Totals.Turns != 1 || len(result.Scenarios) != 1 || len(result.Scenarios[0].Turns) != 1 {
		t.Fatalf("deadline erased partial report: HTTP%d %+v", response.StatusCode, result)
	}
}
