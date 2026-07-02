package sentinel

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
)

// sorted returns a copy of ss sorted ascending, so slice-equality
// assertions below don't depend on sentinelKeywordList's internal order.
func sorted(ss []string) []string {
	out := append([]string(nil), ss...)
	sort.Strings(out)
	return out
}

func TestExtractKeywords_Match(t *testing.T) {
	t.Parallel()
	got := extractKeywords("Apache RCE vulnerability in Linux kernel")

	// "rce" is itself one of sentinelKeywordList's security-category
	// entries, and the input text contains it as a standalone word, so a
	// correct implementation matches it too alongside apache/linux.
	assert.Equal(t, []string{"apache", "linux", "rce"}, sorted(got))
}

func TestExtractKeywords_CaseInsensitive(t *testing.T) {
	t.Parallel()
	got := extractKeywords("ANDROID Privilege Escalation")
	assert.Equal(t, []string{"android", "escalation", "privilege"}, sorted(got))
}

func TestExtractKeywords_NoMatch(t *testing.T) {
	t.Parallel()
	got := extractKeywords("Random text with no security keywords")
	assert.NotNil(t, got, "extractKeywords must return an empty slice, never nil")
	assert.Empty(t, got)
}

func TestExtractKeywords_NoDuplicates(t *testing.T) {
	t.Parallel()
	got := extractKeywords("apache apache apache nginx nginx")
	assert.Equal(t, []string{"apache", "nginx"}, sorted(got))
}
