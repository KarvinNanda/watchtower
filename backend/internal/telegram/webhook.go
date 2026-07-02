package telegram

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
)

// WebhookHandler returns a Gin handler that processes incoming Telegram
// webhook updates for botType ("asset" or "sentinel"). It always responds
// 200 OK regardless of processing outcome — Telegram retries a webhook
// indefinitely on any non-2xx response, so a transient processing error
// must not cause Telegram to keep resending the same update forever.
func (h *BotHandler) WebhookHandler(botType string) gin.HandlerFunc {
	return func(c *gin.Context) {
		var update TelegramUpdate
		if err := json.NewDecoder(c.Request.Body).Decode(&update); err != nil {
			log.Printf("[ERROR] WebhookHandler: decode update for %s bot: %v", botType, err)
			c.Status(http.StatusOK)
			return
		}

		log.Printf("[INFO] WebhookHandler: received update %d for %s bot", update.UpdateID, botType)

		if err := h.processUpdate(botType, update); err != nil {
			log.Printf("[ERROR] WebhookHandler: process update %d for %s bot: %v", update.UpdateID, botType, err)
		}

		c.Status(http.StatusOK)
	}
}
