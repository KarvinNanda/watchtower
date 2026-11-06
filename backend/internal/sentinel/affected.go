package sentinel

import "strings"

// AffectedVersion is one affected product and version range of a CVE or
// advisory. Values are source data and stored raw.
type AffectedVersion struct {
	Product string // CPE product unescaped, "_" to " "; or GHSA package.name
	Range   string // "< X", "≥ S, < X", "> S, ≤ X", "≤ X", GHSA range verbatim, or a bare version
	Fixed   string // first safe version; "" when unknown
	NoFix   bool   // GHSA said first_patched_version is null. Never set from NVD.
}

// splitCPE splits a CPE 2.3 string on ":" that is not preceded by a
// backslash, then drops the backslash from each "\x" escape.
func splitCPE(s string) []string {
	var fields []string
	var cur strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\\' && i+1 < len(s):
			i++
			cur.WriteByte(s[i])
		case c == ':':
			fields = append(fields, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	return append(fields, cur.String())
}

// affectedFromCPE turns one NVD cpeMatch row into an AffectedVersion.
// ok is false for a malformed CPE, or when there is no range and the
// version is "*" or "-" (it would say nothing useful).
func affectedFromCPE(criteria, startIncl, startExcl, endIncl, endExcl string) (AffectedVersion, bool) {
	f := splitCPE(criteria)
	if len(f) < 6 || f[0] != "cpe" || f[1] != "2.3" || f[4] == "" {
		return AffectedVersion{}, false
	}
	av := AffectedVersion{Product: strings.ReplaceAll(f[4], "_", " ")}

	var parts []string
	if startIncl != "" {
		parts = append(parts, "≥ "+startIncl)
	} else if startExcl != "" {
		parts = append(parts, "> "+startExcl)
	}
	if endExcl != "" {
		parts = append(parts, "< "+endExcl)
		av.Fixed = endExcl
	} else if endIncl != "" {
		parts = append(parts, "≤ "+endIncl)
	}
	if len(parts) > 0 {
		av.Range = strings.Join(parts, ", ")
		return av, true
	}

	version := f[5]
	if version == "" || version == "*" || version == "-" {
		return AffectedVersion{}, false
	}
	if len(f) > 6 && f[6] != "*" && f[6] != "-" && f[6] != "" {
		version += "-" + f[6]
	}
	av.Range = version
	return av, true
}

// nvdConfiguration is the configurations part of an NVD CVE record.
type nvdConfiguration struct {
	Negate bool `json:"negate"`
	Nodes  []struct {
		Negate   bool `json:"negate"`
		CPEMatch []struct {
			Vulnerable            bool   `json:"vulnerable"`
			Criteria              string `json:"criteria"`
			VersionStartIncluding string `json:"versionStartIncluding"`
			VersionStartExcluding string `json:"versionStartExcluding"`
			VersionEndIncluding   string `json:"versionEndIncluding"`
			VersionEndExcluding   string `json:"versionEndExcluding"`
		} `json:"cpeMatch"`
	} `json:"nodes"`
}

// affectedFromConfigurations collects vulnerable cpeMatch rows in source
// order, skipping negated nodes and collapsing duplicates (same Product
// and Range).
func affectedFromConfigurations(configs []nvdConfiguration) []AffectedVersion {
	var out []AffectedVersion
	seen := map[[2]string]bool{}
	for _, c := range configs {
		if c.Negate {
			continue
		}
		for _, n := range c.Nodes {
			if n.Negate {
				continue
			}
			for _, m := range n.CPEMatch {
				if !m.Vulnerable {
					continue
				}
				av, ok := affectedFromCPE(m.Criteria, m.VersionStartIncluding, m.VersionStartExcluding, m.VersionEndIncluding, m.VersionEndExcluding)
				if !ok {
					continue
				}
				key := [2]string{av.Product, av.Range}
				if seen[key] {
					continue
				}
				seen[key] = true
				out = append(out, av)
			}
		}
	}
	return out
}
