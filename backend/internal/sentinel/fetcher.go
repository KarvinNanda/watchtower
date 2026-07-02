// Package sentinel polls threat-intel sources (NVD/NIST, CISA KEV, RSS
// feeds, GitHub repositories/advisories, Exploit-DB), and produces
// AI-assisted security analysis per item via DeepSeek.
package sentinel

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	fetcherHTTPTimeout = 30 * time.Second
	lookbackWindow     = 7 * 24 * time.Hour

	nvdAPIURL         = "https://services.nvd.nist.gov/rest/json/cves/2.0"
	nvdRateLimitDelay = 6 * time.Second

	cisaKEVFeedURL = "https://www.cisa.gov/sites/default/files/feeds/known_exploited_vulnerabilities.json"

	bleepingComputerRSS = "https://www.bleepingcomputer.com/feed/"
	theHackerNewsRSS    = "https://feeds.feedburner.com/TheHackersNews"
	exploitDBRSS        = "https://www.exploit-db.com/rss.xml"

	githubRepoSearchURL = "https://api.github.com/search/repositories"
	githubAdvisoriesURL = "https://api.github.com/advisories"
)

// SentinelItem is a normalized security item collected from any source.
type SentinelItem struct {
	SourceType  string
	Identifier  string
	Title       string
	Description string
	URL         string
	PublishedAt time.Time
	Keywords    []string
	RawContent  string
}

// SentinelFetcher polls all six WatchTower threat-intel sources: NVD/NIST,
// CISA KEV, RSS feeds, GitHub repository search, Exploit-DB, and GitHub
// Security Advisories.
type SentinelFetcher struct {
	githubToken    string
	httpClient     *http.Client
	githubWarnOnce sync.Once
}

// NewSentinelFetcher builds a SentinelFetcher. githubToken is optional —
// when set, it raises GitHub API rate limits; when empty, GitHub calls run
// unauthenticated (10 requests/minute) and a warning is logged.
func NewSentinelFetcher(githubToken string) *SentinelFetcher {
	return &SentinelFetcher{
		githubToken: githubToken,
		httpClient:  &http.Client{Timeout: fetcherHTTPTimeout},
	}
}

// SetHTTPClient overrides the fetcher's HTTP client, primarily for tests.
func (f *SentinelFetcher) SetHTTPClient(client *http.Client) {
	f.httpClient = client
}

// FetchNVD retrieves CVEs published in the last 7 days from NVD/NIST.
func (f *SentinelFetcher) FetchNVD() ([]SentinelItem, error) {
	const nvdTimeFormat = "2006-01-02T15:04:05.000"

	now := time.Now().UTC()
	start := now.Add(-lookbackWindow)

	endpoint := fmt.Sprintf("%s?pubStartDate=%s&pubEndDate=%s&resultsPerPage=50",
		nvdAPIURL,
		url.QueryEscape(start.Format(nvdTimeFormat)),
		url.QueryEscape(now.Format(nvdTimeFormat)),
	)

	var payload struct {
		Vulnerabilities []struct {
			CVE struct {
				ID           string `json:"id"`
				Published    string `json:"published"`
				Descriptions []struct {
					Lang  string `json:"lang"`
					Value string `json:"value"`
				} `json:"descriptions"`
			} `json:"cve"`
		} `json:"vulnerabilities"`
	}

	err := f.getJSON(endpoint, nil, &payload)

	// NVD's public (unauthenticated) rate limit is roughly 5 requests per
	// rolling 30 seconds; pause after every call to stay well within it.
	time.Sleep(nvdRateLimitDelay)

	if err != nil {
		return nil, fmt.Errorf("sentinel: fetch nvd: %w", err)
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

		title := fmt.Sprintf("%s: %s", v.CVE.ID, truncate(description, 80))
		rawContent := description

		items = append(items, SentinelItem{
			SourceType:  "cve",
			Identifier:  v.CVE.ID,
			Title:       title,
			Description: description,
			URL:         fmt.Sprintf("https://nvd.nist.gov/vuln/detail/%s", v.CVE.ID),
			PublishedAt: parseFlexibleTime(v.CVE.Published, now),
			Keywords:    extractKeywords(title + " " + description + " " + rawContent),
			RawContent:  rawContent,
		})
	}

	return items, nil
}

// FetchCISAKEV retrieves CISA Known Exploited Vulnerabilities added in the
// last 7 days.
func (f *SentinelFetcher) FetchCISAKEV() ([]SentinelItem, error) {
	var payload struct {
		Vulnerabilities []struct {
			CveID             string `json:"cveID"`
			VendorProject     string `json:"vendorProject"`
			Product           string `json:"product"`
			VulnerabilityName string `json:"vulnerabilityName"`
			DateAdded         string `json:"dateAdded"`
			ShortDescription  string `json:"shortDescription"`
		} `json:"vulnerabilities"`
	}

	if err := f.getJSON(cisaKEVFeedURL, nil, &payload); err != nil {
		return nil, fmt.Errorf("sentinel: fetch cisa kev: %w", err)
	}

	cutoff := time.Now().Add(-lookbackWindow)

	items := make([]SentinelItem, 0)
	for _, v := range payload.Vulnerabilities {
		dateAdded, err := time.Parse("2006-01-02", v.DateAdded)
		if err != nil || dateAdded.Before(cutoff) {
			continue
		}

		title := v.VulnerabilityName
		description := v.ShortDescription
		rawContent := v.ShortDescription
		keywordText := title + " " + description + " " + rawContent + " " + v.VendorProject + " " + v.Product

		items = append(items, SentinelItem{
			SourceType:  "cisa_kev",
			Identifier:  v.CveID,
			Title:       title,
			Description: description,
			URL:         fmt.Sprintf("https://nvd.nist.gov/vuln/detail/%s", v.CveID),
			PublishedAt: dateAdded,
			Keywords:    extractKeywords(keywordText),
			RawContent:  rawContent,
		})
	}

	return items, nil
}

type rssDocument struct {
	Channel struct {
		Items []rssItem `xml:"item"`
	} `xml:"channel"`
}

type rssItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	Description string `xml:"description"`
	PubDate     string `xml:"pubDate"`
	GUID        string `xml:"guid"`
}

type atomDocument struct {
	Entries []atomEntry `xml:"entry"`
}

type atomEntry struct {
	Title     string `xml:"title"`
	Summary   string `xml:"summary"`
	Published string `xml:"published"`
	Updated   string `xml:"updated"`
	ID        string `xml:"id"`
	Links     []struct {
		Href string `xml:"href,attr"`
		Rel  string `xml:"rel,attr"`
	} `xml:"link"`
}

// FetchRSS retrieves and parses items from an RSS 2.0 or Atom feed URL,
// filtering to entries published in the last 7 days.
func (f *SentinelFetcher) FetchRSS(feedURL string) ([]SentinelItem, error) {
	body, err := f.getBody(feedURL, nil)
	if err != nil {
		return nil, fmt.Errorf("sentinel: fetch rss %s: %w", feedURL, err)
	}

	cutoff := time.Now().Add(-lookbackWindow)

	var rss rssDocument
	_ = xml.Unmarshal(body, &rss)

	if len(rss.Channel.Items) > 0 {
		items := make([]SentinelItem, 0, len(rss.Channel.Items))
		for _, it := range rss.Channel.Items {
			published := parseFlexibleTime(it.PubDate, time.Now())
			if published.Before(cutoff) {
				continue
			}

			identifier := it.Link
			if identifier == "" {
				identifier = it.GUID
			}

			rawContent := it.Description

			items = append(items, SentinelItem{
				SourceType:  "rss",
				Identifier:  identifier,
				Title:       it.Title,
				Description: it.Description,
				URL:         it.Link,
				PublishedAt: published,
				Keywords:    extractKeywords(it.Title + " " + it.Description + " " + rawContent),
				RawContent:  rawContent,
			})
		}
		return items, nil
	}

	var atom atomDocument
	if err := xml.Unmarshal(body, &atom); err != nil {
		return nil, fmt.Errorf("sentinel: decode feed %s: %w", feedURL, err)
	}

	items := make([]SentinelItem, 0, len(atom.Entries))
	for _, e := range atom.Entries {
		dateStr := e.Published
		if dateStr == "" {
			dateStr = e.Updated
		}
		published := parseFlexibleTime(dateStr, time.Now())
		if published.Before(cutoff) {
			continue
		}

		link := e.ID
		for _, l := range e.Links {
			if l.Rel == "" || l.Rel == "alternate" {
				link = l.Href
				break
			}
		}

		rawContent := e.Summary

		items = append(items, SentinelItem{
			SourceType:  "rss",
			Identifier:  link,
			Title:       e.Title,
			Description: e.Summary,
			URL:         link,
			PublishedAt: published,
			Keywords:    extractKeywords(e.Title + " " + e.Summary + " " + rawContent),
			RawContent:  rawContent,
		})
	}

	return items, nil
}

const (
	githubRepoResultsPerQuery = 10
	githubRepoMaxResults      = 30
	githubQueryDelay          = 1 * time.Second
)

// FetchGitHubRepos searches for repositories tagged with CVE, exploit, or
// security-vulnerability topics created in the last 7 days. It runs three
// separate, broader queries rather than one query ANDing all three topics
// together (which was so restrictive it rarely matched any repo), then
// merges and deduplicates the results by full_name, capped at 30 items.
func (f *SentinelFetcher) FetchGitHubRepos() ([]SentinelItem, error) {
	f.warnIfNoGitHubToken()

	cutoff := time.Now().Add(-lookbackWindow).Format("2006-01-02")

	queries := []string{
		fmt.Sprintf("topic:cve created:>%s", cutoff),
		fmt.Sprintf("topic:exploit created:>%s", cutoff),
		fmt.Sprintf("topic:security-vulnerability created:>%s", cutoff),
	}

	var all []SentinelItem
	for i, query := range queries {
		if i > 0 {
			// Avoid GitHub's search rate limit between successive queries.
			time.Sleep(githubQueryDelay)
		}

		items, err := f.searchGitHubRepos(query)
		if err != nil {
			log.Printf("[ERROR] FetchGitHubRepos: query %q failed: %v", query, err)
			continue
		}
		all = append(all, items...)
	}

	deduped := dedupeByIdentifier(all)
	if len(deduped) > githubRepoMaxResults {
		deduped = deduped[:githubRepoMaxResults]
	}

	return deduped, nil
}

func (f *SentinelFetcher) searchGitHubRepos(query string) ([]SentinelItem, error) {
	endpoint := fmt.Sprintf("%s?q=%s&sort=stars&order=desc&per_page=%d",
		githubRepoSearchURL, url.QueryEscape(query), githubRepoResultsPerQuery)

	var payload struct {
		Items []struct {
			FullName    string   `json:"full_name"`
			Description string   `json:"description"`
			HTMLURL     string   `json:"html_url"`
			Topics      []string `json:"topics"`
			CreatedAt   string   `json:"created_at"`
		} `json:"items"`
	}

	if err := f.getJSON(endpoint, f.githubHeaders(), &payload); err != nil {
		return nil, fmt.Errorf("sentinel: fetch github repos: %w", err)
	}

	items := make([]SentinelItem, 0, len(payload.Items))
	for _, r := range payload.Items {
		title := fmt.Sprintf("%s: %s", r.FullName, r.Description)
		description := r.Description
		rawContent := r.Description

		keywords := dedupeStrings(append(append([]string{}, r.Topics...),
			extractKeywords(title+" "+description+" "+rawContent+" "+strings.Join(r.Topics, " "))...))

		items = append(items, SentinelItem{
			SourceType:  "github",
			Identifier:  r.FullName,
			Title:       title,
			Description: description,
			URL:         r.HTMLURL,
			PublishedAt: parseFlexibleTime(r.CreatedAt, time.Now()),
			Keywords:    keywords,
			RawContent:  rawContent,
		})
	}

	return items, nil
}

// FetchExploitDB retrieves the latest exploits from Exploit-DB's RSS feed.
func (f *SentinelFetcher) FetchExploitDB() ([]SentinelItem, error) {
	items, err := f.FetchRSS(exploitDBRSS)
	if err != nil {
		return nil, fmt.Errorf("sentinel: fetch exploit-db: %w", err)
	}

	for i := range items {
		items[i].SourceType = "exploit_db"
	}

	return items, nil
}

// FetchGitHubAdvisories retrieves reviewed GitHub Security Advisories
// published in the last 7 days.
func (f *SentinelFetcher) FetchGitHubAdvisories() ([]SentinelItem, error) {
	f.warnIfNoGitHubToken()

	cutoff := time.Now().Add(-lookbackWindow).Format("2006-01-02")
	endpoint := fmt.Sprintf("%s?type=reviewed&per_page=30&published=%s",
		githubAdvisoriesURL, url.QueryEscape(">"+cutoff))

	var payload []struct {
		GHSAID      string `json:"ghsa_id"`
		Summary     string `json:"summary"`
		Description string `json:"description"`
		HTMLURL     string `json:"html_url"`
		PublishedAt string `json:"published_at"`
		CWEs        []struct {
			CWEID string `json:"cwe_id"`
			Name  string `json:"name"`
		} `json:"cwes"`
	}

	if err := f.getJSON(endpoint, f.githubHeaders(), &payload); err != nil {
		return nil, fmt.Errorf("sentinel: fetch github advisories: %w", err)
	}

	items := make([]SentinelItem, 0, len(payload))
	for _, a := range payload {
		var cweNames []string
		for _, c := range a.CWEs {
			cweNames = append(cweNames, c.Name)
		}

		title := a.Summary
		description := a.Description
		rawContent := a.Description
		keywordText := title + " " + description + " " + rawContent + " " + strings.Join(cweNames, " ")

		items = append(items, SentinelItem{
			SourceType:  "github_advisory",
			Identifier:  a.GHSAID,
			Title:       title,
			Description: description,
			URL:         a.HTMLURL,
			PublishedAt: parseFlexibleTime(a.PublishedAt, time.Now()),
			Keywords:    extractKeywords(keywordText),
			RawContent:  rawContent,
		})
	}

	return items, nil
}

// FetchAll polls all six sources, isolating failures (and panics) so a
// single broken source never stops the others, then deduplicates the
// combined result by Identifier.
func (f *SentinelFetcher) FetchAll() ([]SentinelItem, error) {
	type namedSource struct {
		name  string
		fetch func() ([]SentinelItem, error)
	}

	sources := []namedSource{
		{"nvd", f.FetchNVD},
		{"cisa_kev", f.FetchCISAKEV},
		{"rss:bleepingcomputer", func() ([]SentinelItem, error) { return f.FetchRSS(bleepingComputerRSS) }},
		{"rss:thehackernews", func() ([]SentinelItem, error) { return f.FetchRSS(theHackerNewsRSS) }},
		{"github_repos", f.FetchGitHubRepos},
		{"exploit_db", f.FetchExploitDB},
		{"github_advisories", f.FetchGitHubAdvisories},
	}

	var all []SentinelItem
	for _, src := range sources {
		all = append(all, safeFetch(src.name, src.fetch)...)
	}

	return dedupeByIdentifier(all), nil
}

// safeFetch runs fn, recovering from any panic and logging both panics and
// errors, so FetchAll never stops (or crashes) because of one bad source.
func safeFetch(name string, fn func() ([]SentinelItem, error)) (items []SentinelItem) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[ERROR] FetchAll: recovered from panic in source %s: %v", name, r)
			items = nil
		}
	}()

	result, err := fn()
	if err != nil {
		log.Printf("[ERROR] FetchAll: source %s failed: %v", name, err)
		return nil
	}
	return result
}

func dedupeByIdentifier(items []SentinelItem) []SentinelItem {
	seen := make(map[string]struct{}, len(items))
	result := make([]SentinelItem, 0, len(items))
	for _, item := range items {
		if item.Identifier == "" {
			continue
		}
		if _, ok := seen[item.Identifier]; ok {
			continue
		}
		seen[item.Identifier] = struct{}{}
		result = append(result, item)
	}
	return result
}

func (f *SentinelFetcher) githubHeaders() map[string]string {
	headers := map[string]string{
		"Accept": "application/vnd.github+json",
	}
	if f.githubToken != "" {
		headers["Authorization"] = "Bearer " + f.githubToken
	}
	return headers
}

// warnIfNoGitHubToken logs the unauthenticated-rate-limit warning at most
// once per SentinelFetcher, regardless of how many GitHub-calling methods
// (or, per FetchGitHubRepos, how many queries within one method) run in a
// given fetch cycle — otherwise the identical message would print once per
// call and look like duplicated output.
func (f *SentinelFetcher) warnIfNoGitHubToken() {
	if f.githubToken == "" {
		f.githubWarnOnce.Do(func() {
			log.Println("[WARN] GitHub rate limit: 10 req/menit")
		})
	}
}

func (f *SentinelFetcher) getBody(endpoint string, headers map[string]string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	// Some feed hosts (e.g. BleepingComputer) block requests carrying Go's
	// default "Go-http-client" User-Agent; a browser-like one avoids that
	// without misrepresenting the request in any functional way.
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; WatchTowerBot/1.0)")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := f.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	return body, nil
}

func (f *SentinelFetcher) getJSON(endpoint string, headers map[string]string, dest interface{}) error {
	body, err := f.getBody(endpoint, headers)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, dest); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

var feedTimeLayouts = []string{
	time.RFC3339,
	time.RFC3339Nano,
	time.RFC1123Z,
	time.RFC1123,
	"2006-01-02T15:04:05.000",
	"2006-01-02T15:04:05",
	"2006-01-02",
}

// parseFlexibleTime tries a handful of common feed/API timestamp formats,
// returning fallback (rather than erroring) if none match — a single
// unparseable timestamp should never take down a whole fetch.
func parseFlexibleTime(s string, fallback time.Time) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return fallback
	}
	for _, layout := range feedTimeLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	log.Printf("[WARN] parseFlexibleTime: could not parse %q, using fallback", s)
	return fallback
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return strings.TrimSpace(s[:maxLen]) + "..."
}

func dedupeStrings(ss []string) []string {
	seen := make(map[string]struct{}, len(ss))
	result := make([]string, 0, len(ss))
	for _, s := range ss {
		s = strings.ToLower(strings.TrimSpace(s))
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		result = append(result, s)
	}
	return result
}

// sentinelKeywordList is WatchTower's curated list of platform/technology
// keywords checked against fetched item text.
var sentinelKeywordList = []string{
	// platform
	"android", "ios", "windows", "linux", "macos", "ubuntu", "debian", "redhat", "centos", "unix",
	// browser
	"chrome", "firefox", "safari", "edge", "webkit",
	// server
	"apache", "nginx", "iis", "tomcat", "wordpress", "drupal", "joomla",
	// runtime
	"java", "python", "php", "nodejs", "ruby", "golang",
	// database
	"mysql", "postgresql", "mongodb", "redis", "sqlite", "oracle", "mssql",
	// network
	"vpn", "firewall", "router", "cisco", "juniper", "fortinet", "paloalto",
	// security
	"ransomware", "phishing", "malware", "backdoor", "rootkit", "exploit", "rce", "sqli", "xss", "csrf",
	"privilege", "escalation", "injection", "overflow", "bypass", "authentication",
	// vendor
	"microsoft", "apple", "google", "samsung", "toshiba", "dell", "hp", "lenovo", "asus",
	"qualcomm", "broadcom", "intel", "amd",
}

// extractKeywords returns every keyword from sentinelKeywordList that
// appears anywhere (case-insensitive substring match) in text, deduplicated
// and in a stable order. Callers should pass in every relevant text field
// (title + description + rawContent, plus any source-specific extras) so
// keywords aren't missed just because they only appear in one field.
// Returns an empty (non-nil) slice, never nil, when nothing matches.
func extractKeywords(text string) []string {
	lowerText := strings.ToLower(text)

	matched := make([]string, 0)
	seen := make(map[string]struct{}, len(sentinelKeywordList))

	for _, kw := range sentinelKeywordList {
		if _, ok := seen[kw]; ok {
			continue
		}
		if strings.Contains(lowerText, strings.ToLower(kw)) {
			matched = append(matched, kw)
			seen[kw] = struct{}{}
		}
	}

	return matched
}
