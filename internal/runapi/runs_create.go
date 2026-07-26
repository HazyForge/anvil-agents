package runapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apiValidation "k8s.io/apimachinery/pkg/api/validation"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	agentsv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

const (
	createRunMaxBodyBytes = 256 * 1024
	createRunMaxPrompt    = 1024 * 1024
)

// CreateAgentRunRequest is the console/API body for append-only AgentRun creation.
type CreateAgentRunRequest struct {
	Name               string   `json:"name,omitempty"`
	GenerateName       string   `json:"generateName,omitempty"`
	Prompt             string   `json:"prompt"`
	ProfileName        string   `json:"profileName"`
	HarnessProfileName string   `json:"harnessProfileName,omitempty"`
	SkillSetNames      []string `json:"skillSetNames,omitempty"`
	ToolSetNames       []string `json:"toolSetNames,omitempty"`
	Intent             string   `json:"intent,omitempty"`
	Purpose            string   `json:"purpose,omitempty"`
	SourceKind         string   `json:"sourceKind,omitempty"`
	SourceName         string   `json:"sourceName,omitempty"`
	SourceAPIVersion   string   `json:"sourceAPIVersion,omitempty"`
	SourceNamespace    string   `json:"sourceNamespace,omitempty"`
}

func (server *Server) handleCreateRun(writer http.ResponseWriter, request *http.Request) {
	namespace := request.PathValue("namespace")
	principal := principalFromContext(request.Context())
	if !server.config.Runs.CreateEnabled {
		writeAPIError(writer, http.StatusNotFound, "runs_create_disabled", "AgentRun create is disabled")
		return
	}
	if !server.authorizer.Allowed(principal, PermissionRunsCreate, namespace) {
		writeAPIError(writer, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	writerClient, ok := server.writerClient(writer)
	if !ok {
		return
	}

	body, err := readCreateRunBody(request)
	if err != nil {
		writeAPIError(writer, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	run, err := buildAgentRunFromCreateRequest(namespace, body)
	if err != nil {
		writeAPIError(writer, http.StatusBadRequest, "invalid", err.Error())
		return
	}
	if err := writerClient.Create(request.Context(), run); err != nil {
		if apierrors.IsAlreadyExists(err) {
			writeAPIError(writer, http.StatusConflict, "conflict", "AgentRun already exists; choose a new name")
			return
		}
		if apierrors.IsInvalid(err) || apierrors.IsBadRequest(err) {
			writeAPIError(writer, http.StatusBadRequest, "invalid", err.Error())
			return
		}
		server.log.Error(err, "create AgentRun", "subject", principal.Subject, "namespace", namespace)
		writeAPIError(writer, http.StatusServiceUnavailable, "kubernetes_unavailable", "AgentRun create failed")
		return
	}
	server.log.Info("AgentRun create",
		"subject", principal.Subject,
		"issuer", principal.Issuer,
		"namespace", run.Namespace,
		"agentRun", run.Name,
		"profile", body.ProfileName,
	)
	writeJSON(writer, http.StatusCreated, NewAgentRunView(run, true))
}

func readCreateRunBody(request *http.Request) (CreateAgentRunRequest, error) {
	defer request.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(request.Body, createRunMaxBodyBytes+1))
	if err != nil {
		return CreateAgentRunRequest{}, fmt.Errorf("read body: %w", err)
	}
	if len(raw) > createRunMaxBodyBytes {
		return CreateAgentRunRequest{}, fmt.Errorf("request body exceeds %d bytes", createRunMaxBodyBytes)
	}
	var body CreateAgentRunRequest
	if err := json.Unmarshal(raw, &body); err != nil {
		return CreateAgentRunRequest{}, fmt.Errorf("decode JSON body: %w", err)
	}
	return body, nil
}

func buildAgentRunFromCreateRequest(namespace string, body CreateAgentRunRequest) (*agentsv1alpha1.AgentRun, error) {
	if problems := apiValidation.NameIsDNSSubdomain(namespace, false); len(problems) > 0 {
		return nil, fmt.Errorf("invalid namespace: %s", strings.Join(problems, "; "))
	}
	name := strings.TrimSpace(body.Name)
	generateName := strings.TrimSpace(body.GenerateName)
	if name == "" && generateName == "" {
		generateName = "console-card-"
	}
	if name != "" && generateName != "" {
		return nil, fmt.Errorf("set only one of name or generateName")
	}
	if name != "" {
		if problems := apiValidation.NameIsDNSSubdomain(name, false); len(problems) > 0 {
			return nil, fmt.Errorf("invalid name: %s", strings.Join(problems, "; "))
		}
	}
	if generateName != "" {
		if problems := apiValidation.NameIsDNSSubdomain(generateName, true); len(problems) > 0 {
			return nil, fmt.Errorf("invalid generateName: %s", strings.Join(problems, "; "))
		}
	}
	profile := strings.TrimSpace(body.ProfileName)
	if profile == "" {
		return nil, fmt.Errorf("profileName is required")
	}
	prompt := body.Prompt
	if strings.TrimSpace(prompt) == "" {
		return nil, fmt.Errorf("prompt is required")
	}
	if len(prompt) > createRunMaxPrompt {
		return nil, fmt.Errorf("prompt exceeds %d bytes", createRunMaxPrompt)
	}
	purpose := agentsv1alpha1.AgentRunPurposeManual
	if strings.TrimSpace(body.Purpose) != "" {
		purpose = agentsv1alpha1.AgentRunPurpose(strings.TrimSpace(body.Purpose))
		switch purpose {
		case agentsv1alpha1.AgentRunPurposeManual, agentsv1alpha1.AgentRunPurposeAdverseSituation, agentsv1alpha1.AgentRunPurposeScheduledHealthCheck:
		default:
			return nil, fmt.Errorf("invalid purpose %q", body.Purpose)
		}
	}
	intent := agentsv1alpha1.AgentRunIntent(strings.TrimSpace(body.Intent))
	if intent != "" {
		switch intent {
		case agentsv1alpha1.AgentRunIntentObserve, agentsv1alpha1.AgentRunIntentFixTransient, agentsv1alpha1.AgentRunIntentProposeChange, agentsv1alpha1.AgentRunIntentCleanup:
		default:
			return nil, fmt.Errorf("invalid intent %q", body.Intent)
		}
	}
	sourceKind := strings.TrimSpace(body.SourceKind)
	if sourceKind == "" {
		sourceKind = "ConsoleCard"
	}
	sourceName := strings.TrimSpace(body.SourceName)
	if sourceName == "" {
		sourceName = "browser"
	}

	run := &agentsv1alpha1.AgentRun{
		TypeMeta: metav1.TypeMeta{
			APIVersion: agentsv1alpha1.GroupVersion.String(),
			Kind:       "AgentRun",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:         name,
			GenerateName: generateName,
			Namespace:    namespace,
			Labels: map[string]string{
				"control.anvil.hazyforge.io/created-by": "anvil-agents-console",
			},
		},
		Spec: agentsv1alpha1.AgentRunSpec{
			Purpose: purpose,
			SourceRef: agentsv1alpha1.AgentRunSourceRef{
				APIVersion: strings.TrimSpace(body.SourceAPIVersion),
				Kind:       sourceKind,
				Namespace:  firstNonEmpty(strings.TrimSpace(body.SourceNamespace), namespace),
				Name:       sourceName,
			},
			Prompt:     prompt,
			ProfileRef: &agentsv1alpha1.NamespacedObjectReference{Name: profile},
		},
	}
	if harness := strings.TrimSpace(body.HarnessProfileName); harness != "" {
		run.Spec.HarnessProfileRef = &agentsv1alpha1.NamespacedObjectReference{Name: harness}
	}
	if intent != "" {
		run.Spec.Harness.Intent = intent
	}
	if refs := namespacedRefs(body.SkillSetNames); len(refs) > 0 {
		run.Spec.SkillSets = &agentsv1alpha1.AgentSkillCompositionSpec{
			Mode: agentsv1alpha1.AgentSkillCompositionReplace,
			Refs: refs,
		}
	}
	if refs := namespacedRefs(body.ToolSetNames); len(refs) > 0 {
		run.Spec.ToolSets = &agentsv1alpha1.AgentToolCompositionSpec{
			Mode: agentsv1alpha1.AgentToolCompositionReplace,
			Refs: refs,
		}
	}
	return run, nil
}

func namespacedRefs(names []string) []agentsv1alpha1.NamespacedObjectReference {
	out := make([]agentsv1alpha1.NamespacedObjectReference, 0, len(names))
	seen := map[string]struct{}{}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, agentsv1alpha1.NamespacedObjectReference{Name: name})
	}
	return out
}
