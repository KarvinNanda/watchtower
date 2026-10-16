package alertmsg

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/karvin-nanda/watchtower/internal/rules"
)

const maxHeaderSymbols = 5

// Rate is the one USD -> IDR rate used for every IDR amount of a message.
// Value 0 means unavailable: no amount is converted. Callers never pass the
// currency package's hardcoded fallback here.
type Rate struct {
	Value     float64   // IDR per 1 USD
	UpdatedAt time.Time // when the provider last updated it; zero = unknown
}

func (r Rate) available() bool { return r.Value > 0 }

// AssetAlert is one fired rule. NativeIDR is set only when the source
// quotes the asset in IDR (ANTAM); it is shown as is, never converted.
type AssetAlert struct {
	RuleID    uint64
	Symbol    string
	PriceUSD  float64
	NativeIDR float64
	ChangePct *float64
	Trigger   rules.Trigger
}

type assetGroup struct {
	symbol    string
	priceUSD  float64
	nativeIDR float64
	changePct *float64
	alerts    []AssetAlert
}

// AssetMessages renders one user's fired rules as one or more message
// parts: one block per symbol (alphabetical), one trigger line per rule
// (by rule ID). at must already be in the instance timezone.
func AssetMessages(lang string, at time.Time, rate Rate, alerts []AssetAlert) []string {
	if len(alerts) == 0 {
		return nil
	}
	groups := groupAlerts(alerts)
	blocks := make([]string, len(groups))
	for i, g := range groups {
		blocks[i] = assetBlock(lang, rate, g)
	}
	h := header{
		first: "🔔 *" + pick(lang, "Asset alert", "Alert aset") + "* · " + esc(assetSummary(lang, groups, len(alerts))),
		lines: []string{sentAtLine(at, lang), rateLine(lang, at.Location(), rate)},
	}
	return pack(lang, h, blocks)
}

// groupAlerts groups alerts by symbol. All alerts of a symbol come from the
// same fetch in the same run, so the first alert's prices are the group's.
func groupAlerts(alerts []AssetAlert) []assetGroup {
	bySymbol := make(map[string]*assetGroup)
	var symbols []string
	for _, a := range alerts {
		g, ok := bySymbol[a.Symbol]
		if !ok {
			g = &assetGroup{symbol: a.Symbol, priceUSD: a.PriceUSD, nativeIDR: a.NativeIDR, changePct: a.ChangePct}
			bySymbol[a.Symbol] = g
			symbols = append(symbols, a.Symbol)
		}
		g.alerts = append(g.alerts, a)
	}
	sort.Strings(symbols)
	groups := make([]assetGroup, 0, len(symbols))
	for _, s := range symbols {
		g := bySymbol[s]
		sort.SliceStable(g.alerts, func(i, j int) bool { return g.alerts[i].RuleID < g.alerts[j].RuleID })
		groups = append(groups, *g)
	}
	return groups
}

// symbolList is the header list of symbols: at most maxHeaderSymbols, then "+N".
func symbolList(names []string) string {
	if len(names) <= maxHeaderSymbols {
		return strings.Join(names, ", ")
	}
	return strings.Join(names[:maxHeaderSymbols], ", ") + fmt.Sprintf(", +%d", len(names)-maxHeaderSymbols)
}

// assetSummary is the header text after the title: "BTC, ETH (3 trigger)".
func assetSummary(lang string, groups []assetGroup, fired int) string {
	names := make([]string, len(groups))
	for i, g := range groups {
		names[i] = g.symbol
	}
	noun := "trigger"
	if isEN(lang) && fired != 1 {
		noun = "triggers"
	}
	return fmt.Sprintf("%s (%d %s)", symbolList(names), fired, noun)
}

// priceLine is a symbol block's first line: "📊 *BTC* · $81,714.00 · Rp …".
func priceLine(rate Rate, g assetGroup) string {
	first := "📊 *" + esc(g.symbol) + "* · " + esc(formatUSD(g.priceUSD))
	if idr := groupIDR(rate, g); idr > 0 {
		first += " · " + esc(formatIDR(idr))
	}
	return first
}

// changeLine is a symbol block's last line: "24h 1.20%" or "24h n/a".
func changeLine(changePct *float64) string {
	change := "n/a"
	if changePct != nil {
		change = fmt.Sprintf("%.2f%%", *changePct)
	}
	return esc("24h " + change)
}

func assetBlock(lang string, rate Rate, g assetGroup) string {
	lines := []string{priceLine(rate, g)}
	for _, a := range g.alerts {
		lines = append(lines, triggerEmoji(a.Trigger)+" "+esc(triggerSentence(lang, rate, a.Trigger)))
	}
	return strings.Join(append(lines, changeLine(g.changePct)), "\n")
}

func rateLine(lang string, loc *time.Location, rate Rate) string {
	if !rate.available() {
		return "💱 " + esc(pick(lang, "USD/IDR rate unavailable", "Kurs USD/IDR tidak tersedia"))
	}
	s := "1 USD = " + formatIDR(math.Round(rate.Value))
	if !rate.UpdatedAt.IsZero() {
		s += " (" + pick(lang, "rate as of ", "kurs ") + formatTime(rate.UpdatedAt.In(loc), rateTimeLayout, lang) + ")"
	}
	return "💱 " + esc(s)
}

// groupIDR is the IDR price to show: the source's own IDR price when there
// is one, otherwise the USD price at the message rate, otherwise none.
func groupIDR(rate Rate, g assetGroup) float64 {
	if g.nativeIDR > 0 {
		return g.nativeIDR
	}
	if rate.available() {
		return g.priceUSD * rate.Value
	}
	return 0
}

// amount renders a USD amount, plus its IDR equivalent when the rate is
// known. Rules are evaluated in USD, so IDR is only an approximation.
func amount(rate Rate, usd float64) string {
	s := formatUSD(usd)
	if rate.available() {
		s += " ≈ " + formatIDR(usd*rate.Value)
	}
	return s
}

// levelAmount shows a price_level rule's level in the currency the user
// entered it in (#40), with the other currency after "≈" only when a real
// rate is known. USD rules keep amount's "$X ≈ Rp Y".
func levelAmount(rate Rate, t rules.Trigger) string {
	if t.Currency != rules.CurrencyIDR {
		return amount(rate, t.LevelUSD)
	}
	return idrAmount(rate, t.LevelIDR)
}

// idrAmount renders a rupiah amount first, with its USD equivalent after "≈"
// only when a real rate is known (#40, #58).
func idrAmount(rate Rate, idr float64) string {
	s := formatIDR(idr)
	if rate.available() {
		s += " ≈ " + formatUSD(idr/rate.Value)
	}
	return s
}

// triggerEmoji marks the rule type. It is a type marker for the user's own
// rule, never a buy/sell signal.
func triggerEmoji(t rules.Trigger) string {
	switch t.Type {
	case rules.TypePriceLevel:
		switch t.Intent {
		case rules.IntentBuyTarget:
			return "🛒"
		case rules.IntentTakeProfit:
			return "🎯"
		case rules.IntentStopLoss:
			return "🛑"
		}
		if t.Direction == rules.DirectionAbove {
			return "⬆️"
		}
		return "⬇️"
	case rules.TypePctChange24h:
		return "⚡"
	case rules.TypeTrailingStop:
		return "📉"
	case rules.TypeWeek52Break:
		return "🆕"
	}
	return "•"
}

// triggerSentence says why a rule fired. Intent wording repeats the user's
// own plan; it is never advice.
func triggerSentence(lang string, rate Rate, t rules.Trigger) string {
	level := levelAmount(rate, t)
	pct := formatPct(t.Pct)
	switch t.Type {
	case rules.TypePriceLevel:
		switch t.Intent {
		case rules.IntentBuyTarget:
			return pick(lang, "Reached your buy target ("+level+")", "Mencapai buy target kamu ("+level+")")
		case rules.IntentTakeProfit:
			return pick(lang, "Reached your take-profit level ("+level+")", "Mencapai level take-profit kamu ("+level+")")
		case rules.IntentStopLoss:
			return pick(lang, "Reached your stop-loss level ("+level+")", "Mencapai level stop-loss kamu ("+level+")")
		}
		if t.Direction == rules.DirectionAbove {
			return pick(lang, "Above "+level, "Di atas "+level)
		}
		return pick(lang, "Below "+level, "Di bawah "+level)
	case rules.TypePctChange24h:
		return pick(lang, "24h change beyond ±"+pct, "Perubahan 24 jam melewati ±"+pct)
	case rules.TypeTrailingStop:
		peak := amount(rate, t.PeakPriceUSD)
		if t.Currency == rules.CurrencyIDR {
			peak = idrAmount(rate, t.PeakPriceIDR) // gold is tracked in rupiah (#58)
		}
		return pick(lang, "Down "+pct+" or more from peak "+peak, "Turun "+pct+" atau lebih dari peak "+peak)
	case rules.TypeWeek52Break:
		prev := amount(rate, t.PrevRefUSD)
		if t.Direction == rules.DirectionBelow {
			return pick(lang, "New 52-week low (previous "+prev+")", "52-week low baru (sebelumnya "+prev+")")
		}
		return pick(lang, "New 52-week high (previous "+prev+")", "52-week high baru (sebelumnya "+prev+")")
	}
	return string(t.Type)
}
