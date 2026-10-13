package runapi

import (
	"context"
	"encoding/json"
	"k8s.io/apimachinery/pkg/types"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	agents "github.com/hazyforge/anvil-agents/api/v1alpha1"
	"github.com/hazyforge/anvil-agents/internal/chat"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// A real Create receipt is available, but its owner disappeared before recording
// FinishDispatch. Recovery must release resources after terminal execution.
func TestTerminalReceiptResolvesAbandonedDispatch(t *testing.T) {
	backends := []string{"memory"}
	database := os.Getenv("ANVIL_AGENTS_TEST_CHAT_DATABASE_URL")
	if database != "" {
		backends = append(backends, "PostgreSQL")
	}
	for _, backend := range backends {
		for _, cancelJourney := range []bool{false, true} {
			for _, succeeded := range []bool{false, true} {
				name := "terminal-peer"
				if cancelJourney {
					name = "cancelled-journey"
				}
				if succeeded {
					name += "/succeeded"
				} else {
					name += "/failed"
				}
				t.Run(backend+"/"+name, func(t *testing.T) {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					owner := chatTestServer(t, true)
					if backend == "PostgreSQL" {
						store, err := chat.OpenPostgresStore(ctx, database)
						if err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { store.Close() })
						owner.chatStore = store
					}
					profileName := "inert-recovery-" + uuid.NewString()
					if err := owner.writes.Create(ctx, &agents.AgentRunProfile{ObjectMeta: metav1.ObjectMeta{Namespace: "agents", Name: profileName}}); err != nil {
						t.Fatal(err)
					}
					newThread := func(s *Server) chat.Thread {
						thread, err := s.chatStore.CreateThread(ctx, chat.Thread{Namespace: "agents", ProfileName: profileName, CreatedBy: "inert-user", Mode: chat.ModePersona})
						if err != nil {
							t.Fatal(err)
						}
						return thread
					}
					parentThread := newThread(owner)
					parent, _, _, err := owner.chatStore.QueueTurn(ctx, chat.Turn{Namespace: "agents", ThreadID: parentThread.ID, ProfileName: profileName, RequestID: uuid.NewString(), RunName: "inert-original"}, chat.Message{Content: "original task"})
					if err != nil {
						t.Fatal(err)
					}
					childThread := newThread(owner)
					child, err := owner.queueChatTurnWithOptions(ctx, "agents", childThread.ID, AppendChatMessageRequest{Content: "inert peer task"}, chatTurnOptions{deferred: true, noDrive: true, parent: parent.ID, parentThread: parent.ThreadID}, 0)
					if err != nil {
						t.Fatal(err)
					}
					parent.Status = "succeeded"
					parent.Coordination = &chat.Coordination{Status: "waiting"}
					parent.Delegates = []chat.Delegate{{ProfileName: profileName, ThreadID: childThread.ID, TurnID: child.Turn.ID, RunName: child.Turn.RunName}}
					if err = owner.chatStore.CompleteTurn(ctx, parent, chat.Message{Role: chat.RoleAssistant, Content: "request queued"}); err != nil {
						t.Fatal(err)
					}
					if active, err := owner.chatStore.ActivateTurn(ctx, child.Turn); err != nil || !active {
						t.Fatalf("activate peer: %v %v", active, err)
					}
					child.Turn.Status = "queued"
					if claimed, err := owner.chatStore.ClaimDispatch(ctx, child.Turn, "owner-that-crashed"); err != nil || !claimed {
						t.Fatalf("claim: %v %v", claimed, err)
					}
					var run agents.AgentRun
					if err = json.Unmarshal(child.Turn.RunJSON, &run); err != nil {
						t.Fatal(err)
					}
					run.UID = types.UID(uuid.NewString())
					if err = owner.writes.Create(ctx, &run); err != nil {
						t.Fatal(err)
					}
					// External creation committed. No FinishDispatch: simulate the crash window.
					run.Status.Phase = agents.AgentRunPhaseFailed
					run.Status.Error = "inert terminal execution failure"
					if succeeded {
						run.Status.Phase = agents.AgentRunPhaseSucceeded
						run.Status.Backend = "openCode"
						run.Status.Output = `{"type":"text","part":{"text":"verified peer output"}}`
					}
					if err = owner.writes.Status().Update(ctx, &run); err != nil {
						t.Fatal(err)
					}
					observer := chatTestServer(t, true)
					observer.chatStore = owner.chatStore
					if backend == "PostgreSQL" {
						separate, err := chat.OpenPostgresStore(ctx, database)
						if err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { separate.Close() })
						observer.chatStore = separate
					}
					observer.runs = owner.runs
					observer.writes = owner.writes
					if cancelJourney {
						if _, err = observer.chatStore.CancelCoordination(ctx, "agents", parentThread.ID, parent.ID); err != nil {
							t.Fatal(err)
						}
					}
					for i := 0; i < 3; i++ {
						turns, err := observer.chatStore.ListTurns(ctx, "agents", childThread.ID)
						if err != nil {
							t.Fatal(err)
						}
						recoveryErr := observer.reconcileChatTurn(ctx, &turns[0])
						if recoveryErr != nil {
							t.Fatalf("recovery %d: %v", i, recoveryErr)
						}
					}
					turns, err := observer.chatStore.ListTurns(ctx, "agents", childThread.ID)
					if err != nil {
						t.Fatal(err)
					}
					if chat.Active(turns[0]) {
						t.Errorf("terminal AgentRun receipt left peer active: status=%s dispatch=%+v", turns[0].Status, turns[0].Dispatch)
					}
					if turns[0].Dispatch == nil || turns[0].Dispatch.State != "terminal" || turns[0].RunUID != string(run.UID) {
						t.Fatal("verified terminal ownership/UID was not recorded atomically")
					}
					want := "failed"
					if succeeded {
						want = "succeeded"
					}
					if cancelJourney {
						want = "cancelled"
					}
					if turns[0].Status != want {
						t.Fatalf("turn status=%s want %s", turns[0].Status, want)
					}
					messages, _ := observer.chatStore.ListMessages(ctx, "agents", childThread.ID)
					if len(messages) != 2 {
						t.Fatalf("repeated recovery duplicated messages: %d", len(messages))
					}
					if cancelJourney && messages[1].Role == chat.RoleAssistant {
						t.Fatal("cancelled recovery persisted late assistant output")
					}
					// The original owner can return after recovery; it cannot downgrade the
					// terminal fence or replay a native execution.
					if err = owner.chatStore.FinishDispatch(ctx, child.Turn, "owner-that-crashed", "created"); err != nil {
						t.Fatalf("late owner acknowledgement: %v", err)
					}
					nextThread := newThread(observer)
					next, err := observer.queueChatTurnWithOptions(ctx, "agents", nextThread.ID, AppendChatMessageRequest{Content: "inert next task"}, chatTurnOptions{deferred: true, noDrive: true}, 0)
					if err != nil {
						t.Fatal(err)
					}
					if active, err := observer.chatStore.ActivateTurn(ctx, next.Turn); err != nil || !active {
						t.Errorf("actual terminal receipt did not release exclusive resources: active=%v err=%v", active, err)
					}
				})
			}
		}
	}
}
