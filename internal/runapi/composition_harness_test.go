package runapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	agentsv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

func harnessPatchProfile(name, harness string) *agentsv1alpha1.AgentRunProfile {
	profile := &agentsv1alpha1.AgentRunProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       "agents",
			ResourceVersion: "10",
			Labels:          map[string]string{LabelManagedBy: ManagedByConsole},
		},
		Spec: agentsv1alpha1.AgentRunProfileSpec{
			Description: "standing agent",
			Harness: agentsv1alpha1.AgentRunHarnessSpec{
				Intent:       agentsv1alpha1.AgentRunIntentObserve,
				SystemPrompt: "Stay on scope.",
			},
			SkillSets: &agentsv1alpha1.AgentSkillCompositionSpec{
				Refs: []agentsv1alpha1.NamespacedObjectReference{{Name: "review-skills"}},
			},
			ToolSets: &agentsv1alpha1.AgentToolCompositionSpec{
				Refs: []agentsv1alpha1.NamespacedObjectReference{{Name: "review-tools"}},
			},
		},
	}
	if harness != "" {
		profile.Spec.HarnessProfileRef = &agentsv1alpha1.NamespacedObjectReference{Name: harness}
	}
	return profile
}

func harnessPatchHarness(name string, kind agentsv1alpha1.AgentRunHarnessBackendKind) *agentsv1alpha1.AgentHarnessProfile {
	return &agentsv1alpha1.AgentHarnessProfile{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "agents", ResourceVersion: "1"},
		Spec: agentsv1alpha1.AgentHarnessProfileSpec{
			Backend: agentsv1alpha1.AgentRunHarnessBackendSpec{Kind: kind},
		},
	}
}

func doHarnessPatch(server *Server, profile, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/namespaces/agents/agent-run-profiles/"+profile+"/harness", bytes.NewBufferString(body))
	request.Header.Set("Authorization", "Bearer valid")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	return response
}

func TestRunProfileHarnessPatchSwitchesRefOnly(t *testing.T) {
	server := compositionTestServer(t, true, true,
		harnessPatchProfile("scout", "codex-standard"),
		harnessPatchHarness("codex-standard", agentsv1alpha1.AgentRunHarnessBackendCodex),
		harnessPatchHarness("pi-large", agentsv1alpha1.AgentRunHarnessBackendPiAgent),
	)

	response := doHarnessPatch(server, "scout", `{"harnessProfileName":"pi-large"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("patch status = %d %s", response.Code, response.Body.String())
	}
	var doc CompositionDocument
	if err := json.Unmarshal(response.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	var spec agentsv1alpha1.AgentRunProfileSpec
	raw, _ := json.Marshal(doc.Spec)
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	if spec.HarnessProfileRef == nil || spec.HarnessProfileRef.Name != "pi-large" {
		t.Fatalf("harnessProfileRef = %+v, want pi-large", spec.HarnessProfileRef)
	}
	// Only the ref changes: role, skills, tools, and inline harness stay.
	if spec.Harness.Intent != agentsv1alpha1.AgentRunIntentObserve || spec.Harness.SystemPrompt != "Stay on scope." {
		t.Fatalf("inline harness was rewritten: %+v", spec.Harness)
	}
	if len(spec.SkillSets.Refs) != 1 || spec.SkillSets.Refs[0].Name != "review-skills" {
		t.Fatalf("skillSets changed: %+v", spec.SkillSets)
	}
	if len(spec.ToolSets.Refs) != 1 || spec.ToolSets.Refs[0].Name != "review-tools" {
		t.Fatalf("toolSets changed: %+v", spec.ToolSets)
	}
}

func TestRunProfileHarnessPatchManagerProfile(t *testing.T) {
	server := compositionTestServer(t, true, true,
		harnessPatchProfile("hazy-trade-agent-manager", "codex-standard"),
		harnessPatchHarness("codex-standard", agentsv1alpha1.AgentRunHarnessBackendCodex),
		harnessPatchHarness("grok-build-large", agentsv1alpha1.AgentRunHarnessBackendGrokBuild),
	)

	response := doHarnessPatch(server, "hazy-trade-agent-manager", `{"harnessProfileName":"grok-build-large"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("manager patch status = %d %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "grok-build-large") {
		t.Fatalf("expected new harness ref, got %s", response.Body.String())
	}
}

func TestRunProfileHarnessPatchClearsRef(t *testing.T) {
	server := compositionTestServer(t, true, true, harnessPatchProfile("scout", "codex-standard"))

	response := doHarnessPatch(server, "scout", `{"harnessProfileName":""}`)
	if response.Code != http.StatusOK {
		t.Fatalf("clear status = %d %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "codex-standard") {
		t.Fatalf("expected cleared ref, got %s", response.Body.String())
	}
}

func TestRunProfileHarnessPatchRejectsUnknownHarness(t *testing.T) {
	server := compositionTestServer(t, true, true, harnessPatchProfile("scout", "codex-standard"))

	response := doHarnessPatch(server, "scout", `{"harnessProfileName":"missing-harness"}`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected bad request, got %d %s", response.Code, response.Body.String())
	}
}

func TestRunProfileHarnessPatchRejectsBadBody(t *testing.T) {
	server := compositionTestServer(t, true, true,
		harnessPatchProfile("scout", "codex-standard"),
		harnessPatchHarness("pi-large", agentsv1alpha1.AgentRunHarnessBackendPiAgent),
	)

	for _, body := range []string{`{}`, `{"harnessProfileName":"Bad_Name!"}`, `not-json`} {
		response := doHarnessPatch(server, "scout", body)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("body %q: expected bad request, got %d %s", body, response.Code, response.Body.String())
		}
	}
}

func TestRunProfileHarnessPatchRespectsManagement(t *testing.T) {
	gitops := harnessPatchProfile("gitops-agent", "codex-standard")
	gitops.Labels = map[string]string{"argocd.argoproj.io/instance": "agents"}
	unmanaged := harnessPatchProfile("kubectl-agent", "codex-standard")
	unmanaged.Labels = nil
	server := compositionTestServer(t, true, true,
		gitops, unmanaged,
		harnessPatchHarness("pi-large", agentsv1alpha1.AgentRunHarnessBackendPiAgent),
	)

	response := doHarnessPatch(server, "gitops-agent", `{"harnessProfileName":"pi-large"}`)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "gitops_protected") {
		t.Fatalf("expected gitops_protected, got %d %s", response.Code, response.Body.String())
	}
	response = doHarnessPatch(server, "kubectl-agent", `{"harnessProfileName":"pi-large"}`)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "not_console_managed") {
		t.Fatalf("expected not_console_managed, got %d %s", response.Code, response.Body.String())
	}
}

func TestRunProfileHarnessPatchWriteDisabled(t *testing.T) {
	server := compositionTestServer(t, true, false,
		harnessPatchProfile("scout", "codex-standard"),
		harnessPatchHarness("pi-large", agentsv1alpha1.AgentRunHarnessBackendPiAgent),
	)

	response := doHarnessPatch(server, "scout", `{"harnessProfileName":"pi-large"}`)
	if response.Code != http.StatusNotFound {
		t.Fatalf("expected not found without write grant, got %d %s", response.Code, response.Body.String())
	}
}
