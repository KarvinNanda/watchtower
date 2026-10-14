package sentinel

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// nvdCVSSMetric is one entry of an NVD metrics.cvssMetricV* array.
type nvdCVSSMetric struct {
	Type     string `json:"type"`
	CVSSData struct {
		BaseScore    float64 `json:"baseScore"`
		BaseSeverity string  `json:"baseSeverity"`
	} `json:"cvssData"`
}

type nvdResponse struct {
	Vulnerabilities []struct {
		CVE struct {
			ID           string `json:"id"`
			Published    string `json:"published"`
			Descriptions []struct {
				Lang  string `json:"lang"`
				Value string `json:"value"`
			} `json:"descriptions"`
			Metrics struct {
				V31 []nvdCVSSMetric `json:"cvssMetricV31"`
				V40 []nvdCVSSMetric `json:"cvssMetricV40"`
				V30 []nvdCVSSMetric `json:"cvssMetricV30"`
			} `json:"metrics"`
		} `json:"cve"`
	} `json:"vulnerabilities"`
}

// pickCVSS returns the first available score, preferring CVSS v3.1, then
// v4.0, then v3.0, and within one version NVD's own "Primary" entry over
// CNA "Secondary" ones. Unscored CVEs return (0, "").
func pickCVSS(groups ...[]nvdCVSSMetric) (float64, string) {
	for _, g := range groups {
		if len(g) == 0 {
			continue
		}
		chosen := g[0]
		for _, m := range g {
			if m.Type == "Primary" {
				chosen = m
				break
			}
		}
		return chosen.CVSSData.BaseScore, strings.ToUpper(chosen.CVSSData.BaseSeverity)
	}
	return 0, ""
}

func parseNVD(body []byte, now time.Time) ([]SentinelItem, error) {
	var payload nvdResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode nvd response: %w", err)
	}

	items := make([]SentinelItem, 0, len(payload.Vulnerabilities))
	for _, v := range payload.Vulnerabilities {
		description := ""
		for _, d := range v.CVE.Descriptions {
			if d.Lang == "en" {
				description = d.Value
				break
			}
		}
		if description == "" && len(v.CVE.Descriptions) > 0 {
			description = v.CVE.Descriptions[0].Value
		}

		score, severity := pickCVSS(v.CVE.Metrics.V31, v.CVE.Metrics.V40, v.CVE.Metrics.V30)
		title := fmt.Sprintf("%s: %s", v.CVE.ID, truncate(description, 80))

		items = append(items, SentinelItem{
			SourceType:  "cve",
			Identifier:  v.CVE.ID,
			Title:       title,
			Description: description,
			URL:         fmt.Sprintf("https://nvd.nist.gov/vuln/detail/%s", v.CVE.ID),
			PublishedAt: parseFlexibleTime(v.CVE.Published, now),
			Keywords:    extractKeywords(title + " " + description),
			RawContent:  description,
			CVEID:       v.CVE.ID,
			Severity:    severity,
			CVSSScore:   score,
		})
	}
	return items, nil
}

func parseKEV(body []byte, cutoff time.Time) ([]SentinelItem, error) {
	var payload struct {
		Vulnerabilities []struct {
			CveID                      string `json:"cveID"`
			VendorProject              string `json:"vendorProject"`
			Product                    string `json:"product"`
			VulnerabilityName          string `json:"vulnerabilityName"`
			DateAdded                  string `json:"dateAdded"`
			ShortDescription           string `json:"shortDescription"`
			RequiredAction             string `json:"requiredAction"`
			DueDate                    string `json:"dueDate"`
			KnownRansomwareCampaignUse string `json:"knownRansomwareCampaignUse"`
		} `json:"vulnerabilities"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode cisa kev response: %w", err)
	}

	items := make([]SentinelItem, 0)
	for _, v := range payload.Vulnerabilities {
		dateAdded, err := time.Parse("2006-01-02", v.DateAdded)
		if err != nil || dateAdded.Before(cutoff) {
			continue
		}

		keywordText := v.VulnerabilityName + " " + v.ShortDescription + " " + v.VendorProject + " " + v.Product
		items = append(items, SentinelItem{
			SourceType:     "cisa_kev",
			Identifier:     v.CveID,
			Title:          v.VulnerabilityName,
			Description:    v.ShortDescription,
			URL:            fmt.Sprintf("https://nvd.nist.gov/vuln/detail/%s", v.CveID),
			PublishedAt:    dateAdded,
			Keywords:       extractKeywords(keywordText),
			RawContent:     v.ShortDescription,
			CVEID:          v.CveID,
			KnownExploited: true,
			RequiredAction: v.RequiredAction,
			DueDate:        v.DueDate,
			Ransomware:     v.KnownRansomwareCampaignUse == "Known",
		})
	}
	return items, nil
}

func parseGitHubAdvisories(body []byte, now time.Time) ([]SentinelItem, error) {
	var payload []struct {
		GHSAID      string `json:"ghsa_id"`
		CVEID       string `json:"cve_id"`
		Summary     string `json:"summary"`
		Description string `json:"description"`
		HTMLURL     string `json:"html_url"`
		PublishedAt string `json:"published_at"`
		Severity    string `json:"severity"`
		CVSS        struct {
			Score float64 `json:"score"`
		} `json:"cvss"`
		CWEs []struct {
			Name string `json:"name"`
		} `json:"cwes"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode github advisories response: %w", err)
	}

	items := make([]SentinelItem, 0, len(payload))
	for _, a := range payload {
		cweNames := make([]string, 0, len(a.CWEs))
		for _, c := range a.CWEs {
			cweNames = append(cweNames, c.Name)
		}

		severity := strings.ToUpper(a.Severity)
		if severity == "UNKNOWN" {
			severity = ""
		}

		items = append(items, SentinelItem{
			SourceType:  "github_advisory",
			Identifier:  a.GHSAID,
			Title:       a.Summary,
			Description: a.Description,
			URL:         a.HTMLURL,
			PublishedAt: parseFlexibleTime(a.PublishedAt, now),
			Keywords:    extractKeywords(a.Summary + " " + a.Description + " " + strings.Join(cweNames, " ")),
			RawContent:  a.Description,
			CVEID:       a.CVEID,
			Severity:    severity,
			CVSSScore:   a.CVSS.Score,
		})
	}
	return items, nil
}
