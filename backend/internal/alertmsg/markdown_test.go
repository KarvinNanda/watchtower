package alertmsg

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// checkMarkdownV2 reports the first MarkdownV2 problem in msg: an
// unescaped special character outside the entities this package emits on
// purpose (bold "*", links "[text](url)"), a dangling backslash, an
// unbalanced "*", or a malformed link. It is stricter than Telegram on
// purpose: anything it accepts, Telegram accepts.
func checkMarkdownV2(msg string) error {
	const special = "_*[]()~`>#+-=|{}.!"
	rs := []rune(msg)
	bold, inLinkText := false, false
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case r == '\\':
			if i+1 >= len(rs) {
				return fmt.Errorf("dangling backslash at rune %d", i)
			}
			i++
		case r == '*':
			bold = !bold
		case r == '[' && !inLinkText:
			inLinkText = true
		case r == ']' && inLinkText:
			inLinkText = false
			if i+1 >= len(rs) || rs[i+1] != '(' {
				return fmt.Errorf("link text without (url) at rune %d", i)
			}
			j := i + 2
			for ; j < len(rs) && rs[j] != ')'; j++ {
				if rs[j] == '\\' {
					j++ // inside the URL only ")" and "\" are escaped
				}
			}
			if j >= len(rs) {
				return fmt.Errorf("unterminated link url starting at rune %d", i+2)
			}
			i = j
		case strings.ContainsRune(special, r):
			return fmt.Errorf("unescaped %q at rune %d", r, i)
		}
	}
	if bold {
		return errors.New("unbalanced *")
	}
	if inLinkText {
		return errors.New("unterminated link text")
	}
	return nil
}

func TestCheckMarkdownV2(t *testing.T) {
	valid := []string{
		`🔔 *Alert aset* · BTC \(1 trigger\)`,
		`🔗 [nvd\.nist\.gov](https://nvd.nist.gov/a_(b\))`,
		`C:\\Windows\\ \.`,
	}
	for _, m := range valid {
		assert.NoError(t, checkMarkdownV2(m), m)
	}
	invalid := []string{
		`price 1.5`,     // bare "."
		`*bold`,         // unbalanced
		`trailing \`,    // dangling backslash
		`[text] no url`, // link without url
		`[a](https://x`, // unterminated url
		`a\\.`,          // escaped backslash then bare "."
	}
	for _, m := range invalid {
		assert.Error(t, checkMarkdownV2(m), m)
	}
}
