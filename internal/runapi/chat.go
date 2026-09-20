package runapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	apiValidation "k8s.io/apimachinery/pkg/api/validation"

	"github.com/hazyforge/anvil-agents/internal/chat"
)

const (
	chatMaxBodyBytes = 256 * 1024
	chatListDefault  = 50
	chatListMax      = 200
)

type CreateChatThreadRequest struct {
	Standing           bool            `json:"standing,omitempty"`
	HarnessProfileName string          `json:"harnessProfileName,omitempty"`
	ProfileName        string          `json:"profileName"`
	Mode               string          `json:"mode"`
	Title              string          `json:"title"`
	Metadata           json.RawMessage `json:"metadata"`
}

type AppendChatMessageRequest struct {
	RequestID string          `json:"requestId,omitempty"`
	Content   string          `json:"content"`
	Metadata  json.RawMessage `json:"metadata"`
}

type ChatThreadListResponse struct {
	Items []chat.Thread `json:"items"`
}

type ChatThreadDetailResponse struct {
	chat.Thread
	Messages   []chat.Message `json:"messages"`
	Turns      []chat.Turn    `json:"turns"`
	ActiveTurn *chat.Turn     `json:"activeTurn,omitempty"`
	// RecoveryPending means execution progress could not be refreshed. The
	// returned transcript and turns are durable records, not a failed execution.
	RecoveryPending bool `json:"recoveryPending,omitempty"`
}

type ChatAppendResponse struct {
	Thread chat.Thread  `json:"thread"`
	User   chat.Message `json:"user"`
	Turn   chat.Turn    `json:"turn"`
}

func (server *Server) registerChatRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/namespaces/{namespace}/chat/threads", server.authenticate(http.HandlerFunc(server.handleListChatThreads)))
	mux.Handle("POST /api/v1/namespaces/{namespace}/chat/threads", server.authenticate(http.HandlerFunc(server.handleCreateChatThread)))
	mux.Handle("GET /api/v1/namespaces/{namespace}/chat/threads/{threadID}", server.authenticate(http.HandlerFunc(server.handleGetChatThread)))
	mux.Handle("GET /api/v1/namespaces/{namespace}/chat/threads/{threadID}/messages", server.authenticate(http.HandlerFunc(server.handleListChatMessages)))
	mux.Handle("POST /api/v1/namespaces/{namespace}/chat/threads/{threadID}/messages", server.authenticate(http.HandlerFunc(server.handleAppendChatMessage)))
	// Slice-1 standing stream: same snapshot + terminal events over SSE or a
	// WebSocket upgrade. Browser WebSocket clients cannot set Authorization,
	// so this route authenticates itself (header bearer or verified bearer
	// subprotocol) instead of using the shared authenticate middleware.
	mux.HandleFunc("GET /api/v1/namespaces/{namespace}/chat/threads/{threadID}/stream", server.handleChatThreadStream)
}

func (server *Server) SetChatStore(store chat.Store) {
	if server == nil {
		return
	}
	server.chatStore = store
}

func (server *Server) handleListChatThreads(writer http.ResponseWriter, request *http.Request) {
	namespace, principal, ok := server.authorizeChat(writer, request, PermissionChatRead)
	if !ok {
		return
	}
	limit := chatListDefault
	if raw := strings.TrimSpace(request.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > chatListMax {
			writeAPIError(writer, http.StatusBadRequest, "invalid_limit", "limit must be between 1 and 200")
			return
		}
		limit = parsed
	}
	threads, err := server.chatStore.ListThreads(request.Context(), chat.ThreadFilter{
		Namespace:   namespace,
		ProfileName: strings.TrimSpace(request.URL.Query().Get("profileName")),
		Mode:        strings.TrimSpace(request.URL.Query().Get("mode")),
		Limit:       limit,
	})
	if err != nil {
		server.writeChatStoreError(writer, err, principal, namespace)
		return
	}
	server.log.Info("chat threads list", "subject", principal.Subject, "namespace", namespace, "count", len(threads))
	writeJSON(writer, http.StatusOK, ChatThreadListResponse{Items: threads})
}

func (server *Server) handleCreateChatThread(writer http.ResponseWriter, request *http.Request) {
	namespace, principal, ok := server.authorizeChat(writer, request, PermissionChatWrite)
	if !ok {
		return
	}
	body, err := readJSONBody[CreateChatThreadRequest](request, chatMaxBodyBytes)
	if err != nil {
		writeAPIError(writer, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	if body.Standing && (strings.TrimSpace(body.ProfileName) == "" || strings.TrimSpace(body.HarnessProfileName) != "" || len(body.Metadata) > 0) {
		writeAPIError(writer, http.StatusBadRequest, "invalid_standing_target", "standing conversations require profileName and use the agent's existing identity and configured harness")
		return
	}
	// Ensuring may return someone else's existing namespace-visible thread;
	// preserve its read permission as well as the create permission above.
	if body.Standing && !server.authorizer.Allowed(principal, PermissionChatRead, namespace) {
		writeAPIError(writer, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	if err := server.validateChatTarget(request.Context(), namespace, body.ProfileName, body.HarnessProfileName); err != nil {
		writeAPIError(writer, http.StatusBadRequest, "invalid_target", err.Error())
		return
	}
	// Execution selectors are server-controlled metadata. Caller metadata cannot
	// replace them or turn a normal thread into a privileged manager.
	metadata := map[string]any{}
	if len(body.Metadata) > 0 {
		if err := json.Unmarshal(body.Metadata, &metadata); err != nil || metadata == nil {
			writeAPIError(writer, http.StatusBadRequest, "invalid_metadata", "metadata must be an object")
			return
		}
	}
	delete(metadata, "harnessProfileName")
	delete(metadata, "applicationName")
	delete(metadata, "sourceTurnId")
	delete(metadata, "sourceThreadId")
	delete(metadata, "sourceProfileName")
	if body.HarnessProfileName != "" {
		metadata["harnessProfileName"] = strings.TrimSpace(body.HarnessProfileName)
	}
	body.Metadata, _ = json.Marshal(metadata)
	application, err := server.resolveChatApplication(request.Context(), chat.Thread{Namespace: namespace, ProfileName: body.ProfileName, Metadata: body.Metadata})
	if err != nil {
		writeAPIError(writer, http.StatusBadRequest, "invalid_target", err.Error())
		return
	}
	if application != "" {
		metadata["applicationName"] = application
		body.Metadata, _ = json.Marshal(metadata)
	}
	if err := server.validateCoordination(request.Context(), chat.Thread{Namespace: namespace, ProfileName: body.ProfileName, Metadata: body.Metadata}); err != nil {
		writeAPIError(writer, http.StatusBadRequest, "invalid_coordination", err.Error())
		return
	}
	seed := chat.Thread{
		Namespace:   namespace,
		ProfileName: body.ProfileName,
		Mode:        body.Mode,
		Title:       body.Title,
		CreatedBy:   principal.Subject,
		Metadata:    body.Metadata,
	}
	var thread chat.Thread
	created := true
	if body.Standing {
		thread, created, err = server.chatStore.EnsureStandingThread(request.Context(), seed)
	} else {
		thread, err = server.chatStore.CreateThread(request.Context(), seed)
	}
	if err != nil {
		server.writeChatStoreError(writer, err, principal, namespace)
		return
	}
	server.log.Info("chat thread create",
		"subject", principal.Subject,
		"namespace", namespace,
		"thread", thread.ID,
		"profile", thread.ProfileName,
		"mode", thread.Mode,
	)
	status := http.StatusCreated
	if !created {
		status = http.StatusOK
	}
	writeJSON(writer, status, thread)
}

func (server *Server) handleGetChatThread(writer http.ResponseWriter, request *http.Request) {
	namespace, principal, ok := server.authorizeChat(writer, request, PermissionChatRead)
	if !ok {
		return
	}
	threadID := request.PathValue("threadID")
	thread, err := server.chatStore.GetThread(request.Context(), namespace, threadID)
	if err != nil {
		server.writeChatStoreError(writer, err, principal, namespace)
		return
	}
	// Observe-only refresh: complete already-Succeeded runs and report a
	// live standing claim as running, but never StreamTurn. Desktop polls
	// this GET every few seconds with a 3s budget; driving grok here is
	// what logged `harness "grokBuild" timed out: context deadline exceeded`
	// and killed the in-flight standing process.
	recoveryCtx, cancel := context.WithTimeout(request.Context(), 3*time.Second)
	turns, err := server.reconcileChatThreadMode(recoveryCtx, namespace, threadID, false)
	cancel()
	recoveryPending := err != nil
	if err != nil {
		// A Kubernetes outage must not hide a healthy persisted conversation or
		// pretend to complete its execution. Background recovery retains the
		// original run identity and resource locks until a terminal receipt.
		server.log.Error(err, "chat execution refresh unavailable", "namespace", namespace, "thread", threadID)
		turns, err = server.chatStore.ListTurns(request.Context(), namespace, threadID)
		if err != nil {
			server.writeChatStoreError(writer, err, principal, namespace)
			return
		}
	}
	messages, err := server.chatStore.ListMessages(request.Context(), namespace, threadID)
	if err != nil {
		server.writeChatStoreError(writer, err, principal, namespace)
		return
	}
	if !recoveryPending && server.authorizer.Allowed(principal, PermissionRunsRead, namespace) {
		server.enrichChatFailureView(request.Context(), namespace, turns, messages)
		server.enrichOpenClawReplyView(request.Context(), namespace, turns, messages)
	}
	server.log.Info("chat thread read", "subject", principal.Subject, "namespace", namespace, "thread", thread.ID)
	response := ChatThreadDetailResponse{Thread: thread, Messages: safeChatMessages(messages), Turns: turns, RecoveryPending: recoveryPending}
	for i := range turns {
		if chat.Active(turns[i]) {
			response.ActiveTurn = &turns[i]
			break
		}
	}
	writeJSON(writer, http.StatusOK, response)
}

func (server *Server) handleListChatMessages(writer http.ResponseWriter, request *http.Request) {
	namespace, principal, ok := server.authorizeChat(writer, request, PermissionChatRead)
	if !ok {
		return
	}
	messages, err := server.chatStore.ListMessages(request.Context(), namespace, request.PathValue("threadID"))
	if err != nil {
		server.writeChatStoreError(writer, err, principal, namespace)
		return
	}
	if server.authorizer.Allowed(principal, PermissionRunsRead, namespace) {
		turns, turnErr := server.chatStore.ListTurns(request.Context(), namespace, request.PathValue("threadID"))
		if turnErr == nil {
			server.enrichOpenClawReplyView(request.Context(), namespace, turns, messages)
		}
	}
	writeJSON(writer, http.StatusOK, map[string]any{"items": safeChatMessages(messages)})
}

func (server *Server) handleAppendChatMessage(writer http.ResponseWriter, request *http.Request) {
	namespace, principal, ok := server.authorizeChat(writer, request, PermissionChatWrite)
	if !ok {
		return
	}
	body, err := readJSONBody[AppendChatMessageRequest](request, chatMaxBodyBytes)
	if err != nil {
		writeAPIError(writer, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	if !server.config.Runs.CreateEnabled || !server.authorizer.Allowed(principal, PermissionRunsCreate, namespace) {
		writeAPIError(writer, http.StatusNotFound, "runs_create_disabled", "chat execution requires AgentRun create permission")
		return
	}
	thread, err := server.chatStore.GetThread(request.Context(), namespace, request.PathValue("threadID"))
	if err != nil {
		server.writeChatStoreError(writer, err, principal, namespace)
		return
	}
	if coordinationConfig(thread).Enabled && !server.authorizer.Allowed(principal, PermissionRunsRead, namespace) {
		writeAPIError(writer, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	if _, ok := server.writerClient(writer); !ok {
		return
	}
	response, err := server.queueChatTurn(request.Context(), namespace, request.PathValue("threadID"), body)
	if err != nil {
		server.writeChatStoreError(writer, err, principal, namespace)
		return
	}
	writeJSON(writer, http.StatusAccepted, response)
}

func (server *Server) authorizeChat(writer http.ResponseWriter, request *http.Request, permission string) (string, Principal, bool) {
	namespace := request.PathValue("namespace")
	principal := principalFromContext(request.Context())
	if !server.config.Chat.Enabled {
		writeAPIError(writer, http.StatusNotFound, "chat_disabled", "standing chat is disabled")
		return "", principal, false
	}
	if problems := apiValidation.NameIsDNSSubdomain(namespace, false); len(problems) > 0 {
		writeAPIError(writer, http.StatusNotFound, "not_found", "resource not found")
		return "", principal, false
	}
	if !server.authorizer.Allowed(principal, permission, namespace) {
		writeAPIError(writer, http.StatusNotFound, "not_found", "resource not found")
		return "", principal, false
	}
	if server.chatStore == nil {
		writeAPIError(writer, http.StatusServiceUnavailable, "chat_unavailable", "standing-chat store is unavailable")
		return "", principal, false
	}
	return namespace, principal, true
}

func (server *Server) writeChatStoreError(writer http.ResponseWriter, err error, principal Principal, namespace string) {
	switch {
	case errors.Is(err, chat.ErrConversationChanged):
		writeAPIError(writer, http.StatusConflict, "conversation_changed", err.Error())
	case errors.Is(err, chat.ErrTurnActive):
		writeAPIError(writer, http.StatusConflict, "turn_active", err.Error())
	case errors.Is(err, chat.ErrRequestConflict):
		writeAPIError(writer, http.StatusConflict, "request_conflict", err.Error())
	case errors.Is(err, chat.ErrNotFound):
		writeAPIError(writer, http.StatusNotFound, "not_found", "resource not found")
	case errors.Is(err, chat.ErrInvalid):
		writeAPIError(writer, http.StatusBadRequest, "invalid", err.Error())
	default:
		server.log.Error(err, "standing-chat store", "subject", principal.Subject, "namespace", namespace)
		writeAPIError(writer, http.StatusServiceUnavailable, "chat_unavailable", "standing-chat store is unavailable")
	}
}

func readJSONBody[T any](request *http.Request, maxBytes int) (T, error) {
	var body T
	defer request.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(request.Body, int64(maxBytes)+1))
	if err != nil {
		return body, fmt.Errorf("read body: %w", err)
	}
	if len(raw) > maxBytes {
		return body, fmt.Errorf("request body exceeds %d bytes", maxBytes)
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return body, fmt.Errorf("decode JSON body")
	}
	return body, nil
}
