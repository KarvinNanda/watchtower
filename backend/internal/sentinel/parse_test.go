package sentinel

import (
	"os"
	"strings"
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
	it := byID(items)["CVE-2026-109708"] // real, status "Deferred", no metrics
	assert.Equal(t, "CVE-2026-109708", it.CVEID)
	assert.Empty(t, it.Severity)
	assert.Zero(t, it.CVSSScore)
}

func TestParseNVD_PrimaryBeatsSecondary(t *testing.T) {
	items, err := parseNVD(readFixture(t, "nvd.json"), fixtureNow)
	require.NoError(t, err)
	// Real: NVD Primary 5.5 vs CNA Secondary 6.2. The higher CNA score must
	// not win, so this also proves the parser does not just take the max.
	it := byID(items)["CVE-2026-74871"]
	assert.Equal(t, "MEDIUM", it.Severity)
	assert.InDelta(t, 5.5, it.CVSSScore, 0.001)
}

func TestParseNVD_FallsBackToV40(t *testing.T) {
	items, err := parseNVD(readFixture(t, "nvd.json"), fixtureNow)
	require.NoError(t, err)
	it := byID(items)["CVE-2026-96535"] // real, status "Received", CVSS 4.0 only
	assert.Equal(t, "MEDIUM", it.Severity)
	assert.InDelta(t, 5.1, it.CVSSScore, 0.001)
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

// Real advisories (trimmed) from GET /advisories, 2026-10-15. GitHub
// deprecated "cvss" for "cvss_severities"; a missing score there is 0.0.
func TestParseGitHubAdvisories_ReadsCVSSSeverities(t *testing.T) {
	items, err := parseGitHubAdvisories(readFixture(t, "ghsa.json"), fixtureNow)
	require.NoError(t, err)
	m := byID(items)

	v3 := m["GHSA-pwhx-cvv3-qj5c"]
	assert.Equal(t, "HIGH", v3.Severity)
	assert.InDelta(t, 8.2, v3.CVSSScore, 0.001)

	// Only a CVSS 4.0 score: the deprecated "cvss" is null here, so reading
	// it alone dropped the score of a critical advisory.
	v4 := m["GHSA-p8wg-vrv2-v86f"]
	assert.Equal(t, "CRITICAL", v4.Severity)
	assert.InDelta(t, 9.2, v4.CVSSScore, 0.001)
}

func TestParseNVD_CarriesVectorOfChosenMetric(t *testing.T) {
	items, err := parseNVD(readFixture(t, "nvd.json"), fixtureNow)
	require.NoError(t, err)
	m := byID(items)

	// NVD Primary v3.1 entry wins, so its vector (not the CNA one) is kept.
	assert.Equal(t, "CVSS:3.1/AV:L/AC:L/PR:L/UI:N/S:U/C:H/I:N/A:N", m["CVE-2026-74871"].CVSSVector)
	// Only a v4.0 metric exists here.
	assert.True(t, strings.HasPrefix(m["CVE-2026-96535"].CVSSVector, "CVSS:4.0/"))
	// No metrics, no vector.
	assert.Empty(t, m["CVE-2026-109708"].CVSSVector)
}

func TestParseNVD_SourceDateIsPublished(t *testing.T) {
	items, err := parseNVD(readFixture(t, "nvd.json"), fixtureNow)
	require.NoError(t, err)
	want := time.Date(2024, 3, 29, 17, 15, 21, 150000000, time.UTC)
	assert.True(t, byID(items)["CVE-2024-3094"].SourceDate.Equal(want))
}

func TestParseTimeKnown(t *testing.T) {
	got, ok := parseTimeKnown("")
	assert.False(t, ok)
	assert.True(t, got.IsZero())

	got, ok = parseTimeKnown("garbage")
	assert.False(t, ok)
	assert.True(t, got.IsZero())

	got, ok = parseTimeKnown("2024-04-12T08:15:06.230")
	assert.True(t, ok)
	assert.True(t, got.Equal(time.Date(2024, 4, 12, 8, 15, 6, 230000000, time.UTC)))
}

func TestParseGitHubAdvisories_VectorFollowsScore(t *testing.T) {
	items, err := parseGitHubAdvisories(readFixture(t, "ghsa.json"), fixtureNow)
	require.NoError(t, err)
	m := byID(items)

	// v3 score > 0: the v3 vector.
	assert.Equal(t, "CVSS:3.1/AV:N/AC:H/PR:L/UI:N/S:C/C:H/I:H/A:N", m["GHSA-pwhx-cvv3-qj5c"].CVSSVector)
	// v3 score 0, v4 score > 0: the v4 vector.
	assert.Equal(t, "CVSS:4.0/AV:N/AC:L/AT:P/PR:N/UI:N/VC:H/VI:H/VA:H/SC:N/SI:N/SA:N", m["GHSA-p8wg-vrv2-v86f"].CVSSVector)
	// Everything null: empty vector.
	assert.Empty(t, m["GHSA-aaaa-bbbb-cccc"].CVSSVector)
}

func TestFirstScored_NullVectorOfWinner(t *testing.T) {
	score, vec := firstScored(scoredVector{Score: 0}, scoredVector{Score: 7.5, Vector: ""})
	assert.InDelta(t, 7.5, score, 0.001)
	assert.Empty(t, vec)
}

func TestParseGitHubAdvisories_SourceDate(t *testing.T) {
	items, err := parseGitHubAdvisories(readFixture(t, "ghsa.json"), fixtureNow)
	require.NoError(t, err)
	want := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	assert.True(t, byID(items)["GHSA-4j9v-h7pp-725h"].SourceDate.Equal(want))
}

func TestParseKEV_SourceDateIsZero(t *testing.T) {
	cutoff := fixtureNow.Add(-lookbackWindow)
	items, err := parseKEV(readFixture(t, "kev.json"), cutoff)
	require.NoError(t, err)
	require.NotEmpty(t, items)
	for _, it := range items {
		assert.True(t, it.SourceDate.IsZero(), "KEV dateAdded is not the publish date")
	}
}
