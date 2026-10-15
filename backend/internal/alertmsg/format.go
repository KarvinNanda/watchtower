// Package alertmsg builds the Telegram MarkdownV2 messages WatchTower
// sends: the asset alert and the sentinel report. It is pure: no database,
// no HTTP, no clock. Callers pass the send time (already in the instance
// timezone) and, for asset alerts, the USD/IDR rate.
//
// Escaping rule: every dynamic value is built as a plain string first and
// escaped as a whole with esc. Only this package's own template text
// (emoji, "*" bold markers, " · ", link brackets) is written unescaped.
package alertmsg

import (
	"strconv"
	"strings"
	"time"

	"golang.org/x/text/language"
	"golang.org/x/text/message"

	"github.com/karvin-nanda/watchtower/internal/telegram"
)

const (
	sentAtLayout   = "02 Jan 2006 15:04 MST"
	rateTimeLayout = "02 Jan 15:04 MST"
	dueDateLayout  = "2 Jan 2006"
)

var monthsID = [12]string{"Jan", "Feb", "Mar", "Apr", "Mei", "Jun", "Jul", "Agu", "Sep", "Okt", "Nov", "Des"}

// linkURLReplacer escapes the URL part of an inline link "[text](url)":
// inside the parentheses MarkdownV2 requires only ")" and "\" escaped.
var linkURLReplacer = strings.NewReplacer(`\`, `\\`, ")", `\)`)

func esc(s string) string { return telegram.EscapeTelegramMarkdown(s) }

func escapeLinkURL(u string) string { return linkURLReplacer.Replace(u) }

func isEN(lang string) bool { return lang == "en" }

func pick(lang, en, id string) string {
	if isEN(lang) {
		return en
	}
	return id
}

// formatUSD renders amount as "$61,633.00". Amounts below $1 keep up to 8
// decimals without trailing zeros, matching the UI.
func formatUSD(amount float64) string {
	if amount > -1 && amount < 1 && amount != 0 {
		return "$" + trimDecimals(amount, 8)
	}
	return message.NewPrinter(language.English).Sprintf("$%.2f", amount)
}

// formatIDR renders amount in id-ID style like the UI: "Rp 1.463.319.195".
// Amounts below Rp 1 keep up to 4 decimals: "Rp 0,1966".
func formatIDR(amount float64) string {
	if amount > -1 && amount < 1 && amount != 0 {
		return "Rp " + strings.Replace(trimDecimals(amount, 4), ".", ",", 1)
	}
	return "Rp " + message.NewPrinter(language.Indonesian).Sprintf("%.0f", amount)
}

// trimDecimals formats amount with at most decimals digits after the point
// and drops trailing zeros.
func trimDecimals(amount float64, decimals int) string {
	s := strconv.FormatFloat(amount, 'f', decimals, 64)
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}

// formatPct renders a threshold without trailing zeros: 5 -> "5%".
func formatPct(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64) + "%"
}

// TruncateRunes bounds s to n runes, appending an ellipsis when truncated.
func TruncateRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}

// formatTime formats t with a layout whose month is written "Jan". For
// Indonesian the English month abbreviation is swapped for monthsID. The
// month is the first letters in every layout this package uses, so the
// first match is always the month, never the zone.
func formatTime(t time.Time, layout, lang string) string {
	s := t.Format(layout)
	if isEN(lang) {
		return s
	}
	return strings.Replace(s, t.Format("Jan"), monthsID[t.Month()-1], 1)
}

// formatDueDate renders a CISA KEV due date ("2006-01-02") as "5 Nov 2026".
// Anything unparsable is shown as published.
func formatDueDate(raw, lang string) string {
	t, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return raw
	}
	return formatTime(t, dueDateLayout, lang)
}
