package scheduler

import (
	"log"

	"github.com/karvin-nanda/watchtower/internal/sentinel"
)

// maxNVDLookupsPerRun bounds the NVD detail lookups for unmerged CISA KEV
// items in one run. Each lookup sleeps the NVD rate-limit delay.
const maxNVDLookupsPerRun = 20

// SentinelSource is what SentinelWorker needs from the fetcher. It lets
// tests inject a fake without network access.
type SentinelSource interface {
	FetchAll() ([]sentinel.SentinelItem, error)
	FetchNVDByCVE(cveID string) (*sentinel.SentinelItem, error)
	FetchEPSS(cveIDs []string) (map[string]float64, error)
}

// enrichNewItems adds NVD details and EPSS scores to items in place. It runs
// on every new item before keyword matching, so the outside lookups never
// reveal which items match a user's keywords. Failures are logged and the
// item is kept as it is.
func (w *SentinelWorker) enrichNewItems(items []sentinel.SentinelItem) {
	// A panic here must not abort the run: items already enriched stay
	// enriched, and matching and dispatch still happen.
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[ERROR] enrichNewItems: recovered from panic: %v", r)
		}
	}()

	lookups, skipped := 0, 0
	for i := range items {
		if items[i].SourceType != "cisa_kev" || items[i].CVEID == "" {
			continue
		}
		if lookups >= maxNVDLookupsPerRun {
			skipped++
			continue
		}
		lookups++

		nvd, err := w.fetcher.FetchNVDByCVE(items[i].CVEID)
		if err != nil {
			log.Printf("[WARN] enrichNewItems: NVD lookup for %s failed: %v", items[i].CVEID, err)
			continue
		}
		if nvd != nil {
			applyNVD(&items[i], *nvd)
		}
	}
	if skipped > 0 {
		log.Printf("[WARN] enrichNewItems: NVD lookup cap of %d reached, skipped %d KEV items", maxNVDLookupsPerRun, skipped)
	}

	var ids []string
	for _, it := range items {
		if it.CVEID != "" {
			ids = append(ids, it.CVEID)
		}
	}
	if len(ids) == 0 {
		return
	}

	// FetchEPSS may return a partial map together with an error: use both.
	scores, err := w.fetcher.FetchEPSS(ids)
	if err != nil {
		log.Printf("[WARN] enrichNewItems: EPSS lookup incomplete: %v", err)
	}
	for i := range items {
		if score, ok := scores[items[i].CVEID]; ok && items[i].CVEID != "" {
			s := score
			items[i].EPSS = &s
		}
	}
}

// applyNVD copies the NVD scoring and affected-version fields into dst and
// leaves the KEV fields (exploited flag, required action, due date,
// ransomware) untouched.
func applyNVD(dst *sentinel.SentinelItem, nvd sentinel.SentinelItem) {
	dst.CVSSScore = nvd.CVSSScore
	dst.Severity = nvd.Severity
	dst.CVSSVector = nvd.CVSSVector
	dst.Affected = nvd.Affected
	dst.SourceDate = nvd.SourceDate
}

// shouldSend reports whether a matched item is worth a message. Actively
// exploited items always are. LOW and NONE are not, and neither is an item
// with no severity label but a score under 4.0.
func shouldSend(item sentinel.SentinelItem) bool {
	if item.KnownExploited {
		return true
	}
	switch item.Severity {
	case "LOW", "NONE":
		return false
	case "":
		return !(item.CVSSScore > 0 && item.CVSSScore < 4.0)
	}
	return true
}
