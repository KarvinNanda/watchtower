package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

const (
	// pollingHTTPTimeout must exceed Telegram's own long-poll window
	// (pollingTimeoutSecs) — otherwise the HTTP client aborts every single
	// poll before Telegram ever gets a chance to respond.
	pollingHTTPTimeout  = 35 * time.Second
	pollingTimeoutSecs  = 30
	pollingErrorBackoff = 3 * time.Second
)

// pollingHTTPClient is dedicated to long-polling getUpdates requests,
// separate from BotHandler's regular 10-second reply-timeout client.
var pollingHTTPClient = &http.Client{Timeout: pollingHTTPTimeout}

// PollingResponse is Telegram's getUpdates response envelope.
type PollingResponse struct {
	OK     bool             `json:"ok"`
	Result []TelegramUpdate `json:"result"`
}

// StartPolling long-polls Telegram's getUpdates endpoint for botType
// ("asset" or "sentinel"), dispatching every update to processUpdate, until
// ctx is cancelled — at which point it returns nil, allowing the caller to
// shut down gracefully.
func (h *BotHandler) StartPolling(ctx context.Context, botType string) error {
	token, err := h.tokenFor(botType)
	if err != nil {
		return err
	}

	log.Printf("[INFO] StartPolling: starting long-polling for %s bot", botType)

	var lastUpdateID int64

	for {
		select {
		case <-ctx.Done():
			log.Printf("[INFO] StartPolling: context cancelled, stopping %s bot polling", botType)
			return nil
		default:
		}

		updates, err := h.getUpdates(ctx, token, lastUpdateID+1)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			log.Printf("[ERROR] StartPolling: get updates for %s bot failed: %v", botType, err)

			select {
			case <-ctx.Done():
				return nil
			case <-time.After(pollingErrorBackoff):
			}
			continue
		}

		for _, update := range updates {
			if update.UpdateID > lastUpdateID {
				lastUpdateID = update.UpdateID
			}
			if procErr := h.processUpdate(botType, update); procErr != nil {
				log.Printf("[ERROR] StartPolling: process update %d for %s bot failed: %v", update.UpdateID, botType, procErr)
			}
		}
	}
}

// getUpdates fetches the next batch of updates starting at offset, long-
// polling for up to pollingTimeoutSecs. The request is bound to ctx so a
// shutdown signal aborts an in-flight poll immediately instead of waiting
// out the full timeout.
func (h *BotHandler) getUpdates(ctx context.Context, token string, offset int64) ([]TelegramUpdate, error) {
	endpoint := fmt.Sprintf("%s%s/getUpdates?offset=%d&timeout=%d", telegramAPIBase, token, offset, pollingTimeoutSecs)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build getUpdates request: %w", err)
	}

	resp, err := pollingHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do getUpdates request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read getUpdates response: %w", err)
	}

	var parsed PollingResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("decode getUpdates response: %w", err)
	}

	if !parsed.OK {
		return nil, fmt.Errorf("telegram getUpdates returned ok=false")
	}

	return parsed.Result, nil
}
