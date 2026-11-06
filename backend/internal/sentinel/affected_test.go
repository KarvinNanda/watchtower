package sentinel

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAffectedFromCPE(t *testing.T) {
	cases := []struct {
		name, crit, si, se, ei, ee string
		want                       AffectedVersion
		ok                         bool
	}{
		{"end excl", "cpe:2.3:o:fortinet:fortios:*:*:*:*:*:*:*:*", "", "", "", "7.4.3", AffectedVersion{Product: "fortios", Range: "< 7.4.3", Fixed: "7.4.3"}, true},
		{"start incl + end excl", "cpe:2.3:o:fortinet:fortios:*:*:*:*:*:*:*:*", "7.0.0", "", "", "7.4.3", AffectedVersion{Product: "fortios", Range: "≥ 7.0.0, < 7.4.3", Fixed: "7.4.3"}, true},
		{"start excl + end incl", "cpe:2.3:a:x:y:*:*:*:*:*:*:*:*", "", "1.0", "2.0", "", AffectedVersion{Product: "y", Range: "> 1.0, ≤ 2.0"}, true},
		{"end incl", "cpe:2.3:a:x:y:*:*:*:*:*:*:*:*", "", "", "2.1", "", AffectedVersion{Product: "y", Range: "≤ 2.1"}, true},
		{"exact with update", "cpe:2.3:o:paloaltonetworks:pan-os:10.2.0:h1:*:*:*:*:*:*", "", "", "", "", AffectedVersion{Product: "pan-os", Range: "10.2.0-h1"}, true},
		{"exact dash update", "cpe:2.3:o:paloaltonetworks:pan-os:10.2.0:-:*:*:*:*:*:*", "", "", "", "", AffectedVersion{Product: "pan-os", Range: "10.2.0"}, true},
		{"escaped dot", `cpe:2.3:a:nodejs:node\.js:*:*:*:*:*:*:*:*`, "", "", "", "18.0.0", AffectedVersion{Product: "node.js", Range: "< 18.0.0", Fixed: "18.0.0"}, true},
		{"escaped colon keeps fields", `cpe:2.3:a:acme:foo\:bar:1.2:*:*:*:*:*:*:*`, "", "", "", "", AffectedVersion{Product: "foo:bar", Range: "1.2"}, true},
		{"underscore", "cpe:2.3:a:apache:http_server:*:*:*:*:*:*:*:*", "", "", "", "2.4.60", AffectedVersion{Product: "http server", Range: "< 2.4.60", Fixed: "2.4.60"}, true},
		{"star no range", "cpe:2.3:a:x:y:*:*:*:*:*:*:*:*", "", "", "", "", AffectedVersion{}, false},
		{"malformed", "not-a-cpe", "", "", "", "", AffectedVersion{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := affectedFromCPE(tc.crit, tc.si, tc.se, tc.ei, tc.ee)
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestParseNVD_AffectedFromConfigurations(t *testing.T) {
	items, err := parseNVD(readFixture(t, "nvd_cve_cpe.json"), fixtureNow)
	require.NoError(t, err)
	require.Len(t, items, 1)
	aff := items[0].Affected
	require.Len(t, aff, 4)
	assert.Equal(t, AffectedVersion{Product: "pan-os", Range: "10.2.0"}, aff[0])
	assert.Equal(t, AffectedVersion{Product: "pan-os", Range: "10.2.0-h1"}, aff[1])
	for _, a := range aff {
		assert.False(t, a.NoFix)
	}
}

// The real fixture has no negate node and no vulnerable:false row, so this
// small inline body covers both, plus duplicate collapse and source order.
func TestParseNVD_AffectedSkipsNegateAndNonVulnerable(t *testing.T) {
	body := []byte(`{"vulnerabilities":[{"cve":{"id":"CVE-2099-0001","published":"2099-01-01T00:00:00.000","descriptions":[{"lang":"en","value":"x"}],
	"configurations":[{"nodes":[
	 {"negate":false,"cpeMatch":[
	  {"vulnerable":true,"criteria":"cpe:2.3:a:v:b:*:*:*:*:*:*:*:*","versionEndExcluding":"2.0"},
	  {"vulnerable":false,"criteria":"cpe:2.3:o:v:running_on:1.0:*:*:*:*:*:*:*"},
	  {"vulnerable":true,"criteria":"cpe:2.3:a:v:b:*:*:*:*:*:*:*:*","versionEndExcluding":"2.0"},
	  {"vulnerable":true,"criteria":"cpe:2.3:a:v:a:*:*:*:*:*:*:*:*","versionEndIncluding":"1.5"}]},
	 {"negate":true,"cpeMatch":[{"vulnerable":true,"criteria":"cpe:2.3:a:v:neg:3.0:*:*:*:*:*:*:*"}]}
	]}]}}]}`)
	items, err := parseNVD(body, fixtureNow)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, []AffectedVersion{
		{Product: "b", Range: "< 2.0", Fixed: "2.0"},
		{Product: "a", Range: "≤ 1.5"},
	}, items[0].Affected)
}

func TestParseGitHubAdvisories_Affected(t *testing.T) {
	items, err := parseGitHubAdvisories(readFixture(t, "ghsa_vulns.json"), fixtureNow)
	require.NoError(t, err)
	m := byID(items)

	a := m["GHSA-m5ph-mmg5-6w5h"].Affected
	assert.Equal(t, []AffectedVersion{{Product: "filecoin-pin", Range: "<= 1.1.0", Fixed: "1.1.1"}}, a)

	b := m["GHSA-mc8m-rr6c-r5qr"].Affected
	require.Len(t, b, 2)
	assert.Equal(t, ">= 3.25.0, < 3.90.0", b[0].Range)
	assert.Equal(t, "4.0.0-canary.34", b[1].Fixed)

	// first_patched_version is null in this entry (hand-edited in the fixture).
	c := m["GHSA-3cgg-c5pp-57h6"].Affected
	assert.Equal(t, []AffectedVersion{{Product: "odf-kit", Range: ">= 0.6.0, < 0.14.2", Fixed: "", NoFix: true}}, c)
}
