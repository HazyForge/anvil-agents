package controller

import (
	_ "embed"
	"strings"
)

//go:embed prompts/agent-run-system.md
var agentRunSystemPromptMarkdown string

func agentRunSystemPrompt() string {
	return strings.TrimSpace(agentRunSystemPromptMarkdown)
}
