// Package telegram handles inbound Telegram bot interactions for
// WatchTower's two bots (asset alerts and sentinel threat-intel alerts):
// replying to /start with the sender's chat ID so they can register it in
// the WatchTower dashboard themselves. It does not persist any chat ID —
// that's entered manually by the user in the dashboard.
package telegram

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

const (
	telegramAPIBase    = "https://api.telegram.org/bot"
	handlerHTTPTimeout = 10 * time.Second
)

// markdownV2SpecialChars replaces every character Telegram's MarkdownV2
// parse mode treats as a formatting entity with its escaped form.
var markdownV2SpecialChars = strings.NewReplacer(
	"_", "\\_",
	"*", "\\*",
	"[", "\\[",
	"]", "\\]",
	"(", "\\(",
	")", "\\)",
	"~", "\\~",
	"`", "\\`",
	">", "\\>",
	"#", "\\#",
	"+", "\\+",
	"-", "\\-",
	"=", "\\=",
	"|", "\\|",
	"{", "\\{",
	"}", "\\}",
	".", "\\.",
	"!", "\\!",
)

// EscapeTelegramMarkdown escapes every MarkdownV2 special character in
// text so it can be safely interpolated into a MarkdownV2 message without
// breaking Telegram's entity parser (e.g. a raw "_" or unmatched "*" in
// user-supplied or fetched text otherwise causes Telegram to reject the
// message with "can't parse entities").
func EscapeTelegramMarkdown(text string) string {
	return markdownV2SpecialChars.Replace(text)
}

// BotHandler handles incoming Telegram updates for WatchTower's asset and
// sentinel bots.
type BotHandler struct {
	assetToken    string
	sentinelToken string
	httpClient    *http.Client
}

// NewBotHandler builds a BotHandler for the given bot tokens, with a
// 10-second HTTP timeout used for replies. Long-polling uses its own
// longer-lived client — see StartPolling in polling.go.
func NewBotHandler(assetToken, sentinelToken string) *BotHandler {
	return &BotHandler{
		assetToken:    assetToken,
		sentinelToken: sentinelToken,
		httpClient:    &http.Client{Timeout: handlerHTTPTimeout},
	}
}

// tokenFor resolves botType ("asset" or "sentinel") to its bot token.
func (h *BotHandler) tokenFor(botType string) (string, error) {
	switch botType {
	case "asset":
		return h.assetToken, nil
	case "sentinel":
		return h.sentinelToken, nil
	default:
		return "", fmt.Errorf("telegram: unknown bot type %q", botType)
	}
}

// TelegramUpdate is the subset of Telegram's Update object WatchTower
// needs to handle /start messages.
type TelegramUpdate struct {
	UpdateID int64 `json:"update_id"`
	Message  *struct {
		MessageID int64 `json:"message_id"`
		From      *struct {
			ID        int64  `json:"id"`
			FirstName string `json:"first_name"`
			Username  string `json:"username"`
		} `json:"from"`
		Chat *struct {
			ID   int64  `json:"id"`
			Type string `json:"type"`
		} `json:"chat"`
		Text string `json:"text"`
	} `json:"message"`
}

// processUpdate handles a single Telegram update for botType ("asset" or
// "sentinel"). It never panics — any recovered panic is logged and
// returned as an error. Only "/start" is handled; anything else (including
// updates with no message) is a no-op.
func (h *BotHandler) processUpdate(botType string, update TelegramUpdate) (err error) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[ERROR] processUpdate: recovered from panic for %s bot: %v", botType, r)
			err = fmt.Errorf("recovered from panic: %v", r)
		}
	}()

	if update.Message == nil || update.Message.Text != "/start" || update.Message.Chat == nil {
		return nil
	}

	chatID := update.Message.Chat.ID
	firstName := "there"
	if update.Message.From != nil && update.Message.From.FirstName != "" {
		firstName = update.Message.From.FirstName
	}

	message := buildWelcomeMessage(botType, firstName, chatID)

	token, err := h.tokenFor(botType)
	if err != nil {
		return err
	}

	if err := h.sendMessage(token, chatID, message); err != nil {
		log.Printf("[ERROR] processUpdate: reply to %s (%d) via %s bot failed: %v", firstName, chatID, botType, err)
		return nil
	}

	log.Printf("[INFO] /start from %s (%d) via %s bot", firstName, chatID, botType)

	return nil
}

// buildWelcomeMessage builds the /start reply for botType. firstName is
// the only truly dynamic (user-supplied) field, so it's the only one run
// through EscapeTelegramMarkdown — the rest of the template's punctuation
// is pre-escaped as literal MarkdownV2 source (e.g. "\\." for a literal
// period), and the bold/code markers (*, `) are intentional formatting.
func buildWelcomeMessage(botType, firstName string, chatID int64) string {
	name := EscapeTelegramMarkdown(firstName)

	if botType == "sentinel" {
		return fmt.Sprintf(
			"👋 Halo %s\\!\n\n"+
				"Chat ID kamu: `%d`\n\n"+
				"📋 *Langkah selanjutnya:*\n"+
				"1\\. Copy angka di atas\n"+
				"2\\. Buka dashboard WatchTower\n"+
				"3\\. Paste di bagian *Sentinel Setup*\n\n"+
				"✅ Setelah itu kamu akan menerima\n"+
				"alert keamanan otomatis\\!",
			name, chatID,
		)
	}

	return fmt.Sprintf(
		"👋 Halo %s\\!\n\n"+
			"Chat ID kamu: `%d`\n\n"+
			"📋 *Langkah selanjutnya:*\n"+
			"1\\. Copy angka di atas\n"+
			"2\\. Buka dashboard WatchTower\n"+
			"3\\. Paste di bagian *Asset Alert Setup*\n\n"+
			"✅ Setelah itu kamu akan menerima\n"+
			"alert harga aset otomatis\\!",
		name, chatID,
	)
}

type telegramSendMessageRequest struct {
	ChatID    int64  `json:"chat_id"`
	Text      string `json:"text"`
	ParseMode string `json:"parse_mode"`
}

type telegramAPIResponse struct {
	OK          bool   `json:"ok"`
	Description string `json:"description"`
}

// sendMessage delivers text to chatID via the Telegram Bot API using the
// given bot token.
func (h *BotHandler) sendMessage(token string, chatID int64, text string) error {
	endpoint := telegramAPIBase + token + "/sendMessage"

	body, err := json.Marshal(telegramSendMessageRequest{
		ChatID:    chatID,
		Text:      text,
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

	resp, err := h.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send telegram message: %w", err)
	}
	defer resp.Body.Close()

	var parsed telegramAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return fmt.Errorf("decode telegram response: %w", err)
	}

	if !parsed.OK {
		return fmt.Errorf("telegram api error: %s", parsed.Description)
	}

	return nil
}
