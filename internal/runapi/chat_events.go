package runapi

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hazyforge/anvil-agents/internal/chat"
)

// Standing-chat change events.
//
// GET /api/v1/namespaces/{namespace}/chat/events is a read-only Server-Sent
// Events stream that tells an open client which threads changed, so it can
// re-read them through the normal authorized thread endpoints as soon as a
// peer agent, another person, or a finished run writes to them, instead of
// re-reading every open thread on a fixed timer.
//
// Events carry identifiers only (thread, turn, reason), never message
// content, metadata, or run output. They are scoped to one namespace and
// need the same chat read permission as GET .../chat/threads, which already
// lists every thread in that namespace to the reader.
//
// Like the standing token hub, the hub is process-local and sees the writes
// this API process makes (the chart runs one replica). Clients keep a slow
// safety-net poll, and a `resync` event tells a reader that a burst overflowed
// its buffer so it should re-read what it shows.
const chatEventHubBuffer = 64

const (
	chatEventReasonCreated = "created"
	chatEventReasonMessage = "message"
	chatEventReasonTurn    = "turn"
)

type chatThreadEvent struct {
	Type     string    `json:"type"`
	ThreadID string    `json:"threadId"`
	Reason   string    `json:"reason"`
	TurnID   string    `json:"turnId,omitempty"`
	At       time.Time `json:"at"`
}

type chatEventsNotice struct {
	Type      string `json:"type"`
	Namespace string `json:"namespace,omitempty"`
	Code      string `json:"code,omitempty"`
	Message   string `json:"message,omitempty"`
}

type chatEventSubscriber struct {
	events  chan chatThreadEvent
	dropped atomic.Bool
}

// chatEventHub fans thread change events out to open namespace streams.
type chatEventHub struct {
	mu   sync.Mutex
	subs map[string]map[*chatEventSubscriber]struct{}
}

func newChatEventHub() *chatEventHub {
	return &chatEventHub{subs: map[string]map[*chatEventSubscriber]struct{}{}}
}

// subscribe attaches a buffered receiver for one namespace. The caller must
// call the returned unsubscribe function.
func (hub *chatEventHub) subscribe(namespace string) (*chatEventSubscriber, func()) {
	subscriber := &chatEventSubscriber{events: make(chan chatThreadEvent, chatEventHubBuffer)}
	if hub == nil {
		return subscriber, func() {}
	}
	hub.mu.Lock()
	if hub.subs[namespace] == nil {
		hub.subs[namespace] = map[*chatEventSubscriber]struct{}{}
	}
	hub.subs[namespace][subscriber] = struct{}{}
	hub.mu.Unlock()
	var once sync.Once
	return subscriber, func() {
		once.Do(func() {
			hub.mu.Lock()
			defer hub.mu.Unlock()
			if live := hub.subs[namespace]; live != nil {
				delete(live, subscriber)
				if len(live) == 0 {
					delete(hub.subs, namespace)
				}
			}
		})
	}
}

// publish never blocks a chat write: a full subscriber buffer drops the event
// and marks the subscriber so its stream sends `resync` instead.
func (hub *chatEventHub) publish(namespace string, event chatThreadEvent) {
	if hub == nil || namespace == "" || event.ThreadID == "" {
		return
	}
	event.Type = "thread"
	if event.At.IsZero() {
		event.At = time.Now().UTC()
	}
	hub.mu.Lock()
	targets := make([]*chatEventSubscriber, 0, len(hub.subs[namespace]))
	for subscriber := range hub.subs[namespace] {
		targets = append(targets, subscriber)
	}
	hub.mu.Unlock()
	for _, subscriber := range targets {
		select {
		case subscriber.events <- event:
		default:
			subscriber.dropped.Store(true)
		}
	}
}

// subscriberCount reports live subscribers for one namespace. Tests only.
func (hub *chatEventHub) subscriberCount(namespace string) int {
	if hub == nil {
		return 0
	}
	hub.mu.Lock()
	defer hub.mu.Unlock()
	return len(hub.subs[namespace])
}

// notifyingChatStore publishes a change event after every successful chat
// write, so each write path (HTTP handlers, standing turns, background
// recovery, coordination fan-out) is covered without touching its call site.
type notifyingChatStore struct {
	chat.Store
	hub *chatEventHub
}

func (store notifyingChatStore) notify(namespace, threadID, reason, turnID string) {
	store.hub.publish(namespace, chatThreadEvent{ThreadID: threadID, Reason: reason, TurnID: turnID})
}

func (store notifyingChatStore) QueueTurn(ctx context.Context, turn chat.Turn, message chat.Message) (chat.Turn, chat.Message, chat.Thread, error) {
	queued, user, thread, err := store.Store.QueueTurn(ctx, turn, message)
	if err == nil {
		store.notify(firstNonEmpty(queued.Namespace, turn.Namespace), firstNonEmpty(queued.ThreadID, turn.ThreadID), chatEventReasonTurn, firstNonEmpty(queued.ID, turn.ID))
	}
	return queued, user, thread, err
}

func (store notifyingChatStore) CompleteTurn(ctx context.Context, turn chat.Turn, message chat.Message) error {
	err := store.Store.CompleteTurn(ctx, turn, message)
	if err == nil {
		store.notify(turn.Namespace, turn.ThreadID, chatEventReasonTurn, turn.ID)
	}
	return err
}

func (store notifyingChatStore) RecordRun(ctx context.Context, turn chat.Turn, runName string) error {
	err := store.Store.RecordRun(ctx, turn, runName)
	if err == nil {
		store.notify(turn.Namespace, turn.ThreadID, chatEventReasonTurn, turn.ID)
	}
	return err
}

func (store notifyingChatStore) RetryTurn(ctx context.Context, turn chat.Turn, retry chat.TurnRetry) (chat.Turn, error) {
	retried, err := store.Store.RetryTurn(ctx, turn, retry)
	if err == nil {
		store.notify(firstNonEmpty(retried.Namespace, turn.Namespace), firstNonEmpty(retried.ThreadID, turn.ThreadID), chatEventReasonTurn, firstNonEmpty(retried.ID, turn.ID))
	}
	return retried, err
}

func (store notifyingChatStore) ActivateTurn(ctx context.Context, turn chat.Turn) (bool, error) {
	activated, err := store.Store.ActivateTurn(ctx, turn)
	if err == nil && activated {
		store.notify(turn.Namespace, turn.ThreadID, chatEventReasonTurn, turn.ID)
	}
	return activated, err
}

func (store notifyingChatStore) CreateThread(ctx context.Context, thread chat.Thread) (chat.Thread, error) {
	created, err := store.Store.CreateThread(ctx, thread)
	if err == nil {
		store.notify(created.Namespace, created.ID, chatEventReasonCreated, "")
	}
	return created, err
}

func (store notifyingChatStore) EnsureStandingThread(ctx context.Context, thread chat.Thread) (chat.Thread, bool, error) {
	ensured, created, err := store.Store.EnsureStandingThread(ctx, thread)
	if err == nil && created {
		store.notify(ensured.Namespace, ensured.ID, chatEventReasonCreated, "")
	}
	return ensured, created, err
}

func (store notifyingChatStore) RepairAssistantReply(ctx context.Context, namespace string, original chat.Message, content string, format string) (chat.Message, error) {
	repaired, err := store.Store.RepairAssistantReply(ctx, namespace, original, content, format)
	if err == nil {
		store.notify(namespace, firstNonEmpty(repaired.ThreadID, original.ThreadID), chatEventReasonMessage, "")
	}
	return repaired, err
}

func (store notifyingChatStore) AppendMessages(ctx context.Context, namespace, threadID string, messages []chat.Message) ([]chat.Message, chat.Thread, error) {
	appended, thread, err := store.Store.AppendMessages(ctx, namespace, threadID, messages)
	if err == nil {
		store.notify(namespace, threadID, chatEventReasonMessage, "")
	}
	return appended, thread, err
}

func (server *Server) handleChatEvents(writer http.ResponseWriter, request *http.Request) {
	namespace, principal, ok := server.authorizeChat(writer, request, PermissionChatRead)
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
	flusher, ok := writer.(http.Flusher)
	if !ok {
		writeAPIError(writer, http.StatusInternalServerError, "stream_unsupported", "HTTP streaming is unavailable")
		return
	}
	// Subscribe before `ready` so a write that lands while the client reads
	// the first frame is still delivered.
	subscriber, unsubscribe := server.chatEvents.subscribe(namespace)
	defer unsubscribe()

	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-store, no-transform")
	writer.Header().Set("Connection", "keep-alive")
	writer.Header().Set("X-Accel-Buffering", "no")
	writer.WriteHeader(http.StatusOK)
	flusher.Flush()

	sse := newSSEWriter(writer, flusher)
	server.log.Info("chat events stream opened", "subject", principal.Subject, "namespace", namespace)
	defer server.log.Info("chat events stream closed", "subject", principal.Subject, "namespace", namespace)
	if err := sse.write("ready", "", chatEventsNotice{Type: "ready", Namespace: namespace}); err != nil {
		return
	}

	streamTimer := time.NewTimer(server.config.Stream.MaxDuration.Duration)
	defer streamTimer.Stop()
	tokenTimer := time.NewTimer(time.Until(principal.Expiry))
	defer tokenTimer.Stop()
	heartbeat := time.NewTicker(server.config.Stream.HeartbeatInterval.Duration)
	defer heartbeat.Stop()
	resyncIfDropped := func() error {
		if subscriber.dropped.Swap(false) {
			return sse.write("resync", "", chatEventsNotice{Type: "resync", Message: "some thread changes were not delivered; re-read open threads"})
		}
		return nil
	}
	for {
		select {
		case <-request.Context().Done():
			return
		case <-tokenTimer.C:
			_ = sse.write("error", "", chatEventsNotice{Type: "error", Code: "token_expired", Message: "access token expired; reconnect with a refreshed token"})
			return
		case <-streamTimer.C:
			_ = sse.write("complete", "", chatEventsNotice{Type: "complete", Code: "stream_duration_reached", Message: "reconnect to continue receiving chat events"})
			return
		case <-heartbeat.C:
			if err := resyncIfDropped(); err != nil {
				return
			}
			if err := sse.heartbeat(); err != nil {
				return
			}
		case event := <-subscriber.events:
			if err := resyncIfDropped(); err != nil {
				return
			}
			if err := sse.write("thread", "", event); err != nil {
				return
			}
		}
	}
}
