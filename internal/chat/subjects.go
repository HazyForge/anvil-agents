package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Subject is the durable metadata around one owned conversation. Merging keeps
// the original conversation and receipts intact and redirects future routing.
type Subject struct {
	ID          string    `json:"id"`
	Namespace   string    `json:"namespace"`
	Owner       string    `json:"owner"`
	Kind        string    `json:"kind"`
	Label       string    `json:"label"`
	Aliases     []string  `json:"aliases"`
	Summary     string    `json:"summary"`
	ThreadID    string    `json:"threadId"`
	ProfileName string    `json:"profileName"`
	Status      string    `json:"status"`
	MergedInto  string    `json:"mergedInto,omitempty"`
	OpenedAt    time.Time `json:"openedAt"`
	TouchedAt   time.Time `json:"touchedAt"`
}

// RoutingRequest is the durable receipt for a manager message. The request hash
// binds content and execution target; clarification may refine only its route.
// Events and Result contain public, redacted API data, never provider responses.
type RoutingRequest struct {
	ID                 string          `json:"id"`
	Namespace          string          `json:"namespace"`
	Owner              string          `json:"owner"`
	RequestHash        string          `json:"-"`
	Content            string          `json:"content"`
	ProfileName        string          `json:"profileName"`
	RequestedSubjectID string          `json:"requestedSubjectId,omitempty"`
	Status             string          `json:"status"`
	SubjectID          string          `json:"subjectId,omitempty"`
	ThreadID           string          `json:"threadId,omitempty"`
	TurnID             string          `json:"turnId,omitempty"`
	Events             json.RawMessage `json:"events"`
	Result             json.RawMessage `json:"result,omitempty"`
	CreatedAt          time.Time       `json:"createdAt"`
	UpdatedAt          time.Time       `json:"updatedAt"`
}

// SubjectStore is separate from Store so existing chat backends do not silently
// claim support for durable subject routing. Scope always comes from OIDC.
type SubjectStore interface {
	ListSubjects(context.Context, string, string, int) ([]Subject, error)
	GetSubject(context.Context, string, string, string) (Subject, error)
	PutSubject(context.Context, Subject) (Subject, error)
	TouchSubject(context.Context, string, string, string, string) (Subject, error)
	MergeSubjects(context.Context, string, string, string, string) (Subject, error)
	GetRoutingRequest(context.Context, string, string, string) (RoutingRequest, error)
	ListRoutingRequests(context.Context, string, string, int) ([]RoutingRequest, error)
	SaveRoutingRequest(context.Context, RoutingRequest) (RoutingRequest, error)
	// AcquireRoutingRequest serializes processing of one request across replicas.
	// Release must be called. No DB transaction is held during inference or work.
	AcquireRoutingRequest(context.Context, string, string, string) (func(), error)
}

const subjectSchema = `
CREATE TABLE IF NOT EXISTS anvil_agents_chat.subjects (
 namespace TEXT NOT NULL,
 owner TEXT NOT NULL,
 id TEXT NOT NULL,
 thread_id TEXT NOT NULL REFERENCES anvil_agents_chat.threads(id) ON DELETE CASCADE,
 status TEXT NOT NULL CHECK (status IN ('open','parked','done','merged')),
 touched_at TIMESTAMPTZ NOT NULL,
 payload JSONB NOT NULL,
 PRIMARY KEY(namespace,owner,id),
 UNIQUE(namespace,owner,thread_id)
);
CREATE INDEX IF NOT EXISTS subjects_owner_recent_idx
 ON anvil_agents_chat.subjects(namespace,owner,touched_at DESC);
CREATE TABLE IF NOT EXISTS anvil_agents_chat.routing_requests (
 namespace TEXT NOT NULL,
 owner TEXT NOT NULL,
 id TEXT NOT NULL,
 request_hash TEXT NOT NULL,
 status TEXT NOT NULL CHECK (status IN ('routing','clarify','accepted','failed')),
 updated_at TIMESTAMPTZ NOT NULL,
 payload JSONB NOT NULL,
 PRIMARY KEY(namespace,owner,id)
);
CREATE INDEX IF NOT EXISTS routing_requests_owner_recent_idx
 ON anvil_agents_chat.routing_requests(namespace,owner,updated_at DESC);
`

func subjectKey(namespace, owner, id string) string { return namespace + "\x00" + owner + "\x00" + id }
func validateSubjectScope(namespace, owner string) error {
	for _, s := range []string{namespace, owner} {
		if s == "" || strings.TrimSpace(s) != s || len(s) > 1024 || strings.ContainsRune(s, 0) {
			return fmt.Errorf("%w: subject scope is required", ErrInvalid)
		}
	}
	return nil
}
func validateSubjectLookup(namespace, owner, id string) error {
	if err := validateSubjectScope(namespace, owner); err != nil {
		return err
	}
	if _, err := uuid.Parse(id); err != nil {
		return fmt.Errorf("%w: subject/request id must be a UUID", ErrInvalid)
	}
	return nil
}
func subjectLimit(limit int) (int, error) {
	if limit == 0 {
		return 50, nil
	}
	if limit < 1 || limit > 200 {
		return 0, fmt.Errorf("%w: limit must be between 1 and 200", ErrInvalid)
	}
	return limit, nil
}
func normalizeSubject(s Subject) (Subject, error) {
	if s.ID == "" {
		s.ID = uuid.NewString()
	}
	if err := validateSubjectLookup(s.Namespace, s.Owner, s.ID); err != nil {
		return Subject{}, err
	}
	if _, err := uuid.Parse(s.ThreadID); err != nil {
		return Subject{}, fmt.Errorf("%w: subject requires a target thread", ErrInvalid)
	}
	s.Label = strings.TrimSpace(s.Label)
	if s.Label == "" || utf8.RuneCountInString(s.Label) > 200 || !utf8.ValidString(s.Label) || len(s.Summary) > 4096 || !utf8.ValidString(s.Summary) {
		return Subject{}, fmt.Errorf("%w: invalid subject label or summary", ErrInvalid)
	}
	switch s.Kind {
	case "project", "session", "preference", "idea", "conversation":
	default:
		return Subject{}, fmt.Errorf("%w: invalid subject kind", ErrInvalid)
	}
	if s.Status == "" {
		s.Status = "open"
	}
	switch s.Status {
	case "open", "parked", "done":
	case "merged":
		if _, err := uuid.Parse(s.MergedInto); err != nil || s.MergedInto == s.ID {
			return Subject{}, fmt.Errorf("%w: invalid merged subject", ErrInvalid)
		}
	default:
		return Subject{}, fmt.Errorf("%w: invalid subject status", ErrInvalid)
	}
	if s.Status != "merged" && s.MergedInto != "" {
		return Subject{}, fmt.Errorf("%w: only merged subjects can redirect", ErrInvalid)
	}
	if len(s.Aliases) > 32 {
		return Subject{}, fmt.Errorf("%w: too many subject aliases", ErrInvalid)
	}
	aliases := []string{}
	for _, alias := range s.Aliases {
		alias = strings.TrimSpace(alias)
		if alias == "" || utf8.RuneCountInString(alias) > 200 || !utf8.ValidString(alias) {
			return Subject{}, fmt.Errorf("%w: invalid subject alias", ErrInvalid)
		}
		aliases = appendAlias(aliases, alias)
	}
	s.Aliases = aliases
	now := time.Now().UTC()
	if s.OpenedAt.IsZero() {
		s.OpenedAt = now
	}
	s.TouchedAt = now
	return s, nil
}
func normalizeRoutingRequest(r RoutingRequest) (RoutingRequest, error) {
	if err := validateSubjectLookup(r.Namespace, r.Owner, r.ID); err != nil {
		return RoutingRequest{}, err
	}
	if r.RequestHash == "" || len(r.RequestHash) > 128 || strings.TrimSpace(r.Content) == "" || len(r.Content) > maxMessageBytes || !utf8.ValidString(r.Content) {
		return RoutingRequest{}, fmt.Errorf("%w: invalid routing request", ErrInvalid)
	}
	switch r.Status {
	case "routing", "clarify", "accepted", "failed":
	default:
		return RoutingRequest{}, fmt.Errorf("%w: invalid routing status", ErrInvalid)
	}
	if len(r.Events) == 0 {
		r.Events = json.RawMessage(`[]`)
	}
	var events []json.RawMessage
	if len(r.Events) > 256*1024 || json.Unmarshal(r.Events, &events) != nil || events == nil {
		return RoutingRequest{}, fmt.Errorf("%w: routing events must be a bounded array", ErrInvalid)
	}
	// A permitted 64 KiB user message can expand sixfold when JSON escapes
	// HTML characters. Leave room for that content plus the bounded trace and
	// handoff metadata, so an accepted outbox can always persist its receipt.
	if len(r.Result) > 1024*1024 || (len(r.Result) > 0 && !json.Valid(r.Result)) {
		return RoutingRequest{}, fmt.Errorf("%w: invalid routing result", ErrInvalid)
	}
	now := time.Now().UTC()
	if r.CreatedAt.IsZero() {
		r.CreatedAt = now
	}
	r.UpdatedAt = now
	return r, nil
}
func updateRoutingRequest(current, next RoutingRequest) (RoutingRequest, error) {
	if current.RequestHash != next.RequestHash || current.Content != next.Content || current.ProfileName != next.ProfileName {
		return RoutingRequest{}, ErrRequestConflict
	}
	if current.Status == "accepted" {
		return current, nil
	}
	next.CreatedAt = current.CreatedAt
	return next, nil
}
func appendAlias(aliases []string, label string) []string {
	for _, alias := range aliases {
		if strings.EqualFold(alias, label) {
			return aliases
		}
	}
	if len(aliases) < 32 {
		return append(aliases, label)
	}
	return aliases
}
func mergeSubjectValues(source, target Subject) (Subject, Subject, error) {
	if source.ID == target.ID || source.Status == "merged" || target.Status == "merged" {
		return Subject{}, Subject{}, fmt.Errorf("%w: subjects must be distinct and not already merged", ErrInvalid)
	}
	target.Aliases = appendAlias(target.Aliases, source.Label)
	for _, alias := range source.Aliases {
		target.Aliases = appendAlias(target.Aliases, alias)
	}
	if source.Summary != "" && source.Summary != target.Summary {
		summary := strings.TrimSpace(target.Summary + "\nMerged subject: " + source.Summary)
		if len(summary) > 4096 {
			summary = summary[:4096]
			for !utf8.ValidString(summary) {
				summary = summary[:len(summary)-1]
			}
		}
		target.Summary = summary
	}
	now := time.Now().UTC()
	target.TouchedAt = now
	target.Status = "open"
	source.Status = "merged"
	source.MergedInto = target.ID
	source.TouchedAt = now
	return source, target, nil
}
func cloneSubject(s Subject) Subject { s.Aliases = append([]string{}, s.Aliases...); return s }
func cloneRoutingRequest(r RoutingRequest) RoutingRequest {
	r.Events = append(json.RawMessage{}, r.Events...)
	r.Result = append(json.RawMessage(nil), r.Result...)
	return r
}
