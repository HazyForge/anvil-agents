package runapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/types"

	agentsv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
	"github.com/hazyforge/anvil-agents/internal/chat"
	"github.com/hazyforge/anvil-agents/internal/standing"
	"github.com/hazyforge/anvil-agents/internal/substrate"
)

// Slice-1 standing chat stream contract, extended by slice 3 with a
// long-lived WebSocket token subscription.
//
// GET /api/v1/namespaces/{namespace}/chat/threads/{threadID}/stream delivers
// the same events over two transports:
//
//   - Server-Sent Events (default): text/event-stream with snapshot +
//     terminal, then close. SSE stays the fallback and never subscribes to
//     live tokens.
//   - WebSocket: request Upgrade: websocket and the server answers 101, then
//     sends the snapshot JSON text frame. Job-plane threads (and every
//     gate-off read) follow with the terminal frame and close, byte-identical
//     to slice 1. Standing (InProcess and SubstrateActor) threads keep the connection open past
//     the snapshot and multiplex live `token` frames for the thread's turns,
//     then send the same terminal frame and close.
//
// Both transports are read-only views of durable state: the endpoint never
// creates AgentRuns, never consumes model, and never stores tokens. It proves
// the upgrade path, the bearer-subprotocol auth browsers need (browsers
// cannot set Authorization on a WebSocket), and that a standing process owns
// the thread's harness session across turns (the snapshot carries the resumed
// session). Slice 3 adds live token frames into open standing streams; the
// durable turn record stays the source of truth, so the snapshot tail already
// carries the full reply even when a live frame drops.
//
// Auth: bearer access token in the Authorization header, or (WebSocket only)
// a bearer subprotocol of the form `bearer.<token>` alongside the selected
// `anvil-agents.chat-stream.v1` protocol. Access tokens in query strings are
// rejected like every other API route. Browser cross-origin handshakes keep
// the exact-origin CORS enforcement from the shared middleware.
const (
	chatStreamProtocol     = "anvil-agents.chat-stream.v1"
	chatStreamBearerPrefix = "bearer."

	chatStreamSnapshotTailDefault = 50
	chatStreamSnapshotTailMax     = 200
)

// SetStandingBackend attaches the standing in-process harness backend used to
// resume a thread's session for the stream snapshot. Nil (the default) keeps
// the endpoint transport-only: every thread terminates with job_plane.
func (server *Server) SetStandingBackend(backend standing.Backend) {
	if server == nil {
		return
	}
	server.standing = backend
}

type chatStreamStandingView struct {
	SessionName string `json:"sessionName"`
	Harness     string `json:"harness"`
	Warm        bool   `json:"warm"`
	Resumes     int    `json:"resumes"`
}

type chatStreamSnapshot struct {
	Type         string                  `json:"type"`
	Thread       chat.Thread             `json:"thread"`
	Messages     []chat.Message          `json:"messages"`
	MessageTotal int                     `json:"messageTotal"`
	Turns        []chat.Turn             `json:"turns"`
	ActiveTurn   *chat.Turn              `json:"activeTurn,omitempty"`
	Standing     *chatStreamStandingView `json:"standing,omitempty"`
}

type chatStreamTerminal struct {
	Type    string `json:"type"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

// chatStreamTokenFrame is one live token frame multiplexed into an open
// standing WebSocket stream. ThreadID/TurnID/RunName bind the token to the
// durable turn identity (runName is stamped by standingRunSink); Seq orders
// tokens within the turn and the final frame carries Done. Frames are built
// with structured marshaling only, never string concatenation.
type chatStreamTokenFrame struct {
	Type     string `json:"type"`
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
	RunName  string `json:"runName,omitempty"`
	Seq      int    `json:"seq"`
	Token    string `json:"token"`
	Done     bool   `json:"done"`
}

func (server *Server) handleChatThreadStream(writer http.ResponseWriter, request *http.Request) {
	if request.URL.Query().Has("access_token") {
		writeAPIError(writer, http.StatusBadRequest, "query_token_rejected", "access tokens are accepted only in the Authorization header")
		return
	}
	principal, protocol, request, ok := server.authenticateChatStream(writer, request)
	if !ok {
		return
	}
	namespace, _, ok := server.authorizeChat(writer, request, PermissionChatRead)
	if !ok {
		return
	}
	release, ok := server.limiter.acquire(principal.Subject)
	if !ok {
		writer.Header().Set("Retry-After", "5")
		writeAPIError(writer, http.StatusTooManyRequests, "stream_limit", "too many active streams")
		return
	}
	defer release()

	threadID := request.PathValue("threadID")
	thread, err := server.chatStore.GetThread(request.Context(), namespace, threadID)
	if err != nil {
		server.writeChatStoreError(writer, err, principal, namespace)
		return
	}
	tail, err := chatStreamSnapshotTail(request)
	if err != nil {
		writeAPIError(writer, http.StatusBadRequest, "invalid_messages", err.Error())
		return
	}
	messages, err := server.chatStore.ListMessages(request.Context(), namespace, threadID)
	if err != nil {
		server.writeChatStoreError(writer, err, principal, namespace)
		return
	}
	turns, err := server.chatStore.ListTurns(request.Context(), namespace, threadID)
	if err != nil {
		server.writeChatStoreError(writer, err, principal, namespace)
		return
	}
	snapshot := chatStreamSnapshot{Type: "snapshot", Thread: thread, MessageTotal: len(messages), Turns: turns}
	messages = safeChatMessages(messages)
	if len(messages) > tail {
		messages = messages[len(messages)-tail:]
	}
	snapshot.Messages = messages
	for i := range turns {
		if chat.Active(turns[i]) {
			snapshot.ActiveTurn = &turns[i]
			break
		}
	}
	terminal := chatStreamTerminal{Type: "terminal", Code: "job_plane",
		Message: "thread stays on the Job plane; use the AgentRun events stream for live output"}
	if view, resumed := server.resumeChatStreamSession(request.Context(), thread); resumed {
		snapshot.Standing = view
		terminal.Code = "standing_ready"
		terminal.Message = "standing session resumed for this thread"
	}
	server.log.Info("chat thread stream", "subject", principal.Subject, "namespace", namespace, "thread", thread.ID, "terminal", terminal.Code)

	if isChatStreamUpgrade(request) {
		server.serveChatStreamWebSocket(writer, request, protocol, namespace, thread, snapshot, terminal)
		return
	}
	server.serveChatStreamSSE(writer, snapshot, terminal)
}

// resumeChatStreamSession binds the thread to its standing session through
// the same path every turn will use (EnsureTurnSession): the parent thread
// owns one session, and each durable peer child thread owns its own, so peer
// cooperation keeps working with the same turn path. It reports false when no
// backend is configured, when the thread's harness stays on the Job (or
// SubstrateActor) plane, or when the resume fails; failures only downgrade
// the terminal code, they never fail the stream.
func (server *Server) resumeChatStreamSession(ctx context.Context, thread chat.Thread) (*chatStreamStandingView, bool) {
	if server.standing == nil {
		return nil, false
	}
	spec, ok := server.standingSessionSpec(ctx, thread)
	if !ok {
		return nil, false
	}
	handle, warm, err := standing.EnsureTurnSession(ctx, server.standing, spec)
	if err != nil {
		server.log.Error(err, "resume standing session for stream", "namespace", thread.Namespace, "thread", thread.ID)
		return nil, false
	}
	return &chatStreamStandingView{SessionName: handle.SessionName, Harness: handle.HarnessKind, Warm: warm || handle.Warm, Resumes: handle.Resumes}, true
}

// standingSessionSpec resolves the thread's harness selection to a standing
// session. Threads whose harness profile selects InProcess or SubstrateActor
// are eligible; Job threads, profile-inline harnesses, and unresolvable
// selections stay on their existing planes.
func (server *Server) standingSessionSpec(ctx context.Context, thread chat.Thread) (standing.SessionSpec, bool) {
	harnessName := chatHarness(thread)
	if harnessName == "" && thread.ProfileName != "" {
		profile := &agentsv1alpha1.AgentRunProfile{}
		if err := server.runs.Get(ctx, types.NamespacedName{Namespace: thread.Namespace, Name: thread.ProfileName}, profile); err != nil {
			return standing.SessionSpec{}, false
		}
		if profile.Spec.HarnessProfileRef != nil {
			harnessName = profile.Spec.HarnessProfileRef.Name
		}
	}
	if strings.TrimSpace(harnessName) == "" {
		return standing.SessionSpec{}, false
	}
	harness := &agentsv1alpha1.AgentHarnessProfile{}
	if err := server.runs.Get(ctx, types.NamespacedName{Namespace: thread.Namespace, Name: strings.TrimSpace(harnessName)}, harness); err != nil {
		return standing.SessionSpec{}, false
	}
	if !harness.Spec.Execution.UsesStandingChat() {
		return standing.SessionSpec{}, false
	}
	actorClass := ""
	if harness.Spec.Execution.Substrate != nil {
		actorClass = harness.Spec.Execution.Substrate.ActorClass
	}
	if server != nil && substrate.GenerateEnabled(true, server.config.Standing.GenerateActorClasses, actorClass) {
		return standing.SessionSpec{}, false
	}
	return standing.SessionSpec{
		Namespace:   thread.Namespace,
		ThreadID:    thread.ID,
		SessionName: standing.SessionNameForThread(thread.ID),
		HarnessKind: string(harness.Spec.Backend.Kind),
		Runtime:     string(harness.Spec.Execution.EffectiveRuntime()),
	}, true
}

func (server *Server) serveChatStreamSSE(writer http.ResponseWriter, snapshot chatStreamSnapshot, terminal chatStreamTerminal) {
	flusher, ok := writer.(http.Flusher)
	if !ok {
		writeAPIError(writer, http.StatusInternalServerError, "stream_unsupported", "HTTP streaming is unavailable")
		return
	}
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-store, no-transform")
	writer.Header().Set("Connection", "keep-alive")
	writer.Header().Set("X-Accel-Buffering", "no")
	writer.WriteHeader(http.StatusOK)
	flusher.Flush()
	sse := newSSEWriter(writer, flusher)
	_ = sse.write("snapshot", "snapshot", snapshot)
	_ = sse.write("terminal", "terminal", terminal)
}

func (server *Server) serveChatStreamWebSocket(writer http.ResponseWriter, request *http.Request, protocol string, namespace string, thread chat.Thread, snapshot chatStreamSnapshot, terminal chatStreamTerminal) {
	snapshotRaw, err := json.Marshal(snapshot)
	if err != nil {
		writeAPIError(writer, http.StatusInternalServerError, "stream_unavailable", "chat stream snapshot is unavailable")
		return
	}
	terminalRaw, err := json.Marshal(terminal)
	if err != nil {
		writeAPIError(writer, http.StatusInternalServerError, "stream_unavailable", "chat stream snapshot is unavailable")
		return
	}
	// Subscribe before the upgrade so tokens published while the handshake
	// completes still reach this stream. Job-plane threads never subscribe:
	// they keep the slice-1 snapshot + terminal + close lifecycle
	// byte-identical, and the existing AgentRun events stream stays their
	// live path.
	live := snapshot.Standing != nil && terminal.Code == "standing_ready"
	var events <-chan standing.TokenEvent
	var unsubscribe func()
	if live {
		events, unsubscribe = server.standingHub.subscribe(namespace, thread.ID)
		defer unsubscribe()
	}
	conn, err := standing.Upgrade(writer, request, protocol)
	if err != nil {
		writeAPIError(writer, http.StatusBadRequest, "websocket_rejected", "WebSocket upgrade was rejected")
		return
	}
	defer conn.Close()
	if err := conn.WriteText(snapshotRaw); err != nil {
		return
	}
	if !live {
		_ = conn.WriteText(terminalRaw)
		// Job-plane and gate-off reads close after the terminal frame.
		// Client messages are not part of the contract; the deferred close
		// ends the stream after the terminal frame.
		return
	}
	server.serveChatStreamLive(request, conn, namespace, thread.ID, events, terminalRaw)
}

// serveChatStreamLive multiplexes stamped token frames into an open standing
// stream after the snapshot, then sends the terminal frame and closes. The
// durable turn record stays the source of truth: a slow reader drops live
// frames (see the hub) and still reads the full reply from the snapshot tail
// on its next open. Client messages are not part of the contract; the reader
// below only watches for close so a departing client releases the stream.
func (server *Server) serveChatStreamLive(request *http.Request, conn *standing.Conn, namespace, threadID string, events <-chan standing.TokenEvent, terminalRaw []byte) {
	ctx := request.Context()
	clientClosed := make(chan struct{})
	go func() {
		defer close(clientClosed)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()
	maxDuration := server.config.Stream.MaxDuration.Duration
	if maxDuration <= 0 {
		maxDuration = 15 * time.Minute
	}
	deadline := time.NewTimer(maxDuration)
	defer deadline.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-clientClosed:
			return
		case <-deadline.C:
			_ = conn.WriteText(terminalRaw)
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			if event.ThreadID != threadID {
				continue
			}
			frame, err := json.Marshal(chatStreamTokenFrame{
				Type:     "token",
				ThreadID: event.ThreadID,
				TurnID:   event.TurnID,
				RunName:  event.RunName,
				Seq:      event.Seq,
				Token:    event.Token,
				Done:     event.Done,
			})
			if err != nil {
				continue
			}
			if err := conn.WriteText(frame); err != nil {
				return
			}
			if event.Done {
				_ = conn.WriteText(terminalRaw)
				return
			}
		}
	}
}

// authenticateChatStream verifies the bearer token for the stream endpoint.
// Non-browser clients use the Authorization header like every other route.
// Browser WebSocket clients cannot set headers, so a WebSocket upgrade may
// instead offer the versioned stream protocol plus a bearer subprotocol of
// the form `bearer.<token>`; the verified versioned protocol is selected.
// The returned request carries the principal for authorizeChat.
func (server *Server) authenticateChatStream(writer http.ResponseWriter, request *http.Request) (Principal, string, *http.Request, bool) {
	if raw, ok := bearerToken(request.Header.Values("Authorization")); ok {
		principal, err := server.authenticator.Verify(request.Context(), raw)
		if err != nil {
			if isAuthUnavailable(err) {
				writeAPIError(writer, http.StatusServiceUnavailable, "authentication_unavailable", "OIDC verification is temporarily unavailable")
				return Principal{}, "", nil, false
			}
			writer.Header().Set("WWW-Authenticate", `Bearer realm="anvil-agents"`)
			writeAPIError(writer, http.StatusUnauthorized, "unauthorized", "the bearer access token is invalid")
			return Principal{}, "", nil, false
		}
		return principal, "", request.WithContext(context.WithValue(request.Context(), principalContextKey{}, principal)), true
	}
	if isChatStreamUpgrade(request) {
		token, offered := chatStreamProtocols(request)
		if !offered {
			writeAPIError(writer, http.StatusUnauthorized, "unauthorized", "the WebSocket stream requires the chat-stream subprotocol")
			return Principal{}, "", nil, false
		}
		if strings.TrimSpace(token) == "" {
			writer.Header().Set("WWW-Authenticate", `Bearer realm="anvil-agents"`)
			writeAPIError(writer, http.StatusUnauthorized, "unauthorized", "a bearer access token is required")
			return Principal{}, "", nil, false
		}
		principal, err := server.authenticator.Verify(request.Context(), token)
		if err != nil {
			if isAuthUnavailable(err) {
				writeAPIError(writer, http.StatusServiceUnavailable, "authentication_unavailable", "OIDC verification is temporarily unavailable")
				return Principal{}, "", nil, false
			}
			writer.Header().Set("WWW-Authenticate", `Bearer realm="anvil-agents"`)
			writeAPIError(writer, http.StatusUnauthorized, "unauthorized", "the bearer access token is invalid")
			return Principal{}, "", nil, false
		}
		return principal, chatStreamProtocol, request.WithContext(context.WithValue(request.Context(), principalContextKey{}, principal)), true
	}
	writer.Header().Set("WWW-Authenticate", `Bearer realm="anvil-agents"`)
	writeAPIError(writer, http.StatusUnauthorized, "unauthorized", "a bearer access token is required")
	return Principal{}, "", nil, false
}

// chatStreamProtocols scans the offered WebSocket subprotocols for the
// versioned stream protocol and a bearer token carrier. The token is
// verified, never logged, and never reflected beyond the selected protocol.
func chatStreamProtocols(request *http.Request) (token string, offered bool) {
	for _, part := range strings.Split(request.Header.Get("Sec-WebSocket-Protocol"), ",") {
		candidate := strings.TrimSpace(part)
		if candidate == chatStreamProtocol {
			offered = true
			continue
		}
		if strings.HasPrefix(candidate, chatStreamBearerPrefix) {
			if token == "" {
				token = strings.TrimSpace(strings.TrimPrefix(candidate, chatStreamBearerPrefix))
			}
		}
	}
	return token, offered
}

func isChatStreamUpgrade(request *http.Request) bool {
	return headerIsToken(request.Header.Get("Upgrade"), "websocket") &&
		headerIsToken(request.Header.Get("Connection"), "upgrade")
}

func headerIsToken(header, token string) bool {
	for _, part := range strings.Split(header, ",") {
		if strings.EqualFold(strings.TrimSpace(part), token) {
			return true
		}
	}
	return false
}

func chatStreamSnapshotTail(request *http.Request) (int, error) {
	tail := chatStreamSnapshotTailDefault
	if raw := strings.TrimSpace(request.URL.Query().Get("messages")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 || parsed > chatStreamSnapshotTailMax {
			return 0, &chatStreamTailError{max: chatStreamSnapshotTailMax}
		}
		tail = parsed
	}
	return tail, nil
}

type chatStreamTailError struct {
	max int
}

func (err *chatStreamTailError) Error() string {
	return "messages must be between 0 and " + strconv.Itoa(err.max)
}

func isAuthUnavailable(err error) bool {
	return errors.Is(err, ErrAuthenticationUnavailable)
}
