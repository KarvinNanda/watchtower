package alertmsg

import (
	"fmt"
	"math"
	"net/url"
	"sort"
	"strings"
	"time"

	"golang.org/x/net/idna"
)

const (
	titleMaxRunes   = 60
	excerptMaxRunes = 200
	// maxLinkURLBytes bounds the URL inside a link. Longer URLs (usually
	// tracking links) are replaced by the site root so one item cannot push
	// its message part past Telegram's limit.
	maxLinkURLBytes = 1000
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
	// CVSSVector is the raw vector string; it feeds the impact and attack lines.
	CVSSVector string
	Affected   []AffectedVersion
	// EPSS is a probability from 0 to 1; nil means unknown (no line).
	EPSS *float64
	// PublishedAt is when the source published the item; zero means no line.
	PublishedAt time.Time
}

// AffectedVersion is one affected product and version range. It is
// alertmsg's own copy so this package does not import internal/sentinel.
// Fixed is empty when no safe version is known. NoFix is true only when the
// source says no fixed version exists yet.
type AffectedVersion struct {
	Product string
	Range   string
	Fixed   string
	NoFix   bool
}

const (
	productMaxRunes  = 40
	versionMaxRunes  = 30
	maxAffectedLines = 2
)

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
	if v, ok := parseCVSSVector(e.CVSSVector); ok {
		for _, l := range []string{impactLine(lang, v), attackLine(lang, v)} {
			if l != "" {
				lines = append(lines, esc(l))
			}
		}
	}
	for _, l := range affectedLines(lang, e.Affected) {
		lines = append(lines, esc(l))
	}
	if l := epssLine(lang, e.EPSS); l != "" {
		lines = append(lines, esc(l))
	}
	if l := publishedLine(lang, e.PublishedAt); l != "" {
		lines = append(lines, esc(l))
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
	sev := strings.ToUpper(e.Severity)
	if sev == "" {
		sev = cvssBand(e.CVSSScore)
	}
	switch sev {
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

// cvssBand maps a CVSS v3 base score to its qualitative rating, for items
// whose source gives a score but no severity label.
func cvssBand(score float64) string {
	switch {
	case score >= 9.0:
		return "CRITICAL"
	case score >= 7.0:
		return "HIGH"
	case score >= 4.0:
		return "MEDIUM"
	case score > 0:
		return "LOW"
	}
	return ""
}

func severityText(e SentinelEntry) string {
	sev := strings.ToUpper(e.Severity)
	switch {
	case e.KnownExploited && sev != "" && e.CVSSScore > 0:
		return fmt.Sprintf("ACTIVELY EXPLOITED · %s %.1f", sev, e.CVSSScore)
	case e.KnownExploited && e.CVSSScore > 0:
		return fmt.Sprintf("ACTIVELY EXPLOITED · %.1f", e.CVSSScore)
	case e.KnownExploited:
		return "ACTIVELY EXPLOITED"
	case sev != "" && e.CVSSScore > 0:
		return fmt.Sprintf("%s %.1f", sev, e.CVSSScore)
	case sev != "":
		return sev
	case e.CVSSScore > 0:
		return fmt.Sprintf("%.1f", e.CVSSScore)
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
// site. Non-ASCII hosts are shown in their punycode (xn--) form: Telegram
// would auto-link a plain-text URL anyway, and a homograph such as a
// Cyrillic "а" must not look like the real domain. Anything that is not
// http(s), or whose host is not a valid domain, gets no link at all.
// A URL over maxLinkURLBytes links to the site root instead.
func linkLine(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	if !isASCII(host) {
		if host, err = idna.Lookup.ToASCII(host); err != nil {
			return ""
		}
	}
	target := raw
	if len(target) > maxLinkURLBytes {
		target = u.Scheme + "://" + u.Host + "/"
	}
	return "🔗 [" + esc(host) + "](" + escapeLinkURL(target) + ")"
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// affectedLines returns up to two "📦" lines, then one "+N more" line. An
// entry without a Range says nothing useful, so it is skipped and does not
// count toward the limit or the "+N" total. Text is unescaped.
func affectedLines(lang string, av []AffectedVersion) []string {
	var out []string
	rest := 0
	for _, a := range av {
		if a.Range == "" {
			continue
		}
		if len(out) >= maxAffectedLines {
			rest++
			continue
		}
		rng := TruncateRunes(a.Range, versionMaxRunes)
		var phrase string
		if strings.ContainsRune("<>≤≥=", []rune(a.Range)[0]) {
			phrase = pick(lang, "affected ", "terdampak ") + rng
		} else {
			phrase = pick(lang, "affected version ", "terdampak versi ") + rng
		}
		line := "📦 " + TruncateRunes(a.Product, productMaxRunes) + ": " + phrase
		switch {
		case a.Fixed != "":
			line += pick(lang, " · safe from ", " · aman mulai ") + TruncateRunes(a.Fixed, versionMaxRunes)
		case a.NoFix:
			line += pick(lang, " · no fixed version yet", " · belum ada versi aman")
		}
		out = append(out, line)
	}
	if rest > 0 {
		out = append(out, fmt.Sprintf("📦 +%d %s", rest, pick(lang, "more", "lainnya")))
	}
	return out
}

// epssLine shows the exploitation probability. The percent is floored so a
// probability just under 1 never reads as a certain 100%.
func epssLine(lang string, p *float64) string {
	if p == nil {
		return ""
	}
	v := *p
	if math.IsNaN(v) || v < 0 {
		v = 0
	}
	if v > 1 {
		v = 1
	}
	// Round away float error (0.29*100 is 28.999999999999996) before flooring.
	pct := math.Round(v*100*1e6) / 1e6
	var s string
	switch {
	case pct >= 1:
		s = fmt.Sprintf("%d%%", int(math.Floor(pct)))
	case pct >= 0.1:
		s = fmt.Sprintf("%.1f%%", math.Floor(pct*10)/10)
	default:
		s = "< 0.1%"
	}
	return "📈 EPSS: " + s + pick(lang, " chance of exploitation in the next 30 days", " peluang dieksploitasi dalam 30 hari")
}

func publishedLine(lang string, t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return pick(lang, "📅 Published: ", "📅 Dipublikasi: ") + formatDueDate(t.UTC().Format("2006-01-02"), lang)
}
