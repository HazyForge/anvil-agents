package runapi

import (
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	agentsv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

const (
	controlMaxBodyBytes = 256 * 1024
)

// ControlSourceView is the descriptive audit metadata carried by a control.
type ControlSourceView struct {
	Kind  string `json:"kind,omitempty"`
	Name  string `json:"name,omitempty"`
	URL   string `json:"url,omitempty"`
	Actor string `json:"actor,omitempty"`
}

// ControlManagement mirrors CompositionManagement for launch gates.
type ControlManagement struct {
	Writable  bool   `json:"writable"`
	Reason    string `json:"reason"`
	ManagedBy string `json:"managedBy,omitempty"`
}

// ControlView is the browser-facing envelope for one AgentRunControl.
type ControlView struct {
	Name              string             `json:"name"`
	Application       string             `json:"application"`
	LaunchPolicy      string             `json:"launchPolicy"`
	Phase             string             `json:"phase,omitempty"`
	Reason            string             `json:"reason,omitempty"`
	ExpiresAt         *time.Time         `json:"expiresAt,omitempty"`
	Source            *ControlSourceView `json:"source,omitempty"`
	AffectedSchedules int32              `json:"affectedSchedules,omitempty"`
	PendingRuns       int32              `json:"pendingRuns,omitempty"`
	ActiveRuns        int32              `json:"activeRuns,omitempty"`
	ResourceVersion   string             `json:"resourceVersion"`
	Generation        int64              `json:"generation,omitempty"`
	CreatedAt         time.Time          `json:"createdAt"`
	Management        ControlManagement  `json:"management"`
}

type ControlListResponse struct {
	Items []ControlView `json:"items"`
}

// ControlWriteRequest is the pause/resume mutation accepted by PUT.
type ControlWriteRequest struct {
	// LaunchPolicy must be "Paused" (pause) or "Allowed" (resume).
	LaunchPolicy string `json:"launchPolicy"`
	// Reason is required and explains why the launch gate changed.
	Reason string `json:"reason"`
	// ExpiresAt bounds a pause in RFC3339; empty means indefinite (human-only).
	ExpiresAt string `json:"expiresAt,omitempty"`
	// SourceName/URL/Actor record immutable event or directive metadata.
	SourceName  string `json:"sourceName,omitempty"`
	SourceURL   string `json:"sourceUrl,omitempty"`
	SourceActor string `json:"sourceActor,omitempty"`
	// ResourceVersion must match the current control to avoid clobbering.
	ResourceVersion string `json:"resourceVersion"`
}

func (server *Server) registerControlRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/controls", server.authenticate(http.HandlerFunc(server.handleControlsList)))
	mux.Handle("GET /api/v1/controls/{name}", server.authenticate(http.HandlerFunc(server.handleControlsGet)))
	mux.Handle("PUT /api/v1/controls/{name}", server.authenticate(http.HandlerFunc(server.handleControlsUpdate)))
}

func (server *Server) handleControlsList(writer http.ResponseWriter, request *http.Request) {
	if !server.config.Controls.ReadEnabled {
		writeAPIError(writer, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	principal := principalFromContext(request.Context())
	list := &agentsv1alpha1.AgentRunControlList{}
	if err := server.runs.List(request.Context(), list); err != nil {
		server.log.Error(err, "list AgentRunControls", "subject", principal.Subject)
		writeAPIError(writer, http.StatusServiceUnavailable, "kubernetes_unavailable", "launch gate state is unavailable")
		return
	}
	items := make([]agentsv1alpha1.AgentRunControl, 0, len(list.Items))
	for i := range list.Items {
		control := &list.Items[i]
		application := strings.TrimSpace(control.Spec.ApplicationRef.Name)
		if !server.authorizer.Allowed(principal, PermissionControlsRead, application) {
			continue
		}
		items = append(items, *control)
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].Name < items[j].Name
	})
	response := ControlListResponse{Items: make([]ControlView, 0, len(items))}
	for i := range items {
		response.Items = append(response.Items, newControlView(&items[i]))
	}
	writeJSON(writer, http.StatusOK, response)
}

func (server *Server) handleControlsGet(writer http.ResponseWriter, request *http.Request) {
	if !server.config.Controls.ReadEnabled {
		writeAPIError(writer, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	name := request.PathValue("name")
	control := &agentsv1alpha1.AgentRunControl{}
	if err := server.runs.Get(request.Context(), types.NamespacedName{Name: name}, control); err != nil {
		if apierrors.IsNotFound(err) {
			writeAPIError(writer, http.StatusNotFound, "not_found", "resource not found")
			return
		}
		server.log.Error(err, "get AgentRunControl", "name", name)
		writeAPIError(writer, http.StatusServiceUnavailable, "kubernetes_unavailable", "launch gate state is unavailable")
		return
	}
	principal := principalFromContext(request.Context())
	application := strings.TrimSpace(control.Spec.ApplicationRef.Name)
	if !server.authorizer.Allowed(principal, PermissionControlsRead, application) {
		writeAPIError(writer, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	writeJSON(writer, http.StatusOK, newControlView(control))
}

func (server *Server) handleControlsUpdate(writer http.ResponseWriter, request *http.Request) {
	if !server.config.Controls.WriteEnabled {
		writeAPIError(writer, http.StatusNotFound, "controls_write_disabled", "launch gate write is disabled")
		return
	}
	name := request.PathValue("name")
	principal := principalFromContext(request.Context())
	writerClient, ok := server.writerClient(writer)
	if !ok {
		return
	}

	defer request.Body.Close()
	limited := io.LimitReader(request.Body, controlMaxBodyBytes+1)
	raw, err := io.ReadAll(limited)
	if err != nil {
		writeAPIError(writer, http.StatusBadRequest, "invalid_body", "read body failed")
		return
	}
	if len(raw) > controlMaxBodyBytes {
		writeAPIError(writer, http.StatusBadRequest, "invalid_body", "request body too large")
		return
	}
	var body ControlWriteRequest
	if err := json.Unmarshal(raw, &body); err != nil {
		writeAPIError(writer, http.StatusBadRequest, "invalid_body", "decode JSON body failed")
		return
	}

	launchPolicy := agentsv1alpha1.AgentRunControlLaunchPolicy(strings.TrimSpace(body.LaunchPolicy))
	if launchPolicy != agentsv1alpha1.AgentRunControlLaunchPolicyPaused && launchPolicy != agentsv1alpha1.AgentRunControlLaunchPolicyAllowed {
		writeAPIError(writer, http.StatusBadRequest, "invalid", "launchPolicy must be Paused or Allowed")
		return
	}
	reason := strings.TrimSpace(body.Reason)
	if reason == "" {
		writeAPIError(writer, http.StatusBadRequest, "invalid", "reason is required")
		return
	}
	var expiresAt *time.Time
	if value := strings.TrimSpace(body.ExpiresAt); value != "" {
		parsed, err := time.Parse(time.RFC3339, value)
		if err != nil {
			writeAPIError(writer, http.StatusBadRequest, "invalid", "expiresAt must be an RFC3339 timestamp")
			return
		}
		expiresAt = &parsed
	}
	if launchPolicy == agentsv1alpha1.AgentRunControlLaunchPolicyAllowed && expiresAt != nil {
		writeAPIError(writer, http.StatusBadRequest, "invalid", "expiresAt is only valid when pausing")
		return
	}

	current := &agentsv1alpha1.AgentRunControl{}
	if err := writerClient.Get(request.Context(), types.NamespacedName{Name: name}, current); err != nil {
		if apierrors.IsNotFound(err) {
			writeAPIError(writer, http.StatusNotFound, "not_found", "resource not found")
			return
		}
		server.log.Error(err, "get AgentRunControl for update", "name", name)
		writeAPIError(writer, http.StatusServiceUnavailable, "kubernetes_unavailable", "launch gate state is unavailable")
		return
	}
	application := strings.TrimSpace(current.Spec.ApplicationRef.Name)
	if !server.authorizer.Allowed(principal, PermissionControlsWrite, application) {
		writeAPIError(writer, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	if management := evaluateControlManagement(current); !management.Writable {
		writeAPIError(writer, http.StatusForbidden, management.Reason, controlWriteBlockedMessage(management))
		return
	}
	if strings.TrimSpace(body.ResourceVersion) == "" {
		writeAPIError(writer, http.StatusBadRequest, "invalid_body", "resourceVersion is required for updates")
		return
	}
	if current.GetResourceVersion() != strings.TrimSpace(body.ResourceVersion) {
		writeAPIError(writer, http.StatusConflict, "conflict", "resourceVersion does not match the current control; reload and retry")
		return
	}

	current.Spec.LaunchPolicy = launchPolicy
	current.Spec.Reason = reason
	current.Spec.Source = controlSourceFromWrite(body)
	if launchPolicy == agentsv1alpha1.AgentRunControlLaunchPolicyPaused {
		if expiresAt != nil {
			stamp := metav1NewTime(*expiresAt)
			current.Spec.ExpiresAt = &stamp
		} else {
			current.Spec.ExpiresAt = nil
		}
	} else {
		current.Spec.ExpiresAt = nil
	}

	if err := writerClient.Update(request.Context(), current); err != nil {
		if apierrors.IsConflict(err) {
			writeAPIError(writer, http.StatusConflict, "conflict", "resourceVersion conflict; reload and retry")
			return
		}
		if apierrors.IsInvalid(err) || apierrors.IsBadRequest(err) {
			writeAPIError(writer, http.StatusBadRequest, "invalid", err.Error())
			return
		}
		server.log.Error(err, "update AgentRunControl", "subject", principal.Subject, "name", name, "application", application, "launchPolicy", launchPolicy)
		writeAPIError(writer, http.StatusServiceUnavailable, "kubernetes_unavailable", "launch gate update failed")
		return
	}
	server.log.Info("AgentRunControl update",
		"subject", principal.Subject,
		"issuer", principal.Issuer,
		"name", name,
		"application", application,
		"launchPolicy", launchPolicy,
		"reason", reason,
	)
	writeJSON(writer, http.StatusOK, newControlView(current))
}

// evaluateControlManagement decides whether the API may mutate a launch gate.
// Controls are operational state rather than GitOps composition source: only
// well-known GitOps ownership markers block writes.
func evaluateControlManagement(meta metav1.Object) ControlManagement {
	if meta == nil {
		return ControlManagement{Writable: true}
	}
	if manager, ok := detectGitOpsManager(meta); ok {
		return ControlManagement{
			Writable:  false,
			Reason:    managementReasonGitOpsProtected,
			ManagedBy: manager,
		}
	}
	managedBy := ""
	if labels := meta.GetLabels(); labels != nil {
		managedBy = strings.TrimSpace(labels[LabelManagedBy])
	}
	if managedBy == "" {
		managedBy = "operator"
	}
	return ControlManagement{
		Writable:  true,
		Reason:    managementReasonConsoleManaged,
		ManagedBy: managedBy,
	}
}

func controlWriteBlockedMessage(management ControlManagement) string {
	if management.Reason == managementReasonGitOpsProtected {
		return "launch gate is owned by GitOps (" + management.ManagedBy + "); edit the Git source of truth instead"
	}
	return "launch gate is not writable through this API"
}

func controlSourceFromWrite(body ControlWriteRequest) *agentsv1alpha1.AgentRunControlSourceSpec {
	name := strings.TrimSpace(body.SourceName)
	url := strings.TrimSpace(body.SourceURL)
	actor := strings.TrimSpace(body.SourceActor)
	if name == "" && url == "" && actor == "" {
		return &agentsv1alpha1.AgentRunControlSourceSpec{Kind: "Operator"}
	}
	source := &agentsv1alpha1.AgentRunControlSourceSpec{Kind: "Operator"}
	if strings.Contains(strings.ToLower(url), "github.com/") {
		source.Kind = "PullRequest"
	}
	source.Name = name
	source.URL = url
	source.Actor = actor
	return source
}

func newControlView(control *agentsv1alpha1.AgentRunControl) ControlView {
	view := ControlView{
		Name:              control.Name,
		Application:       strings.TrimSpace(control.Spec.ApplicationRef.Name),
		LaunchPolicy:      string(control.Spec.LaunchPolicy),
		Phase:             string(control.Status.Phase),
		Reason:            control.Spec.Reason,
		AffectedSchedules: control.Status.AffectedScheduleCount,
		PendingRuns:       control.Status.PendingRunCount,
		ActiveRuns:        control.Status.ActiveRunCount,
		ResourceVersion:   control.GetResourceVersion(),
		Generation:        control.GetGeneration(),
		CreatedAt:         control.GetCreationTimestamp().Time,
		Management:        evaluateControlManagement(control),
	}
	if control.Spec.ExpiresAt != nil {
		expiry := control.Spec.ExpiresAt.Time
		view.ExpiresAt = &expiry
	}
	if control.Spec.Source != nil {
		view.Source = &ControlSourceView{
			Kind:  control.Spec.Source.Kind,
			Name:  control.Spec.Source.Name,
			URL:   control.Spec.Source.URL,
			Actor: control.Spec.Source.Actor,
		}
	}
	return view
}

func metav1NewTime(value time.Time) metav1.Time {
	return metav1.Time{Time: value}
}
