package alertmsg

import "strings"

// cvssVector is a parsed CVSS vector string. Only the version and the metric
// map are kept. Extra metrics (v4 supplemental, threat, environmental) are
// stored but never read.
type cvssVector struct {
	version string            // "3.0", "3.1", "4.0"
	m       map[string]string // metric to value
}

var (
	cvssV3Required = []string{"AV", "AC", "PR", "UI", "S", "C", "I", "A"}
	cvssV4Required = []string{"AV", "AC", "AT", "PR", "UI", "VC", "VI", "VA", "SC", "SI", "SA"}
)

// parseCVSSVector accepts CVSS 3.0, 3.1 and 4.0 vectors. It returns ok=false
// for an unknown prefix or when a required base metric is missing.
func parseCVSSVector(s string) (cvssVector, bool) {
	parts := strings.Split(s, "/")
	if len(parts) < 2 {
		return cvssVector{}, false
	}
	head, ver, found := strings.Cut(parts[0], ":")
	if !found || head != "CVSS" {
		return cvssVector{}, false
	}
	var required []string
	switch ver {
	case "3.0", "3.1":
		required = cvssV3Required
	case "4.0":
		required = cvssV4Required
	default:
		return cvssVector{}, false
	}
	m := make(map[string]string, len(parts))
	for _, p := range parts[1:] {
		k, val, ok := strings.Cut(p, ":")
		if !ok || k == "" {
			continue
		}
		m[k] = val
	}
	for _, k := range required {
		if m[k] == "" {
			return cvssVector{}, false
		}
	}
	return cvssVector{version: ver, m: m}, true
}

func (v cvssVector) isV4() bool { return v.version == "4.0" }

// impactLine describes what an attacker gains. It returns "" when there is
// nothing to say. The text is not escaped.
func impactLine(lang string, v cvssVector) string {
	c, i, a := "C", "I", "A"
	if v.isV4() {
		c, i, a = "VC", "VI", "VA"
	}
	var parts []string
	switch v.m[c] {
	case "H":
		parts = append(parts, pick(lang, "data can leak", "data bisa bocor"))
	case "L":
		parts = append(parts, pick(lang, "some data can leak", "sebagian data bisa bocor"))
	}
	switch v.m[i] {
	case "H":
		parts = append(parts, pick(lang, "data can be changed", "data bisa diubah"))
	case "L":
		parts = append(parts, pick(lang, "some data can be changed", "sebagian data bisa diubah"))
	}
	switch v.m[a] {
	case "H":
		parts = append(parts, pick(lang, "the service can go down", "service bisa mati"))
	case "L":
		parts = append(parts, pick(lang, "the service can be disrupted", "service bisa terganggu"))
	}
	if v.spreads() {
		parts = append(parts, pick(lang, "can spread to other systems", "bisa merembet ke sistem lain"))
	}
	if len(parts) == 0 {
		return ""
	}
	return pick(lang, "💥 Impact: ", "💥 Dampak: ") + strings.Join(parts, ", ")
}

// spreads reports whether the impact reaches other systems. In v3 that is
// Scope Changed. In v4 it is a subsequent system impact. In v4 the metric S is
// the Safety supplemental metric, so it is never read there.
func (v cvssVector) spreads() bool {
	if v.isV4() {
		for _, k := range []string{"SC", "SI", "SA"} {
			if val := v.m[k]; val != "" && val != "N" {
				return true
			}
		}
		return false
	}
	return v.m["S"] == "C"
}

// attackLine describes how the flaw is reached. It returns "" when there is
// nothing to say. The text is not escaped.
func attackLine(lang string, v cvssVector) string {
	var parts []string
	switch v.m["AV"] {
	case "N":
		parts = append(parts, pick(lang, "from the internet", "dari internet"))
	case "A":
		parts = append(parts, pick(lang, "from the same local network", "dari jaringan lokal yang sama"))
	case "L":
		parts = append(parts, pick(lang, "needs access to the device", "harus punya akses ke perangkat"))
	case "P":
		parts = append(parts, pick(lang, "needs physical access", "harus memegang perangkatnya"))
	}
	switch v.m["PR"] {
	case "N":
		parts = append(parts, pick(lang, "no login needed", "tanpa login"))
	case "L":
		parts = append(parts, pick(lang, "needs a normal account", "perlu akun biasa"))
	case "H":
		parts = append(parts, pick(lang, "needs an admin account", "perlu akun admin"))
	}
	switch ui := v.m["UI"]; {
	case ui == "N":
		parts = append(parts, pick(lang, "no action from the victim", "korban tidak perlu klik apa pun"))
	case ui == "R" && !v.isV4(), (ui == "P" || ui == "A") && v.isV4():
		parts = append(parts, pick(lang, "the victim must do something, such as click a link", "korban harus melakukan sesuatu, misalnya klik link"))
	}
	if v.m["AC"] == "H" || (v.isV4() && v.m["AT"] == "P") {
		parts = append(parts, pick(lang, "needs special conditions", "butuh kondisi khusus"))
	}
	if len(parts) == 0 {
		return ""
	}
	return pick(lang, "🚪 How it is attacked: ", "🚪 Cara serang: ") + strings.Join(parts, " · ")
}
