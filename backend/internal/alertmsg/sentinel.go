package alertmsg

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
)

const (
	titleMaxRunes   = 60
	excerptMaxRunes = 200
)

var sourceNames = map[string]string{
	"cve":             "NVD",
	"cisa_kev":        "CISA KEV",
	"github_advisory": "GitHub Advisory",
	"github":          "GitHub",
	"exploit_db":      "Exploit-DB",
	"rss":             "RSS",
}

// SentinelEntry is one item matched for one user. Source content (title,
// description, required action) is shown as published.
type SentinelEntry struct {
	SourceType      string
	Title           string
	Description     string
	URL             string
	CVEID           string
	Severity        string
	CVSSScore       float64
	KnownExploited  bool
	RequiredAction  string
	DueDate         string
	Ransomware      bool
	MatchedKeywords []string
}

// SentinelMessages renders one user's matched items, most urgent first.
// at must already be in the instance timezone.
func SentinelMessages(lang string, at time.Time, entries []SentinelEntry) []string {
	if len(entries) == 0 {
		return nil
	}
	sorted := append([]SentinelEntry(nil), entries...)
	sort.SliceStable(sorted, func(i, j int) bool { return sentinelLess(sorted[i], sorted[j]) })

	blocks := make([]string, len(sorted))
	kev := 0
	for i, e := range sorted {
		blocks[i] = sentinelBlock(lang, e)
		if e.KnownExploited {
			kev++
		}
	}
	h := header{
		first: "🛡️ *Sentinel* · " + esc(sentinelSummary(lang, len(sorted), kev)),
		lines: []string{sentAtLine(at, lang)},
	}
	return pack(lang, h, blocks)
}

func sentinelSummary(lang string, n, kev int) string {
	noun := "item"
	if isEN(lang) && n != 1 {
		noun = "items"
	}
	s := fmt.Sprintf("%d %s", n, noun)
	if kev > 0 {
		s += fmt.Sprintf(" (%d actively exploited)", kev)
	}
	return s
}

// tier: 0 known exploited, 1 scored, 2 severity text only, 3 none.
func tier(e SentinelEntry) int {
	switch {
	case e.KnownExploited:
		return 0
	case e.CVSSScore > 0:
		return 1
	case e.Severity != "":
		return 2
	}
	return 3
}

func severityRank(s string) int {
	switch strings.ToUpper(s) {
	case "CRITICAL":
		return 4
	case "HIGH":
		return 3
	case "MEDIUM", "MODERATE":
		return 2
	case "LOW":
		return 1
	}
	return 0
}

func sentinelLess(a, b SentinelEntry) bool {
	if ta, tb := tier(a), tier(b); ta != tb {
		return ta < tb
	}
	if a.CVSSScore != b.CVSSScore {
		return a.CVSSScore > b.CVSSScore
	}
	if ra, rb := severityRank(a.Severity), severityRank(b.Severity); ra != rb {
		return ra > rb
	}
	if (a.CVEID == "") != (b.CVEID == "") {
		return a.CVEID != "" // a tracked CVE before an untracked item
	}
	if a.CVEID != b.CVEID {
		return a.CVEID < b.CVEID
	}
	return a.Title < b.Title
}

func sourceName(sourceType string) string {
	if name := sourceNames[sourceType]; name != "" {
		return name
	}
	return sourceType
}

func sentinelBlock(lang string, e SentinelEntry) string {
	title := TruncateRunes(e.Title, titleMaxRunes)
	id, showTitle := e.CVEID, true
	if id == "" {
		id, showTitle = title, false
	}
	if id == "" {
		id = sourceName(e.SourceType)
	}

	headline := severityEmoji(e) + " *" + esc(id) + "*"
	if sev := severityText(e); sev != "" {
		headline += " · " + esc(sev)
	}
	lines := []string{headline}
	if showTitle && title != "" {
		lines = append(lines, esc(title))
	}
	if e.Description != "" && e.Description != e.Title {
		lines = append(lines, esc(TruncateRunes(e.Description, excerptMaxRunes)))
	}
	if e.RequiredAction != "" {
		lines = append(lines, esc(pick(lang, "Action: ", "Aksi: ")+TruncateRunes(e.RequiredAction, excerptMaxRunes)))
	}
	var kev []string
	if e.DueDate != "" {
		kev = append(kev, pick(lang, "Due: ", "Batas waktu: ")+formatDueDate(e.DueDate, lang))
	}
	if e.Ransomware {
		kev = append(kev, "Ransomware: "+pick(lang, "yes", "ya"))
	}
	if len(kev) > 0 {
		lines = append(lines, esc(strings.Join(kev, " · ")))
	}
	lines = append(lines, "🏷️ "+esc(tagLine(e)))
	if link := linkLine(e.URL); link != "" {
		lines = append(lines, link)
	}
	return strings.Join(lines, "\n")
}

func severityEmoji(e SentinelEntry) string {
	if e.KnownExploited {
		return "🚨"
	}
	switch strings.ToUpper(e.Severity) {
	case "CRITICAL":
		return "🔴"
	case "HIGH":
		return "🟠"
	case "MEDIUM", "MODERATE":
		return "🟡"
	case "LOW":
		return "🟢"
	}
	return "⚪"
}

func severityText(e SentinelEntry) string {
	sev := strings.ToUpper(e.Severity)
	switch {
	case e.KnownExploited:
		return "ACTIVELY EXPLOITED"
	case sev != "" && e.CVSSScore > 0:
		return fmt.Sprintf("%s %.1f", sev, e.CVSSScore)
	case sev != "":
		return sev
	case e.SourceType == "cve" || e.SourceType == "github_advisory":
		return "N/A"
	}
	return ""
}

func tagLine(e SentinelEntry) string {
	if len(e.MatchedKeywords) == 0 {
		return sourceName(e.SourceType)
	}
	return strings.Join(e.MatchedKeywords, ", ") + " · " + sourceName(e.SourceType)
}

// linkLine renders "🔗 [host](url)". The link text is the host parsed from
// the same URL (userinfo is not part of it), so it cannot claim a different
// site. A non-ASCII host (possible homograph) is shown as the full URL in
// plain text instead; anything that is not http(s) gets no link at all.
func linkLine(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	if !isASCII(host) {
		return "🔗 " + esc(raw)
	}
	return "🔗 [" + esc(host) + "](" + escapeLinkURL(raw) + ")"
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}
