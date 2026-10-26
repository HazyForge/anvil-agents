package chat

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
)

// The shared contract exercises the two independently implemented stores. It
// owns isolation, merge provenance, receipt idempotency, and lock cancellation;
// API tests own authorization and the actual routing/dispatch lifecycle.
func TestMemorySubjectStore(t *testing.T) { exerciseSubjectStore(t, NewMemoryStore()) }

type subjectChatStore interface {
	Store
	SubjectStore
}

func exerciseSubjectStore(t *testing.T, s subjectChatStore) RoutingRequest {
	t.Helper()
	ctx := context.Background()
	ns := "subject-test-" + uuid.NewString()
	owner := "subject-owner"
	makeSubject := func(label string) Subject {
		thread, err := s.CreateThread(ctx, Thread{Namespace: ns, Mode: ModePersona, ProfileName: "manager", CreatedBy: owner})
		if err != nil {
			t.Fatal(err)
		}
		subject, err := s.PutSubject(ctx, Subject{Namespace: ns, Owner: owner, Kind: "project", Label: label, Summary: "Work on " + label, ThreadID: thread.ID, ProfileName: thread.ProfileName})
		if err != nil {
			t.Fatal(err)
		}
		return subject
	}
	a, b := makeSubject("Launch"), makeSubject("Website")
	if _, _, err := s.AppendMessages(ctx, ns, a.ThreadID, []Message{{Role: RoleUser, Content: "Keep the launch discussion"}}); err != nil {
		t.Fatal(err)
	}
	for _, scope := range [][2]string{{ns, "another-owner"}, {"another-namespace", owner}} {
		got, err := s.ListSubjects(ctx, scope[0], scope[1], 10)
		if err != nil || len(got) != 0 {
			t.Fatalf("scope leak: %+v %v", got, err)
		}
		if _, err = s.GetSubject(ctx, scope[0], scope[1], a.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("foreign subject read: %v", err)
		}
		forged := a
		forged.Namespace, forged.Owner = scope[0], scope[1]
		if _, err = s.PutSubject(ctx, forged); !errors.Is(err, ErrNotFound) {
			t.Fatalf("foreign target linked: %v", err)
		}
	}
	a.Status = "done"
	if _, err := s.PutSubject(ctx, a); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListSubjects(ctx, ns, owner, 1)
	if err != nil || len(got) != 1 || got[0].ID != b.ID {
		t.Fatalf("completed subject displaced active work: %+v %v", got, err)
	}
	touched, err := s.TouchSubject(ctx, ns, owner, a.ID, "Launch now needs a website")
	if err != nil || touched.Status != "open" || touched.Summary != "Launch now needs a website" {
		t.Fatalf("subject did not reopen: %+v %v", touched, err)
	}
	merged, err := s.MergeSubjects(ctx, ns, owner, a.ID, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(merged.Aliases, "Launch") || merged.Summary == b.Summary {
		t.Fatalf("merge lost source context: %+v", merged)
	}
	source, err := s.GetSubject(ctx, ns, owner, a.ID)
	if err != nil || source.MergedInto != b.ID || source.Status != "merged" {
		t.Fatalf("merge did not retain provenance: %+v %v", source, err)
	}
	got, err = s.ListSubjects(ctx, ns, owner, 10)
	if err != nil || len(got) != 1 || got[0].ID != b.ID {
		t.Fatalf("merged source still offered: %+v %v", got, err)
	}
	messages, err := s.ListMessages(ctx, ns, a.ThreadID)
	if err != nil || len(messages) != 1 || messages[0].Content != "Keep the launch discussion" {
		t.Fatalf("merge altered transcript: %+v %v", messages, err)
	}
	if _, err = s.MergeSubjects(ctx, ns, owner, b.ID, a.ID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("merge cycle accepted: %v", err)
	}

	request := RoutingRequest{ID: uuid.NewString(), Namespace: ns, Owner: owner, RequestHash: "content-and-profile-hash", Content: "How is the launch going?", ProfileName: "manager", Status: "routing", Events: json.RawMessage(`[{"step":"received"}]`)}
	request, err = s.SaveRoutingRequest(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if request.RequestHash == "" {
		t.Fatal("request binding lost")
	}
	for _, scope := range [][2]string{{ns, "another-owner"}, {"another-namespace", owner}} {
		if _, err = s.GetRoutingRequest(ctx, scope[0], scope[1], request.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("foreign receipt read: %v", err)
		}
		receipts, e := s.ListRoutingRequests(ctx, scope[0], scope[1], 10)
		if e != nil || len(receipts) != 0 {
			t.Fatalf("foreign receipts: %+v %v", receipts, e)
		}
	}
	conflict := request
	conflict.RequestHash = "different-message"
	if _, err = s.SaveRoutingRequest(ctx, conflict); !errors.Is(err, ErrRequestConflict) {
		t.Fatalf("idempotency key reused: %v", err)
	}
	request.Status = "clarify"
	request.Events = json.RawMessage(`[{"step":"received"},{"step":"clarify"}]`)
	request, err = s.SaveRoutingRequest(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	// Resolving a clarification changes only route fields, retaining its message.
	request.Status = "accepted"
	request.SubjectID = b.ID
	request.ThreadID = b.ThreadID
	request.TurnID = uuid.NewString()
	request.Result = json.RawMessage(`{"accepted":true}`)
	request, err = s.SaveRoutingRequest(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	stale := request
	stale.Status = "routing"
	stale.SubjectID = a.ID
	stale.Result = nil
	stale.Events = json.RawMessage(`[]`)
	preserved, err := s.SaveRoutingRequest(ctx, stale)
	var receiptResult struct {
		Accepted bool `json:"accepted"`
	}
	resultErr := json.Unmarshal(preserved.Result, &receiptResult)
	if err != nil || resultErr != nil || !receiptResult.Accepted || preserved.Status != "accepted" || preserved.SubjectID != b.ID || preserved.TurnID != request.TurnID {
		t.Fatalf("accepted receipt changed: %+v %v", preserved, err)
	}

	lockID := uuid.NewString()
	release, err := s.AcquireRoutingRequest(ctx, ns, owner, lockID)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	otherRelease, err := s.AcquireRoutingRequest(ctx, ns, "another-owner", lockID)
	if err != nil {
		t.Fatal(err)
	}
	otherRelease()
	waitCtx, cancel := context.WithTimeout(ctx, 80*time.Millisecond)
	if unlock, e := s.AcquireRoutingRequest(waitCtx, ns, owner, lockID); e == nil {
		unlock()
		t.Fatal("same routing request acquired twice")
	} else if !errors.Is(e, context.DeadlineExceeded) {
		t.Fatalf("lock wait did not honor cancellation: %v", e)
	}
	cancel()
	release()
	release() // release is safe on deferred and explicit paths.
	resumedRelease, err := s.AcquireRoutingRequest(ctx, ns, owner, lockID)
	if err != nil {
		t.Fatal(err)
	}
	resumedRelease()
	return request
}

func TestPostgresSubjectStoreIntegration(t *testing.T) {
	databaseURL := os.Getenv("ANVIL_AGENTS_TEST_CHAT_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("ANVIL_AGENTS_TEST_ARCHIVE_DATABASE_URL")
	}
	if databaseURL == "" {
		t.Skip("set ANVIL_AGENTS_TEST_CHAT_DATABASE_URL for durable subject integration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store, err := OpenPostgresStore(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	// The shared behavior runs against actual SQL, including constrained ownership
	// and independent advisory-lock connections.
	receipt := exerciseSubjectStore(t, store)
	store.Close()
	reopened, err := OpenPostgresStore(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	defer func() {
		_, _ = reopened.pool.Exec(ctx, `DELETE FROM anvil_agents_chat.routing_requests WHERE namespace=$1`, receipt.Namespace)
		_, _ = reopened.pool.Exec(ctx, `DELETE FROM anvil_agents_chat.threads WHERE namespace=$1`, receipt.Namespace)
	}()
	durable, err := reopened.GetRoutingRequest(ctx, receipt.Namespace, receipt.Owner, receipt.ID)
	if err != nil || durable.Status != "accepted" || durable.TurnID != receipt.TurnID || durable.RequestHash != receipt.RequestHash {
		t.Fatalf("restart lost accepted handoff: %+v %v", durable, err)
	}
	subject, err := reopened.GetSubject(ctx, receipt.Namespace, receipt.Owner, receipt.SubjectID)
	if err != nil || subject.ThreadID != receipt.ThreadID || !slices.Contains(subject.Aliases, "Launch") {
		t.Fatalf("restart lost subject context: %+v %v", subject, err)
	}
	// The SQL query itself must filter private records before LIMIT; the API
	// tests independently check that the authenticated owner reaches this filter.
	paginationNamespace := receipt.Namespace + "-page"
	shared, err := reopened.CreateThread(ctx, Thread{Namespace: paginationNamespace, Mode: ModeFleet, CreatedBy: "shared-author", Title: "Shared history", CreatedAt: time.Now().Add(-time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = reopened.pool.Exec(ctx, `DELETE FROM anvil_agents_chat.threads WHERE namespace=$1`, paginationNamespace)
	}()
	if _, err = reopened.CreateThread(ctx, Thread{Namespace: paginationNamespace, Mode: ModeFleet, CreatedBy: receipt.Owner, Title: "New private history", Metadata: json.RawMessage(`{"subjectOwner":"subject-owner"}`)}); err != nil {
		t.Fatal(err)
	}
	visible, err := reopened.ListThreads(ctx, ThreadFilter{Namespace: paginationNamespace, VisibleTo: "another-owner", Limit: 1})
	if err != nil || len(visible) != 1 || visible[0].ID != shared.ID {
		t.Fatalf("SQL private record consumed visible page: %+v %v", visible, err)
	}
}
