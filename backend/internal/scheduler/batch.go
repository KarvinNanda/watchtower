package scheduler

import (
	"fmt"
	"strings"

	"github.com/karvin-nanda/watchtower/internal/notifier"
)

// telegramMaxMessageLen is Telegram's hard limit on a single sendMessage
// text payload. Shared by both AssetWorker and SentinelWorker, since both
// now send one combined message per user per run instead of one message
// per triggered symbol/item.
const telegramMaxMessageLen = 4096

// itemSeparator visually divides consecutive alert/item blocks within a
// batched message.
const itemSeparator = "\n\n━━━━━━━━━━━━━━━\n\n"

// buildBatchedMessages assembles pre-escaped item blocks into as few
// Telegram messages as possible, each prefixed with a "{title}\n⏰
// {timestamp}\n━━━━━━━━━━━━━━━" header. If everything fits in one message
// (the common case), a single-element slice is returned with no "part"
// marker. Otherwise blocks are greedily packed across multiple messages,
// each carrying its own header plus a "(Part X/Y)" suffix on the title, so
// no individual message ever exceeds telegramMaxMessageLen.
func buildBatchedMessages(title, timestampEscaped string, blocks []string) []string {
	if len(blocks) == 0 {
		return nil
	}

	buildHeader := func(partSuffix string) string {
		t := title
		if partSuffix != "" {
			t += " " + partSuffix
		}
		return fmt.Sprintf("%s\n⏰ %s\n━━━━━━━━━━━━━━━", t, timestampEscaped)
	}

	header := buildHeader("")
	full := header + "\n\n" + strings.Join(blocks, itemSeparator)
	if len(full) <= telegramMaxMessageLen || len(blocks) <= 1 {
		// A lone block that's still too long can't be shrunk by "splitting"
		// a batch of one — send it as-is rather than looping pointlessly.
		return []string{full}
	}

	// Reserve enough budget in each group for the header plus a generous
	// allowance for the "(Part N/N)" suffix added once the final group
	// count is known below.
	headerBudget := len(header) + 40

	var groups [][]string
	var current []string
	currentLen := headerBudget

	for _, block := range blocks {
		addLen := len(block)
		if len(current) > 0 {
			addLen += len(itemSeparator)
		}
		if len(current) > 0 && currentLen+addLen > telegramMaxMessageLen {
			groups = append(groups, current)
			current = nil
			currentLen = headerBudget
			addLen = len(block)
		}
		current = append(current, block)
		currentLen += addLen
	}
	if len(current) > 0 {
		groups = append(groups, current)
	}

	total := len(groups)
	messages := make([]string, 0, total)
	for i, group := range groups {
		partSuffix := notifier.EscapeTelegramMarkdown(fmt.Sprintf("(Part %d/%d)", i+1, total))
		messages = append(messages, buildHeader(partSuffix)+"\n\n"+strings.Join(group, itemSeparator))
	}
	return messages
}
