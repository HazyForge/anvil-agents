package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunHelpReturnsSuccess(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := run([]string{"ask", "--help"}, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("run help returned error: %v", err)
	}
	if !strings.Contains(stderr.String(), "Usage of anvil-agent-feedback ask:") {
		t.Fatalf("help output = %q, want usage", stderr.String())
	}
}

func TestRunHelpDoesNotPrintEnvBackedDiscordCredentials(t *testing.T) {
	t.Setenv("ANVIL_AGENT_FEEDBACK_DISCORD_BOT_TOKEN", "secret-bot-token")
	t.Setenv("ANVIL_AGENT_FEEDBACK_DISCORD_CHANNEL_ID", "secret-channel-id")
	t.Setenv("ANVIL_AGENT_FEEDBACK_DISCORD_API_BASE_URL", "https://discord.example.invalid/api")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := run([]string{"ask", "--help"}, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("run help returned error: %v", err)
	}
	help := stderr.String()
	for _, secret := range []string{"secret-bot-token", "secret-channel-id", "discord.example.invalid"} {
		if strings.Contains(help, secret) {
			t.Fatalf("help output contains env-backed Discord config %q: %q", secret, help)
		}
	}
}

func TestRunRequiresQuestion(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := run([]string{"ask"}, strings.NewReader(""), &stdout, &stderr)
	if err == nil {
		t.Fatal("run without a question returned nil error")
	}
	if !strings.Contains(err.Error(), "question is required") {
		t.Fatalf("error = %q, want question requirement", err)
	}
}
