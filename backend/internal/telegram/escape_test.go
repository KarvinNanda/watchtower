package telegram

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEscapeTelegramMarkdown_EscapesBackslash(t *testing.T) {
	// A raw "\" would turn the following escape into a literal backslash
	// and leave the special character bare.
	assert.Equal(t, `C:\\Windows\\ \.`, EscapeTelegramMarkdown(`C:\Windows\ .`))
}

func TestEscapeTelegramMarkdown_EscapesEverySpecialCharacter(t *testing.T) {
	assert.Equal(t, `\_\*\[\]\(\)\~`+"\\`"+`\>\#\+\-\=\|\{\}\.\!\\`,
		EscapeTelegramMarkdown("_*[]()~`>#+-=|{}.!\\"))
}
