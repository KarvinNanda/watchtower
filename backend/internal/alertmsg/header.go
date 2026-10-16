package alertmsg

import (
	"fmt"
	"strings"
	"time"
)

const (
	// telegramMaxMessageLen is Telegram's limit for one sendMessage text.
	// Telegram counts UTF-16 code units after entity parsing; the raw
	// UTF-8 byte length measured here is never smaller, so it is safe.
	telegramMaxMessageLen = 4096
	divider               = "━━━━━━━━━━━━━━━"
	blockSeparator        = "\n\n"
)

// header is the top of every message part. first is the summary line,
// lines go between it and the divider. Both are already escaped.
type header struct {
	first string
	lines []string
}

// render returns the header with label (already escaped, may be empty)
// appended to the first line.
func (h header) render(label string) string {
	first := h.first
	if label != "" {
		first += " " + label
	}
	parts := append([]string{first}, h.lines...)
	return strings.Join(append(parts, divider), "\n")
}

func partLabel(lang string, i, total int) string {
	return esc(fmt.Sprintf("(%s %d/%d)", pick(lang, "Part", "Bagian"), i, total))
}

func sentAtLine(at time.Time, lang string) string {
	return "⏰ " + esc(formatTime(at, sentAtLayout, lang))
}

// pack joins blocks under h. If everything fits in one message there is no
// part label. Otherwise blocks are packed greedily into parts, each with
// the same header plus "(Bagian X/Y)" / "(Part X/Y)". A block that does not
// fit in one part is split by line first (#63); a single line over the
// limit is sent as is.
func pack(lang string, h header, blocks []string) []string {
	if len(blocks) == 0 {
		return nil
	}
	full := h.render("") + blockSeparator + strings.Join(blocks, blockSeparator)
	if len(full) <= telegramMaxMessageLen {
		return []string{full}
	}

	// Reserve room for the longest label this message could need.
	headerLen := len(h.render(partLabel(lang, 999, 999))) + len(blockSeparator)
	blocks = splitOversized(blocks, telegramMaxMessageLen-headerLen)
	if len(blocks) == 1 {
		return []string{full}
	}
	var groups [][]string
	var current []string
	currentLen := headerLen
	for _, b := range blocks {
		add := len(b)
		if len(current) > 0 {
			add += len(blockSeparator)
		}
		if len(current) > 0 && currentLen+add > telegramMaxMessageLen {
			groups = append(groups, current)
			current, currentLen, add = nil, headerLen, len(b)
		}
		current = append(current, b)
		currentLen += add
	}
	groups = append(groups, current)

	msgs := make([]string, len(groups))
	for i, g := range groups {
		msgs[i] = h.render(partLabel(lang, i+1, len(groups))) + blockSeparator + strings.Join(g, blockSeparator)
	}
	return msgs
}

// splitOversized splits every block longer than max by line (#63). Each
// piece repeats the block's first line (the symbol and its price) so it
// reads on its own. A block whose lines cannot be split stays as is.
func splitOversized(blocks []string, max int) []string {
	out := make([]string, 0, len(blocks))
	for _, b := range blocks {
		if len(b) <= max {
			out = append(out, b)
			continue
		}
		lines := strings.Split(b, "\n")
		first := lines[0]
		piece := first
		for _, l := range lines[1:] {
			if piece != first && len(piece)+1+len(l) > max {
				out = append(out, piece)
				piece = first
			}
			piece += "\n" + l
		}
		out = append(out, piece)
	}
	return out
}
