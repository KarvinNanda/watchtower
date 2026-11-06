package alertmsg

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustParse(t *testing.T, s string) cvssVector {
	t.Helper()
	v, ok := parseCVSSVector(s)
	require.True(t, ok, s)
	return v
}

func TestImpactLine(t *testing.T) {
	cases := []struct{ lang, vec, want string }{
		{"id", "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H", "💥 Dampak: data bisa bocor, data bisa diubah, service bisa mati"},
		{"en", "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H", "💥 Impact: data can leak, data can be changed, the service can go down"},
		{"id", "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:C/C:L/I:N/A:L", "💥 Dampak: sebagian data bisa bocor, service bisa terganggu, bisa merembet ke sistem lain"},
		{"en", "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:C/C:L/I:L/A:N", "💥 Impact: some data can leak, some data can be changed, can spread to other systems"},
		{"id", "CVSS:3.0/AV:L/AC:L/PR:N/UI:N/S:U/C:N/I:N/A:N", ""},
		{"id", "CVSS:4.0/AV:L/AC:L/AT:N/PR:N/UI:N/VC:H/VI:N/VA:N/SC:N/SI:N/SA:N", "💥 Dampak: data bisa bocor"},
		{"id", "CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:N/VI:H/VA:N/SC:L/SI:N/SA:N", "💥 Dampak: data bisa diubah, bisa merembet ke sistem lain"},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, impactLine(c.lang, mustParse(t, c.vec)), c.lang+" "+c.vec)
	}
}

func TestImpactLine_V4SafetyIsNotScope(t *testing.T) {
	// the real NVD v4 vector with S:X and every supplemental metric X
	v := mustParse(t, "CVSS:4.0/AV:L/AC:L/AT:N/PR:N/UI:N/VC:H/VI:N/VA:N/SC:N/SI:N/SA:N/E:X/CR:X/IR:X/AR:X/MAV:X/MAC:X/MAT:X/MPR:X/MUI:X/MVC:X/MVI:X/MVA:X/MSC:X/MSI:X/MSA:X/S:X/AU:X/R:X/V:X/RE:X/U:X")
	assert.Equal(t, "💥 Dampak: data bisa bocor", impactLine("id", v))
	// S:P (Safety present) and S:C must not add the spread clause in v4
	for _, s := range []string{"P", "C"} {
		v := mustParse(t, "CVSS:4.0/AV:L/AC:L/AT:N/PR:N/UI:N/VC:H/VI:N/VA:N/SC:N/SI:N/SA:N/S:"+s)
		assert.Equal(t, "💥 Dampak: data bisa bocor", impactLine("id", v), "S:"+s)
	}
}

func TestAttackLine(t *testing.T) {
	assert.Equal(t, "🚪 Cara serang: dari internet · tanpa login · korban tidak perlu klik apa pun",
		attackLine("id", mustParse(t, "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H")))
	assert.Equal(t, "🚪 Cara serang: dari jaringan lokal yang sama · perlu akun biasa · korban harus melakukan sesuatu, misalnya klik link · butuh kondisi khusus",
		attackLine("id", mustParse(t, "CVSS:3.1/AV:A/AC:H/PR:L/UI:R/S:U/C:H/I:H/A:H")))
	en := attackLine("en", mustParse(t, "CVSS:3.1/AV:P/AC:L/PR:H/UI:N/S:U/C:H/I:H/A:H"))
	assert.Contains(t, en, "needs physical access")
	assert.Contains(t, en, "needs an admin account")
	assert.True(t, strings.HasPrefix(en, "🚪 How it is attacked: "))

	v4 := attackLine("id", mustParse(t, "CVSS:4.0/AV:L/AC:L/AT:P/PR:N/UI:A/VC:H/VI:N/VA:N/SC:N/SI:N/SA:N"))
	assert.Contains(t, v4, "harus punya akses ke perangkat")
	assert.Contains(t, v4, "korban harus melakukan sesuatu")
	assert.Equal(t, 1, strings.Count(v4, "butuh kondisi khusus"))

	both := attackLine("id", mustParse(t, "CVSS:4.0/AV:N/AC:H/AT:P/PR:N/UI:N/VC:H/VI:N/VA:N/SC:N/SI:N/SA:N"))
	assert.Equal(t, 1, strings.Count(both, "butuh kondisi khusus"))
}

func TestParseCVSSVector_Invalid(t *testing.T) {
	for _, s := range []string{
		"",
		"AV:N/AC:L",
		"CVSS:2.0/AV:N/AC:L/Au:N/C:P/I:P/A:P",
		"CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H",
		"CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:H/VI:H/VA:H/SC:N/SI:N",
	} {
		_, ok := parseCVSSVector(s)
		assert.False(t, ok, s)
	}
}
