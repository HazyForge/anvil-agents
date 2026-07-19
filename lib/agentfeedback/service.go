package agentfeedback

import (
	"context"
	"errors"
	"strings"
	"time"
)

var ErrUnsupportedTransport = errors.New("unsupported feedback transport")

type Question struct {
	Prompt         string
	Context        string
	RunName        string
	Timeout        time.Duration
	PollInterval   time.Duration
	AllowedUserIDs []string
	AcceptAnyAfter bool
}

type Response struct {
	Text           string `json:"text"`
	AuthorID       string `json:"authorId,omitempty"`
	AuthorUsername string `json:"authorUsername,omitempty"`
	MessageID      string `json:"messageId,omitempty"`
	ChannelID      string `json:"channelId,omitempty"`
}

type Transport interface {
	Ask(ctx context.Context, question Question) (Response, error)
}

type Service struct {
	Transport Transport
}

func (s Service) Ask(ctx context.Context, question Question) (Response, error) {
	if s.Transport == nil {
		return Response{}, ErrUnsupportedTransport
	}
	question.Prompt = strings.TrimSpace(question.Prompt)
	question.Context = strings.TrimSpace(question.Context)
	question.RunName = strings.TrimSpace(question.RunName)
	if question.Prompt == "" {
		return Response{}, errors.New("question prompt is required")
	}
	if question.PollInterval <= 0 {
		question.PollInterval = 5 * time.Second
	}
	return s.Transport.Ask(ctx, question)
}

func FormatQuestionMessage(question Question) string {
	var builder strings.Builder
	builder.WriteString("**Anvil AgentRun needs operator feedback**\n\n")
	if question.RunName != "" {
		builder.WriteString("AgentRun: `")
		builder.WriteString(question.RunName)
		builder.WriteString("`\n\n")
	}
	builder.WriteString("Question:\n")
	builder.WriteString(question.Prompt)
	if question.Context != "" {
		builder.WriteString("\n\nContext:\n")
		builder.WriteString(question.Context)
	}
	builder.WriteString("\n\nPlease reply directly to this message. The agent is waiting for an authorized operator reply.")
	return limitRunes(builder.String(), 1900)
}

func limitRunes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	suffix := "\n\n[truncated by anvil-agent-feedback; ask a narrower follow-up if needed]"
	suffixRunes := []rune(suffix)
	if len(suffixRunes) >= limit {
		return string(runes[:limit])
	}
	return strings.TrimSpace(string(runes[:limit-len(suffixRunes)])) + suffix
}
