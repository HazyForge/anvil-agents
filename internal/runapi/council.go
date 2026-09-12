package runapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	agentsv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
	"github.com/hazyforge/anvil-agents/internal/chat"
	corev1 "k8s.io/api/core/v1"
)

const (
	anvilCouncilName          = "anvil-council"
	anvilAgentProfileName     = "anvil-agent"
	councilResearcherProfile  = "council-researcher"
	councilImplementerProfile = "council-implementer"
	councilResearchHarness    = "council-research"
	councilGrokHarness        = "council-grok"
	gitOpsResearchHarness     = "demo-runtime"
	councilDemoImage          = "anvil-agents-demo:dev"
	councilRunnerSA           = "agent-runner"
)

type CouncilConnectRequest struct {
	Name string `json:"name,omitempty"`
}

type CouncilTurnRequest struct {
	Content string `json:"content"`
}

type CouncilMemberView struct {
	Role        string `json:"role"`
	ProfileName string `json:"profileName"`
	Harness     string `json:"harness,omitempty"`
	Backend     string `json:"backend,omitempty"`
	Description string `json:"description,omitempty"`
}

type CouncilMessageView struct {
	chat.Message
	AuthorKind    string `json:"authorKind,omitempty"`
	AuthorProfile string `json:"authorProfile,omitempty"`
	AuthorRole    string `json:"authorRole,omitempty"`
	Kind          string `json:"kind,omitempty"`
}

type CouncilDelegatedRun struct {
	Name               string `json:"name"`
	Namespace          string `json:"namespace"`
	ProfileName        string `json:"profileName"`
	HarnessProfileName string `json:"harnessProfileName"`
	Backend            string `json:"backend"`
	Role               string `json:"role"`
	Application        string `json:"application,omitempty"`
}

type CouncilState struct {
	Name            string                 `json:"name"`
	Namespace       string                 `json:"namespace"`
	Controller      string                 `json:"controller"`
	Connected       bool                   `json:"connected"`
	Members         []CouncilMemberView    `json:"members"`
	Thread          chat.Thread            `json:"thread"`
	Messages        []CouncilMessageView   `json:"messages"`
	Knowledge       []chat.KnowledgeEntry  `json:"knowledge"`
	Memory          []chat.MemoryEntry     `json:"memory"`
	DelegatedRuns   []CouncilDelegatedRun  `json:"delegatedRuns,omitempty"`
	Interrupts      []CouncilMessageView   `json:"interrupts,omitempty"`
}

type CouncilTurnResponse struct {
	CouncilState
	User      CouncilMessageView  `json:"user"`
	Confer    []CouncilMessageView `json:"confer"`
	Interrupt *CouncilMessageView `json:"interrupt,omitempty"`
}

func (server *Server) registerCouncilRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/namespaces/{namespace}/anvil-council", server.authenticate(http.HandlerFunc(server.handleGetAnvilCouncil)))
	mux.Handle("POST /api/v1/namespaces/{namespace}/anvil-council", server.authenticate(http.HandlerFunc(server.handleConnectAnvilCouncil)))
	mux.Handle("POST /api/v1/namespaces/{namespace}/anvil-council/turns", server.authenticate(http.HandlerFunc(server.handleAnvilCouncilTurn)))
}

func (server *Server) handleGetAnvilCouncil(writer http.ResponseWriter, request *http.Request) {
	namespace, principal, ok := server.authorizeChat(writer, request, PermissionChatRead)
	if !ok {
		return
	}
	if !server.authorizer.Allowed(principal, PermissionCompositionRead, namespace) {
		writeAPIError(writer, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	state, err := server.loadCouncilState(request.Context(), namespace, principal.Subject, false)
	if err != nil {
		server.writeChatStoreError(writer, err, principal, namespace)
		return
	}
	writeJSON(writer, http.StatusOK, state)
}

func (server *Server) handleConnectAnvilCouncil(writer http.ResponseWriter, request *http.Request) {
	namespace, principal, ok := server.authorizeChat(writer, request, PermissionChatWrite)
	if !ok {
		return
	}
	if !server.authorizer.Allowed(principal, PermissionCompositionWrite, namespace) {
		writeAPIError(writer, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	if !server.config.Composition.WriteEnabled {
		writeAPIError(writer, http.StatusNotFound, "composition_write_disabled", "composition write is disabled")
		return
	}
	if _, ok := server.writerClient(writer); !ok {
		return
	}
	if err := server.ensureAnvilCouncilObjects(request.Context(), namespace); err != nil {
		server.log.Error(err, "ensure Anvil council", "subject", principal.Subject, "namespace", namespace)
		writeAPIError(writer, http.StatusServiceUnavailable, "council_unavailable", "failed to connect Anvil council")
		return
	}
	state, err := server.loadCouncilState(request.Context(), namespace, principal.Subject, true)
	if err != nil {
		server.writeChatStoreError(writer, err, principal, namespace)
		return
	}
	server.log.Info("anvil council connect", "subject", principal.Subject, "namespace", namespace, "thread", state.Thread.ID)
	writeJSON(writer, http.StatusOK, state)
}

func (server *Server) handleAnvilCouncilTurn(writer http.ResponseWriter, request *http.Request) {
	namespace, principal, ok := server.authorizeChat(writer, request, PermissionChatWrite)
	if !ok {
		return
	}
	if !server.config.Runs.CreateEnabled || !server.authorizer.Allowed(principal, PermissionRunsCreate, namespace) {
		writeAPIError(writer, http.StatusNotFound, "runs_create_disabled", "append-only AgentRun create is required for council delegate")
		return
	}
	if !server.authorizer.Allowed(principal, PermissionCompositionWrite, namespace) && !server.authorizer.Allowed(principal, PermissionCompositionRead, namespace) {
		writeAPIError(writer, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	body, err := readJSONBody[CouncilTurnRequest](request, chatMaxBodyBytes)
	if err != nil {
		writeAPIError(writer, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	content := strings.TrimSpace(body.Content)
	if content == "" {
		writeAPIError(writer, http.StatusBadRequest, "invalid", "content is required")
		return
	}
	if _, ok := server.writerClient(writer); !ok {
		return
	}
	if err := server.ensureAnvilCouncilObjects(request.Context(), namespace); err != nil {
		server.log.Error(err, "ensure Anvil council before turn", "subject", principal.Subject, "namespace", namespace)
		writeAPIError(writer, http.StatusServiceUnavailable, "council_unavailable", "failed to connect Anvil council")
		return
	}
	response, err := server.runAnvilCouncilTurn(request.Context(), namespace, principal.Subject, content)
	if err != nil {
		server.log.Error(err, "anvil council turn", "subject", principal.Subject, "namespace", namespace)
		writeAPIError(writer, http.StatusServiceUnavailable, "council_unavailable", err.Error())
		return
	}
	writeJSON(writer, http.StatusCreated, response)
}

func (server *Server) runAnvilCouncilTurn(ctx context.Context, namespace, subject, content string) (CouncilTurnResponse, error) {
	state, err := server.loadCouncilState(ctx, namespace, subject, true)
	if err != nil {
		return CouncilTurnResponse{}, err
	}
	user := councilMessage(chat.RoleUser, content, map[string]any{
		"authorKind":    "human",
		"authorSubject": subject,
		"kind":          "utterance",
		"audience":      "all",
	})
	overlap := councilWorkOverlaps(content)
	anvilLine := fmt.Sprintf(
		"Anvil agent is in charge of council %s. Researcher maps inventory on harness %s; implementer executes on harness %s. Shared memory and the knowledge base stay attached while we are connected.",
		anvilCouncilName, state.memberHarness(councilResearcherProfile), state.memberHarness(councilImplementerProfile),
	)
	if overlap {
		anvilLine += " Both members started the same inventory claim — I am interrupting the duplicate so they do not copy each other."
	} else {
		anvilLine += " Work is split: research versus implement. Confer, then delegate in parallel."
	}
	controller := councilMessage(chat.RoleAssistant, anvilLine, memberMeta(anvilAgentProfileName, "controller", "utterance"))
	researcher := councilMessage(chat.RoleAssistant,
		"I will inventory the Kind namespace and write findings into shared memory. I will not implement.",
		memberMeta(councilResearcherProfile, "researcher", "utterance"))
	implementerContent := "I will implement the council proof on my own harness while the researcher inventories."
	implementerKind := "utterance"
	if overlap {
		implementerContent = "Stopping: Anvil agent interrupted my inventory claim. I am switching to implement-only on my own harness so we do not duplicate that work."
		implementerKind = "interrupt"
	}
	implementer := councilMessage(chat.RoleAssistant, implementerContent, memberMeta(councilImplementerProfile, "implementer", implementerKind))

	stored, _, err := server.chatStore.AppendMessages(ctx, namespace, state.Thread.ID, []chat.Message{user, controller, researcher, implementer})
	if err != nil {
		return CouncilTurnResponse{}, err
	}
	if len(stored) != 4 {
		return CouncilTurnResponse{}, fmt.Errorf("unexpected council append count %d", len(stored))
	}

	if _, err := server.chatStore.UpsertMemory(ctx, chat.MemoryEntry{
		Namespace:   namespace,
		CouncilName: anvilCouncilName,
		Key:         "claim:inventory",
		Value:       councilResearcherProfile,
	}); err != nil {
		return CouncilTurnResponse{}, err
	}
	if _, err := server.chatStore.UpsertMemory(ctx, chat.MemoryEntry{
		Namespace:   namespace,
		CouncilName: anvilCouncilName,
		Key:         "claim:implement",
		Value:       councilImplementerProfile,
	}); err != nil {
		return CouncilTurnResponse{}, err
	}
	if _, err := server.chatStore.UpsertMemory(ctx, chat.MemoryEntry{
		Namespace:   namespace,
		CouncilName: anvilCouncilName,
		Key:         "last-turn",
		Value:       titleFromPrompt(content),
	}); err != nil {
		return CouncilTurnResponse{}, err
	}

	researcherHarness := state.memberHarness(councilResearcherProfile)
	implementerHarness := state.memberHarness(councilImplementerProfile)
	runs, err := server.delegateCouncilRuns(ctx, namespace, content, researcherHarness, implementerHarness)
	if err != nil {
		return CouncilTurnResponse{}, err
	}

	next, err := server.loadCouncilState(ctx, namespace, subject, false)
	if err != nil {
		return CouncilTurnResponse{}, err
	}
	next.DelegatedRuns = runs
	views := make([]CouncilMessageView, 0, len(stored))
	for _, message := range stored {
		views = append(views, viewCouncilMessage(message))
	}
	var interrupt *CouncilMessageView
	if overlap {
		msg := views[3]
		interrupt = &msg
		next.Interrupts = []CouncilMessageView{msg}
	}
	return CouncilTurnResponse{
		CouncilState: next,
		User:         views[0],
		Confer:       views[1:],
		Interrupt:    interrupt,
	}, nil
}

func (server *Server) delegateCouncilRuns(ctx context.Context, namespace, content, researcherHarness, implementerHarness string) ([]CouncilDelegatedRun, error) {
	type job struct {
		role    string
		profile string
		harness string
		backend string
		prompt  string
		intent  string
		app     string
	}
	jobs := []job{
		{
			role:    "researcher",
			profile: councilResearcherProfile,
			harness: researcherHarness,
			backend: "custom",
			prompt:  "Council research (do not implement): inventory namespace " + namespace + " for the Anvil council. Operator said:\n" + content,
			intent:  string(agentsv1alpha1.AgentRunIntentObserve),
			app:     councilApplicationKey(namespace, "researcher"),
		},
		{
			role:    "implementer",
			profile: councilImplementerProfile,
			harness: implementerHarness,
			backend: "grokBuild",
			prompt:  "Council implement (do not redo inventory): execute the mixed-harness proof on your own harness. Operator said:\n" + content,
			intent:  string(agentsv1alpha1.AgentRunIntentProposeChange),
			app:     councilApplicationKey(namespace, "implementer"),
		},
	}
	out := make([]CouncilDelegatedRun, len(jobs))
	errs := make([]error, len(jobs))
	var wg sync.WaitGroup
	for i, item := range jobs {
		wg.Add(1)
		go func(i int, item job) {
			defer wg.Done()
			run, err := buildAgentRunFromCreateRequest(namespace, CreateAgentRunRequest{
				GenerateName:       "council-" + item.role + "-",
				Prompt:             item.prompt,
				ProfileName:        item.profile,
				HarnessProfileName: item.harness,
				Intent:             item.intent,
				Purpose:            string(agentsv1alpha1.AgentRunPurposeManual),
				SourceKind:         "AgentCouncil",
				SourceName:         anvilCouncilName,
			})
			if err != nil {
				errs[i] = err
				return
			}
			run.Spec.Scope.ApplicationRef = &agentsv1alpha1.ApplicationReferenceSpec{Name: item.app}
			run.Spec.CouncilRef = &agentsv1alpha1.NamespacedObjectReference{Name: anvilCouncilName}
			if run.Labels == nil {
				run.Labels = map[string]string{}
			}
			run.Labels["control.anvil.hazyforge.io/council"] = anvilCouncilName
			run.Labels["control.anvil.hazyforge.io/council-role"] = item.role
			if err := server.writes.Create(ctx, run); err != nil {
				errs[i] = err
				return
			}
			view := NewAgentRunView(run, false)
			backend := strings.TrimSpace(view.Backend)
			if backend == "" {
				backend = item.backend
			}
			out[i] = CouncilDelegatedRun{
				Name:               run.Name,
				Namespace:          run.Namespace,
				ProfileName:        item.profile,
				HarnessProfileName: item.harness,
				Backend:            backend,
				Role:               item.role,
				Application:        item.app,
			}
		}(i, item)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (server *Server) loadCouncilState(ctx context.Context, namespace, subject string, connect bool) (CouncilState, error) {
	if connect {
		if err := server.attachCouncilLedger(ctx, namespace); err != nil {
			return CouncilState{}, err
		}
	}
	thread, _, err := server.chatStore.EnsureCouncilThread(ctx, chat.Thread{
		Namespace:   namespace,
		CouncilName: anvilCouncilName,
		Mode:        chat.ModeCouncil,
		Title:       "Anvil council",
		CreatedBy:   firstNonEmpty(subject, "anvil-council"),
		Metadata:    json.RawMessage(`{"canonical":true,"controller":"anvil-agent"}`),
	})
	if err != nil {
		return CouncilState{}, err
	}
	messages, err := server.chatStore.ListMessages(ctx, namespace, thread.ID)
	if err != nil {
		return CouncilState{}, err
	}
	knowledge, err := server.chatStore.ListKnowledge(ctx, namespace, anvilCouncilName)
	if err != nil {
		return CouncilState{}, err
	}
	memory, err := server.chatStore.ListMemory(ctx, namespace, anvilCouncilName)
	if err != nil {
		return CouncilState{}, err
	}
	members := server.listCouncilMembers(ctx, namespace)
	views := make([]CouncilMessageView, 0, len(messages))
	var interrupts []CouncilMessageView
	for _, message := range messages {
		view := viewCouncilMessage(message)
		views = append(views, view)
		if view.Kind == "interrupt" {
			interrupts = append(interrupts, view)
		}
	}
	runs := server.listCouncilDelegatedRuns(ctx, namespace)
	return CouncilState{
		Name:          anvilCouncilName,
		Namespace:     namespace,
		Controller:    anvilAgentProfileName,
		Connected:     true,
		Members:       members,
		Thread:        thread,
		Messages:      views,
		Knowledge:     knowledge,
		Memory:        memory,
		DelegatedRuns: runs,
		Interrupts:    interrupts,
	}, nil
}

func (server *Server) attachCouncilLedger(ctx context.Context, namespace string) error {
	if err := server.chatStore.SeedKnowledge(ctx, []chat.KnowledgeEntry{
		{
			Namespace:   namespace,
			CouncilName: anvilCouncilName,
			Title:       "Anvil council charter",
			Body:        "Anvil agent controls this council. Members share one knowledge base and one memory while connected. They confer as distinct voices. If two claim the same work, Anvil interrupts the duplicate. Parallel delegate uses a distinct AgentRun and harness per member.",
		},
		{
			Namespace:   namespace,
			CouncilName: anvilCouncilName,
			Title:       "Harness mix",
			Body:        "council-researcher uses a custom demo harness. council-implementer uses council-grok (grokBuild). Do not share demo-state across both Jobs.",
		},
	}); err != nil {
		return err
	}
	if existing, err := server.chatStore.ListMemory(ctx, namespace, anvilCouncilName); err != nil {
		return err
	} else if len(existing) == 0 {
		if _, err := server.chatStore.UpsertMemory(ctx, chat.MemoryEntry{
			Namespace:   namespace,
			CouncilName: anvilCouncilName,
			Key:         "connected",
			Value:       "anvil-agent",
		}); err != nil {
			return err
		}
	}
	return nil
}

func (server *Server) listCouncilMembers(ctx context.Context, namespace string) []CouncilMemberView {
	council := &agentsv1alpha1.AgentCouncil{}
	if err := server.runs.Get(ctx, types.NamespacedName{Namespace: namespace, Name: anvilCouncilName}, council); err != nil {
		return defaultCouncilMembers()
	}
	out := make([]CouncilMemberView, 0, len(council.Spec.Members))
	for _, member := range council.Spec.Members {
		view := CouncilMemberView{
			Role:        member.Role,
			ProfileName: member.ProfileRef.Name,
			Description: member.Description,
		}
		profile := &agentsv1alpha1.AgentRunProfile{}
		if err := server.runs.Get(ctx, types.NamespacedName{Namespace: namespace, Name: member.ProfileRef.Name}, profile); err == nil && profile.Spec.HarnessProfileRef != nil {
			view.Harness = profile.Spec.HarnessProfileRef.Name
			harness := &agentsv1alpha1.AgentHarnessProfile{}
			if err := server.runs.Get(ctx, types.NamespacedName{Namespace: namespace, Name: view.Harness}, harness); err == nil {
				view.Backend = string(harness.Spec.Backend.Kind)
			}
		}
		out = append(out, view)
	}
	if len(out) == 0 {
		return defaultCouncilMembers()
	}
	return out
}

func (server *Server) listCouncilDelegatedRuns(ctx context.Context, namespace string) []CouncilDelegatedRun {
	list := &agentsv1alpha1.AgentRunList{}
	if err := server.runs.List(ctx, list, client.InNamespace(namespace), client.MatchingLabels{
		"control.anvil.hazyforge.io/council": anvilCouncilName,
	}); err != nil {
		return nil
	}
	out := make([]CouncilDelegatedRun, 0, len(list.Items))
	for i := range list.Items {
		run := &list.Items[i]
		view := NewAgentRunView(run, false)
		harness := ""
		if run.Spec.HarnessProfileRef != nil {
			harness = run.Spec.HarnessProfileRef.Name
		}
		profile := ""
		if run.Spec.ProfileRef != nil {
			profile = run.Spec.ProfileRef.Name
		}
		backend := strings.TrimSpace(view.Backend)
		if backend == "" && harness == councilGrokHarness {
			backend = "grokBuild"
		}
		if backend == "" {
			backend = "custom"
		}
		out = append(out, CouncilDelegatedRun{
			Name:               run.Name,
			Namespace:          run.Namespace,
			ProfileName:        profile,
			HarnessProfileName: harness,
			Backend:            backend,
			Role:               run.Labels["control.anvil.hazyforge.io/council-role"],
			Application:        view.Application,
		})
	}
	return out
}

func (server *Server) ensureAnvilCouncilObjects(ctx context.Context, namespace string) error {
	if server.writes == nil {
		return fmt.Errorf("composition write client is not configured")
	}
	researcherHarness, err := server.ensureResearchHarness(ctx, namespace)
	if err != nil {
		return err
	}
	if err := server.ensureObject(ctx, server.implementerHarness(namespace)); err != nil {
		return err
	}
	if err := server.ensureObject(ctx, anvilAgentProfile(namespace)); err != nil {
		return err
	}
	if err := server.ensureObject(ctx, councilMemberProfile(namespace, councilResearcherProfile, "researcher", researcherHarness, "chat:"+namespace+"/"+anvilCouncilName+"/researcher", agentsv1alpha1.AgentRunIntentObserve)); err != nil {
		return err
	}
	if err := server.ensureObject(ctx, councilMemberProfile(namespace, councilImplementerProfile, "implementer", councilGrokHarness, "chat:"+namespace+"/"+anvilCouncilName+"/implementer", agentsv1alpha1.AgentRunIntentProposeChange)); err != nil {
		return err
	}
	return server.ensureObject(ctx, anvilCouncilObject(namespace))
}

func (server *Server) ensureResearchHarness(ctx context.Context, namespace string) (string, error) {
	existing := &agentsv1alpha1.AgentHarnessProfile{}
	if err := server.runs.Get(ctx, types.NamespacedName{Namespace: namespace, Name: gitOpsResearchHarness}, existing); err == nil {
		return gitOpsResearchHarness, nil
	} else if !apierrors.IsNotFound(err) {
		return "", err
	}
	if err := server.ensureObject(ctx, researchHarness(namespace)); err != nil {
		return "", err
	}
	return councilResearchHarness, nil
}

func (server *Server) ensureObject(ctx context.Context, obj client.Object) error {
	stampConsoleManaged(obj)
	current := obj.DeepCopyObject().(client.Object)
	if err := server.runs.Get(ctx, types.NamespacedName{Namespace: obj.GetNamespace(), Name: obj.GetName()}, current); err != nil {
		if apierrors.IsNotFound(err) {
			if err := server.writes.Create(ctx, obj); err != nil && !apierrors.IsAlreadyExists(err) {
				return err
			}
			return nil
		}
		return err
	}
	if evaluateCompositionManagement(current).Reason == managementReasonGitOpsProtected {
		return nil
	}
	return nil
}

func (state CouncilState) memberHarness(profile string) string {
	for _, member := range state.Members {
		if member.ProfileName == profile && member.Harness != "" {
			return member.Harness
		}
	}
	if profile == councilImplementerProfile {
		return councilGrokHarness
	}
	return gitOpsResearchHarness
}

func defaultCouncilMembers() []CouncilMemberView {
	return []CouncilMemberView{
		{Role: "controller", ProfileName: anvilAgentProfileName, Description: "Main chat agent in charge of the council"},
		{Role: "researcher", ProfileName: councilResearcherProfile, Harness: gitOpsResearchHarness, Backend: "custom"},
		{Role: "implementer", ProfileName: councilImplementerProfile, Harness: councilGrokHarness, Backend: "grokBuild"},
	}
}

func anvilAgentProfile(namespace string) *agentsv1alpha1.AgentRunProfile {
	profile := &agentsv1alpha1.AgentRunProfile{
		TypeMeta: metav1.TypeMeta{APIVersion: agentsv1alpha1.GroupVersion.String(), Kind: "AgentRunProfile"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      anvilAgentProfileName,
			Namespace: namespace,
		},
		Spec: agentsv1alpha1.AgentRunProfileSpec{
			Description: "Anvil agent — main chat agent that controls the Anvil council.",
			Scope: agentsv1alpha1.AgentRunScopeSpec{
				Summary:        "Coordinate the Anvil council, shared memory, and parallel mixed-harness delegate.",
				ApplicationRef: &agentsv1alpha1.ApplicationReferenceSpec{Name: councilApplicationKey(namespace, "controller")},
			},
			CouncilRef: &agentsv1alpha1.NamespacedObjectReference{Name: anvilCouncilName},
		},
	}
	return profile
}

func councilMemberProfile(namespace, name, role, harness, application string, intent agentsv1alpha1.AgentRunIntent) *agentsv1alpha1.AgentRunProfile {
	return &agentsv1alpha1.AgentRunProfile{
		TypeMeta: metav1.TypeMeta{APIVersion: agentsv1alpha1.GroupVersion.String(), Kind: "AgentRunProfile"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: agentsv1alpha1.AgentRunProfileSpec{
			Description: "Anvil council " + role,
			Scope: agentsv1alpha1.AgentRunScopeSpec{
				ApplicationRef: &agentsv1alpha1.ApplicationReferenceSpec{Name: application},
			},
			Harness:           agentsv1alpha1.AgentRunHarnessSpec{Intent: intent},
			HarnessProfileRef: &agentsv1alpha1.NamespacedObjectReference{Name: harness},
			CouncilRef:        &agentsv1alpha1.NamespacedObjectReference{Name: anvilCouncilName},
		},
	}
}

func researchHarness(namespace string) *agentsv1alpha1.AgentHarnessProfile {
	return &agentsv1alpha1.AgentHarnessProfile{
		TypeMeta: metav1.TypeMeta{APIVersion: agentsv1alpha1.GroupVersion.String(), Kind: "AgentHarnessProfile"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      councilResearchHarness,
			Namespace: namespace,
		},
		Spec: agentsv1alpha1.AgentHarnessProfileSpec{
			Description: "Council researcher custom harness (no shared PVC).",
			Backend: agentsv1alpha1.AgentRunHarnessBackendSpec{
				Kind:            agentsv1alpha1.AgentRunHarnessBackendCustom,
				Image:           councilDemoImage,
				ImagePullPolicy: corev1.PullIfNotPresent,
			},
			Execution: agentsv1alpha1.AgentRunHarnessExecutionSpec{
				ServiceAccountName: councilRunnerSA,
				TimeoutSeconds:     120,
			},
		},
	}
}

func (server *Server) implementerHarness(namespace string) *agentsv1alpha1.AgentHarnessProfile {
	return &agentsv1alpha1.AgentHarnessProfile{
		TypeMeta: metav1.TypeMeta{APIVersion: agentsv1alpha1.GroupVersion.String(), Kind: "AgentHarnessProfile"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      councilGrokHarness,
			Namespace: namespace,
		},
		Spec: agentsv1alpha1.AgentHarnessProfileSpec{
			Description: "Council implementer grokBuild harness (demo image on Kind, no demo-state PVC).",
			Backend: agentsv1alpha1.AgentRunHarnessBackendSpec{
				Kind:            agentsv1alpha1.AgentRunHarnessBackendGrokBuild,
				Image:           councilDemoImage,
				ImagePullPolicy: corev1.PullIfNotPresent,
			},
			Execution: agentsv1alpha1.AgentRunHarnessExecutionSpec{
				ServiceAccountName: councilRunnerSA,
				TimeoutSeconds:     120,
			},
		},
	}
}

func anvilCouncilObject(namespace string) *agentsv1alpha1.AgentCouncil {
	return &agentsv1alpha1.AgentCouncil{
		TypeMeta: metav1.TypeMeta{APIVersion: agentsv1alpha1.GroupVersion.String(), Kind: "AgentCouncil"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      anvilCouncilName,
			Namespace: namespace,
		},
		Spec: agentsv1alpha1.AgentCouncilSpec{
			Description: "Anvil agent council: shared knowledge and memory, confer, interrupt duplicates, parallel mixed-harness runs.",
			CouncilPrompt: "Anvil agent is in charge. Confer as distinct members. Interrupt if two claim the same work. Delegate in parallel on each member's own harness. Use the attached knowledge base and unified memory while connected.",
			Members: []agentsv1alpha1.AgentCouncilMemberSpec{
				{Role: "controller", ProfileRef: agentsv1alpha1.NamespacedObjectReference{Name: anvilAgentProfileName}, Description: "Anvil agent"},
				{Role: "researcher", ProfileRef: agentsv1alpha1.NamespacedObjectReference{Name: councilResearcherProfile}, Description: "Inventory on a custom harness"},
				{Role: "implementer", ProfileRef: agentsv1alpha1.NamespacedObjectReference{Name: councilImplementerProfile}, Description: "Implement on grokBuild"},
			},
		},
	}
}

func councilMessage(role, content string, metadata map[string]any) chat.Message {
	raw, err := json.Marshal(metadata)
	if err != nil {
		raw = []byte(`{}`)
	}
	return chat.Message{Role: role, Content: content, Metadata: raw}
}

func memberMeta(profile, role, kind string) map[string]any {
	return map[string]any{
		"authorKind":    "member",
		"authorProfile": profile,
		"authorRole":    role,
		"kind":          kind,
		"audience":      "all",
	}
}

func viewCouncilMessage(message chat.Message) CouncilMessageView {
	view := CouncilMessageView{Message: message, Kind: "utterance"}
	var meta map[string]any
	if err := json.Unmarshal(message.Metadata, &meta); err != nil {
		return view
	}
	view.AuthorKind, _ = meta["authorKind"].(string)
	view.AuthorProfile, _ = meta["authorProfile"].(string)
	view.AuthorRole, _ = meta["authorRole"].(string)
	if kind, ok := meta["kind"].(string); ok && kind != "" {
		view.Kind = kind
	}
	return view
}

func councilWorkOverlaps(content string) bool {
	lower := strings.ToLower(content)
	markers := []string{
		"both of you",
		"both start",
		"same work",
		"same task",
		"same namespace",
		"duplicate",
		"both research",
		"both implement",
		"both inventory",
	}
	for _, marker := range markers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func councilApplicationKey(namespace, role string) string {
	return "chat:" + namespace + "/" + anvilCouncilName + "/" + role
}

func titleFromPrompt(content string) string {
	collapsed := strings.Join(strings.Fields(strings.TrimSpace(content)), " ")
	runes := []rune(collapsed)
	if len(runes) > 80 {
		return string(runes[:80])
	}
	return collapsed
}
