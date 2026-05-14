// Package notify provides alerting and notification utilities for the MM bot.
// It supports sending messages via Telegram, arbitrary webhooks, or multiple
// channels simultaneously.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Notifier sends alert messages to an external channel.
type Notifier interface {
	Send(ctx context.Context, msg string) error
}

// ── Telegram ──────────────────────────────────────────────────────────────────

// Telegram sends messages via the Telegram Bot API (stdlib HTTP only).
// token is the bot token (from BotFather), chatID is the target chat.
type Telegram struct {
	Token  string
	ChatID string
	http   *http.Client
}

// NewTelegram creates a Telegram notifier with a default HTTP client.
func NewTelegram(token, chatID string) *Telegram {
	return &Telegram{
		Token:  token,
		ChatID: chatID,
		http:   &http.Client{Timeout: 10 * time.Second},
	}
}

// Send posts a message to the Telegram Bot API.
// POST https://api.telegram.org/bot{token}/sendMessage
// body: {"chat_id": chatID, "text": msg, "parse_mode": "HTML"}
func (t *Telegram) Send(ctx context.Context, msg string) error {
	payload := map[string]string{
		"chat_id":    t.ChatID,
		"text":       msg,
		"parse_mode": "HTML",
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("telegram: marshal: %w", err)
	}

	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", t.Token)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("telegram: request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.http.Do(req)
	if err != nil {
		return fmt.Errorf("telegram: http: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("telegram: unexpected status %d", resp.StatusCode)
	}
	return nil
}

// ── Webhook ───────────────────────────────────────────────────────────────────

// Webhook sends a JSON POST to a URL.
type Webhook struct {
	URL  string
	http *http.Client
}

// NewWebhook creates a Webhook notifier with a default HTTP client.
func NewWebhook(url string) *Webhook {
	return &Webhook{
		URL:  url,
		http: &http.Client{Timeout: 10 * time.Second},
	}
}

// Send posts a JSON payload to the webhook URL.
// POST url body: {"text": msg, "timestamp": time.Now().UTC().Format(time.RFC3339)}
func (w *Webhook) Send(ctx context.Context, msg string) error {
	payload := map[string]string{
		"text":      msg,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("webhook: marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.URL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("webhook: request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := w.http.Do(req)
	if err != nil {
		return fmt.Errorf("webhook: http: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook: unexpected status %d", resp.StatusCode)
	}
	return nil
}

// ── Multi ─────────────────────────────────────────────────────────────────────

// Multi sends to multiple Notifiers; continues on error, returns last error.
type Multi struct{ notifiers []Notifier }

// NewMulti creates a Multi notifier that fans out to all provided Notifiers.
func NewMulti(notifiers ...Notifier) *Multi {
	return &Multi{notifiers: notifiers}
}

// Send delivers msg to every contained Notifier. It continues even if one
// fails, and returns the last error encountered (nil if all succeeded).
func (m *Multi) Send(ctx context.Context, msg string) error {
	var lastErr error
	for _, n := range m.notifiers {
		if err := n.Send(ctx, msg); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

// ── Noop ──────────────────────────────────────────────────────────────────────

// Noop is a no-op notifier (used when no notification is configured).
type Noop struct{}

// Send does nothing and always returns nil.
func (Noop) Send(_ context.Context, _ string) error { return nil }
