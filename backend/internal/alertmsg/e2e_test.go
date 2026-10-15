//go:build telegram_e2e

package alertmsg

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSendGoldensToTelegram sends every golden message to a real chat so a
// person can check it on a phone. Excluded from normal runs by the build
// tag. Needs TELEGRAM_E2E_BOT_TOKEN and TELEGRAM_E2E_CHAT_ID (a chat that
// has sent /start to that bot). The token never appears in test output.
func TestSendGoldensToTelegram(t *testing.T) {
	token := os.Getenv("TELEGRAM_E2E_BOT_TOKEN")
	chatID := os.Getenv("TELEGRAM_E2E_CHAT_ID")
	if token == "" || chatID == "" {
		t.Skip("set TELEGRAM_E2E_BOT_TOKEN and TELEGRAM_E2E_CHAT_ID")
	}
	redact := func(s string) string { return strings.ReplaceAll(s, token, "<token>") }

	paths, err := filepath.Glob("testdata/*.golden")
	require.NoError(t, err)
	require.NotEmpty(t, paths)
	client := &http.Client{Timeout: 15 * time.Second}

	for _, p := range paths {
		raw, err := os.ReadFile(p)
		require.NoError(t, err)
		for i, msg := range strings.Split(strings.TrimSuffix(string(raw), "\n"), goldenPartSeparator) {
			body, err := json.Marshal(map[string]any{"chat_id": chatID, "text": msg, "parse_mode": "MarkdownV2"})
			require.NoError(t, err)
			resp, err := client.Post("https://api.telegram.org/bot"+token+"/sendMessage", "application/json", bytes.NewReader(body))
			if err != nil {
				t.Fatalf("%s part %d: %s", filepath.Base(p), i+1, redact(err.Error()))
			}
			respBody, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			assert.Equal(t, http.StatusOK, resp.StatusCode, "%s part %d: %s", filepath.Base(p), i+1, redact(string(respBody)))
			time.Sleep(1100 * time.Millisecond) // Telegram: about 1 message/second per chat
		}
	}
}
