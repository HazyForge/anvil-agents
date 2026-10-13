package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrCoordinationCancelled = errors.New("coordination cancelled")

func CoordinationPending(t Turn) bool {
	return t.Coordination != nil && (t.Coordination.Status == "waiting" || t.Coordination.Status == "resuming")
}

func SynthesisRequestID(parentID string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("anvil-chat-synthesis/"+parentID)).String()
}

func cloneTurn(t Turn) Turn {
	if t.Dispatch != nil {
		d := *t.Dispatch
		t.Dispatch = &d
	}
	if t.Coordination != nil {
		c := *t.Coordination
		t.Coordination = &c
	}
	t.Delegates = append([]Delegate(nil), t.Delegates...)
	t.RunJSON = append(json.RawMessage(nil), t.RunJSON...)
	t.LockKeys = append([]string(nil), t.LockKeys...)
	return t
}

func validateParent(t, parent Turn, completion bool) error {
	if parent.ID != t.ParentTurnID || parent.Namespace != t.Namespace || parent.ThreadID != t.ParentThreadID {
		return ErrRequestConflict
	}
	if parent.Coordination != nil && parent.Coordination.Status == "cancelled" && !completion {
		return ErrCoordinationCancelled
	}
	if t.Resumption && !completion {
		if t.ThreadID != parent.ThreadID || t.RequestID != SynthesisRequestID(parent.ID) || parent.Status != "succeeded" || parent.Coordination == nil || parent.Coordination.Status != "waiting" {
			return ErrRequestConflict
		}
	}
	if t.Resumption && completion && (parent.Coordination == nil || parent.Coordination.ResumeTurnID != t.ID) {
		return ErrRequestConflict
	}
	return nil
}

func lockCompletionParent(ctx context.Context, tx pgx.Tx, t Turn) (Turn, error) {
	if t.ParentTurnID == "" {
		return Turn{}, nil
	}
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT payload FROM anvil_agents_chat.turns WHERE id=$1 AND namespace=$2 AND thread_id=$3 FOR UPDATE`, t.ParentTurnID, t.Namespace, t.ParentThreadID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return Turn{}, ErrNotFound
	}
	if err != nil {
		return Turn{}, err
	}
	parent, err := decodeTurn(raw)
	if err != nil {
		return Turn{}, err
	}
	return parent, validateParent(t, parent, true)
}

func lockCoordinationParent(ctx context.Context, tx pgx.Tx, t Turn) (Turn, error) {
	if t.ParentTurnID == "" {
		return Turn{}, nil
	}
	// Completion accepts a cancelled parent to suppress an already-running
	// result. Admission rejects it before any new outbox entry is committed.
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT payload FROM anvil_agents_chat.turns WHERE id=$1 AND namespace=$2 AND thread_id=$3 FOR UPDATE`, t.ParentTurnID, t.Namespace, t.ParentThreadID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return Turn{}, ErrNotFound
	}
	if err != nil {
		return Turn{}, err
	}
	parent, err := decodeTurn(raw)
	if err != nil {
		return Turn{}, err
	}
	return parent, validateParent(t, parent, false)
}

func memoryCompletionParent(s *MemoryStore, t Turn) (Turn, error) {
	if t.ParentTurnID == "" {
		return Turn{}, nil
	}
	parent, ok := s.turns[t.ParentTurnID]
	if !ok {
		return Turn{}, ErrNotFound
	}
	return cloneTurn(parent), validateParent(t, parent, true)
}

func memoryCoordinationParent(s *MemoryStore, t Turn) (Turn, error) {
	if t.ParentTurnID == "" {
		return Turn{}, nil
	}
	parent, ok := s.turns[t.ParentTurnID]
	if !ok {
		return Turn{}, ErrNotFound
	}
	return cloneTurn(parent), validateParent(t, parent, false)
}

func writeCoordination(ctx context.Context, tx pgx.Tx, parent Turn) error {
	raw, err := json.Marshal(parent.Coordination)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE anvil_agents_chat.turns SET payload=jsonb_set(payload,'{coordination}',$1::jsonb) WHERE id=$2 AND namespace=$3 AND thread_id=$4`, raw, parent.ID, parent.Namespace, parent.ThreadID)
	return err
}

func cancellation(parent Turn) (Turn, Message, error) {
	if parent.Coordination == nil {
		return Turn{}, Message{}, fmt.Errorf("%w: turn has no reply-back journey", ErrInvalid)
	}
	if !CoordinationPending(parent) && parent.Coordination.Status != "cancelled" {
		return Turn{}, Message{}, ErrRequestConflict
	}
	c := *parent.Coordination
	c.Status = "cancelled"
	parent.Coordination = &c
	return parent, Message{ID: uuid.NewSHA1(uuid.NameSpaceURL, []byte("anvil-chat-cancel/"+parent.ID)).String(), Role: RoleSystem, Content: "Coordination cancelled. Late peer results will not resume this conversation.", Metadata: json.RawMessage(`{"kind":"coordination_cancelled"}`)}, nil
}

func (s *PostgresStore) CancelCoordination(ctx context.Context, ns, threadID, turnID string) (Turn, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Turn{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	thread, err := scanThread(tx.QueryRow(ctx, `SELECT id,namespace,profile_name,mode,title,created_at,updated_at,created_by,metadata FROM anvil_agents_chat.threads WHERE namespace=$1 AND id=$2 FOR UPDATE`, ns, threadID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Turn{}, ErrNotFound
	}
	if err != nil {
		return Turn{}, err
	}
	var raw []byte
	err = tx.QueryRow(ctx, `SELECT payload FROM anvil_agents_chat.turns WHERE id=$1 AND namespace=$2 AND thread_id=$3 FOR UPDATE`, turnID, ns, threadID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return Turn{}, ErrNotFound
	}
	if err != nil {
		return Turn{}, err
	}
	parent, err := decodeTurn(raw)
	if err != nil {
		return Turn{}, err
	}
	if parent.Coordination != nil && parent.Coordination.Status == "cancelled" {
		return parent, tx.Commit(ctx)
	}
	parent, message, err := cancellation(parent)
	if err != nil {
		return Turn{}, err
	}
	if err = writeCoordination(ctx, tx, parent); err != nil {
		return Turn{}, err
	}
	if _, err = insertTurnMessage(ctx, tx, thread, message); err != nil {
		return Turn{}, err
	}
	return parent, tx.Commit(ctx)
}

func (s *MemoryStore) CancelCoordination(_ context.Context, ns, threadID, turnID string) (Turn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	parent, ok := s.turns[turnID]
	if !ok || parent.Namespace != ns || parent.ThreadID != threadID {
		return Turn{}, ErrNotFound
	}
	if parent.Coordination != nil && parent.Coordination.Status == "cancelled" {
		return cloneTurn(parent), nil
	}
	parent, message, err := cancellation(cloneTurn(parent))
	if err != nil {
		return Turn{}, err
	}
	key := s.threadKey(ns, threadID)
	message.ThreadID = threadID
	message.Sequence = int64(len(s.messages[key]) + 1)
	message, err = NormalizeMessage(message, time.Now().UTC())
	if err != nil {
		return Turn{}, err
	}
	s.turns[turnID] = parent
	s.messages[key] = append(s.messages[key], message)
	return cloneTurn(parent), nil
}
