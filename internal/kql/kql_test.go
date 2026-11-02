package kql

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestTheReportIsRead(t *testing.T) {
	if Version() == "" || len(Cases) < 200 {
		t.Fatalf("version %q, %d cases", Version(), len(Cases))
	}

	seen := map[string]bool{}

	for _, c := range Cases {
		if seen[c.ID] || c.Query == "" || c.Expected == "" || c.Section == "" {
			t.Errorf("case %+v", c)
		}

		seen[c.ID] = true
	}

	if c := byID(t, "TAGS-02"); c.Query != `tag:("foo-bar")` || c.Expected != "invoice.txt" || c.Section != "tags" {
		t.Errorf("TAGS-02 %+v", c)
	}

	// a case that leans on the suite's own ids is not one a user can repeat
	if seen["SCOPE-02"] {
		t.Error("SCOPE-02 was taken")
	}
}

// shape is a query with its values blanked and its field names lowercased, so two queries of the
// same form compare equal.
func shape(q string) string {
	q = regexp.MustCompile(`"[^"]*"`).ReplaceAllString(q, `"_"`)
	q = regexp.MustCompile(`[0-9]+`).ReplaceAllString(q, "0")

	return regexp.MustCompile(`(?i)\b[a-z][a-z.]*\s*:`).ReplaceAllStringFunc(q, strings.ToLower)
}

// Every term the filter builds must be a form the parity report proves on both engines, so the
// search never sends a query only one of them answers right.
func TestEveryTermIsAProvenForm(t *testing.T) {
	day := time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)

	for _, c := range []struct {
		filter Filter
		term   string
		proof  string
	}{
		{Filter{Words: "restore"}, `name:"*restore*"`, "NAME-04"},
		{Filter{Words: "restore"}, `content:"restore"`, "CONTENT-03"},
		{Filter{Folder: "guides/2026"}, `path:"./guides/2026"`, "PATH-01"},
		{Filter{Tags: []string{"invoice"}}, `tag:("invoice")`, "TAGS-02"},
		{Filter{Type: "image"}, `mediatype:image`, "MEDIATYPE-05"},
		{Filter{Type: "Text/Markdown"}, `mediatype:text/markdown`, "MEDIATYPE-01"},
		{Filter{After: day}, `mtime>"2026-01-31T00:00:00Z"`, "RANGE-03"},
		{Filter{Before: day}, `mtime<"2026-01-31T00:00:00Z"`, "RANGE-04"},
		{Filter{Larger: 100}, `size>100`, "RANGE-01"},
		{Filter{Smaller: 100}, `size<100`, "RANGE-02"},
	} {
		q, err := c.filter.KQL()
		if err != nil || !strings.Contains(q, c.term) {
			t.Errorf("%+v: %q %v, want the term %s", c.filter, q, err, c.term)

			continue
		}

		if proof := byID(t, c.proof); shape(c.term) != shape(proof.Query) {
			t.Errorf("%s is not of the form of %s, %s", c.term, c.proof, proof.Query)
		}
	}

	// OpenCloud's search service turns is:favorite into the user's favorites field before the query
	// reaches an engine (services/search/pkg/search/service.go)
	if q, _ := (Filter{Favorites: true}).KQL(); q != "is:favorite" || shape(`Favorites:"0"`) != shape(byID(t, "FAVORITES-01").Query) {
		t.Errorf("favorites %q", q)
	}

	// terms join with AND, as BOOLEAN-01 and BOOLEAN-04 prove
	byID(t, "BOOLEAN-04")

	q, err := Filter{Words: "report", Tags: []string{"final", "q4"}, Type: "pdf", Query: "NOT hidden:true"}.KQL()
	if err != nil || q != `(name:"*report*" OR content:"report") AND (NOT hidden:true) AND tag:("final") AND tag:("q4") AND mediatype:pdf` {
		t.Errorf("all together %q %v", q, err)
	}
}

func TestAFilterOfNothingOrAnOddTypeIsRefused(t *testing.T) {
	for _, f := range []Filter{{}, {Words: `""`}, {Type: "pictures of cats"}, {Type: `image" OR name:"x`}} {
		if q, err := f.KQL(); err == nil {
			t.Errorf("%+v built %q", f, q)
		}
	}

	if q, _ := (Filter{Words: `say "hi"`, Tags: []string{`a"b`}}).KQL(); q != `(name:"*say  hi*" OR content:"say  hi") AND tag:("a b")` {
		t.Errorf("quotes %q", q)
	}
}

func TestTheGuideAndTheExamplesComeFromTheReport(t *testing.T) {
	g := Guide()
	for _, want := range []string{"OpenCloud " + Version(), "## tags", "- `invoice.txt`, Tags = foo-bar", "- `tag:(\"foo-bar\")` finds invoice.txt", "is refused"} {
		if !strings.Contains(g, want) {
			t.Errorf("the guide lacks %q", want)
		}
	}

	for _, c := range Examples(`tag:("x") AND mtime>"2026"`, 50) {
		if !strings.Contains(strings.ToLower(c.Query), "tag") && !strings.Contains(strings.ToLower(c.Query), "mtime") {
			t.Errorf("an example of neither field: %s", c.Query)
		}
	}

	if ex := Examples("is:favorite", 5); len(ex) == 0 || ex[0].Section != "favorites" {
		t.Errorf("favorites %+v", ex)
	}

	if ex := Examples("restore", 3); len(ex) != 3 || ex[0].Section != "boolean" {
		t.Errorf("no field %+v", ex)
	}
}

func byID(t *testing.T, id string) Case {
	t.Helper()

	for _, c := range Cases {
		if c.ID == id {
			return c
		}
	}

	t.Fatalf("the report has no case %s", id)

	return Case{}
}

func TestOpenCloudsParserReadsWhatTheFilterBuilds(t *testing.T) {
	q, err := Filter{
		Words: "report", Query: "NOT hidden:true", Folder: "guides", Tags: []string{"final"}, Type: "image/*",
		After: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Larger: 1, Smaller: 9, Favorites: true,
	}.KQL()
	if err != nil {
		t.Fatal(err)
	}

	if err := Check(q); err != nil {
		t.Errorf("%s: %v", q, err)
	}

	if err := Check(`tag:("draft") AND (mediatype:pdf`); err == nil || !strings.Contains(err.Error(), "invalid query syntax at 1:33") {
		t.Errorf("an open parenthesis: %v", err)
	}
}

func TestAQueryThatFindsNothingSaysWhatItMayHaveMissed(t *testing.T) {
	for q, want := range map[string]string{
		`size>abc`:          "OpenCloud reads `size>abc` as words, not as a range",
		`mtime>"soon"`:      "OpenCloud reads `mtime>\"soon\"` as words",
		`colour:red`:        "proves no field colour",
		`name:x AND tags:y`: "",
		`report`:            "",
	} {
		got := strings.Join(Doubts(q), "; ")
		if want == "" && got != "" || !strings.Contains(got, want) {
			t.Errorf("%s: %q, want %q", q, got, want)
		}
	}
}
