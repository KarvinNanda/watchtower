// Package notifier delivers alerts to users via per-purpose Telegram bots
// and records every delivery attempt in notification_logs.
package notifier

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/karvin-nanda/watchtower/internal/telegram"
	"github.com/karvin-nanda/watchtower/internal/utils"
)

// Type mirrors the notification_logs.notif_type enum.
type Type string

const (
	TypeAsset    Type = "asset"
	TypeSentinel Type = "sentinel"
)

// Status mirrors the notification_logs.status enum.
type Status string

const (
	StatusSent   Status = "sent"
	StatusFailed Status = "failed"
)

const (
	telegramAPIBase  = "https://api.telegram.org/bot"
	sendRetryBackoff = 500 * time.Millisecond
)

// Notifier sends alert messages through WatchTower's two Telegram bots: one
// for asset price alerts, one for sentinel threat-intel alerts.
type Notifier struct {
	assetBotToken    string
	sentinelBotToken string
	httpClient       *http.Client
}

// NewNotifier builds a Notifier for the given asset/sentinel bot tokens,
// with the standard hardened HTTP client (see utils.NewHTTPClient).
func NewNotifier(assetToken, sentinelToken string) *Notifier {
	return &Notifier{
		assetBotToken:    assetToken,
		sentinelBotToken: sentinelToken,
		httpClient:       utils.NewHTTPClient(),
	}
}

// EscapeTelegramMarkdown escapes every MarkdownV2 special character in
// text. Without this, dynamic content containing stray underscores,
// asterisks, or other formatting characters (e.g. "wp_ajax_nopriv_..." in a
// CVE description) breaks Telegram's entity parser with errors like
// "can't parse entities: Can't find end of the entity starting at byte
// offset N". Callers must run every dynamically-inserted field (titles,
// descriptions, URLs, AI-generated analysis text, timestamps, etc.)
// through this before interpolating it into a message — literal formatting
// characters in the message template itself (e.g. the `*` around a bold
// header) must NOT be escaped, only the dynamic values.
//
// The actual implementation lives in internal/telegram (shared with the
// bot /start handler); this is a thin re-export so existing callers in
// internal/scheduler don't need to import internal/telegram directly.
func EscapeTelegramMarkdown(text string) string {
	return telegram.EscapeTelegramMarkdown(text)
}

type telegramSendMessageRequest struct {
	ChatID    int64  `json:"chat_id"`
	Text      string `json:"text"`
	ParseMode string `json:"parse_mode"`
}

type telegramResponse struct {
	OK          bool   `json:"ok"`
	Description string `json:"description"`
}

// SendAssetAlert delivers message to chatID via the asset alert bot.
func (n *Notifier) SendAssetAlert(chatID int64, message string) error {
	if err := n.send(n.assetBotToken, chatID, message); err != nil {
		return fmt.Errorf("notifier: send asset alert: %w", err)
	}
	return nil
}

// SendSentinelAlert delivers message to chatID via the sentinel alert bot.
func (n *Notifier) SendSentinelAlert(chatID int64, message string) error {
	if err := n.send(n.sentinelBotToken, chatID, message); err != nil {
		return fmt.Errorf("notifier: send sentinel alert: %w", err)
	}
	return nil
}

// send delivers message via botToken's sendMessage endpoint, retrying
// exactly once if the first attempt times out.
func (n *Notifier) send(botToken string, chatID int64, message string) error {
	err := n.sendOnce(botToken, chatID, message)
	if err != nil && isTimeout(err) {
		time.Sleep(sendRetryBackoff)
		err = n.sendOnce(botToken, chatID, message)
	}
	return err
}

func (n *Notifier) sendOnce(botToken string, chatID int64, message string) error {
	endpoint := telegramAPIBase + botToken + "/sendMessage"

	body, err := json.Marshal(telegramSendMessageRequest{
		ChatID:    chatID,
		Text:      message,
		ParseMode: "MarkdownV2",
	})
	if err != nil {
		return fmt.Errorf("marshal telegram request: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build telegram request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := n.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send telegram message: %w", err)
	}
	defer resp.Body.Close()

	var parsed telegramResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return fmt.Errorf("decode telegram response: %w", err)
	}

	if !parsed.OK {
		return fmt.Errorf("telegram api error: %s", parsed.Description)
	}

	return nil
}

// isTimeout reports whether err represents a network timeout (e.g. the
// http.Client's configured timeout was exceeded).
func isTimeout(err error) bool {
	var timeoutErr interface{ Timeout() bool }
	if errors.As(err, &timeoutErr) {
		return timeoutErr.Timeout()
	}
	return false
}

// LogRepository persists notification delivery attempts to notification_logs.
type LogRepository struct {
	db *sql.DB
}

// NewLogRepository builds a LogRepository backed by db.
func NewLogRepository(db *sql.DB) *LogRepository {
	return &LogRepository{db: db}
}

// LogEntry captures the fields of a delivered (or failed) notification.
type LogEntry struct {
	UserID         uint64
	NotifType      Type
	AssetSymbol    *string
	Keyword        *string
	ContentSummary string
	Status         Status
}

// Record inserts a notification_logs row for entry.
func (r *LogRepository) Record(ctx context.Context, entry LogEntry) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO notification_logs (user_id, notif_type, asset_symbol, keyword, content_summary, status)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		entry.UserID, entry.NotifType, entry.AssetSymbol, entry.Keyword, entry.ContentSummary, entry.Status,
	)
	if err != nil {
		return fmt.Errorf("notifier: record log: %w", err)
	}
	return nil
}
