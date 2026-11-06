package sentinel

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sorted returns a copy of ss sorted ascending, so slice-equality
// assertions below don't depend on sentinelKeywordList's internal order.
func sorted(ss []string) []string {
	out := append([]string(nil), ss...)
	sort.Strings(out)
	return out
}

func TestExtractKeywords_Match(t *testing.T) {
	t.Parallel()
	got := extractKeywords("Apache RCE vulnerability in Linux kernel")

	// "rce" is itself one of sentinelKeywordList's security-category
	// entries, and the input text contains it as a standalone word, so a
	// correct implementation matches it too alongside apache/linux.
	assert.Equal(t, []string{"apache", "linux", "rce"}, sorted(got))
}

func TestExtractKeywords_CaseInsensitive(t *testing.T) {
	t.Parallel()
	got := extractKeywords("ANDROID Privilege Escalation")
	assert.Equal(t, []string{"android", "escalation", "privilege"}, sorted(got))
}

func TestExtractKeywords_NoMatch(t *testing.T) {
	t.Parallel()
	got := extractKeywords("Random text with no security keywords")
	assert.NotNil(t, got, "extractKeywords must return an empty slice, never nil")
	assert.Empty(t, got)
}

func TestExtractKeywords_NoDuplicates(t *testing.T) {
	t.Parallel()
	got := extractKeywords("apache apache apache nginx nginx")
	assert.Equal(t, []string{"apache", "nginx"}, sorted(got))
}

func dedupeFixtures() (SentinelItem, SentinelItem) {
	nvd := SentinelItem{SourceType: "cve", Identifier: "CVE-2026-1", CVEID: "CVE-2026-1", CVSSScore: 3.1, Severity: "LOW", CVSSVector: "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:L/I:N/A:N"}
	kev := SentinelItem{SourceType: "cisa_kev", Identifier: "CVE-2026-1", CVEID: "CVE-2026-1", KnownExploited: true, RequiredAction: "Patch.", DueDate: "2026-11-20", Ransomware: true}
	return nvd, kev
}

func assertMergedKEV(t *testing.T, got []SentinelItem) {
	t.Helper()
	require.Len(t, got, 1)
	assert.Equal(t, "cve", got[0].SourceType)
	assert.True(t, got[0].KnownExploited)
	assert.Equal(t, "Patch.", got[0].RequiredAction)
	assert.Equal(t, "2026-11-20", got[0].DueDate)
	assert.True(t, got[0].Ransomware)
	assert.Equal(t, 3.1, got[0].CVSSScore)
	assert.Equal(t, "LOW", got[0].Severity)
	assert.Equal(t, "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:L/I:N/A:N", got[0].CVSSVector)
}

func TestDedupe_MergesKEVIntoNVD(t *testing.T) {
	t.Parallel()
	nvd, kev := dedupeFixtures()
	assertMergedKEV(t, dedupeByIdentifier([]SentinelItem{nvd, kev}))
}

func TestDedupe_MergesWhenKEVComesFirst(t *testing.T) {
	t.Parallel()
	nvd, kev := dedupeFixtures()
	assertMergedKEV(t, dedupeByIdentifier([]SentinelItem{kev, nvd}))
}

func TestDedupe_KEVFirstKeepsEarlierPosition(t *testing.T) {
	t.Parallel()
	nvd, kev := dedupeFixtures()
	other := SentinelItem{SourceType: "rss", Identifier: "other"}
	got := dedupeByIdentifier([]SentinelItem{kev, other, nvd})
	require.Len(t, got, 2)
	assert.Equal(t, "cve", got[0].SourceType)
	assert.Equal(t, "other", got[1].Identifier)
}

func TestDedupe_OtherDuplicatesFirstWins(t *testing.T) {
	t.Parallel()
	a := SentinelItem{SourceType: "rss", Identifier: "x", Title: "first"}
	b := SentinelItem{SourceType: "rss", Identifier: "x", Title: "second"}
	got := dedupeByIdentifier([]SentinelItem{a, b})
	require.Len(t, got, 1)
	assert.Equal(t, "first", got[0].Title)
	assert.False(t, got[0].KnownExploited)
}
