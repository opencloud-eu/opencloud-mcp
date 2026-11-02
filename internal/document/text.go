// Package document turns a file into the text a model reads and finds its sections by heading.
package document

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/ledongthuc/pdf"
)

var (
	// ErrNotText is a file that is neither text nor a PDF.
	ErrNotText = errors.New("not text")
	// ErrNotPDF is a PDF the reader cannot open.
	ErrNotPDF = errors.New("not a readable PDF")
	// ErrNoText is a PDF without a text layer.
	ErrNoText = errors.New("a PDF without text")
	// ErrTooLarge is a PDF with a page that inflates past what a document holds.
	ErrTooLarge = errors.New("a page too large to read")
)

const (
	// a PDF may declare any page count; past this many pages the rest is not read
	maxPages = 300
	// a page's content inflated past this is a bomb, not a document
	maxPageBytes = 8 << 20
	// reading stops once this much text is in hand
	maxChars = 1 << 20
)

// Text is the text of a file: text as written, the text layer of a PDF.
func Text(name string, data []byte) (string, error) {
	if strings.EqualFold(path.Ext(name), ".pdf") || bytes.HasPrefix(data, []byte("%PDF-")) {
		return pdfText(data)
	}

	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return "", ErrNotText
	}

	return strings.TrimSpace(string(data)), nil
}

// pdfText reads a PDF's text layer page by page; the reader panics on some broken files, which
// count as unreadable.
func pdfText(data []byte) (text string, err error) {
	defer func() {
		if recover() != nil {
			text, err = "", ErrNotPDF
		}
	}()

	r, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", ErrNotPDF
	}

	var b strings.Builder

	fonts := map[string]*pdf.Font{}
	read := 0

	for i := 1; i <= min(r.NumPage(), maxPages) && b.Len() <= maxChars; i++ {
		read = i

		p := r.Page(i)
		if p.V.IsNull() {
			continue
		}

		if !pageFits(p.V.Key("Contents")) {
			return "", ErrTooLarge
		}

		for _, f := range p.Fonts() {
			if _, ok := fonts[f]; !ok {
				font := p.Font(f)
				fonts[f] = &font
			}
		}

		text, err := p.GetPlainText(fonts)
		if err != nil {
			return "", ErrNoText
		}

		b.WriteString(text)
	}

	// whoever reads a PDF in part knows the rest was not read
	if read < r.NumPage() {
		return strings.TrimSpace(b.String()) + fmt.Sprintf("\n\n(Read in part: the first %d of %d pages.)", read, r.NumPage()), nil
	}

	return strings.TrimSpace(b.String()), nil
}

// pageFits measures a page's content through a capped reader before the library inflates it whole.
func pageFits(content pdf.Value) bool {
	streams := []pdf.Value{content}
	if content.Kind() == pdf.Array {
		streams = streams[:0]
		for i := range content.Len() {
			streams = append(streams, content.Index(i))
		}
	}

	left := int64(maxPageBytes)

	for _, s := range streams {
		if s.Kind() != pdf.Stream {
			continue
		}

		rc := s.Reader()
		n, err := io.Copy(io.Discard, io.LimitReader(rc, left+1))
		_ = rc.Close() // the stream is in memory; closing reports nothing worth acting on

		if err != nil || n > left {
			return false
		}

		left -= n
	}

	return true
}
