package runapi

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	agents "github.com/hazyforge/anvil-agents/api/v1alpha1"
	"github.com/hazyforge/anvil-agents/internal/chat"
)

const chatStartupRecoveryReason = "The agent did not start. No runner Job was created; retrying your saved message."

// Only the controller's fenced, authoritative no-Job deadline is an automatic
// retry receipt. Native output, pod termination text, provider errors and a
// missing reply cannot establish that an agent did not already perform work.
func chatSafeStartupFailure(run *agents.AgentRun) bool {
	if run.Status.Phase != agents.AgentRunPhaseFailed || run.Status.StartedAt != nil || run.Status.JobRef != nil || run.Status.JobUID != "" || run.Status.JobCreateAttemptedAt != nil || run.Status.RunnerPodRef != nil || run.Status.RunnerPodUID != "" {
		return false
	}
	for _, condition := range run.Status.Conditions {
		if condition.Type == "Ready" && condition.Status == "False" && condition.Reason == "ChatStartupDeadlineExceeded" && condition.ObservedGeneration == run.Generation {
			return true
		}
	}
	return false
}

func (server *Server) retryChatStartup(ctx context.Context, turn *chat.Turn, run *agents.AgentRun) (bool, error) {
	if !chatSafeStartupFailure(run) || turn.RetryCount >= chat.MaxTurnRetries || !server.config.Runs.CreateEnabled {
		return false, nil
	}
	// Reuse the frozen accepted intent, changing only its Kubernetes name. Profile,
	// harness, prompt, application scope and turn/thread labels remain unchanged.
	nextRun := &agents.AgentRun{}
	if err := json.Unmarshal(turn.RunJSON, nextRun); err != nil {
		return true, err
	}
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s/%s/%s/retry/%d", turn.Namespace, turn.ThreadID, turn.RequestID, turn.RetryCount+1)))
	nextRun.Name = fmt.Sprintf("chat-turn-%x", digest[:20])
	raw, err := json.Marshal(nextRun)
	if err != nil {
		return true, err
	}
	delay := 10 * time.Second
	if turn.RetryCount > 0 {
		delay = 30 * time.Second
	}
	next, err := server.chatStore.RetryTurn(ctx, *turn, chat.TurnRetry{RunName: nextRun.Name, RunJSON: raw, Reason: chatStartupRecoveryReason, At: time.Now().UTC().Add(delay)})
	if errors.Is(err, chat.ErrRequestConflict) {
		// Another reconciler advanced or completed this attempt. Reload the durable
		// value before returning a thread view; never regress its public receipt.
		turns, readErr := server.chatStore.ListTurns(ctx, turn.Namespace, turn.ThreadID)
		if readErr != nil {
			return true, readErr
		}
		for _, current := range turns {
			if current.ID == turn.ID {
				*turn = current
				return true, nil
			}
		}
	}
	if err != nil {
		return true, err
	}
	*turn = next
	return true, nil
}
