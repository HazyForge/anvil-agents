package desktop

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hazyforge/anvil-agents/internal/standing"
)

func newOnechatDesktop(t *testing.T, stateDir string) *Server {
	t.Helper()
	s, err := NewServer(Options{Listen: "127.0.0.1:0", ConfigDir: t.TempDir(), OnechatStateDir: stateDir})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func writeOnechatFiles(t *testing.T, dir, listen string) string {
	t.Helper()
	raw, err := json.Marshal(onechatHost{Listen: listen, HostID: "test-host", Version: "test-version"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "host.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		t.Fatal(err)
	}
	token := base64.RawURLEncoding.EncodeToString(secret)
	if err := os.WriteFile(filepath.Join(dir, "host-token"), []byte(token), 0600); err != nil {
		t.Fatal(err)
	}
	return token
}

func TestOnechatStatusAndRefusals(t *testing.T) {
	dir := t.TempDir()
	s := newOnechatDesktop(t, dir)
	live := httptest.NewServer(s.Handler())
	defer live.Close()
	get := func(path string) (int, string) {
		t.Helper()
		resp, err := http.Get(live.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode, string(raw)
	}
	code, body := get("/local/v1/onechat/status")
	if code != 200 || !strings.Contains(body, `"configured":false`) || !strings.Contains(body, "onechat serve") {
		t.Fatalf("missing host: %d %s", code, body)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" || r.Header.Get("Authorization") != "" {
			t.Errorf("unexpected probe %s, authorization present=%t", r.URL.Path, r.Header.Get("Authorization") != "")
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "hostId": "test-host"})
	}))
	defer upstream.Close()
	token := writeOnechatFiles(t, dir, strings.TrimPrefix(upstream.URL, "http://"))
	code, body = get("/local/v1/onechat/status")
	if code != 200 || !strings.Contains(body, `"running":true`) || !strings.Contains(body, `"hostId":"test-host"`) || strings.Contains(body, token) {
		t.Fatalf("live status: %d (body withheld if credential leaked)", code)
	}
	if err := os.Chmod(filepath.Join(dir, "host-token"), 0644); err != nil {
		t.Fatal(err)
	}
	code, body = get("/local/v1/onechat/connect")
	if code != 502 || !strings.Contains(body, "0600") {
		t.Fatalf("unsafe token: %d %s", code, body)
	}
	writeOnechatFiles(t, dir, "192.0.2.1:1740")
	code, body = get("/local/v1/onechat/connect")
	if code != 502 || !strings.Contains(body, "loopback") {
		t.Fatalf("non-loopback host: %d %s", code, body)
	}
	if err := os.Chmod(filepath.Join(dir, "host-token"), 0600); err != nil {
		t.Fatal(err)
	}
	writeOnechatFiles(t, dir, strings.TrimPrefix(upstream.URL, "http://"))
	upstream.Close()
	code, body = get("/local/v1/onechat/connect")
	if code != 502 || !strings.Contains(body, "unreachable") {
		t.Fatalf("stopped host: %d %s", code, body)
	}
	code, body = get("/local/v1/onechat/status")
	if code != 200 || !strings.Contains(body, `"running":false`) || !strings.Contains(body, `"configured":true`) {
		t.Fatalf("stopped status: %d %s", code, body)
	}
}

func TestOnechatRejectsStaleDiscoveryBeforeCredentials(t *testing.T) {
	for _, tc := range []struct {
		name, health, message string
		status                int
		stopped               bool
	}{
		{"stopped", `{"ok":true,"hostId":"test-host"}`, "stopped", 200, true},
		{"another host", `{"ok":true,"hostId":"another-host"}`, "identity", 200, false},
		{"unhealthy", `{"ok":false,"hostId":"test-host"}`, "identity", 200, false},
		{"missing identity", `{"ok":true}`, "identity", 200, false},
		{"invalid health", `not JSON`, "identity", 200, false},
		{"HTTP failure", `{"ok":true,"hostId":"test-host"}`, "identity", 503, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var probes, connects atomic.Int32
			var authorized atomic.Bool
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "" {
					authorized.Store(true)
				}
				if r.URL.Path == "/healthz" {
					probes.Add(1)
				} else {
					connects.Add(1)
				}
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.health)
			}))
			defer upstream.Close()
			dir := t.TempDir()
			listen := strings.TrimPrefix(upstream.URL, "http://")
			writeOnechatFiles(t, dir, listen)
			if tc.stopped {
				stopped := time.Now().UTC()
				raw, err := json.Marshal(map[string]any{"listen": listen, "hostId": "test-host", "stoppedAt": stopped})
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "host.json"), raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			s := newOnechatDesktop(t, dir)
			live := httptest.NewServer(s.Handler())
			defer live.Close()
			for _, op := range []string{"status", "connect"} {
				request, err := http.NewRequest(http.MethodGet, live.URL+"/local/v1/onechat/"+op, nil)
				if err != nil {
					t.Fatal(err)
				}
				request.Header.Set("Authorization", "Bearer browser-credential-must-stay-local")
				response, err := http.DefaultClient.Do(request)
				if err != nil {
					t.Fatal(err)
				}
				raw, err := io.ReadAll(response.Body)
				response.Body.Close()
				if err != nil {
					t.Fatal(err)
				}
				if op == "status" {
					var status onechatStatus
					if response.StatusCode != 200 || json.Unmarshal(raw, &status) != nil || !status.Configured || status.Running || !strings.Contains(status.Message, tc.message) {
						t.Errorf("stale host status = %d %s", response.StatusCode, raw)
					}
				} else if response.StatusCode != 502 || !strings.Contains(string(raw), `"error":"onechat_unavailable"`) || !strings.Contains(string(raw), tc.message) {
					t.Errorf("stale host connect = %d %s", response.StatusCode, raw)
				}
			}
			if authorized.Load() || connects.Load() != 0 {
				t.Errorf("stale listener received credentials=%t connect requests=%d", authorized.Load(), connects.Load())
			}
			if tc.stopped && probes.Load() != 0 {
				t.Errorf("stopped discovery was probed %d times", probes.Load())
			}
			if !tc.stopped && probes.Load() != 2 {
				t.Errorf("status and connect must each verify host identity; probes=%d", probes.Load())
			}
		})
	}
}

func TestOnechatProxyWebSocketPastDeadlinesAndRestart(t *testing.T) {
	dir := t.TempDir()
	s := newOnechatDesktop(t, dir)
	live := httptest.NewUnstartedServer(s.Handler())
	live.Config.ReadTimeout = 100 * time.Millisecond
	live.Config.WriteTimeout = 100 * time.Millisecond
	live.Start()
	defer live.Close()
	for _, name := range []string{"first", "restarted"} {
		t.Run(name, func(t *testing.T) {
			seen := make(chan *http.Request, 1)
			upstreamErrors := make(chan error, 1)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/healthz" {
					if r.Header.Get("Authorization") != "" {
						t.Error("health probe received credentials")
					}
					writeJSON(w, http.StatusOK, map[string]any{"ok": true, "hostId": "test-host"})
					return
				}
				seen <- r.Clone(context.Background())
				conn, err := standing.Upgrade(w, r, "anvil.onechat.v1")
				if err != nil {
					upstreamErrors <- err
					return
				}
				defer conn.Close()
				_, raw, err := conn.ReadMessage()
				if err == nil {
					err = conn.WriteText(append([]byte(name+":"), raw...))
				}
				upstreamErrors <- err
			}))
			defer upstream.Close()
			listen := strings.TrimPrefix(upstream.URL, "http://")
			token := writeOnechatFiles(t, dir, listen)
			conn, reader := dialDesktopOnechat(t, live.URL)
			defer conn.Close()
			request := <-seen
			if request.Host != listen || request.URL.Path != "/v1/connect" || request.Header.Get("Authorization") != "Bearer "+token || request.Header.Get("Sec-WebSocket-Protocol") != "anvil.onechat.v1" {
				t.Fatal("upstream did not receive rewritten host, path, token and protocol")
			}
			for _, header := range []string{"Origin", "Referer", "Cookie", "Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto"} {
				if request.Header.Get(header) != "" {
					t.Errorf("upstream received %s", header)
				}
			}
			// Both a late client frame and its upstream reply must survive.
			time.Sleep(250 * time.Millisecond)
			payload := []byte("hello")
			frame := []byte{0x81, 0x80 | byte(len(payload)), 1, 2, 3, 4}
			for i, b := range payload {
				frame = append(frame, b^byte(i%4+1))
			}
			if _, err := conn.Write(frame); err != nil {
				t.Fatal(err)
			}
			header := make([]byte, 2)
			if _, err := io.ReadFull(reader, header); err != nil {
				t.Fatal(err)
			}
			if header[0] != 0x81 || header[1] > 125 {
				t.Fatalf("unexpected response frame %v", header)
			}
			raw := make([]byte, header[1])
			if _, err := io.ReadFull(reader, raw); err != nil {
				t.Fatal(err)
			}
			if string(raw) != name+":hello" {
				t.Fatalf("echo = %q", raw)
			}
			if err := <-upstreamErrors; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func dialDesktopOnechat(t *testing.T, origin string) (net.Conn, *bufio.Reader) {
	t.Helper()
	addr := strings.TrimPrefix(origin, "http://")
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	_, err = fmt.Fprintf(conn, "GET /local/v1/onechat/connect HTTP/1.1\r\nHost: %s\r\nOrigin: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: MDEyMzQ1Njc4OWFiY2RlZg==\r\nSec-WebSocket-Protocol: anvil.onechat.v1\r\nCookie: unrelated=cookie\r\nReferer: %s/manager\r\nForwarded: host=attacker.invalid\r\nX-Forwarded-For: attacker.invalid\r\n\r\n", addr, origin, origin)
	if err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, &http.Request{Method: http.MethodGet})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols || resp.Header.Get("Sec-WebSocket-Protocol") != "anvil.onechat.v1" {
		resp.Body.Close()
		t.Fatalf("upgrade = %s, protocol=%q", resp.Status, resp.Header.Get("Sec-WebSocket-Protocol"))
	}
	return conn, reader
}

func TestOnechatStateDirSelection(t *testing.T) {
	t.Setenv("ANVIL_ONECHAT_STATE_DIR", filepath.Join(t.TempDir(), "env"))
	for _, tc := range []struct{ input, want string }{
		{"", os.Getenv("ANVIL_ONECHAT_STATE_DIR")},
		{"explicit", "explicit"},
	} {
		s, err := NewServer(Options{ConfigDir: t.TempDir(), OnechatStateDir: tc.input})
		if err != nil || s.opts.OnechatStateDir != tc.want {
			t.Fatalf("input=%q server=%v error=%v", tc.input, s, err)
		}
	}
	t.Setenv("ANVIL_ONECHAT_STATE_DIR", "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewServer(Options{ConfigDir: t.TempDir()})
	if err != nil || s.opts.OnechatStateDir != filepath.Join(home, ".config", "onechat") {
		t.Fatalf("default state directory error=%v", err)
	}
}
