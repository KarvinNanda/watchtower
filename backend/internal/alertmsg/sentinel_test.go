package alertmsg

import (
	"strings"
	"testing"

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
