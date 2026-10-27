package onechat

import (
	"bytes"
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/google/uuid"
)

type Event struct {
	Name string
	At   time.Time
	Body any
}

type turnIDKey struct{}

// WithTurnID lets a host announce a turn before starting it and retrieve its
// saved trace under the same ID. A typed UUID cannot introduce a trace path.
func WithTurnID(ctx context.Context, id uuid.UUID) context.Context {
	return context.WithValue(ctx, turnIDKey{}, id)
}

func (m *Manager) emit(observe func(Event), name string, body any) {
	if observe == nil {
		return
	}
	// Preserve integer usage counts while detaching nested slices and pointers
	// from the trace, so an observer cannot change the result or manager state.
	encoded, err := json.Marshal(body)
	if err != nil {
		return
	}
	raw := json.RawMessage(encoded)
	if err := m.sanitize(&raw); err != nil {
		return
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&body); err != nil {
		return
	}
	observe(Event{Name: name, At: time.Now().UTC(), Body: body})
}

func inventoryEventSessions(sessions []Session) []map[string]string {
	out := make([]map[string]string, 0, len(sessions))
	for _, s := range sessions {
		out = append(out, map[string]string{"id": s.ID, "location": s.Location, "harness": s.Harness, "title": s.Title, "state": s.State})
	}
	return out
}

func cloneSessions(sessions []Session) []Session {
	out := append([]Session{}, sessions...)
	for i := range out {
		out[i].Transcript = append([]Message{}, sessions[i].Transcript...)
	}
	return out
}

// Called while the turn mutex is held. Readers only see immutable, sanitized
// copies, including full transcripts rather than the bounded model inventory.
func (m *Manager) refreshInventorySnapshot(at time.Time) {
	sessions := cloneSessions(m.state.Sessions)
	for _, s := range m.cloudSessions {
		s.Transcript = append([]Message{}, s.Transcript...)
		sessions = append(sessions, s)
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].ID < sessions[j].ID })
	if err := m.sanitize(&sessions); err != nil {
		return
	}
	m.snapshotMu.Lock()
	m.lastInventory, m.inventoryAt = sessions, at
	m.snapshotMu.Unlock()
}

func (m *Manager) refreshHistorySnapshot() {
	history := append([]Message{}, m.state.History...)
	if err := m.sanitize(&history); err != nil {
		return
	}
	m.snapshotMu.Lock()
	m.history = history
	m.snapshotMu.Unlock()
}

// LastInventory never waits for a turn or performs backend I/O. Its time is zero
// until the first successful inventory read or completed action in this process.
func (m *Manager) LastInventory() ([]Session, time.Time) {
	m.snapshotMu.RLock()
	sessions, at := m.lastInventory, m.inventoryAt
	m.snapshotMu.RUnlock()
	return cloneSessions(sessions), at
}

// History returns the newest limit messages, in conversation order, without
// waiting for a turn. A nonpositive limit returns the entire history.
func (m *Manager) History(limit int) []Message {
	m.snapshotMu.RLock()
	history := m.history
	m.snapshotMu.RUnlock()
	if limit > 0 && len(history) > limit {
		history = history[len(history)-limit:]
	}
	return append([]Message{}, history...)
}
