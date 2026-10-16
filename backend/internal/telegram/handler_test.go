package telegram

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// #81: in a group Telegram sends "/start@BotName" when the command is
// picked from the menu, and a privacy-mode bot only receives that form.
func TestIsStartCommand(t *testing.T) {
	for text, want := range map[string]bool{
		"/start":                true,
		"/start@watchtower_bot": true,
		"/start@":               false,
		"/start now":            false,
		"/start@bot extra":      false,
		"/stop":                 false,
		"/started":              false,
		"":                      false,
		"hello /start":          false,
	} {
		assert.Equal(t, want, isStartCommand(text), text)
	}
}

func TestBuildWelcomeMessage_GroupSaysGroupChatID(t *testing.T) {
	msg := buildWelcomeMessage("sentinel", "Karvin", -1001234567890, "supergroup")
	assert.Contains(t, msg, "Chat ID grup ini: `-1001234567890`")
	assert.NotContains(t, msg, "Chat ID kamu")
}

func TestBuildWelcomeMessage_PrivateSaysYourChatID(t *testing.T) {
	msg := buildWelcomeMessage("asset", "Karvin", 1332941423, "private")
	assert.True(t, strings.Contains(msg, "Chat ID kamu: `1332941423`"), msg)
}
