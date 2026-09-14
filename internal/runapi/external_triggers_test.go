package runapi

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	agentsv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

func TestVerifyGitHubSignature256(t *testing.T) {
	t.Parallel()
	secret := "test-webhook-secret"
	body := []byte(`{"repository":{"full_name":"HazyForge/anvil-agents"}}`)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	good := githubSignaturePrefix + hex.EncodeToString(mac.Sum(nil))

	if err := verifyGitHubSignature256(secret, good, body); err != nil {
		t.Fatalf("good signature rejected: %v", err)
	}
	if err := verifyGitHubSignature256(secret, githubSignaturePrefix+strings.Repeat("0", 64), body); err == nil {
		t.Fatal("bad signature accepted")
	}
	if err := verifyGitHubSignature256(secret, "", body); err == nil {
		t.Fatal("missing signature accepted")
	}
	if err := verifyGitHubSignature256("", good, body); err == nil {
		t.Fatal("empty secret accepted")
	}
}

func TestExternalTriggerWebhook(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	if err := agentsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	secretValue := "super-secret"
	trigger := &agentsv1alpha1.AgentExternalTrigger{
		ObjectMeta: metav1.ObjectMeta{Name: "github-push", Namespace: "agents", UID: "trigger-uid", Generation: 3},
		Spec: agentsv1alpha1.AgentExternalTriggerSpec{
			Source: agentsv1alpha1.AgentExternalTriggerSourceSpec{
				Kind: agentsv1alpha1.AgentExternalTriggerSourceGitHubWebhook,
				GitHub: &agentsv1alpha1.AgentExternalTriggerGitHubSpec{
					Repositories: []string{"HazyForge/anvil-agents"},
					Events:       []string{"push"},
				},
			},
			SecretRef: agentsv1alpha1.AgentExternalTriggerSecretRef{Name: "webhook-secret"},
			Targets: []agentsv1alpha1.AgentExternalTriggerTargetSpec{{
				Kind: agentsv1alpha1.AgentExternalTriggerTargetAgentRunProfile,
				Name: "operator",
			}},
			ConcurrencyPolicy: agentsv1alpha1.AgentExternalTriggerConcurrencyAllow,
		},
		Status: agentsv1alpha1.AgentExternalTriggerStatus{
			Phase:       agentsv1alpha1.AgentExternalTriggerPhaseReady,
			ReceiverID:  "abcd1234abcd1234abcd1234abcd1234",
			WebhookPath: "/api/v1/external-triggers/agents/github-push/abcd1234abcd1234abcd1234abcd1234",
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "webhook-secret", Namespace: "agents"},
		Data:       map[string][]byte{agentsv1alpha1.DefaultWebhookSecretKey: []byte(secretValue)},
	}
	profile := &agentsv1alpha1.AgentRunProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "operator", Namespace: "agents"},
	}

	kube := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&agentsv1alpha1.AgentExternalTrigger{}).WithObjects(trigger, secret, profile).Build()
	server := newExternalTriggerTestServer(t, kube, true)

	body := []byte(`{"repository":{"full_name":"HazyForge/anvil-agents"},"ref":"refs/heads/master","sender":{"login":"octocat"}}`)
	deliveryID := "delivery-1"

	t.Run("disabled returns 404", func(t *testing.T) {
		disabled := newExternalTriggerTestServer(t, kube, false)
		req := newSignedWebhookRequest(t, trigger, body, deliveryID, "push", secretValue)
		rr := httptest.NewRecorder()
		disabled.routes().ServeHTTP(rr, req)
		if rr.Code != http.StatusNotFound {
			t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
		}
	})

	t.Run("bad signature", func(t *testing.T) {
		req := newSignedWebhookRequest(t, trigger, body, deliveryID, "push", "wrong-secret")
		rr := httptest.NewRecorder()
		server.routes().ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
		}
	})

	t.Run("unknown receiver", func(t *testing.T) {
		req := newSignedWebhookRequest(t, trigger, body, deliveryID, "push", secretValue)
		req.SetPathValue("receiverID", "nope")
		// PathValue on ServeMux comes from pattern; rebuild URL path.
		req = httptest.NewRequest(http.MethodPost, "/api/v1/external-triggers/agents/github-push/nope", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-GitHub-Event", "push")
		req.Header.Set("X-GitHub-Delivery", deliveryID)
		req.Header.Set("X-Hub-Signature-256", signGitHubBody(secretValue, body))
		rr := httptest.NewRecorder()
		server.routes().ServeHTTP(rr, req)
		if rr.Code != http.StatusNotFound {
			t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
		}
	})

	t.Run("repo filter", func(t *testing.T) {
		other := []byte(`{"repository":{"full_name":"Other/repo"}}`)
		req := newSignedWebhookRequest(t, trigger, other, "delivery-repo", "push", secretValue)
		rr := httptest.NewRecorder()
		server.routes().ServeHTTP(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
		}
	})

	t.Run("event filter", func(t *testing.T) {
		req := newSignedWebhookRequest(t, trigger, body, "delivery-event", "issues", secretValue)
		rr := httptest.NewRecorder()
		server.routes().ServeHTTP(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
		}
	})

	t.Run("suspend", func(t *testing.T) {
		suspended := trigger.DeepCopy()
		suspended.Spec.Suspend = true
		suspended.Status.Phase = agentsv1alpha1.AgentExternalTriggerPhaseSuspended
		if err := kube.Update(context.Background(), suspended); err != nil {
			t.Fatal(err)
		}
		req := newSignedWebhookRequest(t, suspended, body, "delivery-suspend", "push", secretValue)
		rr := httptest.NewRecorder()
		server.routes().ServeHTTP(rr, req)
		if rr.Code != http.StatusConflict {
			t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
		}
		suspended.Spec.Suspend = false
		suspended.Status.Phase = agentsv1alpha1.AgentExternalTriggerPhaseReady
		if err := kube.Update(context.Background(), suspended); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("accept and idempotent replay", func(t *testing.T) {
		req := newSignedWebhookRequest(t, trigger, body, "delivery-ok", "push", secretValue)
		rr := httptest.NewRecorder()
		server.routes().ServeHTTP(rr, req)
		if rr.Code != http.StatusAccepted {
			t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
		}
		runs := &agentsv1alpha1.AgentRunList{}
		if err := kube.List(context.Background(), runs, client.InNamespace("agents")); err != nil {
			t.Fatal(err)
		}
		if len(runs.Items) != 1 {
			t.Fatalf("expected 1 run, got %d", len(runs.Items))
		}
		run := runs.Items[0]
		if run.Spec.SourceRef.Kind != "AgentExternalTrigger" || run.Spec.Purpose != agentsv1alpha1.AgentRunPurposeManual {
			t.Fatalf("unexpected run source/purpose: %+v", run.Spec)
		}
		if run.Spec.ProfileRef == nil || run.Spec.ProfileRef.Name != "operator" {
			t.Fatalf("profileRef = %+v", run.Spec.ProfileRef)
		}

		fresh := &agentsv1alpha1.AgentExternalTrigger{}
		if err := kube.Get(context.Background(), client.ObjectKey{Namespace: "agents", Name: "github-push"}, fresh); err != nil {
			t.Fatal(err)
		}
		if fresh.Status.LastDeliveryID != "delivery-ok" || fresh.Status.DeliveryCount != 1 {
			t.Fatalf("status not updated: %+v", fresh.Status)
		}

		replay := newSignedWebhookRequest(t, trigger, body, "delivery-ok", "push", secretValue)
		rr2 := httptest.NewRecorder()
		server.routes().ServeHTTP(rr2, replay)
		if rr2.Code != http.StatusOK {
			t.Fatalf("replay status = %d body=%s", rr2.Code, rr2.Body.String())
		}
		var payload map[string]any
		if err := json.Unmarshal(rr2.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		if payload["duplicate"] != true {
			t.Fatalf("expected duplicate=true, got %#v", payload)
		}
		runs2 := &agentsv1alpha1.AgentRunList{}
		if err := kube.List(context.Background(), runs2, client.InNamespace("agents")); err != nil {
			t.Fatal(err)
		}
		if len(runs2.Items) != 1 {
			t.Fatalf("replay created extra runs: %d", len(runs2.Items))
		}
	})
}

func TestExternalTriggerUIConfig(t *testing.T) {
	t.Parallel()
	scheme := runtime.NewScheme()
	_ = agentsv1alpha1.AddToScheme(scheme)
	kube := fake.NewClientBuilder().WithScheme(scheme).Build()
	server := newExternalTriggerTestServer(t, kube, true)
	req := httptest.NewRequest(http.MethodGet, "/ui-config.json", nil)
	rr := httptest.NewRecorder()
	server.routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"externalTriggers"`) || !strings.Contains(rr.Body.String(), `"enabled":true`) {
		t.Fatalf("ui-config missing externalTriggers: %s", rr.Body.String())
	}
}

func TestGitHubRepositoryAndEventFilters(t *testing.T) {
	t.Parallel()
	github := &agentsv1alpha1.AgentExternalTriggerGitHubSpec{
		Repositories: []string{"HazyForge/anvil-agents"},
		Events:       []string{"push", "pull_request"},
	}
	if !githubRepositoryAllowed(github, "hazyforge/anvil-agents") {
		t.Fatal("expected case-insensitive repo allow")
	}
	if githubRepositoryAllowed(github, "other/repo") {
		t.Fatal("unexpected repo allow")
	}
	if !githubEventAllowed(github, "PUSH") {
		t.Fatal("expected event allow")
	}
	if githubEventAllowed(github, "issues") {
		t.Fatal("unexpected event allow")
	}
	if !githubEventAllowed(&agentsv1alpha1.AgentExternalTriggerGitHubSpec{Repositories: []string{"a/b"}}, "anything") {
		t.Fatal("empty events should allow all")
	}
}

func newExternalTriggerTestServer(t *testing.T, kube client.Client, enabled bool) *Server {
	t.Helper()
	config := DefaultConfig()
	config.OIDC.Issuer = "https://issuer.example"
	config.OIDC.Audiences = []string{"anvil-agents-api"}
	config.OIDC.AllowInsecureIssuer = true
	config.Authorization.Bindings = []AuthorizationBinding{{
		Name:        "test",
		Roles:       []string{"viewer"},
		Permissions: []string{PermissionRunsRead, PermissionRunsStream},
		Namespaces:  []string{"agents"},
	}}
	config.ExternalTriggers.Enabled = enabled
	server, err := NewServer(config, staticAuthenticator{ready: true}, kube, staticLogSource{}, logr.Discard())
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func newSignedWebhookRequest(t *testing.T, trigger *agentsv1alpha1.AgentExternalTrigger, body []byte, deliveryID, eventType, secret string) *http.Request {
	t.Helper()
	path := fmt.Sprintf("/api/v1/external-triggers/%s/%s/%s", trigger.Namespace, trigger.Name, trigger.Status.ReceiverID)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Event", eventType)
	req.Header.Set("X-GitHub-Delivery", deliveryID)
	req.Header.Set("X-Hub-Signature-256", signGitHubBody(secret, body))
	return req
}

func signGitHubBody(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return githubSignaturePrefix + hex.EncodeToString(mac.Sum(nil))
}