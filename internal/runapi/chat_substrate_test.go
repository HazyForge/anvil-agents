package runapi

import (
	"encoding/json"
	"testing"

	agentsv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
	"github.com/hazyforge/anvil-agents/internal/chat"
)

// A standing-chat turn addresses its harness only through the thread's
// profile/harness selection. The Substrate spike adds no new chat endpoint:
// selecting a SubstrateActor harness profile must survive turn construction so
// composition resolves the actor runtime while the default Job path is
// untouched for every other harness.
func TestBuildChatRunPreservesSubstrateHarnessSelection(t *testing.T) {
	t.Parallel()

	metadata, err := json.Marshal(map[string]string{"harnessProfileName": "substrate-chat"})
	if err != nil {
		t.Fatalf("metadata: %v", err)
	}
	thread := chat.Thread{ID: "thread-1", Namespace: "agents", ProfileName: "desktop-assistant", Metadata: metadata}
	run, err := buildChatRun("agents", "chat-turn-1", "hello", thread)
	if err != nil {
		t.Fatalf("buildChatRun: %v", err)
	}
	if run.Spec.ProfileRef == nil || run.Spec.ProfileRef.Name != "desktop-assistant" {
		t.Fatalf("chat run profile ref = %+v, want desktop-assistant", run.Spec.ProfileRef)
	}
	if run.Spec.HarnessProfileRef == nil || run.Spec.HarnessProfileRef.Name != "substrate-chat" {
		t.Fatalf("chat run harness profile ref = %+v, want substrate-chat", run.Spec.HarnessProfileRef)
	}
	if run.Spec.HarnessProfileRef.Namespace != "" {
		t.Fatalf("chat run harness ref namespace = %q, want same-namespace default", run.Spec.HarnessProfileRef.Namespace)
	}

	harnessOnly, err := buildChatRun("agents", "chat-turn-2", "hello", chat.Thread{ID: "thread-2", Namespace: "agents", Metadata: metadata})
	if err != nil {
		t.Fatalf("harness-only buildChatRun: %v", err)
	}
	if harnessOnly.Spec.ProfileRef != nil {
		t.Fatalf("harness-only run must not gain a profile ref: %+v", harnessOnly.Spec.ProfileRef)
	}
	if harnessOnly.Spec.HarnessProfileRef == nil || harnessOnly.Spec.HarnessProfileRef.Name != "substrate-chat" {
		t.Fatalf("harness-only run harness ref = %+v, want substrate-chat", harnessOnly.Spec.HarnessProfileRef)
	}
}

// Peer child threads carry the recipient profile and defer the harness choice
// to that profile, so a peer is eligible for the actor plane exactly when its
// own configured harness selects it. The live backend then resumes the
// recipient's thread actor (see substrate.ActorNameForThread).
func TestBuildPeerChildRunDefersHarnessToRecipientProfile(t *testing.T) {
	t.Parallel()

	metadata, err := json.Marshal(map[string]string{
		"sourceTurnId":      "parent-turn",
		"sourceThreadId":    "parent-thread",
		"sourceProfileName": "desktop-manager",
	})
	if err != nil {
		t.Fatalf("metadata: %v", err)
	}
	child, err := buildChatRun("agents", "chat-turn-3", "review the proposal", chat.Thread{
		ID: "child-id", Namespace: "agents", ProfileName: "desktop-reviewer", Metadata: metadata,
	})
	if err != nil {
		t.Fatalf("peer child buildChatRun: %v", err)
	}
	if child.Spec.ProfileRef == nil || child.Spec.ProfileRef.Name != "desktop-reviewer" {
		t.Fatalf("peer child profile ref = %+v, want desktop-reviewer", child.Spec.ProfileRef)
	}
	if child.Spec.HarnessProfileRef != nil {
		t.Fatalf("peer child must not inherit the parent harness: %+v", child.Spec.HarnessProfileRef)
	}
}

// The resolved execution beside the selected harness profile is what opts a
// chat turn into the actor plane. A Job harness profile must keep resolving
// to the Job plane even after the Substrate fields exist.
func TestChatHarnessExecutionRuntimeSelection(t *testing.T) {
	t.Parallel()

	job := agentsv1alpha1.AgentRunHarnessExecutionSpec{}
	if job.UsesSubstrateActors() {
		t.Fatal("empty execution must stay on the Job plane")
	}
	substrate := agentsv1alpha1.AgentRunHarnessExecutionSpec{
		Runtime:   agentsv1alpha1.AgentRunExecutionRuntimeSubstrateActor,
		Substrate: &agentsv1alpha1.AgentRunSubstrateActorSpec{ActorClass: "standing-chat"},
	}
	if !substrate.UsesSubstrateActors() {
		t.Fatal("substrate harness profile execution must select the actor plane")
	}
	if reason, message := agentsv1alpha1.ValidateSubstrateExecution(&substrate); reason != "" {
		t.Fatalf("substrate chat execution invalid: %s %s", reason, message)
	}
}
