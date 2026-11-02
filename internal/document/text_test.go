package document

import (
	"bytes"
	"compress/zlib"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// pdfOf builds a one-page PDF whose page content is the given stream, declaring pages as its page
// count, so a test can hand the reader a file shaped as it likes.
func pdfOf(t *testing.T, content []byte, deflate bool, pages int) []byte {
	t.Helper()

	filter := ""
	if deflate {
		var z bytes.Buffer
		w := zlib.NewWriter(&z)
		if _, err := w.Write(content); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		content, filter = z.Bytes(), " /Filter /FlateDecode"
	}

	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		fmt.Sprintf("<< /Type /Pages /Kids [3 0 R] /Count %d >>", pages),
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>",
		fmt.Sprintf("<< /Length %d%s >>\nstream\n%s\nendstream", len(content), filter, content),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}

	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")

	offsets := make([]int, len(objects))
	for i, o := range objects {
		offsets[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}

	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, off := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)

	return b.Bytes()
}

func TestAPDFsTextIsRead(t *testing.T) {
	text, err := Text("note.pdf", pdfOf(t, []byte("BT /F1 12 Tf 72 712 Td (Restart the agent first) Tj ET"), false, 1))
	if err != nil || !strings.Contains(text, "Restart the agent first") || strings.Contains(text, "Read in part") {
		t.Fatalf("text %q, err %v", text, err)
	}
}

func TestAPDFThatInflatesPastAPageIsRefused(t *testing.T) {
	bomb := bytes.Repeat([]byte{' '}, maxPageBytes+1024)

	if _, err := Text("bomb.pdf", pdfOf(t, bomb, true, 1)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("an inflating page was read: %v", err)
	}
}

func TestAPDFDeclaringEndlessPagesEndsQuickly(t *testing.T) {
	start := time.Now()

	text, err := Text("endless.pdf", pdfOf(t, []byte("BT /F1 12 Tf 72 712 Td (One page) Tj ET"), false, 1_000_000_000))
	if err != nil || !strings.Contains(text, "One page") || !strings.HasSuffix(text, "\n\n(Read in part: the first 300 of 1000000000 pages.)") {
		t.Fatalf("text %q, err %v", text, err)
	}

	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("a declared page count of a billion took %s", took)
	}
}

func TestTextIsReadAsWrittenAndAnythingElseRefused(t *testing.T) {
	if text, err := Text("notes.md", []byte("\xef\xbb\xbf# Notes\n\nRestart first.\n")); err != nil || text != "# Notes\n\nRestart first." {
		t.Errorf("markdown %q, err %v", text, err)
	}

	if _, err := Text("tool.bin", []byte{0x00, 0x01, 0xff}); !errors.Is(err, ErrNotText) {
		t.Errorf("a binary file was read: %v", err)
	}
}
