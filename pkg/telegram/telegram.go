// Package telegram is the smallest Telegram Bot API client: sendMessage to a chat id. The bot token is a secret:
// it is part of the request URL, so no error this package returns carries the URL.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	defaultBaseURL = "https://api.telegram.org"
	// MaxMessageRunes is the Bot API limit of one message.
	MaxMessageRunes = 4096
)

var (
	// ErrDisabled is returned when no bot token is configured.
	ErrDisabled = errors.New("telegram is disabled: no bot token")
	// ErrForbidden: the bot may not write to the chat (the person blocked it, never pressed Start, or the bot
	// was removed from the group).
	ErrForbidden = errors.New("telegram: the bot is blocked or removed")
	// ErrChatNotFound: the chat id does not exist for this bot.
	ErrChatNotFound = errors.New("telegram: chat not found")
)

var reChatID = regexp.MustCompile(`^(?:-?\d{1,20}|@[A-Za-z0-9_]{5,32})$`)

// ValidChatID reports whether s can be a chat id: a number (negative for groups) or an @channel name.
func ValidChatID(s string) bool { return reChatID.MatchString(s) }

// Client sends messages as one bot. The zero token disables it.
type Client struct {
	token   string
	baseURL string
	http    *http.Client
}

// New builds a client; an empty token gives a disabled one.
func New(token string) *Client {
	return &Client{token: strings.TrimSpace(token), baseURL: defaultBaseURL, http: &http.Client{Timeout: 10 * time.Second}}
}

// WithBaseURL points the client at another API server (tests).
func (c *Client) WithBaseURL(base string) *Client {
	c.baseURL = strings.TrimRight(base, "/")
	return c
}

// Enabled reports whether a bot token is set.
func (c *Client) Enabled() bool { return c != nil && c.token != "" }

type apiResponse struct {
	OK          bool   `json:"ok"`
	Description string `json:"description"`
}

// Send writes text to the chat. Errors never include the request URL.
func (c *Client) Send(ctx context.Context, chatID, text string) error {
	if !c.Enabled() {
		return ErrDisabled
	}
	if r := []rune(text); len(r) > MaxMessageRunes {
		text = string(r[:MaxMessageRunes-1]) + "…"
	}
	body, err := json.Marshal(map[string]any{"chat_id": chatID, "text": text, "disable_web_page_preview": true})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/bot"+c.token+"/sendMessage", bytes.NewReader(body))
	if err != nil {
		return errors.New("telegram: build request")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err // the url.Error text carries the URL, and the URL carries the token
		}
		return fmt.Errorf("telegram: request failed: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var parsed apiResponse
	_ = json.Unmarshal(raw, &parsed)
	switch {
	case resp.StatusCode == http.StatusOK && parsed.OK:
		return nil
	case resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("%w: %s", ErrForbidden, parsed.Description)
	case resp.StatusCode == http.StatusBadRequest && strings.Contains(strings.ToLower(parsed.Description), "chat not found"):
		return ErrChatNotFound
	default:
		return fmt.Errorf("telegram: status %d: %s", resp.StatusCode, parsed.Description)
	}
}
