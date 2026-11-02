// Package kql is the query language of OpenCloud's search as its parity report proves it: every
// query of the report ran against both of OpenCloud's search engines with the same answer.
// parity.md is that report as OpenCloud writes it, fetched by `mise run kql` for the OpenCloud
// version in the file version; borrowed with thanks from github.com/opencloud-eu/opencloud,
// services/search/pkg/parity, Apache-2.0 like this repository. Queries are read with OpenCloud's
// own parser, pkg/kql of the OpenCloud module.
package kql

import (
	_ "embed"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/opencloud-eu/opencloud/pkg/ast"
	ockql "github.com/opencloud-eu/opencloud/pkg/kql"
)

var (
	//go:embed parity.md
	parity string
	//go:embed version
	version string
)

// Version is the OpenCloud release whose parity report this is.
func Version() string { return strings.TrimSpace(version) }

// Case is one query of the report and what it finds among the fixtures of its section.
type Case struct {
	ID      string
	Section string
	Query   string
	// Expected names what the query finds, "no match", or "bad request" for a query OpenCloud refuses.
	Expected string
}

// a row of the report: | ID | `query`[ context] | expected | bleve | OpenSearch | same? |
var row = regexp.MustCompile("^\\| ([A-Z]+-[0-9]+) \\| `(.*)`(.*?) \\| (.+?) \\| .+ \\| .+ \\| (.+) \\|$")

// Cases are the queries of the report on which both engines agree with the expectation; a query
// that depends on the test's own ids or limits is left out.
var Cases = func() []Case {
	var (
		out     []Case
		section string
		queries bool
	)

	for line := range strings.Lines(parity) {
		line = strings.TrimRight(line, "\n")

		switch {
		case strings.HasPrefix(line, "## "):
			queries = line == "## Queries"
		case strings.HasPrefix(line, "### "):
			section = strings.TrimPrefix(line, "### ")
		}

		m := row.FindStringSubmatch(line)
		if !queries || m == nil || m[3] != "" || !strings.HasPrefix(m[5], "✅") {
			continue
		}

		out = append(out, Case{ID: m[1], Section: section, Query: m[2], Expected: m[4]})
	}

	return out
}()

// Guide is the language by its examples, a markdown document for a model to read.
func Guide() string {
	var b strings.Builder

	fmt.Fprintf(&b, "# OpenCloud's search query language\n\n"+
		"Every example below ran against both search engines of OpenCloud %s with the same answer, from OpenCloud's parity report. "+
		"Terms combine with AND, OR, NOT and parentheses; a bare word looks at the name. "+
		"Each section names the files its examples search.\n", Version())

	section := ""
	fixtures := sectionFixtures()

	for _, c := range Cases {
		if c.Section != section {
			section = c.Section
			fmt.Fprintf(&b, "\n## %s\n\nAmong these files:\n\n%s\nthe queries find:\n\n", section, fixtures[section])
		}

		fmt.Fprintf(&b, "- `%s` %s\n", c.Query, finds(c.Expected))
	}

	return b.String()
}

// Examples are cases that use the fields of the query, the limit shared among its fields; the
// boolean ones when it names none.
func Examples(query string, limit int) []Case {
	used := fields(query)
	if len(used) == 0 {
		return section("boolean", limit)
	}

	var out []Case

	share := max(1, limit/len(used))

	for _, f := range used {
		n := 0

		for _, c := range Cases {
			if n == share || len(out) == limit {
				break
			}

			if slices.Contains(fields(c.Query), f) && !slices.Contains(out, c) {
				out = append(out, c)
				n++
			}
		}
	}

	return out
}

func section(name string, limit int) []Case {
	var out []Case

	for _, c := range Cases {
		if c.Section == name && len(out) < limit {
			out = append(out, c)
		}
	}

	return out
}

// Line is a case as a guide line: the query and what it finds.
func (c Case) Line() string {
	return fmt.Sprintf("`%s` %s", c.Query, finds(c.Expected))
}

func finds(expected string) string {
	switch expected {
	case "no match":
		return "finds nothing"
	case "bad request":
		return "is refused"
	}

	return "finds " + expected
}

// Check is the refusal of OpenCloud's own query parser, nil for a query it reads; the search
// service parses with it before it asks an engine.
func Check(query string) error {
	if _, err := (ockql.Builder{}).Build(query); err != nil {
		return fmt.Errorf("OpenCloud's search refuses %s: %w", query, err)
	}

	return nil
}

// Doubts are why a query that found nothing may have missed: a range OpenCloud's parser read as
// words, or a field the report knows nothing of, which the search answers with nothing.
func Doubts(query string) []string {
	a, err := (ockql.Builder{}).Build(query)
	if err != nil {
		return nil
	}

	var out []string

	walk(a.Nodes, func(n ast.Node) {
		if s, ok := n.(*ast.StringNode); ok && s.Key == "" && misread.MatchString(s.Value) {
			out = append(out, fmt.Sprintf("OpenCloud reads `%s` as words, not as a range: a range takes a number or a date", s.Value))
		}
	})

	for _, f := range fields(query) {
		if !slices.Contains(known(), f) {
			out = append(out, fmt.Sprintf("OpenCloud's parity report proves no field %s, and the search finds nothing for a field it does not know", f))
		}
	}

	return out
}

// misread is a field and a comparison the parser could not make a range of
var misread = regexp.MustCompile(`^[A-Za-z][\w.]*\s*(<=|>=|<|>|=)`)

// known are the fields the report's queries use.
var known = sync.OnceValue(func() []string {
	var out []string

	for _, c := range Cases {
		for _, f := range fields(c.Query) {
			if !slices.Contains(out, f) {
				out = append(out, f)
			}
		}
	}

	return out
})

// guessed is a word before a field's colon or comparison, for a query the parser cannot read
var guessed = regexp.MustCompile(`(?i)\b([a-z][a-z.]*)\s*(?::|=|<|>)`)

// fields are the field names a query uses as OpenCloud's parser reads it, lowercased and in their
// singular spelling; for a query it cannot read, the words before a colon or comparison.
func fields(query string) []string {
	var keys []string

	if a, err := (ockql.Builder{}).Build(query); err == nil {
		walk(a.Nodes, func(n ast.Node) { keys = append(keys, key(n)) })
	} else {
		for _, m := range guessed.FindAllStringSubmatch(query, -1) {
			keys = append(keys, m[1])
		}
	}

	var out []string

	for _, f := range keys {
		f = strings.ToLower(f)

		switch {
		case f == "is":
			// is:favorite, which OpenCloud turns into the user's favorites field
			f = "favorite"
		case len(f) > 3:
			f = strings.TrimSuffix(f, "s")
		}

		if f != "" && !slices.Contains(out, f) {
			out = append(out, f)
		}
	}

	return out
}

// key is the field a node names, "" for words and operators.
func key(n ast.Node) string {

	switch n := n.(type) {
	case *ast.StringNode:
		return n.Key
	case *ast.BooleanNode:
		return n.Key
	case *ast.DateTimeNode:
		return n.Key
	case *ast.NumberNode:
		return n.Key
	case *ast.GroupNode:
		return n.Key
	}

	return ""
}

// walk visits the nodes and those inside their groups.
func walk(nodes []ast.Node, visit func(ast.Node)) {
	for _, n := range nodes {
		visit(n)

		if g, ok := n.(*ast.GroupNode); ok {
			walk(g.Nodes, visit)
		}
	}
}

// sectionFixtures is each section's list of fixtures as the report writes it.
func sectionFixtures() map[string]string {
	out := map[string]string{}
	section := ""

	for line := range strings.Lines(parity) {
		line = strings.TrimRight(line, "\n")

		switch {
		case strings.HasPrefix(line, "### "):
			section = strings.TrimPrefix(line, "### ")
		case strings.HasPrefix(line, "- ") && section != "":
			out[section] += line + "\n"
		}
	}

	return out
}
