package runapi

import (
	"bytes"
	"context"
	"encoding/json"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
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

func TestChatDisabledReturnsNotFound(t *testing.T) {
	server := chatTestServer(t, false)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/namespaces/agents/chat/threads", nil)
	request.Header.Set("Authorization", "Bearer valid")
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), "chat_disabled") {
		t.Fatalf("status = %d %s", response.Code, response.Body.String())
	}
}

func TestChatThreadCreateListGetAndAppend(t *testing.T) {
	server := chatTestServer(t, true)

	createBody := `{"profileName":"grok45","mode":"persona","title":"First thread"}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/namespaces/agents/chat/threads", bytes.NewBufferString(createBody))
	request.Header.Set("Authorization", "Bearer valid")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("create status = %d %s", response.Code, response.Body.String())
	}
	var created chat.Thread
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.Namespace != "agents" || created.ProfileName != "grok45" || created.CreatedBy != "user-1" {
		t.Fatalf("created = %#v", created)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v1/namespaces/agents/chat/threads?profileName=grok45", nil)
	request.Header.Set("Authorization", "Bearer valid")
	response = httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), created.ID) {
		t.Fatalf("list status = %d %s", response.Code, response.Body.String())
	}

	appendBody := `{"content":"hello standing chat"}`
	request = httptest.NewRequest(http.MethodPost, "/api/v1/namespaces/agents/chat/threads/"+created.ID+"/messages", bytes.NewBufferString(appendBody))
	request.Header.Set("Authorization", "Bearer valid")
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("append status = %d %s", response.Code, response.Body.String())
	}
	var appended ChatAppendResponse
	if err := json.Unmarshal(response.Body.Bytes(), &appended); err != nil {
		t.Fatal(err)
	}
	if appended.User.Role != chat.RoleUser || appended.Turn.RunName == "" {
		t.Fatalf("append = %#v", appended)
	}
	run := &agentsv1alpha1.AgentRun{}
	if err := server.writes.Get(context.Background(), types.NamespacedName{Namespace: "agents", Name: appended.Turn.RunName}, run); err != nil {
		t.Fatal(err)
	}
	run.Status.Phase = agentsv1alpha1.AgentRunPhaseSucceeded
	run.Status.Output = "This is the actual harness reply"
	if err := server.writes.Status().Update(context.Background(), run); err != nil {
		t.Fatal(err)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v1/namespaces/agents/chat/threads/"+created.ID, nil)
	request.Header.Set("Authorization", "Bearer valid")
	response = httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("get status = %d %s", response.Code, response.Body.String())
	}
	var detail ChatThreadDetailResponse
	if err := json.Unmarshal(response.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if len(detail.Messages) != 2 || detail.Messages[0].Sequence != 1 || detail.Messages[1].Role != chat.RoleAssistant {
		t.Fatalf("detail = %#v", detail)
	}
}

func TestChatPersonaCreateRequiresProfile(t *testing.T) {
	server := chatTestServer(t, true)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/namespaces/agents/chat/threads", bytes.NewBufferString(`{"mode":"persona"}`))
	request.Header.Set("Authorization", "Bearer valid")
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "profileName") {
		t.Fatalf("status = %d %s", response.Code, response.Body.String())
	}
}

func TestChatUnauthorizedNamespaceIsNotFound(t *testing.T) {
	server := chatTestServer(t, true)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/namespaces/other/chat/threads", nil)
	request.Header.Set("Authorization", "Bearer valid")
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d %s", response.Code, response.Body.String())
	}
}

func TestChatReadyRequiresStorePing(t *testing.T) {
	server := chatTestServer(t, true)
	request := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("ready with store = %d %s", response.Code, response.Body.String())
	}

	server.chatStore = nil
	request = httptest.NewRequest(http.MethodGet, "/readyz", nil)
	response = httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "chat_unavailable") {
		t.Fatalf("ready without store = %d %s", response.Code, response.Body.String())
	}
}

func TestUIConfigIncludesChatFlag(t *testing.T) {
	server := chatTestServer(t, true)
	request := httptest.NewRequest(http.MethodGet, "/ui-config.json", nil)
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"enabled":true`) {
		t.Fatalf("ui-config = %d %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"chat"`) {
		t.Fatalf("ui-config missing chat: %s", response.Body.String())
	}
}

func chatTestServer(t *testing.T, enabled bool) *Server {
	t.Helper()
	config := DefaultConfig()
	config.OIDC.Issuer = "https://issuer.example"
	config.OIDC.Audiences = []string{"anvil-agents-api"}
	config.Chat.Enabled = enabled
	config.Runs.CreateEnabled = true
	permissions := []string{PermissionRunsRead, PermissionRunsStream}
	if enabled {
		permissions = append(permissions, PermissionChatRead, PermissionChatWrite, PermissionRunsCreate)
	}
	config.Authorization.Bindings = []AuthorizationBinding{{
		Roles:       []string{"viewer"},
		Permissions: permissions,
		Namespaces:  []string{"agents"},
	}}
	scheme := runtime.NewScheme()
	if err := agentsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(config, staticAuthenticator{
		ready:     true,
		principal: testPrincipal(time.Now().Add(time.Hour)),
	}, fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&agentsv1alpha1.AgentRun{}).WithObjects(&agentsv1alpha1.AgentRunProfile{ObjectMeta: metav1.ObjectMeta{Name: "grok45", Namespace: "agents"}}).Build(), staticLogSource{}, logr.Discard())
	if err != nil {
		t.Fatal(err)
	}
	if enabled {
		server.SetChatStore(chat.NewMemoryStore())
	}
	return server
}
