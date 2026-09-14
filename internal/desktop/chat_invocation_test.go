package desktop

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLocalChatInvocationPreservesLegacyAndNativeDefaults(t *testing.T) {
	for _, id := range []string{"codex", "grok", "opencode", "prime", "agy"} {
		t.Run(id, func(t *testing.T) {
			tool, _ := catalogTool(id)
			original := append([]string(nil), tool.Invoke.Args...)
			chat := tool.forLocalChat()
			if !reflect.DeepEqual(tool.Invoke.Args, original) {
				t.Fatal("chat changed legacy delegate arguments")
			}
			if chat.Invoke.Mode != tool.Invoke.Mode || chat.Invoke.FileFlag != tool.Invoke.FileFlag {
				t.Fatal("chat changed native prompt transport")
			}
			if id == "prime" || id == "agy" {
				if !reflect.DeepEqual(chat.Invoke.Args, original) {
					t.Fatal("chat changed an existing native JSON invocation")
				}
			}
			if len(chat.Invoke.Args) > 0 {
				chat.Invoke.Args[0] = "mutation"
			}
			if !reflect.DeepEqual(tool.Invoke.Args, original) {
				t.Fatal("chat aliases legacy invocation arguments")
			}
		})
	}
}

func TestLocalChatNativeJSONInvocation(t *testing.T) {
	// These fake CLIs require the documented native argv and emit their public
	// envelopes only after verifying the prompt arrived outside argv. Exercise
	// the HTTP handler and both launch paths, not just the catalog constants.
	cases := []struct{ id, check, event string }{
		{"codex", `[ "$#" = 3 ] && [ "$1" = exec ] && [ "$2" = --skip-git-repo-check ] && [ "$3" = --json ] && check_prompt "$(cat)"`, `{"type":"item.completed","item":{"type":"agent_message","text":"Codex reply"}}`},
		{"grok", `[ "$#" = 4 ] && [ "$1" = --output-format ] && [ "$2" = streaming-messages-json ] && [ "$3" = --prompt-file ] && check_prompt "$(cat "$4")" && [ "$(stat -c %a "$4")" = 600 ]`, `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Grok reply"}]}}`},
		{"opencode", `[ "$#" = 3 ] && [ "$1" = run ] && [ "$2" = --format ] && [ "$3" = json ] && check_prompt "$(cat)"`, `{"type":"text","part":{"text":"OpenCode reply"}}`},
	}
	for _, tc := range cases {
		for _, target := range []string{HarnessTargetNative, HarnessTargetWSL} {
			t.Run(tc.id+"/"+target, func(t *testing.T) {
				binDir := t.TempDir()
				writeExec(t, filepath.Join(binDir, tc.id), "#!/bin/sh\ncheck_prompt() { case \"$1\" in 'private prompt'*'Anvil API is not connected for this turn.'*) return 0;; *) return 1;; esac; }\n"+tc.check+" || exit 91\nprintf '%s\\n' '"+tc.event+"'\n")
				s := streamTestServer(t, binDir, t.TempDir(), target)
				body, _ := json.Marshal(DelegateRequest{Harness: tc.id, Prompt: "private prompt", Workdir: t.TempDir()})
				req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:1738/local/v1/chat/stream", bytes.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				rec := httptest.NewRecorder()
				s.Handler().ServeHTTP(rec, req)
				if rec.Code != http.StatusOK {
					t.Fatalf("chat HTTP status=%d", rec.Code)
				}
				wire := rec.Body.String()
				var streamed string
				var result DelegateResult
				foundResult := false
				for _, frame := range strings.Split(wire, "\n\n") {
					if strings.HasPrefix(frame, "event: stdout\ndata: ") {
						var data struct {
							Line string `json:"line"`
						}
						if err := json.Unmarshal([]byte(strings.TrimPrefix(frame, "event: stdout\ndata: ")), &data); err != nil {
							t.Fatal(err)
						}
						streamed += data.Line
					}
					if strings.HasPrefix(frame, "event: result\ndata: ") {
						if err := json.Unmarshal([]byte(strings.TrimPrefix(frame, "event: result\ndata: ")), &result); err != nil {
							t.Fatal(err)
						}
						foundResult = true
					}
				}
				if !foundResult || result.ExitCode != 0 || result.TimedOut || result.StdoutEventsDropped {
					t.Fatalf("native invocation failed: result=%t exit=%d", foundResult, result.ExitCode)
				}
				if streamed != tc.event || result.Stdout != tc.event+"\n" {
					t.Fatal("native public envelope did not reach stream and final capture")
				}
				if strings.Contains(wire, "private prompt") {
					t.Fatal("prompt leaked to output")
				}
			})
		}
	}
}
