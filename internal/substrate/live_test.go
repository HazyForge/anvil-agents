package substrate

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// liveTestBackend is a minimal in-memory gateway speaking the spike lifecycle
// paths so live-client tests never need a Substrate cluster.
type liveTestBackend struct {
	mu       sync.Mutex
	actors   map[string]liveActorPayload
	authSeen []string
}

func newLiveTestBackend() *liveTestBackend {
	return &liveTestBackend{actors: map[string]liveActorPayload{}}
}

func (b *liveTestBackend) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if auth := request.Header.Get("Authorization"); auth != "" {
		b.mu.Lock()
		b.authSeen = append(b.authSeen, auth)
		b.mu.Unlock()
	}
	rest := strings.TrimPrefix(request.URL.Path, "/v1/actors/")
	parts := strings.Split(rest, "/")
	if len(parts) < 2 {
		http.Error(writer, "not found", http.StatusNotFound)
		return
	}
	namespace, name := parts[0], parts[1]
	action := ""
	if len(parts) > 2 {
		action = parts[2]
	}
	key := namespace + "/" + name
	b.mu.Lock()
	defer b.mu.Unlock()
	actor, ok := b.actors[key]
	switch {
	case request.Method == http.MethodPut && action == "":
		var body liveActorPayload
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			http.Error(writer, "bad request", http.StatusBadRequest)
			return
		}
		if ok {
			// Idempotent create-or-reuse: warm reuse keeps the resume count.
			actor.State = string(ActorStateActive)
			b.actors[key] = actor
			writeLiveJSON(writer, actor)
			return
		}
		actor = liveActorPayload{Namespace: namespace, Name: name, ID: "live-" + key, State: string(ActorStateActive), ActorClass: body.ActorClass, Pool: body.Pool, HarnessKind: body.HarnessKind, Labels: body.Labels}
		b.actors[key] = actor
		writer.WriteHeader(http.StatusCreated)
		writeLiveJSON(writer, actor)
	case request.Method == http.MethodGet && action == "":
		if !ok {
			http.Error(writer, "not found", http.StatusNotFound)
			return
		}
		writeLiveJSON(writer, actor)
	case request.Method == http.MethodPost && action == "resume":
		if !ok {
			http.Error(writer, "not found", http.StatusNotFound)
			return
		}
		actor.State = string(ActorStateActive)
		actor.Resumes++
		b.actors[key] = actor
		writeLiveJSON(writer, actor)
	case request.Method == http.MethodPost && (action == "suspend" || action == "pause"):
		if !ok {
			http.Error(writer, "not found", http.StatusNotFound)
			return
		}
		if action == "suspend" {
			actor.State = string(ActorStateSuspended)
		} else {
			actor.State = string(ActorStatePaused)
		}
		b.actors[key] = actor
		writeLiveJSON(writer, actor)
	default:
		http.Error(writer, "not found", http.StatusNotFound)
	}
}

func writeLiveJSON(writer http.ResponseWriter, payload liveActorPayload) {
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(payload)
}

func newLiveTestClient(t *testing.T, backend *liveTestBackend, token string) (*LiveClient, context.Context) {
	t.Helper()
	server := httptest.NewServer(backend)
	t.Cleanup(server.Close)
	client, err := NewLiveClient(LiveConfig{Endpoint: server.URL, AuthToken: token})
	if err != nil {
		t.Fatalf("live client: %v", err)
	}
	return client, context.Background()
}

func TestLiveClientRejectsBadEndpoints(t *testing.T) {
	t.Parallel()

	for _, endpoint := range []string{
		"",
		"not-a-url",
		"ftp://gateway/x",
		"http://gateway/x?token=secret",
		"http://user@gateway/x",
		"http://gateway/x#frag",
	} {
		if _, err := NewLiveClient(LiveConfig{Endpoint: endpoint}); err == nil {
			t.Fatalf("endpoint %q must be rejected", endpoint)
		}
	}
}

func TestLiveClientLifecycleRoundTrip(t *testing.T) {
	t.Parallel()

	backend := newLiveTestBackend()
	client, ctx := newLiveTestClient(t, backend, "")
	spec := ActorSpec{Namespace: "agents", Name: "chat-thread-1", HarnessKind: "openCode", ActorClass: "standing-chat", Pool: "warm"}

	created, err := client.CreateActor(ctx, spec)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.ID == "" || created.State != ActorStateActive {
		t.Fatalf("created handle = %+v, want active with an ID", created)
	}
	if _, err := client.ResumeActor(ctx, "other", "chat-thread-1"); !errors.Is(err, ErrActorNotFound) {
		t.Fatalf("cross-namespace resume err = %v, want ErrActorNotFound", err)
	}
	resumed, err := client.ResumeActor(ctx, "agents", "chat-thread-1")
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if resumed.ID != created.ID || resumed.Resumes != 1 {
		t.Fatalf("resumed handle = %+v, want stable ID with one resume", resumed)
	}
	suspended, err := client.SuspendActor(ctx, "agents", "chat-thread-1")
	if err != nil {
		t.Fatalf("suspend: %v", err)
	}
	if suspended.State != ActorStateSuspended {
		t.Fatalf("suspended state = %q, want Suspended", suspended.State)
	}
	paused, err := client.PauseActor(ctx, "agents", "chat-thread-1")
	if err != nil {
		t.Fatalf("pause: %v", err)
	}
	if paused.State != ActorStatePaused {
		t.Fatalf("paused state = %q, want Paused", paused.State)
	}
	described, err := client.DescribeActor(ctx, "agents", "chat-thread-1")
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	if described.ID != created.ID || described.Resumes != 1 {
		t.Fatalf("described handle = %+v, want stable identity", described)
	}
}

func TestLiveClientCreateIsIdempotent(t *testing.T) {
	t.Parallel()

	backend := newLiveTestBackend()
	client, ctx := newLiveTestClient(t, backend, "")
	spec := ActorSpec{Namespace: "agents", Name: "chat-thread-1", HarnessKind: "openCode"}

	first, err := client.CreateActor(ctx, spec)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	second, err := client.CreateActor(ctx, spec)
	if err != nil {
		t.Fatalf("re-create: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("re-create ID = %q, want warm reuse of %q", second.ID, first.ID)
	}
}

func TestLiveClientAuthTokenUsesHeaderOnly(t *testing.T) {
	t.Parallel()

	backend := newLiveTestBackend()
	client, ctx := newLiveTestClient(t, backend, "spike-token")
	if _, err := client.CreateActor(ctx, ActorSpec{Namespace: "agents", Name: "chat-thread-1"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if len(backend.authSeen) == 0 || backend.authSeen[0] != "Bearer spike-token" {
		t.Fatalf("auth headers = %v, want exactly the bearer header", backend.authSeen)
	}
}

func TestLiveClientNilClientFailsClosed(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	var client *LiveClient
	if _, err := client.CreateActor(ctx, ActorSpec{Namespace: "agents", Name: "a"}); err == nil {
		t.Fatal("nil live client must fail closed")
	}
	if got := client.Endpoint(); got != "" {
		t.Fatalf("nil endpoint = %q, want empty", got)
	}
}
