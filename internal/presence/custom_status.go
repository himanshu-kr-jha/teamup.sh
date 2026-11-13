package presence

import (
	"errors"
	"unicode/utf8"
)

// A custom status is shown beside a name everywhere that person appears, kept
// in Redis and sent to the whole organization on every change, so it is held
// to one line of text and one emoji. Without a limit one person could make
// every client in the organization download and render a megabyte each time
// they changed it.
const (
	maxCustomStatusRunes = 100
	// maxCustomEmojiBytes leaves room for the longest single emoji in common
	// use, such as a family with skin tones joined by zero-width joiners.
	maxCustomEmojiBytes = 32
)

// ErrCustomStatus is a custom status or emoji over the limits above.
var ErrCustomStatus = errors.New("keep the status to 100 characters and a single emoji")

func validCustomStatus(text, emoji string) error {
	if !utf8.ValidString(text) || utf8.RuneCountInString(text) > maxCustomStatusRunes {
		return ErrCustomStatus
	}
	if emoji == "" {
		return nil
	}
	if !utf8.ValidString(emoji) || len(emoji) > maxCustomEmojiBytes || !oneEmoji(emoji) {
		return ErrCustomStatus
	}
	return nil
}

// oneEmoji is a rough single-grapheme test, enough without a Unicode
// segmentation library: it counts the characters that start a new symbol,
// skipping those that only modify or join the one before (variation
// selectors, skin tones, tags, the keycap mark, and whatever follows a
// zero-width joiner). A flag is a pair of regional indicators, so two of
// those count as one.
func oneEmoji(s string) bool {
	bases, regional := 0, 0
	joined := false
	for _, r := range s {
		switch {
		case r == 0x200D:
			joined = true
			continue
		case r == 0xFE0E || r == 0xFE0F || r == 0x20E3 ||
			(r >= 0x1F3FB && r <= 0x1F3FF) || (r >= 0xE0020 && r <= 0xE007F):
			continue
		case joined:
			joined = false
			continue
		case r >= 0x1F1E6 && r <= 0x1F1FF:
			regional++
			if regional%2 == 1 {
				bases++
			}
			continue
		}
		bases++
	}
	return bases == 1
}
