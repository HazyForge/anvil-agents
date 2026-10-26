package runapi

import "testing"

func TestOneChatNativeReplyText(t *testing.T) {
	cases := []struct{ kind, output, want string }{
		{"codex", `{"type":"item.completed","item":{"type":"agent_message","text":"Codex answer"}}`, "Codex answer"},
		{"openCode", `{"type":"text","part":{"text":"OpenCode answer"}}`, "OpenCode answer"},
		{"grokBuild", `{"stopReason":"end_turn","text":"Grok answer"}`, "Grok answer"},
		{"primeAgent", `{"type":"message_end","message":{"role":"assistant","stopReason":"stop","content":[{"type":"thinking","text":"private"},{"type":"text","text":"Prime answer"}]}}`, "Prime answer"},
		{"agy", `{"event":"result","result":{"status":"SUCCESS","response":"agy answer"}}`, "agy answer"},
		{"codex", `{"type":"item.completed","item":{"type":"command_execution","text":"tool output"}}`, ""},
		{"claudeCode", `{"result":"not supported here"}`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.kind+tc.want, func(t *testing.T) {
			got, err := NativeReplyText(tc.kind, tc.output)
			if tc.want == "" {
				if err == nil {
					t.Fatal("accepted non-reply")
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("got %q %v", got, err)
			}
		})
	}
}
