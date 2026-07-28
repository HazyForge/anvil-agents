package runapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	agentsv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
	"github.com/hazyforge/anvil-agents/internal/archive"
)

type memoryArchiveStore struct {
	rows map[string]struct{}
}

func (s *memoryArchiveStore) ArchiveAgentRun(context.Context, archive.AgentRunArchiveRecord) (archive.AgentRunArchiveResult, error) {
	return archive.AgentRunArchiveResult{}, nil
}

func (s *memoryArchiveStore) ListAgentRunArchives(context.Context, string, int) ([]archive.AgentRunArchiveListItem, error) {
	return nil, nil
}

func (s *memoryArchiveStore) HasAgentRunArchive(_ context.Context, namespace, name, uid string) (bool, error) {
	_, ok := s.rows[namespace+"/"+name+"/"+uid]
	return ok, nil
}

func (s *memoryArchiveStore) Close() {}

func TestPurgeRunsDeletesArchivedTerminalOnly(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := agentsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	archived := metav1.NewTime(now.Add(-time.Hour))
	completed := metav1.NewTime(now.Add(-2 * time.Hour))
	old := &agentsv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "old-failed",
			Namespace:         "agents",
			UID:               "uid-old",
			CreationTimestamp: metav1.NewTime(now.Add(-3 * time.Hour)),
		},
		Status: agentsv1alpha1.AgentRunStatus{
			Phase:       agentsv1alpha1.AgentRunPhaseFailed,
			CompletedAt: &completed,
			Archive: &agentsv1alpha1.AgentRunArchiveStatus{
				Store:      archive.AgentRunArchiveStorePostgres,
				ArchivedAt: &archived,
				Digest:     "sha256:old",
			},
		},
	}
	running := &agentsv1alpha1.AgentRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "still-running",
			Namespace:         "agents",
			UID:               "uid-run",
			CreationTimestamp: metav1.NewTime(now.Add(-time.Minute)),
		},
		Status: agentsv1alpha1.AgentRunStatus{Phase: agentsv1alpha1.AgentRunPhaseRunning},
	}
	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(old, running).Build()
	store := &memoryArchiveStore{rows: map[string]struct{}{
		"agents/old-failed/uid-old": {},
	}}

	config := DefaultConfig()
	config.OIDC.Issuer = "https://issuer.example"
	config.OIDC.Audiences = []string{"anvil-agents-api"}
	config.Runs.PurgeEnabled = true
	config.Authorization.Bindings = []AuthorizationBinding{{
		Roles:       []string{"viewer"},
		Permissions: []string{PermissionRunsRead, PermissionRunsPurge},
		Namespaces:  []string{"agents"},
	}}
	principal := testPrincipal(time.Now().Add(time.Hour))
	// Ensure purge permission is present on the principal used by the static authenticator.
	// testPrincipal is shaped for the default bindings used by other tests; override roles.
	server, err := NewServerWithArchive(config, staticAuthenticator{
		ready:     true,
		principal: principal,
	}, client, staticLogSource{}, store, logr.Discard())
	if err != nil {
		t.Fatal(err)
	}

	body, _ := json.Marshal(AgentRunPurgeRequest{KeepLatest: 1, KeepPerSchedule: 1, DryRun: false})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/namespaces/agents/agent-runs/purge", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer valid")
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	server.routes().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	var response AgentRunPurgeResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Deleted) != 1 || response.Deleted[0] != "old-failed" {
		t.Fatalf("deleted = %#v", response.Deleted)
	}
	list := &agentsv1alpha1.AgentRunList{}
	if err := client.List(request.Context(), list); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 1 || list.Items[0].Name != "still-running" {
		t.Fatalf("remaining = %#v", list.Items)
	}
}
