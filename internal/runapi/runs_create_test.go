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
)

func TestCreateAgentRunFromCard(t *testing.T) {
	server := createRunTestServer(t, true)
	body := `{
		"generateName": "card-audit-",
		"prompt": "Review production health",
		"profileName": "hazy-trade-production-auditor",
		"skillSetNames": ["repo-review"],
		"toolSetNames": ["kb"],
		"intent": "observe",
		"sourceKind": "ConsoleCard",
		"sourceName": "card-123"
	}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/namespaces/agents/agent-runs", bytes.NewBufferString(body))
	request.Header.Set("Authorization", "Bearer valid")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d %s", response.Code, response.Body.String())
	}
	var view AgentRunView
	if err := json.Unmarshal(response.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.Namespace != "agents" || view.Name == "" {
		t.Fatalf("unexpected view: %#v", view)
	}
	if !strings.HasPrefix(view.Name, "card-audit-") {
		t.Fatalf("expected generateName prefix, got %q", view.Name)
	}
}

func TestCreateAgentRunDisabled(t *testing.T) {
	server := createRunTestServer(t, false)
	body := `{"prompt":"x","profileName":"p"}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/namespaces/agents/agent-runs", bytes.NewBufferString(body))
	request.Header.Set("Authorization", "Bearer valid")
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d %s", response.Code, response.Body.String())
	}
}

func createRunTestServer(t *testing.T, createEnabled bool) *Server {
	t.Helper()
	config := DefaultConfig()
	config.OIDC.Issuer = "https://issuer.example"
	config.OIDC.Audiences = []string{"anvil-agents-api"}
	config.Runs.CreateEnabled = createEnabled
	permissions := []string{PermissionRunsRead, PermissionRunsStream}
	if createEnabled {
		permissions = append(permissions, PermissionRunsCreate)
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
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	server, err := NewServer(config, staticAuthenticator{
		ready:     true,
		principal: testPrincipal(time.Now().Add(time.Hour)),
	}, fakeClient, staticLogSource{}, logr.Discard())
	if err != nil {
		t.Fatal(err)
	}
	return server
}
