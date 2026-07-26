package runapi

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

type deadlineRecorder struct {
	*httptest.ResponseRecorder
	deadlines []time.Time
}

func (writer *deadlineRecorder) SetWriteDeadline(deadline time.Time) error {
	writer.deadlines = append(writer.deadlines, deadline)
	return nil
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

func TestListRejectsNamespacesAboveConfiguredObjectBound(t *testing.T) {
	authenticator := staticAuthenticator{ready: true, principal: testPrincipal(time.Now().Add(time.Hour))}
	server := testServer(t, nil, authenticator, staticLogSource{})
	server.config.List.MaxItems = 2
	scheme := runtime.NewScheme()
	if err := agentsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	objects := make([]client.Object, 0, 3)
	for index := 0; index < 3; index++ {
		run := testAgentRun(agentsv1alpha1.AgentRunPhaseRunning)
		run.Name = fmt.Sprintf("run-%d", index)
		run.UID = types.UID(fmt.Sprintf("run-%d-uid", index))
		objects = append(objects, run)
	}
	server.runs = fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()

	request := httptest.NewRequest(http.MethodGet, "/api/v1/namespaces/agents/agent-runs", nil)
	request.Header.Set("Authorization", "Bearer valid")
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "list_too_large") {
		t.Fatalf("response = %d %s, want bounded-list rejection", response.Code, response.Body.String())
	}
}

func TestJSONResponsesSetAndClearWriteDeadline(t *testing.T) {
	writer := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	writeJSON(writer, http.StatusOK, map[string]bool{"ok": true})
	if len(writer.deadlines) != 2 || writer.deadlines[0].IsZero() || !writer.deadlines[1].IsZero() {
		t.Fatalf("write deadlines = %#v, want bounded deadline followed by clear", writer.deadlines)
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

func TestUIConfigExposesPublicOIDCSettings(t *testing.T) {
	server := testServer(t, nil, staticAuthenticator{ready: true}, staticLogSource{})
	server.config.UI.ProductTitle = "Anvil Agents Console"
	server.config.UI.DefaultNamespaces = []string{"hazy-trade"}
	server.config.UI.OIDC.ClientID = "console-client"
	server.config.OIDC.Issuer = "https://issuer.example"
	server.config.OIDC.Audiences = []string{"anvil-agents"}
	request := httptest.NewRequest(http.MethodGet, "/ui-config.json", nil)
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("expected ui-config 200, got %d %s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, want := range []string{
		`"productTitle":"Anvil Agents Console"`,
		`"clientId":"console-client"`,
		`"issuer":"https://issuer.example"`,
		`urn:zitadel:iam:org:project:id:anvil-agents:aud`,
		`"hazy-trade"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("ui-config missing %q in %s", want, body)
		}
	}
	if csp := response.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") {
		t.Fatalf("expected API CSP for ui-config, got %q", csp)
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

func TestCORSAllowsExactConfiguredOrigin(t *testing.T) {
	server := testServer(t, nil, staticAuthenticator{ready: true}, staticLogSource{})
	server.config.CORS.AllowedOrigins = []string{"https://agents.example.com"}
	request := httptest.NewRequest(http.MethodOptions, "/api/v1/namespaces/agents/agent-runs", nil)
	request.Host = "agents.example.com"
	request.Header.Set("Origin", "https://agents.example.com")
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("expected exact configured origin allow, got %d %s", response.Code, response.Body.String())
	}
	if response.Header().Get("Access-Control-Allow-Origin") != "https://agents.example.com" {
		t.Fatalf("missing ACAO header: %#v", response.Header())
	}
}

func TestCORSRejectsSameHostWithoutConfiguredOrigin(t *testing.T) {
	server := testServer(t, nil, staticAuthenticator{ready: true}, staticLogSource{})
	request := httptest.NewRequest(http.MethodOptions, "/api/v1/namespaces/agents/agent-runs", nil)
	request.Host = "agents.example.com"
	request.Header.Set("Origin", "https://agents.example.com")
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("expected unconfigured same-host origin denial, got %d %s", response.Code, response.Body.String())
	}
}

func TestCORSRejectsSchemeMismatchedSameHostOrigin(t *testing.T) {
	server := testServer(t, nil, staticAuthenticator{ready: true}, staticLogSource{})
	server.config.CORS.AllowedOrigins = []string{"https://agents.example.com"}
	request := httptest.NewRequest(http.MethodOptions, "/api/v1/namespaces/agents/agent-runs", nil)
	request.Host = "agents.example.com"
	request.Header.Set("Origin", "http://agents.example.com")
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("expected http/https scheme mismatch denial, got %d %s", response.Code, response.Body.String())
	}
}

func TestConsoleIndexIsServedAtRoot(t *testing.T) {
	server := testServer(t, nil, staticAuthenticator{ready: true}, staticLogSource{})
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("expected console index, got %d %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "Anvil Agents Console") {
		t.Fatalf("unexpected console body: %s", response.Body.String())
	}
	if csp := response.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self'") {
		t.Fatalf("expected SPA CSP, got %q", csp)
	}
}

func TestConsoleSPAFallbackServesIndex(t *testing.T) {
	server := testServer(t, nil, staticAuthenticator{ready: true}, staticLogSource{})
	request := httptest.NewRequest(http.MethodGet, "/ns/hazy-trade/runs/run-1", nil)
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Anvil Agents Console") {
		t.Fatalf("expected SPA fallback index, got %d %s", response.Code, response.Body.String())
	}
}

func TestConsoleDoesNotCaptureAPIOrProbes(t *testing.T) {
	server := testServer(t, nil, staticAuthenticator{ready: true}, staticLogSource{})
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"ok":true`) {
		t.Fatalf("healthz captured by UI: %d %s", response.Code, response.Body.String())
	}
	if csp := response.Header().Get("Content-Security-Policy"); csp != "default-src 'none'; frame-ancestors 'none'" {
		t.Fatalf("expected API CSP on probes, got %q", csp)
	}
}

func TestUnregisteredAPIPathsDoNotServeSPA(t *testing.T) {
	server := testServer(t, nil, staticAuthenticator{ready: true}, staticLogSource{})
	cases := []struct {
		path       string
		wantStatus int
		wantBody   string
		wantCSP    string
	}{
		{
			path:       "/api",
			wantStatus: http.StatusNotFound,
			wantBody:   "not_found",
			wantCSP:    "default-src 'none'; frame-ancestors 'none'",
		},
		{
			path:       "/api/v1/unknown",
			wantStatus: http.StatusNotFound,
			wantBody:   "not_found",
			wantCSP:    "default-src 'none'; frame-ancestors 'none'",
		},
		{
			path:       "/api/v1/namespaces/agents/nope",
			wantStatus: http.StatusNotFound,
			wantBody:   "not_found",
			wantCSP:    "default-src 'none'; frame-ancestors 'none'",
		},
	}
	for _, tc := range cases {
		request := httptest.NewRequest(http.MethodGet, tc.path, nil)
		response := httptest.NewRecorder()
		server.routes().ServeHTTP(response, request)
		if response.Code != tc.wantStatus {
			t.Fatalf("%s: status %d, want %d body=%s", tc.path, response.Code, tc.wantStatus, response.Body.String())
		}
		if strings.Contains(response.Body.String(), "<!doctype html") || strings.Contains(response.Body.String(), "<html") {
			t.Fatalf("%s: SPA HTML leaked for unregistered API path: %s", tc.path, response.Body.String())
		}
		if !strings.Contains(response.Body.String(), tc.wantBody) {
			t.Fatalf("%s: body %q missing %q", tc.path, response.Body.String(), tc.wantBody)
		}
		if csp := response.Header().Get("Content-Security-Policy"); csp != tc.wantCSP {
			t.Fatalf("%s: CSP %q, want %q", tc.path, csp, tc.wantCSP)
		}
	}
}

func TestConsoleStaticDirOverride(t *testing.T) {
	dir := t.TempDir()
	index := filepath.Join(dir, "index.html")
	if err := os.WriteFile(index, []byte("<!doctype html><title>override-console</title>"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := testServer(t, nil, staticAuthenticator{ready: true}, staticLogSource{})
	server.config.UI.StaticDir = dir
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "override-console") {
		t.Fatalf("expected staticDir override, got %d %s", response.Code, response.Body.String())
	}
}

func TestConsoleMissingAssetWithExtensionIsNotFound(t *testing.T) {
	server := testServer(t, nil, staticAuthenticator{ready: true}, staticLogSource{})
	request := httptest.NewRequest(http.MethodGet, "/assets/missing.js", nil)
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing asset, got %d %s", response.Code, response.Body.String())
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
