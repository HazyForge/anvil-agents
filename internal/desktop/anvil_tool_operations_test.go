package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const toolTestRequestID = "c0e510a5-9a5b-4fa4-a7ef-ea7e1c46f102"
const toolTestThreadID = "a0e510a5-9a5b-4fa4-a7ef-ea7e1c46f103"

func TestAnvilToolReadOperationsScope(t *testing.T) {
	for _, tc := range []struct{ action, input, path string }{
		{"list_agents", `{}`, "agent-run-profiles?limit=100"},
		{"get_agent", `{"name":"desktop-assistant"}`, "agent-run-profiles/desktop-assistant"},
		{"list_harnesses", `{}`, "agent-harness-profiles?limit=100"},
		{"list_runs", `{}`, "agent-runs?limit=50"},
		{"list_runs", `{"limit":7}`, "agent-runs?limit=7"},
		{"get_run", `{"name":"run-123"}`, "agent-runs/run-123"},
		{"get_thread", `{"id":"` + toolTestThreadID + `"}`, "chat/threads/" + toolTestThreadID},
	} {
		t.Run(tc.action+tc.input, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "GET" || r.URL.RequestURI() != "/api/v1/namespaces/allowed/"+tc.path {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.RequestURI())
				}
				if r.Header.Get("Authorization") != "Bearer fixture-token" {
					t.Error("missing auth header")
				}
				if r.URL.Query().Has("access_token") {
					t.Error("token in query")
				}
				fmt.Fprint(w, `{"items":[]}`)
			}))
			defer server.Close()
			got, err := executeAnvilTool(context.Background(), server.Client(), server.URL, "allowed", "fixture-token", tc.action, json.RawMessage(tc.input))
			if err != nil || string(got) != `{"items":[]}` || calls != 1 {
				t.Fatalf("result=%s err=%v calls=%d", got, err, calls)
			}
		})
	}
}

func TestAnvilToolRejectsInvalidArgumentsBeforeNetwork(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; fmt.Fprint(w, `{}`) }))
	defer server.Close()
	for _, tc := range []struct{ action, input string }{
		{"delete_agent", `{}`}, {"list_agents", `{"namespace":"other"}`}, {"list_agents", `{"url":"https://elsewhere"}`},
		{"get_agent", `{"name":"../secrets"}`}, {"get_run", `{"name":"ok?access_token=x"}`}, {"get_thread", `{"id":"../../secrets"}`},
		{"list_runs", `{"limit":101}`}, {"list_runs", `{"limit":1.5}`}, {"list_runs", `{"limit":null}`},
		{"send_message", `{"profileName":"a","text":"hello"}`}, {"start_run", `{"profileName":"a","prompt":"hello"}`},
		{"send_message", `{"profileName":"a","text":"hello","requestId":"not-a-uuid"}`},
		{"start_run", `{"profileName":"a","prompt":"hello","requestId":"` + toolTestRequestID + `","harnessProfileName":"other"}`},
		{"list_agents", `null`}, {"list_agents", `[]`}, {"list_agents", `{} {}`},
	} {
		t.Run(tc.action+tc.input, func(t *testing.T) {
			_, err := executeAnvilTool(context.Background(), server.Client(), server.URL, "allowed", "fixture-token", tc.action, json.RawMessage(tc.input))
			var safe *anvilToolError
			if !errors.As(err, &safe) || safe.Code != "invalid_argument" {
				t.Fatalf("expected safe invalid argument, got %v", err)
			}
		})
	}
	if calls != 0 {
		t.Fatalf("invalid operations made %d requests", calls)
	}
}

func TestAnvilToolMessageRetryUsesCanonicalThreadAndStableRequest(t *testing.T) {
	ensures, appends := 0, 0
	stored := map[string]string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("unexpected method %s", r.Method)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch r.URL.Path {
		case "/api/v1/namespaces/allowed/chat/threads":
			ensures++
			if body["standing"] != true || body["profileName"] != "worker" || len(body) != 2 {
				t.Errorf("unexpected ensure: %v", body)
			}
			fmt.Fprintf(w, `{"id":%q,"namespace":"allowed","profileName":"worker"}`, toolTestThreadID)
		case "/api/v1/namespaces/allowed/chat/threads/" + toolTestThreadID + "/messages":
			appends++
			id, _ := body["requestId"].(string)
			text, _ := body["content"].(string)
			if id != toolTestRequestID || text != "Do this once" || len(body) != 2 {
				t.Errorf("unexpected append %v", body)
			}
			stored[id] = text
			if appends == 1 {
				w.WriteHeader(503)
				fmt.Fprint(w, `{"error":"private-upstream-secret"}`)
				return
			}
			w.WriteHeader(202)
			fmt.Fprintf(w, `{"turn":{"requestId":%q},"thread":{"id":%q}}`, id, toolTestThreadID)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	input := json.RawMessage(`{"profileName":"worker","text":"Do this once","requestId":"` + toolTestRequestID + `"}`)
	_, err := executeAnvilTool(context.Background(), server.Client(), server.URL, "allowed", "fixture-token", "send_message", input)
	if err == nil || strings.Contains(err.Error(), "private-upstream-secret") {
		t.Fatalf("unsafe error %v", err)
	}
	if _, err := executeAnvilTool(context.Background(), server.Client(), server.URL, "allowed", "fixture-token", "send_message", input); err != nil {
		t.Fatal(err)
	}
	if ensures != 2 || appends != 2 || len(stored) != 1 {
		t.Fatalf("retry counters ensure=%d append=%d stored=%d", ensures, appends, len(stored))
	}
}

func TestAnvilToolStartRunDeterministicAndProfileBound(t *testing.T) {
	names := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/v1/namespaces/allowed/agent-runs" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["profileName"] != "worker" || body["prompt"] != "Task" || len(body) != 3 || !strings.HasPrefix(body["name"], "desktop-tool-") {
			t.Errorf("unexpected run body %v", body)
		}
		names = append(names, body["name"])
		if len(names) > 1 {
			w.WriteHeader(409)
			fmt.Fprint(w, `{"error":"private-conflict-detail"}`)
			return
		}
		w.WriteHeader(201)
		fmt.Fprintf(w, `{"name":%q}`, body["name"])
	}))
	defer server.Close()
	input := json.RawMessage(`{"profileName":"worker","prompt":"Task","requestId":"` + toolTestRequestID + `"}`)
	if _, err := executeAnvilTool(context.Background(), server.Client(), server.URL, "allowed", "fixture-token", "start_run", input); err != nil {
		t.Fatal(err)
	}
	_, err := executeAnvilTool(context.Background(), server.Client(), server.URL, "allowed", "fixture-token", "start_run", input)
	var safe *anvilToolError
	if !errors.As(err, &safe) || safe.Code != "conflict" {
		t.Fatalf("expected conflict: %v", err)
	}
	if len(names) != 2 || names[0] != names[1] {
		t.Fatalf("retry produced different run names: %v", names)
	}
}

func TestAnvilToolRejectsWrongStandingScope(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		fmt.Fprintf(w, `{"id":%q,"namespace":"other","profileName":"worker"}`, toolTestThreadID)
	}))
	defer server.Close()
	_, err := executeAnvilTool(context.Background(), server.Client(), server.URL, "allowed", "fixture-token", "send_message", json.RawMessage(`{"profileName":"worker","text":"message","requestId":"`+toolTestRequestID+`"}`))
	if err == nil || calls != 1 {
		t.Fatalf("wrong scope should stop before append, calls=%d err=%v", calls, err)
	}
}

func TestAnvilToolRejectsRedirectAndUnboundedOrInvalidResponses(t *testing.T) {
	leaked := false
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked = true; fmt.Fprint(w, `{}`) }))
	defer destination.Close()
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"redirect", 302, ""}, {"auth", 401, `{"error":"private-provider-token"}`}, {"denied", 403, `{"error":"private-provider-token"}`}, {"not-found", 404, `{"error":"private-provider-token"}`},
		{"oversize", 200, `{"data":"` + strings.Repeat("x", anvilToolResponseLimit) + `"}`}, {"not-json", 200, "private-provider-token"}, {"array", 200, `[]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", destination.URL)
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			_, err := executeAnvilTool(context.Background(), server.Client(), server.URL, "allowed", "fixture-token", "list_agents", nil)
			if err == nil || strings.Contains(err.Error(), "private-provider-token") || strings.Contains(err.Error(), "fixture-token") {
				t.Fatalf("unsafe error %v", err)
			}
		})
	}
	if leaked {
		t.Fatal("followed redirect")
	}
}

func TestAnvilToolRejectsCallerScopeAndUnsafeOrigin(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; fmt.Fprint(w, `{}`) }))
	defer server.Close()
	for _, tc := range []struct{ origin, namespace, token string }{
		{server.URL, "../other", "fixture-token"},
		{server.URL + "?access_token=private", "allowed", "fixture-token"},
		{"https://user:private@example.com", "allowed", "fixture-token"},
		{"http://example.com", "allowed", "fixture-token"},
		{server.URL, "allowed", ""},
		{server.URL, "allowed", "private\r\nInjected: true"},
	} {
		_, err := executeAnvilTool(context.Background(), server.Client(), tc.origin, tc.namespace, tc.token, "list_agents", nil)
		if err == nil || strings.Contains(err.Error(), "private") {
			t.Fatalf("unsafe config error %v", err)
		}
	}
	if calls != 0 {
		t.Fatalf("invalid configuration reached API %d times", calls)
	}
}

func TestAnvilToolUsesOperationDeadlineInsteadOfDiscoveryTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(30 * time.Millisecond)
		fmt.Fprint(w, `{"items":[]}`)
	}))
	defer server.Close()
	client := server.Client()
	client.Timeout = time.Millisecond
	got, err := executeAnvilTool(context.Background(), client, server.URL, "allowed", "fixture-token", "list_agents", json.RawMessage(`{}`))
	if err != nil || string(got) != `{"items":[]}` {
		t.Fatalf("result=%s err=%v", got, err)
	}
	if client.Timeout != time.Millisecond {
		t.Fatal("shared discovery client was mutated")
	}
}
