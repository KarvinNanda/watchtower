package scheduler

import (
	"strconv"

	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

// formatUSD renders amount as a thousands-separated USD string, e.g.
// formatUSD(61633.0) == "$61,633.00". Amounts below $1 keep up to 8
// significant decimals (formatUSD(0.00001234) == "$0.00001234") so small
// crypto prices never collapse to "$0.00".
func formatUSD(amount float64) string {
	if amount > -1 && amount < 1 && amount != 0 {
		return "$" + strconv.FormatFloat(amount, 'f', -1, 64)
	}
	p := message.NewPrinter(language.English)
	return p.Sprintf("$%.2f", amount)
}

// formatIDR renders amount as a thousands-separated Rupiah string, e.g.
// formatIDR(1108743364.0) == "Rp 1,108,743,364".
func formatIDR(amount float64) string {
	p := message.NewPrinter(language.English)
	return "Rp " + p.Sprintf("%.0f", amount)
}
