// Local inert E2E fixture: real runapi + ProcessBackend + ACP/HTTP client,
// with an in-memory Kubernetes client and chat store. No provider/model calls.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/go-logr/logr"
	agents "github.com/hazyforge/anvil-agents/api/v1alpha1"
	"github.com/hazyforge/anvil-agents/internal/chat"
	"github.com/hazyforge/anvil-agents/internal/runapi"
	"github.com/hazyforge/anvil-agents/internal/standing"
	"github.com/hazyforge/anvil-agents/internal/substrate"
	"io"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"net"
	"net/url"
	"os"
	"os/signal"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"syscall"
	"time"
)

type localAuth struct{}

func (localAuth) Ready() bool { return true }
func (localAuth) Verify(_ context.Context, marker string) (runapi.Principal, error) {
	if marker != "inert-local-marker" {
		return runapi.Principal{}, fmt.Errorf("inert local fixture marker required")
	}
	return runapi.Principal{Subject: "inert-user", Issuer: "https://inert.example", Roles: []string{"inert"}, Namespaces: []string{"agents"}, Expiry: time.Now().Add(time.Hour)}, nil
}

type noLogs struct{}

func (noLogs) Open(context.Context, *agents.AgentRun, corev1.PodLogOptions) (io.ReadCloser, *corev1.Pod, error) {
	return nil, nil, runapi.ErrLogsPending
}

type localRunner struct {
	clients map[string]*substrate.AtenetClient
}

func (r localRunner) Run(ctx context.Context, kind, prompt string, emit func(string) error) (string, error) {
	return "", fmt.Errorf("inert runner requires a conversation-scoped SessionRunner call")
}

func (r localRunner) RunWithResume(ctx context.Context, handle standing.SessionHandle, turnID, prompt, resumeID string, emit func(string) error) (string, string, error) {
	output, err := r.runSession(ctx, handle.HarnessKind, handle.ThreadID, prompt, emit)
	return output, "", err // ACP adapter owns the logical-thread/native-session map.
}

func (r localRunner) runSession(ctx context.Context, kind, threadID, prompt string, emit func(string) error) (string, error) {
	generator := r.clients[kind]
	if generator == nil {
		return "", fmt.Errorf("unconfigured inert profile")
	}
	result, err := generator.Generate(ctx, substrate.GenerateRequest{ActorName: "inert-" + kind, Atespace: "local-inert", SessionID: threadID, Prompt: prompt})
	if err != nil {
		return "", err
	}
	var envelope any
	switch kind {
	case "codex":
		envelope = map[string]any{"type": "item.completed", "item": map[string]string{"type": "agent_message", "text": result.Text}}
	case "openCode":
		envelope = map[string]any{"type": "text", "part": map[string]string{"text": result.Text}}
	default:
		return "", fmt.Errorf("unexpected inert harness kind")
	}
	raw, err := json.Marshal(envelope)
	if err != nil {
		return "", err
	}
	output := string(raw) + "\n"
	if err = emit(output); err != nil {
		return "", err
	}
	return output, nil
}

func main() {
	if len(os.Args) != 4 {
		panic("usage: fixture LOOPBACK_BIND A_ENDPOINT B_ENDPOINT")
	}
	for _, address := range os.Args[1:] {
		host, _, err := net.SplitHostPort(address)
		if err != nil || host != "127.0.0.1" {
			panic("fixture endpoints must be IPv4 loopback")
		}
	}
	config := runapi.DefaultConfig()
	config.BindAddress = os.Args[1]
	config.OIDC.Issuer = "https://inert.example"
	config.OIDC.Audiences = []string{"inert-local-api"}
	config.Chat.Enabled = true
	config.Runs.CreateEnabled = true
	config.Standing.LiveEnabled = true
	config.Authorization.Bindings = []runapi.AuthorizationBinding{{Roles: []string{"inert"}, Namespaces: []string{"agents"}, Permissions: []string{runapi.PermissionChatRead, runapi.PermissionChatWrite, runapi.PermissionRunsCreate, runapi.PermissionRunsRead, runapi.PermissionRunsStream}}}
	scheme := runtime.NewScheme()
	if err := agents.AddToScheme(scheme); err != nil {
		panic(err)
	}
	objects := []client.Object{}
	kinds := []agents.AgentRunHarnessBackendKind{agents.AgentRunHarnessBackendCodex, agents.AgentRunHarnessBackendOpenCode}
	for i, name := range []string{"inert-a", "inert-b"} {
		harness := &agents.AgentHarnessProfile{ObjectMeta: metav1.ObjectMeta{Name: name + "-harness", Namespace: "agents"}, Spec: agents.AgentHarnessProfileSpec{Backend: agents.AgentRunHarnessBackendSpec{Kind: kinds[i]}, Execution: agents.AgentRunHarnessExecutionSpec{Runtime: agents.AgentRunExecutionRuntimeInProcess}}}
		profile := &agents.AgentRunProfile{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "agents"}, Spec: agents.AgentRunProfileSpec{HarnessProfileRef: &agents.NamespacedObjectReference{Name: harness.Name}, Scope: agents.AgentRunScopeSpec{ApplicationRef: &agents.ApplicationReferenceSpec{Name: "inert-app"}}}}
		objects = append(objects, harness, profile)
	}
	foreign := &agents.AgentRunProfile{ObjectMeta: metav1.ObjectMeta{Name: "inert-foreign", Namespace: "agents"}, Spec: agents.AgentRunProfileSpec{Scope: agents.AgentRunScopeSpec{ApplicationRef: &agents.ApplicationReferenceSpec{Name: "other-app"}}}}
	objects = append(objects, foreign)
	kube := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&agents.AgentRun{}).WithObjects(objects...).Build()
	server, err := runapi.NewServer(config, localAuth{}, kube, noLogs{}, logr.Discard())
	if err != nil {
		panic(err)
	}
	store := chat.Store(chat.NewMemoryStore())
	// Optional isolated PostgreSQL fixture supplied only by the test launcher.
	if database := os.Getenv("INERT_CHAT_DATABASE_URL"); database != "" {
		parsed, parseErr := url.Parse(database)
		if parseErr != nil || parsed.Hostname() != "127.0.0.1" || parsed.Path != "/anvil_chat_inert" {
			panic("database must be the isolated local anvil_chat_inert fixture")
		}
		store, err = chat.OpenPostgresStore(context.Background(), database)
		if err != nil {
			panic(err)
		}
	}
	defer store.Close()
	server.SetChatStore(store)
	clients := map[string]*substrate.AtenetClient{}
	for i, kind := range []string{"codex", "openCode"} {
		generator, err := substrate.NewAtenetClient(substrate.AtenetConfig{Endpoint: os.Args[i+2], Timeout: 20 * time.Second})
		if err != nil {
			panic(err)
		}
		clients[kind] = generator
	}
	server.SetStandingBackend(standing.NewProcessBackend(localRunner{clients: clients}))
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	if err = server.Start(ctx); err != nil {
		panic(err)
	}
}
