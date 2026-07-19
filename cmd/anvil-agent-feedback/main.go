package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/hazyforge/anvil-agents/lib/agentfeedback"
)

type stringList []string

func (s *stringList) String() string {
	return strings.Join(*s, ",")
}

func (s *stringList) Set(value string) error {
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			*s = append(*s, item)
		}
	}
	return nil
}

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "anvil-agent-feedback: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) > 0 && args[0] == "ask" {
		args = args[1:]
	}
	flags := flag.NewFlagSet("anvil-agent-feedback ask", flag.ContinueOnError)
	flags.SetOutput(stderr)

	var allowedUserIDs stringList
	question := flags.String("question", "", "question text to post")
	questionFile := flags.String("question-file", "", "file containing question text, or '-' for stdin")
	contextText := flags.String("context", "", "optional context text")
	contextFile := flags.String("context-file", "", "file containing context text, or '-' for stdin")
	runName := flags.String("run", envFirst("ANVIL_AGENT_RUN"), "AgentRun name for message context")
	transport := flags.String("transport", envFirstDefault("discord", "ANVIL_AGENT_FEEDBACK_TRANSPORT", "ANVIL_SOCIAL_TRANSPORT"), "feedback transport")
	timeoutRaw := flags.String("timeout", envFirstDefault("30m", "ANVIL_AGENT_FEEDBACK_TIMEOUT"), "maximum wait time; use 0 for no timeout")
	pollRaw := flags.String("poll-interval", envFirstDefault("5s", "ANVIL_AGENT_FEEDBACK_POLL_INTERVAL"), "poll interval")
	output := flags.String("output", envFirstDefault("text", "ANVIL_AGENT_FEEDBACK_OUTPUT"), "output format: text or json")
	acceptAnyAfter := flags.Bool("accept-any-after", envBool("ANVIL_AGENT_FEEDBACK_ACCEPT_ANY_AFTER", false), "accept first non-bot message after the question instead of requiring a direct reply")
	allowAnyUser := flags.Bool("allow-any-user", envBool("ANVIL_AGENT_FEEDBACK_ALLOW_ANY_USER", false), "allow replies from any non-bot channel member; disabled by default")
	flags.Var(&allowedUserIDs, "allowed-user-id", "allowed Discord user id; may be repeated or comma-separated")
	discordToken := flags.String("discord-token", "", "Discord bot token")
	discordChannelID := flags.String("discord-channel-id", "", "Discord channel id")
	discordAPIBaseURL := flags.String("discord-api-base-url", "", "Discord API base URL")

	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if envAllowed := envFirst("ANVIL_AGENT_FEEDBACK_ALLOWED_USER_IDS", "ANVIL_DISCORD_ALLOWED_USER_IDS"); envAllowed != "" {
		_ = allowedUserIDs.Set(envAllowed)
	}

	prompt, err := readTextInput(*question, *questionFile, stdin)
	if err != nil {
		return err
	}
	contextBody, err := readOptionalTextInput(*contextText, *contextFile, stdin)
	if err != nil {
		return err
	}
	timeout, err := parseDurationOrZero(*timeoutRaw)
	if err != nil {
		return fmt.Errorf("parse timeout: %w", err)
	}
	pollInterval, err := time.ParseDuration(*pollRaw)
	if err != nil {
		return fmt.Errorf("parse poll interval: %w", err)
	}

	discordTokenValue := envDefaultIfEmpty(*discordToken, "ANVIL_AGENT_FEEDBACK_DISCORD_BOT_TOKEN", "ANVIL_DISCORD_BOT_TOKEN", "DISCORD_BOT_TOKEN")
	discordChannelIDValue := envDefaultIfEmpty(*discordChannelID, "ANVIL_AGENT_FEEDBACK_DISCORD_CHANNEL_ID", "ANVIL_DISCORD_CHANNEL_ID", "DISCORD_CHANNEL_ID")
	discordAPIBaseURLValue := envDefaultIfEmpty(*discordAPIBaseURL, "ANVIL_AGENT_FEEDBACK_DISCORD_API_BASE_URL", "DISCORD_API_BASE_URL")

	adapter, err := buildTransport(*transport, discordTokenValue, discordChannelIDValue, discordAPIBaseURLValue, *acceptAnyAfter, *allowAnyUser)
	if err != nil {
		return err
	}
	service := agentfeedback.Service{Transport: adapter}
	response, err := service.Ask(context.Background(), agentfeedback.Question{
		Prompt:         prompt,
		Context:        contextBody,
		RunName:        *runName,
		Timeout:        timeout,
		PollInterval:   pollInterval,
		AllowedUserIDs: allowedUserIDs,
		AcceptAnyAfter: *acceptAnyAfter,
	})
	if err != nil {
		return err
	}

	switch strings.ToLower(strings.TrimSpace(*output)) {
	case "", "text":
		_, err = fmt.Fprintln(stdout, response.Text)
	case "json":
		encoder := json.NewEncoder(stdout)
		err = encoder.Encode(response)
	default:
		err = fmt.Errorf("unsupported output format %q", *output)
	}
	return err
}

func buildTransport(transport, discordToken, discordChannelID, discordAPIBaseURL string, acceptAnyAfter, allowAnyUser bool) (agentfeedback.Transport, error) {
	switch strings.ToLower(strings.TrimSpace(transport)) {
	case "", "discord":
		return agentfeedback.NewDiscordAdapter(agentfeedback.DiscordConfig{
			BotToken:       discordToken,
			ChannelID:      discordChannelID,
			APIBaseURL:     discordAPIBaseURL,
			AcceptAnyAfter: acceptAnyAfter,
			AllowAnyUser:   allowAnyUser,
		})
	default:
		return nil, fmt.Errorf("%w: %s", agentfeedback.ErrUnsupportedTransport, transport)
	}
}

func readTextInput(value, path string, stdin io.Reader) (string, error) {
	value = strings.TrimSpace(value)
	if value != "" {
		return value, nil
	}
	if strings.TrimSpace(path) != "" {
		body, err := readInputPath(path, stdin)
		return strings.TrimSpace(body), err
	}
	if file, ok := stdin.(*os.File); ok {
		info, err := file.Stat()
		if err == nil && info.Mode()&os.ModeCharDevice == 0 {
			body, err := io.ReadAll(stdin)
			return strings.TrimSpace(string(body)), err
		}
	}
	return "", fmt.Errorf("question is required; pass --question, --question-file, or pipe stdin")
}

func readOptionalTextInput(value, path string, stdin io.Reader) (string, error) {
	value = strings.TrimSpace(value)
	if value != "" {
		return value, nil
	}
	if strings.TrimSpace(path) == "" {
		return "", nil
	}
	body, err := readInputPath(path, stdin)
	return strings.TrimSpace(body), err
}

func readInputPath(path string, stdin io.Reader) (string, error) {
	path = strings.TrimSpace(path)
	if path == "-" {
		body, err := io.ReadAll(stdin)
		return string(body), err
	}
	body, err := os.ReadFile(path)
	return string(body), err
}

func parseDurationOrZero(value string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" || value == "0" {
		return 0, nil
	}
	return time.ParseDuration(value)
}

func envFirst(names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}
	return ""
}

func envFirstDefault(fallback string, names ...string) string {
	if value := envFirst(names...); value != "" {
		return value
	}
	return fallback
}

func envDefaultIfEmpty(value string, names ...string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return envFirst(names...)
}

func envBool(name string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}
