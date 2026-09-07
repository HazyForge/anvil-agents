package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	SchemaName = "anvil_agents_chat"

	ModePersona = "persona"
	ModeFleet   = "fleet"

	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"

	DefaultThreadTitle = "New chat"

	maxTitleRunes    = 200
	maxMessageBytes  = 64 * 1024
	maxMetadataBytes = 8 * 1024
	defaultListLimit = 50
	maxListLimit     = 200
	maxAppendBatch   = 16
)

var (
	ErrNotFound = errors.New("chat resource not found")
	ErrInvalid  = errors.New("invalid chat request")
)

const chatSchema = `
CREATE SCHEMA IF NOT EXISTS anvil_agents_chat;

CREATE TABLE IF NOT EXISTS anvil_agents_chat.threads (
    id TEXT PRIMARY KEY,
    namespace TEXT NOT NULL,
    profile_name TEXT,
    mode TEXT NOT NULL,
    title TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by TEXT NOT NULL,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    CONSTRAINT threads_mode_check CHECK (mode IN ('persona', 'fleet')),
    CONSTRAINT threads_persona_profile_check CHECK (
        mode <> 'persona' OR (profile_name IS NOT NULL AND profile_name <> '')
    )
);
CREATE INDEX IF NOT EXISTS threads_namespace_updated_idx
    ON anvil_agents_chat.threads (namespace, updated_at DESC);
CREATE INDEX IF NOT EXISTS threads_namespace_profile_updated_idx
    ON anvil_agents_chat.threads (namespace, profile_name, updated_at DESC);

CREATE TABLE IF NOT EXISTS anvil_agents_chat.messages (
    id TEXT PRIMARY KEY,
    thread_id TEXT NOT NULL REFERENCES anvil_agents_chat.threads (id) ON DELETE CASCADE,
    role TEXT NOT NULL,
    content TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    sequence BIGINT NOT NULL,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    CONSTRAINT messages_role_check CHECK (role IN ('system', 'user', 'assistant', 'tool')),
    CONSTRAINT messages_thread_sequence_unique UNIQUE (thread_id, sequence)
);
CREATE INDEX IF NOT EXISTS messages_thread_sequence_idx
    ON anvil_agents_chat.messages (thread_id, sequence);

CREATE TABLE IF NOT EXISTS anvil_agents_chat.checkpoints (
    id TEXT PRIMARY KEY,
    thread_id TEXT NOT NULL REFERENCES anvil_agents_chat.threads (id) ON DELETE CASCADE,
    checkpoint_id TEXT NOT NULL,
    parent_checkpoint_id TEXT,
    checkpoint JSONB NOT NULL DEFAULT '{}'::jsonb,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT checkpoints_thread_checkpoint_unique UNIQUE (thread_id, checkpoint_id)
);
`

type Store interface {
	CreateThread(ctx context.Context, thread Thread) (Thread, error)
	ListThreads(ctx context.Context, filter ThreadFilter) ([]Thread, error)
	GetThread(ctx context.Context, namespace, id string) (Thread, error)
	ListMessages(ctx context.Context, namespace, threadID string) ([]Message, error)
	AppendMessages(ctx context.Context, namespace, threadID string, messages []Message) ([]Message, Thread, error)
	Ping(ctx context.Context) error
	Close()
}

type Thread struct {
	ID          string          `json:"id"`
	Namespace   string          `json:"namespace"`
	ProfileName string          `json:"profileName,omitempty"`
	Mode        string          `json:"mode"`
	Title       string          `json:"title"`
	CreatedAt   time.Time       `json:"createdAt"`
	UpdatedAt   time.Time       `json:"updatedAt"`
	CreatedBy   string          `json:"createdBy"`
	Metadata    json.RawMessage `json:"metadata"`
}

type Message struct {
	ID        string          `json:"id"`
	ThreadID  string          `json:"threadId"`
	Role      string          `json:"role"`
	Content   string          `json:"content"`
	CreatedAt time.Time       `json:"createdAt"`
	Sequence  int64           `json:"sequence"`
	Metadata  json.RawMessage `json:"metadata"`
}

type ThreadFilter struct {
	Namespace   string
	ProfileName string
	Mode        string
	Limit       int
}

type PostgresStore struct {
	pool *pgxpool.Pool
}

func OpenPostgresStore(ctx context.Context, databaseURL string) (*PostgresStore, error) {
	databaseURL = strings.TrimSpace(databaseURL)
	if databaseURL == "" {
		return nil, fmt.Errorf("database URL is required")
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse standing-chat database URL: %w", err)
	}
	config.MaxConns = 4
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("open standing-chat database: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping standing-chat database: %w", err)
	}
	store := &PostgresStore{pool: pool}
	if err := store.Migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return store, nil
}

func (s *PostgresStore) Close() {
	if s != nil && s.pool != nil {
		s.pool.Close()
	}
}

func (s *PostgresStore) Ping(ctx context.Context) error {
	if s == nil || s.pool == nil {
		return fmt.Errorf("standing-chat store is not configured")
	}
	return s.pool.Ping(ctx)
}

func (s *PostgresStore) Migrate(ctx context.Context) error {
	if _, err := s.pool.Exec(ctx, chatSchema); err != nil {
		return fmt.Errorf("migrate standing-chat schema: %w", err)
	}
	return nil
}

func (s *PostgresStore) CreateThread(ctx context.Context, thread Thread) (Thread, error) {
	normalized, err := NormalizeThread(thread, time.Now().UTC())
	if err != nil {
		return Thread{}, err
	}
	_, err = s.pool.Exec(ctx, `
INSERT INTO anvil_agents_chat.threads (
    id, namespace, profile_name, mode, title, created_at, updated_at, created_by, metadata
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		normalized.ID,
		normalized.Namespace,
		nullableString(normalized.ProfileName),
		normalized.Mode,
		normalized.Title,
		normalized.CreatedAt.UTC(),
		normalized.UpdatedAt.UTC(),
		normalized.CreatedBy,
		[]byte(normalized.Metadata),
	)
	if err != nil {
		return Thread{}, fmt.Errorf("create standing-chat thread: %w", err)
	}
	return normalized, nil
}

func (s *PostgresStore) ListThreads(ctx context.Context, filter ThreadFilter) ([]Thread, error) {
	filter, err := normalizeThreadFilter(filter)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
SELECT id, namespace, profile_name, mode, title, created_at, updated_at, created_by, metadata
FROM anvil_agents_chat.threads
WHERE namespace = $1
  AND ($2::text IS NULL OR profile_name = $2)
  AND ($3::text IS NULL OR mode = $3)
ORDER BY updated_at DESC
LIMIT $4`,
		filter.Namespace,
		nullableString(filter.ProfileName),
		nullableString(filter.Mode),
		filter.Limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list standing-chat threads: %w", err)
	}
	defer rows.Close()
	var threads []Thread
	for rows.Next() {
		thread, err := scanThread(rows)
		if err != nil {
			return nil, err
		}
		threads = append(threads, thread)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list standing-chat threads: %w", err)
	}
	if threads == nil {
		threads = []Thread{}
	}
	return threads, nil
}

func (s *PostgresStore) GetThread(ctx context.Context, namespace, id string) (Thread, error) {
	namespace, id, err := normalizeThreadLookup(namespace, id)
	if err != nil {
		return Thread{}, err
	}
	row := s.pool.QueryRow(ctx, `
SELECT id, namespace, profile_name, mode, title, created_at, updated_at, created_by, metadata
FROM anvil_agents_chat.threads
WHERE namespace = $1 AND id = $2`, namespace, id)
	thread, err := scanThread(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Thread{}, fmt.Errorf("%w: thread %s/%s", ErrNotFound, namespace, id)
	}
	if err != nil {
		return Thread{}, fmt.Errorf("get standing-chat thread: %w", err)
	}
	return thread, nil
}

func (s *PostgresStore) ListMessages(ctx context.Context, namespace, threadID string) ([]Message, error) {
	if _, err := s.GetThread(ctx, namespace, threadID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
SELECT id, thread_id, role, content, created_at, sequence, metadata
FROM anvil_agents_chat.messages
WHERE thread_id = $1
ORDER BY sequence ASC`, threadID)
	if err != nil {
		return nil, fmt.Errorf("list standing-chat messages: %w", err)
	}
	defer rows.Close()
	var messages []Message
	for rows.Next() {
		message, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list standing-chat messages: %w", err)
	}
	if messages == nil {
		messages = []Message{}
	}
	return messages, nil
}

func (s *PostgresStore) AppendMessages(ctx context.Context, namespace, threadID string, messages []Message) ([]Message, Thread, error) {
	namespace, threadID, err := normalizeThreadLookup(namespace, threadID)
	if err != nil {
		return nil, Thread{}, err
	}
	if len(messages) == 0 {
		return nil, Thread{}, fmt.Errorf("%w: at least one message is required", ErrInvalid)
	}
	if len(messages) > maxAppendBatch {
		return nil, Thread{}, fmt.Errorf("%w: at most %d messages may be appended at once", ErrInvalid, maxAppendBatch)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, Thread{}, fmt.Errorf("begin standing-chat append: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	row := tx.QueryRow(ctx, `
SELECT id, namespace, profile_name, mode, title, created_at, updated_at, created_by, metadata
FROM anvil_agents_chat.threads
WHERE namespace = $1 AND id = $2
FOR UPDATE`, namespace, threadID)
	thread, err := scanThread(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, Thread{}, fmt.Errorf("%w: thread %s/%s", ErrNotFound, namespace, threadID)
	}
	if err != nil {
		return nil, Thread{}, fmt.Errorf("lock standing-chat thread: %w", err)
	}

	var next int64
	if err := tx.QueryRow(ctx, `
SELECT COALESCE(MAX(sequence), 0)
FROM anvil_agents_chat.messages
WHERE thread_id = $1`, threadID).Scan(&next); err != nil {
		return nil, Thread{}, fmt.Errorf("read standing-chat sequence: %w", err)
	}

	now := time.Now().UTC()
	stored := make([]Message, 0, len(messages))
	for _, incoming := range messages {
		incoming.ThreadID = threadID
		next++
		incoming.Sequence = next
		normalized, err := NormalizeMessage(incoming, now)
		if err != nil {
			return nil, Thread{}, err
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO anvil_agents_chat.messages (
    id, thread_id, role, content, created_at, sequence, metadata
) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			normalized.ID,
			normalized.ThreadID,
			normalized.Role,
			normalized.Content,
			normalized.CreatedAt.UTC(),
			normalized.Sequence,
			[]byte(normalized.Metadata),
		); err != nil {
			return nil, Thread{}, fmt.Errorf("insert standing-chat message: %w", err)
		}
		stored = append(stored, normalized)
		if thread.Title == "" || thread.Title == DefaultThreadTitle {
			if normalized.Role == RoleUser {
				thread.Title = titleFromContent(normalized.Content)
			}
		}
	}
	thread.UpdatedAt = now
	if _, err := tx.Exec(ctx, `
UPDATE anvil_agents_chat.threads
SET title = $1, updated_at = $2
WHERE namespace = $3 AND id = $4`,
		thread.Title, thread.UpdatedAt.UTC(), namespace, threadID); err != nil {
		return nil, Thread{}, fmt.Errorf("touch standing-chat thread: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, Thread{}, fmt.Errorf("commit standing-chat append: %w", err)
	}
	return stored, thread, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanThread(row rowScanner) (Thread, error) {
	var thread Thread
	var profileName *string
	var metadata []byte
	if err := row.Scan(
		&thread.ID,
		&thread.Namespace,
		&profileName,
		&thread.Mode,
		&thread.Title,
		&thread.CreatedAt,
		&thread.UpdatedAt,
		&thread.CreatedBy,
		&metadata,
	); err != nil {
		return Thread{}, err
	}
	if profileName != nil {
		thread.ProfileName = *profileName
	}
	thread.CreatedAt = thread.CreatedAt.UTC()
	thread.UpdatedAt = thread.UpdatedAt.UTC()
	thread.Metadata = json.RawMessage(metadata)
	if len(thread.Metadata) == 0 {
		thread.Metadata = json.RawMessage(`{}`)
	}
	return thread, nil
}

func scanMessage(row rowScanner) (Message, error) {
	var message Message
	var metadata []byte
	if err := row.Scan(
		&message.ID,
		&message.ThreadID,
		&message.Role,
		&message.Content,
		&message.CreatedAt,
		&message.Sequence,
		&metadata,
	); err != nil {
		return Message{}, err
	}
	message.CreatedAt = message.CreatedAt.UTC()
	message.Metadata = json.RawMessage(metadata)
	if len(message.Metadata) == 0 {
		message.Metadata = json.RawMessage(`{}`)
	}
	return message, nil
}

func NormalizeThread(thread Thread, now time.Time) (Thread, error) {
	thread.ID = strings.TrimSpace(thread.ID)
	thread.Namespace = strings.TrimSpace(thread.Namespace)
	thread.ProfileName = strings.TrimSpace(thread.ProfileName)
	thread.Mode = strings.TrimSpace(thread.Mode)
	thread.Title = strings.TrimSpace(thread.Title)
	thread.CreatedBy = strings.TrimSpace(thread.CreatedBy)
	if thread.Mode == "" {
		thread.Mode = ModePersona
	}
	if thread.Mode != ModePersona && thread.Mode != ModeFleet {
		return Thread{}, fmt.Errorf("%w: mode must be persona or fleet", ErrInvalid)
	}
	if thread.Namespace == "" {
		return Thread{}, fmt.Errorf("%w: namespace is required", ErrInvalid)
	}
	if thread.CreatedBy == "" {
		return Thread{}, fmt.Errorf("%w: createdBy is required", ErrInvalid)
	}
	if thread.Mode == ModePersona && thread.ProfileName == "" {
		return Thread{}, fmt.Errorf("%w: profileName is required for persona threads", ErrInvalid)
	}
	if thread.Title == "" {
		thread.Title = DefaultThreadTitle
	}
	if utf8.RuneCountInString(thread.Title) > maxTitleRunes {
		return Thread{}, fmt.Errorf("%w: title must be at most %d characters", ErrInvalid, maxTitleRunes)
	}
	metadata, err := normalizeMetadata(thread.Metadata)
	if err != nil {
		return Thread{}, err
	}
	thread.Metadata = metadata
	if thread.ID == "" {
		thread.ID = uuid.NewString()
	} else if _, err := uuid.Parse(thread.ID); err != nil {
		return Thread{}, fmt.Errorf("%w: thread id must be a UUID", ErrInvalid)
	}
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	if thread.CreatedAt.IsZero() {
		thread.CreatedAt = now
	} else {
		thread.CreatedAt = thread.CreatedAt.UTC()
	}
	if thread.UpdatedAt.IsZero() {
		thread.UpdatedAt = thread.CreatedAt
	} else {
		thread.UpdatedAt = thread.UpdatedAt.UTC()
	}
	return thread, nil
}

func NormalizeMessage(message Message, now time.Time) (Message, error) {
	message.ID = strings.TrimSpace(message.ID)
	message.ThreadID = strings.TrimSpace(message.ThreadID)
	message.Role = strings.TrimSpace(message.Role)
	if message.Role != RoleSystem && message.Role != RoleUser && message.Role != RoleAssistant && message.Role != RoleTool {
		return Message{}, fmt.Errorf("%w: role must be system, user, assistant, or tool", ErrInvalid)
	}
	if strings.TrimSpace(message.Content) == "" {
		return Message{}, fmt.Errorf("%w: content is required", ErrInvalid)
	}
	if len(message.Content) > maxMessageBytes {
		return Message{}, fmt.Errorf("%w: content exceeds %d bytes", ErrInvalid, maxMessageBytes)
	}
	if message.ThreadID == "" {
		return Message{}, fmt.Errorf("%w: threadId is required", ErrInvalid)
	}
	if _, err := uuid.Parse(message.ThreadID); err != nil {
		return Message{}, fmt.Errorf("%w: thread id must be a UUID", ErrInvalid)
	}
	if message.Sequence < 1 {
		return Message{}, fmt.Errorf("%w: sequence must be positive", ErrInvalid)
	}
	metadata, err := normalizeMetadata(message.Metadata)
	if err != nil {
		return Message{}, err
	}
	message.Metadata = metadata
	if message.ID == "" {
		message.ID = uuid.NewString()
	} else if _, err := uuid.Parse(message.ID); err != nil {
		return Message{}, fmt.Errorf("%w: message id must be a UUID", ErrInvalid)
	}
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	if message.CreatedAt.IsZero() {
		message.CreatedAt = now
	} else {
		message.CreatedAt = message.CreatedAt.UTC()
	}
	return message, nil
}

func normalizeThreadFilter(filter ThreadFilter) (ThreadFilter, error) {
	filter.Namespace = strings.TrimSpace(filter.Namespace)
	filter.ProfileName = strings.TrimSpace(filter.ProfileName)
	filter.Mode = strings.TrimSpace(filter.Mode)
	if filter.Namespace == "" {
		return ThreadFilter{}, fmt.Errorf("%w: namespace is required", ErrInvalid)
	}
	if filter.Mode != "" && filter.Mode != ModePersona && filter.Mode != ModeFleet {
		return ThreadFilter{}, fmt.Errorf("%w: mode must be persona or fleet", ErrInvalid)
	}
	if filter.Limit <= 0 {
		filter.Limit = defaultListLimit
	}
	if filter.Limit > maxListLimit {
		return ThreadFilter{}, fmt.Errorf("%w: limit must be between 1 and %d", ErrInvalid, maxListLimit)
	}
	return filter, nil
}

func normalizeThreadLookup(namespace, id string) (string, string, error) {
	namespace = strings.TrimSpace(namespace)
	id = strings.TrimSpace(id)
	if namespace == "" {
		return "", "", fmt.Errorf("%w: namespace is required", ErrInvalid)
	}
	parsed, err := uuid.Parse(id)
	if err != nil {
		return "", "", fmt.Errorf("%w: thread id must be a UUID", ErrInvalid)
	}
	return namespace, parsed.String(), nil
}

func normalizeMetadata(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return json.RawMessage(`{}`), nil
	}
	if len(raw) > maxMetadataBytes {
		return nil, fmt.Errorf("%w: metadata exceeds %d bytes", ErrInvalid, maxMetadataBytes)
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return json.RawMessage(`{}`), nil
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, fmt.Errorf("%w: metadata must be a JSON object", ErrInvalid)
	}
	return json.RawMessage(trimmed), nil
}

func nullableString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return strings.TrimSpace(value)
}

func titleFromContent(content string) string {
	collapsed := strings.Join(strings.Fields(strings.TrimSpace(content)), " ")
	if collapsed == "" {
		return DefaultThreadTitle
	}
	runes := []rune(collapsed)
	if len(runes) > 80 {
		return string(runes[:80])
	}
	return collapsed
}
