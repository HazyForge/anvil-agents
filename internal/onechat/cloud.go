package onechat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/hazyforge/anvil-agents/internal/chat"
	"github.com/hazyforge/anvil-agents/internal/runapi"
)

type CloudBackend struct {
	tokenMu       sync.RWMutex
	refreshMu     sync.Mutex
	Config        CloudConfig
	Client        *http.Client
	Timeout       time.Duration
	PollInterval  time.Duration
	TokenSource   func(context.Context) (string, error)
	token         string
	secrets       []string
	sessionTokens *fileTokens
}

// NewCloudBackend acquires the initial credential. Commands refresh on 401;
// signed-in sessions also refresh proactively before expiry.
func NewCloudBackend(ctx context.Context, config CloudConfig, timeout time.Duration, client *http.Client, source func(context.Context) (string, error)) (*CloudBackend, error) {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	copyClient := *client
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	b := &CloudBackend{Config: config, Client: &copyClient, Timeout: timeout, PollInterval: time.Second, TokenSource: source}
	if b.Timeout <= 0 {
		b.Timeout = defaultTimeout
	}
	if b.TokenSource == nil && len(config.TokenCommand) == 0 {
		var err error
		b.sessionTokens, err = newFileTokens(config.SessionFile, &copyClient)
		if err != nil {
			return nil, err
		}
	} else if b.TokenSource == nil {
		b.TokenSource = func(ctx context.Context) (string, error) {
			if len(config.TokenCommand) == 0 {
				return "", fmt.Errorf("missing cloud token command")
			}
			// This trusted authentication helper may need KUBECONFIG or OIDC
			// environment configuration. It is never a model-facing child.
			output, _, err := runCommandEnv(ctx, "", config.TokenCommand[0], config.TokenCommand[1:], "", 30*time.Second, os.Environ())
			if err != nil {
				return "", fmt.Errorf("cloud token command failed")
			}
			return strings.TrimSpace(output), nil
		}
	}
	if err := b.refresh(ctx, false); err != nil {
		return nil, err
	}
	return b, nil
}

func (b *CloudBackend) refresh(ctx context.Context, force bool) error {
	b.refreshMu.Lock()
	defer b.refreshMu.Unlock()
	var token string
	var err error
	if b.sessionTokens != nil {
		token, err = b.sessionTokens.Token(ctx, force)
	} else {
		token, err = b.TokenSource(ctx)
	}
	if err != nil {
		if b.sessionTokens != nil {
			return err
		}
		return fmt.Errorf("cloud token refresh failed")
	}
	token = strings.TrimSpace(token)
	if token == "" || strings.ContainsAny(token, "\r\n\t ") {
		return fmt.Errorf("cloud token command must return one nonempty bearer token")
	}
	b.tokenMu.Lock()
	defer b.tokenMu.Unlock()
	if token != b.token {
		b.secrets = append(b.secrets, token)
	}
	b.token = token
	return nil
}

// Redact also retains retired tokens after refresh, so an earlier outcome cannot
// leak an old bearer when the complete turn is written later.
func (b *CloudBackend) Redact(s string) string {
	if b.sessionTokens != nil {
		s = b.sessionTokens.Redact(s)
	}
	b.tokenMu.RLock()
	defer b.tokenMu.RUnlock()
	for _, token := range b.secrets {
		s = strings.ReplaceAll(s, token, "[REDACTED]")
	}
	return s
}

func (b *CloudBackend) request(ctx context.Context, method, path string, body, dest any) (int, error) {
	if b.sessionTokens != nil {
		if err := b.refresh(ctx, false); err != nil {
			return 0, err
		}
	}
	var data []byte
	var err error
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return 0, err
		}
	}
	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, b.Config.BaseURL+path, bytes.NewReader(data))
		if err != nil {
			return 0, fmt.Errorf("invalid cloud request URL")
		}
		b.tokenMu.RLock()
		token := b.token
		b.tokenMu.RUnlock()
		req.Header.Set("Authorization", "Bearer "+token)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := b.Client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return 0, ctx.Err()
			}
			return 0, fmt.Errorf("cloud transport failed; delivery may be unconfirmed")
		}
		raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 8*outputLimit+1))
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			if err = b.refresh(ctx, true); err != nil {
				return resp.StatusCode, err
			}
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			// The API's own error code and message say why (for example a busy
			// agent), which the manager and the person need to decide what next.
			var apiErr struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if json.Unmarshal(raw, &apiErr) == nil && apiErr.Error.Code != "" {
				return resp.StatusCode, fmt.Errorf("cloud API returned HTTP %d %s: %s", resp.StatusCode, apiErr.Error.Code, truncate(apiErr.Error.Message, 300))
			}
			return resp.StatusCode, fmt.Errorf("cloud API returned HTTP %d", resp.StatusCode)
		}
		if readErr != nil || len(raw) > 8*outputLimit {
			return resp.StatusCode, fmt.Errorf("cloud response unreadable or exceeds limit")
		}
		// Decode first, then redact individual strings through JSON roundtrip at
		// the manager boundary; never store raw responses or bearer headers.
		if dest != nil && json.Unmarshal(raw, dest) != nil {
			return resp.StatusCode, fmt.Errorf("invalid cloud JSON response")
		}
		return resp.StatusCode, nil
	}
	return http.StatusUnauthorized, fmt.Errorf("cloud authorization failed after refresh")
}

func threadPath(ns, id string) string {
	path := "/api/v1/namespaces/" + url.PathEscape(ns) + "/chat/threads"
	if id != "" {
		path += "/" + url.PathEscape(id)
	}
	return path
}

func cloudID(base, ns, id string) string { return base + "/" + ns + "/" + id }

func (b *CloudBackend) session(t chat.Thread) Session {
	return Session{ID: cloudID(b.Config.BaseURL, t.Namespace, t.ID), Location: "cloud", Title: t.Title, State: "idle", CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt, BaseURL: b.Config.BaseURL, Namespace: t.Namespace, ThreadID: t.ID, Profile: t.ProfileName, Transcript: []Message{}}
}

func (b *CloudBackend) Read(ctx context.Context, s Session) (Session, error) {
	var detail runapi.ChatThreadDetailResponse
	_, err := b.request(ctx, http.MethodGet, threadPath(s.Namespace, s.ThreadID), nil, &detail)
	if err != nil {
		return s, err
	}
	return b.detailSession(detail), nil
}

func (b *CloudBackend) detailSession(d runapi.ChatThreadDetailResponse) Session {
	s := b.session(d.Thread)
	for _, msg := range d.Messages {
		s.Transcript = append(s.Transcript, Message{Role: msg.Role, Text: b.Redact(msg.Content), At: msg.CreatedAt})
	}
	for _, turn := range d.Turns {
		if turn.Status == "failed" {
			s.State = "failed"
		} else if turn.Status == "succeeded" {
			s.State = "idle"
		}
	}
	for _, turn := range d.Turns {
		if chat.Active(turn) {
			s.State = "running"
		}
	}
	if d.ActiveTurn != nil || d.RecoveryPending {
		s.State = "running"
	}
	if len(s.Transcript) > 0 {
		s.Summary = truncate(s.Transcript[len(s.Transcript)-1].Text, 600)
	}
	return s
}

func (b *CloudBackend) List(ctx context.Context) ([]Session, error) {
	var sessions []Session
	for _, ns := range b.Config.Namespaces {
		var list runapi.ChatThreadListResponse
		_, err := b.request(ctx, http.MethodGet, threadPath(ns, "")+"?limit=200", nil, &list)
		if err != nil {
			return nil, err
		}
		// The current API has no pagination. Refuse a possibly incomplete
		// inventory instead of silently hiding sessions from the manager.
		if len(list.Items) >= 200 {
			return nil, fmt.Errorf("cloud thread discovery reached the API's 200-thread limit; narrow configured namespaces")
		}
		for _, thread := range list.Items {
			thread.Namespace = ns
			sessions = append(sessions, b.session(thread))
		}
	}
	return sessions, nil
}

func (b *CloudBackend) Create(ctx context.Context, setup Setup, title, _ string) (Session, Receipt, error) {
	var thread chat.Thread
	code, err := b.request(ctx, http.MethodPost, threadPath(setup.Namespace, ""), map[string]any{"profileName": setup.Profile, "mode": "persona", "title": title}, &thread)
	if err != nil {
		return Session{}, Receipt{}, err
	}
	if thread.ID == "" || thread.ProfileName != setup.Profile || thread.Namespace != setup.Namespace {
		return Session{}, Receipt{}, fmt.Errorf("cloud create returned a mismatched thread")
	}
	s := b.session(thread)
	s.SetupID, s.Harness = setup.ID, setup.Harness
	created := code == http.StatusCreated
	r := Receipt{Cloud: &CloudReceipt{BaseURL: s.BaseURL, Namespace: s.Namespace, ThreadID: s.ThreadID, MessageIDs: []string{}, Created: &created}}
	if !created {
		return Session{}, r, fmt.Errorf("cloud persona create returned HTTP %d; expected 201 for a new thread", code)
	}
	return s, r, nil
}

func (b *CloudBackend) Send(ctx context.Context, setup Setup, session Session, text string) (result ActionResult, err error) {
	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, b.Timeout)
	defer cancel()
	r := &CloudReceipt{BaseURL: b.Config.BaseURL, Namespace: session.Namespace, ThreadID: session.ThreadID, RequestID: uuid.NewString(), MessageIDs: []string{}}
	result = ActionResult{SentText: text, SessionID: session.ID, State: "failed", Harness: setup.Harness, Receipt: Receipt{Cloud: r}}
	defer func() {
		result.DurationMS = time.Since(start).Milliseconds()
		result.Usage.DurationMS = result.DurationMS
		if err != nil {
			result.Error = b.Redact(err.Error())
		}
	}()
	var appended runapi.ChatAppendResponse
	_, err = b.request(ctx, http.MethodPost, threadPath(session.Namespace, session.ThreadID)+"/messages", map[string]string{"requestId": r.RequestID, "content": text}, &appended)
	if err != nil {
		return result, err
	}
	r.TurnID, r.RunName = appended.Turn.ID, appended.Turn.RunName
	if appended.User.ID != "" {
		r.MessageIDs = append(r.MessageIDs, appended.User.ID)
	}
	result.HarnessCalled = true
	result.State = "running"
	if r.TurnID == "" {
		return result, fmt.Errorf("cloud append returned no turn receipt; completion is unknown")
	}
	for {
		var d runapi.ChatThreadDetailResponse
		_, err = b.request(ctx, http.MethodGet, threadPath(session.Namespace, session.ThreadID), nil, &d)
		if err != nil {
			return result, err
		}
		// Include failure/system message receipts as well as assistant replies.
		// Polling may return the same messages repeatedly, so keep IDs unique.
		for _, msg := range d.Messages {
			var meta struct {
				TurnID string `json:"turnId"`
			}
			if msg.ID == "" || json.Unmarshal(msg.Metadata, &meta) != nil || meta.TurnID != r.TurnID {
				continue
			}
			found := false
			for _, id := range r.MessageIDs {
				if id == msg.ID {
					found = true
					break
				}
			}
			if !found {
				r.MessageIDs = append(r.MessageIDs, msg.ID)
			}
		}
		for _, turn := range d.Turns {
			if turn.ID != r.TurnID {
				continue
			}
			if turn.RunName != "" {
				r.RunName = turn.RunName
			}
			if turn.Status == "failed" || turn.Status == "cancelled" || turn.Status == "canceled" {
				result.State = "failed"
				return result, fmt.Errorf("cloud turn %s: %s", turn.Status, b.Redact(turn.Error))
			}
		}
		for _, msg := range d.Messages {
			var meta struct {
				TurnID  string          `json:"turnId"`
				RunName string          `json:"runName"`
				Usage   json.RawMessage `json:"usage"`
			}
			if msg.Role != "assistant" || json.Unmarshal(msg.Metadata, &meta) != nil || meta.TurnID != r.TurnID {
				continue
			}
			if strings.TrimSpace(msg.Content) == "" {
				continue
			}
			if meta.RunName != "" {
				r.RunName = meta.RunName
			}
			result.Reply, result.State = b.Redact(msg.Content), "idle"
			result.Usage = parseUsage(meta.Usage)
			return result, nil
		}
		interval := b.PollInterval
		if interval <= 0 {
			interval = time.Second
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return result, ctx.Err()
		case <-timer.C:
		}
	}
}
