package chat

import (
	"context"
	"errors"
)

// Dispatch fences the external Create request. An unresolved request may still
// commit later, so cancellation cannot release its resources on a missing GET.
type Dispatch struct {
	Owner string `json:"owner"`
	State string `json:"state"`
}

var ErrDispatchPending = errors.New("AgentRun creation outcome is unresolved")

func claimDispatch(old, expected Turn, parent Turn, owner string) (Turn, bool, error) {
	if !Active(old) || old.Status == "waiting" || !sameTurnAttempt(old, expected) || owner == "" {
		return old, false, ErrRequestConflict
	}
	if parent.Coordination != nil && parent.Coordination.Status == "cancelled" {
		return old, false, ErrCoordinationCancelled
	}
	if old.Dispatch != nil {
		return old, false, nil
	}
	old.Dispatch = &Dispatch{Owner: owner, State: "creating"}
	return old, true, nil
}
func (s *PostgresStore) ClaimDispatch(ctx context.Context, t Turn, owner string) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var raw []byte
	if err = tx.QueryRow(ctx, `SELECT payload FROM anvil_agents_chat.turns WHERE id=$1 AND namespace=$2 AND thread_id=$3 FOR UPDATE`, t.ID, t.Namespace, t.ThreadID).Scan(&raw); err != nil {
		return false, err
	}
	old, err := decodeTurn(raw)
	if err != nil {
		return false, err
	}
	parent, err := lockCompletionParent(ctx, tx, old)
	if err != nil {
		return false, err
	}
	next, claimed, err := claimDispatch(old, t, parent, owner)
	if err != nil || !claimed {
		return false, err
	}
	raw, err = encodeTurn(next)
	if err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `UPDATE anvil_agents_chat.turns SET payload=$1 WHERE id=$2`, raw, next.ID); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}
func (s *MemoryStore) ClaimDispatch(_ context.Context, t Turn, owner string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.turns[t.ID]
	if !ok {
		return false, ErrNotFound
	}
	parent, err := memoryCompletionParent(s, old)
	if err != nil {
		return false, err
	}
	next, claimed, err := claimDispatch(cloneTurn(old), t, parent, owner)
	if err == nil && claimed {
		s.turns[t.ID] = next
	}
	return claimed, err
}
func finishDispatch(old, expected Turn, owner, state string) (Turn, error) {
	if !Active(old) || !sameTurnAttempt(old, expected) || old.Dispatch == nil || old.Dispatch.Owner != owner || old.Dispatch.State != "creating" || (state != "created" && state != "rejected") {
		return old, ErrRequestConflict
	}
	old.Dispatch = &Dispatch{Owner: owner, State: state}
	return old, nil
}
func (s *PostgresStore) FinishDispatch(ctx context.Context, t Turn, owner, state string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var raw []byte
	if err = tx.QueryRow(ctx, `SELECT payload FROM anvil_agents_chat.turns WHERE id=$1 AND namespace=$2 AND thread_id=$3 FOR UPDATE`, t.ID, t.Namespace, t.ThreadID).Scan(&raw); err != nil {
		return err
	}
	old, err := decodeTurn(raw)
	if err != nil {
		return err
	}
	next, err := finishDispatch(old, t, owner, state)
	if err != nil {
		return err
	}
	raw, err = encodeTurn(next)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE anvil_agents_chat.turns SET payload=$1 WHERE id=$2`, raw, next.ID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *MemoryStore) FinishDispatch(_ context.Context, t Turn, owner, state string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.turns[t.ID]
	if !ok {
		return ErrNotFound
	}
	next, err := finishDispatch(cloneTurn(old), t, owner, state)
	if err == nil {
		s.turns[t.ID] = next
	}
	return err
}

func prepareSynthesisRetry(old, expected, parent Turn, retry TurnRetry) (Turn, bool, error) {
	if !old.Resumption || !expected.Resumption || old.ID != expected.ID || old.ThreadID != expected.ThreadID || old.Namespace != expected.Namespace || old.ParentTurnID != expected.ParentTurnID || old.ParentThreadID != expected.ParentThreadID {
		return old, false, ErrRequestConflict
	}
	if parent.Coordination == nil || parent.Coordination.ResumeTurnID != old.ID {
		return old, false, ErrRequestConflict
	}
	if parent.Coordination.Status == "cancelled" {
		return old, false, ErrCoordinationCancelled
	}
	// A lost or duplicate explicit retry receipt is an observation, even if
	// that newer attempt has already failed. It must never start another one.
	if old.RetryCount > expected.RetryCount {
		return old, false, nil
	}
	if old.Status != "failed" || parent.Coordination.Status != "failed" || !sameTurnAttempt(old, expected) {
		return old, false, ErrRequestConflict
	}
	original := old
	original.Status = "queued"
	next, err := prepareTurnRetry(original, expected, retry)
	if err != nil {
		return old, false, err
	}
	next.Status = "waiting"
	next.Dispatch = nil
	return next, true, nil
}
func (s *PostgresStore) RetrySynthesis(ctx context.Context, t Turn, retry TurnRetry) (Turn, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Turn{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Same source-thread lock as human appends and synthesis completion.
	var id string
	if err = tx.QueryRow(ctx, `SELECT id FROM anvil_agents_chat.threads WHERE namespace=$1 AND id=$2 FOR UPDATE`, t.Namespace, t.ThreadID).Scan(&id); err != nil {
		return Turn{}, err
	}
	var raw []byte
	if err = tx.QueryRow(ctx, `SELECT payload FROM anvil_agents_chat.turns WHERE id=$1 AND namespace=$2 AND thread_id=$3 FOR UPDATE`, t.ID, t.Namespace, t.ThreadID).Scan(&raw); err != nil {
		return Turn{}, err
	}
	old, err := decodeTurn(raw)
	if err != nil {
		return Turn{}, err
	}
	parent, err := lockCompletionParent(ctx, tx, old)
	if err != nil {
		return Turn{}, err
	}
	next, changed, err := prepareSynthesisRetry(old, t, parent, retry)
	if err != nil {
		return Turn{}, err
	}
	if !changed {
		return next, tx.Commit(ctx)
	}
	var active bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM anvil_agents_chat.turns WHERE namespace=$1 AND thread_id=$2 AND status IN ('waiting','queued','running'))`, t.Namespace, t.ThreadID).Scan(&active); err != nil {
		return Turn{}, err
	}
	if active {
		return Turn{}, ErrTurnActive
	}
	raw, err = encodeTurn(next)
	if err != nil {
		return Turn{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE anvil_agents_chat.turns SET status=$1,payload=$2 WHERE id=$3`, next.Status, raw, next.ID); err != nil {
		return Turn{}, err
	}
	parent.Coordination = &Coordination{Status: "resuming", ResumeTurnID: next.ID, RetryCount: next.RetryCount}
	if err = writeCoordination(ctx, tx, parent); err != nil {
		return Turn{}, err
	}
	return next, tx.Commit(ctx)
}
func (s *MemoryStore) RetrySynthesis(_ context.Context, t Turn, retry TurnRetry) (Turn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.turns[t.ID]
	if !ok {
		return Turn{}, ErrNotFound
	}
	parent, err := memoryCompletionParent(s, old)
	if err != nil {
		return Turn{}, err
	}
	next, changed, err := prepareSynthesisRetry(cloneTurn(old), t, parent, retry)
	if err != nil {
		return Turn{}, err
	}
	if !changed {
		return cloneTurn(next), nil
	}
	for _, other := range s.turns {
		if other.Namespace == t.Namespace && other.ThreadID == t.ThreadID && Active(other) {
			return Turn{}, ErrTurnActive
		}
	}
	parent.Coordination = &Coordination{Status: "resuming", ResumeTurnID: next.ID, RetryCount: next.RetryCount}
	s.turns[parent.ID] = parent
	s.turns[next.ID] = next
	return cloneTurn(next), nil
}
