package chat

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// MemoryStore is an in-process standing-chat store for API tests.
type MemoryStore struct {
	mu         sync.Mutex
	threads    map[string]Thread
	messages   map[string][]Message
	knowledge  map[string][]KnowledgeEntry
	memory     map[string]map[string]MemoryEntry
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		threads:   map[string]Thread{},
		messages:  map[string][]Message{},
		knowledge: map[string][]KnowledgeEntry{},
		memory:    map[string]map[string]MemoryEntry{},
	}
}

func (s *MemoryStore) threadKey(namespace, id string) string {
	return namespace + "/" + id
}

func (s *MemoryStore) Ping(context.Context) error { return nil }

func (s *MemoryStore) Close() {}

func (s *MemoryStore) CreateThread(_ context.Context, thread Thread) (Thread, error) {
	normalized, err := NormalizeThread(thread, time.Now().UTC())
	if err != nil {
		return Thread{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := s.threadKey(normalized.Namespace, normalized.ID)
	if _, exists := s.threads[key]; exists {
		return Thread{}, fmt.Errorf("%w: thread %s already exists", ErrInvalid, normalized.ID)
	}
	s.threads[key] = cloneThread(normalized)
	s.messages[key] = []Message{}
	return cloneThread(normalized), nil
}

func (s *MemoryStore) ListThreads(_ context.Context, filter ThreadFilter) ([]Thread, error) {
	filter, err := normalizeThreadFilter(filter)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Thread, 0)
	for _, thread := range s.threads {
		if thread.Namespace != filter.Namespace {
			continue
		}
		if filter.ProfileName != "" && thread.ProfileName != filter.ProfileName {
			continue
		}
		if filter.Mode != "" && thread.Mode != filter.Mode {
			continue
		}
		if filter.CouncilName != "" && thread.CouncilName != filter.CouncilName {
			continue
		}
		out = append(out, cloneThread(thread))
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	if len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

func (s *MemoryStore) GetThread(_ context.Context, namespace, id string) (Thread, error) {
	namespace, id, err := normalizeThreadLookup(namespace, id)
	if err != nil {
		return Thread{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	thread, ok := s.threads[s.threadKey(namespace, id)]
	if !ok {
		return Thread{}, fmt.Errorf("%w: thread %s/%s", ErrNotFound, namespace, id)
	}
	return cloneThread(thread), nil
}

func (s *MemoryStore) ListMessages(_ context.Context, namespace, threadID string) ([]Message, error) {
	namespace, threadID, err := normalizeThreadLookup(namespace, threadID)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := s.threadKey(namespace, threadID)
	if _, ok := s.threads[key]; !ok {
		return nil, fmt.Errorf("%w: thread %s/%s", ErrNotFound, namespace, threadID)
	}
	return cloneMessages(s.messages[key]), nil
}

func (s *MemoryStore) AppendMessages(_ context.Context, namespace, threadID string, messages []Message) ([]Message, Thread, error) {
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
	s.mu.Lock()
	defer s.mu.Unlock()
	key := s.threadKey(namespace, threadID)
	thread, ok := s.threads[key]
	if !ok {
		return nil, Thread{}, fmt.Errorf("%w: thread %s/%s", ErrNotFound, namespace, threadID)
	}
	existing := s.messages[key]
	next := int64(len(existing))
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
		stored = append(stored, normalized)
		if thread.Title == "" || thread.Title == DefaultThreadTitle {
			if normalized.Role == RoleUser {
				thread.Title = titleFromContent(normalized.Content)
			}
		}
	}
	thread.UpdatedAt = now
	s.threads[key] = thread
	s.messages[key] = append(existing, stored...)
	return cloneMessages(stored), cloneThread(thread), nil
}

func (s *MemoryStore) councilKey(namespace, councilName string) string {
	return namespace + "/" + councilName
}

func (s *MemoryStore) EnsureCouncilThread(ctx context.Context, thread Thread) (Thread, bool, error) {
	thread.Mode = ModeCouncil
	existing, err := s.ListThreads(ctx, ThreadFilter{
		Namespace:   strings.TrimSpace(thread.Namespace),
		CouncilName: strings.TrimSpace(thread.CouncilName),
		Mode:        ModeCouncil,
		Limit:       1,
	})
	if err != nil {
		return Thread{}, false, err
	}
	if len(existing) > 0 {
		return existing[0], false, nil
	}
	created, err := s.CreateThread(ctx, thread)
	if err != nil {
		return Thread{}, false, err
	}
	return created, true, nil
}

func (s *MemoryStore) ListKnowledge(_ context.Context, namespace, councilName string) ([]KnowledgeEntry, error) {
	namespace, councilName, err := normalizeCouncilLookup(namespace, councilName)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entries := s.knowledge[s.councilKey(namespace, councilName)]
	if entries == nil {
		return []KnowledgeEntry{}, nil
	}
	out := make([]KnowledgeEntry, len(entries))
	copy(out, entries)
	return out, nil
}

func (s *MemoryStore) SeedKnowledge(_ context.Context, entries []KnowledgeEntry) error {
	if len(entries) == 0 {
		return nil
	}
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, incoming := range entries {
		normalized, err := normalizeKnowledge(incoming, now)
		if err != nil {
			return err
		}
		key := s.councilKey(normalized.Namespace, normalized.CouncilName)
		if len(s.knowledge[key]) > 0 {
			return nil
		}
	}
	for _, incoming := range entries {
		normalized, err := normalizeKnowledge(incoming, now)
		if err != nil {
			return err
		}
		key := s.councilKey(normalized.Namespace, normalized.CouncilName)
		s.knowledge[key] = append(s.knowledge[key], normalized)
	}
	return nil
}

func (s *MemoryStore) ListMemory(_ context.Context, namespace, councilName string) ([]MemoryEntry, error) {
	namespace, councilName, err := normalizeCouncilLookup(namespace, councilName)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	byKey := s.memory[s.councilKey(namespace, councilName)]
	out := make([]MemoryEntry, 0, len(byKey))
	for _, entry := range byKey {
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out, nil
}

func (s *MemoryStore) UpsertMemory(_ context.Context, entry MemoryEntry) (MemoryEntry, error) {
	normalized, err := normalizeMemory(entry, time.Now().UTC())
	if err != nil {
		return MemoryEntry{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := s.councilKey(normalized.Namespace, normalized.CouncilName)
	if s.memory[key] == nil {
		s.memory[key] = map[string]MemoryEntry{}
	}
	if existing, ok := s.memory[key][normalized.Key]; ok {
		normalized.ID = existing.ID
	}
	s.memory[key][normalized.Key] = normalized
	return normalized, nil
}

func cloneThread(thread Thread) Thread {
	cloned := thread
	if thread.Metadata != nil {
		cloned.Metadata = append([]byte(nil), thread.Metadata...)
	}
	return cloned
}

func cloneMessages(messages []Message) []Message {
	if messages == nil {
		return []Message{}
	}
	out := make([]Message, len(messages))
	for i, message := range messages {
		out[i] = message
		if message.Metadata != nil {
			out[i].Metadata = append([]byte(nil), message.Metadata...)
		}
	}
	return out
}
