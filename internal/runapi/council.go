package runapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

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
	councilLLMHarness         = "council-llm"
	councilLLMSecretName      = "council-llm"
	councilLLMImage           = "anvil-agents-council-llm:dev"
	councilDemoImage          = "anvil-agents-demo:dev"
	councilRunnerSA           = "agent-runner"
)

type CouncilConnectRequest struct {
	Name string `json:"name,omitempty"`
}

type CouncilTurnRequest struct {
	Content string `json:"content"`
	To      string `json:"to,omitempty"`
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
	DisplayName   string `json:"displayName,omitempty"`
	WaitingOn     string `json:"waitingOn,omitempty"`
	AddressedTo   string `json:"addressedTo,omitempty"`
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
	Kind               string `json:"kind,omitempty"`
}

type CouncilState struct {
	Name          string                `json:"name"`
	Namespace     string                `json:"namespace"`
	Controller    string                `json:"controller"`
	Connected     bool                  `json:"connected"`
	Members       []CouncilMemberView   `json:"members"`
	Thread        chat.Thread           `json:"thread"`
	Messages      []CouncilMessageView  `json:"messages"`
	Knowledge     []chat.KnowledgeEntry `json:"knowledge"`
	Memory        []chat.MemoryEntry    `json:"memory"`
	DelegatedRuns []CouncilDelegatedRun `json:"delegatedRuns,omitempty"`
	Interrupts    []CouncilMessageView  `json:"interrupts,omitempty"`
}

type CouncilTurnResponse struct {
	CouncilState
	User      CouncilMessageView   `json:"user"`
	Confer    []CouncilMessageView `json:"confer"`
	Interrupt *CouncilMessageView  `json:"interrupt,omitempty"`
	To        string               `json:"to,omitempty"`
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
	workCtx, cancel := context.WithTimeout(context.WithoutCancel(request.Context()), councilHarnessWait+time.Minute)
	defer cancel()
	response, err := server.runAnvilCouncilTurn(workCtx, namespace, principal.Subject, content, body.To)
	if err != nil {
		server.log.Error(err, "anvil council turn", "subject", principal.Subject, "namespace", namespace)
		writeAPIError(writer, http.StatusServiceUnavailable, "council_unavailable", err.Error())
		return
	}
	writeJSON(writer, http.StatusCreated, response)
}

func (server *Server) runAnvilCouncilTurn(ctx context.Context, namespace, subject, content, to string) (CouncilTurnResponse, error) {
	state, err := server.loadCouncilState(ctx, namespace, subject, true)
	if err != nil {
		return CouncilTurnResponse{}, err
	}
	addressee := canonicalCouncilProfile(firstNonEmpty(to, anvilAgentProfileName))
	if !councilMemberSet(state)[addressee] {
		return CouncilTurnResponse{}, fmt.Errorf("cannot message %q; that profile is not in the council", addressee)
	}
	user := councilMessage(chat.RoleUser, content, map[string]any{
		"authorKind":    "human",
		"authorSubject": subject,
		"displayName":   "You",
		"kind":          "utterance",
		"addressedTo":   addressee,
		"audience":      addressee,
	})
	storedUser, _, err := server.chatStore.AppendMessages(ctx, namespace, state.Thread.ID, []chat.Message{user})
	if err != nil {
		return CouncilTurnResponse{}, err
	}
	if len(storedUser) != 1 {
		return CouncilTurnResponse{}, fmt.Errorf("unexpected user append count %d", len(storedUser))
	}

	prompt := buildCouncilHarnessPrompt(state, addressee, content)
	conversation, err := server.startCouncilHarnessRun(ctx, namespace, addressee, prompt)
	if err != nil {
		return CouncilTurnResponse{}, err
	}
	output, err := server.waitCouncilHarnessOutput(ctx, namespace, conversation.Name)
	if err != nil {
		_, _, _ = server.chatStore.AppendMessages(ctx, namespace, state.Thread.ID, []chat.Message{
			councilMessage(chat.RoleAssistant, "Harness did not produce a decision: "+err.Error(), memberMeta(addressee, councilRoleFor(state, addressee), "status", "", "")),
		})
		return CouncilTurnResponse{}, fmt.Errorf("council harness: %w", err)
	}
	parsed, err := parseCouncilHarnessDecision(output)
	if err != nil {
		_, _, _ = server.chatStore.AppendMessages(ctx, namespace, state.Thread.ID, []chat.Message{
			councilMessage(chat.RoleAssistant, "Harness output was not a council decision: "+err.Error(), memberMeta(addressee, councilRoleFor(state, addressee), "status", "", "")),
		})
		return CouncilTurnResponse{}, fmt.Errorf("council harness: %w", err)
	}
	decision, err := normalizeCouncilDecision(state, addressee, parsed)
	if err != nil {
		_, _, _ = server.chatStore.AppendMessages(ctx, namespace, state.Thread.ID, []chat.Message{
			councilMessage(chat.RoleAssistant, "Harness decision was invalid: "+err.Error(), memberMeta(addressee, councilRoleFor(state, addressee), "status", "", "")),
		})
		return CouncilTurnResponse{}, fmt.Errorf("council harness: %w", err)
	}

	conferMessages := make([]chat.Message, 0, len(decision.Utterances))
	for _, utterance := range decision.Utterances {
		meta := memberMeta(utterance.Profile, utterance.Role, utterance.Kind, utterance.WaitingOn, utterance.AddressedTo)
		conferMessages = append(conferMessages, councilMessage(chat.RoleAssistant, utterance.Content, meta))
	}
	storedConfer, _, err := server.chatStore.AppendMessages(ctx, namespace, state.Thread.ID, conferMessages)
	if err != nil {
		return CouncilTurnResponse{}, err
	}

	if err := server.applyCouncilMemory(ctx, namespace, content, addressee, decision); err != nil {
		return CouncilTurnResponse{}, err
	}

	workRuns, err := server.delegateCouncilDecision(ctx, namespace, decision)
	if err != nil {
		return CouncilTurnResponse{}, err
	}
	runs := append([]CouncilDelegatedRun{conversation}, workRuns...)

	next, err := server.loadCouncilState(ctx, namespace, subject, false)
	if err != nil {
		return CouncilTurnResponse{}, err
	}
	next.DelegatedRuns = runs
	userView := viewCouncilMessage(storedUser[0])
	conferViews := make([]CouncilMessageView, 0, len(storedConfer))
	var interrupt *CouncilMessageView
	for _, message := range storedConfer {
		view := viewCouncilMessage(message)
		conferViews = append(conferViews, view)
		if view.Kind == "interrupt" && interrupt == nil {
			copyView := view
			interrupt = &copyView
		}
	}
	return CouncilTurnResponse{
		CouncilState: next,
		User:         userView,
		Confer:       conferViews,
		Interrupt:    interrupt,
		To:           addressee,
	}, nil
}

func (server *Server) applyCouncilMemory(ctx context.Context, namespace, content, addressee string, decision councilHarnessDecision) error {
	if _, err := server.chatStore.UpsertMemory(ctx, chat.MemoryEntry{
		Namespace:   namespace,
		CouncilName: anvilCouncilName,
		Key:         "last-turn",
		Value:       titleFromPrompt(content),
	}); err != nil {
		return err
	}
	if _, err := server.chatStore.UpsertMemory(ctx, chat.MemoryEntry{
		Namespace:   namespace,
		CouncilName: anvilCouncilName,
		Key:         "last-addressee",
		Value:       firstNonEmpty(addressee, decision.Speaker, anvilAgentProfileName),
	}); err != nil {
		return err
	}
	for _, entry := range decision.Memory {
		if _, err := server.chatStore.UpsertMemory(ctx, chat.MemoryEntry{
			Namespace:   namespace,
			CouncilName: anvilCouncilName,
			Key:         entry.Key,
			Value:       entry.Value,
		}); err != nil {
			return err
		}
	}
	for _, delegate := range decision.Delegates {
		if delegate.Skip || delegate.Claim == "" {
			continue
		}
		if _, err := server.chatStore.UpsertMemory(ctx, chat.MemoryEntry{
			Namespace:   namespace,
			CouncilName: anvilCouncilName,
			Key:         "claim:" + delegate.Claim,
			Value:       delegate.Profile,
		}); err != nil {
			return err
		}
	}
	for _, utterance := range decision.Utterances {
		if utterance.WaitingOn == "" {
			continue
		}
		if _, err := server.chatStore.UpsertMemory(ctx, chat.MemoryEntry{
			Namespace:   namespace,
			CouncilName: anvilCouncilName,
			Key:         "waiting-on",
			Value:       utterance.WaitingOn,
		}); err != nil {
			return err
		}
		break
	}
	return nil
}

func (server *Server) delegateCouncilDecision(ctx context.Context, namespace string, decision councilHarnessDecision) ([]CouncilDelegatedRun, error) {
	type job struct {
		delegate councilHarnessDelegate
		app      string
	}
	jobs := make([]job, 0, len(decision.Delegates))
	for _, item := range decision.Delegates {
		if item.Skip || item.Profile == "" {
			continue
		}
		jobs = append(jobs, job{delegate: item, app: councilApplicationKey(namespace, firstNonEmpty(item.Role, "member"))})
	}
	if len(jobs) == 0 {
		return nil, nil
	}
	out := make([]CouncilDelegatedRun, len(jobs))
	errs := make([]error, len(jobs))
	var wg sync.WaitGroup
	for i, item := range jobs {
		wg.Add(1)
		go func(i int, item job) {
			defer wg.Done()
			run, err := buildAgentRunFromCreateRequest(namespace, CreateAgentRunRequest{
				Name:               newCouncilRunName(item.delegate.Role),
				Prompt:             item.delegate.Prompt,
				ProfileName:        item.delegate.Profile,
				HarnessProfileName: item.delegate.Harness,
				Intent:             item.delegate.Intent,
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
			run.Labels["control.anvil.hazyforge.io/council-role"] = item.delegate.Role
			run.Labels["control.anvil.hazyforge.io/council-kind"] = "delegate"
			if err := server.writes.Create(ctx, run); err != nil {
				errs[i] = err
				return
			}
			view := NewAgentRunView(run, false)
			backend := strings.TrimSpace(view.Backend)
			if backend == "" {
				backend = item.delegate.Backend
			}
			out[i] = CouncilDelegatedRun{
				Name:               run.Name,
				Namespace:          run.Namespace,
				ProfileName:        item.delegate.Profile,
				HarnessProfileName: item.delegate.Harness,
				Backend:            backend,
				Role:               item.delegate.Role,
				Application:        item.app,
				Kind:               "delegate",
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
			Body:        "Anvil agent controls this council. Talk to Anvil agent and its harness decides who confers and what to delegate. You can also talk to a member directly; that member replies as itself and may message Anvil or peers. Members share one knowledge base and one memory. If two claim the same work, the duplicate is interrupted.",
		},
		{
			Namespace:   namespace,
			CouncilName: anvilCouncilName,
			Title:       "Harness mix",
			Body:        "Conversation turns use council-llm. Work delegates mix council-research (custom) and council-grok (grokBuild). Do not share demo-state across Jobs.",
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
			Kind:               run.Labels["control.anvil.hazyforge.io/council-kind"],
		})
	}
	return out
}

func (server *Server) ensureAnvilCouncilObjects(ctx context.Context, namespace string) error {
	if server.writes == nil {
		return fmt.Errorf("composition write client is not configured")
	}
	if err := server.ensureConsoleOwned(ctx, councilLLMHarnessObject(namespace)); err != nil {
		return err
	}
	if err := server.ensureConsoleOwned(ctx, researchHarness(namespace)); err != nil {
		return err
	}
	if err := server.ensureConsoleOwned(ctx, server.implementerHarness(namespace)); err != nil {
		return err
	}
	if err := server.ensureConsoleOwned(ctx, anvilAgentProfile(namespace)); err != nil {
		return err
	}
	if err := server.ensureConsoleOwned(ctx, councilMemberProfile(namespace, councilResearcherProfile, "researcher", councilResearchHarness, "chat:"+namespace+"/"+anvilCouncilName+"/researcher", agentsv1alpha1.AgentRunIntentObserve)); err != nil {
		return err
	}
	if err := server.ensureConsoleOwned(ctx, councilMemberProfile(namespace, councilImplementerProfile, "implementer", councilGrokHarness, "chat:"+namespace+"/"+anvilCouncilName+"/implementer", agentsv1alpha1.AgentRunIntentProposeChange)); err != nil {
		return err
	}
	return server.ensureConsoleOwned(ctx, anvilCouncilObject(namespace))
}

func (server *Server) ensureConsoleOwned(ctx context.Context, desired client.Object) error {
	stampConsoleManaged(desired)
	current := desired.DeepCopyObject().(client.Object)
	if err := server.runs.Get(ctx, types.NamespacedName{Namespace: desired.GetNamespace(), Name: desired.GetName()}, current); err != nil {
		if apierrors.IsNotFound(err) {
			if err := server.writes.Create(ctx, desired); err != nil && !apierrors.IsAlreadyExists(err) {
				return err
			}
			return nil
		}
		return err
	}
	reason := evaluateCompositionManagement(current).Reason
	if reason == managementReasonGitOpsProtected {
		return nil
	}
	if reason != managementReasonConsoleManaged {
		return nil
	}
	switch want := desired.(type) {
	case *agentsv1alpha1.AgentRunProfile:
		have := current.(*agentsv1alpha1.AgentRunProfile)
		have.Spec = want.Spec
		return server.writes.Update(ctx, have)
	case *agentsv1alpha1.AgentHarnessProfile:
		have := current.(*agentsv1alpha1.AgentHarnessProfile)
		want.Spec.Execution.ExtraEnv = mergeHarnessExtraEnv(have.Spec.Execution.ExtraEnv, want.Spec.Execution.ExtraEnv)
		have.Spec = want.Spec
		return server.writes.Update(ctx, have)
	case *agentsv1alpha1.AgentCouncil:
		have := current.(*agentsv1alpha1.AgentCouncil)
		have.Spec = want.Spec
		return server.writes.Update(ctx, have)
	default:
		return nil
	}
}

func (state CouncilState) memberHarness(profile string) string {
	return state.memberWorkHarness(profile)
}

func (state CouncilState) memberWorkHarness(profile string) string {
	for _, member := range state.Members {
		if member.ProfileName == profile && member.Harness != "" && member.Harness != councilLLMHarness {
			return member.Harness
		}
		if member.ProfileName == profile && member.Harness != "" {
			return member.Harness
		}
	}
	switch profile {
	case councilImplementerProfile:
		return councilGrokHarness
	case anvilAgentProfileName:
		return councilLLMHarness
	default:
		return councilResearchHarness
	}
}

func defaultCouncilMembers() []CouncilMemberView {
	return []CouncilMemberView{
		{Role: "controller", ProfileName: anvilAgentProfileName, Harness: councilLLMHarness, Backend: "custom", Description: "Main chat agent in charge of the council"},
		{Role: "researcher", ProfileName: councilResearcherProfile, Harness: councilResearchHarness, Backend: "custom"},
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
			Harness:           agentsv1alpha1.AgentRunHarnessSpec{Intent: agentsv1alpha1.AgentRunIntentObserve},
			HarnessProfileRef: &agentsv1alpha1.NamespacedObjectReference{Name: councilLLMHarness},
			CouncilRef:        &agentsv1alpha1.NamespacedObjectReference{Name: anvilCouncilName},
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

func councilLLMHarnessObject(namespace string) *agentsv1alpha1.AgentHarnessProfile {
	return &agentsv1alpha1.AgentHarnessProfile{
		TypeMeta: metav1.TypeMeta{APIVersion: agentsv1alpha1.GroupVersion.String(), Kind: "AgentHarnessProfile"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      councilLLMHarness,
			Namespace: namespace,
		},
		Spec: agentsv1alpha1.AgentHarnessProfileSpec{
			Description: "Non-deterministic council conversation harness. Reads DEEPSEEK_API_KEY from Secret council-llm; the API never mounts that Secret.",
			Backend: agentsv1alpha1.AgentRunHarnessBackendSpec{
				Kind:            agentsv1alpha1.AgentRunHarnessBackendCustom,
				Image:           councilLLMImage,
				ImagePullPolicy: corev1.PullIfNotPresent,
			},
			Execution: agentsv1alpha1.AgentRunHarnessExecutionSpec{
				ServiceAccountName: councilRunnerSA,
				TimeoutSeconds:     180,
				EnvSecretRefs:      []agentsv1alpha1.NamespacedObjectReference{{Name: councilLLMSecretName}},
				ExtraEnv: []corev1.EnvVar{
					{Name: "DEEPSEEK_MODEL", Value: "deepseek-chat"},
				},
			},
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
			Description:   "Anvil agent council: talk to Anvil or a member. Anvil's harness decides delegation. Members reply as themselves and may message peers.",
			CouncilPrompt: "Anvil agent is in charge. A message to Anvil goes through the council-llm harness, which decides conferral and delegation. A message to a member is a conversation with that agent; they may address Anvil or peers. Interrupt duplicate work. Mix harnesses on parallel delegates.",
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

func memberMeta(profile, role, kind, waitingOn, addressedTo string) map[string]any {
	meta := map[string]any{
		"authorKind":    "member",
		"authorProfile": profile,
		"authorRole":    role,
		"displayName":   councilDisplayName(profile),
		"kind":          kind,
		"audience":      firstNonEmpty(addressedTo, "all"),
	}
	if waitingOn != "" {
		meta["waitingOn"] = waitingOn
	}
	if addressedTo != "" {
		meta["addressedTo"] = addressedTo
	}
	return meta
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
	view.DisplayName, _ = meta["displayName"].(string)
	view.WaitingOn, _ = meta["waitingOn"].(string)
	view.AddressedTo, _ = meta["addressedTo"].(string)
	if kind, ok := meta["kind"].(string); ok && kind != "" {
		view.Kind = kind
	}
	if view.DisplayName == "" {
		if view.AuthorKind == "human" {
			view.DisplayName = "You"
		} else {
			view.DisplayName = councilDisplayName(view.AuthorProfile)
		}
	}
	return view
}

func councilDisplayName(profile string) string {
	switch strings.TrimSpace(profile) {
	case anvilAgentProfileName:
		return "Anvil agent"
	case councilResearcherProfile:
		return "Council Researcher"
	case councilImplementerProfile:
		return "Council Implementer"
	case "":
		return ""
	default:
		return profile
	}
}

func mergeHarnessExtraEnv(existing, desired []corev1.EnvVar) []corev1.EnvVar {
	out := append([]corev1.EnvVar(nil), desired...)
	seen := map[string]struct{}{}
	for _, item := range out {
		if name := strings.TrimSpace(item.Name); name != "" {
			seen[name] = struct{}{}
		}
	}
	for _, item := range existing {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		out = append(out, item)
		seen[name] = struct{}{}
	}
	return out
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
