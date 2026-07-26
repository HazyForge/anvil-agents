package runapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	agentsv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

const (
	compositionMaxBodyBytes = 2 * 1024 * 1024
	compositionListDefault  = 50
	compositionListMax      = 200
)

// CompositionDocument is the browser-editable envelope for one composition CR.
type CompositionDocument struct {
	APIVersion string                `json:"apiVersion"`
	Kind       string                `json:"kind"`
	Metadata   CompositionMetadata   `json:"metadata"`
	Spec       json.RawMessage       `json:"spec"`
	Status     json.RawMessage       `json:"status,omitempty"`
	Management CompositionManagement `json:"management"`
}

type CompositionMetadata struct {
	Name              string            `json:"name"`
	Namespace         string            `json:"namespace"`
	UID               string            `json:"uid,omitempty"`
	ResourceVersion   string            `json:"resourceVersion,omitempty"`
	Generation        int64             `json:"generation,omitempty"`
	CreationTimestamp *time.Time        `json:"creationTimestamp,omitempty"`
	Labels            map[string]string `json:"labels,omitempty"`
	Annotations       map[string]string `json:"annotations,omitempty"`
}

type CompositionListResponse struct {
	Items []CompositionDocument `json:"items"`
}

type compositionKind struct {
	PathSegment string
	Kind        string
	NewObject   func() client.Object
	NewList     func() client.ObjectList
	HasStatus   bool
}

var compositionKinds = map[string]compositionKind{
	"agent-run-profiles": {
		PathSegment: "agent-run-profiles",
		Kind:        "AgentRunProfile",
		NewObject:   func() client.Object { return &agentsv1alpha1.AgentRunProfile{} },
		NewList:     func() client.ObjectList { return &agentsv1alpha1.AgentRunProfileList{} },
	},
	"agent-harness-profiles": {
		PathSegment: "agent-harness-profiles",
		Kind:        "AgentHarnessProfile",
		NewObject:   func() client.Object { return &agentsv1alpha1.AgentHarnessProfile{} },
		NewList:     func() client.ObjectList { return &agentsv1alpha1.AgentHarnessProfileList{} },
	},
	"agent-skill-sets": {
		PathSegment: "agent-skill-sets",
		Kind:        "AgentSkillSet",
		NewObject:   func() client.Object { return &agentsv1alpha1.AgentSkillSet{} },
		NewList:     func() client.ObjectList { return &agentsv1alpha1.AgentSkillSetList{} },
	},
	"agent-tool-sets": {
		PathSegment: "agent-tool-sets",
		Kind:        "AgentToolSet",
		NewObject:   func() client.Object { return &agentsv1alpha1.AgentToolSet{} },
		NewList:     func() client.ObjectList { return &agentsv1alpha1.AgentToolSetList{} },
	},
	"volume-profiles": {
		PathSegment: "volume-profiles",
		Kind:        "VolumeProfile",
		NewObject:   func() client.Object { return &agentsv1alpha1.VolumeProfile{} },
		NewList:     func() client.ObjectList { return &agentsv1alpha1.VolumeProfileList{} },
		HasStatus:   true,
	},
	"agent-data-volumes": {
		PathSegment: "agent-data-volumes",
		Kind:        "AgentDataVolume",
		NewObject:   func() client.Object { return &agentsv1alpha1.AgentDataVolume{} },
		NewList:     func() client.ObjectList { return &agentsv1alpha1.AgentDataVolumeList{} },
		HasStatus:   true,
	},
}

func (server *Server) registerCompositionRoutes(mux *http.ServeMux) {
	for pathSegment := range compositionKinds {
		segment := pathSegment
		listPath := "/api/v1/namespaces/{namespace}/" + segment
		itemPath := "/api/v1/namespaces/{namespace}/" + segment + "/{name}"
		mux.Handle("GET "+listPath, server.authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			server.handleCompositionList(w, r, segment)
		})))
		mux.Handle("GET "+itemPath, server.authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			server.handleCompositionGet(w, r, segment)
		})))
		mux.Handle("POST "+listPath, server.authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			server.handleCompositionCreate(w, r, segment)
		})))
		mux.Handle("PUT "+itemPath, server.authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			server.handleCompositionUpdate(w, r, segment)
		})))
		mux.Handle("DELETE "+itemPath, server.authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			server.handleCompositionDelete(w, r, segment)
		})))
	}
}

func (server *Server) handleCompositionList(writer http.ResponseWriter, request *http.Request, pathSegment string) {
	kind, principal, ok := server.authorizeCompositionKind(writer, request, pathSegment, PermissionCompositionRead)
	if !ok {
		return
	}
	if !server.config.Composition.ReadEnabled {
		writeAPIError(writer, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	namespace := request.PathValue("namespace")
	limit := compositionListDefault
	if raw := strings.TrimSpace(request.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > compositionListMax {
			writeAPIError(writer, http.StatusBadRequest, "invalid_limit", "limit must be between 1 and 200")
			return
		}
		limit = parsed
	}

	list := kind.NewList()
	if err := server.runs.List(request.Context(), list, client.InNamespace(namespace)); err != nil {
		server.log.Error(err, "list composition objects", "kind", kind.Kind, "subject", principal.Subject, "namespace", namespace)
		writeAPIError(writer, http.StatusServiceUnavailable, "kubernetes_unavailable", "composition state is unavailable")
		return
	}
	items := objectListItems(list)
	sort.Slice(items, func(i, j int) bool {
		return items[i].GetName() < items[j].GetName()
	})
	if len(items) > limit {
		items = items[:limit]
	}
	response := CompositionListResponse{Items: make([]CompositionDocument, 0, len(items))}
	for _, item := range items {
		doc, err := newCompositionDocument(kind, item)
		if err != nil {
			server.log.Error(err, "encode composition object", "kind", kind.Kind, "name", item.GetName())
			writeAPIError(writer, http.StatusInternalServerError, "encode_failed", "failed to encode composition object")
			return
		}
		response.Items = append(response.Items, doc)
	}
	writeJSON(writer, http.StatusOK, response)
}

func (server *Server) handleCompositionGet(writer http.ResponseWriter, request *http.Request, pathSegment string) {
	kind, principal, ok := server.authorizeCompositionKind(writer, request, pathSegment, PermissionCompositionRead)
	if !ok {
		return
	}
	if !server.config.Composition.ReadEnabled {
		writeAPIError(writer, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	namespace := request.PathValue("namespace")
	name := request.PathValue("name")
	obj := kind.NewObject()
	if err := server.runs.Get(request.Context(), types.NamespacedName{Namespace: namespace, Name: name}, obj); err != nil {
		if apierrors.IsNotFound(err) {
			writeAPIError(writer, http.StatusNotFound, "not_found", "resource not found")
			return
		}
		server.log.Error(err, "get composition object", "kind", kind.Kind, "subject", principal.Subject, "namespace", namespace, "name", name)
		writeAPIError(writer, http.StatusServiceUnavailable, "kubernetes_unavailable", "composition state is unavailable")
		return
	}
	doc, err := newCompositionDocument(kind, obj)
	if err != nil {
		server.log.Error(err, "encode composition object", "kind", kind.Kind, "name", name)
		writeAPIError(writer, http.StatusInternalServerError, "encode_failed", "failed to encode composition object")
		return
	}
	writeJSON(writer, http.StatusOK, doc)
}

func (server *Server) handleCompositionCreate(writer http.ResponseWriter, request *http.Request, pathSegment string) {
	kind, principal, ok := server.authorizeCompositionKind(writer, request, pathSegment, PermissionCompositionWrite)
	if !ok {
		return
	}
	if !server.config.Composition.WriteEnabled {
		writeAPIError(writer, http.StatusNotFound, "composition_write_disabled", "composition write is disabled")
		return
	}
	writerClient, ok := server.writerClient(writer)
	if !ok {
		return
	}
	namespace := request.PathValue("namespace")
	body, err := readCompositionBody(request)
	if err != nil {
		writeAPIError(writer, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	name := strings.TrimSpace(body.Metadata.Name)
	if name == "" {
		writeAPIError(writer, http.StatusBadRequest, "invalid_body", "metadata.name is required")
		return
	}
	if body.Metadata.Namespace != "" && body.Metadata.Namespace != namespace {
		writeAPIError(writer, http.StatusBadRequest, "invalid_body", "metadata.namespace must match the URL namespace")
		return
	}

	obj := kind.NewObject()
	if err := decodeSpecIntoObject(kind, obj, body.Spec); err != nil {
		writeAPIError(writer, http.StatusBadRequest, "invalid", err.Error())
		return
	}
	obj.SetName(name)
	obj.SetNamespace(namespace)
	if len(body.Metadata.Labels) > 0 {
		obj.SetLabels(copyStringMap(body.Metadata.Labels))
	}
	if len(body.Metadata.Annotations) > 0 {
		obj.SetAnnotations(copyStringMap(body.Metadata.Annotations))
	}
	stampConsoleManaged(obj)

	if err := writerClient.Create(request.Context(), obj); err != nil {
		if apierrors.IsAlreadyExists(err) {
			writeAPIError(writer, http.StatusConflict, "conflict", "resource already exists")
			return
		}
		if apierrors.IsInvalid(err) || apierrors.IsBadRequest(err) {
			writeAPIError(writer, http.StatusBadRequest, "invalid", err.Error())
			return
		}
		server.log.Error(err, "create composition object", "kind", kind.Kind, "subject", principal.Subject, "namespace", namespace, "name", name)
		writeAPIError(writer, http.StatusServiceUnavailable, "kubernetes_unavailable", "composition create failed")
		return
	}
	server.log.Info("composition create",
		"subject", principal.Subject,
		"issuer", principal.Issuer,
		"namespace", namespace,
		"kind", kind.Kind,
		"name", name,
	)
	doc, err := newCompositionDocument(kind, obj)
	if err != nil {
		writeJSON(writer, http.StatusCreated, map[string]string{"status": "created"})
		return
	}
	writeJSON(writer, http.StatusCreated, doc)
}

func (server *Server) handleCompositionUpdate(writer http.ResponseWriter, request *http.Request, pathSegment string) {
	kind, principal, ok := server.authorizeCompositionKind(writer, request, pathSegment, PermissionCompositionWrite)
	if !ok {
		return
	}
	if !server.config.Composition.WriteEnabled {
		writeAPIError(writer, http.StatusNotFound, "composition_write_disabled", "composition write is disabled")
		return
	}
	writerClient, ok := server.writerClient(writer)
	if !ok {
		return
	}
	namespace := request.PathValue("namespace")
	name := request.PathValue("name")
	body, err := readCompositionBody(request)
	if err != nil {
		writeAPIError(writer, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	if body.Metadata.Name != "" && body.Metadata.Name != name {
		writeAPIError(writer, http.StatusBadRequest, "invalid_body", "metadata.name must match the URL name")
		return
	}
	if body.Metadata.Namespace != "" && body.Metadata.Namespace != namespace {
		writeAPIError(writer, http.StatusBadRequest, "invalid_body", "metadata.namespace must match the URL namespace")
		return
	}
	if strings.TrimSpace(body.Metadata.ResourceVersion) == "" {
		writeAPIError(writer, http.StatusBadRequest, "invalid_body", "metadata.resourceVersion is required for updates")
		return
	}

	current := kind.NewObject()
	if err := writerClient.Get(request.Context(), types.NamespacedName{Namespace: namespace, Name: name}, current); err != nil {
		if apierrors.IsNotFound(err) {
			writeAPIError(writer, http.StatusNotFound, "not_found", "resource not found")
			return
		}
		server.log.Error(err, "get composition object for update", "kind", kind.Kind, "subject", principal.Subject, "namespace", namespace, "name", name)
		writeAPIError(writer, http.StatusServiceUnavailable, "kubernetes_unavailable", "composition state is unavailable")
		return
	}
	management := evaluateCompositionManagement(current)
	if !management.Writable {
		writeAPIError(writer, http.StatusForbidden, management.Reason, compositionWriteBlockedMessage(management))
		return
	}
	if current.GetResourceVersion() != body.Metadata.ResourceVersion {
		writeAPIError(writer, http.StatusConflict, "conflict", "resourceVersion does not match the current object; reload and retry")
		return
	}

	if err := decodeSpecIntoObject(kind, current, body.Spec); err != nil {
		writeAPIError(writer, http.StatusBadRequest, "invalid", err.Error())
		return
	}
	if body.Metadata.Labels != nil {
		labels := copyStringMap(body.Metadata.Labels)
		labels[LabelManagedBy] = ManagedByConsole
		for key := range labels {
			if isGitOpsLabelKey(key) {
				delete(labels, key)
			}
		}
		current.SetLabels(labels)
	} else {
		stampConsoleManaged(current)
	}
	if body.Metadata.Annotations != nil {
		annotations := copyStringMap(body.Metadata.Annotations)
		for key := range annotations {
			if isGitOpsAnnotationKey(key) {
				delete(annotations, key)
			}
		}
		current.SetAnnotations(annotations)
	}
	labels := current.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	} else {
		labels = copyStringMap(labels)
	}
	labels[LabelManagedBy] = ManagedByConsole
	current.SetLabels(labels)
	current.SetResourceVersion(body.Metadata.ResourceVersion)

	if err := writerClient.Update(request.Context(), current); err != nil {
		if apierrors.IsConflict(err) {
			writeAPIError(writer, http.StatusConflict, "conflict", "resourceVersion conflict; reload and retry")
			return
		}
		if apierrors.IsInvalid(err) || apierrors.IsBadRequest(err) {
			writeAPIError(writer, http.StatusBadRequest, "invalid", err.Error())
			return
		}
		server.log.Error(err, "update composition object", "kind", kind.Kind, "subject", principal.Subject, "namespace", namespace, "name", name)
		writeAPIError(writer, http.StatusServiceUnavailable, "kubernetes_unavailable", "composition update failed")
		return
	}
	server.log.Info("composition update",
		"subject", principal.Subject,
		"issuer", principal.Issuer,
		"namespace", namespace,
		"kind", kind.Kind,
		"name", name,
	)
	doc, err := newCompositionDocument(kind, current)
	if err != nil {
		writeJSON(writer, http.StatusOK, map[string]string{"status": "updated"})
		return
	}
	writeJSON(writer, http.StatusOK, doc)
}

func (server *Server) handleCompositionDelete(writer http.ResponseWriter, request *http.Request, pathSegment string) {
	kind, principal, ok := server.authorizeCompositionKind(writer, request, pathSegment, PermissionCompositionWrite)
	if !ok {
		return
	}
	if !server.config.Composition.WriteEnabled {
		writeAPIError(writer, http.StatusNotFound, "composition_write_disabled", "composition write is disabled")
		return
	}
	writerClient, ok := server.writerClient(writer)
	if !ok {
		return
	}
	namespace := request.PathValue("namespace")
	name := request.PathValue("name")
	current := kind.NewObject()
	if err := writerClient.Get(request.Context(), types.NamespacedName{Namespace: namespace, Name: name}, current); err != nil {
		if apierrors.IsNotFound(err) {
			writeAPIError(writer, http.StatusNotFound, "not_found", "resource not found")
			return
		}
		server.log.Error(err, "get composition object for delete", "kind", kind.Kind, "subject", principal.Subject, "namespace", namespace, "name", name)
		writeAPIError(writer, http.StatusServiceUnavailable, "kubernetes_unavailable", "composition state is unavailable")
		return
	}
	management := evaluateCompositionManagement(current)
	if !management.Writable {
		writeAPIError(writer, http.StatusForbidden, management.Reason, compositionWriteBlockedMessage(management))
		return
	}
	if err := writerClient.Delete(request.Context(), current); err != nil {
		if apierrors.IsNotFound(err) {
			writeAPIError(writer, http.StatusNotFound, "not_found", "resource not found")
			return
		}
		server.log.Error(err, "delete composition object", "kind", kind.Kind, "subject", principal.Subject, "namespace", namespace, "name", name)
		writeAPIError(writer, http.StatusServiceUnavailable, "kubernetes_unavailable", "composition delete failed")
		return
	}
	server.log.Info("composition delete",
		"subject", principal.Subject,
		"issuer", principal.Issuer,
		"namespace", namespace,
		"kind", kind.Kind,
		"name", name,
	)
	writer.WriteHeader(http.StatusNoContent)
}

func (server *Server) authorizeCompositionKind(writer http.ResponseWriter, request *http.Request, pathSegment, permission string) (compositionKind, Principal, bool) {
	namespace := request.PathValue("namespace")
	principal := principalFromContext(request.Context())
	if !server.authorizer.Allowed(principal, permission, namespace) {
		writeAPIError(writer, http.StatusNotFound, "not_found", "resource not found")
		return compositionKind{}, principal, false
	}
	kind, ok := compositionKinds[pathSegment]
	if !ok {
		writeAPIError(writer, http.StatusNotFound, "not_found", "resource not found")
		return compositionKind{}, principal, false
	}
	return kind, principal, true
}

func (server *Server) writerClient(writer http.ResponseWriter) (client.Client, bool) {
	if server.writes == nil {
		writeAPIError(writer, http.StatusServiceUnavailable, "composition_write_disabled", "composition write client is not configured")
		return nil, false
	}
	return server.writes, true
}

func compositionWriteBlockedMessage(management CompositionManagement) string {
	switch management.Reason {
	case managementReasonGitOpsProtected:
		return "object is owned by GitOps (" + management.ManagedBy + "); edit the Git source of truth instead"
	case managementReasonNotConsoleManaged:
		return "object is not console-managed; only objects labeled control.anvil.hazyforge.io/managed-by=anvil-agents-console can be mutated"
	default:
		return "object is not writable through the composition API"
	}
}

func readCompositionBody(request *http.Request) (CompositionDocument, error) {
	defer request.Body.Close()
	limited := io.LimitReader(request.Body, compositionMaxBodyBytes+1)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return CompositionDocument{}, fmt.Errorf("read body: %w", err)
	}
	if len(raw) > compositionMaxBodyBytes {
		return CompositionDocument{}, fmt.Errorf("request body exceeds %d bytes", compositionMaxBodyBytes)
	}
	var body CompositionDocument
	if err := json.Unmarshal(raw, &body); err != nil {
		return CompositionDocument{}, fmt.Errorf("decode JSON body: %w", err)
	}
	if len(body.Spec) == 0 || string(body.Spec) == "null" {
		return CompositionDocument{}, fmt.Errorf("spec is required")
	}
	return body, nil
}

func newCompositionDocument(kind compositionKind, obj client.Object) (CompositionDocument, error) {
	raw, err := json.Marshal(obj)
	if err != nil {
		return CompositionDocument{}, err
	}
	var envelope struct {
		Spec   json.RawMessage `json:"spec"`
		Status json.RawMessage `json:"status"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return CompositionDocument{}, err
	}
	var created *time.Time
	if ts := obj.GetCreationTimestamp(); !ts.IsZero() {
		t := ts.Time
		created = &t
	}
	doc := CompositionDocument{
		APIVersion: agentsv1alpha1.GroupVersion.String(),
		Kind:       kind.Kind,
		Metadata: CompositionMetadata{
			Name:              obj.GetName(),
			Namespace:         obj.GetNamespace(),
			UID:               string(obj.GetUID()),
			ResourceVersion:   obj.GetResourceVersion(),
			Generation:        obj.GetGeneration(),
			CreationTimestamp: created,
			Labels:            copyStringMap(obj.GetLabels()),
			Annotations:       copyStringMap(obj.GetAnnotations()),
		},
		Spec:       envelope.Spec,
		Management: evaluateCompositionManagement(obj),
	}
	if kind.HasStatus && len(envelope.Status) > 0 && string(envelope.Status) != "null" {
		doc.Status = envelope.Status
	}
	return doc, nil
}

func decodeSpecIntoObject(kind compositionKind, obj client.Object, spec json.RawMessage) error {
	wrapper := map[string]json.RawMessage{
		"apiVersion": json.RawMessage(strconv.Quote(agentsv1alpha1.GroupVersion.String())),
		"kind":       json.RawMessage(strconv.Quote(kind.Kind)),
		"spec":       spec,
	}
	raw, err := json.Marshal(wrapper)
	if err != nil {
		return fmt.Errorf("encode spec wrapper: %w", err)
	}
	if err := json.Unmarshal(raw, obj); err != nil {
		return fmt.Errorf("decode spec into %s: %w", kind.Kind, err)
	}
	return nil
}

func objectListItems(list client.ObjectList) []client.Object {
	switch typed := list.(type) {
	case *agentsv1alpha1.AgentRunProfileList:
		out := make([]client.Object, len(typed.Items))
		for i := range typed.Items {
			out[i] = &typed.Items[i]
		}
		return out
	case *agentsv1alpha1.AgentHarnessProfileList:
		out := make([]client.Object, len(typed.Items))
		for i := range typed.Items {
			out[i] = &typed.Items[i]
		}
		return out
	case *agentsv1alpha1.AgentSkillSetList:
		out := make([]client.Object, len(typed.Items))
		for i := range typed.Items {
			out[i] = &typed.Items[i]
		}
		return out
	case *agentsv1alpha1.AgentToolSetList:
		out := make([]client.Object, len(typed.Items))
		for i := range typed.Items {
			out[i] = &typed.Items[i]
		}
		return out
	case *agentsv1alpha1.VolumeProfileList:
		out := make([]client.Object, len(typed.Items))
		for i := range typed.Items {
			out[i] = &typed.Items[i]
		}
		return out
	case *agentsv1alpha1.AgentDataVolumeList:
		out := make([]client.Object, len(typed.Items))
		for i := range typed.Items {
			out[i] = &typed.Items[i]
		}
		return out
	default:
		return nil
	}
}
