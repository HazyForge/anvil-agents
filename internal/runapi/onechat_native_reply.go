package runapi

import (
	"encoding/json"
	"fmt"
	"strings"

	agents "github.com/hazyforge/anvil-agents/api/v1alpha1"
)

// NativeReplyText shares the API's public reply extraction with local onechat
// sessions. It does not expose tool output as a substitute for a missing reply.
func NativeReplyText(harnessKind, stdout string) (string, error) {
	if harnessKind == "codex" {
		// Codex can emit progress agent_message items before its final reply.
		// Select the last item for onechat without changing the API extractor.
		last := ""
		for _, line := range strings.Split(stdout, "\n") {
			var event struct {
				Type string `json:"type"`
				Item struct {
					Type string `json:"type"`
				} `json:"item"`
			}
			if json.Unmarshal([]byte(line), &event) == nil && event.Type == "item.completed" && event.Item.Type == "agent_message" {
				last = line
			}
		}
		stdout = last
	}
	switch harnessKind {
	case "codex", "openCode", "grokBuild", "primeAgent", "agy":
		return extractChatReply(agents.AgentRunHarnessBackendKind(harnessKind), stdout)
	default:
		return "", fmt.Errorf("unsupported native reply harness %q", harnessKind)
	}
}
