package runapi

import (
	agents "github.com/hazyforge/anvil-agents/api/v1alpha1"
	"testing"
)

func TestNativeChatRepliesExcludeToolAndRunnerOutput(t *testing.T) {
	tests := []struct {
		backend   agents.AgentRunHarnessBackendKind
		raw, want string
	}{
		{agents.AgentRunHarnessBackendCodex, "ANVIL_AGENT_RUN_START backend=codex\n" + `{"type":"item.completed","item":{"type":"command_execution","aggregated_output":"private tool output"}}` + "\n" + `{"type":"item.completed","item":{"type":"agent_message","text":"Hello from Codex"}}` + "\nANVIL_AGENT_RUN_COMPLETE", "Hello from Codex"},
		{agents.AgentRunHarnessBackendOpenCode, `{"type":"tool_use","part":{"text":"not a reply"}}` + "\n" + `{"type":"text","part":{"text":"Hello from OpenCode"}}`, "Hello from OpenCode"},
		{agents.AgentRunHarnessBackendAgy, `{"event":"step_update","step_update":{"step_type":"agent_response","text_delta":"Hello from AGY"}}` + "\n" + `{"event":"result","result":{"status":"SUCCESS","response":"Hello from AGY"}}`, "Hello from AGY"},
		{agents.AgentRunHarnessBackendGrokBuild, `{"type":"assistant","message":{"content":[{"type":"thinking","text":"hidden"},{"type":"text","text":"intermediate"}]}}` + "\n" + `{"type":"result","result":"Final Grok reply"}`, "Final Grok reply"},
		{agents.AgentRunHarnessBackendCustom, "Installing tools\nANVIL_AGENT_RUN_START\nA plain reply\nANVIL_AGENT_RUN_COMPLETE", "A plain reply"},
	}
	for _, tt := range tests {
		t.Run(string(tt.backend), func(t *testing.T) {
			got, err := extractChatReply(tt.backend, tt.raw)
			if err != nil || got != tt.want {
				t.Fatalf("got %q, %v; want %q", got, err, tt.want)
			}
		})
	}
	grok, err := extractChatReply(agents.AgentRunHarnessBackendGrokBuild, `{"text":"Actual native Grok reply","stopReason":"end_turn"}`)
	if err != nil || grok != "Actual native Grok reply" {
		t.Fatalf("native Grok: %q %v", grok, err)
	}
	if got, err := extractChatReply(agents.AgentRunHarnessBackendGrokBuild, `{"type":"tool_result","response":"tool text"}`); err == nil {
		t.Fatalf("tool response became reply: %q", got)
	}
	for _, raw := range []string{"ANVIL_AGENT_RUN_COMPLETE", `{"type":"item.completed","item":{"type":"command_execution","text":"tool output"}}`, `{"type":"turn.completed"}`} {
		if text, err := extractChatReply(agents.AgentRunHarnessBackendCodex, raw); err == nil {
			t.Fatalf("accepted non-reply: %q", text)
		}
	}
}

func TestGrokBuildReplyFallsBackPastToolFrames(t *testing.T) {
	// Succeeded Job path that previously surfaced "harness completed without a
	// persisted reply": structured tool noise plus a trailing plain answer.
	raw := `{"type":"tool_result","response":"tool text"}` + "\n" + "Hello from Grok after tools\n"
	got, err := extractChatReply(agents.AgentRunHarnessBackendGrokBuild, raw)
	if err != nil || got != "Hello from Grok after tools" {
		t.Fatalf("mixed grok: %q %v", got, err)
	}
	msg := `{"type":"message_end","message":{"role":"assistant","stopReason":"end_turn","content":[{"type":"text","text":"Message end reply"}]}}`
	got, err = extractChatReply(agents.AgentRunHarnessBackendGrokBuild, msg)
	if err != nil || got != "Message end reply" {
		t.Fatalf("message_end grok: %q %v", got, err)
	}
}
