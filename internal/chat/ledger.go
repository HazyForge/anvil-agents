package chat

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const schemaMigrationCouncil = 2

const councilSchemaSQL = `
CREATE TABLE IF NOT EXISTS anvil_agents_chat.schema_migrations (
    version INTEGER PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

ALTER TABLE anvil_agents_chat.threads
    ADD COLUMN IF NOT EXISTS council_name TEXT;

ALTER TABLE anvil_agents_chat.threads
    DROP CONSTRAINT IF EXISTS threads_mode_check;
ALTER TABLE anvil_agents_chat.threads
    ADD CONSTRAINT threads_mode_check CHECK (mode IN ('persona', 'fleet', 'council'));

ALTER TABLE anvil_agents_chat.threads
    DROP CONSTRAINT IF EXISTS threads_council_name_check;
ALTER TABLE anvil_agents_chat.threads
    ADD CONSTRAINT threads_council_name_check CHECK (
        mode <> 'council' OR (council_name IS NOT NULL AND council_name <> '')
    );

CREATE UNIQUE INDEX IF NOT EXISTS threads_canonical_council_idx
    ON anvil_agents_chat.threads (namespace, council_name)
    WHERE mode = 'council';

CREATE TABLE IF NOT EXISTS anvil_agents_chat.knowledge_entries (
    id TEXT PRIMARY KEY,
    namespace TEXT NOT NULL,
    council_name TEXT NOT NULL,
    title TEXT NOT NULL,
    body TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT knowledge_title_unique UNIQUE (namespace, council_name, title)
);
CREATE INDEX IF NOT EXISTS knowledge_council_idx
    ON anvil_agents_chat.knowledge_entries (namespace, council_name, created_at ASC);

CREATE TABLE IF NOT EXISTS anvil_agents_chat.memory_entries (
    id TEXT PRIMARY KEY,
    namespace TEXT NOT NULL,
    council_name TEXT NOT NULL,
    key TEXT NOT NULL,
    value TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT memory_key_unique UNIQUE (namespace, council_name, key)
);
CREATE INDEX IF NOT EXISTS memory_council_idx
    ON anvil_agents_chat.memory_entries (namespace, council_name, updated_at DESC);
`

type KnowledgeEntry struct {
	ID          string    `json:"id"`
	Namespace   string    `json:"namespace"`
	CouncilName string    `json:"councilName"`
	Title       string    `json:"title"`
	Body        string    `json:"body"`
	CreatedAt   time.Time `json:"createdAt"`
}

type MemoryEntry struct {
	ID          string    `json:"id"`
	Namespace   string    `json:"namespace"`
	CouncilName string    `json:"councilName"`
	Key         string    `json:"key"`
	Value       string    `json:"value"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

func (s *PostgresStore) applySchemaMigrations(ctx context.Context) error {
	if _, err := s.pool.Exec(ctx, councilSchemaSQL); err != nil {
		return fmt.Errorf("migrate council chat schema: %w", err)
	}
	if _, err := s.pool.Exec(ctx, `
INSERT INTO anvil_agents_chat.schema_migrations (version)
VALUES ($1)
ON CONFLICT (version) DO NOTHING`, schemaMigrationCouncil); err != nil {
		return fmt.Errorf("record council chat schema migration: %w", err)
	}
	return nil
}

func (s *PostgresStore) EnsureCouncilThread(ctx context.Context, thread Thread) (Thread, bool, error) {
	thread.Mode = ModeCouncil
	normalized, err := NormalizeThread(thread, time.Now().UTC())
	if err != nil {
		return Thread{}, false, err
	}
	existing, err := s.ListThreads(ctx, ThreadFilter{
		Namespace:   normalized.Namespace,
		CouncilName: normalized.CouncilName,
		Mode:        ModeCouncil,
		Limit:       1,
	})
	if err != nil {
		return Thread{}, false, err
	}
	if len(existing) > 0 {
		return existing[0], false, nil
	}
	created, err := s.CreateThread(ctx, normalized)
	if err != nil {
		existing, listErr := s.ListThreads(ctx, ThreadFilter{
			Namespace:   normalized.Namespace,
			CouncilName: normalized.CouncilName,
			Mode:        ModeCouncil,
			Limit:       1,
		})
		if listErr == nil && len(existing) > 0 {
			return existing[0], false, nil
		}
		return Thread{}, false, err
	}
	return created, true, nil
}

func (s *PostgresStore) ListKnowledge(ctx context.Context, namespace, councilName string) ([]KnowledgeEntry, error) {
	namespace, councilName, err := normalizeCouncilLookup(namespace, councilName)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
SELECT id, namespace, council_name, title, body, created_at
FROM anvil_agents_chat.knowledge_entries
WHERE namespace = $1 AND council_name = $2
ORDER BY created_at ASC`, namespace, councilName)
	if err != nil {
		return nil, fmt.Errorf("list council knowledge: %w", err)
	}
	defer rows.Close()
	var entries []KnowledgeEntry
	for rows.Next() {
		entry, err := scanKnowledge(rows)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list council knowledge: %w", err)
	}
	if entries == nil {
		entries = []KnowledgeEntry{}
	}
	return entries, nil
}

func (s *PostgresStore) SeedKnowledge(ctx context.Context, entries []KnowledgeEntry) error {
	if len(entries) == 0 {
		return nil
	}
	namespace := strings.TrimSpace(entries[0].Namespace)
	councilName := strings.TrimSpace(entries[0].CouncilName)
	existing, err := s.ListKnowledge(ctx, namespace, councilName)
	if err != nil {
		return err
	}
	if len(existing) > 0 {
		return nil
	}
	now := time.Now().UTC()
	for _, incoming := range entries {
		normalized, err := normalizeKnowledge(incoming, now)
		if err != nil {
			return err
		}
		if _, err := s.pool.Exec(ctx, `
INSERT INTO anvil_agents_chat.knowledge_entries (id, namespace, council_name, title, body, created_at)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (namespace, council_name, title) DO NOTHING`,
			normalized.ID,
			normalized.Namespace,
			normalized.CouncilName,
			normalized.Title,
			normalized.Body,
			normalized.CreatedAt.UTC(),
		); err != nil {
			return fmt.Errorf("seed council knowledge: %w", err)
		}
	}
	return nil
}

func (s *PostgresStore) ListMemory(ctx context.Context, namespace, councilName string) ([]MemoryEntry, error) {
	namespace, councilName, err := normalizeCouncilLookup(namespace, councilName)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
SELECT id, namespace, council_name, key, value, updated_at
FROM anvil_agents_chat.memory_entries
WHERE namespace = $1 AND council_name = $2
ORDER BY updated_at DESC`, namespace, councilName)
	if err != nil {
		return nil, fmt.Errorf("list council memory: %w", err)
	}
	defer rows.Close()
	var entries []MemoryEntry
	for rows.Next() {
		entry, err := scanMemory(rows)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list council memory: %w", err)
	}
	if entries == nil {
		entries = []MemoryEntry{}
	}
	return entries, nil
}

func (s *PostgresStore) UpsertMemory(ctx context.Context, entry MemoryEntry) (MemoryEntry, error) {
	normalized, err := normalizeMemory(entry, time.Now().UTC())
	if err != nil {
		return MemoryEntry{}, err
	}
	row := s.pool.QueryRow(ctx, `
INSERT INTO anvil_agents_chat.memory_entries (id, namespace, council_name, key, value, updated_at)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (namespace, council_name, key)
DO UPDATE SET value = EXCLUDED.value, updated_at = EXCLUDED.updated_at
RETURNING id, namespace, council_name, key, value, updated_at`,
		normalized.ID,
		normalized.Namespace,
		normalized.CouncilName,
		normalized.Key,
		normalized.Value,
		normalized.UpdatedAt.UTC(),
	)
	stored, err := scanMemory(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return MemoryEntry{}, fmt.Errorf("upsert council memory: empty result")
		}
		return MemoryEntry{}, fmt.Errorf("upsert council memory: %w", err)
	}
	return stored, nil
}

func scanKnowledge(row rowScanner) (KnowledgeEntry, error) {
	var entry KnowledgeEntry
	if err := row.Scan(&entry.ID, &entry.Namespace, &entry.CouncilName, &entry.Title, &entry.Body, &entry.CreatedAt); err != nil {
		return KnowledgeEntry{}, err
	}
	entry.CreatedAt = entry.CreatedAt.UTC()
	return entry, nil
}

func scanMemory(row rowScanner) (MemoryEntry, error) {
	var entry MemoryEntry
	if err := row.Scan(&entry.ID, &entry.Namespace, &entry.CouncilName, &entry.Key, &entry.Value, &entry.UpdatedAt); err != nil {
		return MemoryEntry{}, err
	}
	entry.UpdatedAt = entry.UpdatedAt.UTC()
	return entry, nil
}

func normalizeCouncilLookup(namespace, councilName string) (string, string, error) {
	namespace = strings.TrimSpace(namespace)
	councilName = strings.TrimSpace(councilName)
	if namespace == "" {
		return "", "", fmt.Errorf("%w: namespace is required", ErrInvalid)
	}
	if councilName == "" {
		return "", "", fmt.Errorf("%w: councilName is required", ErrInvalid)
	}
	return namespace, councilName, nil
}

func normalizeKnowledge(entry KnowledgeEntry, now time.Time) (KnowledgeEntry, error) {
	entry.ID = strings.TrimSpace(entry.ID)
	entry.Namespace = strings.TrimSpace(entry.Namespace)
	entry.CouncilName = strings.TrimSpace(entry.CouncilName)
	entry.Title = strings.TrimSpace(entry.Title)
	entry.Body = strings.TrimSpace(entry.Body)
	if entry.Namespace == "" || entry.CouncilName == "" || entry.Title == "" || entry.Body == "" {
		return KnowledgeEntry{}, fmt.Errorf("%w: knowledge title and body are required", ErrInvalid)
	}
	if utf8.RuneCountInString(entry.Title) > maxTitleRunes {
		return KnowledgeEntry{}, fmt.Errorf("%w: knowledge title must be at most %d characters", ErrInvalid, maxTitleRunes)
	}
	if len(entry.Body) > maxMessageBytes {
		return KnowledgeEntry{}, fmt.Errorf("%w: knowledge body exceeds %d bytes", ErrInvalid, maxMessageBytes)
	}
	if entry.ID == "" {
		entry.ID = uuid.NewString()
	} else if _, err := uuid.Parse(entry.ID); err != nil {
		return KnowledgeEntry{}, fmt.Errorf("%w: knowledge id must be a UUID", ErrInvalid)
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = now.UTC()
	} else {
		entry.CreatedAt = entry.CreatedAt.UTC()
	}
	return entry, nil
}

func normalizeMemory(entry MemoryEntry, now time.Time) (MemoryEntry, error) {
	entry.ID = strings.TrimSpace(entry.ID)
	entry.Namespace = strings.TrimSpace(entry.Namespace)
	entry.CouncilName = strings.TrimSpace(entry.CouncilName)
	entry.Key = strings.TrimSpace(entry.Key)
	entry.Value = strings.TrimSpace(entry.Value)
	if entry.Namespace == "" || entry.CouncilName == "" || entry.Key == "" || entry.Value == "" {
		return MemoryEntry{}, fmt.Errorf("%w: memory key and value are required", ErrInvalid)
	}
	if utf8.RuneCountInString(entry.Key) > maxTitleRunes {
		return MemoryEntry{}, fmt.Errorf("%w: memory key must be at most %d characters", ErrInvalid, maxTitleRunes)
	}
	if len(entry.Value) > maxMessageBytes {
		return MemoryEntry{}, fmt.Errorf("%w: memory value exceeds %d bytes", ErrInvalid, maxMessageBytes)
	}
	if entry.ID == "" {
		entry.ID = uuid.NewString()
	} else if _, err := uuid.Parse(entry.ID); err != nil {
		return MemoryEntry{}, fmt.Errorf("%w: memory id must be a UUID", ErrInvalid)
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	entry.UpdatedAt = now.UTC()
	return entry, nil
}
