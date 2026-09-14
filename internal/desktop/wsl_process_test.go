package desktop

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Unlike fake-wsl.sh's exec, this launcher deliberately leaves its Linux child
// outside the Windows-launcher stand-in's process group. Killing only the
// launcher must not satisfy this test.
func detachedWSLDiscoverer(t *testing.T, dir string) Discoverer {
	t.Helper()
	d := fakeWSLDiscoverer(t, dir)
	original, err := d.WSL.LookExe()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("ANVIL_TEST_WSL_EXEC", original)
	launcher := filepath.Join(dir, "detached-wsl")
	writeExec(t, launcher, `#!/bin/bash
managed=0
for arg in "$@"; do case "$arg" in ANVIL_DESKTOP_RUN_TIMEOUT=*) managed=1;; esac; done
if [ "$managed" = 1 ]; then
  setsid "$ANVIL_TEST_WSL_EXEC" "$@" <&0 &
  wait "$!"
else
  exec "$ANVIL_TEST_WSL_EXEC" "$@"
fi
`)
	d.WSL.LookExe = func() (string, error) { return launcher, nil }
	return d
}

func TestManagedWSLCancellationStopsDetachedWorkload(t *testing.T) {
	dir := t.TempDir()
	heartbeat := filepath.Join(dir, "heartbeat")
	pgid := filepath.Join(dir, "pgid")
	t.Setenv("ANVIL_TEST_HEARTBEAT", heartbeat)
	t.Setenv("ANVIL_TEST_PGID", pgid)
	writeExec(t, filepath.Join(dir, "prime-agent"), `#!/bin/sh
ps -o pgid= -p "$$" > "$ANVIL_TEST_PGID"
(while :; do printf x >> "$ANVIL_TEST_HEARTBEAT"; sleep 0.02; done) &
printf '{"type":"ready"}\n'
wait
`)
	t.Cleanup(func() {
		if raw, err := os.ReadFile(pgid); err == nil {
			_ = exec.Command("/bin/kill", "-KILL", "--", "-"+strings.TrimSpace(string(raw))).Run()
		}
	})
	d := detachedWSLDiscoverer(t, dir)
	tool, _ := catalogTool("prime")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var output bytes.Buffer
	observed := make(chan struct{}, 1)
	out := &jsonLineWriter{emit: func(line string) error {
		output.WriteString(line)
		if strings.Contains(line, "ready") {
			observed <- struct{}{}
		}
		return nil
	}}
	done := make(chan error, 1)
	go func() {
		_, err := runDelegateWithOptions(ctx, d, tool, invokeTarget{Mode: HarnessTargetWSL, Distro: "Ubuntu-24.04"}, "hello", time.Minute, delegateOptions{Workdir: dir, Stdout: out})
		done <- err
	}()
	select {
	case <-observed:
	case <-time.After(4 * time.Second):
		t.Fatal("detached workload did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("WSL cleanup did not finish")
	}
	before, err := os.ReadFile(heartbeat)
	if err != nil || len(before) == 0 {
		t.Fatalf("workload never ran: %v", err)
	}
	time.Sleep(150 * time.Millisecond)
	after, _ := os.ReadFile(heartbeat)
	if !bytes.Equal(before, after) {
		t.Fatal("Linux child kept working after canceled WSL request returned")
	}
}

func TestManagedWSLLateLaunchCannotEscapeCleanup(t *testing.T) {
	dir := t.TempDir()
	d := fakeWSLDiscoverer(t, dir)
	setup, err := d.wslRunner().Run(context.Background(), WSLRequest{Argv: []string{"/bin/mktemp", "-d", "/tmp/anvil-desktop-run.XXXXXXXXXX"}})
	control := strings.TrimSpace(setup.Stdout)
	if err != nil || !wslRunDirRE.MatchString(control) {
		t.Fatal("control setup failed")
	}
	marker := filepath.Join(dir, "must-not-run")
	cleanup, err := d.wslRunner().Run(context.Background(), WSLRequest{Argv: []string{"/bin/bash", "-c", wslManagedCleanupScript}, Env: []string{"ANVIL_DESKTOP_RUN_DIR=" + control}})
	if err != nil || cleanup.ExitCode != 0 {
		t.Fatalf("cleanup=%+v %v", cleanup, err)
	}
	late, err := d.wslRunner().Run(context.Background(), WSLRequest{Argv: []string{"setsid", "--wait", "/bin/bash", "-c", wslManagedRunScript, "test", "/usr/bin/touch", marker}, Env: []string{"ANVIL_DESKTOP_RUN_DIR=" + control, "ANVIL_DESKTOP_RUN_TIMEOUT=1s"}})
	if err != nil || late.ExitCode == 0 {
		t.Fatalf("late launch=%+v %v", late, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("late WSL startup escaped cancellation")
	}
}

func TestChatStreamWorkspaceSingleFlightAndFinalLine(t *testing.T) {
	for _, target := range []string{HarnessTargetNative, HarnessTargetWSL} {
		t.Run(target, func(t *testing.T) {
			dir := t.TempDir()
			gate := filepath.Join(dir, "gate")
			t.Setenv("ANVIL_TEST_GATE", gate)
			writeExec(t, filepath.Join(dir, "prime-agent"), `#!/bin/sh
printf '{"type":"ready"}\n'
while [ ! -f "$ANVIL_TEST_GATE" ]; do sleep 0.01; done
printf '{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"FINAL"}]}}'
`)
			s := streamTestServer(t, dir, t.TempDir(), target)
			ts := httptest.NewServer(s.Handler())
			defer ts.Close()
			body, _ := json.Marshal(DelegateRequest{Harness: "prime", Prompt: "hello", Workdir: dir})
			post := func() *http.Response {
				t.Helper()
				req, _ := http.NewRequest(http.MethodPost, ts.URL+"/local/v1/chat/stream", bytes.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				return resp
			}
			first := post()
			defer first.Body.Close()
			if first.StatusCode != 200 {
				t.Fatalf("firstHTTP%d", first.StatusCode)
			}
			second := post()
			_ = second.Body.Close()
			if second.StatusCode != 409 {
				t.Fatalf("overlapping HTTP%d", second.StatusCode)
			}
			if err := os.WriteFile(gate, []byte("done"), 0600); err != nil {
				t.Fatal(err)
			}
			raw, err := io.ReadAll(first.Body)
			if err != nil {
				t.Fatal(err)
			}
			terminal := bytes.Index(raw, []byte(`\"text\":\"FINAL\"`))
			result := bytes.Index(raw, []byte("event: result"))
			if terminal < 0 || result < terminal {
				t.Fatalf("unterminated native JSON did not flush before result: %s", raw)
			}
			third := post()
			_, _ = io.Copy(io.Discard, third.Body)
			_ = third.Body.Close()
			if third.StatusCode != 200 {
				t.Fatalf("completed workspace stayed locked: %d", third.StatusCode)
			}
		})
	}
}

func TestChatJSONFlushAndTruncationMetadata(t *testing.T) {
	var lines []string
	w := &jsonLineWriter{emit: func(line string) error { lines = append(lines, line); return nil }}
	_, _ = w.Write([]byte(`{"complete":true}`))
	if err := w.Flush(); err != nil || len(lines) != 1 {
		t.Fatalf("flush=%q %v", lines, err)
	}
	_, _ = w.Write([]byte(`{"incomplete":`))
	_ = w.Flush()
	if len(lines) != 1 || !w.dropped {
		t.Fatal("incomplete final JSON accepted")
	}
	buffer := &cappedBuffer{limit: 3}
	_, _ = buffer.Write([]byte("abc"))
	if buffer.truncated {
		t.Fatal("exact capture reported truncation")
	}
	_, _ = buffer.Write([]byte("d"))
	if !buffer.truncated || buffer.String() != "abc" {
		t.Fatal("capture truncation missing")
	}
}

func TestChatStreamKeepsWorkspaceLockedIfWSLCleanupUnconfirmed(t *testing.T) {
	dir := t.TempDir()
	writeExec(t, filepath.Join(dir, "prime-agent"), "#!/bin/sh\nexit 0\n")
	s := streamTestServer(t, dir, t.TempDir(), HarnessTargetWSL)
	run := s.opts.Discoverer.wslRunner().Run
	s.opts.Discoverer.WSL.Run = func(ctx context.Context, req WSLRequest) (WSLResult, error) {
		if req.ManagedProcess {
			return WSLResult{ExitCode: -1}, errWSLCleanupUnconfirmed
		}
		return run(ctx, req)
	}
	body, _ := json.Marshal(DelegateRequest{Harness: "prime", Prompt: "hello", Workdir: dir})
	post := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:1738/local/v1/chat/stream", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		return rec
	}
	first := post()
	if first.Code != 200 || !strings.Contains(first.Body.String(), "cleanup_unconfirmed") {
		t.Fatalf("cleanup failure not reported: %d %s", first.Code, first.Body.String())
	}
	next := post()
	if next.Code != 409 {
		t.Fatalf("unconfirmed process cleanup allowed another run: %d", next.Code)
	}
}
