package runapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hazyforge/anvil-agents/internal/chat"
)

func TestStandingChatEndpointKeepsExistingIdentityAndAuthorization(t *testing.T) {
	s := chatTestServer(t, true)
	post := func(ns, body, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/namespaces/"+ns+"/chat/threads", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		response := httptest.NewRecorder()
		s.routes().ServeHTTP(response, req)
		return response
	}
	body := `{"standing":true,"profileName":"grok45"}`
	first := post("agents", body, "valid")
	if first.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", first.Code, first.Body.String())
	}
	var a, b chat.Thread
	if err := json.Unmarshal(first.Body.Bytes(), &a); err != nil {
		t.Fatal(err)
	}
	second := post("agents", body, "valid")
	if second.Code != http.StatusOK {
		t.Fatalf("ensure: %d %s", second.Code, second.Body.String())
	}
	if err := json.Unmarshal(second.Body.Bytes(), &b); err != nil {
		t.Fatal(err)
	}
	if a.ID != b.ID || chatHarness(b) != "" {
		t.Fatalf("standing identity/override changed: %#v %#v", a, b)
	}
	for _, invalid := range []string{`{"standing":true,"harnessProfileName":"other"}`, `{"standing":true,"profileName":"grok45","harnessProfileName":"other"}`, `{"standing":true,"profileName":"grok45","metadata":{"coordination":{"enabled":true}}}`} {
		if got := post("agents", invalid, "valid"); got.Code != http.StatusBadRequest {
			t.Fatalf("invalid accepted: %d %s", got.Code, got.Body.String())
		}
	}
	if got := post("other", body, "valid"); got.Code != http.StatusNotFound {
		t.Fatalf("namespace widened: %d", got.Code)
	}
	if got := post("agents", body, ""); got.Code != http.StatusUnauthorized {
		t.Fatalf("auth bypass: %d", got.Code)
	}
	threads, err := s.chatStore.ListThreads(context.Background(), chat.ThreadFilter{Namespace: "agents"})
	if err != nil || len(threads) != 1 {
		t.Fatalf("invalid calls created threads: %d %v", len(threads), err)
	}
	s.authorizer = NewAuthorizer(AuthorizationConfig{Bindings: []AuthorizationBinding{{Roles: []string{"viewer"}, Permissions: []string{PermissionChatWrite}, Namespaces: []string{"agents"}}}})
	if got := post("agents", body, "valid"); got.Code != http.StatusNotFound {
		t.Fatalf("ensure leaked existing thread to write-only binding: %d", got.Code)
	}
}
