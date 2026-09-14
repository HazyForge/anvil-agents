package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrTurnActive = errors.New("a turn is already active in this thread")
var ErrConversationChanged = errors.New("conversation changed while the turn was being prepared")
var ErrRequestConflict = errors.New("request ID already used with different content")

// Turn is a durable outbox entry. RunJSON freezes the accepted execution intent;
// an API restart retries the same Kubernetes name rather than creating new work.
type Turn struct {
	ExpectedSequence *int64          `json:"-"`
	LockKeys         []string        `json:"-"`
	Deferred         bool            `json:"-"`
	Delegates        []Delegate      `json:"delegates,omitempty"`
	ProfileName      string          `json:"profileName"`
	RunUID           string          `json:"runUid,omitempty"`
	ID               string          `json:"id"`
	ThreadID         string          `json:"threadId"`
	Namespace        string          `json:"namespace"`
	RequestID        string          `json:"requestId"`
	RunName          string          `json:"runName"`
	Status           string          `json:"status"`
	Error            string          `json:"error,omitempty"`
	UserMessageID    string          `json:"userMessageId"`
	CreatedAt        time.Time       `json:"createdAt"`
	RunJSON          json.RawMessage `json:"-"`
}

type Delegate struct {
	ProfileName string `json:"profileName"`
	ThreadID    string `json:"threadId"`
	TurnID      string `json:"turnId"`
	RunName     string `json:"runName"`
	Status      string `json:"status"`
}

// storedTurn keeps the execution intent private from HTTP responses.
type storedTurn struct {
	Turn
	Run  json.RawMessage `json:"run"`
	Keys []string        `json:"lockKeys"`
}

func encodeTurn(t Turn) ([]byte, error) { return json.Marshal(storedTurn{t, t.RunJSON, t.LockKeys}) }
func decodeTurn(raw []byte) (Turn, error) {
	var v storedTurn
	err := json.Unmarshal(raw, &v)
	v.Turn.RunJSON = v.Run
	v.Turn.LockKeys = v.Keys
	return v.Turn, err
}
func Active(t Turn) bool {
	return t.Status == "waiting" || t.Status == "queued" || t.Status == "running"
}

func prepareTurn(t Turn, m Message) (Turn, Message, error) {
	if _, err := uuid.Parse(t.RequestID); err != nil {
		return t, m, fmt.Errorf("%w: requestId must be a UUID", ErrInvalid)
	}
	if _, _, err := normalizeThreadLookup(t.Namespace, t.ThreadID); err != nil {
		return t, m, err
	}
	if t.ID == "" {
		t.ID = uuid.NewString()
	}
	sort.Strings(t.LockKeys)
	t.Status = "queued"
	if t.Deferred {
		t.Status = "waiting"
	}
	t.CreatedAt = time.Now().UTC()
	m.ThreadID = t.ThreadID
	m.Sequence = 1
	m.Role = RoleUser
	var err error
	m, err = NormalizeMessage(m, t.CreatedAt)
	t.UserMessageID = m.ID
	return t, m, err
}

func (s *PostgresStore) QueueTurn(ctx context.Context, t Turn, m Message) (Turn, Message, Thread, error) {
	t, m, err := prepareTurn(t, m)
	if err != nil {
		return Turn{}, Message{}, Thread{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Turn{}, Message{}, Thread{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = lockTurnResources(ctx, tx, t); err != nil {
		return Turn{}, Message{}, Thread{}, err
	}
	thread, err := scanThread(tx.QueryRow(ctx, `SELECT id,namespace,profile_name,mode,title,created_at,updated_at,created_by,metadata FROM anvil_agents_chat.threads WHERE namespace=$1 AND id=$2 FOR UPDATE`, t.Namespace, t.ThreadID))
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	if err != nil {
		return Turn{}, Message{}, Thread{}, err
	}
	var raw []byte
	err = tx.QueryRow(ctx, `SELECT payload FROM anvil_agents_chat.turns WHERE thread_id=$1 AND request_id=$2`, t.ThreadID, t.RequestID).Scan(&raw)
	if err == nil {
		existing, e := decodeTurn(raw)
		if e != nil {
			return Turn{}, Message{}, Thread{}, e
		}
		user, e := scanMessage(tx.QueryRow(ctx, `SELECT id,thread_id,role,content,created_at,sequence,metadata FROM anvil_agents_chat.messages WHERE id=$1`, existing.UserMessageID))
		if e == nil && user.Content != m.Content {
			e = ErrRequestConflict
		}
		return existing, user, thread, e
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Turn{}, Message{}, Thread{}, err
	}
	var active bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM anvil_agents_chat.turns WHERE namespace=$1 AND ((thread_id=$2 AND status IN ('waiting','queued','running')) OR ($4=false AND (payload->>'profileName'=$3 OR payload->'lockKeys' ?| $5) AND status IN ('queued','running'))))`, t.Namespace, t.ThreadID, t.ProfileName, t.Deferred, t.LockKeys).Scan(&active); err != nil {
		return Turn{}, Message{}, Thread{}, err
	}
	if active {
		return Turn{}, Message{}, Thread{}, ErrTurnActive
	}
	if t.ExpectedSequence != nil {
		var current int64
		if err = tx.QueryRow(ctx, `SELECT COALESCE(MAX(sequence),0) FROM anvil_agents_chat.messages WHERE thread_id=$1`, t.ThreadID).Scan(&current); err != nil {
			return Turn{}, Message{}, Thread{}, err
		}
		if current != *t.ExpectedSequence {
			return Turn{}, Message{}, Thread{}, ErrConversationChanged
		}
	}
	m, err = insertTurnMessage(ctx, tx, thread, m)
	if err != nil {
		return Turn{}, Message{}, Thread{}, err
	}
	raw, err = encodeTurn(t)
	if err != nil {
		return Turn{}, Message{}, Thread{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO anvil_agents_chat.turns(id,thread_id,namespace,request_id,status,payload) VALUES($1,$2,$3,$4,$5,$6)`, t.ID, t.ThreadID, t.Namespace, t.RequestID, t.Status, raw)
	if err != nil {
		return Turn{}, Message{}, Thread{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Turn{}, Message{}, Thread{}, err
	}
	return t, m, thread, nil
}

func insertTurnMessage(ctx context.Context, tx pgx.Tx, thread Thread, m Message) (Message, error) {
	if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(sequence),0)+1 FROM anvil_agents_chat.messages WHERE thread_id=$1`, thread.ID).Scan(&m.Sequence); err != nil {
		return Message{}, err
	}
	m.ThreadID = thread.ID
	var err error
	m, err = NormalizeMessage(m, time.Now().UTC())
	if err != nil {
		return Message{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO anvil_agents_chat.messages(id,thread_id,role,content,created_at,sequence,metadata) VALUES($1,$2,$3,$4,$5,$6,$7)`, m.ID, m.ThreadID, m.Role, m.Content, m.CreatedAt, m.Sequence, []byte(m.Metadata))
	if err != nil {
		return Message{}, err
	}
	title := thread.Title
	if (title == "" || title == DefaultThreadTitle) && m.Role == RoleUser {
		title = titleFromContent(m.Content)
	}
	_, err = tx.Exec(ctx, `UPDATE anvil_agents_chat.threads SET updated_at=$1,title=$2 WHERE id=$3`, time.Now().UTC(), title, thread.ID)
	return m, err
}
func (s *PostgresStore) ListTurns(ctx context.Context, namespace, id string) ([]Turn, error) {
	if _, err := s.GetThread(ctx, namespace, id); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT payload FROM anvil_agents_chat.turns WHERE namespace=$1 AND thread_id=$2 ORDER BY created_at`, namespace, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return readTurns(rows)
}
func (s *PostgresStore) PendingTurns(ctx context.Context, limit int) ([]Turn, error) {
	rows, err := s.pool.Query(ctx, `SELECT payload FROM anvil_agents_chat.turns WHERE status IN ('waiting','queued','running') ORDER BY CASE WHEN status='waiting' THEN 1 ELSE 0 END, created_at LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return readTurns(rows)
}
func readTurns(rows pgx.Rows) ([]Turn, error) {
	out := []Turn{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		t, err := decodeTurn(raw)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
func (s *PostgresStore) CompleteTurn(ctx context.Context, t Turn, m Message) error {
	if Active(t) {
		return fmt.Errorf("%w: completion must be terminal", ErrInvalid)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	thread, err := scanThread(tx.QueryRow(ctx, `SELECT id,namespace,profile_name,mode,title,created_at,updated_at,created_by,metadata FROM anvil_agents_chat.threads WHERE namespace=$1 AND id=$2 FOR UPDATE`, t.Namespace, t.ThreadID))
	if err != nil {
		return err
	}
	var raw []byte
	if err = tx.QueryRow(ctx, `SELECT payload FROM anvil_agents_chat.turns WHERE id=$1 AND thread_id=$2`, t.ID, t.ThreadID).Scan(&raw); err != nil {
		return err
	}
	old, err := decodeTurn(raw)
	if err != nil {
		return err
	}
	if !Active(old) {
		return nil
	}
	if _, err = insertTurnMessage(ctx, tx, thread, m); err != nil {
		return err
	}
	old.Status = t.Status
	old.Error = t.Error
	old.Delegates = t.Delegates
	raw, err = encodeTurn(old)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE anvil_agents_chat.turns SET status=$1,payload=$2 WHERE id=$3`, old.Status, raw, old.ID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *MemoryStore) QueueTurn(_ context.Context, t Turn, m Message) (Turn, Message, Thread, error) {
	t, m, err := prepareTurn(t, m)
	if err != nil {
		return Turn{}, Message{}, Thread{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := s.threadKey(t.Namespace, t.ThreadID)
	thread, ok := s.threads[key]
	if !ok {
		return Turn{}, Message{}, Thread{}, ErrNotFound
	}
	for _, old := range s.turns {
		if old.ThreadID == t.ThreadID && old.RequestID == t.RequestID {
			for _, user := range s.messages[key] {
				if user.ID == old.UserMessageID {
					if user.Content != m.Content {
						return Turn{}, Message{}, Thread{}, ErrRequestConflict
					}
					return old, user, cloneThread(thread), nil
				}
			}
		}
	}
	for _, old := range s.turns {
		if old.Namespace == t.Namespace && ((old.ThreadID == t.ThreadID && Active(old)) || (!t.Deferred && sharedTurnResource(old, t) && Active(old) && old.Status != "waiting")) {
			return Turn{}, Message{}, Thread{}, ErrTurnActive
		}
	}
	if t.ExpectedSequence != nil && int64(len(s.messages[key])) != *t.ExpectedSequence {
		return Turn{}, Message{}, Thread{}, ErrConversationChanged
	}
	m.Sequence = int64(len(s.messages[key]) + 1)
	s.messages[key] = append(s.messages[key], m)
	s.turns[t.ID] = t
	thread.UpdatedAt = t.CreatedAt
	if thread.Title == DefaultThreadTitle {
		thread.Title = titleFromContent(m.Content)
	}
	s.threads[key] = thread
	return t, m, cloneThread(thread), nil
}
func (s *MemoryStore) ListTurns(_ context.Context, namespace, id string) ([]Turn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.threads[s.threadKey(namespace, id)]; !ok {
		return nil, ErrNotFound
	}
	out := []Turn{}
	for _, t := range s.turns {
		if t.Namespace == namespace && t.ThreadID == id {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}
func (s *MemoryStore) PendingTurns(_ context.Context, limit int) ([]Turn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Turn{}
	for _, t := range s.turns {
		if Active(t) {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
func (s *MemoryStore) CompleteTurn(_ context.Context, t Turn, m Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.turns[t.ID]
	if !ok {
		return ErrNotFound
	}
	if !Active(old) {
		return nil
	}
	if Active(t) {
		return ErrInvalid
	}
	key := s.threadKey(t.Namespace, t.ThreadID)
	m.ThreadID = t.ThreadID
	m.Sequence = int64(len(s.messages[key]) + 1)
	var err error
	m, err = NormalizeMessage(m, time.Now().UTC())
	if err != nil {
		return err
	}
	s.messages[key] = append(s.messages[key], m)
	old.Status = t.Status
	old.Error = t.Error
	old.Delegates = t.Delegates
	s.turns[t.ID] = old
	return nil
}

// RecordRun binds the durable turn to the immutable Kubernetes execution UID.
func (s *PostgresStore) RecordRun(ctx context.Context, t Turn, uid string) error {
	result, err := s.pool.Exec(ctx, `UPDATE anvil_agents_chat.turns SET payload=jsonb_set(payload,'{runUid}',to_jsonb($1::text)) WHERE id=$2 AND status IN ('queued','running') AND (COALESCE(payload->>'runUid','')='' OR payload->>'runUid'=$1)`, uid, t.ID)
	if err == nil && result.RowsAffected() != 1 {
		return ErrRequestConflict
	}
	return err
}
func (s *MemoryStore) RecordRun(_ context.Context, t Turn, uid string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.turns[t.ID]
	if !ok {
		return ErrNotFound
	}
	if old.RunUID != "" && old.RunUID != uid {
		return ErrInvalid
	}
	old.RunUID = uid
	s.turns[t.ID] = old
	return nil
}

func (s *PostgresStore) ActivateTurn(ctx context.Context, t Turn) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = lockTurnResources(ctx, tx, t); err != nil {
		return false, err
	}
	var status string
	if err = tx.QueryRow(ctx, `SELECT status FROM anvil_agents_chat.turns WHERE id=$1`, t.ID).Scan(&status); err != nil {
		return false, err
	}
	if status != "waiting" {
		return status == "queued" || status == "running", nil
	}
	var busy bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM anvil_agents_chat.turns WHERE namespace=$1 AND (payload->>'profileName'=$2 OR payload->'lockKeys' ?| $3) AND status IN ('queued','running'))`, t.Namespace, t.ProfileName, t.LockKeys).Scan(&busy); err != nil {
		return false, err
	}
	if busy {
		return false, nil
	}
	if _, err = tx.Exec(ctx, `UPDATE anvil_agents_chat.turns SET status='queued',payload=jsonb_set(payload,'{status}','"queued"') WHERE id=$1`, t.ID); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}
func (s *MemoryStore) ActivateTurn(_ context.Context, t Turn) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.turns[t.ID]
	if !ok {
		return false, ErrNotFound
	}
	if old.Status != "waiting" {
		return Active(old), nil
	}
	for _, other := range s.turns {
		if other.Namespace == old.Namespace && sharedTurnResource(other, old) && Active(other) && other.Status != "waiting" {
			return false, nil
		}
	}
	old.Status = "queued"
	s.turns[t.ID] = old
	return true, nil
}

func lockTurnResources(ctx context.Context, tx pgx.Tx, t Turn) error {
	keys := append([]string{"profile:" + t.ProfileName}, t.LockKeys...)
	sort.Strings(keys)
	for _, key := range keys {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, t.Namespace+"/"+key); err != nil {
			return err
		}
	}
	return nil
}
func sharedTurnResource(a, b Turn) bool {
	if a.ProfileName == b.ProfileName {
		return true
	}
	for _, ak := range a.LockKeys {
		for _, bk := range b.LockKeys {
			if ak == bk {
				return true
			}
		}
	}
	return false
}
