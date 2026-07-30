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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	agentsv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

func TestCompositionListAndGet(t *testing.T) {
	gitops := &agentsv1alpha1.AgentSkillSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "gitops-skills",
			Namespace:       "agents",
			UID:             types.UID("gitops-uid"),
			ResourceVersion: "1",
			Labels: map[string]string{
				"argocd.argoproj.io/instance": "agents",
			},
		},
		Spec: agentsv1alpha1.AgentSkillSetSpec{Description: "from git"},
	}
	console := &agentsv1alpha1.AgentSkillSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "console-skills",
			Namespace:       "agents",
			UID:             types.UID("console-uid"),
			ResourceVersion: "2",
			Labels: map[string]string{
				LabelManagedBy: ManagedByConsole,
			},
		},
		Spec: agentsv1alpha1.AgentSkillSetSpec{Description: "from console"},
	}
	server := compositionTestServer(t, true, true, gitops, console)

	request := httptest.NewRequest(http.MethodGet, "/api/v1/namespaces/agents/agent-skill-sets?limit=200", nil)
	request.Header.Set("Authorization", "Bearer valid")
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("list status = %d %s", response.Code, response.Body.String())
	}
	var list CompositionListResponse
	if err := json.Unmarshal(response.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(list.Items))
	}
	byName := map[string]CompositionDocument{}
	for _, item := range list.Items {
		byName[item.Metadata.Name] = item
	}
	if byName["gitops-skills"].Management.Writable {
		t.Fatal("gitops object unexpectedly writable")
	}
	if byName["gitops-skills"].Management.Reason != managementReasonGitOpsProtected {
		t.Fatalf("gitops reason = %q", byName["gitops-skills"].Management.Reason)
	}
	if !byName["console-skills"].Management.Writable {
		t.Fatal("console object not writable")
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v1/namespaces/agents/agent-skill-sets/console-skills", nil)
	request.Header.Set("Authorization", "Bearer valid")
	response = httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "from console") {
		t.Fatalf("get status = %d %s", response.Code, response.Body.String())
	}
}

func TestCompositionRegistryIncludesCanonicalCapabilityKinds(t *testing.T) {
	t.Parallel()
	want := map[string]string{
		"agent-skills":      "AgentSkill",
		"agent-tools":       "AgentTool",
		"agent-mcp-servers": "AgentMCPServer",
		"agent-mcp-sets":    "AgentMCPSet",
	}
	for segment, kindName := range want {
		registered, ok := compositionKinds[segment]
		if !ok {
			t.Errorf("compositionKinds missing %q", segment)
			continue
		}
		if registered.Kind != kindName {
			t.Errorf("compositionKinds[%q].Kind = %q, want %q", segment, registered.Kind, kindName)
		}
		if registered.NewObject() == nil || registered.NewList() == nil {
			t.Errorf("compositionKinds[%q] has nil constructors", segment)
		}
	}
}

func TestCanonicalCapabilityCompositionCRUD(t *testing.T) {
	tests := []struct {
		segment string
		name    string
		spec    string
	}{
		{"agent-skills", "review", `{"inline":{"skillMD":"# Review"}}`},
		{"agent-tools", "query", `{"executable":{"name":"query","path":"query"},"source":{"inlineScript":{"interpreter":["sh"],"script":"exit 0"}},"verifyCommand":["query","--help"]}`},
		{"agent-mcp-servers", "knowledge", `{"transport":{"stdio":{"command":["knowledge-mcp"]}}}`},
		{"agent-mcp-sets", "research", `{"serverRefs":[{"name":"knowledge"}]}`},
	}
	for _, test := range tests {
		t.Run(test.segment, func(t *testing.T) {
			server := compositionTestServer(t, true, true)
			body := `{"metadata":{"name":"` + test.name + `"},"spec":` + test.spec + `}`
			request := httptest.NewRequest(http.MethodPost, "/api/v1/namespaces/agents/"+test.segment, bytes.NewBufferString(body))
			request.Header.Set("Authorization", "Bearer valid")
			response := httptest.NewRecorder()
			server.routes().ServeHTTP(response, request)
			if response.Code != http.StatusCreated {
				t.Fatalf("create status = %d %s", response.Code, response.Body.String())
			}
			if !strings.Contains(response.Body.String(), `"`+test.name+`"`) || !strings.Contains(response.Body.String(), `"console_managed"`) {
				t.Fatalf("create response missing name/management: %s", response.Body.String())
			}
			var created CompositionDocument
			if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
				t.Fatal(err)
			}
			listRequest := httptest.NewRequest(http.MethodGet, "/api/v1/namespaces/agents/"+test.segment, nil)
			listRequest.Header.Set("Authorization", "Bearer valid")
			listResponse := httptest.NewRecorder()
			server.routes().ServeHTTP(listResponse, listRequest)
			if listResponse.Code != http.StatusOK || !strings.Contains(listResponse.Body.String(), `"`+test.name+`"`) {
				t.Fatalf("list status = %d %s", listResponse.Code, listResponse.Body.String())
			}

			var updatedSpec map[string]any
			if err := json.Unmarshal(created.Spec, &updatedSpec); err != nil {
				t.Fatal(err)
			}
			updatedSpec["description"] = "updated"
			updateBody, err := json.Marshal(map[string]any{
				"metadata": map[string]any{"name": test.name, "resourceVersion": created.Metadata.ResourceVersion, "labels": created.Metadata.Labels},
				"spec":     updatedSpec,
			})
			if err != nil {
				t.Fatal(err)
			}
			updateRequest := httptest.NewRequest(http.MethodPut, "/api/v1/namespaces/agents/"+test.segment+"/"+test.name, bytes.NewReader(updateBody))
			updateRequest.Header.Set("Authorization", "Bearer valid")
			updateResponse := httptest.NewRecorder()
			server.routes().ServeHTTP(updateResponse, updateRequest)
			if updateResponse.Code != http.StatusOK || !strings.Contains(updateResponse.Body.String(), "updated") {
				t.Fatalf("update status = %d %s", updateResponse.Code, updateResponse.Body.String())
			}

			deleteRequest := httptest.NewRequest(http.MethodDelete, "/api/v1/namespaces/agents/"+test.segment+"/"+test.name, nil)
			deleteRequest.Header.Set("Authorization", "Bearer valid")
			deleteResponse := httptest.NewRecorder()
			server.routes().ServeHTTP(deleteResponse, deleteRequest)
			if deleteResponse.Code != http.StatusNoContent {
				t.Fatalf("delete status = %d %s", deleteResponse.Code, deleteResponse.Body.String())
			}
		})
	}
}

func TestCompositionCreateStampsConsoleManaged(t *testing.T) {
	server := compositionTestServer(t, true, true)

	body := `{
		"metadata": {"name": "new-skills"},
		"spec": {"description": "created", "skills": [{"name": "a", "content": "do things"}]}
	}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/namespaces/agents/agent-skill-sets", bytes.NewBufferString(body))
	request.Header.Set("Authorization", "Bearer valid")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("create status = %d %s", response.Code, response.Body.String())
	}
	var doc CompositionDocument
	if err := json.Unmarshal(response.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Metadata.Labels[LabelManagedBy] != ManagedByConsole {
		t.Fatalf("labels = %#v", doc.Metadata.Labels)
	}
	if !doc.Management.Writable {
		t.Fatalf("management = %#v", doc.Management)
	}
}

func TestCompositionUpdateRejectsGitOpsProtected(t *testing.T) {
	gitops := &agentsv1alpha1.AgentSkillSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "gitops-skills",
			Namespace:       "agents",
			UID:             types.UID("gitops-uid"),
			ResourceVersion: "5",
			Labels: map[string]string{
				"argocd.argoproj.io/instance": "agents",
			},
		},
		Spec: agentsv1alpha1.AgentSkillSetSpec{Description: "from git"},
	}
	server := compositionTestServer(t, true, true, gitops)

	body := `{
		"metadata": {"name": "gitops-skills", "resourceVersion": "5"},
		"spec": {"description": "overwrite attempt"}
	}`
	request := httptest.NewRequest(http.MethodPut, "/api/v1/namespaces/agents/agent-skill-sets/gitops-skills", bytes.NewBufferString(body))
	request.Header.Set("Authorization", "Bearer valid")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "gitops_protected") {
		t.Fatalf("expected gitops_protected, got %d %s", response.Code, response.Body.String())
	}
}

func TestCompositionUpdateConsoleManaged(t *testing.T) {
	console := &agentsv1alpha1.AgentSkillSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "console-skills",
			Namespace:       "agents",
			UID:             types.UID("console-uid"),
			ResourceVersion: "7",
			Labels: map[string]string{
				LabelManagedBy: ManagedByConsole,
			},
		},
		Spec: agentsv1alpha1.AgentSkillSetSpec{Description: "old"},
	}
	server := compositionTestServer(t, true, true, console)

	body := `{
		"metadata": {"name": "console-skills", "resourceVersion": "7", "labels": {"control.anvil.hazyforge.io/managed-by": "anvil-agents-console"}},
		"spec": {"description": "new"}
	}`
	request := httptest.NewRequest(http.MethodPut, "/api/v1/namespaces/agents/agent-skill-sets/console-skills", bytes.NewBufferString(body))
	request.Header.Set("Authorization", "Bearer valid")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("update status = %d %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"description":"new"`) {
		t.Fatalf("expected updated description, got %s", response.Body.String())
	}
}

func TestCompositionUpdateResourceVersionConflict(t *testing.T) {
	console := &agentsv1alpha1.AgentSkillSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "console-skills",
			Namespace:       "agents",
			UID:             types.UID("console-uid"),
			ResourceVersion: "7",
			Labels: map[string]string{
				LabelManagedBy: ManagedByConsole,
			},
		},
		Spec: agentsv1alpha1.AgentSkillSetSpec{Description: "old"},
	}
	server := compositionTestServer(t, true, true, console)

	body := `{
		"metadata": {"name": "console-skills", "resourceVersion": "6"},
		"spec": {"description": "new"}
	}`
	request := httptest.NewRequest(http.MethodPut, "/api/v1/namespaces/agents/agent-skill-sets/console-skills", bytes.NewBufferString(body))
	request.Header.Set("Authorization", "Bearer valid")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "conflict") {
		t.Fatalf("expected conflict, got %d %s", response.Code, response.Body.String())
	}
}

func TestCompositionDeleteRejectsUnlabeled(t *testing.T) {
	unmanaged := &agentsv1alpha1.AgentSkillSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "unmanaged",
			Namespace:       "agents",
			UID:             types.UID("u-uid"),
			ResourceVersion: "1",
		},
		Spec: agentsv1alpha1.AgentSkillSetSpec{Description: "kubectl created"},
	}
	server := compositionTestServer(t, true, true, unmanaged)

	request := httptest.NewRequest(http.MethodDelete, "/api/v1/namespaces/agents/agent-skill-sets/unmanaged", nil)
	request.Header.Set("Authorization", "Bearer valid")
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "not_console_managed") {
		t.Fatalf("expected not_console_managed, got %d %s", response.Code, response.Body.String())
	}
}

func TestCompositionWriteDisabled(t *testing.T) {
	server := compositionTestServer(t, true, false)
	body := `{"metadata":{"name":"x"},"spec":{"description":"x"}}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/namespaces/agents/agent-skill-sets", bytes.NewBufferString(body))
	request.Header.Set("Authorization", "Bearer valid")
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	// Without write permission (composition write disabled strips write from bindings), expect 404.
	if response.Code != http.StatusNotFound {
		t.Fatalf("expected not found without write grant, got %d %s", response.Code, response.Body.String())
	}
}

func TestCompositionAuthSessionAppendOnly(t *testing.T) {
	session := &agentsv1alpha1.AgentAuthSession{
		TypeMeta: metav1.TypeMeta{APIVersion: agentsv1alpha1.GroupVersion.String(), Kind: "AgentAuthSession"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "grok-reauth-home",
			Namespace: "agents",
		},
		Spec: agentsv1alpha1.AgentAuthSessionSpec{
			Provider:         agentsv1alpha1.AgentAuthSessionProviderGrokBuild,
			Action:           agentsv1alpha1.AgentAuthSessionActionReauth,
			DataVolumeRef:    corev1.LocalObjectReference{Name: "home"},
			StagingSecretRef: &corev1.LocalObjectReference{Name: "staging"},
			SeedID:           "seed-1",
		},
		Status: agentsv1alpha1.AgentAuthSessionStatus{Phase: agentsv1alpha1.AgentAuthSessionPhaseRunning},
	}
	server := compositionTestServer(t, true, true, session)

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/namespaces/agents/agent-auth-sessions", nil)
	listReq.Header.Set("Authorization", "Bearer valid")
	listRes := httptest.NewRecorder()
	server.routes().ServeHTTP(listRes, listReq)
	if listRes.Code != http.StatusOK {
		t.Fatalf("list status = %d %s", listRes.Code, listRes.Body.String())
	}
	if !strings.Contains(listRes.Body.String(), `"AgentAuthSession"`) || !strings.Contains(listRes.Body.String(), "grok-reauth-home") {
		t.Fatalf("list body missing session: %s", listRes.Body.String())
	}

	body := `{"metadata":{"name":"x"},"spec":{"provider":"grokBuild","action":"logout","dataVolumeRef":{"name":"home"}}}`
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/namespaces/agents/agent-auth-sessions", bytes.NewBufferString(body))
	createReq.Header.Set("Authorization", "Bearer valid")
	createRes := httptest.NewRecorder()
	server.routes().ServeHTTP(createRes, createReq)
	if createRes.Code != http.StatusForbidden {
		t.Fatalf("create expected forbidden, got %d %s", createRes.Code, createRes.Body.String())
	}
	if !strings.Contains(createRes.Body.String(), "append_only") {
		t.Fatalf("expected append_only code, got %s", createRes.Body.String())
	}
}

func TestUIConfigIncludesCompositionFlags(t *testing.T) {
	server := compositionTestServer(t, true, true)
	request := httptest.NewRequest(http.MethodGet, "/ui-config.json", nil)
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"readEnabled":true`) || !strings.Contains(response.Body.String(), `"writeEnabled":true`) {
		t.Fatalf("ui-config missing composition flags: %s", response.Body.String())
	}
}

func compositionTestServer(t *testing.T, readEnabled, writeEnabled bool, objects ...runtime.Object) *Server {
	t.Helper()
	config := DefaultConfig()
	config.OIDC.Issuer = "https://issuer.example"
	config.OIDC.Audiences = []string{"anvil-agents-api"}
	config.Composition.ReadEnabled = readEnabled
	config.Composition.WriteEnabled = writeEnabled
	permissions := []string{PermissionRunsRead, PermissionRunsStream}
	if readEnabled {
		permissions = append(permissions, PermissionCompositionRead)
	}
	if writeEnabled {
		permissions = append(permissions, PermissionCompositionWrite)
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
	builder := fake.NewClientBuilder().WithScheme(scheme)
	if len(objects) > 0 {
		builder = builder.WithRuntimeObjects(objects...)
	}
	fakeClient := builder.Build()
	server, err := NewServer(config, staticAuthenticator{
		ready:     true,
		principal: testPrincipal(time.Now().Add(time.Hour)),
	}, fakeClient, staticLogSource{}, logr.Discard())
	if err != nil {
		t.Fatal(err)
	}
	return server
}
