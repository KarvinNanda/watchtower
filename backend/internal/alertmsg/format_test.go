package alertmsg

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestFormatUSD(t *testing.T) {
	assert.Equal(t, "$61,633.00", formatUSD(61633))
	assert.Equal(t, "$0.5", formatUSD(0.5))
	assert.Equal(t, "$0.00001234", formatUSD(0.00001234))
	assert.Equal(t, "$0.12345679", formatUSD(0.123456789), "at most 8 decimals, like the UI")
}

func TestFormatIDR_UsesIndonesianSeparators(t *testing.T) {
	assert.Equal(t, "Rp 1.463.319.195", formatIDR(1463319194.91))
	assert.Equal(t, "Rp 17.908", formatIDR(17908))
	assert.Equal(t, "Rp 0,1966", formatIDR(0.19663), "SHIB in IDR must not read as Rp 0")
	assert.Equal(t, "Rp 0", formatIDR(0))
}

func TestFormatPct(t *testing.T) {
	assert.Equal(t, "5%", formatPct(5))
	assert.Equal(t, "2.5%", formatPct(2.5))
}

func TestTruncateRunes(t *testing.T) {
	assert.Equal(t, "abc", TruncateRunes("abc", 3))
	assert.Equal(t, "ab…", TruncateRunes("abc", 2))
	assert.Equal(t, "éé…", TruncateRunes("ééé", 2), "counts runes, not bytes")
}

func TestFormatTime_LocalizesMonth(t *testing.T) {
	jkt, err := time.LoadLocation("Asia/Jakarta")
	assert.NoError(t, err)
	at := time.Date(2026, 10, 15, 9, 30, 0, 0, jkt)
	assert.Equal(t, "15 Okt 2026 09:30 WIB", formatTime(at, sentAtLayout, "id"))
	assert.Equal(t, "15 Oct 2026 09:30 WIB", formatTime(at, sentAtLayout, "en"))
	may := time.Date(2026, 5, 1, 0, 0, 0, 0, jkt)
	assert.Equal(t, "1 Mei 2026", formatTime(may, dueDateLayout, "id"))
	aug := time.Date(2026, 8, 1, 0, 0, 0, 0, jkt)
	assert.Equal(t, "1 Agu 2026", formatTime(aug, dueDateLayout, "id"))
	dec := time.Date(2026, 12, 1, 0, 0, 0, 0, jkt)
	assert.Equal(t, "1 Des 2026", formatTime(dec, dueDateLayout, "id"))
}

func TestFormatDueDate(t *testing.T) {
	assert.Equal(t, "5 Nov 2026", formatDueDate("2026-11-05", "en"))
	assert.Equal(t, "5 Nov 2026", formatDueDate("2026-11-05", "id"))
	assert.Equal(t, "next week", formatDueDate("next week", "en"), "unparsable dates are shown as published")
}

func TestEscapeLinkURL(t *testing.T) {
	assert.Equal(t, `https://x.org/a_(b\)\\c`, escapeLinkURL(`https://x.org/a_(b)\c`))
}
