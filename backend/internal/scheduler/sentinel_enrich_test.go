package scheduler

import (
	"errors"
	"fmt"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/karvin-nanda/watchtower/internal/sentinel"
)

type fakeSentinelSource struct {
	items   []sentinel.SentinelItem
	nvd     map[string]*sentinel.SentinelItem
	nvdErr  map[string]error
	epss    map[string]float64
	epssErr error

	nvdCalls  []string
	epssCalls [][]string
}

func (f *fakeSentinelSource) FetchAll() ([]sentinel.SentinelItem, error) { return f.items, nil }

func (f *fakeSentinelSource) FetchNVDByCVE(id string) (*sentinel.SentinelItem, error) {
	f.nvdCalls = append(f.nvdCalls, id)
	if err := f.nvdErr[id]; err != nil {
		return nil, err
	}
	return f.nvd[id], nil
}

func (f *fakeSentinelSource) FetchEPSS(ids []string) (map[string]float64, error) {
	f.epssCalls = append(f.epssCalls, append([]string(nil), ids...))
	return f.epss, f.epssErr
}

func TestEnrichNewItems_LooksUpUnmergedKEVOnly(t *testing.T) {
	pub := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	src := &fakeSentinelSource{nvd: map[string]*sentinel.SentinelItem{
		"CVE-2026-0001": {
			CVSSScore: 9.8, Severity: "CRITICAL", CVSSVector: "CVSS:3.1/AV:N",
			Affected:   []sentinel.AffectedVersion{{Product: "p", Range: "< 2", Fixed: "2"}},
			SourceDate: pub,
		},
	}}
	w := &SentinelWorker{fetcher: src}
	items := []sentinel.SentinelItem{
		{SourceType: "cisa_kev", CVEID: "CVE-2026-0001", KnownExploited: true, RequiredAction: "Patch."},
		{SourceType: "cve", CVEID: "CVE-2026-0002"},
		{SourceType: "cve", CVEID: "CVE-2026-0003", KnownExploited: true},
	}

	w.enrichNewItems(items)

	assert.Equal(t, []string{"CVE-2026-0001"}, src.nvdCalls)
	assert.Equal(t, 9.8, items[0].CVSSScore)
	assert.Equal(t, "CRITICAL", items[0].Severity)
	assert.Equal(t, "CVSS:3.1/AV:N", items[0].CVSSVector)
	assert.Len(t, items[0].Affected, 1)
	assert.Equal(t, pub, items[0].SourceDate)
	assert.True(t, items[0].KnownExploited)
	assert.Equal(t, "Patch.", items[0].RequiredAction)
}

func TestEnrichNewItems_EPSSForAllNewItems(t *testing.T) {
	src := &fakeSentinelSource{epss: map[string]float64{"CVE-2026-0001": 0.5, "CVE-2026-0002": 0.01}}
	w := &SentinelWorker{fetcher: src}
	items := []sentinel.SentinelItem{
		{SourceType: "cve", CVEID: "CVE-2026-0001"},
		{SourceType: "cve", CVEID: "CVE-2026-0002"},
		{SourceType: "cve", CVEID: "CVE-2026-0003"},
		{SourceType: "rss", Title: "no cve"},
	}

	w.enrichNewItems(items)

	require.Equal(t, [][]string{{"CVE-2026-0001", "CVE-2026-0002", "CVE-2026-0003"}}, src.epssCalls)
	require.NotNil(t, items[0].EPSS)
	assert.Equal(t, 0.5, *items[0].EPSS)
	require.NotNil(t, items[1].EPSS)
	assert.Equal(t, 0.01, *items[1].EPSS)
	assert.Nil(t, items[2].EPSS)
	assert.Nil(t, items[3].EPSS)
}

func TestEnrichNewItems_CapsNVDLookups(t *testing.T) {
	src := &fakeSentinelSource{}
	w := &SentinelWorker{fetcher: src}
	var items []sentinel.SentinelItem
	for i := 0; i < 25; i++ {
		items = append(items, sentinel.SentinelItem{SourceType: "cisa_kev", CVEID: fmt.Sprintf("CVE-2026-%04d", i)})
	}

	w.enrichNewItems(items)

	assert.Len(t, src.nvdCalls, maxNVDLookupsPerRun)
	assert.Equal(t, 20, maxNVDLookupsPerRun)
}

func TestEnrichNewItems_FailuresKeepItems(t *testing.T) {
	src := &fakeSentinelSource{
		nvdErr:  map[string]error{"CVE-2026-0001": errors.New("nvd down")},
		epss:    map[string]float64{"CVE-2026-0003": 0.2},
		epssErr: errors.New("partial"),
	}
	w := &SentinelWorker{fetcher: src}
	items := []sentinel.SentinelItem{
		{SourceType: "cisa_kev", CVEID: "CVE-2026-0001"},
		{SourceType: "cisa_kev", CVEID: "CVE-2026-0002"},
		{SourceType: "cve", CVEID: "CVE-2026-0003"},
	}

	require.NotPanics(t, func() { w.enrichNewItems(items) })

	assert.Equal(t, []string{"CVE-2026-0001", "CVE-2026-0002"}, src.nvdCalls)
	assert.Zero(t, items[0].CVSSScore)
	assert.Nil(t, items[0].EPSS)
	assert.Nil(t, items[1].EPSS)
	require.NotNil(t, items[2].EPSS)
	assert.Equal(t, 0.2, *items[2].EPSS)
}

func TestShouldSend(t *testing.T) {
	cases := []struct {
		name string
		item sentinel.SentinelItem
		want bool
	}{
		{"LOW", sentinel.SentinelItem{Severity: "LOW"}, false},
		{"NONE", sentinel.SentinelItem{Severity: "NONE"}, false},
		{"empty 3.9", sentinel.SentinelItem{CVSSScore: 3.9}, false},
		{"empty 0", sentinel.SentinelItem{}, true},
		{"LOW exploited", sentinel.SentinelItem{Severity: "LOW", KnownExploited: true}, true},
		{"MODERATE", sentinel.SentinelItem{Severity: "MODERATE"}, true},
		{"MEDIUM", sentinel.SentinelItem{Severity: "MEDIUM"}, true},
		{"HIGH", sentinel.SentinelItem{Severity: "HIGH"}, true},
		{"empty 4.0", sentinel.SentinelItem{CVSSScore: 4.0}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, shouldSend(c.item))
		})
	}
}

func TestFetchAndProcess_EnrichesBeforeMatching(t *testing.T) {
	w, mock, _ := newMockSentinelWorker(t)
	src := &fakeSentinelSource{
		items: []sentinel.SentinelItem{{SourceType: "cve", Identifier: "CVE-2026-0009", CVEID: "CVE-2026-0009", Title: "t"}},
		epss:  map[string]float64{},
	}
	w.fetcher = src

	mock.ExpectQuery(`SELECT id FROM sentinel_seen_items`).
		WithArgs("cve", "CVE-2026-0009").
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(`FROM keyword_subscriptions ks`).
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "keyword", "telegram_sentinel_chat_id", "preferred_language"}))
	mock.ExpectExec(`INSERT INTO sentinel_seen_items`).
		WithArgs("cve", "CVE-2026-0009").
		WillReturnResult(sqlmock.NewResult(1, 1))

	counters := &ProcessCounters{}
	require.NoError(t, w.fetchAndProcess(counters, map[uint64]*SentinelBatch{}))

	assert.Equal(t, [][]string{{"CVE-2026-0009"}}, src.epssCalls, "lookup covers unmatched items too")
	assert.Equal(t, 1, counters.Fetched)
	assert.Equal(t, 1, counters.New)
	assert.Equal(t, 0, counters.Matched)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func expectOneMatch(mock sqlmock.Sqlmock, id string) {
	mock.ExpectQuery(`FROM keyword_subscriptions ks`).
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "keyword", "telegram_sentinel_chat_id", "preferred_language"}).
			AddRow(1, "x", 100, "en"))
	mock.ExpectExec(`INSERT INTO sentinel_seen_items`).
		WithArgs("cve", id).
		WillReturnResult(sqlmock.NewResult(1, 1))
}

func TestProcessItem_LowItemIsSeenButNotBatched(t *testing.T) {
	w, mock, _ := newMockSentinelWorker(t)
	expectOneMatch(mock, "CVE-2026-0010")
	item := sentinel.SentinelItem{SourceType: "cve", Identifier: "CVE-2026-0010", CVEID: "CVE-2026-0010",
		Title: "x", Severity: "LOW", CVSSScore: 2.0}

	counters := &ProcessCounters{}
	batches := map[uint64]*SentinelBatch{}
	require.NoError(t, w.processItem(item, counters, batches))

	assert.Empty(t, batches)
	assert.Equal(t, 0, counters.Matched)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestProcessItem_CarriesEnrichmentIntoBatch(t *testing.T) {
	w, mock, _ := newMockSentinelWorker(t)
	expectOneMatch(mock, "CVE-2026-0011")
	epss := 0.42
	pub := time.Date(2026, 3, 4, 0, 0, 0, 0, time.UTC)
	item := sentinel.SentinelItem{SourceType: "cve", Identifier: "CVE-2026-0011", CVEID: "CVE-2026-0011",
		Title: "x", Severity: "HIGH", CVSSScore: 8.1, CVSSVector: "CVSS:3.1/AV:N",
		Affected:   []sentinel.AffectedVersion{{Product: "p", Range: "< 2", Fixed: "2", NoFix: true}},
		EPSS:       &epss,
		SourceDate: pub,
	}

	batches := map[uint64]*SentinelBatch{}
	require.NoError(t, w.processItem(item, &ProcessCounters{}, batches))

	require.Contains(t, batches, uint64(1))
	got := batches[1].Items[0]
	assert.Equal(t, "CVSS:3.1/AV:N", got.CVSSVector)
	require.Len(t, got.Affected, 1)
	assert.Equal(t, "p", got.Affected[0].Product)
	assert.Equal(t, "< 2", got.Affected[0].Range)
	assert.Equal(t, "2", got.Affected[0].Fixed)
	assert.True(t, got.Affected[0].NoFix)
	require.NotNil(t, got.EPSS)
	assert.Equal(t, 0.42, *got.EPSS)
	assert.Equal(t, pub, got.PublishedAt)
	assert.NoError(t, mock.ExpectationsWereMet())
}
