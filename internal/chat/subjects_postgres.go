package chat

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

var _ SubjectStore = (*PostgresStore)(nil)

func (s *PostgresStore) ListSubjects(ctx context.Context, ns, owner string, limit int) ([]Subject, error) {
	if err := validateSubjectScope(ns, owner); err != nil {
		return nil, err
	}
	limit, err := subjectLimit(limit)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT payload FROM anvil_agents_chat.subjects WHERE namespace=$1 AND owner=$2 AND status<>'merged' ORDER BY (status='done'),touched_at DESC,id LIMIT $3`, ns, owner, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Subject{}
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		var subject Subject
		if err = json.Unmarshal(raw, &subject); err != nil {
			return nil, err
		}
		out = append(out, subject)
	}
	return out, rows.Err()
}
func (s *PostgresStore) GetSubject(ctx context.Context, ns, owner, id string) (Subject, error) {
	if err := validateSubjectLookup(ns, owner, id); err != nil {
		return Subject{}, err
	}
	return scanSubject(s.pool.QueryRow(ctx, `SELECT payload FROM anvil_agents_chat.subjects WHERE namespace=$1 AND owner=$2 AND id=$3`, ns, owner, id))
}
func scanSubject(row pgx.Row) (Subject, error) {
	var raw []byte
	if err := row.Scan(&raw); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Subject{}, ErrNotFound
		}
		return Subject{}, err
	}
	var subject Subject
	err := json.Unmarshal(raw, &subject)
	return subject, err
}
func (s *PostgresStore) PutSubject(ctx context.Context, in Subject) (Subject, error) {
	subject, err := normalizeSubject(in)
	if err != nil {
		return Subject{}, err
	}
	if subject.Status == "merged" {
		return Subject{}, fmt.Errorf("%w: use MergeSubjects to merge", ErrInvalid)
	}
	// The thread is the work target. A caller cannot attach another owner's
	// conversation, even if it knows a namespace-visible thread ID.
	var owned bool
	err = s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM anvil_agents_chat.threads WHERE id=$1 AND namespace=$2 AND created_by=$3 AND COALESCE(profile_name,'')=$4)`, subject.ThreadID, subject.Namespace, subject.Owner, subject.ProfileName).Scan(&owned)
	if err != nil {
		return Subject{}, err
	}
	if !owned {
		return Subject{}, ErrNotFound
	}
	raw, err := json.Marshal(subject)
	if err != nil {
		return Subject{}, err
	}
	row := s.pool.QueryRow(ctx, `INSERT INTO anvil_agents_chat.subjects(namespace,owner,id,thread_id,status,touched_at,payload)
 VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(namespace,owner,id) DO UPDATE SET
 thread_id=EXCLUDED.thread_id,status=EXCLUDED.status,touched_at=EXCLUDED.touched_at,
 payload=jsonb_set(EXCLUDED.payload,'{openedAt}',anvil_agents_chat.subjects.payload->'openedAt')
 WHERE anvil_agents_chat.subjects.status<>'merged' RETURNING payload`, subject.Namespace, subject.Owner, subject.ID, subject.ThreadID, subject.Status, subject.TouchedAt, raw)
	return scanSubject(row)
}
func (s *PostgresStore) TouchSubject(ctx context.Context, ns, owner, id, summary string) (Subject, error) {
	if err := validateSubjectLookup(ns, owner, id); err != nil {
		return Subject{}, err
	}
	// Validate the bounded summary through the same contract as PutSubject.
	if len(summary) > 4096 || !utf8.ValidString(summary) {
		return Subject{}, fmt.Errorf("%w: invalid subject summary", ErrInvalid)
	}
	now := time.Now().UTC()
	return scanSubject(s.pool.QueryRow(ctx, `UPDATE anvil_agents_chat.subjects SET status='open',touched_at=$4,
 payload=payload || jsonb_build_object('status','open','touchedAt',$4::timestamptz) || CASE WHEN $5='' THEN '{}'::jsonb ELSE jsonb_build_object('summary',$5::text) END
 WHERE namespace=$1 AND owner=$2 AND id=$3 AND status<>'merged' RETURNING payload`, ns, owner, id, now, summary))
}
func (s *PostgresStore) MergeSubjects(ctx context.Context, ns, owner, sourceID, targetID string) (Subject, error) {
	if err := validateSubjectLookup(ns, owner, sourceID); err != nil {
		return Subject{}, err
	}
	if err := validateSubjectLookup(ns, owner, targetID); err != nil {
		return Subject{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Subject{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Always lock in ID order, including reciprocal merge requests.
	first, second := sourceID, targetID
	if first > second {
		first, second = second, first
	}
	a, err := scanSubject(tx.QueryRow(ctx, `SELECT payload FROM anvil_agents_chat.subjects WHERE namespace=$1 AND owner=$2 AND id=$3 FOR UPDATE`, ns, owner, first))
	if err != nil {
		return Subject{}, err
	}
	b, err := scanSubject(tx.QueryRow(ctx, `SELECT payload FROM anvil_agents_chat.subjects WHERE namespace=$1 AND owner=$2 AND id=$3 FOR UPDATE`, ns, owner, second))
	if err != nil {
		return Subject{}, err
	}
	source, target := a, b
	if a.ID != sourceID {
		source, target = b, a
	}
	source, target, err = mergeSubjectValues(source, target)
	if err != nil {
		return Subject{}, err
	}
	for _, subject := range []Subject{source, target} {
		raw, marshalErr := json.Marshal(subject)
		if marshalErr != nil {
			return Subject{}, marshalErr
		}
		if _, err = tx.Exec(ctx, `UPDATE anvil_agents_chat.subjects SET status=$4,touched_at=$5,payload=$6 WHERE namespace=$1 AND owner=$2 AND id=$3`, ns, owner, subject.ID, subject.Status, subject.TouchedAt, raw); err != nil {
			return Subject{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return Subject{}, err
	}
	return target, nil
}
func (s *PostgresStore) GetRoutingRequest(ctx context.Context, ns, owner, id string) (RoutingRequest, error) {
	if err := validateSubjectLookup(ns, owner, id); err != nil {
		return RoutingRequest{}, err
	}
	return scanRoutingRequest(s.pool.QueryRow(ctx, `SELECT payload,request_hash FROM anvil_agents_chat.routing_requests WHERE namespace=$1 AND owner=$2 AND id=$3`, ns, owner, id))
}
func scanRoutingRequest(row pgx.Row) (RoutingRequest, error) {
	var raw []byte
	var hash string
	if err := row.Scan(&raw, &hash); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return RoutingRequest{}, ErrNotFound
		}
		return RoutingRequest{}, err
	}
	var r RoutingRequest
	err := json.Unmarshal(raw, &r)
	r.RequestHash = hash
	return r, err
}
func (s *PostgresStore) ListRoutingRequests(ctx context.Context, ns, owner string, limit int) ([]RoutingRequest, error) {
	if err := validateSubjectScope(ns, owner); err != nil {
		return nil, err
	}
	limit, err := subjectLimit(limit)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT payload,request_hash FROM anvil_agents_chat.routing_requests WHERE namespace=$1 AND owner=$2 ORDER BY updated_at DESC,id LIMIT $3`, ns, owner, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RoutingRequest{}
	for rows.Next() {
		r, err := scanRoutingRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s *PostgresStore) SaveRoutingRequest(ctx context.Context, in RoutingRequest) (RoutingRequest, error) {
	request, err := normalizeRoutingRequest(in)
	if err != nil {
		return RoutingRequest{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return RoutingRequest{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	raw, err := json.Marshal(request)
	if err != nil {
		return RoutingRequest{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO anvil_agents_chat.routing_requests(namespace,owner,id,request_hash,status,updated_at,payload) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(namespace,owner,id) DO NOTHING`, request.Namespace, request.Owner, request.ID, request.RequestHash, request.Status, request.UpdatedAt, raw); err != nil {
		return RoutingRequest{}, err
	}
	current, err := scanRoutingRequest(tx.QueryRow(ctx, `SELECT payload,request_hash FROM anvil_agents_chat.routing_requests WHERE namespace=$1 AND owner=$2 AND id=$3 FOR UPDATE`, request.Namespace, request.Owner, request.ID))
	if err != nil {
		return RoutingRequest{}, err
	}
	request, err = updateRoutingRequest(current, request)
	if err != nil {
		return RoutingRequest{}, err
	}
	raw, err = json.Marshal(request)
	if err != nil {
		return RoutingRequest{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE anvil_agents_chat.routing_requests SET status=$4,updated_at=$5,payload=$6 WHERE namespace=$1 AND owner=$2 AND id=$3`, request.Namespace, request.Owner, request.ID, request.Status, request.UpdatedAt, raw); err != nil {
		return RoutingRequest{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return RoutingRequest{}, err
	}
	return request, nil
}
func (s *PostgresStore) AcquireRoutingRequest(ctx context.Context, ns, owner, id string) (func(), error) {
	if err := validateSubjectLookup(ns, owner, id); err != nil {
		return nil, err
	}
	// Use a dedicated connection, not a pooled slot: concurrent inference must
	// never exhaust the pool that persists its events and queues its work.
	config := s.pool.Config()
	conn, err := pgx.ConnectConfig(ctx, config.ConnConfig.Copy())
	if err != nil {
		return nil, err
	}
	var once sync.Once
	release := func() {
		once.Do(func() {
			closeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = conn.Close(closeCtx)
		})
	}
	if config.AfterConnect != nil {
		if err = config.AfterConnect(ctx, conn); err != nil {
			release()
			return nil, err
		}
	}
	hash := sha256.Sum256([]byte("subject-routing\x00" + subjectKey(ns, owner, id)))
	lockKey := int64(binary.BigEndian.Uint64(hash[:8]))
	if _, err = conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, lockKey); err != nil {
		release()
		return nil, err
	}
	return release, nil
}
