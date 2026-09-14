package desktop

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func streamTestServer(t *testing.T, binDir, configDir, target string) *Server {
	t.Helper()
	d := Discoverer{Path: binDir, InsideWSL: func() bool { return false }, WSL: WSLRunner{LookExe: func() (string, error) { return "", os.ErrNotExist }}}
	if target == HarnessTargetWSL {
		d = fakeWSLDiscoverer(t, binDir)
	}
	s, err := NewServer(Options{Listen: "127.0.0.1:0", ConfigDir: configDir, HarnessTarget: target, Discoverer: d})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestChatStreamImmediateOutputAndWorkdir(t *testing.T) {
	for _, target := range []string{HarnessTargetNative, HarnessTargetWSL} {
		t.Run(target, func(t *testing.T) {
			binDir, configDir := t.TempDir(), t.TempDir()
			workdir := filepath.Join(t.TempDir(), "space $(touch INJECTED)")
			if err := os.Mkdir(workdir, 0700); err != nil {
				t.Fatal(err)
			}
			gate, record, envRecord := filepath.Join(binDir, "gate"), filepath.Join(binDir, "cwd"), filepath.Join(binDir, "env")
			t.Setenv("ANVIL_STREAM_GATE", gate)
			t.Setenv("ANVIL_STREAM_CWD", record)
			t.Setenv("ANVIL_STREAM_ENV", envRecord)
			t.Setenv("ANVIL_ACCESS_TOKEN", "must-not-inherit")
			t.Setenv("AUTHORIZATION", "must-not-inherit")
			t.Setenv("KUBECONFIG", "must-not-inherit")
			writeExec(t, filepath.Join(binDir, "prime-agent"), `#!/bin/sh
printf '%s' "$PWD" > "$ANVIL_STREAM_CWD"
printf '%s/%s/%s' "${ANVIL_ACCESS_TOKEN-unset}" "${AUTHORIZATION-unset}" "${KUBECONFIG-unset}" > "$ANVIL_STREAM_ENV"
printf '{"type":"ready"}\n'
printf 'diagnostic only\n' >&2
while [ ! -f "$ANVIL_STREAM_GATE" ]; do sleep 0.01; done
printf '{"type":"message_end","message":{"role":"assistant","content":[]}}\n'
`)
			s := streamTestServer(t, binDir, configDir, target)
			ts := httptest.NewServer(s.Handler())
			defer ts.Close()
			body, _ := json.Marshal(DelegateRequest{Harness: "prime", Prompt: "hello", Workdir: workdir})
			req, _ := http.NewRequest(http.MethodPost, ts.URL+"/local/v1/chat/stream", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Origin", ts.URL)
			req.Header.Set("Authorization", "Bearer request-token-not-forwarded")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != 200 {
				raw, _ := io.ReadAll(resp.Body)
				t.Fatalf("HTTP%d: %s", resp.StatusCode, raw)
			}
			scan := bufio.NewScanner(resp.Body)
			scan.Buffer(nil, 1<<20)
			readEvent := func() (string, json.RawMessage) {
				t.Helper()
				kind := ""
				var data json.RawMessage
				for scan.Scan() {
					line := scan.Text()
					if line == "" && kind != "" {
						return kind, data
					}
					if strings.HasPrefix(line, "event: ") {
						kind = strings.TrimPrefix(line, "event: ")
					}
					if strings.HasPrefix(line, "data: ") {
						data = json.RawMessage(strings.TrimPrefix(line, "data: "))
					}
				}
				t.Fatalf("stream ended: %v", scan.Err())
				return "", nil
			}
			kind, data := readEvent()
			var started map[string]string
			_ = json.Unmarshal(data, &started)
			if kind != "started" || started["workdir"] != workdir || started["target"] != target {
				t.Fatalf("started=%s %s", kind, data)
			}
			kind, data = readEvent()
			if kind != "stdout" || !bytes.Contains(data, []byte("ready")) {
				t.Fatalf("first output=%s %s", kind, data)
			}
			// The process cannot have exited; it is waiting for this explicit release.
			if _, err := os.Stat(gate); !os.IsNotExist(err) {
				t.Fatalf("gate already exists: %v", err)
			}
			if err := os.WriteFile(gate, []byte("finish"), 0600); err != nil {
				t.Fatal(err)
			}
			kind, _ = readEvent()
			if kind != "stdout" {
				t.Fatalf("completion output=%s", kind)
			}
			kind, data = readEvent()
			var result DelegateResult
			_ = json.Unmarshal(data, &result)
			if kind != "result" || result.ExitCode != 0 || result.Workdir != workdir || !strings.Contains(result.Stdout, "message_end") {
				t.Fatalf("result=%s %s", kind, data)
			}
			cwd, _ := os.ReadFile(record)
			if string(cwd) != workdir {
				t.Fatalf("cwd=%q", cwd)
			}
			env, _ := os.ReadFile(envRecord)
			if string(env) != "unset/unset/unset" {
				t.Fatalf("credential environment was inherited")
			}
			if _, err := os.Stat(filepath.Join(workdir, "INJECTED")); !os.IsNotExist(err) {
				t.Fatalf("path was executed")
			}
		})
	}
}

func TestChatStreamRequestBoundary(t *testing.T) {
	s := streamTestServer(t, t.TempDir(), t.TempDir(), HarnessTargetNative)
	for _, tc := range []struct{ name, host, origin, site, content string }{
		{"other origin", "127.0.0.1:1738", "https://evil.invalid", "", "application/json"},
		{"other port", "127.0.0.1:1738", "http://127.0.0.1:1234", "", "application/json"},
		{"rebound host", "evil.invalid:1738", "http://evil.invalid:1738", "", "application/json"},
		{"null origin", "127.0.0.1:1738", "null", "", "application/json"},
		{"cross site", "127.0.0.1:1738", "", "cross-site", "application/json"},
		{"same site", "127.0.0.1:1738", "", "same-site", "application/json"},
		{"form", "127.0.0.1:1738", "", "", "text/plain"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "http://"+tc.host+"/local/v1/chat/stream", strings.NewReader(`{"harness":"prime","prompt":"hello"}`))
			req.Header.Set("Origin", tc.origin)
			req.Header.Set("Sec-Fetch-Site", tc.site)
			req.Header.Set("Content-Type", tc.content)
			rec := httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status%d", rec.Code)
			}
		})
	}
	for _, host := range []string{"127.0.0.1:1738", "localhost:1738", "[::1]:1738"} {
		req := httptest.NewRequest(http.MethodPost, "http://"+host+"/local/v1/chat/stream", nil)
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
		if !localChatRequest(req) {
			t.Fatalf("rejected local JSON client %s", host)
		}
	}
}

func TestChatWorkspaceDefaultsAndValidation(t *testing.T) {
	for _, target := range []string{HarnessTargetNative, HarnessTargetWSL} {
		t.Run(target, func(t *testing.T) {
			binDir, configDir, home := t.TempDir(), t.TempDir(), t.TempDir()
			t.Setenv("HOME", home)
			s := streamTestServer(t, binDir, configDir, target)
			d := s.discovererFor(s.currentPrefs())
			invoke := invokeTarget{Mode: target, Distro: "Ubuntu-24.04"}
			expected := filepath.Join(configDir, "workspace")
			if target == HarnessTargetWSL {
				expected = filepath.Join(home, ".local/share/anvil-agents-desktop/workspace")
			}
			first, err := s.chatWorkdir(context.Background(), d, invoke, "")
			if err != nil || first != expected {
				t.Fatalf("workspace=%q err=%v", first, err)
			}
			marker := filepath.Join(first, "persistent")
			if err := os.WriteFile(marker, []byte("saved"), 0600); err != nil {
				t.Fatal(err)
			}
			second, err := s.chatWorkdir(context.Background(), d, invoke, "")
			if err != nil || second != first {
				t.Fatalf("second workspace=%q err=%v", second, err)
			}
			if _, err := os.Stat(marker); err != nil {
				t.Fatal(err)
			}
			for _, invalid := range []string{"relative", filepath.Join(home, "missing"), marker, "/bad\x00path"} {
				if _, err := s.chatWorkdir(context.Background(), d, invoke, invalid); err == nil {
					t.Fatalf("accepted invalid directory %q", invalid)
				}
			}
		})
	}
}

func TestChatJSONLinesAreCompleteAndBounded(t *testing.T) {
	var lines []string
	w := &jsonLineWriter{emit: func(line string) error { lines = append(lines, line); return nil }}
	chunks := []string{`{"type":`, "\"partial\"}\nnot json\n", strings.Repeat("x", maxDelegateCapture), "overflow\n", `{"type":"next"}` + "\n", `{"unfinished":`}
	for _, chunk := range chunks {
		n, err := w.Write([]byte(chunk))
		if err != nil || n != len(chunk) {
			t.Fatalf("write=%d %v", n, err)
		}
		if len(w.pending) > maxDelegateCapture {
			t.Fatalf("unbounded pending line")
		}
	}
	if len(lines) != 2 || lines[0] != `{"type":"partial"}` || lines[1] != `{"type":"next"}` {
		t.Fatalf("lines=%q", lines)
	}
}

func TestStreamingDelegateTimeout(t *testing.T) {
	for _, target := range []string{HarnessTargetNative, HarnessTargetWSL} {
		t.Run(target, func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "prime-agent")
			writeExec(t, bin, "#!/bin/sh\nprintf '{\"type\":\"ready\"}\\n'\nsleep 60\n")
			d := Discoverer{}
			if target == HarnessTargetWSL {
				d = fakeWSLDiscoverer(t, dir)
			}
			tool, _ := catalogTool("prime")
			var output bytes.Buffer
			start := time.Now()
			result, err := runDelegateWithOptions(context.Background(), d, tool, invokeTarget{Mode: target, Bin: bin, Distro: "Ubuntu-24.04"}, "hello", 100*time.Millisecond, delegateOptions{Workdir: dir, Stdout: &output})
			if err != nil || !result.TimedOut || result.ExitCode != -1 || time.Since(start) > 3*time.Second || !strings.Contains(output.String(), "ready") {
				t.Fatalf("timeout result=%+v err=%v duration=%v", result, err, time.Since(start))
			}
		})
	}
}

func TestChatStreamDisconnectCancelsHarness(t *testing.T) {
	dir := t.TempDir()
	writeExec(t, filepath.Join(dir, "prime-agent"), "#!/bin/sh\nprintf '{\"type\":\"ready\"}\\n'\nsleep 60\n")
	s := streamTestServer(t, dir, t.TempDir(), HarnessTargetNative)
	done := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { defer close(done); s.Handler().ServeHTTP(w, r) }))
	defer ts.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, ts.URL+"/local/v1/chat/stream", strings.NewReader(`{"harness":"prime","prompt":"hello"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("HTTP%d", resp.StatusCode)
	}
	scan := bufio.NewScanner(resp.Body)
	ready := false
	for scan.Scan() {
		if strings.Contains(scan.Text(), "ready") {
			ready = true
			break
		}
	}
	if !ready {
		t.Fatal("process never emitted ready")
	}
	cancel()
	_ = resp.Body.Close()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("request cancellation left the harness running")
	}
}
