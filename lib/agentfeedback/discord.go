package agentfeedback

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const defaultDiscordAPIBaseURL = "https://discord.com/api/v10"

type DiscordConfig struct {
	BotToken       string
	ChannelID      string
	APIBaseURL     string
	UserAgent      string
	HTTPClient     *http.Client
	AcceptAnyAfter bool
	AllowAnyUser   bool
}

type DiscordAdapter struct {
	config DiscordConfig
	client *http.Client
}

func NewDiscordAdapter(config DiscordConfig) (*DiscordAdapter, error) {
	config.BotToken = strings.TrimSpace(config.BotToken)
	config.ChannelID = strings.TrimSpace(config.ChannelID)
	config.APIBaseURL = strings.TrimRight(strings.TrimSpace(config.APIBaseURL), "/")
	config.UserAgent = strings.TrimSpace(config.UserAgent)
	if config.BotToken == "" {
		return nil, errors.New("discord bot token is required")
	}
	if config.ChannelID == "" {
		return nil, errors.New("discord channel id is required")
	}
	if config.APIBaseURL == "" {
		config.APIBaseURL = defaultDiscordAPIBaseURL
	}
	if config.UserAgent == "" {
		config.UserAgent = "anvil-agent-feedback/0"
	}
	client := config.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	return &DiscordAdapter{config: config, client: client}, nil
}

func (a *DiscordAdapter) Ask(ctx context.Context, question Question) (Response, error) {
	if len(normalizedDiscordUserIDs(question.AllowedUserIDs)) == 0 && !a.config.AllowAnyUser {
		return Response{}, errors.New("at least one allowed Discord user id is required unless allow-any-user is explicitly enabled")
	}
	if question.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, question.Timeout)
		defer cancel()
	}
	prompt, err := a.createMessage(ctx, FormatQuestionMessage(question))
	if err != nil {
		return Response{}, err
	}
	return a.waitForReply(ctx, prompt.ID, question)
}

func (a *DiscordAdapter) createMessage(ctx context.Context, content string) (discordMessage, error) {
	var message discordMessage
	request := discordCreateMessageRequest{
		Content: content,
		AllowedMentions: discordAllowedMentions{
			Parse: []string{},
		},
	}
	if err := a.doJSON(ctx, http.MethodPost, "/channels/"+url.PathEscape(a.config.ChannelID)+"/messages", nil, request, &message); err != nil {
		return discordMessage{}, fmt.Errorf("post discord question: %w", err)
	}
	if strings.TrimSpace(message.ID) == "" {
		return discordMessage{}, errors.New("discord create message response did not include message id")
	}
	return message, nil
}

func (a *DiscordAdapter) waitForReply(ctx context.Context, questionMessageID string, question Question) (Response, error) {
	pollInterval := question.PollInterval
	if pollInterval <= 0 {
		pollInterval = 5 * time.Second
	}
	acceptAnyAfter := question.AcceptAnyAfter || a.config.AcceptAnyAfter
	for {
		messages, err := a.channelMessagesAfter(ctx, questionMessageID)
		if err != nil {
			return Response{}, err
		}
		if message, ok := selectDiscordReply(messages, questionMessageID, a.config.ChannelID, question.AllowedUserIDs, a.config.AllowAnyUser, acceptAnyAfter); ok {
			return Response{
				Text:           strings.TrimSpace(message.Content),
				AuthorID:       message.Author.ID,
				AuthorUsername: message.Author.Username,
				MessageID:      message.ID,
				ChannelID:      message.ChannelID,
			}, nil
		}
		timer := time.NewTimer(pollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return Response{}, fmt.Errorf("wait for discord reply: %w", ctx.Err())
		case <-timer.C:
		}
	}
}

func (a *DiscordAdapter) channelMessagesAfter(ctx context.Context, messageID string) ([]discordMessage, error) {
	values := url.Values{}
	values.Set("after", messageID)
	values.Set("limit", "50")
	var messages []discordMessage
	if err := a.doJSON(ctx, http.MethodGet, "/channels/"+url.PathEscape(a.config.ChannelID)+"/messages", values, nil, &messages); err != nil {
		return nil, fmt.Errorf("poll discord replies: %w", err)
	}
	return messages, nil
}

func normalizedDiscordUserIDs(allowedUserIDs []string) map[string]struct{} {
	allowed := map[string]struct{}{}
	for _, id := range allowedUserIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			allowed[id] = struct{}{}
		}
	}
	return allowed
}

func selectDiscordReply(messages []discordMessage, questionMessageID, channelID string, allowedUserIDs []string, allowAnyUser, acceptAnyAfter bool) (discordMessage, bool) {
	allowed := normalizedDiscordUserIDs(allowedUserIDs)
	if len(allowed) == 0 && !allowAnyUser {
		return discordMessage{}, false
	}
	for i := len(messages) - 1; i >= 0; i-- {
		message := messages[i]
		if message.Author.Bot || strings.TrimSpace(message.Content) == "" {
			continue
		}
		if len(allowed) > 0 {
			if _, ok := allowed[message.Author.ID]; !ok {
				continue
			}
		}
		if !acceptAnyAfter && !discordMessageReferences(message, questionMessageID, channelID) {
			continue
		}
		return message, true
	}
	return discordMessage{}, false
}

func discordMessageReferences(message discordMessage, questionMessageID, channelID string) bool {
	if message.MessageReference == nil {
		return false
	}
	if message.MessageReference.MessageID != questionMessageID {
		return false
	}
	return message.MessageReference.ChannelID == "" || message.MessageReference.ChannelID == channelID
}

func (a *DiscordAdapter) doJSON(ctx context.Context, method, path string, query url.Values, in any, out any) error {
	for attempt := 0; ; attempt++ {
		var body io.Reader
		if in != nil {
			payload, err := json.Marshal(in)
			if err != nil {
				return err
			}
			body = bytes.NewReader(payload)
		}
		endpoint := a.config.APIBaseURL + path
		if len(query) > 0 {
			endpoint += "?" + query.Encode()
		}
		request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
		if err != nil {
			return err
		}
		request.Header.Set("Authorization", "Bot "+a.config.BotToken)
		request.Header.Set("User-Agent", a.config.UserAgent)
		if in != nil {
			request.Header.Set("Content-Type", "application/json")
		}
		response, err := a.client.Do(request)
		if err != nil {
			return err
		}
		if response.StatusCode == http.StatusTooManyRequests && attempt < 5 {
			delay := discordRetryDelay(response.Body, response.Header.Get("Retry-After"))
			response.Body.Close()
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
				continue
			}
		}
		defer response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode > 299 {
			body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
			return fmt.Errorf("discord API %s %s returned %s: %s", method, path, response.Status, strings.TrimSpace(string(body)))
		}
		if out == nil {
			return nil
		}
		if err := json.NewDecoder(response.Body).Decode(out); err != nil {
			return err
		}
		return nil
	}
}

func discordRetryDelay(body io.Reader, retryAfterHeader string) time.Duration {
	var payload struct {
		RetryAfter float64 `json:"retry_after"`
	}
	if err := json.NewDecoder(body).Decode(&payload); err == nil && payload.RetryAfter > 0 {
		return time.Duration(payload.RetryAfter * float64(time.Second))
	}
	if retryAfterHeader != "" {
		if delay, err := time.ParseDuration(retryAfterHeader + "s"); err == nil {
			return delay
		}
	}
	return time.Second
}

type discordCreateMessageRequest struct {
	Content         string                 `json:"content"`
	AllowedMentions discordAllowedMentions `json:"allowed_mentions"`
}

type discordAllowedMentions struct {
	Parse []string `json:"parse"`
}

type discordMessage struct {
	ID               string                   `json:"id"`
	ChannelID        string                   `json:"channel_id"`
	Content          string                   `json:"content"`
	Author           discordUser              `json:"author"`
	MessageReference *discordMessageReference `json:"message_reference"`
}

type discordUser struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Bot      bool   `json:"bot"`
}

type discordMessageReference struct {
	MessageID string `json:"message_id"`
	ChannelID string `json:"channel_id"`
}
