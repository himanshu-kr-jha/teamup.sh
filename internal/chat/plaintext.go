package chat

import (
	"regexp"
	"strings"
)

// Messages are written with the marks chat apps use: *bold*, _italic_,
// ~struck~, `code`, ```code blocks``` and [named links](https://…). The web
// app shows them as formatting (web/src/shared/richText/format.ts); a push
// notification can only show text, so there they are taken off.

var (
	namedLink = regexp.MustCompile(`\[([^\]\n]+)\]\((https?://[^\s)]+)\)`)
	fenceLine = regexp.MustCompile("(?m)^[ \t]*```[A-Za-z0-9+#-]*[ \t]*$\n?")
	codeSpan  = regexp.MustCompile("```([^`\n]+)```|`([^`\n]+)`")
	emphasis  []*regexp.Regexp
)

func init() {
	for _, mark := range []string{"**", "__", "~~", "*", "_", "~"} {
		m := regexp.QuoteMeta(mark)
		// Only around words, and not inside one, so snake_case and 2*3*4 stay.
		emphasis = append(emphasis, regexp.MustCompile(
			`(^|[^\p{L}\p{N}])`+m+`([^\s`+m+`](?:[^\n]*?[^\s`+m+`])?)`+m+`($|[^\p{L}\p{N}])`))
	}
}

// plainText is a message's words without its formatting marks.
func plainText(content string) string {
	s := fenceLine.ReplaceAllString(content, "")
	s = namedLink.ReplaceAllString(s, "$1")
	s = codeSpan.ReplaceAllString(s, "$1$2")
	for _, re := range emphasis {
		// Neighbouring runs share the character between them, so once more
		// until nothing changes.
		for {
			next := re.ReplaceAllString(s, "$1$2$3")
			if next == s {
				break
			}
			s = next
		}
	}
	return strings.TrimSpace(s)
}
