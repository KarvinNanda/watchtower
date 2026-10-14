package sentinel

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	require.NoError(t, err)
	return b
}

func byID(items []SentinelItem) map[string]SentinelItem {
	m := make(map[string]SentinelItem, len(items))
	for _, it := range items {
		m[it.Identifier] = it
	}
	return m
}

var fixtureNow = time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)

func TestParseNVD_ScoredCVE(t *testing.T) {
	items, err := parseNVD(readFixture(t, "nvd.json"), fixtureNow)
	require.NoError(t, err)
	it := byID(items)["CVE-2024-3094"]
	assert.Equal(t, "cve", it.SourceType)
	assert.Equal(t, "CVE-2024-3094", it.CVEID)
	assert.Equal(t, "CRITICAL", it.Severity)
	assert.InDelta(t, 10.0, it.CVSSScore, 0.001)
	assert.False(t, it.KnownExploited)
}

func TestParseNVD_UnscoredCVE(t *testing.T) {
	items, err := parseNVD(readFixture(t, "nvd.json"), fixtureNow)
	require.NoError(t, err)
	it := byID(items)["CVE-2026-99999"]
	assert.Equal(t, "CVE-2026-99999", it.CVEID)
	assert.Empty(t, it.Severity)
	assert.Zero(t, it.CVSSScore)
}

func TestParseNVD_PrimaryBeatsSecondary(t *testing.T) {
	items, err := parseNVD(readFixture(t, "nvd.json"), fixtureNow)
	require.NoError(t, err)
	it := byID(items)["CVE-2026-88888"]
	assert.Equal(t, "HIGH", it.Severity)
	assert.InDelta(t, 7.5, it.CVSSScore, 0.001)
}

func TestParseNVD_FallsBackToV40(t *testing.T) {
	items, err := parseNVD(readFixture(t, "nvd.json"), fixtureNow)
	require.NoError(t, err)
	it := byID(items)["CVE-2026-77777"]
	assert.Equal(t, "CRITICAL", it.Severity)
	assert.InDelta(t, 9.3, it.CVSSScore, 0.001)
}

func TestParseKEV_EnrichedAndWindowed(t *testing.T) {
	cutoff := fixtureNow.Add(-lookbackWindow)
	items, err := parseKEV(readFixture(t, "kev.json"), cutoff)
	require.NoError(t, err)
	require.Len(t, items, 2, "the 2020 entry is outside the lookback window")

	m := byID(items)
	ivanti := m["CVE-2026-104935"]
	assert.Equal(t, "cisa_kev", ivanti.SourceType)
	assert.Equal(t, "CVE-2026-104935", ivanti.CVEID)
	assert.True(t, ivanti.KnownExploited)
	assert.Equal(t, "Apply mitigations in accordance with vendor instructions.", ivanti.RequiredAction)
	assert.Equal(t, "2026-10-16", ivanti.DueDate)
	assert.False(t, ivanti.Ransomware)

	assert.True(t, m["CVE-2026-100001"].Ransomware)
}

func TestParseGitHubAdvisories(t *testing.T) {
	items, err := parseGitHubAdvisories(readFixture(t, "ghsa.json"), fixtureNow)
	require.NoError(t, err)
	m := byID(items)

	scored := m["GHSA-4j9v-h7pp-725h"]
	assert.Equal(t, "github_advisory", scored.SourceType)
	assert.Equal(t, "CVE-2026-70035", scored.CVEID)
	assert.Equal(t, "HIGH", scored.Severity)
	assert.InDelta(t, 8.8, scored.CVSSScore, 0.001)

	unscored := m["GHSA-aaaa-bbbb-cccc"]
	assert.Empty(t, unscored.CVEID)
	assert.Empty(t, unscored.Severity, `"unknown" severity is treated as no severity`)
	assert.Zero(t, unscored.CVSSScore)
}
