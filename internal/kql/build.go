package kql

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Filter is what a search looks for. Each part becomes a term of a form the parity report holds,
// the terms joined by AND.
type Filter struct {
	// Words are looked for in names and, where the instance extracts it, in the text.
	Words string
	// Query is a query of its own, taken as it is.
	Query string
	// Folder is a folder's path in the space, without a leading slash; it and all below it match.
	Folder string
	Tags   []string
	// Type is a kind (file, folder, document, spreadsheet, presentation, pdf, image, video, audio,
	// archive) or a MIME type like text/markdown or image/*.
	Type            string
	After, Before   time.Time
	Larger, Smaller int64
	Favorites       bool
}

var mediaType = regexp.MustCompile(`^[a-z0-9][a-z0-9.+-]*(/[a-z0-9.+*-]+)?$`)

// KQL is the query of the filter.
func (f Filter) KQL() (string, error) {
	var terms []string

	if w := unquote(f.Words); w != "" {
		terms = append(terms, fmt.Sprintf(`(name:"*%s*" OR content:"%s")`, w, w))
	}

	if q := strings.TrimSpace(f.Query); q != "" {
		terms = append(terms, "("+q+")")
	}

	if p := unquote(strings.Trim(f.Folder, "/")); p != "" {
		terms = append(terms, fmt.Sprintf(`path:"./%s"`, p))
	}

	for _, t := range f.Tags {
		if t = unquote(t); t != "" {
			terms = append(terms, fmt.Sprintf(`tag:("%s")`, t))
		}
	}

	if t := strings.ToLower(strings.TrimSpace(f.Type)); t != "" {
		if !mediaType.MatchString(t) {
			return "", fmt.Errorf("%q is no kind of file and no MIME type", f.Type)
		}

		terms = append(terms, "mediatype:"+t)
	}

	if !f.After.IsZero() {
		terms = append(terms, fmt.Sprintf(`mtime>"%s"`, f.After.UTC().Format(time.RFC3339)))
	}

	if !f.Before.IsZero() {
		terms = append(terms, fmt.Sprintf(`mtime<"%s"`, f.Before.UTC().Format(time.RFC3339)))
	}

	if f.Larger > 0 {
		terms = append(terms, fmt.Sprintf("size>%d", f.Larger))
	}

	if f.Smaller > 0 {
		terms = append(terms, fmt.Sprintf("size<%d", f.Smaller))
	}

	if f.Favorites {
		terms = append(terms, "is:favorite")
	}

	if len(terms) == 0 {
		return "", errors.New("the search names nothing to look for")
	}

	return strings.Join(terms, " AND "), nil
}

// unquote is s without the double quotes that would end a quoted value early.
func unquote(s string) string {
	return strings.TrimSpace(strings.ReplaceAll(s, `"`, " "))
}
