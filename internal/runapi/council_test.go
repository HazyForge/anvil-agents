package runapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	agentsv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
	"github.com/hazyforge/anvil-agents/internal/chat"
)

func TestAnvilCouncilConnectUsesLLMHarness(t *testing.T) {
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
	if len(connected.Knowledge) < 1 || len(connected.Memory) < 1 || len(connected.Members) != 3 {
		t.Fatalf("state = %#v", connected)
	}
	profile := &agentsv1alpha1.AgentRunProfile{}
	if err := server.runs.Get(t.Context(), types.NamespacedName{Namespace: "agents", Name: anvilAgentProfileName}, profile); err != nil {
		t.Fatal(err)
	}
	if profile.Spec.HarnessProfileRef == nil || profile.Spec.HarnessProfileRef.Name != councilLLMHarness {
		t.Fatalf("anvil-agent harness = %#v", profile.Spec.HarnessProfileRef)
	}
}

func TestAnvilCouncilTurnFollowsHarnessDecisionNotCannedScript(t *testing.T) {
	server := councilTestServer(t)
	connectCouncil(t, server)

	overlap := postCouncilTurn(t, server, anvilAgentProfileName, "Both of you start by inventorying the same namespace, then implement in parallel.")
	if overlap.To != anvilAgentProfileName {
		t.Fatalf("to = %q", overlap.To)
	}
	if overlap.User.AddressedTo != anvilAgentProfileName {
		t.Fatalf("user addressedTo = %#v", overlap.User)
	}
	profiles := map[string]string{}
	for _, line := range overlap.Confer {
		if line.AuthorProfile == "" {
			t.Fatalf("line without author: %#v", line)
		}
		profiles[line.AuthorProfile] = line.Content
	}
	if profiles[anvilAgentProfileName] == "" || profiles[councilResearcherProfile] == "" || profiles[councilImplementerProfile] == "" {
		t.Fatalf("overlap confer = %#v", overlap.Confer)
	}
	if overlap.Interrupt == nil || overlap.Interrupt.AuthorProfile != councilImplementerProfile {
		t.Fatalf("interrupt = %#v", overlap.Interrupt)
	}
	if len(overlap.DelegatedRuns) < 3 {
		t.Fatalf("expected conversation + two work runs, got %#v", overlap.DelegatedRuns)
	}
	workHarnesses := map[string]string{}
	for _, run := range overlap.DelegatedRuns {
		if run.Kind == "delegate" {
			workHarnesses[run.Role] = run.HarnessProfileName
		}
	}
	if workHarnesses["researcher"] == workHarnesses["implementer"] || workHarnesses["researcher"] == "" {
		t.Fatalf("expected mixed work harnesses, got %#v", overlap.DelegatedRuns)
	}

	answer := postCouncilTurn(t, server, anvilAgentProfileName, "Only answer: who is in the room? Do not delegate.")
	if !strings.Contains(answer.Confer[0].Content, "room") && !strings.Contains(strings.ToLower(answer.Confer[0].Content), "anvil") {
		t.Fatalf("solo anvil = %#v", answer.Confer)
	}
	if len(answer.Confer) != 1 || answer.Confer[0].AuthorProfile != anvilAgentProfileName {
		t.Fatalf("solo confer should be Anvil only: %#v", answer.Confer)
	}
	if answer.Confer[0].Content == overlap.Confer[0].Content {
		t.Fatal("different user messages produced the same Anvil line; API is still scripted")
	}
	delegateCount := 0
	for _, run := range answer.DelegatedRuns {
		if run.Kind == "delegate" {
			delegateCount++
		}
	}
	if delegateCount != 0 {
		t.Fatalf("solo answer should not delegate work: %#v", answer.DelegatedRuns)
	}
}

func TestAnvilCouncilMemberTurnCanMessageAnvilAndPeers(t *testing.T) {
	server := councilTestServer(t)
	connectCouncil(t, server)

	turned := postCouncilTurn(t, server, councilResearcherProfile, "Researcher, tell Anvil you have the inventory claim and warn Implementer to wait.")
	if turned.To != councilResearcherProfile || turned.User.AddressedTo != councilResearcherProfile {
		t.Fatalf("member turn addressing = %#v", turned)
	}
	if len(turned.Confer) < 2 {
		t.Fatalf("expected member reply plus peer messages, got %#v", turned.Confer)
	}
	var toUser, toAnvil, toImplementer bool
	for _, line := range turned.Confer {
		if line.AuthorProfile != councilResearcherProfile {
			t.Fatalf("member turn leaked another voice: %#v", line)
		}
		switch line.AddressedTo {
		case "user":
			toUser = true
		case anvilAgentProfileName:
			toAnvil = true
		case councilImplementerProfile:
			toImplementer = true
		}
	}
	if !toUser || !toAnvil || !toImplementer {
		t.Fatalf("member did not address user/Anvil/peer: %#v", turned.Confer)
	}
	for _, run := range turned.DelegatedRuns {
		if run.Kind == "delegate" {
			t.Fatalf("member conversation should not create work delegates: %#v", turned.DelegatedRuns)
		}
	}
	claimed := map[string]string{}
	for _, entry := range turned.Memory {
		claimed[entry.Key] = entry.Value
	}
	if claimed["last-addressee"] != councilResearcherProfile {
		t.Fatalf("memory = %#v", turned.Memory)
	}
}

func TestAnvilCouncilHarnessFailureIsNotCanned(t *testing.T) {
	server := councilTestServer(t)
	server.councilHarnessWaiter = func(context.Context, string, string) (string, error) {
		return "", fmt.Errorf("llm unreachable")
	}
	connectCouncil(t, server)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/namespaces/agents/anvil-council/turns", bytes.NewBufferString(`{"content":"Both of you inventory now."}`))
	request.Header.Set("Authorization", "Bearer valid")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "llm unreachable") {
		t.Fatalf("error = %s", response.Body.String())
	}
	state, err := server.loadCouncilState(t.Context(), "agents", "tester", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Messages) < 1 || state.Messages[0].Role != chat.RoleUser {
		t.Fatalf("user message should persist: %#v", state.Messages)
	}
	for _, message := range state.Messages {
		if message.AuthorProfile == councilResearcherProfile || strings.Contains(message.Content, "take the inventory") {
			t.Fatalf("canned conferral leaked after harness failure: %#v", state.Messages)
		}
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

func connectCouncil(t *testing.T, server *Server) {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/namespaces/agents/anvil-council", bytes.NewBufferString(`{}`))
	request.Header.Set("Authorization", "Bearer valid")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("connect status = %d %s", response.Code, response.Body.String())
	}
}

func postCouncilTurn(t *testing.T, server *Server, to, content string) CouncilTurnResponse {
	t.Helper()
	body, err := json.Marshal(CouncilTurnRequest{Content: content, To: to})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/namespaces/agents/anvil-council/turns", bytes.NewBuffer(body))
	request.Header.Set("Authorization", "Bearer valid")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("turn status = %d %s", response.Code, response.Body.String())
	}
	var turned CouncilTurnResponse
	if err := json.Unmarshal(response.Body.Bytes(), &turned); err != nil {
		t.Fatal(err)
	}
	return turned
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
	server.councilHarnessWaiter = func(ctx context.Context, namespace, name string) (string, error) {
		run := &agentsv1alpha1.AgentRun{}
		if err := server.runs.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, run); err != nil {
			return "", err
		}
		return fakeCouncilHarnessDecision(run.Spec.Prompt), nil
	}
	return server
}

func fakeCouncilHarnessDecision(prompt string) string {
	addressee := anvilAgentProfileName
	if _, rest, ok := strings.Cut(prompt, "ADDRESSEE: "); ok {
		addressee = canonicalCouncilProfile(strings.TrimSpace(strings.Split(rest, "\n")[0]))
	}
	user := prompt
	if _, rest, ok := strings.Cut(prompt, "USER_MESSAGE:"); ok {
		user = strings.TrimSpace(strings.Trim(rest, "\n<> "))
	}
	var decision councilHarnessDecision
	switch addressee {
	case councilResearcherProfile:
		decision = councilHarnessDecision{
			Mode:    "member",
			Speaker: councilResearcherProfile,
			Reply:   "I have the inventory claim and will post the map.",
			Messages: []councilHarnessPeerMsg{
				{To: anvilAgentProfileName, Content: "Anvil — researcher speaking. I claimed inventory."},
				{To: councilImplementerProfile, Content: "Implementer, wait on my map. Do not copy the inventory."},
			},
		}
	case councilImplementerProfile:
		decision = councilHarnessDecision{
			Mode:    "member",
			Speaker: councilImplementerProfile,
			Reply:   "I will wait for the map, then implement.",
			Messages: []councilHarnessPeerMsg{
				{To: anvilAgentProfileName, Content: "Anvil — implementer here. I will not duplicate inventory."},
			},
		}
	default:
		lower := strings.ToLower(user)
		if strings.Contains(lower, "only answer") || strings.Contains(lower, "who is in the room") {
			decision = councilHarnessDecision{
				Mode:  "controller",
				Anvil: "The room is Anvil agent, Council Researcher, and Council Implementer. I am not delegating.",
			}
			break
		}
		decision = councilHarnessDecision{
			Mode:  "controller",
			Anvil: "Researcher takes inventory. Implementer waits, then implements on a different harness.",
			Utterances: []councilHarnessUtterance{
				{Profile: councilResearcherProfile, Content: "Taking inventory. Implementer, wait on me.", WaitingOn: councilImplementerProfile},
				{Profile: councilImplementerProfile, Content: "Stopping — I will not inventory the same namespace.", Kind: "interrupt", WaitingOn: councilResearcherProfile},
			},
			Memory: []councilHarnessMemory{
				{Key: "claim:inventory", Value: councilResearcherProfile},
				{Key: "claim:implement", Value: councilImplementerProfile},
			},
			Delegates: []councilHarnessDelegate{
				{Role: "researcher", Profile: councilResearcherProfile, Harness: councilResearchHarness, Intent: "observe", Prompt: "inventory", Claim: "inventory"},
				{Role: "implementer", Profile: councilImplementerProfile, Harness: councilGrokHarness, Intent: "proposeChange", Prompt: "implement", Claim: "implement"},
			},
		}
		if !strings.Contains(lower, "both") && !strings.Contains(lower, "same namespace") {
			decision.Utterances[1].Kind = "utterance"
			decision.Utterances[1].Content = "Waiting on Researcher's map, then I implement."
		}
	}
	raw, err := json.Marshal(decision)
	if err != nil {
		return ""
	}
	return councilDecisionPrefix + string(raw)
}
