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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	agentsv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

func controlsTestServer(t *testing.T, readEnabled, writeEnabled bool, namespaces []string, objects ...runtime.Object) *Server {
	t.Helper()
	config := DefaultConfig()
	config.OIDC.Issuer = "https://issuer.example"
	config.OIDC.Audiences = []string{"anvil-agents-api"}
	config.Controls.ReadEnabled = readEnabled
	config.Controls.WriteEnabled = writeEnabled
	permissions := []string{PermissionRunsRead}
	if readEnabled {
		permissions = append(permissions, PermissionControlsRead)
	}
	if writeEnabled {
		permissions = append(permissions, PermissionControlsWrite)
	}
	config.Authorization.Bindings = []AuthorizationBinding{{
		Roles:       []string{"viewer"},
		Permissions: permissions,
		Namespaces:  namespaces,
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

func controlObject(name, application, policy string, labels map[string]string) *agentsv1alpha1.AgentRunControl {
	return &agentsv1alpha1.AgentRunControl{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			UID:             types.UID("uid-" + name),
			ResourceVersion: "1",
			Labels:          labels,
		},
		Spec: agentsv1alpha1.AgentRunControlSpec{
			ApplicationRef: agentsv1alpha1.ApplicationReferenceSpec{Name: application},
			LaunchPolicy:   agentsv1alpha1.AgentRunControlLaunchPolicy(policy),
			Reason:         "test control",
		},
	}
}

func TestControlsListFiltersByAuthorizedApplication(t *testing.T) {
	server := controlsTestServer(t, true, false, []string{"hazy-trade"},
		controlObject("hazy-trade", "hazy-trade", "Paused", nil),
		controlObject("other-app", "other-app", "Allowed", nil),
	)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/controls", nil)
	request.Header.Set("Authorization", "Bearer valid")
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("list status = %d %s", response.Code, response.Body.String())
	}
	var list ControlListResponse
	if err := json.Unmarshal(response.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("items = %d, want only hazy-trade control", len(list.Items))
	}
	if list.Items[0].Name != "hazy-trade" {
		t.Fatalf("item = %q", list.Items[0].Name)
	}
	if list.Items[0].LaunchPolicy != "Paused" {
		t.Fatalf("launchPolicy = %q", list.Items[0].LaunchPolicy)
	}
	if !list.Items[0].Management.Writable {
		t.Fatal("operator control should be writable")
	}
}

func TestControlsGetNotFoundForUnauthorizedApplication(t *testing.T) {
	server := controlsTestServer(t, true, false, []string{"hazy-trade"},
		controlObject("other-app", "other-app", "Allowed", nil),
	)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/controls/other-app", nil)
	request.Header.Set("Authorization", "Bearer valid")
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", response.Code)
	}
}

func TestControlsPauseSetsPolicyReasonAndExpiry(t *testing.T) {
	server := controlsTestServer(t, true, true, []string{"hazy-trade"},
		controlObject("hazy-trade", "hazy-trade", "Allowed", nil),
	)
	body := ControlWriteRequest{
		LaunchPolicy:    "Paused",
		Reason:          "maintainer requested a bounded review window",
		ExpiresAt:       time.Now().Add(4 * time.Hour).UTC().Format(time.RFC3339),
		SourceName:      "event-42",
		SourceURL:       "https://github.com/HazyForge/hazy-trade/pull/123",
		ResourceVersion: "1",
	}
	raw, _ := json.Marshal(body)
	request := httptest.NewRequest(http.MethodPut, "/api/v1/controls/hazy-trade", bytes.NewReader(raw))
	request.Header.Set("Authorization", "Bearer valid")
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("pause status = %d %s", response.Code, response.Body.String())
	}
	var view ControlView
	if err := json.Unmarshal(response.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.LaunchPolicy != "Paused" {
		t.Fatalf("launchPolicy = %q", view.LaunchPolicy)
	}
	if view.ExpiresAt == nil {
		t.Fatal("expiresAt not set")
	}
	if view.Source == nil || view.Source.Kind != "PullRequest" || view.Source.Name != "event-42" {
		t.Fatalf("source = %#v", view.Source)
	}
}

func TestControlsResumeClearsExpiry(t *testing.T) {
	expiry := metav1.NewTime(time.Now().Add(time.Hour))
	control := controlObject("hazy-trade", "hazy-trade", "Paused", nil)
	control.Spec.ExpiresAt = &expiry
	server := controlsTestServer(t, true, true, []string{"hazy-trade"}, control)
	body := ControlWriteRequest{
		LaunchPolicy:    "Allowed",
		Reason:          "maintainer approved launches after review",
		ResourceVersion: "1",
	}
	raw, _ := json.Marshal(body)
	request := httptest.NewRequest(http.MethodPut, "/api/v1/controls/hazy-trade", bytes.NewReader(raw))
	request.Header.Set("Authorization", "Bearer valid")
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("resume status = %d %s", response.Code, response.Body.String())
	}
	var view ControlView
	if err := json.Unmarshal(response.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.LaunchPolicy != "Allowed" {
		t.Fatalf("launchPolicy = %q", view.LaunchPolicy)
	}
	if view.ExpiresAt != nil {
		t.Fatalf("expiresAt = %v, want cleared", view.ExpiresAt)
	}
}

func TestControlsWriteRequiresReason(t *testing.T) {
	server := controlsTestServer(t, true, true, []string{"hazy-trade"},
		controlObject("hazy-trade", "hazy-trade", "Allowed", nil),
	)
	body := ControlWriteRequest{LaunchPolicy: "Paused", ResourceVersion: "1"}
	raw, _ := json.Marshal(body)
	request := httptest.NewRequest(http.MethodPut, "/api/v1/controls/hazy-trade", bytes.NewReader(raw))
	request.Header.Set("Authorization", "Bearer valid")
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.Code)
	}
}

func TestControlsWriteRejectsGitOpsOwned(t *testing.T) {
	server := controlsTestServer(t, true, true, []string{"anvil-primaris-agent-manager"},
		controlObject("anvil-primaris-agent-manager-concurrency", "anvil-primaris-agent-manager", "Allowed", map[string]string{
			"argocd.argoproj.io/instance": "anvilhub",
		}),
	)
	body := ControlWriteRequest{LaunchPolicy: "Paused", Reason: "should fail", ResourceVersion: "1"}
	raw, _ := json.Marshal(body)
	request := httptest.NewRequest(http.MethodPut, "/api/v1/controls/anvil-primaris-agent-manager-concurrency", bytes.NewReader(raw))
	request.Header.Set("Authorization", "Bearer valid")
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 gitops protected", response.Code)
	}
	if !strings.Contains(response.Body.String(), "gitops_protected") {
		t.Fatalf("body = %s", response.Body.String())
	}
}

func TestControlsWriteConflictOnStaleResourceVersion(t *testing.T) {
	server := controlsTestServer(t, true, true, []string{"hazy-trade"},
		controlObject("hazy-trade", "hazy-trade", "Allowed", nil),
	)
	body := ControlWriteRequest{LaunchPolicy: "Paused", Reason: "stale", ResourceVersion: "999"}
	raw, _ := json.Marshal(body)
	request := httptest.NewRequest(http.MethodPut, "/api/v1/controls/hazy-trade", bytes.NewReader(raw))
	request.Header.Set("Authorization", "Bearer valid")
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", response.Code)
	}
}

func TestControlsWriteDisabledReturns404(t *testing.T) {
	server := controlsTestServer(t, true, false, []string{"hazy-trade"},
		controlObject("hazy-trade", "hazy-trade", "Allowed", nil),
	)
	body := ControlWriteRequest{LaunchPolicy: "Paused", Reason: "x", ResourceVersion: "1"}
	raw, _ := json.Marshal(body)
	request := httptest.NewRequest(http.MethodPut, "/api/v1/controls/hazy-trade", bytes.NewReader(raw))
	request.Header.Set("Authorization", "Bearer valid")
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 write disabled", response.Code)
	}
}

func TestControlsReadDisabledReturns404(t *testing.T) {
	server := controlsTestServer(t, false, false, []string{"hazy-trade"},
		controlObject("hazy-trade", "hazy-trade", "Allowed", nil),
	)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/controls", nil)
	request.Header.Set("Authorization", "Bearer valid")
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 read disabled", response.Code)
	}
}
