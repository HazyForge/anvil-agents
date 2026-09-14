package chat

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

// MemoryStore is an in-process standing-chat store for API tests.
type MemoryStore struct {
	mu       sync.Mutex
	threads  map[string]Thread
	messages map[string][]Message
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		threads:  map[string]Thread{},
		messages: map[string][]Message{},
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
