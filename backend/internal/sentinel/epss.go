package sentinel

import (
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// epssChunkSize matches FIRST.org's default page size, so one chunk is one
// page and never needs paging.
const epssChunkSize = 100

// epssAPIURL is a var so tests can point it at an httptest.Server.
var epssAPIURL = "https://api.first.org/data/v1/epss"

// cveIDPattern is the only shape of CVE ID allowed into a URL.
var cveIDPattern = regexp.MustCompile(`^CVE-\d{4}-\d{4,}$`)

// epssChunks drops invalid and duplicate IDs and splits the rest into
// chunks of at most epssChunkSize.
func epssChunks(ids []string) [][]string {
	seen := make(map[string]struct{}, len(ids))
	var chunks [][]string
	var cur []string
	for _, id := range ids {
		if !cveIDPattern.MatchString(id) {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		cur = append(cur, id)
		if len(cur) == epssChunkSize {
			chunks = append(chunks, cur)
			cur = nil
		}
	}
	if len(cur) > 0 {
		chunks = append(chunks, cur)
	}
	return chunks
}

// parseEPSS maps CVE ID to EPSS score. Entries with an unparseable score
// are skipped.
func parseEPSS(body []byte) (map[string]float64, error) {
	var resp struct {
		Data []struct {
			CVE  string `json:"cve"`
			EPSS string `json:"epss"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("sentinel: parse epss: %w", err)
	}
	out := make(map[string]float64, len(resp.Data))
	for _, d := range resp.Data {
		score, err := strconv.ParseFloat(d.EPSS, 64)
		if err != nil {
			continue
		}
		out[d.CVE] = score
	}
	return out, nil
}

// FetchEPSS gets EPSS scores from FIRST.org, one request per chunk of IDs.
// If a chunk fails it still returns every score gathered so far, together
// with the error.
func (f *SentinelFetcher) FetchEPSS(cveIDs []string) (map[string]float64, error) {
	scores := make(map[string]float64)
	var firstErr error
	for _, chunk := range epssChunks(cveIDs) {
		q := url.Values{}
		q.Set("cve", strings.Join(chunk, ","))
		body, err := f.getBody(epssAPIURL+"?"+q.Encode(), nil)
		if err == nil {
			var part map[string]float64
			part, err = parseEPSS(body)
			for id, s := range part {
				scores[id] = s
			}
		}
		if err != nil {
			log.Printf("[WARN] FetchEPSS: chunk of %d IDs failed: %v", len(chunk), err)
			if firstErr == nil {
				firstErr = fmt.Errorf("sentinel: fetch epss: %w", err)
			}
		}
	}
	return scores, firstErr
}
