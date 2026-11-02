package document

import (
	"regexp"
	"strings"
)

// maxOutline bounds how many headings an outline lists.
const maxOutline = 60

// heading is a markdown heading: its level, its words and where its line starts.
type heading struct {
	level int
	title string
	at    int
}

var (
	atx   = regexp.MustCompile(`^ {0,3}(#{1,6})[ \t]+(.*?)(?:[ \t]+#+)?[ \t]*$`)
	fence = regexp.MustCompile("^ {0,3}(```|~~~)")
)

// headingsOf finds the headings of a markdown text, never inside a fenced block.
func headingsOf(text string) []heading {
	var (
		out    []heading
		fenced string
		at     int
	)

	for line := range strings.SplitAfterSeq(text, "\n") {
		bare := strings.TrimRight(line, "\r\n")

		switch m := fence.FindStringSubmatch(bare); {
		case m != nil && fenced == "":
			fenced = m[1]
		case m != nil && m[1] == fenced:
			fenced = ""
		case fenced == "":
			if h := atx.FindStringSubmatch(bare); h != nil && strings.TrimSpace(h[2]) != "" {
				out = append(out, heading{level: len(h[1]), title: strings.TrimSpace(h[2]), at: at})
			}
		}

		at += len(line)
	}

	return out
}

// Outline is a text's headings down to the third level, each with its level's hashes, like
// "## Restore a single file".
func Outline(text string) []string {
	var out []string

	for _, h := range headingsOf(text) {
		if h.level <= 3 && len(out) < maxOutline {
			out = append(out, strings.Repeat("#", h.level)+" "+h.title)
		}
	}

	return out
}

// Section is the part of text under the heading that matches title, down to the next heading of
// its level or above; the exact heading first, else the first that contains the words.
func Section(text, title string) (string, bool) {
	hs := headingsOf(text)
	want := strings.ToLower(strings.Trim(strings.TrimSpace(title), "# "))

	if want == "" {
		return "", false
	}

	i := -1

	for j, h := range hs {
		if strings.ToLower(h.title) == want {
			i = j

			break
		}
	}

	for j, h := range hs {
		if i < 0 && strings.Contains(strings.ToLower(h.title), want) {
			i = j
		}
	}

	if i < 0 {
		return "", false
	}

	end := len(text)

	for _, h := range hs[i+1:] {
		if h.level <= hs[i].level {
			end = h.at

			break
		}
	}

	return strings.TrimSpace(text[hs[i].at:end]), true
}

// Title is the words of the first heading of a markdown text, or "" when it has none.
func Title(text string) string {
	if hs := headingsOf(text); len(hs) > 0 {
		return strings.TrimSpace(hs[0].title)
	}

	return ""
}
