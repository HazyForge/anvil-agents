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

// fakeRouter emulates the atenet-router HTTP data plane: it parses the Host
// authority <actor>.<atespace>.actors... like upstream ExtProc does,
// resumes-on-request, and maps a missing actor to 404 (the upstream gRPC
// NotFound mapping). It cannot create actors, exactly like the real router.
type fakeRouter struct {
	mu       sync.Mutex
	atespace string
	actors   map[string]string // actor label -> state
	hostSeen []string
	authSeen []string
}

func newFakeRouter(atespace string) *fakeRouter {
	return &fakeRouter{atespace: atespace, actors: map[string]string{}}
}

func (r *fakeRouter) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	host := request.Host
	r.mu.Lock()
	r.hostSeen = append(r.hostSeen, host)
	if auth := request.Header.Get("Authorization"); auth != "" {
		r.authSeen = append(r.authSeen, auth)
	}
	suffix := "." + r.atespace + "." + actorDNSDomainSuffix
	actor, ok := strings.CutSuffix(host, suffix)
	r.mu.Unlock()
	if !ok || actor == "" || strings.Contains(actor, ".") {
		http.Error(writer, "not found", http.StatusNotFound)
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, known := r.actors[actor]; !known {
		http.Error(writer, "not found", http.StatusNotFound)
		return
	}
	// Resume-on-request: any successfully routed request leaves the actor
	// running on a worker.
	r.actors[actor] = string(ActorStateActive)
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write([]byte(`{"ok":true}`))
}

// fakeShim emulates cmd/substrate-ate-shim: the kubectl-ate control bridge
// for create/suspend/pause/describe.
type fakeShim struct {
	mu     sync.Mutex
	actors map[string]shimPayload
}

func newFakeShim() *fakeShim {
	return &fakeShim{actors: map[string]shimPayload{}}
}

func (s *fakeShim) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	rest, ok := strings.CutPrefix(request.URL.Path, "/shim/v1/actors/")
	if !ok {
		http.Error(writer, "not found", http.StatusNotFound)
		return
	}
	segments := strings.Split(rest, "/")
	if len(segments) != 2 {
		http.Error(writer, "not found", http.StatusNotFound)
		return
	}
	name, action, _ := strings.Cut(segments[1], ":")
	key := segments[0] + "/" + name
	s.mu.Lock()
	defer s.mu.Unlock()
	actor, known := s.actors[key]
	write := func(status int, payload shimPayload) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(status)
		_ = json.NewEncoder(writer).Encode(payload)
	}
	switch {
	case request.Method == http.MethodPut && action == "":
		var body shimPayload
		_ = json.NewDecoder(request.Body).Decode(&body)
		if strings.TrimSpace(body.ActorTemplate) == "" {
			http.Error(writer, "actorTemplate is required", http.StatusBadRequest)
			return
		}
		if known {
			actor.State = string(ActorStateActive)
			actor.Reused = true
			s.actors[key] = actor
			write(http.StatusOK, actor)
			return
		}
		actor = shimPayload{Atespace: segments[0], Name: name, ID: key, State: string(ActorStateSuspended)}
		s.actors[key] = actor
		write(http.StatusCreated, actor)
	case request.Method == http.MethodGet && action == "":
		if !known {
			http.Error(writer, "not found", http.StatusNotFound)
			return
		}
		write(http.StatusOK, actor)
	case request.Method == http.MethodPost && (action == "suspend" || action == "pause"):
		if !known {
			http.Error(writer, "not found", http.StatusNotFound)
			return
		}
		if action == "suspend" {
			actor.State = string(ActorStateSuspended)
		} else {
			actor.State = string(ActorStatePaused)
		}
		s.actors[key] = actor
		write(http.StatusOK, actor)
	default:
		http.Error(writer, "not found", http.StatusNotFound)
	}
}

func newLiveTestClient(t *testing.T, router *fakeRouter, shim *fakeShim, mutate func(*LiveConfig)) (*LiveClient, context.Context) {
	t.Helper()
	routerServer := httptest.NewServer(router)
	t.Cleanup(routerServer.Close)
	cfg := LiveConfig{Endpoint: routerServer.URL, Atespace: router.atespace, ActorTemplate: "demo-ns/demo-tmpl"}
	if shim != nil {
		shimServer := httptest.NewServer(shim)
		t.Cleanup(shimServer.Close)
		cfg.ShimEndpoint = shimServer.URL
	}
	if mutate != nil {
		mutate(&cfg)
	}
	client, err := NewLiveClient(cfg)
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

func TestLiveClientRejectsBadAtespaceAndTemplate(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		mutate func(*LiveConfig)
	}{
		{"uppercase atespace", func(cfg *LiveConfig) { cfg.Atespace = "Agents" }},
		{"empty atespace label", func(cfg *LiveConfig) { cfg.Atespace = "agents/" }},
		{"template without slash", func(cfg *LiveConfig) { cfg.ActorTemplate = "counter" }},
		{"template with spaces", func(cfg *LiveConfig) { cfg.ActorTemplate = "demo ns/counter" }},
		{"shim with query", func(cfg *LiveConfig) { cfg.ShimEndpoint = "http://shim:8081/x?token=s" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := LiveConfig{Endpoint: "http://router:8080"}
			tc.mutate(&cfg)
			if _, err := NewLiveClient(cfg); err == nil {
				t.Fatalf("config %+v must be rejected", cfg)
			}
		})
	}
}

func TestLiveClientRouterResumeAndDescribe(t *testing.T) {
	t.Parallel()

	router := newFakeRouter("agents")
	router.actors["chat-thread-1"] = string(ActorStateSuspended)
	client, ctx := newLiveTestClient(t, router, nil, nil)

	described, err := client.DescribeActor(ctx, "agents", "chat-thread-1")
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	if described.State != ActorStateActive || described.ID != "agents/chat-thread-1" {
		t.Fatalf("described handle = %+v, want active with atespace/name ID", described)
	}
	resumed, err := client.ResumeActor(ctx, "agents", "chat-thread-1")
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if resumed.State != ActorStateActive {
		t.Fatalf("resumed state = %q, want Active", resumed.State)
	}
	router.mu.Lock()
	defer router.mu.Unlock()
	if len(router.hostSeen) == 0 || router.hostSeen[0] != "chat-thread-1.agents."+actorDNSDomainSuffix {
		t.Fatalf("router hosts = %v, want the atenet actor authority", router.hostSeen)
	}
}

func TestLiveClientRouterNotFound(t *testing.T) {
	t.Parallel()

	router := newFakeRouter("agents")
	client, ctx := newLiveTestClient(t, router, nil, nil)
	if _, err := client.ResumeActor(ctx, "other", "missing"); !errors.Is(err, ErrActorNotFound) {
		t.Fatalf("cross-actor resume err = %v, want ErrActorNotFound", err)
	}
	if _, err := client.DescribeActor(ctx, "agents", "missing"); !errors.Is(err, ErrActorNotFound) {
		t.Fatalf("missing describe err = %v, want ErrActorNotFound", err)
	}
}

func TestLiveClientControlRequiresShim(t *testing.T) {
	t.Parallel()

	router := newFakeRouter("agents")
	client, ctx := newLiveTestClient(t, router, nil, nil)
	if _, err := client.CreateActor(ctx, ActorSpec{Namespace: "agents", Name: "chat-thread-1"}); !errors.Is(err, ErrLiveControlPlaneRequired) {
		t.Fatalf("create without shim err = %v, want ErrLiveControlPlaneRequired", err)
	}
	if _, err := client.SuspendActor(ctx, "agents", "chat-thread-1"); !errors.Is(err, ErrLiveControlPlaneRequired) {
		t.Fatalf("suspend without shim err = %v, want ErrLiveControlPlaneRequired", err)
	}
	if _, err := client.PauseActor(ctx, "agents", "chat-thread-1"); !errors.Is(err, ErrLiveControlPlaneRequired) {
		t.Fatalf("pause without shim err = %v, want ErrLiveControlPlaneRequired", err)
	}
}

func TestLiveClientShimLifecycleRoundTrip(t *testing.T) {
	t.Parallel()

	router := newFakeRouter("agents")
	shim := newFakeShim()
	client, ctx := newLiveTestClient(t, router, shim, nil)
	spec := ActorSpec{Namespace: "agents", Name: "chat-thread-1", HarnessKind: "openCode", ActorClass: "standing-chat", Pool: "warm"}

	created, err := client.CreateActor(ctx, spec)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.ID != "agents/chat-thread-1" || created.State != ActorStateSuspended {
		t.Fatalf("created handle = %+v, want suspended with atespace/name ID", created)
	}
	// A fresh shim actor is unknown to the router until a first request
	// restores it; the fake router only knows pre-seeded actors.
	router.mu.Lock()
	router.actors["chat-thread-1"] = string(ActorStateSuspended)
	router.mu.Unlock()
	resumed, err := client.ResumeActor(ctx, "agents", "chat-thread-1")
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if resumed.ID != created.ID || resumed.State != ActorStateActive {
		t.Fatalf("resumed handle = %+v, want stable ID and active", resumed)
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
	// With a shim configured, describe reads full control-plane state.
	described, err := client.DescribeActor(ctx, "agents", "chat-thread-1")
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	if described.State != ActorStatePaused || described.ID != created.ID {
		t.Fatalf("described handle = %+v, want paused with stable ID", described)
	}
}

func TestLiveClientCreateIsIdempotent(t *testing.T) {
	t.Parallel()

	router := newFakeRouter("agents")
	shim := newFakeShim()
	client, ctx := newLiveTestClient(t, router, shim, nil)
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

	router := newFakeRouter("agents")
	router.actors["chat-thread-1"] = string(ActorStateActive)
	client, ctx := newLiveTestClient(t, router, nil, func(cfg *LiveConfig) { cfg.AuthToken = "spike-token" })
	if _, err := client.ResumeActor(ctx, "agents", "chat-thread-1"); err != nil {
		t.Fatalf("resume: %v", err)
	}
	router.mu.Lock()
	defer router.mu.Unlock()
	if len(router.authSeen) == 0 || router.authSeen[0] != "Bearer spike-token" {
		t.Fatalf("auth headers = %v, want exactly the bearer header", router.authSeen)
	}
}

func TestLiveClientActorLabelFlattening(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		want string
	}{
		{"chat-thread-1", "chat-thread-1"},
		{"chat.Thread.1", "chat-thread-1"},
		{"CHAT-THREAD-1", "chat-thread-1"},
	} {
		label, err := liveActorLabel(tc.name)
		if err != nil || label != tc.want {
			t.Fatalf("liveActorLabel(%q) = %q err=%v, want %q", tc.name, label, err, tc.want)
		}
	}
	long := "chat-" + strings.Repeat("a", 100)
	label, err := liveActorLabel(long)
	if err != nil || len(label) > maxRouterLabelLen {
		t.Fatalf("long label = %q err=%v, want at most %d chars", label, err, maxRouterLabelLen)
	}
	if _, err := liveActorLabel("..."); err == nil {
		t.Fatal("empty-after-flattening name must be rejected")
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
	if got := client.Atespace(); got != "" {
		t.Fatalf("nil atespace = %q, want empty", got)
	}
}
