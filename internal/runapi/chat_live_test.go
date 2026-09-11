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
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	agentsv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
	"github.com/hazyforge/anvil-agents/internal/chat"
)

func TestLiveReplySpawnsProfilesAndPeerMessages(t *testing.T) {
	server := liveChatTestServer(t)
	create := httptest.NewRequest(http.MethodPost, "/api/v1/namespaces/agents/chat/threads", bytes.NewBufferString(`{"profileName":"coordinator","mode":"persona","title":"Hire"}`))
	create.Header.Set("Authorization", "Bearer valid")
	create.Header.Set("Content-Type", "application/json")
	created := httptest.NewRecorder()
	server.routes().ServeHTTP(created, create)
	if created.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", created.Code, created.Body.String())
	}
	var thread chat.Thread
	if err := json.Unmarshal(created.Body.Bytes(), &thread); err != nil {
		t.Fatal(err)
	}

	appendReq := httptest.NewRequest(http.MethodPost, "/api/v1/namespaces/agents/chat/threads/"+thread.ID+"/messages", bytes.NewBufferString(`{"content":"Hire a researcher and a reviewer and have them introduce themselves."}`))
	appendReq.Header.Set("Authorization", "Bearer valid")
	appendReq.Header.Set("Content-Type", "application/json")
	appended := httptest.NewRecorder()
	server.routes().ServeHTTP(appended, appendReq)
	if appended.Code != http.StatusCreated {
		t.Fatalf("append = %d %s", appended.Code, appended.Body.String())
	}
	var body ChatAppendResponse
	if err := json.Unmarshal(appended.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(body.Assistant.Content, "LangGraph") || strings.Contains(string(body.Assistant.Metadata), `"stub":true`) {
		t.Fatalf("still stub: %s", appended.Body.String())
	}
	if len(body.Replies) < 3 {
		t.Fatalf("want coordinator + 2 peers, got %d: %s", len(body.Replies), appended.Body.String())
	}
	if len(body.Spawned) != 2 {
		t.Fatalf("spawned = %#v", body.Spawned)
	}
	var profiles agentsv1alpha1.AgentRunProfileList
	if err := server.writes.List(t.Context(), &profiles); err != nil {
		t.Fatal(err)
	}
	if len(profiles.Items) != 2 {
		t.Fatalf("profiles = %d", len(profiles.Items))
	}
	for _, profile := range profiles.Items {
		if profile.Labels[LabelManagedBy] != ManagedByConsole {
			t.Fatalf("profile %s missing console managed-by", profile.Name)
		}
	}
}

func TestHeuristicExtractsNamedAgents(t *testing.T) {
	plan := heuristicChatPlan("Please create agents named atlas and nova", "coordinator")
	if len(plan.Spawn) != 2 {
		t.Fatalf("spawn = %#v", plan.Spawn)
	}
	if plan.Spawn[0].Name != "atlas" || plan.Spawn[1].Name != "nova" {
		t.Fatalf("names = %#v", plan.Spawn)
	}
}

func liveChatTestServer(t *testing.T) *Server {
	t.Helper()
	config := DefaultConfig()
	config.OIDC.Issuer = "https://issuer.example"
	config.OIDC.Audiences = []string{"anvil-agents-api"}
	config.Chat.Enabled = true
	config.Composition.ReadEnabled = true
	config.Composition.WriteEnabled = true
	config.Authorization.Bindings = []AuthorizationBinding{{
		Roles: []string{"viewer"},
		Permissions: []string{
			PermissionRunsRead, PermissionRunsStream,
			PermissionChatRead, PermissionChatWrite,
			PermissionCompositionRead, PermissionCompositionWrite,
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
	if server.writes == nil {
		server.writes = kube
	}
	return server
}
