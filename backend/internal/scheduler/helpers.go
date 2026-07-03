package scheduler

import (
	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

// formatUSD renders amount as a thousands-separated USD string, e.g.
// formatUSD(61633.0) == "$61,633.00". Used wherever a price appears in a
// Telegram alert message — fmt.Sprintf's "%.2f" alone has no thousands
// separator and would render as the much harder to read "$61633.00".
func formatUSD(amount float64) string {
	p := message.NewPrinter(language.English)
	return p.Sprintf("$%.2f", amount)
}

// formatIDR renders amount as a thousands-separated Rupiah string, e.g.
// formatIDR(1108743364.0) == "Rp 1,108,743,364".
func formatIDR(amount float64) string {
	p := message.NewPrinter(language.English)
	return "Rp " + p.Sprintf("%.0f", amount)
}
