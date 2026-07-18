package runapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	agentsv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

type staticAuthenticator struct {
	principal Principal
	err       error
	ready     bool
}

func (authenticator staticAuthenticator) Verify(context.Context, string) (Principal, error) {
	return authenticator.principal, authenticator.err
}

func (authenticator staticAuthenticator) Ready() bool {
	return authenticator.ready
}

type staticLogSource struct {
	contents string
	err      error
}

type replacingReader struct {
	client.Reader
	replacement *agentsv1alpha1.AgentRun
	gets        atomic.Int32
}

type blockingAfterFirstReader struct {
	client.Reader
	gets     atomic.Int32
	entered  chan struct{}
	canceled chan struct{}
}

func (reader *blockingAfterFirstReader) Get(ctx context.Context, key client.ObjectKey, object client.Object, options ...client.GetOption) error {
	if reader.gets.Add(1) == 1 {
		return reader.Reader.Get(ctx, key, object, options...)
	}
	select {
	case reader.entered <- struct{}{}:
	default:
	}
	<-ctx.Done()
	select {
	case reader.canceled <- struct{}{}:
	default:
	}
	return ctx.Err()
}

func (reader *replacingReader) Get(ctx context.Context, key client.ObjectKey, object client.Object, options ...client.GetOption) error {
	if reader.gets.Add(1) == 1 {
		return reader.Reader.Get(ctx, key, object, options...)
	}
	replacement := reader.replacement.DeepCopy()
	replacement.DeepCopyInto(object.(*agentsv1alpha1.AgentRun))
	return nil
}

func (source staticLogSource) Open(_ context.Context, run *agentsv1alpha1.AgentRun, _ corev1.PodLogOptions) (io.ReadCloser, *corev1.Pod, error) {
	if source.err != nil {
		return nil, nil, source.err
	}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: run.Status.RunnerPodRef.Name, Namespace: run.Namespace, UID: types.UID("pod-uid")}}
	return io.NopCloser(strings.NewReader(source.contents)), pod, nil
}

func TestProtectedRoutesRequireHeaderBearerToken(t *testing.T) {
	server := testServer(t, nil, staticAuthenticator{ready: true}, staticLogSource{})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/namespaces/agents/agent-runs", nil)
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || response.Header().Get("WWW-Authenticate") == "" {
		t.Fatalf("expected bearer challenge, got %d %#v", response.Code, response.Header())
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v1/namespaces/agents/agent-runs?access_token=leak", nil)
	request.Header.Set("Authorization", "Bearer valid")
	response = httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "query_token_rejected") {
		t.Fatalf("expected query-token rejection, got %d %s", response.Code, response.Body.String())
	}
}

func TestListAndGetReturnCuratedViews(t *testing.T) {
	run := testAgentRun(agentsv1alpha1.AgentRunPhaseRunning)
	authenticator := staticAuthenticator{ready: true, principal: testPrincipal(time.Now().Add(time.Hour))}
	server := testServer(t, run, authenticator, staticLogSource{})

	request := httptest.NewRequest(http.MethodGet, "/api/v1/namespaces/agents/agent-runs", nil)
	request.Header.Set("Authorization", "Bearer valid")
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"name":"run-1"`) {
		t.Fatalf("unexpected list response: %d %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "captured output") {
		t.Fatalf("list response leaked detailed output: %s", response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v1/namespaces/agents/agent-runs/run-1", nil)
	request.Header.Set("Authorization", "Bearer valid")
	response = httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "captured output") {
		t.Fatalf("unexpected detail response: %d %s", response.Code, response.Body.String())
	}
}

func TestUnauthorizedNamespaceDoesNotRevealRun(t *testing.T) {
	run := testAgentRun(agentsv1alpha1.AgentRunPhaseRunning)
	principal := testPrincipal(time.Now().Add(time.Hour))
	principal.Roles = []string{"wrong-role"}
	server := testServer(t, run, staticAuthenticator{ready: true, principal: principal}, staticLogSource{})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/namespaces/agents/agent-runs/run-1", nil)
	request.Header.Set("Authorization", "Bearer valid")
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("expected non-disclosing 404, got %d %s", response.Code, response.Body.String())
	}
}

func TestRunEventsRequiresBothReadAndStreamPermissions(t *testing.T) {
	run := testAgentRun(agentsv1alpha1.AgentRunPhaseRunning)
	principal := testPrincipal(time.Now().Add(time.Hour))
	for _, permissions := range [][]string{{PermissionRunsRead}, {PermissionRunsStream}} {
		server := testServer(t, run, staticAuthenticator{ready: true, principal: principal}, staticLogSource{err: ErrLogsPending})
		server.authorizer = NewAuthorizer(AuthorizationConfig{Bindings: []AuthorizationBinding{{
			Roles:       []string{"viewer"},
			Permissions: permissions,
			Namespaces:  []string{"agents"},
		}}})
		request := httptest.NewRequest(http.MethodGet, "/api/v1/namespaces/agents/agent-runs/run-1/events", nil)
		request.Header.Set("Authorization", "Bearer valid")
		response := httptest.NewRecorder()
		server.routes().ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("permissions %v unexpectedly opened stream: %d %s", permissions, response.Code, response.Body.String())
		}
	}
}

func TestRunEventsStreamsSnapshotLogAndTerminal(t *testing.T) {
	run := testAgentRun(agentsv1alpha1.AgentRunPhaseSucceeded)
	run.Status.RunnerPodRef = &agentsv1alpha1.NamespacedObjectReference{Name: "run-pod", Namespace: "agents"}
	authenticator := staticAuthenticator{ready: true, principal: testPrincipal(time.Now().Add(time.Hour))}
	server := testServer(t, run, authenticator, staticLogSource{contents: "2026-07-17T12:00:00Z first line\n"})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/namespaces/agents/agent-runs/run-1/events?tailLines=1", nil)
	request.Header.Set("Authorization", "Bearer valid")
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("unexpected stream response: %d %#v", response.Code, response.Header())
	}
	body := response.Body.String()
	for _, expected := range []string{"event: snapshot", "event: log", "first line", "event: terminal"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("stream missing %q:\n%s", expected, body)
		}
	}
}

func TestNeedsHumanRunIsTerminal(t *testing.T) {
	if !agentRunTerminal(agentsv1alpha1.AgentRunPhaseNeedsHuman) {
		t.Fatal("NeedsHuman must use the controller's terminal phase semantics")
	}
}

func TestRunEventsClosesAtTokenExpiry(t *testing.T) {
	run := testAgentRun(agentsv1alpha1.AgentRunPhaseRunning)
	principal := testPrincipal(time.Now().Add(30 * time.Millisecond))
	server := testServer(t, run, staticAuthenticator{ready: true, principal: principal}, staticLogSource{err: ErrLogsPending})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/namespaces/agents/agent-runs/run-1/events", nil)
	request.Header.Set("Authorization", "Bearer valid")
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if !strings.Contains(response.Body.String(), "token_expired") {
		t.Fatalf("expected token expiry event:\n%s", response.Body.String())
	}
}

func TestRunEventsDoesNotReplayCompletedLogTailInOneConnection(t *testing.T) {
	run := testAgentRun(agentsv1alpha1.AgentRunPhaseRunning)
	run.Status.RunnerPodRef = &agentsv1alpha1.NamespacedObjectReference{Name: "run-pod", Namespace: "agents"}
	principal := testPrincipal(time.Now().Add(40 * time.Millisecond))
	server := testServer(t, run, staticAuthenticator{ready: true, principal: principal}, staticLogSource{contents: "2026-07-17T12:00:00Z only once\n"})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/namespaces/agents/agent-runs/run-1/events", nil)
	request.Header.Set("Authorization", "Bearer valid")
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if count := strings.Count(response.Body.String(), "only once"); count != 1 {
		t.Fatalf("expected one bounded log tail, got %d:\n%s", count, response.Body.String())
	}
}

func TestRunEventsStopsWhenAgentRunUIDChanges(t *testing.T) {
	run := testAgentRun(agentsv1alpha1.AgentRunPhaseRunning)
	replacement := run.DeepCopy()
	replacement.UID = types.UID("replacement-run-uid")
	replacement.ResourceVersion = "11"
	principal := testPrincipal(time.Now().Add(time.Hour))
	server := testServer(t, run, staticAuthenticator{ready: true, principal: principal}, staticLogSource{err: ErrLogsPending})
	server.runs = &replacingReader{Reader: server.runs, replacement: replacement}

	request := httptest.NewRequest(http.MethodGet, "/api/v1/namespaces/agents/agent-runs/run-1/events", nil)
	request.Header.Set("Authorization", "Bearer valid")
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if !strings.Contains(response.Body.String(), `"code":"replaced"`) {
		t.Fatalf("expected replacement terminal event:\n%s", response.Body.String())
	}
}

func TestRunEventsCancelsBlockedStatusReadAtTokenExpiry(t *testing.T) {
	run := testAgentRun(agentsv1alpha1.AgentRunPhaseRunning)
	principal := testPrincipal(time.Now().Add(150 * time.Millisecond))
	server := testServer(t, run, staticAuthenticator{ready: true, principal: principal}, staticLogSource{err: ErrLogsPending})
	server.config.Stream.StatusPollInterval = NewDuration(100 * time.Millisecond)
	reader := &blockingAfterFirstReader{
		Reader:   server.runs,
		entered:  make(chan struct{}, 1),
		canceled: make(chan struct{}, 1),
	}
	server.runs = reader

	request := httptest.NewRequest(http.MethodGet, "/api/v1/namespaces/agents/agent-runs/run-1/events", nil)
	requestContext, cancel := context.WithCancel(request.Context())
	request = request.WithContext(requestContext)
	request.Header.Set("Authorization", "Bearer valid")
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	cancel()
	if !strings.Contains(response.Body.String(), "token_expired") {
		t.Fatalf("expected token expiry event:\n%s", response.Body.String())
	}
	select {
	case <-reader.entered:
	default:
		t.Fatal("status read did not block before token expiry")
	}
	select {
	case <-reader.canceled:
	case <-time.After(time.Second):
		t.Fatal("blocked status read did not observe stream cancellation")
	}
}

func TestStreamLogBudgetRemainsCumulativeAcrossPodRollover(t *testing.T) {
	budget := streamByteBudget{max: 10}
	if !budget.add(6) {
		t.Fatal("first pod log unexpectedly exceeded budget")
	}
	// A new Pod must share the same per-connection budget.
	if budget.add(5) {
		t.Fatal("pod rollover reset the stream byte budget")
	}
	if budget.used != 6 {
		t.Fatalf("rejected bytes changed budget usage: %d", budget.used)
	}
}

func TestStreamLimiterEnforcesGlobalAndPerSubjectCaps(t *testing.T) {
	limiter := newStreamLimiter(2, 1)
	releaseFirst, ok := limiter.acquire("first")
	if !ok {
		t.Fatal("first stream was rejected")
	}
	if _, ok := limiter.acquire("first"); ok {
		t.Fatal("per-subject stream cap was not enforced")
	}
	releaseSecond, ok := limiter.acquire("second")
	if !ok {
		t.Fatal("second subject stream was rejected")
	}
	if _, ok := limiter.acquire("third"); ok {
		t.Fatal("global stream cap was not enforced")
	}
	releaseFirst()
	releaseSecond()
	if release, ok := limiter.acquire("first"); !ok {
		t.Fatal("released stream slots were not reusable")
	} else {
		release()
	}
}

func TestCORSRequiresExactConfiguredOrigin(t *testing.T) {
	server := testServer(t, nil, staticAuthenticator{ready: true}, staticLogSource{})
	request := httptest.NewRequest(http.MethodOptions, "/api/v1/namespaces/agents/agent-runs", nil)
	request.Header.Set("Origin", "https://evil.example")
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("expected origin denial, got %d", response.Code)
	}
}

func testServer(t *testing.T, run *agentsv1alpha1.AgentRun, authenticator AccessTokenAuthenticator, logs AgentRunLogSource) *Server {
	t.Helper()
	config := DefaultConfig()
	config.OIDC.Issuer = "https://issuer.example"
	config.OIDC.Audiences = []string{"anvil-agents-api"}
	config.Authorization.Bindings = []AuthorizationBinding{{
		Roles:       []string{"viewer"},
		Permissions: []string{PermissionRunsRead, PermissionRunsStream},
		Namespaces:  []string{"agents"},
	}}
	config.Stream.StatusPollInterval = NewDuration(5 * time.Millisecond)
	config.Stream.HeartbeatInterval = NewDuration(10 * time.Millisecond)
	config.Stream.MaxDuration = NewDuration(time.Second)
	scheme := runtime.NewScheme()
	if err := agentsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	builder := fake.NewClientBuilder().WithScheme(scheme)
	if run != nil {
		builder = builder.WithObjects(run)
	}
	server, err := NewServer(config, authenticator, builder.Build(), logs, logr.Discard())
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func testPrincipal(expiry time.Time) Principal {
	return Principal{Subject: "user-1", Issuer: "https://issuer.example", Roles: []string{"viewer"}, Expiry: expiry}
}

func testAgentRun(phase agentsv1alpha1.AgentRunPhase) *agentsv1alpha1.AgentRun {
	return &agentsv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "run-1",
			Namespace:         "agents",
			UID:               types.UID("run-uid"),
			ResourceVersion:   "10",
			CreationTimestamp: metav1.NewTime(time.Now().Add(-time.Minute)),
		},
		Spec: agentsv1alpha1.AgentRunSpec{
			SourceRef: agentsv1alpha1.AgentRunSourceRef{Kind: "Application", Name: "app-1"},
		},
		Status: agentsv1alpha1.AgentRunStatus{Phase: phase, Backend: "codex", Output: "captured output"},
	}
}
