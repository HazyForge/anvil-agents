package runapi

import (
	"context"
	"fmt"
	agents "github.com/hazyforge/anvil-agents/api/v1alpha1"
	"github.com/hazyforge/anvil-agents/internal/chat"
)

func chatRunIdentityMatches(turn chat.Turn, run *agents.AgentRun) bool {
	return run != nil && run.Namespace == turn.Namespace && run.Name == turn.RunName && (turn.RunUID == "" || turn.RunUID == string(run.UID)) && run.Labels[chatTurnLabel] == turn.ID && run.Labels[chatThreadLabel] == turn.ThreadID && run.Spec.SourceRef.Kind == "ChatThread" && run.Spec.SourceRef.Name == turn.ThreadID && run.Spec.SourceRef.Namespace == turn.Namespace
}
func (s *Server) resolveTerminalChatDispatch(ctx context.Context, turn *chat.Turn, run *agents.AgentRun) error {
	if !chatRunIdentityMatches(*turn, run) {
		return fmt.Errorf("%w: AgentRun identity does not match accepted chat turn", chat.ErrRequestConflict)
	}
	if turn.ParentTurnID == "" || (run.Status.Phase != agents.AgentRunPhaseSucceeded && run.Status.Phase != agents.AgentRunPhaseFailed) {
		return nil
	}
	next, err := s.chatStore.ResolveTerminalDispatch(ctx, *turn, chat.TerminalRunReceipt{Namespace: run.Namespace, RunName: run.Name, RunUID: string(run.UID), TurnID: run.Labels[chatTurnLabel], ThreadID: run.Labels[chatThreadLabel], SourceKind: run.Spec.SourceRef.Kind, SourceName: run.Spec.SourceRef.Name, SourceNamespace: run.Spec.SourceRef.Namespace, Phase: string(run.Status.Phase)})
	if err == nil {
		*turn = next
	}
	return err
}
