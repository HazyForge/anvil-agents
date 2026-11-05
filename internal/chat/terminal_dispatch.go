package chat

import (
	"context"
)

// TerminalRunReceipt is observed execution evidence, not a timeout or lease.
// The store rechecks it against the current accepted attempt under its lock.
type TerminalRunReceipt struct {
	Namespace, RunName, RunUID, TurnID, ThreadID   string
	SourceKind, SourceName, SourceNamespace, Phase string
}

func resolveTerminalDispatch(old, expected Turn, receipt TerminalRunReceipt) (Turn, error) {
	if !sameTurnAttempt(old, expected) {
		return old, ErrRequestConflict
	}
	if receipt.Phase != "Succeeded" && receipt.Phase != "Failed" {
		return old, ErrDispatchPending
	}
	if receipt.Namespace != old.Namespace || receipt.RunName != old.RunName || receipt.TurnID != old.ID || receipt.ThreadID != old.ThreadID || receipt.SourceKind != "ChatThread" || receipt.SourceName != old.ThreadID || receipt.SourceNamespace != old.Namespace || (old.RunUID != "" && receipt.RunUID != old.RunUID) {
		return old, ErrRequestConflict
	}
	if old.Dispatch != nil && old.Dispatch.State == "creating" {
		old.Dispatch = &Dispatch{Owner: old.Dispatch.Owner, State: "terminal"}
		// Bind the actual immutable UID in the same transition. The empty UID is
		// supported by the existing in-memory Kubernetes fixture; real API objects
		// always supply one.
		if old.RunUID == "" {
			old.RunUID = receipt.RunUID
		}
	}
	return old, nil
}

func (s *PostgresStore) ResolveTerminalDispatch(ctx context.Context, t Turn, receipt TerminalRunReceipt) (Turn, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Turn{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var raw []byte
	if err = tx.QueryRow(ctx, `SELECT payload FROM anvil_agents_chat.turns WHERE id=$1 AND namespace=$2 AND thread_id=$3 FOR UPDATE`, t.ID, t.Namespace, t.ThreadID).Scan(&raw); err != nil {
		return Turn{}, err
	}
	old, err := decodeTurn(raw)
	if err != nil {
		return Turn{}, err
	}
	next, err := resolveTerminalDispatch(old, t, receipt)
	if err != nil {
		return Turn{}, err
	}
	raw, err = encodeTurn(next)
	if err != nil {
		return Turn{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE anvil_agents_chat.turns SET payload=$1 WHERE id=$2`, raw, next.ID); err != nil {
		return Turn{}, err
	}
	return next, tx.Commit(ctx)
}
func (s *MemoryStore) ResolveTerminalDispatch(_ context.Context, t Turn, receipt TerminalRunReceipt) (Turn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.turns[t.ID]
	if !ok {
		return Turn{}, ErrNotFound
	}
	next, err := resolveTerminalDispatch(cloneTurn(old), t, receipt)
	if err != nil {
		return Turn{}, err
	}
	s.turns[t.ID] = next
	return cloneTurn(next), nil
}
