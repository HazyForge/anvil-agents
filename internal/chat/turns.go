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
// Coordination tracks one bounded request/reply journey separately from the
// originating execution receipt. It lives in the existing durable JSON outbox.
type Coordination struct {
	RetryCount   int    `json:"retryCount,omitempty"`
	Status       string `json:"status"`
	ResumeTurnID string `json:"resumeTurnId,omitempty"`
}

type Turn struct {
	Dispatch         *Dispatch     `json:"dispatch,omitempty"`
	Coordination     *Coordination `json:"coordination,omitempty"`
	ParentTurnID     string        `json:"parentTurnId,omitempty"`
	ParentThreadID   string        `json:"parentThreadId,omitempty"`
	Resumption       bool          `json:"resumption,omitempty"`
	ExpectedSequence *int64        `json:"-"`
	LockKeys         []string      `json:"-"`
	Deferred         bool          `json:"-"`
	// QueueAfterActive is an admission-only opt-in for manager followups. Such
	// turns persist as waiting and activate in order after earlier work ends.
	// Ordinary chat keeps rejecting another unfinished turn on the same thread.
	QueueAfterActive bool            `json:"-"`
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
	RetryCount       int             `json:"retryCount,omitempty"`
	RetryAt          *time.Time      `json:"retryAt,omitempty"`
	RecoveryReason   string          `json:"recoveryReason,omitempty"`
	Attempts         []TurnAttempt   `json:"attempts,omitempty"`
}

// Attempts preserve the immutable execution receipts while one accepted user
// message recovers from an execution that provably never launched.
type TurnAttempt struct {
	RunName     string    `json:"runName"`
	RunUID      string    `json:"runUid,omitempty"`
	Error       string    `json:"error"`
	CompletedAt time.Time `json:"completedAt"`
}

type TurnRetry struct {
	RunName string
	RunJSON json.RawMessage
	Reason  string
	At      time.Time
}

const MaxTurnRetries = 2

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
	if t.Resumption && (t.ParentTurnID == "" || t.ParentThreadID == "") {
		return t, m, fmt.Errorf("%w: resumption requires a coordination parent", ErrInvalid)
	}
	if t.ID == "" {
		t.ID = uuid.NewString()
	}
	sort.Strings(t.LockKeys)
	t.Status = "queued"
	if t.Deferred || t.QueueAfterActive {
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
	parent, err := lockCoordinationParent(ctx, tx, t)
	if err != nil {
		return Turn{}, Message{}, Thread{}, err
	}
	var coordinating bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM anvil_agents_chat.turns WHERE namespace=$1 AND thread_id=$2 AND payload->'coordination'->>'status' IN ('waiting','resuming') AND id<>$3)`, t.Namespace, t.ThreadID, t.ParentTurnID).Scan(&coordinating); err != nil {
		return Turn{}, Message{}, Thread{}, err
	}
	if coordinating && !t.QueueAfterActive {
		return Turn{}, Message{}, Thread{}, ErrTurnActive
	}
	var active bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM anvil_agents_chat.turns WHERE namespace=$1 AND $6=false AND ((thread_id=$2 AND (status IN ('queued','running') OR (status='waiting' AND $7=false))) OR ($4=false AND (payload->>'profileName'=$3 OR payload->'lockKeys' ?| $5) AND status IN ('queued','running'))))`, t.Namespace, t.ThreadID, t.ProfileName, t.Deferred, t.LockKeys, t.QueueAfterActive, t.Resumption).Scan(&active); err != nil {
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
	// Timestamp accepted order using the database clock, after acquiring the
	// shared resource and thread locks. Client clocks and lock wait times cannot
	// reverse FIFO order between API replicas.
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&t.CreatedAt); err != nil {
		return Turn{}, Message{}, Thread{}, err
	}
	m.CreatedAt = t.CreatedAt
	m, err = insertTurnMessage(ctx, tx, thread, m)
	if err != nil {
		return Turn{}, Message{}, Thread{}, err
	}
	raw, err = encodeTurn(t)
	if err != nil {
		return Turn{}, Message{}, Thread{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO anvil_agents_chat.turns(id,thread_id,namespace,request_id,status,payload,created_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, t.ID, t.ThreadID, t.Namespace, t.RequestID, t.Status, raw, t.CreatedAt)
	if err != nil {
		return Turn{}, Message{}, Thread{}, err
	}
	if t.Resumption {
		parent.Coordination = &Coordination{Status: "resuming", ResumeTurnID: t.ID}
		if err = writeCoordination(ctx, tx, parent); err != nil {
			return Turn{}, Message{}, Thread{}, err
		}
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
	rows, err := s.pool.Query(ctx, `SELECT payload FROM anvil_agents_chat.turns WHERE namespace=$1 AND thread_id=$2 ORDER BY created_at,id`, namespace, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return readTurns(rows)
}
func (s *PostgresStore) PendingTurns(ctx context.Context, limit int) ([]Turn, error) {
	rows, err := s.pool.Query(ctx, `SELECT payload FROM anvil_agents_chat.turns WHERE status IN ('waiting','queued','running') OR payload->'coordination'->>'status'='waiting' ORDER BY CASE WHEN status='waiting' THEN 1 ELSE 0 END, created_at,id LIMIT $1`, limit)
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
	if err = tx.QueryRow(ctx, `SELECT payload FROM anvil_agents_chat.turns WHERE id=$1 AND thread_id=$2 FOR UPDATE`, t.ID, t.ThreadID).Scan(&raw); err != nil {
		return err
	}
	old, err := decodeTurn(raw)
	if err != nil {
		return err
	}
	if !Active(old) {
		return nil
	}
	if old.Dispatch != nil && old.Dispatch.State == "creating" {
		return ErrDispatchPending
	}
	if !sameTurnAttempt(old, t) {
		return ErrRequestConflict
	}
	parent, err := lockCompletionParent(ctx, tx, old)
	if err != nil {
		return err
	}
	if parent.Coordination != nil && parent.Coordination.Status == "cancelled" {
		t.Status = "cancelled"
		t.Error = "Coordination cancelled"
		m = Message{Role: RoleSystem, Content: "Coordination cancelled", Metadata: json.RawMessage(`{"kind":"coordination_cancelled"}`)}
	}
	if _, err = insertTurnMessage(ctx, tx, thread, m); err != nil {
		return err
	}
	old.Status = t.Status
	old.Error = t.Error
	old.Delegates = t.Delegates
	old.Coordination = t.Coordination
	if old.Resumption && parent.Coordination != nil && parent.Coordination.Status != "cancelled" {
		state := "done"
		if t.Status != "succeeded" {
			state = "failed"
		}
		parent.Coordination = &Coordination{Status: state, ResumeTurnID: old.ID, RetryCount: old.RetryCount}
		if err = writeCoordination(ctx, tx, parent); err != nil {
			return err
		}
	}
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
	parent, err := memoryCoordinationParent(s, t)
	if err != nil {
		return Turn{}, Message{}, Thread{}, err
	}
	for _, old := range s.turns {
		if !t.QueueAfterActive && old.Namespace == t.Namespace && old.ThreadID == t.ThreadID && CoordinationPending(old) && old.ID != t.ParentTurnID {
			return Turn{}, Message{}, Thread{}, ErrTurnActive
		}
	}
	for _, old := range s.turns {
		if !t.QueueAfterActive && old.Namespace == t.Namespace && ((old.ThreadID == t.ThreadID && Active(old) && (old.Status != "waiting" || !t.Resumption)) || (!t.Deferred && sharedTurnResource(old, t) && Active(old) && old.Status != "waiting")) {
			return Turn{}, Message{}, Thread{}, ErrTurnActive
		}
	}
	if t.ExpectedSequence != nil && int64(len(s.messages[key])) != *t.ExpectedSequence {
		return Turn{}, Message{}, Thread{}, ErrConversationChanged
	}
	t.CreatedAt = time.Now().UTC()
	m.CreatedAt = t.CreatedAt
	m.Sequence = int64(len(s.messages[key]) + 1)
	s.messages[key] = append(s.messages[key], m)
	s.turns[t.ID] = t
	if t.Resumption {
		parent.Coordination = &Coordination{Status: "resuming", ResumeTurnID: t.ID}
		s.turns[parent.ID] = parent
	}
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
			out = append(out, cloneTurn(t))
		}
	}
	sort.Slice(out, func(i, j int) bool { return turnBefore(out[i], out[j]) })
	return out, nil
}
func (s *MemoryStore) PendingTurns(_ context.Context, limit int) ([]Turn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Turn{}
	for _, t := range s.turns {
		if Active(t) || (t.Coordination != nil && t.Coordination.Status == "waiting") {
			out = append(out, cloneTurn(t))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if (out[i].Status == "waiting") != (out[j].Status == "waiting") {
			return out[i].Status != "waiting"
		}
		return turnBefore(out[i], out[j])
	})
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
	if old.Dispatch != nil && old.Dispatch.State == "creating" {
		return ErrDispatchPending
	}
	if !sameTurnAttempt(old, t) {
		return ErrRequestConflict
	}
	if Active(t) {
		return ErrInvalid
	}
	parent, err := memoryCompletionParent(s, old)
	if err != nil {
		return err
	}
	if parent.Coordination != nil && parent.Coordination.Status == "cancelled" {
		t.Status = "cancelled"
		t.Error = "Coordination cancelled"
		m = Message{Role: RoleSystem, Content: "Coordination cancelled", Metadata: json.RawMessage(`{"kind":"coordination_cancelled"}`)}
	}
	key := s.threadKey(t.Namespace, t.ThreadID)
	m.ThreadID = t.ThreadID
	m.Sequence = int64(len(s.messages[key]) + 1)
	m, err = NormalizeMessage(m, time.Now().UTC())
	if err != nil {
		return err
	}
	s.messages[key] = append(s.messages[key], m)
	old.Status = t.Status
	old.Error = t.Error
	old.Delegates = t.Delegates
	old.Coordination = t.Coordination
	s.turns[t.ID] = cloneTurn(old)
	if old.Resumption && parent.Coordination != nil && parent.Coordination.Status != "cancelled" {
		state := "done"
		if t.Status != "succeeded" {
			state = "failed"
		}
		parent.Coordination = &Coordination{Status: state, ResumeTurnID: old.ID, RetryCount: old.RetryCount}
		s.turns[parent.ID] = parent
	}
	return nil
}

// RecordRun binds the durable turn to the immutable Kubernetes execution UID.
func (s *PostgresStore) RecordRun(ctx context.Context, t Turn, uid string) error {
	result, err := s.pool.Exec(ctx, `UPDATE anvil_agents_chat.turns SET payload=jsonb_set(payload,'{runUid}',to_jsonb($1::text)) WHERE id=$2 AND namespace=$3 AND thread_id=$4 AND status IN ('queued','running') AND payload->>'runName'=$5 AND COALESCE((payload->>'retryCount')::int,0)=$6 AND (COALESCE(payload->>'runUid','')='' OR payload->>'runUid'=$1)`, uid, t.ID, t.Namespace, t.ThreadID, t.RunName, t.RetryCount)
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
	if !Active(old) || !sameTurnAttempt(old, t) {
		return ErrRequestConflict
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
	var raw []byte
	if err = tx.QueryRow(ctx, `SELECT payload FROM anvil_agents_chat.turns WHERE id=$1 AND namespace=$2 AND thread_id=$3 FOR UPDATE`, t.ID, t.Namespace, t.ThreadID).Scan(&raw); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			err = ErrNotFound
		}
		return false, err
	}
	old, err := decodeTurn(raw)
	if err != nil {
		return false, err
	}
	if !sameTurnAttempt(old, t) {
		return false, ErrRequestConflict
	}
	if old.Status != "waiting" {
		return old.Status == "queued" || old.Status == "running", nil
	}
	// A follow-up waits for its thread's entire coordination journey. A
	// resumption may pass its own parent and the follow-ups blocked by it.
	resumptionParent := ""
	if old.Resumption {
		resumptionParent = old.ParentTurnID
	}
	var busy bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM anvil_agents_chat.turns AS other
		WHERE other.namespace=$1 AND other.id<>$5 AND (
			(other.thread_id=$4 AND other.id<>$7 AND other.payload->'coordination'->>'status' IN ('waiting','resuming'))
			OR ((other.thread_id=$4 OR other.payload->>'profileName'=$2 OR other.payload->'lockKeys' ?| $3)
				AND (other.status IN ('queued','running') OR (other.status='waiting' AND (other.created_at,other.id)<($6,$5)
					AND NOT EXISTS (
						SELECT 1 FROM anvil_agents_chat.turns AS journey
						WHERE journey.namespace=other.namespace AND journey.thread_id=other.thread_id AND journey.id<>other.id
						AND NOT (COALESCE(other.payload->>'resumption','')='true' AND journey.id=COALESCE(other.payload->>'parentTurnId',''))
						AND journey.payload->'coordination'->>'status' IN ('waiting','resuming')
					)))
		)
	)`, old.Namespace, old.ProfileName, old.LockKeys, old.ThreadID, old.ID, old.CreatedAt, resumptionParent).Scan(&busy); err != nil {
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
	if !sameTurnAttempt(old, t) {
		return false, ErrRequestConflict
	}
	pending := make(map[string][]string)
	for _, turn := range s.turns {
		if CoordinationPending(turn) {
			key := s.threadKey(turn.Namespace, turn.ThreadID)
			pending[key] = append(pending[key], turn.ID)
		}
	}
	blockedByCoordination := func(turn Turn) bool {
		for _, id := range pending[s.threadKey(turn.Namespace, turn.ThreadID)] {
			if id != turn.ID && (!turn.Resumption || id != turn.ParentTurnID) {
				return true
			}
		}
		return false
	}
	if blockedByCoordination(old) {
		return false, nil
	}
	for _, other := range s.turns {
		if other.ID != old.ID && other.Namespace == old.Namespace && (other.ThreadID == old.ThreadID || sharedTurnResource(other, old)) && Active(other) && (other.Status != "waiting" || (turnBefore(other, old) && !blockedByCoordination(other))) {
			return false, nil
		}
	}
	old.Status = "queued"
	s.turns[t.ID] = old
	return true, nil
}

func lockTurnResources(ctx context.Context, tx pgx.Tx, t Turn) error {
	keys := append([]string{"profile:" + t.ProfileName, "thread:" + t.ThreadID}, t.LockKeys...)
	sort.Strings(keys)
	for _, key := range keys {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, t.Namespace+"/"+key); err != nil {
			return err
		}
	}
	return nil
}

func turnBefore(a, b Turn) bool {
	if a.CreatedAt.Equal(b.CreatedAt) {
		return a.ID < b.ID
	}
	return a.CreatedAt.Before(b.CreatedAt)
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
