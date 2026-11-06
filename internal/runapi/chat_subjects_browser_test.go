package runapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	agentsv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
	"github.com/hazyforge/anvil-agents/internal/chat"
	"github.com/hazyforge/anvil-agents/internal/subjectrouting"
	"github.com/jackc/pgx/v5"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// This opt-in browser test serves the built Desktop against the real API and
// PostgreSQL. Only identity, classifier judgments and harness execution are
// explicit fixtures. JavaScript must not intercept or manufacture API replies.
func TestSubjectManagerBrowser(t *testing.T) {
	if os.Getenv("ANVIL_SUBJECT_BROWSER") != "1" {
		t.Skip("set ANVIL_SUBJECT_BROWSER=1 for the built Desktop / API / PostgreSQL flow")
	}
	databaseURL := os.Getenv("ANVIL_AGENTS_TEST_CHAT_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("ANVIL_AGENTS_CHAT_DATABASE_URL")
	}
	parsed, err := url.Parse(databaseURL)
	if err != nil || (parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "localhost" && parsed.Hostname() != "::1") {
		t.Fatal("browser fixture requires a disposable loopback ANVIL_AGENTS_TEST_CHAT_DATABASE_URL")
	}
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dist := filepath.Join(repoRoot, "web", "desktop", "dist")
	if _, err = os.Stat(filepath.Join(dist, "index.html")); err != nil {
		t.Fatal("build web/desktop before the browser test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	store, err := chat.OpenPostgresStore(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ownerPrefix := "browser-" + uuid.NewString() + "-"
	defer func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		conn, e := pgx.Connect(cleanupCtx, databaseURL)
		if e != nil {
			t.Errorf("browser fixture cleanup: %v", e)
			return
		}
		defer conn.Close(cleanupCtx)
		for _, owner := range []string{ownerPrefix + "alice", ownerPrefix + "bob"} {
			for _, statement := range []string{`DELETE FROM anvil_agents_chat.turns WHERE thread_id IN (SELECT id FROM anvil_agents_chat.threads WHERE namespace='agents' AND created_by=$1)`, `DELETE FROM anvil_agents_chat.routing_requests WHERE namespace='agents' AND owner=$1`, `DELETE FROM anvil_agents_chat.threads WHERE namespace='agents' AND created_by=$1`} {
				if _, e = conn.Exec(cleanupCtx, statement, owner); e != nil {
					t.Errorf("browser fixture cleanup: %v", e)
				}
			}
		}
	}()
	server := subjectTestServer(t)
	server.SetChatStore(store)
	server.authenticator = subjectTestAuthenticator{ownerPrefix: ownerPrefix}
	server.config.UI.ProductTitle = "Anvil Agents Desktop"
	server.config.UI.DefaultNamespaces = []string{"agents"}
	server.config.UI.OIDC.ClientID = "browser-fixture"
	server.config.Composition.ReadEnabled = true
	server.config.Authorization.Bindings[0].Permissions = append(server.config.Authorization.Bindings[0].Permissions, PermissionCompositionRead)
	server.authorizer = NewAuthorizer(server.config.Authorization)
	if err = server.writes.Delete(ctx, &agentsv1alpha1.AgentRunProfile{ObjectMeta: metav1.ObjectMeta{Name: "grok45", Namespace: "agents"}}); err != nil {
		t.Fatal(err)
	}
	server.SetSubjectDecisionProvider(subjectDecisionFunc(func(ctx context.Context, input subjectrouting.Input) (subjectrouting.Decision, error) {
		timer := time.NewTimer(350 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return subjectrouting.Decision{}, ctx.Err()
		}
		switch input.Content {
		case "Build a launch website", "Start a documentation project":
			return subjectDecision(input, subjectrouting.NewSubject, subjectrouting.IntentNewWork), nil
		case "How is the launch website going?", "Add a contact page to the launch website":
			for _, candidate := range input.Candidates {
				if strings.Contains(strings.ToLower(candidate.Label), "launch") {
					intent := subjectrouting.IntentStatus
					if strings.HasPrefix(input.Content, "Add ") {
						intent = subjectrouting.IntentAmendRunning
					}
					return subjectDecision(input, candidate.ID, intent), nil
				}
			}
		case "Which one should get this?":
			if len(input.Candidates) > 0 {
				decision := subjectDecision(input, input.Candidates[0].ID, subjectrouting.IntentQuestion)
				decision.SubjectConfidence = .5
				decision.SubjectProbabilities[input.Candidates[0].ID] = .5
				decision.SubjectProbabilities[subjectrouting.NewSubject] = .5
				return decision, nil
			}
		}
		return subjectrouting.Decision{}, subjectrouting.ErrUnavailable
	}))
	var api http.Handler
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/local/v1/snapshot":
			origin := "http://" + r.Host
			writeJSON(w, 200, map[string]any{"productTitle": "Anvil Agents Desktop", "prefs": map[string]string{"apiOrigin": origin}, "api": map[string]any{"origin": origin, "reachable": true, "message": "Local browser fixture"}, "harnesses": []any{}, "wrapper": map[string]any{"tools": []any{}, "message": "Explicit test fixture"}, "wsl": map[string]bool{"available": false}, "harnessTarget": "native"})
		case strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/ui-config.json" || r.URL.Path == "/healthz":
			api.ServeHTTP(w, r)
		case strings.HasPrefix(r.URL.Path, "/assets/"):
			http.FileServer(http.Dir(dist)).ServeHTTP(w, r)
		default:
			http.ServeFile(w, r, filepath.Join(dist, "index.html"))
		}
	}))
	defer httpServer.Close()
	server.config.CORS.AllowedOrigins = []string{httpServer.URL}
	api = server.routes()
	// Complete fake Kubernetes runs, then let normal production reconciliation
	// persist each assistant reply and activate deferred work.
	runnerCtx, stopRunner := context.WithCancel(ctx)
	runnerDone := make(chan struct{})
	runnerErrors := make(chan error, 1)
	go func() {
		defer close(runnerDone)
		ticker := time.NewTicker(25 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-runnerCtx.Done():
				return
			case <-ticker.C:
				list := &agentsv1alpha1.AgentRunList{}
				if e := server.runs.List(runnerCtx, list); e != nil {
					select {
					case runnerErrors <- e:
					default:
					}
					return
				}
				for i := range list.Items {
					run := &list.Items[i]
					if run.Status.Phase == agentsv1alpha1.AgentRunPhaseSucceeded {
						continue
					}
					run.Status.Phase = agentsv1alpha1.AgentRunPhaseSucceeded
					run.Status.Output = "Fixture harness reply: this subject turn completed."
					if e := server.writes.Status().Update(runnerCtx, run); e != nil && !apierrors.IsConflict(e) {
						select {
						case runnerErrors <- e:
						default:
						}
						return
					}
				}
				turns, e := store.PendingTurns(runnerCtx, 100)
				if e != nil {
					select {
					case runnerErrors <- e:
					default:
					}
					return
				}
				for i := range turns {
					if e = server.reconcileChatTurn(runnerCtx, &turns[i]); e != nil && runnerCtx.Err() == nil {
						select {
						case runnerErrors <- e:
						default:
						}
						return
					}
				}
			}
		}
	}()
	defer func() { stopRunner(); <-runnerDone }()
	command := exec.CommandContext(ctx, "node", filepath.Join(repoRoot, "hack", "desktop-subject-manager-live.browser-test.mjs"))
	command.Dir = repoRoot
	command.Env = append(os.Environ(), "ANVIL_SUBJECT_API_ORIGIN="+httpServer.URL)
	output, err := command.CombinedOutput()
	t.Logf("browser fixture output:\n%s", output)
	if err != nil {
		t.Fatalf("browser driver: %v", err)
	}
	select {
	case err = <-runnerErrors:
		t.Fatal(fmt.Errorf("fixture harness: %w", err))
	default:
	}
	receipts, err := store.ListRoutingRequests(ctx, "agents", ownerPrefix+"alice", 50)
	if err != nil || len(receipts) == 0 {
		t.Fatalf("browser did not persist real routing receipts: %d %v", len(receipts), err)
	}
}
