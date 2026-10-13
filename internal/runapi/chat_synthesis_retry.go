package runapi

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	agents "github.com/hazyforge/anvil-agents/api/v1alpha1"
	"github.com/hazyforge/anvil-agents/internal/chat"
	"strings"
	"time"
)

func isServiceSynthesisMessage(m chat.Message) bool {
	var metadata struct {
		AuthorKind string `json:"authorKind"`
	}
	return json.Unmarshal(m.Metadata, &metadata) == nil && metadata.AuthorKind == "agent_results"
}
func (s *Server) queueChatSynthesisRetry(ctx context.Context, ns, id string, body AppendChatMessageRequest) (ChatAppendResponse, error) {
	if body.Content != "" || body.RetryCount == nil || *body.RetryCount < 0 || *body.RetryCount >= chat.MaxTurnRetries {
		return ChatAppendResponse{}, fmt.Errorf("%w: final-answer retry requires saved turn and attempt only", chat.ErrInvalid)
	}
	thread, err := s.chatStore.GetThread(ctx, ns, id)
	if err != nil {
		return ChatAppendResponse{}, err
	}
	if err = s.validateChatTarget(ctx, ns, thread.ProfileName, chatHarness(thread)); err != nil {
		return ChatAppendResponse{}, err
	}
	turns, err := s.chatStore.ListTurns(ctx, ns, id)
	if err != nil {
		return ChatAppendResponse{}, err
	}
	var saved *chat.Turn
	for i := range turns {
		if turns[i].ID == body.RetryTurnID {
			saved = &turns[i]
			break
		}
	}
	if saved == nil || !saved.Resumption || saved.ParentTurnID == "" || saved.ParentThreadID != id {
		return ChatAppendResponse{}, fmt.Errorf("%w: not a saved synthesis turn", chat.ErrInvalid)
	}
	messages, err := s.chatStore.ListMessages(ctx, ns, id)
	if err != nil {
		return ChatAppendResponse{}, err
	}
	var user chat.Message
	for _, m := range messages {
		if m.ID == saved.UserMessageID && isServiceSynthesisMessage(m) {
			user = m
			break
		}
	}
	if user.ID == "" {
		return ChatAppendResponse{}, chat.ErrRequestConflict
	}
	run := &agents.AgentRun{}
	if err = json.Unmarshal(saved.RunJSON, run); err != nil {
		return ChatAppendResponse{}, err
	}
	// Retry only the original frozen synthesis intent. No user/peer content is
	// reclassified, reauthored, rebuilt, or granted another fanout.
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s/%s/%s/retry/%d", ns, id, saved.RequestID, *body.RetryCount+1)))
	run.Name = fmt.Sprintf("chat-turn-%x", digest[:20])
	raw, err := json.Marshal(run)
	if err != nil {
		return ChatAppendResponse{}, err
	}
	expected := *saved
	expected.RetryCount = *body.RetryCount
	next, err := s.chatStore.RetrySynthesis(ctx, expected, chat.TurnRetry{RunName: run.Name, RunJSON: raw, Reason: "Retrying the final answer with the same saved peer results.", At: time.Now().UTC()})
	if err != nil {
		return ChatAppendResponse{}, err
	}
	return ChatAppendResponse{Thread: thread, User: user, Turn: next}, nil
}

func rejectSynthesisPromotion(messages []chat.Message, content string) error {
	for _, m := range messages {
		if isServiceSynthesisMessage(m) && strings.TrimSpace(m.Content) == strings.TrimSpace(content) {
			return fmt.Errorf("%w: use the saved final-answer retry; service results cannot be resent as human input", chat.ErrInvalid)
		}
	}
	return nil
}
