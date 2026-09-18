package substrate

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// fakeATEControl is an in-memory ATEControl standing in for ateapi over the
// wire. It models the server-side semantics the mapping depends on: unknown
// actors fail with ATECodeNotFound on every RPC, duplicate creates fail with
// ATECodeAlreadyExists, and Resume reports whether a resume workflow actually
// ran (ResumeActorResponse.resumed: false when already RUNNING).
type fakeATEControl struct {
	mu     sync.Mutex
	actors map[string]*ATEActor
	// creates records the create payloads so tests can assert template and
	// placement mapping.
	creates []ATECreateSpec
	calls   []string
	uids    int
}

func newFakeATEControl() *fakeATEControl {
	return &fakeATEControl{actors: map[string]*ATEActor{}}
}

func (f *fakeATEControl) key(ref ATEObjectRef) string {
	return ActorKey(ref.Atespace, ref.Name)
}

func (f *fakeATEControl) GetActor(_ context.Context, ref ATEObjectRef) (ATEActor, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "get:"+f.key(ref))
	actor, ok := f.actors[f.key(ref)]
	if !ok {
		return ATEActor{}, ateNotFoundf("actor %s/%s not found", ref.Atespace, ref.Name)
	}
	return *actor, nil
}

func (f *fakeATEControl) CreateActor(_ context.Context, spec ATECreateSpec) (ATEActor, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := ActorKey(spec.Atespace, spec.Name)
	if _, ok := f.actors[key]; ok {
		return ATEActor{}, &ATEError{Code: ATECodeAlreadyExists, Err: fmt.Errorf("actor %s already exists", key)}
	}
	f.uids++
	actor := &ATEActor{
		Atespace:         spec.Atespace,
		Name:             spec.Name,
		UID:              fmt.Sprintf("ate-uid-%d", f.uids),
		TemplateAtespace: spec.TemplateAtespace,
		TemplateName:     spec.TemplateName,
		WorkerSelector:   spec.WorkerSelector,
		State:            ATEActorStateRunning,
	}
	f.actors[key] = actor
	f.creates = append(f.creates, spec)
	f.calls = append(f.calls, "create:"+key)
	return *actor, nil
}

func (f *fakeATEControl) ResumeActor(_ context.Context, ref ATEObjectRef) (ATEActor, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "resume:"+f.key(ref))
	actor, ok := f.actors[f.key(ref)]
	if !ok {
		return ATEActor{}, false, ateNotFoundf("actor %s/%s not found", ref.Atespace, ref.Name)
	}
	if actor.State == ATEActorStateRunning {
		return *actor, false, nil
	}
	actor.State = ATEActorStateRunning
	return *actor, true, nil
}

func (f *fakeATEControl) SuspendActor(_ context.Context, ref ATEObjectRef) (ATEActor, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "suspend:"+f.key(ref))
	actor, ok := f.actors[f.key(ref)]
	if !ok {
		return ATEActor{}, ateNotFoundf("actor %s/%s not found", ref.Atespace, ref.Name)
	}
	actor.State = ATEActorStateSuspended
	return *actor, nil
}

func (f *fakeATEControl) PauseActor(_ context.Context, ref ATEObjectRef) (ATEActor, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "pause:"+f.key(ref))
	actor, ok := f.actors[f.key(ref)]
	if !ok {
		return ATEActor{}, ateNotFoundf("actor %s/%s not found", ref.Atespace, ref.Name)
	}
	actor.State = ATEActorStatePaused
	return *actor, nil
}

func liveTestConfig() ATEClientConfig {
	return ATEClientConfig{Address: "ate-api-server.ate-system.svc:443", Template: "standing-chat"}
}

func liveTestGate() GateConfig {
	return GateConfig{
		Enabled:  true,
		Endpoint: "ate-api-server.ate-system.svc:443",
		Atespace: "",
		Template: "standing-chat",
	}
}

func mustLiveClient(t *testing.T, control ATEControl) (Client, *fakeATEControl) {
	t.Helper()
	fake, ok := control.(*fakeATEControl)
	if !ok {
		t.Fatal("live tests require *fakeATEControl")
	}
	client, err := NewATEClient(liveTestConfig(), control)
	if err != nil {
		t.Fatalf("NewATEClient: %v", err)
	}
	return client, fake
}

func TestATEClientCreateMapsActorClassToTemplate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client, fake := mustLiveClient(t, newFakeATEControl())
	created, err := client.CreateActor(ctx, ActorSpec{Namespace: "agents", Name: "chat-thread-1", HarnessKind: "openCode", ActorClass: "standing-chat", Pool: "warm"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.State != ActorStateActive || created.ID == "" {
		t.Fatalf("created handle = %+v, want active with a server UID", created)
	}
	if len(fake.creates) != 1 {
		t.Fatalf("creates = %d, want 1", len(fake.creates))
	}
	got := fake.creates[0]
	if got.Atespace != "agents" || got.TemplateAtespace != "agents" || got.TemplateName != "standing-chat" {
		t.Fatalf("create spec = %+v, want agents atespace with standing-chat template", got)
	}
	if got.WorkerSelector[WorkerSelectorPoolLabel] != "warm" {
		t.Fatalf("create worker selector = %+v, want pool warm", got.WorkerSelector)
	}
}

func TestATEClientCreateFallsBackToDefaultTemplate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client, fake := mustLiveClient(t, newFakeATEControl())
	if _, err := client.CreateActor(ctx, ActorSpec{Namespace: "agents", Name: "chat-thread-1"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(fake.creates) != 1 || fake.creates[0].TemplateName != "standing-chat" {
		t.Fatalf("creates = %+v, want default standing-chat template", fake.creates)
	}
	if len(fake.creates[0].WorkerSelector) != 0 {
		t.Fatalf("empty pool must leave placement to the template, got %+v", fake.creates[0].WorkerSelector)
	}
}

func TestATEClientWarmReuse(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client, _ := mustLiveClient(t, newFakeATEControl())
	first, err := client.CreateActor(ctx, ActorSpec{Namespace: "agents", Name: "chat-thread-1"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := client.SuspendActor(ctx, "agents", "chat-thread-1"); err != nil {
		t.Fatalf("suspend: %v", err)
	}
	resumed, err := client.ResumeActor(ctx, "agents", "chat-thread-1")
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if resumed.Resumes != 1 {
		t.Fatalf("resumed handle = %+v, want one observed resume workflow", resumed)
	}
	second, err := client.CreateActor(ctx, ActorSpec{Namespace: "agents", Name: "chat-thread-1"})
	if err != nil {
		t.Fatalf("re-create: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("re-create ID = %q, want warm reuse of %q", second.ID, first.ID)
	}
	if second.Resumes != 1 {
		t.Fatalf("re-create resumes = %d, want warm resume count preserved", second.Resumes)
	}
}

func TestATEClientAlreadyRunningResumeIsWarmNoop(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client, _ := mustLiveClient(t, newFakeATEControl())
	if _, err := client.CreateActor(ctx, ActorSpec{Namespace: "agents", Name: "chat-thread-1"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	resumed, err := client.ResumeActor(ctx, "agents", "chat-thread-1")
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if resumed.State != ActorStateActive || resumed.Resumes != 0 {
		t.Fatalf("resume of running actor = %+v, want active with no resume workflow", resumed)
	}
}

func TestATEClientNotFoundMapsToErrActorNotFound(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client, _ := mustLiveClient(t, newFakeATEControl())
	if _, err := client.ResumeActor(ctx, "agents", "missing"); !errors.Is(err, ErrActorNotFound) {
		t.Fatalf("resume err = %v, want ErrActorNotFound", err)
	}
	if _, err := client.SuspendActor(ctx, "agents", "missing"); !errors.Is(err, ErrActorNotFound) {
		t.Fatalf("suspend err = %v, want ErrActorNotFound", err)
	}
	if _, err := client.PauseActor(ctx, "agents", "missing"); !errors.Is(err, ErrActorNotFound) {
		t.Fatalf("pause err = %v, want ErrActorNotFound", err)
	}
	if _, err := client.DescribeActor(ctx, "agents", "missing"); !errors.Is(err, ErrActorNotFound) {
		t.Fatalf("describe err = %v, want ErrActorNotFound", err)
	}
	if _, err := client.ResumeActor(ctx, "other", "chat-thread-1"); !errors.Is(err, ErrActorNotFound) {
		t.Fatalf("cross-atespace resume err = %v, want ErrActorNotFound", err)
	}
}

func TestATEClientSuspendPauseDescribeRoundTrip(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client, _ := mustLiveClient(t, newFakeATEControl())
	created, err := client.CreateActor(ctx, ActorSpec{Namespace: "agents", Name: "chat-thread-1"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	suspended, err := client.SuspendActor(ctx, "agents", "chat-thread-1")
	if err != nil {
		t.Fatalf("suspend: %v", err)
	}
	if suspended.State != ActorStateSuspended || suspended.ID != created.ID {
		t.Fatalf("suspended handle = %+v, want suspended with stable UID", suspended)
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
	if described.State != ActorStatePaused || described.ID != created.ID {
		t.Fatalf("described handle = %+v, want paused with stable UID", described)
	}
}

func TestATEClientAtespaceOverride(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fake := newFakeATEControl()
	cfg := liveTestConfig()
	cfg.Atespace = "shared"
	client, err := NewATEClient(cfg, fake)
	if err != nil {
		t.Fatalf("NewATEClient: %v", err)
	}
	if _, err := client.CreateActor(ctx, ActorSpec{Namespace: "agents", Name: "chat-thread-1"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(fake.creates) != 1 || fake.creates[0].Atespace != "shared" {
		t.Fatalf("creates = %+v, want forced shared atespace", fake.creates)
	}
}

func TestATEClientRejectsOverlongNames(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client, _ := mustLiveClient(t, newFakeATEControl())
	name := "chat-" + strings.Repeat("a", 60)
	if _, err := client.CreateActor(ctx, ActorSpec{Namespace: "agents", Name: name}); err == nil {
		t.Fatal("expected overlong actor name to fail fast")
	}
	uuidThread := "123e4567-e89b-12d3-a456-426614174000"
	if _, err := client.CreateActor(ctx, ActorSpec{Namespace: "agents", Name: ActorNameForThread(uuidThread)}); err != nil {
		t.Fatalf("uuid thread actor name must fit the ATE bound: %v", err)
	}
}

func TestMapATEState(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		state ATEActorState
		want  ActorState
	}{
		{ATEActorStateRunning, ActorStateActive},
		{ATEActorStateResuming, ActorStateActive},
		{ATEActorStateSuspended, ActorStateSuspended},
		{ATEActorStateSuspending, ActorStateSuspended},
		{ATEActorStateCrashed, ActorStateSuspended},
		{ATEActorStateReverting, ActorStateSuspended},
		{ATEActorStatePaused, ActorStatePaused},
		{ATEActorStatePausing, ActorStatePaused},
		{ATEActorStateDeleting, ActorStateActive},
		{ATEActorStateUnspecified, ActorStateActive},
		{"future-state", ActorStateActive},
	} {
		if got := MapATEState(tc.state); got != tc.want {
			t.Fatalf("MapATEState(%q) = %q, want %q", tc.state, got, tc.want)
		}
	}
}

func TestATEClientConfigValidation(t *testing.T) {
	t.Parallel()

	if _, err := NewATEClient(ATEClientConfig{}, newFakeATEControl()); err == nil {
		t.Fatal("expected config without address to fail")
	}
	noTemplate := liveTestConfig()
	noTemplate.Template = ""
	if _, err := NewATEClient(noTemplate, newFakeATEControl()); err == nil {
		t.Fatal("expected config without template to fail")
	}
	if _, err := NewATEClient(liveTestConfig(), nil); err == nil {
		t.Fatal("expected nil control transport to fail")
	}
}

func TestATEClientConfigFromGate(t *testing.T) {
	t.Setenv(GateEnabledEnvVar, "true")
	t.Setenv(GateEndpointEnvVar, "ate-api-server.ate-system.svc:443")
	t.Setenv(GateAtespaceEnvVar, "agents")
	t.Setenv(GateTemplateEnvVar, "standing-chat")
	t.Setenv(GateTokenFileEnvVar, "/run/ate/token")

	gate := GateConfigFromEnv()
	if !gate.LiveEnabled() {
		t.Fatalf("gate from env = %+v, want live", gate)
	}
	cfg := ATEClientConfigFromGate(gate)
	if cfg.Address != "ate-api-server.ate-system.svc:443" || cfg.Atespace != "agents" || cfg.Template != "standing-chat" || cfg.TokenFile != "/run/ate/token" {
		t.Fatalf("client config from gate = %+v", cfg)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("gate-derived config: %v", err)
	}
	if _, err := NewATEClient(cfg, newFakeATEControl()); err != nil {
		t.Fatalf("NewATEClient from gate: %v", err)
	}
}
