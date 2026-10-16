package alertmsg

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testHeader() header {
	return header{first: "🔔 *Alert aset* · BTC \\(1 trigger\\)", lines: []string{"⏰ 15 Okt 2026 09:30 WIB"}}
}

func TestHeaderRender(t *testing.T) {
	h := testHeader()
	assert.Equal(t, "🔔 *Alert aset* · BTC \\(1 trigger\\)\n⏰ 15 Okt 2026 09:30 WIB\n━━━━━━━━━━━━━━━", h.render(""))
	assert.Equal(t, "🔔 *Alert aset* · BTC \\(1 trigger\\) \\(Bagian 1/2\\)\n⏰ 15 Okt 2026 09:30 WIB\n━━━━━━━━━━━━━━━",
		h.render(partLabel("id", 1, 2)))
}

func TestPartLabel(t *testing.T) {
	assert.Equal(t, "\\(Bagian 2/3\\)", partLabel("id", 2, 3))
	assert.Equal(t, "\\(Part 2/3\\)", partLabel("en", 2, 3))
}

func TestSentAtLine(t *testing.T) {
	jkt, err := time.LoadLocation("Asia/Jakarta")
	require.NoError(t, err)
	assert.Equal(t, "⏰ 15 Okt 2026 09:30 WIB", sentAtLine(time.Date(2026, 10, 15, 9, 30, 0, 0, jkt), "id"))
}

func TestPack_SingleMessageHasNoPartLabel(t *testing.T) {
	msgs := pack("id", testHeader(), []string{"a", "b"})
	require.Len(t, msgs, 1)
	assert.Equal(t, testHeader().render("")+"\n\na\n\nb", msgs[0])
}

func TestPack_EmptyIsNil(t *testing.T) {
	assert.Nil(t, pack("id", testHeader(), nil))
}

func TestPack_SplitsWithLocalizedLabels(t *testing.T) {
	block := strings.Repeat("x", 1000)
	blocks := []string{block, block, block, block, block, block}
	for _, lang := range []string{"id", "en"} {
		msgs := pack(lang, testHeader(), blocks)
		require.Len(t, msgs, 2, lang)
		for i, m := range msgs {
			assert.LessOrEqual(t, len(m), telegramMaxMessageLen, "%s part %d", lang, i+1)
			assert.Contains(t, m, partLabel(lang, i+1, 2))
		}
		assert.Equal(t, 6, strings.Count(strings.Join(msgs, ""), block), "no block lost or duplicated")
	}
}

func TestPack_OversizedSingleBlockIsSentAsIs(t *testing.T) {
	huge := strings.Repeat("x", 5000)
	msgs := pack("en", testHeader(), []string{huge})
	require.Len(t, msgs, 1)
	assert.Contains(t, msgs[0], huge)
}

// #63: a block over the limit is split by line; each piece repeats the
// block's first line so it reads on its own, and no line is lost or doubled.
func TestPack_OversizedBlockIsSplitByLine(t *testing.T) {
	first := "📊 *BTC* · $1"
	lines := []string{first}
	for i := 0; i < 60; i++ {
		lines = append(lines, fmt.Sprintf("📉 rule %02d %s", i, strings.Repeat("y", 80)))
	}
	lines = append(lines, "24h 1\\.00%")
	block := strings.Join(lines, "\n")
	require.Greater(t, len(block), telegramMaxMessageLen)

	msgs := pack("id", testHeader(), []string{"📊 *AAA* · $2", block})

	require.Greater(t, len(msgs), 1)
	seen := map[string]int{}
	for i, m := range msgs {
		assert.LessOrEqual(t, len(m), telegramMaxMessageLen, "part %d", i+1)
		assert.Contains(t, m, partLabel("id", i+1, len(msgs)))
		for _, l := range strings.Split(m, "\n") {
			if strings.HasPrefix(l, "📉 rule ") {
				seen[l]++
			}
		}
		if strings.Contains(m, "📉 rule ") {
			assert.Contains(t, m, first+"\n📉 rule ", "part %d: a piece starts with the symbol line", i+1)
		}
	}
	assert.Len(t, seen, 60)
	for l, n := range seen {
		assert.Equal(t, 1, n, l)
	}
	assert.True(t, strings.HasSuffix(msgs[len(msgs)-1], "24h 1\\.00%"), "the 24h line stays at the end")
}
