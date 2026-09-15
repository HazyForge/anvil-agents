package chat

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

func sameTurnAttempt(a, b Turn) bool {
	return a.ID == b.ID && a.Namespace == b.Namespace && a.ThreadID == b.ThreadID && a.RunName == b.RunName && a.RetryCount == b.RetryCount && (b.RunUID == "" || a.RunUID == b.RunUID)
}

func prepareTurnRetry(old, expected Turn, retry TurnRetry) (Turn, error) {
	if !Active(old) || !sameTurnAttempt(old, expected) {
		return Turn{}, ErrRequestConflict
	}
	if old.RetryCount >= MaxTurnRetries || retry.RunName == "" || retry.RunName == old.RunName || len(retry.RunJSON) == 0 || retry.Reason == "" || retry.At.IsZero() {
		return Turn{}, ErrInvalid
	}
	old.Attempts = append(append([]TurnAttempt(nil), old.Attempts...), TurnAttempt{RunName: old.RunName, RunUID: old.RunUID, Error: retry.Reason, CompletedAt: time.Now().UTC()})
	old.RetryCount++
	old.RetryAt = &retry.At
	old.RecoveryReason = retry.Reason
	old.RunName, old.RunUID, old.RunJSON = retry.RunName, "", append([]byte(nil), retry.RunJSON...)
	old.Status, old.Error = "queued", ""
	return old, nil
}

// RetryTurn changes only the execution attempt, atomically retaining the same
// accepted message, request ID and exclusive resource locks. Old reconcilers
// cannot overwrite a newer attempt or add duplicate transcript entries.
func (s *PostgresStore) RetryTurn(ctx context.Context, expected Turn, retry TurnRetry) (Turn, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Turn{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var raw []byte
	if err = tx.QueryRow(ctx, `SELECT payload FROM anvil_agents_chat.turns WHERE id=$1 AND namespace=$2 AND thread_id=$3 FOR UPDATE`, expected.ID, expected.Namespace, expected.ThreadID).Scan(&raw); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			err = ErrNotFound
		}
		return Turn{}, err
	}
	old, err := decodeTurn(raw)
	if err != nil {
		return Turn{}, err
	}
	next, err := prepareTurnRetry(old, expected, retry)
	if err != nil {
		return Turn{}, err
	}
	raw, err = encodeTurn(next)
	if err != nil {
		return Turn{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE anvil_agents_chat.turns SET status=$1,payload=$2 WHERE id=$3`, next.Status, raw, next.ID); err != nil {
		return Turn{}, err
	}
	return next, tx.Commit(ctx)
}

func (s *MemoryStore) RetryTurn(_ context.Context, expected Turn, retry TurnRetry) (Turn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.turns[expected.ID]
	if !ok {
		return Turn{}, ErrNotFound
	}
	next, err := prepareTurnRetry(old, expected, retry)
	if err != nil {
		return Turn{}, err
	}
	s.turns[next.ID] = next
	return next, nil
}
