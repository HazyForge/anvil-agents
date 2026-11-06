package chat

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
	"unicode/utf8"
)

var _ SubjectStore = (*MemoryStore)(nil)

type memoryRoutingLock struct {
	token chan struct{}
	refs  int
}

func (s *MemoryStore) ListSubjects(_ context.Context, ns, owner string, limit int) ([]Subject, error) {
	if err := validateSubjectScope(ns, owner); err != nil {
		return nil, err
	}
	limit, err := subjectLimit(limit)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Subject{}
	for _, subject := range s.subjects {
		if subject.Namespace == ns && subject.Owner == owner && subject.Status != "merged" {
			out = append(out, cloneSubject(subject))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if (out[i].Status == "done") != (out[j].Status == "done") {
			return out[j].Status == "done"
		}
		if out[i].TouchedAt.Equal(out[j].TouchedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].TouchedAt.After(out[j].TouchedAt)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
func (s *MemoryStore) GetSubject(_ context.Context, ns, owner, id string) (Subject, error) {
	if err := validateSubjectLookup(ns, owner, id); err != nil {
		return Subject{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	subject, ok := s.subjects[subjectKey(ns, owner, id)]
	if !ok {
		return Subject{}, ErrNotFound
	}
	return cloneSubject(subject), nil
}
func (s *MemoryStore) PutSubject(_ context.Context, in Subject) (Subject, error) {
	subject, err := normalizeSubject(in)
	if err != nil {
		return Subject{}, err
	}
	if subject.Status == "merged" {
		return Subject{}, fmt.Errorf("%w: use MergeSubjects to merge", ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	thread, ok := s.threads[s.threadKey(subject.Namespace, subject.ThreadID)]
	if !ok || thread.CreatedBy != subject.Owner || thread.ProfileName != subject.ProfileName {
		return Subject{}, ErrNotFound
	}
	if s.subjects == nil {
		s.subjects = map[string]Subject{}
	}
	key := subjectKey(subject.Namespace, subject.Owner, subject.ID)
	if previous, ok := s.subjects[key]; ok {
		if previous.Status == "merged" {
			return Subject{}, ErrNotFound
		}
		subject.OpenedAt = previous.OpenedAt
	}
	for _, other := range s.subjects {
		if other.Namespace == subject.Namespace && other.Owner == subject.Owner && other.ThreadID == subject.ThreadID && other.ID != subject.ID {
			return Subject{}, fmt.Errorf("%w: thread already has a subject", ErrInvalid)
		}
	}
	s.subjects[key] = cloneSubject(subject)
	return cloneSubject(subject), nil
}
func (s *MemoryStore) TouchSubject(_ context.Context, ns, owner, id, summary string) (Subject, error) {
	if err := validateSubjectLookup(ns, owner, id); err != nil {
		return Subject{}, err
	}
	if len(summary) > 4096 || !utf8.ValidString(summary) {
		return Subject{}, fmt.Errorf("%w: invalid subject summary", ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := subjectKey(ns, owner, id)
	subject, ok := s.subjects[key]
	if !ok || subject.Status == "merged" {
		return Subject{}, ErrNotFound
	}
	subject.Status = "open"
	subject.TouchedAt = time.Now().UTC()
	if summary != "" {
		subject.Summary = summary
	}
	s.subjects[key] = subject
	return cloneSubject(subject), nil
}
func (s *MemoryStore) MergeSubjects(_ context.Context, ns, owner, sourceID, targetID string) (Subject, error) {
	if err := validateSubjectLookup(ns, owner, sourceID); err != nil {
		return Subject{}, err
	}
	if err := validateSubjectLookup(ns, owner, targetID); err != nil {
		return Subject{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	source, ok := s.subjects[subjectKey(ns, owner, sourceID)]
	if !ok {
		return Subject{}, ErrNotFound
	}
	target, ok := s.subjects[subjectKey(ns, owner, targetID)]
	if !ok {
		return Subject{}, ErrNotFound
	}
	source, target, err := mergeSubjectValues(cloneSubject(source), cloneSubject(target))
	if err != nil {
		return Subject{}, err
	}
	s.subjects[subjectKey(ns, owner, sourceID)] = source
	s.subjects[subjectKey(ns, owner, targetID)] = target
	return cloneSubject(target), nil
}
func (s *MemoryStore) GetRoutingRequest(_ context.Context, ns, owner, id string) (RoutingRequest, error) {
	if err := validateSubjectLookup(ns, owner, id); err != nil {
		return RoutingRequest{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	request, ok := s.routingRequests[subjectKey(ns, owner, id)]
	if !ok {
		return RoutingRequest{}, ErrNotFound
	}
	return cloneRoutingRequest(request), nil
}
func (s *MemoryStore) ListRoutingRequests(_ context.Context, ns, owner string, limit int) ([]RoutingRequest, error) {
	if err := validateSubjectScope(ns, owner); err != nil {
		return nil, err
	}
	limit, err := subjectLimit(limit)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []RoutingRequest{}
	for _, request := range s.routingRequests {
		if request.Namespace == ns && request.Owner == owner {
			out = append(out, cloneRoutingRequest(request))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
func (s *MemoryStore) SaveRoutingRequest(_ context.Context, in RoutingRequest) (RoutingRequest, error) {
	request, err := normalizeRoutingRequest(in)
	if err != nil {
		return RoutingRequest{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.routingRequests == nil {
		s.routingRequests = map[string]RoutingRequest{}
	}
	key := subjectKey(request.Namespace, request.Owner, request.ID)
	if current, ok := s.routingRequests[key]; ok {
		request, err = updateRoutingRequest(current, request)
		if err != nil {
			return RoutingRequest{}, err
		}
	}
	s.routingRequests[key] = cloneRoutingRequest(request)
	return cloneRoutingRequest(request), nil
}
func (s *MemoryStore) AcquireRoutingRequest(ctx context.Context, ns, owner, id string) (func(), error) {
	if err := validateSubjectLookup(ns, owner, id); err != nil {
		return nil, err
	}
	key := subjectKey(ns, owner, id)
	s.mu.Lock()
	if s.routingLocks == nil {
		s.routingLocks = map[string]*memoryRoutingLock{}
	}
	lock, ok := s.routingLocks[key]
	if !ok {
		lock = &memoryRoutingLock{token: make(chan struct{}, 1)}
		lock.token <- struct{}{}
		s.routingLocks[key] = lock
	}
	lock.refs++
	s.mu.Unlock()
	drop := func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		lock.refs--
		if lock.refs == 0 {
			delete(s.routingLocks, key)
		}
	}
	select {
	case <-ctx.Done():
		drop()
		return nil, ctx.Err()
	case <-lock.token:
	}
	var once sync.Once
	return func() { once.Do(func() { lock.token <- struct{}{}; drop() }) }, nil
}
