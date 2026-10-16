package alertmsg

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/karvin-nanda/watchtower/internal/rules"
)

// ReportSymbol is one symbol of a status report (#7). Unavailable means its
// price could not be fetched; the price fields are then ignored.
type ReportSymbol struct {
	Symbol      string
	Unavailable bool
	PriceUSD    float64
	NativeIDR   float64 // gold: the source's own rupiah price; 0 otherwise
	ChangePct   *float64
	StaleSince  *time.Time // cached data used after a refused or failed refresh
	Rules       []ReportRule
}

// ReportRule is one active rule and how far the price is from it. Nothing
// fired: the report only states where things are.
type ReportRule struct {
	RuleID   uint64
	Trigger  rules.Trigger
	Distance rules.Distance
}

// AssetReportMessages renders one user's status report as one or more
// message parts: one block per symbol (alphabetical), one line per rule (by
// rule ID). at must already be in the instance timezone.
func AssetReportMessages(lang string, at time.Time, rate Rate, symbols []ReportSymbol) []string {
	if len(symbols) == 0 {
		return nil
	}
	sorted := append([]ReportSymbol(nil), symbols...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Symbol < sorted[j].Symbol })

	names := make([]string, len(sorted))
	blocks := make([]string, len(sorted))
	ruleCount := 0
	for i, s := range sorted {
		names[i] = s.Symbol
		blocks[i] = reportBlock(lang, at, rate, s)
		ruleCount += len(s.Rules)
	}
	noun := "rule"
	if isEN(lang) && ruleCount != 1 {
		noun = "rules"
	}
	h := header{
		first: "📋 *" + pick(lang, "Asset status report", "Laporan status aset") + "* · " +
			esc(fmt.Sprintf("%s (%d %s)", symbolList(names), ruleCount, noun)),
		lines: []string{sentAtLine(at, lang), rateLine(lang, at.Location(), rate)},
	}
	return pack(lang, h, blocks)
}

func reportBlock(lang string, at time.Time, rate Rate, s ReportSymbol) string {
	if s.Unavailable {
		return "📊 *" + esc(s.Symbol) + "* · " + esc(pick(lang, "price unavailable", "harga tidak tersedia"))
	}
	g := assetGroup{symbol: s.Symbol, priceUSD: s.PriceUSD, nativeIDR: s.NativeIDR, changePct: s.ChangePct}
	lines := []string{priceLine(rate, g)}
	if s.StaleSince != nil {
		lines = append(lines, "⏳ "+esc(staleText(lang, at.Sub(*s.StaleSince))))
	}
	rs := append([]ReportRule(nil), s.Rules...)
	sort.SliceStable(rs, func(i, j int) bool { return rs[i].RuleID < rs[j].RuleID })
	for _, r := range rs {
		lines = append(lines, triggerEmoji(r.Trigger)+" "+esc(reportRuleText(lang, rate, r)))
	}
	return strings.Join(append(lines, changeLine(s.ChangePct)), "\n")
}

// reportRuleText names the user's rule and where the price is relative to
// it. Intent wording repeats the user's own plan; it is never advice.
func reportRuleText(lang string, rate Rate, r ReportRule) string {
	t, d := r.Trigger, r.Distance
	switch t.Type {
	case rules.TypePriceLevel:
		return levelLabel(lang, rate, t) + distanceTail(lang, d)
	case rules.TypeTrailingStop:
		label := "Trailing stop " + formatPct(t.Pct)
		switch d.Kind {
		case rules.DistanceWaitingPeak:
			return label + pick(lang, " · waiting for a peak", " · menunggu puncak")
		case rules.DistanceLevel:
			return label + ": stop " + amount(rate, d.Level) + pick(lang, " (peak ", " (puncak ") +
				amount(rate, d.PeakUSD) + ")" + distanceTail(lang, d)
		}
		return label + distanceTail(lang, d)
	case rules.TypePctChange24h:
		now := "n/a"
		if d.Current != nil {
			now = fmt.Sprintf("%.2f%%", *d.Current)
		}
		return pick(lang, "24h change ±"+formatPct(t.Pct)+" · now "+now, "Perubahan 24 jam ±"+formatPct(t.Pct)+" · sekarang "+now)
	case rules.TypeWeek52Break:
		label := "52-week high"
		if t.Direction == rules.DirectionBelow {
			label = "52-week low"
		}
		// The level is the extreme stored at the previous evaluation, the
		// one the engine compares with (#52); none before the first run.
		if d.Kind != rules.DistanceLevel {
			return label
		}
		return label + " " + amount(rate, d.Level) + distanceTail(lang, d)
	}
	return string(t.Type)
}

func levelLabel(lang string, rate Rate, t rules.Trigger) string {
	level := levelAmount(rate, t)
	switch t.Intent {
	case rules.IntentBuyTarget:
		return "Buy target " + level
	case rules.IntentTakeProfit:
		return "Take profit " + level
	case rules.IntentStopLoss:
		return "Stop loss " + level
	}
	if t.Direction == rules.DirectionAbove {
		return pick(lang, "Above ", "Di atas ") + level
	}
	return pick(lang, "Below ", "Di bawah ") + level
}

func distanceTail(lang string, d rules.Distance) string {
	switch d.Kind {
	case rules.DistanceLevel:
		if d.Passed {
			return pick(lang, " · passed", " · sudah lewat")
		}
		s := pick(lang, " · distance ", " · jarak ") + fmt.Sprintf("%+.1f%%", d.Pct)
		if d.Near {
			s += pick(lang, " (near)", " (dekat)")
		}
		return s
	case rules.DistanceUnavailable:
		return pick(lang, " · distance unavailable", " · jarak tidak tersedia")
	}
	return ""
}

func staleText(lang string, age time.Duration) string {
	if age < time.Hour {
		m := int(age.Minutes())
		if m < 1 {
			m = 1
		}
		return pick(lang, fmt.Sprintf("data from %d minute(s) ago", m), fmt.Sprintf("data dari %d menit lalu", m))
	}
	h := int(age.Hours())
	return pick(lang, fmt.Sprintf("data from %d hour(s) ago", h), fmt.Sprintf("data dari %d jam lalu", h))
}
