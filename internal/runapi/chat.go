package runapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	apiValidation "k8s.io/apimachinery/pkg/api/validation"

	"github.com/hazyforge/anvil-agents/internal/chat"
)

const (
	chatMaxBodyBytes = 256 * 1024
	chatListDefault  = 50
	chatListMax      = 200
)

type CreateChatThreadRequest struct {
	ProfileName string          `json:"profileName"`
	Mode        string          `json:"mode"`
	Title       string          `json:"title"`
	Metadata    json.RawMessage `json:"metadata"`
}

type AppendChatMessageRequest struct {
	Content  string          `json:"content"`
	Metadata json.RawMessage `json:"metadata"`
}

type ChatThreadListResponse struct {
	Items []chat.Thread `json:"items"`
}

type ChatThreadDetailResponse struct {
	chat.Thread
	Messages []chat.Message `json:"messages"`
}

type ChatAppendResponse struct {
	Thread    chat.Thread  `json:"thread"`
	User      chat.Message `json:"user"`
	Assistant chat.Message `json:"assistant"`
}

func (server *Server) registerChatRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/namespaces/{namespace}/chat/threads", server.authenticate(http.HandlerFunc(server.handleListChatThreads)))
	mux.Handle("POST /api/v1/namespaces/{namespace}/chat/threads", server.authenticate(http.HandlerFunc(server.handleCreateChatThread)))
	mux.Handle("GET /api/v1/namespaces/{namespace}/chat/threads/{threadID}", server.authenticate(http.HandlerFunc(server.handleGetChatThread)))
	mux.Handle("GET /api/v1/namespaces/{namespace}/chat/threads/{threadID}/messages", server.authenticate(http.HandlerFunc(server.handleListChatMessages)))
	mux.Handle("POST /api/v1/namespaces/{namespace}/chat/threads/{threadID}/messages", server.authenticate(http.HandlerFunc(server.handleAppendChatMessage)))
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
	thread, err := server.chatStore.CreateThread(request.Context(), chat.Thread{
		Namespace:   namespace,
		ProfileName: body.ProfileName,
		Mode:        body.Mode,
		Title:       body.Title,
		CreatedBy:   principal.Subject,
		Metadata:    body.Metadata,
	})
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
	writeJSON(writer, http.StatusCreated, thread)
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
	messages, err := server.chatStore.ListMessages(request.Context(), namespace, threadID)
	if err != nil {
		server.writeChatStoreError(writer, err, principal, namespace)
		return
	}
	server.log.Info("chat thread read", "subject", principal.Subject, "namespace", namespace, "thread", thread.ID)
	writeJSON(writer, http.StatusOK, ChatThreadDetailResponse{Thread: thread, Messages: messages})
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
	writeJSON(writer, http.StatusOK, map[string]any{"items": messages})
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
	user := chat.Message{
		Role:     chat.RoleUser,
		Content:  body.Content,
		Metadata: body.Metadata,
	}
	assistant := chat.Message{
		Role:     chat.RoleAssistant,
		Content:  stubAssistantContent(body.Content),
		Metadata: json.RawMessage(`{"stub":true,"engine":"echo"}`),
	}
	stored, thread, err := server.chatStore.AppendMessages(request.Context(), namespace, request.PathValue("threadID"), []chat.Message{user, assistant})
	if err != nil {
		server.writeChatStoreError(writer, err, principal, namespace)
		return
	}
	if len(stored) != 2 {
		writeAPIError(writer, http.StatusInternalServerError, "chat_unavailable", "standing-chat append returned an unexpected result")
		return
	}
	server.log.Info("chat message append",
		"subject", principal.Subject,
		"namespace", namespace,
		"thread", thread.ID,
		"sequence", stored[0].Sequence,
	)
	writeJSON(writer, http.StatusCreated, ChatAppendResponse{
		Thread:    thread,
		User:      stored[0],
		Assistant: stored[1],
	})
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
	case errors.Is(err, chat.ErrNotFound):
		writeAPIError(writer, http.StatusNotFound, "not_found", "resource not found")
	case errors.Is(err, chat.ErrInvalid):
		writeAPIError(writer, http.StatusBadRequest, "invalid", err.Error())
	default:
		server.log.Error(err, "standing-chat store", "subject", principal.Subject, "namespace", namespace)
		writeAPIError(writer, http.StatusServiceUnavailable, "chat_unavailable", "standing-chat store is unavailable")
	}
}

func stubAssistantContent(userContent string) string {
	trimmed := strings.TrimSpace(userContent)
	if trimmed == "" {
		return "(stub) standing chat storage is enabled; LangGraph execution is not wired yet."
	}
	return "(stub) standing chat storage is enabled; LangGraph execution is not wired yet.\n\nYou said:\n" + trimmed
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
