package runapi

import (
	"strings"
	"testing"

	agents "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

func TestPrimeNativeReplyUsesLastAssistantCompletion(t *testing.T) {
	raw := strings.Join([]string{
		`{"type":"message_end","message":{"role":"assistant","stopReason":"toolUse","content":[{"type":"text","text":"I will inspect it"},{"type":"toolCall","name":"ipython","arguments":{"code":"secret"}}]}}`,
		`{"type":"tool_execution_end","toolName":"ipython","result":{"content":[{"type":"text","text":"secret tool output"}]}}`,
		`{"type":"message_end","message":{"role":"toolResult","content":[{"type":"text","text":"secret tool output"}]}}`,
		`{"type":"message_update","message":{"role":"assistant","content":[{"type":"text","text":"partial"}]}}`,
		`{"type":"message_end","message":{"role":"assistant","stopReason":"stop","content":[{"type":"thinking","thinking":"secret reasoning"},{"type":"text","text":"Actual answer"},{"type":"text","text":"Second paragraph"}]}}`,
		`{"type":"agent_end","messages":[{"role":"assistant","content":[{"type":"text","text":"copied answer"}]}]}`,
	}, "\n")
	got, err := extractChatReply(agents.AgentRunHarnessBackendPrimeAgent, raw)
	if err != nil || got != "Actual answer\nSecond paragraph" {
		t.Fatalf("Prime reply = %q, %v", got, err)
	}
}

func TestPrimeNativeReplyRejectsNonRepliesAndTerminalErrors(t *testing.T) {
	for _, raw := range []string{
		"raw unframed private output",
		`{"type":"message_end","message":{"role":"user","content":[{"type":"text","text":"prompt"}]}}`,
		`{"type":"message_end","message":{"role":"toolResult","content":[{"type":"text","text":"secret"}]}}`,
		`{"type":"message_end","message":{"role":"assistant","stopReason":"toolUse","content":[{"type":"text","text":"tool preamble"}]}}`,
		`{"type":"message_end","message":{"role":"assistant","stopReason":"stop","content":[{"type":"thinking","thinking":"private"}]}}`,
		`{"type":"message_end","message":{"role":"assistant","stopReason":"stop","content":"not native blocks"}}`,
		`{"type":"message_end","message":{"role":"assistant","stopReason":"error","content":[{"type":"text","text":"failed partial"}],"errorMessage":"private credential"}}`,
		`{"type":"agent_end","messages":[{"role":"assistant","content":[{"type":"text","text":"not message_end"}]}]}`,
	} {
		if got, err := extractChatReply(agents.AgentRunHarnessBackendPrimeAgent, raw); err == nil || got != "" {
			t.Fatalf("non-reply accepted: %q, %v", got, err)
		}
	}
	good := `{"type":"message_end","message":{"role":"assistant","stopReason":"stop","content":[{"type":"text","text":"earlier"}]}}`
	failed := `{"type":"message_end","message":{"role":"assistant","stopReason":"aborted","content":[]}}`
	if _, err := extractChatReply(agents.AgentRunHarnessBackendPrimeAgent, good+"\n"+failed); err == nil {
		t.Fatal("terminal abort reused earlier answer")
	}
	if got, err := extractChatReply(agents.AgentRunHarnessBackendPrimeAgent, failed+"\n"+good); err != nil || got != "earlier" {
		t.Fatalf("successful retry lost: %q, %v", got, err)
	}
}
