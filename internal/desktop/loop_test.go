package desktop

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func loopHTTP(t *testing.T, origin, path string, body any) (int, loopSnapshot) {
	t.Helper()
	status, snapshot, raw, err := loopCall(origin, path, body)
	if err != nil {
		t.Fatal(err)
	}
	if status >= 500 {
		t.Logf("manager HTTP %d: %s", status, raw)
	}
	return status, snapshot
}
func loopCall(origin, path string, body any) (int, loopSnapshot, string, error) {
	method := http.MethodGet
	var payload io.Reader
	if body != nil {
		method = http.MethodPost
		raw, _ := json.Marshal(body)
		payload = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, origin+path, payload)
	if err != nil {
		return 0, loopSnapshot{}, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", origin)
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Do(req)
	if err != nil {
		return 0, loopSnapshot{}, "", err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	var snapshot loopSnapshot
	_ = json.Unmarshal(raw, &snapshot)
	return response.StatusCode, snapshot, string(raw), err
}
func loopWait(t *testing.T, origin string, predicate func(loopSnapshot) bool) loopSnapshot {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	var last loopSnapshot
	for time.Now().Before(deadline) {
		code, snapshot := loopHTTP(t, origin, "/local/v1/manager/loop", nil)
		last = snapshot
		if code == 200 && predicate(snapshot) {
			return snapshot
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("manager state did not settle: %+v", last)
	return last
}
func loopFixture(t *testing.T, script string) (*Server, *httptest.Server, string, string) {
	t.Helper()
	bin, config, folder := t.TempDir(), t.TempDir(), t.TempDir()
	writeExec(t, filepath.Join(bin, "prime-agent"), script)
	s := streamTestServer(t, bin, config, HarnessTargetNative)
	host := httptest.NewServer(s.Handler())
	t.Cleanup(func() { s.Close(); host.Close() })
	return s, host, folder, config
}
func loopWork(folder string) loopWorkRequest {
	return loopWorkRequest{RequestID: uuid.NewString(), Conversation: loopConversation{ID: "conversation-one", Name: "Saved work", Harness: "prime", Workdir: folder, Target: "native", Messages: []loopMessage{{Role: "user", Content: "Original topic"}, {Role: "assistant", Content: "Original reply"}}}, Content: "Continue the saved work"}
}

const loopSuccessfulPrime = `{"type":"message_end","message":{"role":"assistant","stopReason":"stop","content":[{"type":"text","text":"Public result"}]}}`

// Owns native result interpretation and durable ask/verify behavior: native
// public output differs from successful process exit, and failure after a
// partial reply must not become completed work.
func TestLocalLoopNativeVerification(t *testing.T) {
	for _, tc := range []struct {
		name, output, state, reason string
		exit                        int
	}{
		{"public reply", loopSuccessfulPrime, "completed", "", 0},
		{"tools only", `{"type":"tool_result","content":"not a public answer"}`, "needs_decision", "reply_unverified", 0},
		{"failed native envelope", loopSuccessfulPrime + "\n" + `{"type":"message_end","message":{"role":"assistant","stopReason":"error","content":[]}}`, "needs_decision", "reply_unverified", 0},
		{"nonzero after reply", loopSuccessfulPrime, "needs_decision", "execution_failed", 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script := "#!/bin/sh\ncat > received.txt\nprintf 'run\\n' >> calls\nprintf '%s\\n' '" + tc.output + "'\nexit " + fmt.Sprint(tc.exit) + "\n"
			_, host, folder, config := loopFixture(t, script)
			code, _ := loopHTTP(t, host.URL, "/local/v1/manager/loop/work", loopWork(folder))
			if code != 202 {
				t.Fatalf("admission=%d", code)
			}
			snapshot := loopWait(t, host.URL, func(s loopSnapshot) bool { return len(s.Tasks) == 1 && s.Tasks[0].State == tc.state })
			task := snapshot.Tasks[0]
			if task.Blocking || task.Attempt != 1 {
				t.Fatalf("settlement=%+v", task)
			}
			if tc.state == "completed" {
				if task.Reply != "Public result" || task.Verification == "" {
					t.Fatalf("completion=%+v", task)
				}
			} else if len(snapshot.Decisions) != 1 || snapshot.Decisions[0].Reason != tc.reason || task.Reply != "" {
				t.Fatalf("decision=%+v", snapshot)
			}
			raw, err := os.ReadFile(filepath.Join(config, "manager-loop.json"))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), "not a public answer") || strings.Contains(string(raw), "message_end") {
				t.Fatal("raw native output was persisted")
			}
			info, err := os.Stat(filepath.Join(config, "manager-loop.json"))
			if err != nil {
				t.Fatal(err)
			}
			if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
				t.Fatalf("store permissions %v", info.Mode())
			}
		})
	}
}

// Real simultaneous HTTP submissions/decisions protect the at-most-one native
// attempt boundary. Scripted failure determines process behavior, not receipts.
func TestLocalLoopIdempotentConcurrentDecisions(t *testing.T) {
	script := `#!/bin/sh
cat > received.txt
printf 'run\n' >> calls
if [ ! -f failed-once ]; then touch failed-once; exit 4; fi
printf '%s\n' '` + loopSuccessfulPrime + `'
`
	s, host, folder, config := loopFixture(t, script)
	body := loopWork(folder)
	parallel := func(path string, body any) {
		t.Helper()
		var wg sync.WaitGroup
		errs := make(chan string, 12)
		for range 12 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				status, _, raw, err := loopCall(host.URL, path, body)
				if err != nil || status < 200 || status >= 300 {
					errs <- fmt.Sprintf("%d %s %v", status, raw, err)
				}
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Error(err)
		}
	}
	parallel("/local/v1/manager/loop/work", body)
	snapshot := loopWait(t, host.URL, func(v loopSnapshot) bool { return len(v.Decisions) == 1 && v.Tasks[0].State == "needs_decision" })
	decision := snapshot.Decisions[0]
	if code, _ := loopHTTP(t, host.URL, "/local/v1/chat/stream", map[string]any{"harness": "prime", "prompt": "bypass frozen history", "workdir": folder, "conversationId": body.Conversation.ID}); code != 409 {
		t.Fatalf("unresolved direct chat accepted=%d", code)
	}
	// Another host using this same config cannot own or mutate the store.
	other := streamTestServer(t, s.opts.Discoverer.Path, config, HarnessTargetNative)
	defer other.Close()
	otherHost := httptest.NewServer(other.Handler())
	defer otherHost.Close()
	if code, _ := loopHTTP(t, otherHost.URL, "/local/v1/manager/loop", nil); code != 409 {
		t.Fatalf("second store owner=%d", code)
	}
	choice := loopChoiceRequest{RequestID: uuid.NewString(), Choice: "retry", Version: decision.Version}
	parallel("/local/v1/manager/loop/decisions/"+decision.ID, choice)
	snapshot = loopWait(t, host.URL, func(v loopSnapshot) bool { return v.Tasks[0].State == "completed" })
	calls, _ := os.ReadFile(filepath.Join(folder, "calls"))
	if strings.Count(string(calls), "run\n") != 2 || snapshot.Tasks[0].Attempt != 2 {
		t.Fatalf("duplicated execution: %q %+v", calls, snapshot.Tasks)
	}
	altered := body
	altered.Content = "different intent"
	if code, _ := loopHTTP(t, host.URL, "/local/v1/manager/loop/work", altered); code != 409 {
		t.Fatalf("mutated request replay=%d", code)
	}
	stale := choice
	stale.RequestID = uuid.NewString()
	if code, _ := loopHTTP(t, host.URL, "/local/v1/manager/loop/decisions/"+decision.ID, stale); code != 409 {
		t.Fatalf("stale choice accepted=%d", code)
	}
}

// Disk failure after native completion is a different lifecycle boundary from
// admission failure: recovery must surface uncertainty without replaying work.
func TestLocalLoopStorageFailureRecovery(t *testing.T) {
	script := `#!/bin/sh
cat > received.txt
printf 'run\n' >> calls
touch started
while [ ! -f release ]; do sleep 0.02; done
printf '%s\n' '` + loopSuccessfulPrime + `'
`
	_, host, folder, config := loopFixture(t, script)
	body := loopWork(folder)
	if code, _ := loopHTTP(t, host.URL, "/local/v1/manager/loop/work", body); code != 202 {
		t.Fatal(code)
	}
	waitLoopFile(t, filepath.Join(folder, "started"))
	store := filepath.Join(config, "manager-loop.json")
	saved := store + ".saved"
	if err := os.Rename(store, saved); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(store, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "release"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	loopWait(t, host.URL, func(v loopSnapshot) bool { return v.StorageError != "" })
	if err := os.Remove(store); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(saved, store); err != nil {
		t.Fatal(err)
	}
	snapshot := loopWait(t, host.URL, func(v loopSnapshot) bool { return v.StorageError == "" && len(v.Decisions) == 1 })
	if snapshot.Tasks[0].State != "needs_decision" || !snapshot.Tasks[0].Blocking || snapshot.Decisions[0].Reason != "completion_unknown" {
		t.Fatalf("lost result was not guarded: %+v", snapshot)
	}
	calls, _ := os.ReadFile(filepath.Join(folder, "calls"))
	if string(calls) != "run\n" {
		t.Fatalf("work replayed after storage failure: %q", calls)
	}
}
func waitLoopFile(t *testing.T, path string) {
	t.Helper()
	for deadline := time.Now().Add(8 * time.Second); time.Now().Before(deadline); {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("file was not produced: %s", path)
}

// Child process is a real Desktop host killed without graceful cleanup. Its
// orphaned harmless harness lets the restart test prove uncertain work is held.
func TestLocalLoopCrashHostHelper(t *testing.T) {
	if os.Getenv("ANVIL_LOOP_CRASH_HELPER") != "1" {
		t.Skip("child process helper")
	}
	s, err := NewServer(Options{Listen: "127.0.0.1:0", ConfigDir: os.Getenv("ANVIL_LOOP_CONFIG"), HarnessTarget: HarnessTargetNative, Discoverer: Discoverer{Path: os.Getenv("ANVIL_LOOP_BIN"), InsideWSL: func() bool { return false }, WSL: WSLRunner{LookExe: func() (string, error) { return "", os.ErrNotExist }}}, OnListen: func(addr string) { _ = os.WriteFile(os.Getenv("ANVIL_LOOP_ADDRESS"), []byte("http://"+addr), 0600) }})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func TestLocalLoopCrashRestartPreservesGuard(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX subprocess crash fixture")
	}
	bin, config, folder := t.TempDir(), t.TempDir(), t.TempDir()
	writeExec(t, filepath.Join(bin, "prime-agent"), `#!/bin/sh
cat > received.txt
printf 'run\n' >> calls
touch started
while [ ! -f release ]; do sleep 0.02; done
touch finished
printf '%s\n' '`+loopSuccessfulPrime+`'
`)
	address := filepath.Join(config, "address")
	cmd := exec.Command(os.Args[0], "-test.run=^TestLocalLoopCrashHostHelper$", "-test.count=1")
	cmd.Env = append(os.Environ(), "ANVIL_LOOP_CRASH_HELPER=1", "ANVIL_LOOP_BIN="+bin, "ANVIL_LOOP_CONFIG="+config, "ANVIL_LOOP_ADDRESS="+address)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = os.WriteFile(filepath.Join(folder, "release"), nil, 0600) })
	waitLoopFile(t, address)
	raw, _ := os.ReadFile(address)
	body := loopWork(folder)
	if code, _ := loopHTTP(t, string(raw), "/local/v1/manager/loop/work", body); code != 202 {
		t.Fatal(code)
	}
	waitLoopFile(t, filepath.Join(folder, "started"))
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	s := streamTestServer(t, bin, config, HarnessTargetNative)
	host := httptest.NewServer(s.Handler())
	defer host.Close()
	defer s.Close()
	snapshot := loopWait(t, host.URL, func(v loopSnapshot) bool { return len(v.Decisions) == 1 })
	if snapshot.Tasks[0].State != "needs_decision" || !snapshot.Tasks[0].Blocking || snapshot.Tasks[0].Attempt != 1 {
		t.Fatalf("restart state=%+v", snapshot)
	}
	if code, _ := loopHTTP(t, host.URL, "/local/v1/chat/stream", map[string]any{"harness": "prime", "prompt": "bypass unknown", "workdir": folder}); code != 409 {
		t.Fatalf("unknown workspace bypass=%d", code)
	}
	// Accepted queued work must also wait behind the recovered guard.
	next := loopWork(folder)
	next.Conversation.ID = "another-conversation"
	if code, _ := loopHTTP(t, host.URL, "/local/v1/manager/loop/work", next); code != 202 {
		t.Fatal(code)
	}
	time.Sleep(600 * time.Millisecond)
	calls, _ := os.ReadFile(filepath.Join(folder, "calls"))
	if string(calls) != "run\n" {
		t.Fatalf("restart replayed unknown execution: %q", calls)
	}
	_ = os.WriteFile(filepath.Join(folder, "release"), nil, 0600)
	waitLoopFile(t, filepath.Join(folder, "finished"))
	decision := snapshot.Decisions[0]
	choice := loopChoiceRequest{RequestID: uuid.NewString(), Version: decision.Version, Choice: "checked_stopped"}
	if code, _ := loopHTTP(t, host.URL, "/local/v1/manager/loop/decisions/"+decision.ID, choice); code != 200 {
		t.Fatal(code)
	}
	// Replaying the original answer succeeds even after the decision version moves.
	if code, _ := loopHTTP(t, host.URL, "/local/v1/manager/loop/decisions/"+decision.ID, choice); code != 200 {
		t.Fatalf("lost answer replay=%d", code)
	}
	snapshot = loopWait(t, host.URL, func(v loopSnapshot) bool { return len(v.Tasks) == 2 && v.Tasks[1].State == "completed" })
	if snapshot.Tasks[0].Attempt != 1 || snapshot.Tasks[0].State != "needs_decision" {
		t.Fatalf("acknowledgment restarted old work: %+v", snapshot.Tasks)
	}
	calls, _ = os.ReadFile(filepath.Join(folder, "calls"))
	if strings.Count(string(calls), "run\n") != 2 {
		t.Fatalf("unexpected native attempts: %q", calls)
	}
}

func TestLocalLoopObservedDefaultAndSymlinkBindings(t *testing.T) {
	t.Run("default folder retry", func(t *testing.T) {
		_, host, _, config := loopFixture(t, "#!/bin/sh\ncat > received.txt\nprintf '%s\\n' '"+loopSuccessfulPrime+"'\n")
		conversation := loopWork("").Conversation
		conversation.Messages = append(conversation.Messages, loopMessage{Role: "user", Content: "Original unfinished request"})
		code, snapshot := loopHTTP(t, host.URL, "/local/v1/manager/loop/observe", loopObserveRequest{RequestID: uuid.NewString(), Conversation: conversation, LastError: "Previous native turn failed"})
		if code != 200 || len(snapshot.Tasks) != 1 {
			t.Fatalf("observe=%d %+v", code, snapshot)
		}
		task := snapshot.Tasks[0]
		if task.Conversation.Workdir != filepath.Join(config, "workspace") || task.SubmittedWorkdir != "" || task.WorkspaceUnresolved {
			t.Fatalf("default folder not frozen: %+v", task)
		}
		decision := snapshot.Decisions[0]
		if code, _ := loopHTTP(t, host.URL, "/local/v1/manager/loop/decisions/"+decision.ID, loopChoiceRequest{RequestID: uuid.NewString(), Choice: "retry", Version: decision.Version}); code != 200 {
			t.Fatal(code)
		}
		loopWait(t, host.URL, func(v loopSnapshot) bool { return v.Tasks[0].State == "completed" })
		prompt, err := os.ReadFile(filepath.Join(config, "workspace", "received.txt"))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Count(string(prompt), "Original unfinished request") != 1 {
			t.Fatalf("observed user message was duplicated: %s", prompt)
		}
	})
	t.Run("symlink active guard", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("symlink fixture requires privileges on Windows")
		}
		_, host, folder, _ := loopFixture(t, `#!/bin/sh
cat > received.txt
touch started
while [ ! -f release ]; do sleep 0.02; done
printf '%s\n' '`+loopSuccessfulPrime+`'
`)
		alias := filepath.Join(t.TempDir(), "alias")
		if err := os.Symlink(folder, alias); err != nil {
			t.Fatal(err)
		}
		done := make(chan string, 1)
		go func() {
			status, _, raw, err := loopCall(host.URL, "/local/v1/chat/stream", map[string]any{"harness": "prime", "prompt": "active direct turn", "workdir": folder, "conversationId": "direct-active"})
			done <- fmt.Sprintf("%d %s %v", status, raw, err)
		}()
		waitLoopFile(t, filepath.Join(folder, "started"))
		conversation := loopWork(alias).Conversation
		conversation.Messages = append(conversation.Messages, loopMessage{Role: "user", Content: "Possibly still running"})
		code, snapshot := loopHTTP(t, host.URL, "/local/v1/manager/loop/observe", loopObserveRequest{RequestID: uuid.NewString(), Conversation: conversation, Pending: true})
		if code != 200 || snapshot.Tasks[0].Conversation.Workdir != folder || snapshot.Tasks[0].SubmittedWorkdir != alias {
			t.Fatalf("observation binding=%d %+v", code, snapshot)
		}
		decision := snapshot.Decisions[0]
		if code, _ := loopHTTP(t, host.URL, "/local/v1/manager/loop/decisions/"+decision.ID, loopChoiceRequest{RequestID: uuid.NewString(), Choice: "checked_stopped", Version: decision.Version}); code != 409 {
			t.Fatalf("acknowledged active symlink workspace=%d", code)
		}
		if code, _ := loopHTTP(t, host.URL, "/local/v1/chat/stream", map[string]any{"harness": "prime", "prompt": "bypass alias guard", "workdir": folder}); code != 409 {
			t.Fatalf("alias guard bypass=%d", code)
		}
		_ = os.WriteFile(filepath.Join(folder, "release"), nil, 0600)
		select {
		case result := <-done:
			if !strings.HasPrefix(result, "200 ") {
				t.Fatal(result)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("direct turn did not finish")
		}
		if code, _ := loopHTTP(t, host.URL, "/local/v1/manager/loop/decisions/"+decision.ID, loopChoiceRequest{RequestID: uuid.NewString(), Choice: "checked_stopped", Version: decision.Version}); code != 200 {
			t.Fatalf("stopped symlink acknowledgment=%d", code)
		}
	})
}

func TestLocalLoopDirectTurnAdmissionAndSaveFailure(t *testing.T) {
	t.Run("active conversation cannot freeze stale history", func(t *testing.T) {
		_, host, folder, _ := loopFixture(t, `#!/bin/sh
cat > received.txt
touch started
while [ ! -f release ]; do sleep 0.02; done
printf '%s\n' '`+loopSuccessfulPrime+`'
`)
		// Initialize the durable owner before direct admission, as Server.Start does.
		loopHTTP(t, host.URL, "/local/v1/manager/loop", nil)
		body := loopWork(folder)
		done := make(chan struct{})
		go func() {
			defer close(done)
			_, _, _, _ = loopCall(host.URL, "/local/v1/chat/stream", map[string]any{"harness": "prime", "prompt": "direct follow-up", "workdir": folder, "conversationId": body.Conversation.ID})
		}()
		waitLoopFile(t, filepath.Join(folder, "started"))
		if code, _ := loopHTTP(t, host.URL, "/local/v1/manager/loop/work", body); code != 409 {
			t.Fatalf("stale same-conversation history accepted=%d", code)
		}
		body.Conversation.ID = "different-conversation"
		if code, _ := loopHTTP(t, host.URL, "/local/v1/manager/loop/work", body); code != 202 {
			t.Fatalf("separate conversation could not queue=%d", code)
		}
		_ = os.WriteFile(filepath.Join(folder, "release"), nil, 0600)
		<-done
		loopWait(t, host.URL, func(v loopSnapshot) bool { return len(v.Tasks) == 1 && v.Tasks[0].State == "completed" })
	})
	t.Run("failed admission preserves intent but never starts it", func(t *testing.T) {
		_, host, folder, config := loopFixture(t, "#!/bin/sh\ntouch should-not-run\nprintf '%s\\n' '"+loopSuccessfulPrime+"'\n")
		loopHTTP(t, host.URL, "/local/v1/manager/loop", nil)
		store := filepath.Join(config, "manager-loop.json")
		if err := os.Mkdir(store, 0700); err != nil {
			t.Fatal(err)
		}
		code, snapshot := loopHTTP(t, host.URL, "/local/v1/manager/loop/work", loopWork(folder))
		if code != 503 {
			t.Fatalf("failed store accepted=%d %+v", code, snapshot)
		}
		if err := os.Remove(store); err != nil {
			t.Fatal(err)
		}
		snapshot = loopWait(t, host.URL, func(v loopSnapshot) bool { return v.StorageError == "" })
		if len(snapshot.Tasks) != 0 || len(snapshot.Decisions) != 0 {
			t.Fatalf("failed admission leaked state: %+v", snapshot)
		}
		if _, err := os.Stat(filepath.Join(folder, "should-not-run")); !os.IsNotExist(err) {
			t.Fatal("native process started after failed admission")
		}
	})
}

func TestLocalLoopCorruptStoreAndReadOriginFailClosed(t *testing.T) {
	s, host, folder, config := loopFixture(t, "#!/bin/sh\ntouch unexpected-run\n")
	for _, tc := range []struct{ host, origin, site string }{{"evil.invalid:1738", "http://evil.invalid:1738", ""}, {"127.0.0.1:1738", "https://evil.invalid", ""}, {"127.0.0.1:1738", "", "cross-site"}} {
		req := httptest.NewRequest(http.MethodGet, "http://"+tc.host+"/local/v1/manager/loop", nil)
		req.Header.Set("Origin", tc.origin)
		req.Header.Set("Sec-Fetch-Site", tc.site)
		result := httptest.NewRecorder()
		s.Handler().ServeHTTP(result, req)
		if result.Code != 403 {
			t.Fatalf("unsafe state read=%d", result.Code)
		}
	}
	conversation := loopWork(folder).Conversation
	conversation.Messages = append(conversation.Messages, loopMessage{Role: "user", Content: "Unconfirmed work"})
	if code, _ := loopHTTP(t, host.URL, "/local/v1/manager/loop/observe", loopObserveRequest{RequestID: uuid.NewString(), Conversation: conversation, Pending: true}); code != 200 {
		t.Fatal(code)
	}
	s.Close()
	path := filepath.Join(config, "manager-loop.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var disk loopDisk
	if err = json.Unmarshal(raw, &disk); err != nil {
		t.Fatal(err)
	}
	// This valid JSON contradicts the unresolved decision and must not be queued.
	disk.Tasks[0].State = "queued"
	changed, _ := json.Marshal(disk)
	if err = os.WriteFile(path, changed, 0600); err != nil {
		t.Fatal(err)
	}
	restarted := streamTestServer(t, s.opts.Discoverer.Path, config, HarnessTargetNative)
	defer restarted.Close()
	restartedHost := httptest.NewServer(restarted.Handler())
	defer restartedHost.Close()
	if code, _ := loopHTTP(t, restartedHost.URL, "/local/v1/manager/loop", nil); code != 503 {
		t.Fatalf("inconsistent store accepted=%d", code)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(after, changed) {
		t.Fatal("corrupt store was silently rewritten")
	}
	if _, err := os.Stat(filepath.Join(folder, "unexpected-run")); !os.IsNotExist(err) {
		t.Fatal("corrupt intent executed")
	}
}
