package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func putJSON(t *testing.T, url, body string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodPut, url, bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("new PUT request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("PUT %s: %v", url, err)
	}
	return response
}

func discardBody(response *http.Response) {
	_, _ = io.Copy(io.Discard, response.Body)
	response.Body.Close()
}

type stubRunner struct {
	mu       sync.Mutex
	calls    [][]string
	handlers map[string]func(args []string) (string, error)
}

func (r *stubRunner) Run(_ context.Context, args ...string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, append([]string(nil), args...))
	key := strings.Join(args, " ")
	if handler, ok := r.handlers[key]; ok {
		return handler(args)
	}
	return "", fmt.Errorf("unexpected kubectl call: %s", key)
}

func newTestServer(runner Runner) *httptest.Server {
	srv := &server{runner: runner, defaultTemplate: "demo-ns/demo-tmpl"}
	return httptest.NewServer(srv)
}

func TestSplitPath(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		path                   string
		atespace, name, action string
		ok                     bool
	}{
		{"/shim/v1/actors/agents/chat-a", "agents", "chat-a", "", true},
		{"/shim/v1/actors/agents/chat-a:resume", "agents", "chat-a", "resume", true},
		{"/shim/v1/actors/agents/chat-a:suspend", "agents", "chat-a", "suspend", true},
		{"/shim/v1/other/agents/chat-a", "", "", "", false},
		{"/shim/v1/actors/agents", "", "", "", false},
		{"/shim/v1/actors/agents/", "", "", "", false},
	} {
		atespace, name, action, ok := splitPath(tc.path)
		if ok != tc.ok || atespace != tc.atespace || name != tc.name || action != tc.action {
			t.Fatalf("splitPath(%q) = %q/%q/%q ok=%v, want %q/%q/%q ok=%v",
				tc.path, atespace, name, action, ok, tc.atespace, tc.name, tc.action, tc.ok)
		}
	}
}

func TestKubectlStatusMapping(t *testing.T) {
	t.Parallel()

	table := `
NAME   NAMESPACE   TEMPLATE   ID   STATUS_RUNNING   POD   IP   VERSION
chat-a demo-ns      counter    x    STATUS_RUNNING   w/p   1.2  3
`
	state, err := kubectlStatus(table)
	if err != nil || state != "Active" {
		t.Fatalf("running status = %q err=%v, want Active", state, err)
	}
	for token, want := range map[string]string{
		"STATUS_RESUMING": "Active", "STATUS_SUSPENDED": "Suspended",
		"STATUS_SUSPENDING": "Suspended", "STATUS_PAUSED": "Paused",
		"STATUS_PAUSING": "Paused",
	} {
		if state, err := kubectlStatus("x " + token + " y"); err != nil || state != want {
			t.Fatalf("status %s = %q err=%v, want %q", token, state, err, want)
		}
	}
	if _, err := kubectlStatus("STATUS_CRASHED"); err == nil {
		t.Fatal("crashed actor must surface an error, not a state")
	}
	if _, err := kubectlStatus("no status column here"); err == nil {
		t.Fatal("missing STATUS token must surface an error")
	}
}

func decodeResponse(t *testing.T, response *http.Response) (actorResponse, int) {
	t.Helper()
	defer response.Body.Close()
	var body actorResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode shim response: %v", err)
	}
	return body, response.StatusCode
}

func TestShimCreateThenLifecycle(t *testing.T) {
	t.Parallel()

	runner := &stubRunner{handlers: map[string]func(args []string) (string, error){}}
	runner.handlers["ate create actor chat-a --atespace agents --template demo-ns/demo-tmpl"] = func(args []string) (string, error) {
		return "created", nil
	}
	runner.handlers["ate get actor chat-a --atespace agents"] = func(args []string) (string, error) {
		return "chat-a STATUS_RUNNING", nil
	}
	runner.handlers["ate suspend actor chat-a --atespace agents"] = func(args []string) (string, error) {
		return "suspended", nil
	}
	backend := newTestServer(runner)
	defer backend.Close()

	createdResp := putJSON(t, backend.URL+"/shim/v1/actors/agents/chat-a", `{}`)
	created, status := decodeResponse(t, createdResp)
	if status != http.StatusCreated || created.State != "Suspended" || created.ID != "agents/chat-a" {
		t.Fatalf("create = %+v status=%d, want 201 Suspended with atespace/name ID", created, status)
	}

	suspendReq, _ := http.NewRequest(http.MethodPost, backend.URL+"/shim/v1/actors/agents/chat-a:suspend", nil)
	suspendResp, err := http.DefaultClient.Do(suspendReq)
	if err != nil {
		t.Fatalf("suspend: %v", err)
	}
	suspended, status := decodeResponse(t, suspendResp)
	if status != http.StatusOK || suspended.State != "Active" {
		t.Fatalf("suspend = %+v status=%d, want 200 with post-op observed state", suspended, status)
	}

	described, err := http.Get(backend.URL + "/shim/v1/actors/agents/chat-a")
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	body, status := decodeResponse(t, described)
	if status != http.StatusOK || body.State != "Active" {
		t.Fatalf("describe = %+v status=%d, want 200 Active", body, status)
	}
}

func TestShimCreateReuseOnAlreadyExists(t *testing.T) {
	t.Parallel()

	runner := &stubRunner{handlers: map[string]func(args []string) (string, error){}}
	runner.handlers["ate create actor chat-a --atespace agents --template demo-ns/demo-tmpl"] = func(args []string) (string, error) {
		return "", errors.New("kubectl ate create actor: exit status 1: AlreadyExists: actor already exists")
	}
	runner.handlers["ate get actor chat-a --atespace agents"] = func(args []string) (string, error) {
		return "chat-a STATUS_SUSPENDED", nil
	}
	backend := newTestServer(runner)
	defer backend.Close()

	createdResp := putJSON(t, backend.URL+"/shim/v1/actors/agents/chat-a", `{}`)
	created, status := decodeResponse(t, createdResp)
	if status != http.StatusOK || !created.Reused || created.State != "Suspended" {
		t.Fatalf("reuse = %+v status=%d, want 200 reused Suspended", created, status)
	}
}

func TestShimRejectsBadInputAndMissingActors(t *testing.T) {
	t.Parallel()

	runner := &stubRunner{handlers: map[string]func(args []string) (string, error){}}
	runner.handlers["ate get actor missing --atespace agents"] = func(args []string) (string, error) {
		return "", errors.New("kubectl ate get actor: exit status 1: NotFound: no such actor")
	}
	srv := &server{runner: runner, defaultTemplate: ""}
	backend := httptest.NewServer(srv)
	defer backend.Close()

	// No template anywhere: create must fail closed, not invent one.
	createdResp := putJSON(t, backend.URL+"/shim/v1/actors/agents/chat-a", `{}`)
	discardBody(createdResp)
	if createdResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("create without template status = %d, want 400", createdResp.StatusCode)
	}

	described, err := http.Get(backend.URL + "/shim/v1/actors/agents/missing")
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	discardBody(described)
	if described.StatusCode != http.StatusNotFound {
		t.Fatalf("describe missing status = %d, want 404", described.StatusCode)
	}

	// Invalid DNS labels never reach kubectl.
	invalid, err := http.Get(backend.URL + "/shim/v1/actors/UPPER/chat-a")
	if err != nil {
		t.Fatalf("invalid atespace: %v", err)
	}
	discardBody(invalid)
	if invalid.StatusCode != http.StatusNotFound {
		t.Fatalf("invalid atespace status = %d, want 404", invalid.StatusCode)
	}
	runner.mu.Lock()
	defer runner.mu.Unlock()
	for _, call := range runner.calls {
		for _, arg := range call {
			if arg == "UPPER" {
				t.Fatalf("invalid atespace reached kubectl: %v", runner.calls)
			}
		}
	}
}
