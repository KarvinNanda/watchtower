package alertmsg

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var update = flag.Bool("update", false, "rewrite testdata/*.golden from the current output")

// goldenPartSeparator separates message parts inside one golden file. The
// telegram_e2e test splits on it to send each part.
const goldenPartSeparator = "\n\n=== next message ===\n\n"

// assertGolden checks every part with checkMarkdownV2 and the size limit,
// then compares the parts with testdata/<name>.golden. With -update it
// rewrites the file instead; read the diff before committing it.
func assertGolden(t *testing.T, name string, msgs []string) {
	t.Helper()
	require.NotEmpty(t, msgs)
	for i, m := range msgs {
		require.NoError(t, checkMarkdownV2(m), "%s part %d", name, i+1)
		require.LessOrEqual(t, len(m), telegramMaxMessageLen, "%s part %d", name, i+1)
	}
	got := strings.Join(msgs, goldenPartSeparator) + "\n"
	path := filepath.Join("testdata", name+".golden")
	if *update {
		require.NoError(t, os.MkdirAll("testdata", 0o755))
		require.NoError(t, os.WriteFile(path, []byte(got), 0o644))
		return
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err, "missing %s: run go test ./internal/alertmsg/ -update", path)
	assert.Equal(t, string(want), got)
}
