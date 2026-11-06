package alertmsg

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// basicEntries is out of order on purpose: the KEV item must come first.
func basicEntries() []SentinelEntry {
	return []SentinelEntry{
		{SourceType: "cve", Title: "nginx heap overflow in HTTP/3 module",
			Description: "A heap overflow in the HTTP/3 module allows remote code execution.",
			URL:         "https://nvd.nist.gov/vuln/detail/CVE-2026-1234", CVEID: "CVE-2026-1234",
			Severity: "HIGH", CVSSScore: 8.2, MatchedKeywords: []string{"nginx"}},
		{SourceType: "cisa_kev", Title: "Fortinet FortiOS heap overflow",
			URL: "https://www.cisa.gov/known-exploited-vulnerabilities-catalog", CVEID: "CVE-2026-1111",
			KnownExploited: true, RequiredAction: "Apply mitigations per vendor instructions.", DueDate: "2026-11-05",
			Ransomware: true, MatchedKeywords: []string{"fortios"}},
	}
}

func orderingEntries() []SentinelEntry {
	return []SentinelEntry{
		{SourceType: "rss", Title: "Patch Tuesday roundup", URL: "https://example.com/news", MatchedKeywords: []string{"windows"}},
		{SourceType: "github_advisory", Title: "lodash prototype pollution", Severity: "moderate", URL: "https://github.com/advisories/GHSA-xxxx", MatchedKeywords: []string{"lodash"}},
		{SourceType: "cve", Title: "unscored flaw", CVEID: "CVE-2026-0002", URL: "https://nvd.nist.gov/vuln/detail/CVE-2026-0002", MatchedKeywords: []string{"flaw"}},
		{SourceType: "cve", Title: "low flaw", CVEID: "CVE-2026-0003", Severity: "LOW", CVSSScore: 3.1, URL: "https://nvd.nist.gov/vuln/detail/CVE-2026-0003"},
		{SourceType: "cve", Title: "critical flaw", CVEID: "CVE-2026-0004", Severity: "CRITICAL", CVSSScore: 9.8, URL: "https://nvd.nist.gov/vuln/detail/CVE-2026-0004"},
		{SourceType: "cisa_kev", Title: "exploited flaw", CVEID: "CVE-2026-0001", KnownExploited: true, URL: "https://www.cisa.gov/kev", DueDate: "not a date"},
	}
}

// linkEntries covers the link and escaping edge cases so they also reach
// the real-Telegram E2E run: a backslash in fetched text, ")" in a URL,
// userinfo, an IDN host, a non-http scheme and an empty URL.
func linkEntries() []SentinelEntry {
	return []SentinelEntry{
		{SourceType: "cve", CVEID: "CVE-2026-0101", Severity: "HIGH", CVSSScore: 7.8, Title: `Path traversal in C:\Windows\.`,
			Description: `Writes to C:\Windows\System32\ via ..\..\ sequences.`, URL: "https://example.com/a_(b)"},
		{SourceType: "cve", CVEID: "CVE-2026-0102", Severity: "HIGH", CVSSScore: 7.5, Title: "userinfo link", URL: "https://nvd.nist.gov@evil.com/x"},
		{SourceType: "cve", CVEID: "CVE-2026-0103", Severity: "HIGH", CVSSScore: 7.4, Title: "homograph link", URL: "https://аpple.com/login"},
		{SourceType: "cve", CVEID: "CVE-2026-0104", Severity: "HIGH", CVSSScore: 7.3, Title: "script link", URL: "javascript:alert(1)"},
		{SourceType: "cve", CVEID: "CVE-2026-0105", Severity: "HIGH", CVSSScore: 7.2, Title: "no link", URL: ""},
	}
}

func TestSentinelMessages_Golden(t *testing.T) {
	at := jakartaAt(t)
	cases := []struct {
		name    string
		lang    string
		entries []SentinelEntry
	}{
		{"sentinel_en_basic", "en", basicEntries()},
		{"sentinel_id_basic", "id", basicEntries()},
		{"sentinel_en_ordering", "en", orderingEntries()},
		{"sentinel_id_ordering", "id", orderingEntries()},
		{"sentinel_en_links", "en", linkEntries()},
		{"sentinel_id_links", "id", linkEntries()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertGolden(t, tc.name, SentinelMessages(tc.lang, at, tc.entries))
		})
	}
}

func TestSentinelMessages_EmptyIsNil(t *testing.T) {
	assert.Nil(t, SentinelMessages("en", jakartaAt(t), nil))
}

func TestSentinelMessages_Ordering(t *testing.T) {
	msg := SentinelMessages("en", jakartaAt(t), orderingEntries())[0]
	order := []string{"CVE\\-2026\\-0001", "CVE\\-2026\\-0004", "CVE\\-2026\\-0003", "lodash", "CVE\\-2026\\-0002", "Patch Tuesday"}
	last := -1
	for _, needle := range order {
		i := strings.Index(msg, needle)
		require.NotEqual(t, -1, i, needle)
		assert.Greater(t, i, last, "%s out of order", needle)
		last = i
	}
}

func TestSentinelMessages_NoCVEUsesTitleAsHeadline(t *testing.T) {
	msg := SentinelMessages("en", jakartaAt(t), []SentinelEntry{
		{SourceType: "rss", Title: "Some news", URL: "https://example.com/a", MatchedKeywords: []string{"news"}},
	})[0]
	assert.Contains(t, msg, "⚪ *Some news*\n🏷️ news · RSS\n🔗 [example\\.com](https://example.com/a)")
	assert.Equal(t, 1, strings.Count(msg, "Some news"), "title is not repeated under the headline")
}

func TestSentinelMessages_SeverityLabels(t *testing.T) {
	cases := map[string]SentinelEntry{
		"🟡 *CVE\\-1* · MODERATE":        {SourceType: "github_advisory", CVEID: "CVE-1", Severity: "moderate"},
		"⚪ *CVE\\-2* · N/A":             {SourceType: "cve", CVEID: "CVE-2"},
		"🔴 *CVE\\-3* · CRITICAL 10\\.0": {SourceType: "cve", CVEID: "CVE-3", Severity: "CRITICAL", CVSSScore: 10},
		"🟢 *CVE\\-4* · LOW 3\\.1":       {SourceType: "cve", CVEID: "CVE-4", Severity: "low", CVSSScore: 3.1},
		// A score without a severity label shows the score, with the emoji
		// taken from the standard CVSS v3 bands (#44).
		"🔴 *CVE\\-5* · 9\\.8": {SourceType: "cve", CVEID: "CVE-5", CVSSScore: 9.8},
		"🟠 *CVE\\-6* · 7\\.0": {SourceType: "cve", CVEID: "CVE-6", CVSSScore: 7.0},
		"🟡 *CVE\\-7* · 4\\.0": {SourceType: "github_advisory", CVEID: "CVE-7", CVSSScore: 4.0},
		"🟢 *CVE\\-8* · 0\\.1": {SourceType: "rss", CVEID: "CVE-8", CVSSScore: 0.1},
	}
	for want, e := range cases {
		assert.Contains(t, SentinelMessages("en", jakartaAt(t), []SentinelEntry{e})[0], want)
	}
}

func TestSentinelMessages_TruncatesLongFields(t *testing.T) {
	long := strings.Repeat("a", 500)
	msg := SentinelMessages("en", jakartaAt(t), []SentinelEntry{
		{SourceType: "cisa_kev", CVEID: "CVE-1", KnownExploited: true, Title: long, Description: long + "b", RequiredAction: long},
	})[0]
	assert.NotContains(t, msg, strings.Repeat("a", 201))
	assert.Contains(t, msg, strings.Repeat("a", 60)+"…")
}

func TestLinkLine(t *testing.T) {
	cases := map[string]string{
		"https://nvd.nist.gov/vuln/detail/CVE-1": "🔗 [nvd\\.nist\\.gov](https://nvd.nist.gov/vuln/detail/CVE-1)",
		"https://nvd.nist.gov@evil.com/x":        "🔗 [evil\\.com](https://nvd.nist.gov@evil.com/x)",
		"HTTPS://Example.COM/x":                  "🔗 [example\\.com](HTTPS://Example.COM/x)",
		"https://example.com/a_(b)":              "🔗 [example\\.com](https://example.com/a_(b\\))",
		// Non-ASCII hosts show their punycode form, so a homograph such as a
		// Cyrillic "а" cannot look like the real domain.
		"https://exämple.com/a":                  "🔗 [xn\\-\\-exmple\\-cua\\.com](https://exämple.com/a)",
		"https://аpple.com/login":                "🔗 [xn\\-\\-pple\\-43d\\.com](https://аpple.com/login)",
		"https://nvd.nist.gov%E3%80%82evil.com/": "🔗 [nvd\\.nist\\.gov\\.evil\\.com](https://nvd.nist.gov%E3%80%82evil.com/)",
		"javascript:alert(1)":                    "",
		"ftp://example.com/file":                 "",
		"not a url":                              "",
		"":                                       "",
	}
	for in, want := range cases {
		assert.Equal(t, want, linkLine(in), in)
	}
}

func TestLinkLine_LongURLLinksToTheHostOnly(t *testing.T) {
	long := "https://example.com/track?id=" + strings.Repeat("a", 5000)
	assert.Equal(t, "🔗 [example\\.com](https://example.com/)", linkLine(long))
	assert.Equal(t, "🔗 [example\\.com](https://example.com:8443/)", linkLine("https://u:p@example.com:8443/"+strings.Repeat("b", 1200)),
		"userinfo is dropped, the port is kept")
}

// One huge tracking URL must not make a part that Telegram rejects (#43).
func TestSentinelMessages_LongURLStaysUnderTheLimit(t *testing.T) {
	msgs := SentinelMessages("en", jakartaAt(t), []SentinelEntry{
		{SourceType: "rss", Title: "news", URL: "https://example.com/track?id=" + strings.Repeat("a", 5000)},
	})
	require.Len(t, msgs, 1)
	assert.LessOrEqual(t, len(msgs[0]), telegramMaxMessageLen)
}

func TestEPSSLine(t *testing.T) {
	assert.Equal(t, "📈 EPSS: 99% peluang dieksploitasi dalam 30 hari", epssLine("id", f64(0.99999)))
	assert.Equal(t, "📈 EPSS: 94% peluang dieksploitasi dalam 30 hari", epssLine("id", f64(0.94321)))
	assert.Equal(t, "📈 EPSS: 0.4% peluang dieksploitasi dalam 30 hari", epssLine("id", f64(0.004)))
	assert.Equal(t, "📈 EPSS: < 0.1% peluang dieksploitasi dalam 30 hari", epssLine("id", f64(0.0004)))
	assert.Equal(t, "", epssLine("id", nil))
	for in, want := range map[float64]string{0.29: "29%", 0.57: "57%", 0.01: "1%", 0.001: "0.1%", 1.0: "100%", 1.5: "100%", 0.99999: "99%", -0.2: "< 0.1%"} {
		assert.Equal(t, "📈 EPSS: "+want+" peluang dieksploitasi dalam 30 hari", epssLine("id", f64(in)), "%v", in)
	}
	assert.Equal(t, "📈 EPSS: 94% chance of exploitation in the next 30 days", epssLine("en", f64(0.94321)))
}

func TestAffectedLines(t *testing.T) {
	assert.Equal(t, []string{"📦 fortios: terdampak < 7.4.3 · aman mulai 7.4.3"},
		affectedLines("id", []AffectedVersion{{Product: "fortios", Range: "< 7.4.3", Fixed: "7.4.3"}}))
	assert.Equal(t, []string{"📦 pan-os: terdampak versi 10.2.0-h1"},
		affectedLines("id", []AffectedVersion{{Product: "pan-os", Range: "10.2.0-h1"}}))
	assert.Equal(t, []string{"📦 lodash: affected <= 4.17.20 · no fixed version yet"},
		affectedLines("en", []AffectedVersion{{Product: "lodash", Range: "<= 4.17.20", NoFix: true}}))
	assert.Equal(t, []string{"📦 y: terdampak ≤ 2.1"},
		affectedLines("id", []AffectedVersion{{Product: "y", Range: "≤ 2.1"}}))
	assert.Equal(t, []string{"📦 x: terdampak versi 1.0 · belum ada versi aman"},
		affectedLines("id", []AffectedVersion{{Product: "x", Range: "1.0", NoFix: true}}))

	both := affectedLines("id", []AffectedVersion{{Product: "z", Range: "< 2", Fixed: "2", NoFix: true}})
	assert.Equal(t, []string{"📦 z: terdampak < 2 · aman mulai 2"}, both)

	five := []AffectedVersion{{"a", "1", "", false}, {"b", "2", "", false}, {"c", "3", "", false}, {"d", "4", "", false}, {"e", "5", "", false}}
	got := affectedLines("id", five)
	require.Len(t, got, 3)
	assert.Equal(t, "📦 +3 lainnya", got[2])
	assert.Equal(t, "📦 +3 more", affectedLines("en", five)[2])

	assert.Nil(t, affectedLines("id", nil))
}

func TestAffectedLines_SkipsEmptyRange(t *testing.T) {
	av := []AffectedVersion{{Product: "skip1"}, {Product: "a", Range: "1"}, {Product: "skip2", Fixed: "9"}, {Product: "b", Range: "2"}}
	got := affectedLines("en", av)
	assert.Equal(t, []string{"📦 a: affected version 1", "📦 b: affected version 2"}, got)
	assert.Nil(t, affectedLines("en", []AffectedVersion{{Product: "only"}}))
}

func TestAffectedLines_Truncates(t *testing.T) {
	got := affectedLines("en", []AffectedVersion{{
		Product: strings.Repeat("p", 60), Range: "< " + strings.Repeat("r", 60), Fixed: strings.Repeat("f", 60)}})
	require.Len(t, got, 1)
	assert.Contains(t, got[0], strings.Repeat("p", 39)+"…")
	assert.NotContains(t, got[0], strings.Repeat("p", 41))
	assert.NotContains(t, got[0], strings.Repeat("f", 31))
}

func TestPublishedLine(t *testing.T) {
	assert.Equal(t, "📅 Dipublikasi: 12 Okt 2026", publishedLine("id", time.Date(2026, 10, 12, 3, 0, 0, 0, time.UTC)))
	assert.Equal(t, "", publishedLine("id", time.Time{}))
}

func TestSeverityText_KEVWithScore(t *testing.T) {
	assert.Equal(t, "ACTIVELY EXPLOITED · CRITICAL 9.8", severityText(SentinelEntry{KnownExploited: true, Severity: "critical", CVSSScore: 9.8}))
	assert.Equal(t, "ACTIVELY EXPLOITED · 9.8", severityText(SentinelEntry{KnownExploited: true, CVSSScore: 9.8}))
	assert.Equal(t, "ACTIVELY EXPLOITED", severityText(SentinelEntry{KnownExploited: true}))
	assert.Equal(t, "ACTIVELY EXPLOITED", severityText(SentinelEntry{KnownExploited: true, Severity: "HIGH"}))
}

func enrichedEntries() []SentinelEntry {
	return []SentinelEntry{
		{SourceType: "cisa_kev", Title: "Fortinet FortiOS heap overflow", CVEID: "CVE-2026-1111", URL: "https://nvd.nist.gov/vuln/detail/CVE-2026-1111",
			KnownExploited: true, Severity: "CRITICAL", CVSSScore: 9.8, CVSSVector: "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H",
			RequiredAction: "Apply mitigations per vendor instructions.", DueDate: "2026-11-05", Ransomware: true,
			Affected: []AffectedVersion{{Product: "FortiOS", Range: "< 7.4.3", Fixed: "7.4.3"}},
			EPSS:     f64(0.94321), PublishedAt: time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC), MatchedKeywords: []string{"fortios"}},
		{SourceType: "cve", Title: "Acme gateway auth bypass", CVEID: "CVE-2026-2222", URL: "https://nvd.nist.gov/vuln/detail/CVE-2026-2222",
			Severity: "HIGH", CVSSScore: 7.5, CVSSVector: "CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:H/VI:N/VA:N/SC:N/SI:N/SA:N",
			Affected: []AffectedVersion{
				{Product: "acme gateway", Range: "≥ 2.0, < 2.4", Fixed: "2.4"},
				{Product: "acme gateway", Range: "≤ 1.9"},
				{Product: "acme agent", Range: "3.1.0"},
			},
			EPSS: f64(0.004), MatchedKeywords: []string{"acme"}},
		{SourceType: "rss", Title: "Patch Tuesday roundup", URL: "https://example.com/news", MatchedKeywords: []string{"windows"}},
	}
}

func TestSentinelMessages_EnrichedGolden(t *testing.T) {
	at := jakartaAt(t)
	for _, lang := range []string{"id", "en"} {
		name := "sentinel_" + lang + "_enriched"
		t.Run(name, func(t *testing.T) {
			assertGolden(t, name, SentinelMessages(lang, at, enrichedEntries()))
		})
	}
}

func TestSentinelMessages_ManyEnrichedItemsStayUnderLimit(t *testing.T) {
	var entries []SentinelEntry
	for i := 0; i < 40; i++ {
		var av []AffectedVersion
		for j := 0; j < 60; j++ {
			av = append(av, AffectedVersion{Product: strings.Repeat("very_long_product_", 5), Range: fmt.Sprintf("1.%d.%d_(x)", i, j)})
		}
		entries = append(entries, SentinelEntry{SourceType: "cve", CVEID: fmt.Sprintf("CVE-2026-%04d", i), Title: "t", Severity: "HIGH", CVSSScore: 7.5,
			CVSSVector: "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H", Affected: av, EPSS: f64(0.5),
			PublishedAt: time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)})
	}
	for _, lang := range []string{"id", "en"} {
		msgs := SentinelMessages(lang, jakartaAt(t), entries)
		require.NotEmpty(t, msgs)
		for i, m := range msgs {
			assert.LessOrEqual(t, len(m), telegramMaxMessageLen, "%s part %d", lang, i+1)
			require.NoError(t, checkMarkdownV2(m), "%s part %d", lang, i+1)
		}
	}
}

func TestAffectedLines_KeepsWholeTwoVersionRange(t *testing.T) {
	rng := ">= 4.0.0-canary.0, < 4.0.0-canary.34"
	got := affectedLines("en", []AffectedVersion{{Product: "next", Range: rng}})
	require.Len(t, got, 1)
	assert.Contains(t, got[0], rng)
}
