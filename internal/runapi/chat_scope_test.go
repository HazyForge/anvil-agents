package runapi

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	agents "github.com/hazyforge/anvil-agents/api/v1alpha1"
	"github.com/hazyforge/anvil-agents/internal/chat"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func scopedHarnessFixture(t *testing.T, s *Server) chat.Thread {
	t.Helper()
	ctx := context.Background()
	volume := &agents.AgentDataVolume{ObjectMeta: metav1.ObjectMeta{Name: "agy-home", Namespace: "agents"}, Spec: agents.AgentDataVolumeSpec{ApplicationRef: &agents.ApplicationReferenceSpec{Name: "desktop-chat"}}}
	harness := &agents.AgentHarnessProfile{ObjectMeta: metav1.ObjectMeta{Name: "agy-scoped", Namespace: "agents"}, Spec: agents.AgentHarnessProfileSpec{Execution: agents.AgentRunHarnessExecutionSpec{DataVolumeRefs: []agents.AgentRunDataVolumeRef{{Name: volume.Name}}}}}
	for _, obj := range []client.Object{volume, harness} {
		if err := s.writes.Create(ctx, obj); err != nil {
			t.Fatal(err)
		}
	}
	thread, err := s.chatStore.CreateThread(ctx, chat.Thread{Namespace: "agents", Mode: "persona", CreatedBy: "user", Metadata: json.RawMessage(`{"harnessProfileName":"agy-scoped","applicationName":"untrusted-metadata"}`)})
	if err != nil {
		t.Fatal(err)
	}
	return thread
}
func TestHarnessOnlyDerivesScopedHomeApplication(t *testing.T) {
	ctx := context.Background()
	s := chatTestServer(t, true)
	thread := scopedHarnessFixture(t, s)
	result, err := s.queueChatTurn(ctx, "agents", thread.ID, AppendChatMessageRequest{Content: "hello scoped harness"})
	if err != nil {
		t.Fatal(err)
	}
	run := &agents.AgentRun{}
	if err = s.writes.Get(ctx, types.NamespacedName{Namespace: "agents", Name: result.Turn.RunName}, run); err != nil {
		t.Fatal(err)
	}
	if run.Spec.ProfileRef != nil || run.Spec.Scope.ApplicationRef == nil || run.Spec.Scope.ApplicationRef.Name != "desktop-chat" {
		t.Fatalf("incorrect standalone scope %#v", run.Spec)
	}
	stored, err := s.chatStore.ListTurns(ctx, "agents", thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	frozen := &agents.AgentRun{}
	if err = json.Unmarshal(stored[0].RunJSON, frozen); err != nil {
		t.Fatal(err)
	}
	if frozen.Spec.Scope.ApplicationRef == nil || frozen.Spec.Scope.ApplicationRef.Name != "desktop-chat" {
		t.Fatal("application scope was not frozen into accepted execution")
	}
}
func TestHarnessOnlyRejectsConflictingScopedHomes(t *testing.T) {
	ctx := context.Background()
	s := chatTestServer(t, true)
	thread := scopedHarnessFixture(t, s)
	other := &agents.AgentDataVolume{ObjectMeta: metav1.ObjectMeta{Name: "other-home", Namespace: "agents"}, Spec: agents.AgentDataVolumeSpec{ApplicationRef: &agents.ApplicationReferenceSpec{Name: "other-application"}}}
	if err := s.writes.Create(ctx, other); err != nil {
		t.Fatal(err)
	}
	h := &agents.AgentHarnessProfile{}
	if err := s.writes.Get(ctx, types.NamespacedName{Namespace: "agents", Name: "agy-scoped"}, h); err != nil {
		t.Fatal(err)
	}
	h.Spec.Execution.DataVolumeRefs = append(h.Spec.Execution.DataVolumeRefs, agents.AgentRunDataVolumeRef{Name: other.Name, ReadOnly: true})
	if err := s.writes.Update(ctx, h); err != nil {
		t.Fatal(err)
	}
	if _, err := s.queueChatTurn(ctx, "agents", thread.ID, AppendChatMessageRequest{Content: "should reject"}); !errors.Is(err, chat.ErrInvalid) {
		t.Fatalf("expected scope rejection: %v", err)
	}
	runs := &agents.AgentRunList{}
	_ = s.writes.List(ctx, runs)
	if len(runs.Items) != 0 {
		t.Fatal("conflicting volume scopes executed")
	}
}
func TestProfileScopeMustMatchScopedHomes(t *testing.T) {
	s := chatTestServer(t, true)
	thread := scopedHarnessFixture(t, s)
	thread.ProfileName = "grok45"
	if _, err := s.resolveChatApplication(context.Background(), thread); err == nil {
		t.Fatal("unscoped profile silently acquired scoped harness authority")
	}
}
