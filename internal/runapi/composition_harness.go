package runapi

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apiValidation "k8s.io/apimachinery/pkg/api/validation"
	"k8s.io/apimachinery/pkg/types"

	agentsv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

const compositionHarnessPatchMaxBodyBytes = 64 * 1024

// runProfileHarnessPatchBody is the narrow Desktop harness-switch body. Only
// harnessProfileRef changes; inline backend/execution, skills, tools, and
// scope are preserved verbatim. An empty harnessProfileName clears the ref so
// the profile falls back to its inline harness envelope.
type runProfileHarnessPatchBody struct {
	HarnessProfileName *string `json:"harnessProfileName"`
}

func (server *Server) registerRunProfileHarnessRoute(mux *http.ServeMux) {
	mux.Handle("PATCH /api/v1/namespaces/{namespace}/agent-run-profiles/{name}/harness",
		server.authenticate(http.HandlerFunc(server.handleRunProfileHarnessPatch)))
}

func (server *Server) handleRunProfileHarnessPatch(writer http.ResponseWriter, request *http.Request) {
	_, principal, ok := server.authorizeCompositionKind(writer, request, "agent-run-profiles", PermissionCompositionWrite)
	if !ok {
		return
	}
	if !server.config.Composition.ReadEnabled || !server.config.Composition.WriteEnabled {
		writeAPIError(writer, http.StatusNotFound, "composition_write_disabled", "composition write is disabled")
		return
	}
	writerClient, ok := server.writerClient(writer)
	if !ok {
		return
	}
	namespace := request.PathValue("namespace")
	name := request.PathValue("name")
	body, err := readRunProfileHarnessPatchBody(request)
	if err != nil {
		writeAPIError(writer, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	harnessName := strings.TrimSpace(*body.HarnessProfileName)
	if harnessName != "" {
		if problems := apiValidation.NameIsDNSSubdomain(harnessName, false); len(problems) > 0 {
			writeAPIError(writer, http.StatusBadRequest, "invalid", "harnessProfileName is invalid: "+strings.Join(problems, "; "))
			return
		}
		harness := &agentsv1alpha1.AgentHarnessProfile{}
		if err := writerClient.Get(request.Context(), types.NamespacedName{Namespace: namespace, Name: harnessName}, harness); err != nil {
			if apierrors.IsNotFound(err) {
				writeAPIError(writer, http.StatusBadRequest, "invalid", "harness profile is unavailable in this namespace")
				return
			}
			server.log.Error(err, "get harness profile for patch", "subject", principal.Subject, "namespace", namespace, "name", harnessName)
			writeAPIError(writer, http.StatusServiceUnavailable, "kubernetes_unavailable", "composition state is unavailable")
			return
		}
	}

	profile := &agentsv1alpha1.AgentRunProfile{}
	if err := writerClient.Get(request.Context(), types.NamespacedName{Namespace: namespace, Name: name}, profile); err != nil {
		if apierrors.IsNotFound(err) {
			writeAPIError(writer, http.StatusNotFound, "not_found", "resource not found")
			return
		}
		server.log.Error(err, "get run profile for harness patch", "subject", principal.Subject, "namespace", namespace, "name", name)
		writeAPIError(writer, http.StatusServiceUnavailable, "kubernetes_unavailable", "composition state is unavailable")
		return
	}
	management := evaluateCompositionManagement(profile)
	if !management.Writable {
		writeAPIError(writer, http.StatusForbidden, management.Reason, compositionWriteBlockedMessage(management))
		return
	}

	if harnessName == "" {
		profile.Spec.HarnessProfileRef = nil
	} else {
		profile.Spec.HarnessProfileRef = &agentsv1alpha1.NamespacedObjectReference{Name: harnessName}
	}
	if err := writerClient.Update(request.Context(), profile); err != nil {
		if apierrors.IsConflict(err) {
			writeAPIError(writer, http.StatusConflict, "conflict", "resource was modified; reload and retry")
			return
		}
		if apierrors.IsInvalid(err) || apierrors.IsBadRequest(err) {
			writeAPIError(writer, http.StatusBadRequest, "invalid", err.Error())
			return
		}
		server.log.Error(err, "patch run profile harness", "subject", principal.Subject, "namespace", namespace, "name", name)
		writeAPIError(writer, http.StatusServiceUnavailable, "kubernetes_unavailable", "composition update failed")
		return
	}
	server.log.Info("run profile harness patch",
		"subject", principal.Subject,
		"issuer", principal.Issuer,
		"namespace", namespace,
		"name", name,
		"harness", harnessName,
	)
	doc, err := newCompositionDocument(compositionKinds["agent-run-profiles"], profile)
	if err != nil {
		writeJSON(writer, http.StatusOK, map[string]string{"status": "updated"})
		return
	}
	writeJSON(writer, http.StatusOK, doc)
}

func readRunProfileHarnessPatchBody(request *http.Request) (runProfileHarnessPatchBody, error) {
	defer request.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(request.Body, compositionHarnessPatchMaxBodyBytes+1))
	if err != nil {
		return runProfileHarnessPatchBody{}, err
	}
	if len(raw) > compositionHarnessPatchMaxBodyBytes {
		return runProfileHarnessPatchBody{}, io.ErrUnexpectedEOF
	}
	var body runProfileHarnessPatchBody
	if err := json.Unmarshal(raw, &body); err != nil {
		return runProfileHarnessPatchBody{}, err
	}
	if body.HarnessProfileName == nil {
		return runProfileHarnessPatchBody{}, jsonError("harnessProfileName is required")
	}
	return body, nil
}

type jsonDecodeError string

func (err jsonDecodeError) Error() string { return string(err) }

func jsonError(message string) error { return jsonDecodeError(message) }
