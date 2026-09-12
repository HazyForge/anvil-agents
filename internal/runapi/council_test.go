package runapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	agentsv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
	"github.com/hazyforge/anvil-agents/internal/chat"
)

func TestAnvilCouncilConnectConferInterruptAndParallelRuns(t *testing.T) {
	server := councilTestServer(t)

	request := httptest.NewRequest(http.MethodPost, "/api/v1/namespaces/agents/anvil-council", bytes.NewBufferString(`{}`))
	request.Header.Set("Authorization", "Bearer valid")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("connect status = %d %s", response.Code, response.Body.String())
	}
	var connected CouncilState
	if err := json.Unmarshal(response.Body.Bytes(), &connected); err != nil {
		t.Fatal(err)
	}
	if !connected.Connected || connected.Controller != anvilAgentProfileName || connected.Thread.ID == "" {
		t.Fatalf("connected = %#v", connected)
	}
	if len(connected.Knowledge) < 1 {
		t.Fatalf("expected auto-attached knowledge, got %#v", connected.Knowledge)
	}
	if len(connected.Memory) < 1 {
		t.Fatalf("expected unified memory, got %#v", connected.Memory)
	}
	if len(connected.Members) != 3 {
		t.Fatalf("members = %#v", connected.Members)
	}

	turnBody := `{"content":"Both of you start by inventorying the same namespace, then implement the mixed-harness proof in parallel."}`
	request = httptest.NewRequest(http.MethodPost, "/api/v1/namespaces/agents/anvil-council/turns", bytes.NewBufferString(turnBody))
	request.Header.Set("Authorization", "Bearer valid")
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("turn status = %d %s", response.Code, response.Body.String())
	}
	var turned CouncilTurnResponse
	if err := json.Unmarshal(response.Body.Bytes(), &turned); err != nil {
		t.Fatal(err)
	}
	if turned.User.Role != chat.RoleUser {
		t.Fatalf("user = %#v", turned.User)
	}
	if len(turned.Confer) != 3 {
		t.Fatalf("confer = %#v", turned.Confer)
	}
	profiles := map[string]string{}
	for _, line := range turned.Confer {
		if line.AuthorProfile == "" {
			t.Fatalf("hive-mind line without authorProfile: %#v", line)
		}
		profiles[line.AuthorProfile] = line.Content
	}
	if _, ok := profiles[anvilAgentProfileName]; !ok {
		t.Fatalf("Anvil agent did not confer: %#v", turned.Confer)
	}
	if _, ok := profiles[councilResearcherProfile]; !ok {
		t.Fatalf("researcher did not confer: %#v", turned.Confer)
	}
	if turned.Interrupt == nil || turned.Interrupt.AuthorProfile != councilImplementerProfile || turned.Interrupt.Kind != "interrupt" {
		t.Fatalf("interrupt = %#v", turned.Interrupt)
	}
	if strings.Contains(turned.Confer[0].Content, turned.Confer[1].Content) && turned.Confer[0].AuthorProfile == turned.Confer[1].AuthorProfile {
		t.Fatalf("expected distinct member lines, got %#v", turned.Confer)
	}
	if len(turned.DelegatedRuns) != 2 {
		t.Fatalf("delegated runs = %#v", turned.DelegatedRuns)
	}
	if turned.DelegatedRuns[0].HarnessProfileName == turned.DelegatedRuns[1].HarnessProfileName {
		t.Fatalf("expected mixed harnesses, got %#v", turned.DelegatedRuns)
	}
	backends := map[string]string{}
	for _, run := range turned.DelegatedRuns {
		backends[run.Role] = run.Backend
		if run.Name == "" || run.HarnessProfileName == "" {
			t.Fatalf("incomplete run %#v", run)
		}
	}
	if backends["researcher"] == backends["implementer"] {
		t.Fatalf("expected mixed backends, got %#v", turned.DelegatedRuns)
	}
	claimed := map[string]string{}
	for _, entry := range turned.Memory {
		claimed[entry.Key] = entry.Value
	}
	if claimed["claim:inventory"] != councilResearcherProfile || claimed["claim:implement"] != councilImplementerProfile {
		t.Fatalf("shared memory claims = %#v", turned.Memory)
	}

	list := &agentsv1alpha1.AgentRunList{}
	if err := server.runs.List(t.Context(), list); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 2 {
		t.Fatalf("cluster runs = %d", len(list.Items))
	}
	harnesses := map[string]struct{}{}
	for i := range list.Items {
		run := &list.Items[i]
		if run.Spec.SourceRef.Kind != "AgentCouncil" || run.Spec.SourceRef.Name != anvilCouncilName {
			t.Fatalf("run source = %#v", run.Spec.SourceRef)
		}
		if run.Spec.HarnessProfileRef == nil {
			t.Fatalf("run missing harness: %#v", run.Spec)
		}
		harnesses[run.Spec.HarnessProfileRef.Name] = struct{}{}
	}
	if len(harnesses) != 2 {
		t.Fatalf("expected two harness refs, got %#v", harnesses)
	}
}

func TestAnvilCouncilDisabledWithoutChat(t *testing.T) {
	server := chatTestServer(t, false)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/namespaces/agents/anvil-council", nil)
	request.Header.Set("Authorization", "Bearer valid")
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d %s", response.Code, response.Body.String())
	}
}

func councilTestServer(t *testing.T) *Server {
	t.Helper()
	config := DefaultConfig()
	config.OIDC.Issuer = "https://issuer.example"
	config.OIDC.Audiences = []string{"anvil-agents-api"}
	config.Chat.Enabled = true
	config.Composition.ReadEnabled = true
	config.Composition.WriteEnabled = true
	config.Runs.CreateEnabled = true
	config.Authorization.Bindings = []AuthorizationBinding{{
		Roles: []string{"viewer"},
		Permissions: []string{
			PermissionRunsRead, PermissionRunsStream, PermissionRunsCreate,
			PermissionCompositionRead, PermissionCompositionWrite,
			PermissionChatRead, PermissionChatWrite,
		},
		Namespaces: []string{"agents"},
	}}
	scheme := runtime.NewScheme()
	if err := agentsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).Build()
	server, err := NewServer(config, staticAuthenticator{
		ready:     true,
		principal: testPrincipal(time.Now().Add(time.Hour)),
	}, kube, staticLogSource{}, logr.Discard())
	if err != nil {
		t.Fatal(err)
	}
	server.SetChatStore(chat.NewMemoryStore())
	if _, ok := server.runs.(client.Client); !ok {
		t.Fatal("expected writable fake client")
	}
	return server
}
