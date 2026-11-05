package host

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hazyforge/anvil-agents/internal/onechat"
)

type hostDecider struct {
	workdir string
	calls   atomic.Int32
}

func (d *hostDecider) Decide(_ context.Context, in onechat.DecideInput) (onechat.Decision, onechat.Usage, error) {
	d.calls.Add(1)
	return onechat.Decision{ReasoningSummary: "Start the requested local work.", Confidence: 1,
		Actions: []onechat.Action{{Type: "create", SetupID: "local", Title: "Protocol fixture", Workdir: d.workdir, Message: in.Message}}}, onechat.Usage{}, nil
}

func (*hostDecider) Compose(_ context.Context, in onechat.ComposeInput) (string, onechat.Usage, error) {
	return in.Outcomes[0].Reply, onechat.Usage{}, nil
}

type hostBackend struct {
	reply     string
	entered   chan struct{}
	release   chan struct{}
	cancelled chan struct{}
	once      sync.Once
}

func (*hostBackend) List(context.Context) ([]onechat.Session, error) { return nil, nil }

func (*hostBackend) Create(_ context.Context, setup onechat.Setup, title, workdir string) (onechat.Session, onechat.Receipt, error) {
	return onechat.Session{ID: uuid.NewString(), SetupID: setup.ID, Location: "local", Harness: setup.Harness,
		Title: title, Workdir: workdir, State: "idle", Transcript: []onechat.Message{}}, onechat.Receipt{}, nil
}

func (b *hostBackend) Send(ctx context.Context, _ onechat.Setup, _ onechat.Session, text string) (onechat.ActionResult, error) {
	b.once.Do(func() { close(b.entered) })
	select {
	case <-ctx.Done():
		close(b.cancelled)
		return onechat.ActionResult{SentText: text, State: "failed", HarnessCalled: true}, ctx.Err()
	case <-b.release:
		return onechat.ActionResult{SentText: text, Reply: b.reply, State: "idle", HarnessCalled: true}, nil
	}
}

type hostFixture struct {
	host    *Host
	manager *onechat.Manager
	decider *hostDecider
	backend *hostBackend
	dir     string
	token   string
	stop    func()
}

func newHostFixture(t *testing.T, blocked bool, reply string) *hostFixture {
	t.Helper()
	f := &hostFixture{dir: t.TempDir()}
	cfg := onechat.Config{StateDir: f.dir, Setups: []onechat.Setup{{ID: "local", Location: "local", Harness: "claudeCode"}}}
	cfg.Local.WorkdirRoots = []string{t.TempDir()}
	f.decider = &hostDecider{workdir: cfg.Local.WorkdirRoots[0]}
	f.backend = &hostBackend{reply: reply, entered: make(chan struct{}), release: make(chan struct{}), cancelled: make(chan struct{})}
	if !blocked {
		close(f.backend.release)
	}
	var err error
	f.manager, err = onechat.NewManager(cfg, f.decider, f.backend, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	f.host, err = Start(ctx, Options{Manager: f.manager, StateDir: f.dir, Listen: "127.0.0.1:0", Version: "test-build"})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	var once sync.Once
	f.stop = func() {
		once.Do(func() {
			cancel()
			done := make(chan error, 1)
			go func() { done <- f.host.Wait() }()
			select {
			case err := <-done:
				if err != nil {
					t.Errorf("stop host: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Error("host did not stop after cancellation")
			}
		})
	}
	t.Cleanup(f.stop)
	token, err := os.ReadFile(filepath.Join(f.dir, "host-token"))
	if err != nil {
		t.Fatal(err)
	}
	f.token = strings.TrimSpace(string(token))
	return f
}

type protocolFrame struct {
	Type   string             `json:"type"`
	ID     string             `json:"id"`
	OK     bool               `json:"ok"`
	Body   json.RawMessage    `json:"body"`
	Error  *testProtocolError `json:"error"`
	Event  string             `json:"event"`
	TurnID string             `json:"turnId"`
	Seq    int                `json:"seq"`
	At     time.Time          `json:"at"`
}

type testProtocolError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type protocolClient struct {
	conn   net.Conn
	reader *bufio.Reader
	nextID int
	seen   []protocolFrame
	// readTimeout bounds each frame read; tests that move megabytes raise it
	// because the race detector slows trimming by an order of magnitude.
	readTimeout time.Duration
}

func dialProtocol(t *testing.T, f *hostFixture) *protocolClient {
	t.Helper()
	conn, err := net.DialTimeout("tcp", f.host.Info().Listen, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	var key [16]byte
	if _, err := rand.Read(key[:]); err != nil {
		t.Fatal(err)
	}
	request := "GET /v1/connect HTTP/1.1\r\nHost: " + f.host.Info().Listen + "\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: " + base64.StdEncoding.EncodeToString(key[:]) + "\r\nSec-WebSocket-Protocol: anvil.onechat.v1\r\nAuthorization: Bearer " + f.token + "\r\n\r\n"
	if _, err := io.WriteString(conn, request); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusSwitchingProtocols || response.Header.Get("Sec-WebSocket-Protocol") != "anvil.onechat.v1" {
		t.Fatalf("upgrade: %s, protocol %q", response.Status, response.Header.Get("Sec-WebSocket-Protocol"))
	}
	_ = conn.SetDeadline(time.Time{})
	return &protocolClient{conn: conn, reader: reader, readTimeout: 3 * time.Second}
}

func (c *protocolClient) write(t *testing.T, payload []byte) {
	t.Helper()
	var mask [4]byte
	if _, err := rand.Read(mask[:]); err != nil {
		t.Fatal(err)
	}
	header := []byte{0x81}
	switch {
	case len(payload) < 126:
		header = append(header, 0x80|byte(len(payload)))
	case len(payload) <= 65535:
		header = append(header, 0x80|126, byte(len(payload)>>8), byte(len(payload)))
	default:
		header = append(header, 0x80|127)
		header = binary.BigEndian.AppendUint64(header, uint64(len(payload)))
	}
	header = append(header, mask[:]...)
	for i, value := range payload {
		header = append(header, value^mask[i%4])
	}
	_ = c.conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
	if _, err := c.conn.Write(header); err != nil {
		t.Fatal(err)
	}
}

func (c *protocolClient) read(t *testing.T) protocolFrame {
	t.Helper()
	_ = c.conn.SetReadDeadline(time.Now().Add(c.readTimeout))
	var header [2]byte
	if _, err := io.ReadFull(c.reader, header[:]); err != nil {
		t.Fatal(err)
	}
	if header[0] != 0x81 || header[1]&0x80 != 0 {
		t.Fatalf("expected unmasked JSON text frame, header %x", header)
	}
	length := uint64(header[1])
	if length == 126 {
		var b [2]byte
		if _, err := io.ReadFull(c.reader, b[:]); err != nil {
			t.Fatal(err)
		}
		length = uint64(binary.BigEndian.Uint16(b[:]))
	} else if length == 127 {
		var b [8]byte
		if _, err := io.ReadFull(c.reader, b[:]); err != nil {
			t.Fatal(err)
		}
		length = binary.BigEndian.Uint64(b[:])
	}
	if length > 1<<20 {
		t.Fatalf("protocol frame exceeds 1 MiB: %d", length)
	}
	payload := make([]byte, int(length))
	if _, err := io.ReadFull(c.reader, payload); err != nil {
		t.Fatal(err)
	}
	var frame protocolFrame
	if err := json.Unmarshal(payload, &frame); err != nil {
		t.Fatalf("invalid frame JSON: %v", err)
	}
	if frame.Type == "event" {
		c.seen = append(c.seen, frame)
	}
	return frame
}

func (c *protocolClient) request(t *testing.T, op string, body any) protocolFrame {
	t.Helper()
	c.nextID++
	id := fmt.Sprintf("request-%d", c.nextID)
	raw, err := json.Marshal(map[string]any{"type": "request", "id": id, "op": op, "body": body})
	if err != nil {
		t.Fatal(err)
	}
	c.write(t, raw)
	for {
		frame := c.read(t)
		if frame.Type == "response" {
			if frame.ID != id {
				t.Fatalf("response id %q, want %q", frame.ID, id)
			}
			return frame
		}
	}
}

func bodyAs[T any](t *testing.T, frame protocolFrame) T {
	t.Helper()
	if !frame.OK && frame.Type == "response" {
		t.Fatalf("request failed: %+v", frame.Error)
	}
	var body T
	if err := json.Unmarshal(frame.Body, &body); err != nil {
		t.Fatal(err)
	}
	return body
}

func requireCode(t *testing.T, frame protocolFrame, code string) {
	t.Helper()
	if frame.OK || frame.Error == nil || frame.Error.Code != code || frame.Error.Message == "" {
		t.Fatalf("response = %+v, want %s error", frame, code)
	}
}

func (c *protocolClient) hello(t *testing.T) protocolFrame {
	t.Helper()
	return c.request(t, "hello", map[string]any{"client": map[string]string{"name": "host test", "kind": "test"}})
}

func (c *protocolClient) waitIdle(t *testing.T) {
	t.Helper()
	for {
		for _, frame := range c.seen {
			if frame.Event == "status" && bodyAs[struct{ State string }](t, frame).State == "idle" {
				return
			}
		}
		c.read(t)
	}
}

func waitEntered(t *testing.T, f *hostFixture) {
	t.Helper()
	select {
	case <-f.backend.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("turn did not reach backend")
	}
}

func TestHostFilesAndSingleOwner(t *testing.T) {
	f := newHostFixture(t, false, "done")
	first := f.host.Info()
	if _, err := uuid.Parse(first.HostID); err != nil {
		t.Fatalf("host id: %v", err)
	}
	if first.PID != os.Getpid() || first.Version != "test-build" || first.StartedAt.IsZero() {
		t.Fatalf("metadata = %+v", first)
	}
	for _, filename := range []string{"host-token", "host.json"} {
		info, err := os.Stat(filepath.Join(f.dir, filename))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("%s permissions: info=%v err=%v", filename, info, err)
		}
	}
	decoded, err := base64.RawURLEncoding.DecodeString(f.token)
	if err != nil || len(decoded) != 32 {
		t.Fatal("host token must encode 32 random bytes")
	}
	for _, listen := range []string{"0.0.0.0:0", "192.0.2.1:1740", "[::]:0", "localhost:0"} {
		if _, err := Start(t.Context(), Options{Manager: f.manager, StateDir: t.TempDir(), Listen: listen}); err == nil || !strings.Contains(err.Error(), "loopback") {
			t.Fatalf("address %q: want loopback rejection, got %v", listen, err)
		}
	}
	if _, err := Start(t.Context(), Options{Manager: f.manager, StateDir: f.dir, Listen: "127.0.0.1:0"}); err == nil {
		t.Fatal("accepted a second live host for the same state directory")
	}
	if lock, err := LockState(f.dir); err == nil {
		_ = lock.Close()
		t.Fatal("public state lock succeeded while the host was live")
	}
	f.stop()
	lock, err := LockState(f.dir)
	if err != nil {
		t.Fatalf("public state lock did not become available after host shutdown: %v", err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	restarted, err := Start(ctx, Options{Manager: f.manager, StateDir: f.dir, Listen: "127.0.0.1:0", Version: "new-build"})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	defer func() { cancel(); _ = restarted.Wait() }()
	secondToken, err := os.ReadFile(filepath.Join(f.dir, "host-token"))
	if err != nil || strings.TrimSpace(string(secondToken)) != f.token || restarted.Info().HostID != first.HostID {
		t.Fatal("restart changed the private token or host identity")
	}
}

func TestHostHTTPGuards(t *testing.T) {
	f := newHostFixture(t, false, "done")
	client := &http.Client{Timeout: time.Second}
	cases := []struct {
		name, path, token, origin, host, protocol string
		want                                      int
	}{
		{name: "health", path: "/healthz", want: 200},
		{name: "unknown", path: "/unknown", want: 404},
		{name: "missing token", path: "/v1/connect", want: 401},
		{name: "wrong token", path: "/v1/connect", token: "wrong", want: 401},
		{name: "browser before auth", path: "/v1/connect", origin: "http://localhost", want: 403},
		{name: "browser with token", path: "/v1/connect", token: f.token, origin: "http://" + f.host.Info().Listen, want: 403},
		{name: "rebinding before auth", path: "/v1/connect", host: "attacker.invalid:1740", want: 403},
		{name: "wrong local port", path: "/v1/connect", token: f.token, host: "127.0.0.1:1", want: 403},
		{name: "missing protocol", path: "/v1/connect", token: f.token, want: 400},
		{name: "wrong protocol", path: "/v1/connect", token: f.token, protocol: "wrong", want: 400},
		{name: "query token", path: "/v1/connect?access_token=forbidden", token: f.token, protocol: "anvil.onechat.v1", want: 401},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, "http://"+f.host.Info().Listen+tc.path, nil)
			if err != nil {
				t.Fatal(err)
			}
			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.host != "" {
				req.Host = tc.host
			}
			req.Header.Set("Sec-WebSocket-Protocol", tc.protocol)
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tc.want {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.want)
			}
			if tc.path == "/healthz" {
				var body struct {
					OK     bool
					HostID string
				}
				if err := json.NewDecoder(resp.Body).Decode(&body); err != nil || !body.OK || body.HostID != f.host.Info().HostID {
					t.Fatalf("health response: %+v %v", body, err)
				}
			}
		})
	}
}

func TestConcurrentStartsHaveOneStateDirectoryOwner(t *testing.T) {
	cfg := onechat.Config{StateDir: t.TempDir()}
	cfg.Local.WorkdirRoots = []string{t.TempDir()}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type result struct {
		host *Host
		err  error
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		manager, err := onechat.NewManager(cfg, &hostDecider{workdir: cfg.Local.WorkdirRoots[0]}, &hostBackend{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		go func() {
			<-start
			h, err := Start(ctx, Options{Manager: manager, StateDir: cfg.StateDir, Listen: "127.0.0.1:0"})
			results <- result{h, err}
		}()
	}
	close(start)
	var winners []*Host
	t.Cleanup(func() {
		cancel()
		for _, h := range winners {
			if err := h.Wait(); err != nil {
				t.Errorf("stop contender: %v", err)
			}
		}
	})
	for i := 0; i < 2; i++ {
		select {
		case r := <-results:
			if r.err == nil {
				winners = append(winners, r.host)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("simultaneous startup did not settle")
		}
	}
	if len(winners) != 1 {
		t.Fatalf("simultaneous starts produced %d owners, want one", len(winners))
	}
	raw, err := os.ReadFile(filepath.Join(cfg.StateDir, "host.json"))
	if err != nil {
		t.Fatal(err)
	}
	var metadata Metadata
	if err := json.Unmarshal(raw, &metadata); err != nil || metadata.Listen != winners[0].Info().Listen || metadata.HostID != winners[0].Info().HostID {
		t.Fatalf("discovery metadata does not identify the winning listener: %+v %v", metadata, err)
	}
}

func TestProtocolHelloAndValidation(t *testing.T) {
	f := newHostFixture(t, false, "done")
	c := dialProtocol(t, f)
	requireCode(t, c.request(t, "status", map[string]any{}), "invalid")
	hello := bodyAs[struct {
		Protocol int
		Host     struct{ ID, Name, Version string }
		Status   struct {
			State string
			Turn  any
		}
	}](t, c.hello(t))
	if hello.Protocol != 1 || hello.Host.ID != f.host.Info().HostID || hello.Host.Name == "" || hello.Host.Version != "test-build" || hello.Status.State != "idle" || hello.Status.Turn != nil {
		t.Fatalf("hello = %+v", hello)
	}
	for _, tc := range []struct {
		op, code string
		body     any
	}{
		{"unknown", "invalid", map[string]any{}},
		{"turn.start", "invalid", map[string]any{"text": " \n", "requestId": uuid.NewString()}},
		{"turn.start", "invalid", map[string]any{"text": "work", "requestId": "not-a-uuid"}},
		{"turn.start", "too_large", map[string]any{"text": strings.Repeat("x", 65537), "requestId": uuid.NewString()}},
		{"history.list", "invalid", map[string]any{"limit": 201}},
		{"history.list", "invalid", map[string]any{"limit": -1}},
		{"trace.get", "invalid", map[string]any{"turnId": "../../onechat"}},
		{"trace.get", "not_found", map[string]any{"turnId": uuid.NewString()}},
		{"turn.cancel", "not_found", map[string]any{"turnId": uuid.NewString()}},
		{"session.get", "not_found", map[string]any{"sessionId": "unknown"}},
	} {
		requireCode(t, c.request(t, tc.op, tc.body), tc.code)
	}
	c.write(t, []byte(`{"type":"response","id":"bad-envelope","op":"status","body":{}}`))
	malformed := c.read(t)
	if malformed.ID != "bad-envelope" {
		t.Fatalf("malformed request response id = %q", malformed.ID)
	}
	requireCode(t, malformed, "invalid")
	c.write(t, []byte(`this is not json`))
	status := bodyAs[struct{ State string }](t, c.request(t, "status", map[string]any{}))
	if status.State != "idle" || f.decider.calls.Load() != 0 {
		t.Fatalf("invalid frames started work: status=%+v calls=%d", status, f.decider.calls.Load())
	}
}

func TestFullClientQueueDisconnectsWithoutStallingTurn(t *testing.T) {
	f := newHostFixture(t, true, "turn completed despite the slow client")
	slow := dialProtocol(t, f)
	bodyAs[map[string]any](t, slow.hello(t))
	if err := slow.conn.(*net.TCPConn).SetReadBuffer(1024); err != nil {
		t.Fatal(err)
	}
	f.host.mu.Lock()
	var peer *client
	for c := range f.host.clients {
		peer = c
	}
	f.host.mu.Unlock()
	if peer == nil {
		t.Fatal("upgraded client was not registered")
	}
	fast := dialProtocol(t, f)
	bodyAs[map[string]any](t, fast.hello(t))
	bodyAs[map[string]any](t, fast.request(t, "turn.start", map[string]string{"text": "complete while a client is stalled", "requestId": uuid.NewString()}))
	waitEntered(t, f)
	// Fill the real connection's queue directly: this bounds the fixture without
	// requiring hundreds of expensive turns to outrun the socket's send buffer.
	payload := []byte(`{"type":"event","event":"queue-fixture","body":{"reply":"` + strings.Repeat("x", 1<<19) + `"}}`)
	start := time.Now()
	for i := 0; i < queueSize+32; i++ {
		peer.enqueue(payload)
	}
	select {
	case <-peer.done:
	case <-time.After(time.Second):
		t.Fatal("full queue did not disconnect its client")
	}
	if time.Since(start) > time.Second {
		t.Fatal("queue overflow waited for the stalled socket writer")
	}
	close(f.backend.release)
	fast.waitIdle(t)
	// A closed TCP peer may still have bytes buffered in the kernel. Restore
	// the receive window before draining those bytes to observe the close.
	if err := slow.conn.(*net.TCPConn).SetReadBuffer(4 << 20); err != nil {
		t.Fatal(err)
	}
	_ = slow.conn.SetReadDeadline(time.Now().Add(time.Second))
	_, err := io.Copy(io.Discard, slow.conn)
	if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		t.Fatal("slow client's transport stayed open after its queue filled")
	}
}

func TestTransportFramesPreserveStructure(t *testing.T) {
	turnID, sessionID := uuid.NewString(), uuid.NewString()
	long := strings.Repeat("reply <>& 漢字\n", 200000)
	trace := onechat.TurnTrace{
		Version: "onechat.turn/v1", ID: turnID, At: time.Now().UTC(), Message: "read this trace",
		Inventory:   []onechat.Session{{ID: sessionID, Transcript: []onechat.Message{{Role: "assistant", Text: long}}}},
		RawDecision: map[string]any{strings.Repeat("large model-generated key", 60000): long},
		Actions:     []onechat.ActionResult{{SessionID: sessionID, Reply: long, SentText: long}},
		Answer:      long,
	}
	raw := frame(map[string]any{"type": "response", "id": "trace-read", "ok": true, "body": map[string]any{"trace": trace}})
	if len(raw) == 0 || len(raw) > 1<<20 {
		t.Fatalf("trimmed response length = %d", len(raw))
	}
	var response struct {
		Type, ID string
		OK       bool
		Body     struct {
			Truncated bool
			Trace     json.RawMessage
		}
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err)
	}
	if response.Type != "response" || response.ID != "trace-read" || !response.OK || !response.Body.Truncated {
		t.Fatalf("trimming changed the response envelope: %+v", response)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Trace, &fields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"version", "id", "at", "message", "inventory", "history", "setups", "rawDecision", "decision", "validationErrors", "deciderUsage", "composeUsage", "actions", "answer", "totals", "error", "tracePath"} {
		if _, found := fields[key]; !found {
			t.Errorf("trimming removed trace field %q", key)
		}
	}
	var trimmed onechat.TurnTrace
	if err := json.Unmarshal(response.Body.Trace, &trimmed); err != nil {
		t.Fatal(err)
	}
	if trimmed.ID != turnID || trimmed.Version != "onechat.turn/v1" || len(trimmed.Actions) != 1 || trimmed.Actions[0].SessionID != sessionID {
		t.Fatal("trimming changed trace or action identity")
	}
	if len(trimmed.Answer) == 0 || len(trimmed.Answer) >= len(long) || len(trimmed.Actions[0].Reply) == 0 || len(trimmed.Actions[0].Reply) >= len(long) {
		t.Fatal("oversized answer or reply was not shortened")
	}
	if len(trimmed.Inventory) != 1 || trimmed.Inventory[0].ID != sessionID || len(trimmed.Inventory[0].Transcript) != 1 || len(trimmed.Inventory[0].Transcript[0].Text) >= len(long) {
		t.Fatal("trimming lost session identity or failed to shorten its transcript")
	}
	if trace.Answer != long || trace.Actions[0].Reply != long || trace.Inventory[0].Transcript[0].Text != long {
		t.Fatal("transport trimming mutated the original trace")
	}
}

func TestOwnedDirectoryDoesNotOpenAnotherManager(t *testing.T) {
	dir := t.TempDir()
	lock, err := LockState(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	opened := false
	_, err = Start(t.Context(), Options{StateDir: dir, Listen: "127.0.0.1:0", OpenManager: func(context.Context) (*onechat.Manager, error) {
		opened = true
		return nil, fmt.Errorf("unexpected manager open")
	}})
	if err == nil || opened || !strings.Contains(err.Error(), "already serves") {
		t.Fatalf("occupied state directory opened another manager: opened=%t err=%v", opened, err)
	}
}
