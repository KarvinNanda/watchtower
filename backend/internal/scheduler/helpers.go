package scheduler

import (
	"strconv"
	"strings"

	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

// formatUSD renders amount as a thousands-separated USD string, e.g.
// formatUSD(61633.0) == "$61,633.00". Amounts below $1 keep up to 8
// decimals without trailing zeros (formatUSD(0.00001234) == "$0.00001234"),
// matching the UI, so small crypto prices never collapse to "$0.00".
func formatUSD(amount float64) string {
	if amount > -1 && amount < 1 && amount != 0 {
		return "$" + trimDecimals(amount, 8)
	}
	p := message.NewPrinter(language.English)
	return p.Sprintf("$%.2f", amount)
}

// formatIDR renders amount as a thousands-separated Rupiah string, e.g.
// formatIDR(1108743364.0) == "Rp 1,108,743,364". Amounts below Rp 1 (small
// crypto prices) keep up to 4 decimals: formatIDR(0.19663) == "Rp 0.1966".
func formatIDR(amount float64) string {
	if amount > -1 && amount < 1 && amount != 0 {
		return "Rp " + trimDecimals(amount, 4)
	}
	p := message.NewPrinter(language.English)
	return "Rp " + p.Sprintf("%.0f", amount)
}

// trimDecimals formats amount with at most decimals digits after the point
// and drops trailing zeros.
func trimDecimals(amount float64, decimals int) string {
	s := strconv.FormatFloat(amount, 'f', decimals, 64)
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}
