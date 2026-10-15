package alertmsg

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/karvin-nanda/watchtower/internal/rules"
)

var fuzzAt = time.Date(2026, 10, 15, 2, 30, 0, 0, time.UTC)

func FuzzAssetMessages(f *testing.F) {
	f.Add("BTC", 81714.0, 80000.0, "en")
	f.Add(`a\.b_(c)*[d]~>#+-=|{}!`, 0.5, 1e-9, "id")
	f.Add(`C:\Windows\`, -1.0, 0.0, "en")
	f.Fuzz(func(t *testing.T, symbol string, price, level float64, lang string) {
		if !utf8.ValidString(symbol) {
			t.Skip("Telegram only accepts valid UTF-8")
		}
		for _, rate := range []Rate{{}, {Value: 17908, UpdatedAt: fuzzAt}} {
			msgs := AssetMessages(lang, fuzzAt, rate, []AssetAlert{
				{RuleID: 1, Symbol: symbol, PriceUSD: price, Trigger: rules.Trigger{Type: rules.TypePriceLevel, Direction: rules.DirectionAbove, LevelUSD: level}},
				{RuleID: 2, Symbol: symbol, PriceUSD: price, NativeIDR: price, Trigger: rules.Trigger{Type: rules.TypeTrailingStop, Pct: level, PeakPriceUSD: price}},
			})
			for i, m := range msgs {
				if err := checkMarkdownV2(m); err != nil {
					t.Fatalf("part %d: %v\n%s", i+1, err, m)
				}
			}
		}
	})
}

func FuzzSentinelMessages(f *testing.F) {
	f.Add("CVE-2026-1", "title", `desc C:\path\.`, "kw_1", "https://nvd.nist.gov/a_(b)", "HIGH", "2026-11-05", true)
	f.Add("", "", "", "", "javascript:alert(1)", "", "", false)
	f.Add("", `*[x](y)*`, "", "", "https://a@b.com/)\\", "moderate", "x", true)
	f.Fuzz(func(t *testing.T, cve, title, desc, keyword, rawURL, severity, due string, kev bool) {
		for _, s := range []string{cve, title, desc, keyword, rawURL, severity, due} {
			if !utf8.ValidString(s) {
				t.Skip("Telegram only accepts valid UTF-8")
			}
		}
		for _, lang := range []string{"en", "id"} {
			msgs := SentinelMessages(lang, fuzzAt, []SentinelEntry{{
				SourceType: "cve", CVEID: cve, Title: title, Description: desc, URL: rawURL, Severity: severity,
				CVSSScore: 7.5, KnownExploited: kev, RequiredAction: desc, DueDate: due, Ransomware: kev,
				MatchedKeywords: []string{keyword},
			}})
			for i, m := range msgs {
				if err := checkMarkdownV2(m); err != nil {
					t.Fatalf("%s part %d: %v\n%s", lang, i+1, err, m)
				}
			}
		}
	})
}

// FuzzPack checks the splitting contract on blocks of random sizes: every
// block appears exactly once and in order, and a part is over the limit
// only when it holds a single block that is itself too big.
func FuzzPack(f *testing.F) {
	f.Add(uint8(6), uint16(1000), uint16(37), "id")
	f.Add(uint8(40), uint16(120), uint16(4000), "en")
	f.Add(uint8(3), uint16(5000), uint16(10), "id")
	f.Fuzz(func(t *testing.T, n uint8, base, spread uint16, lang string) {
		h := header{first: "🔔 *Alert aset* · X", lines: []string{"⏰ now"}}
		blocks := make([]string, int(n)%60+1)
		for i := range blocks {
			size := int(base)%6000 + (i*int(spread))%700
			blocks[i] = fmt.Sprintf("<<%d>>", i) + strings.Repeat("x", size)
		}
		msgs := pack(lang, h, blocks)
		joined := strings.Join(msgs, "")
		last := -1
		for i := range blocks {
			marker := fmt.Sprintf("<<%d>>", i)
			if c := strings.Count(joined, marker); c != 1 {
				t.Fatalf("block %d appears %d times", i, c)
			}
			at := strings.Index(joined, marker)
			if at < last {
				t.Fatalf("block %d out of order", i)
			}
			last = at
		}
		for i, m := range msgs {
			if len(m) > telegramMaxMessageLen && strings.Count(m, "<<") != 1 {
				t.Fatalf("part %d is %d bytes with %d blocks", i+1, len(m), strings.Count(m, "<<"))
			}
		}
	})
}
