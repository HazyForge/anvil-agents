package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Standing identity follows the existing namespace-shared visibility of chat.
// CreatedBy remains provenance, not a new owner or authorization dimension.
func standingEligible(thread Thread) bool {
	if thread.ProfileName == "" {
		return false
	}
	var metadata map[string]json.RawMessage
	if json.Unmarshal(thread.Metadata, &metadata) != nil {
		return false
	}
	for _, field := range []string{"harnessProfileName", "sourceThreadId", "sourceTurnId", "sourceProfileName"} {
		if _, exists := metadata[field]; exists {
			return false
		}
	}
	return true
}

func (s *PostgresStore) EnsureStandingThread(ctx context.Context, seed Thread) (Thread, bool, error) {
	seed, err := NormalizeThread(seed, time.Now().UTC())
	if err != nil {
		return Thread{}, false, err
	}
	if !standingEligible(seed) {
		return Thread{}, false, fmt.Errorf("%w: standing conversations require a direct agent profile", ErrInvalid)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Thread{}, false, err
	}
	defer tx.Rollback(ctx)
	// Serialize first-use adoption/creation across API replicas. Ordinary history
	// creation remains independent, and can never replace an established pointer.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "chat-standing/"+seed.Namespace+"/"+seed.ProfileName); err != nil {
		return Thread{}, false, err
	}
	thread, err := scanThread(tx.QueryRow(ctx, `SELECT t.id,t.namespace,t.profile_name,t.mode,t.title,t.created_at,t.updated_at,t.created_by,t.metadata
FROM anvil_agents_chat.standing_threads s JOIN anvil_agents_chat.threads t ON t.id=s.thread_id
WHERE s.namespace=$1 AND s.profile_name=$2`, seed.Namespace, seed.ProfileName))
	if err == nil {
		return thread, false, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Thread{}, false, err
	}
	thread, err = scanThread(tx.QueryRow(ctx, `SELECT id,namespace,profile_name,mode,title,created_at,updated_at,created_by,metadata
FROM anvil_agents_chat.threads WHERE namespace=$1 AND profile_name=$2
AND NOT (metadata ?| ARRAY['harnessProfileName','sourceThreadId','sourceTurnId','sourceProfileName'])
ORDER BY updated_at DESC, id ASC LIMIT 1`, seed.Namespace, seed.ProfileName))
	created := errors.Is(err, pgx.ErrNoRows)
	if err != nil && !created {
		return Thread{}, false, err
	}
	if created {
		thread = seed
		_, err = tx.Exec(ctx, `INSERT INTO anvil_agents_chat.threads(id,namespace,profile_name,mode,title,created_at,updated_at,created_by,metadata)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, thread.ID, thread.Namespace, thread.ProfileName, thread.Mode, thread.Title, thread.CreatedAt, thread.UpdatedAt, thread.CreatedBy, []byte(thread.Metadata))
		if err != nil {
			return Thread{}, false, err
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO anvil_agents_chat.standing_threads(namespace,profile_name,thread_id) VALUES($1,$2,$3)`, thread.Namespace, thread.ProfileName, thread.ID)
	if err != nil {
		return Thread{}, false, err
	}
	return thread, created, tx.Commit(ctx)
}

func (s *MemoryStore) EnsureStandingThread(_ context.Context, seed Thread) (Thread, bool, error) {
	seed, err := NormalizeThread(seed, time.Now().UTC())
	if err != nil {
		return Thread{}, false, err
	}
	if !standingEligible(seed) {
		return Thread{}, false, fmt.Errorf("%w: standing conversations require a direct agent profile", ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.standing == nil {
		s.standing = map[string]string{}
	}
	key := s.threadKey(seed.Namespace, seed.ProfileName)
	if id := s.standing[key]; id != "" {
		return cloneThread(s.threads[s.threadKey(seed.Namespace, id)]), false, nil
	}
	var chosen Thread
	for _, candidate := range s.threads {
		if candidate.Namespace != seed.Namespace || candidate.ProfileName != seed.ProfileName || !standingEligible(candidate) {
			continue
		}
		if chosen.ID == "" || candidate.UpdatedAt.After(chosen.UpdatedAt) || (candidate.UpdatedAt.Equal(chosen.UpdatedAt) && candidate.ID < chosen.ID) {
			chosen = candidate
		}
	}
	created := chosen.ID == ""
	if created {
		chosen = seed
		threadKey := s.threadKey(seed.Namespace, seed.ID)
		if _, exists := s.threads[threadKey]; exists {
			return Thread{}, false, fmt.Errorf("%w: thread already exists", ErrInvalid)
		}
		s.threads[threadKey] = cloneThread(seed)
		s.messages[threadKey] = []Message{}
	}
	s.standing[key] = chosen.ID
	return cloneThread(chosen), created, nil
}
