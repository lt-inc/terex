// Package telegram is a minimal Bot API client: verify a channel, send a post.
// Bot tokens are part of the request URL, so errors are rebuilt without it.
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
	"strconv"
	"strings"
	"time"

	"terex/news-collector/internal/metrics"
)

type Client struct {
	Base string // default https://api.telegram.org
	HTTP *http.Client
}

func New(base string) *Client {
	if base == "" {
		base = "https://api.telegram.org"
	}
	return &Client{Base: strings.TrimRight(base, "/"), HTTP: &http.Client{Timeout: 30 * time.Second}}
}

// Error is a Bot API failure. RetryAfter is set for flood-control (HTTP 429).
type Error struct {
	Code       int
	Message    string
	RetryAfter time.Duration
}

func (e *Error) Error() string { return fmt.Sprintf("telegram: %s (%d)", e.Message, e.Code) }

// Permanent reports whether retrying cannot help (bad token, kicked bot, bad chat).
func (e *Error) Permanent() bool {
	return e.Code == 400 || e.Code == 401 || e.Code == 403 || e.Code == 404
}

var tokenPattern = regexp.MustCompile(`^\d{5,}:[A-Za-z0-9_-]{30,}$`)

func ValidToken(t string) bool { return tokenPattern.MatchString(t) }

func (c *Client) call(ctx context.Context, token, method string, params map[string]any, out any) (err error) {
	started := time.Now()
	defer func() {
		result := "ok"
		var tg *Error
		switch {
		case errors.As(err, &tg) && tg.Code == 429:
			result = "rate_limited"
		case err != nil:
			result = "error"
		}
		metrics.TelegramRequests.WithLabelValues(method, result).Inc()
		metrics.TelegramDuration.WithLabelValues(method).Observe(time.Since(started).Seconds())
	}()
	body, _ := json.Marshal(params)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Base+"/bot"+token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return errors.New("telegram: invalid request")
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.HTTP.Do(req)
	if err != nil {
		if ue, ok := err.(*url.Error); ok { // the wrapped URL contains the token
			err = ue.Err
		}
		return fmt.Errorf("telegram unavailable: %w", err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	var env struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		ErrorCode   int             `json:"error_code"`
		Description string          `json:"description"`
		Parameters  struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	if err = json.Unmarshal(data, &env); err != nil {
		return &Error{Code: res.StatusCode, Message: "unexpected response"}
	}
	if !env.OK {
		code := env.ErrorCode
		if code == 0 {
			code = res.StatusCode
		}
		return &Error{Code: code, Message: env.Description, RetryAfter: time.Duration(env.Parameters.RetryAfter) * time.Second}
	}
	if out != nil {
		return json.Unmarshal(env.Result, out)
	}
	return nil
}

type Me struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

func (c *Client) GetMe(ctx context.Context, token string) (Me, error) {
	var me Me
	err := c.call(ctx, token, "getMe", map[string]any{}, &me)
	return me, err
}

type Chat struct {
	ID    int64  `json:"id"`
	Type  string `json:"type"`
	Title string `json:"title"`
}

func (c *Client) GetChat(ctx context.Context, token, chatID string) (Chat, error) {
	var chat Chat
	err := c.call(ctx, token, "getChat", map[string]any{"chat_id": ChatParam(chatID)}, &chat)
	return chat, err
}

type Member struct {
	Status         string `json:"status"`
	CanPostMessage *bool  `json:"can_post_messages"`
}

func (c *Client) GetMember(ctx context.Context, token, chatID string, userID int64) (Member, error) {
	var m Member
	err := c.call(ctx, token, "getChatMember", map[string]any{"chat_id": ChatParam(chatID), "user_id": userID}, &m)
	return m, err
}

// SendMessage posts HTML text and returns the message id.
func (c *Client) SendMessage(ctx context.Context, token, chatID, htmlText string, preview bool) (int64, error) {
	var msg struct {
		MessageID int64 `json:"message_id"`
	}
	err := c.call(ctx, token, "sendMessage", map[string]any{
		"chat_id":                     ChatParam(chatID),
		"text":                        htmlText,
		"parse_mode":                  "HTML",
		"link_preview_options":        map[string]any{"is_disabled": !preview},
		"disable_notification":        false,
		"protect_content":             false,
		"allow_sending_without_reply": true,
	}, &msg)
	return msg.MessageID, err
}

// ChatParam turns a stored chat id into the JSON value Telegram expects.
func ChatParam(chatID string) any {
	if n, err := strconv.ParseInt(chatID, 10, 64); err == nil {
		return n
	}
	return chatID
}

var chatUsername = regexp.MustCompile(`^@[A-Za-z][A-Za-z0-9_]{3,31}$`)
var chatNumeric = regexp.MustCompile(`^-?\d{5,20}$`)

// NormalizeChat accepts @name, a t.me link, or a numeric id and returns the canonical form.
func NormalizeChat(input string) (string, bool) {
	s := strings.TrimSpace(input)
	for _, prefix := range []string{"https://t.me/", "http://t.me/", "t.me/", "https://telegram.me/"} {
		if strings.HasPrefix(strings.ToLower(s), prefix) {
			s = "@" + strings.TrimPrefix(s[len(prefix):], "@")
			if i := strings.IndexAny(s, "/?"); i > 0 {
				s = s[:i]
			}
			break
		}
	}
	if chatNumeric.MatchString(s) {
		return s, true
	}
	if !strings.HasPrefix(s, "@") && s != "" {
		s = "@" + s
	}
	if chatUsername.MatchString(s) {
		return s, true
	}
	return "", false
}
