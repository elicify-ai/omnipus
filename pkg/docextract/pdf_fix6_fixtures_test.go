package docextract

// #1968 FIX6 fixtures: PDFs built from literals with byte-accurate xref
// tables. Hostile shapes use the exact overflowing integers the security
// re-review named (not merely large /Size values).

import (
	"bytes"
	"compress/zlib"
	"encoding/ascii85"
	"fmt"
	"strings"
	"testing"
)

func pdfFix6Assemble(bodies []string) []byte {
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n")
	offs := make([]int, len(bodies))
	for i, b := range bodies {
		offs[i] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", i+1, b)
	}
	xref := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n0000000000 65535 f \n", len(bodies)+1)
	for _, o := range offs {
		fmt.Fprintf(&buf, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(bodies)+1, xref)
	return buf.Bytes()
}

func pdfFix6Stream(dictExtra string, data []byte) string {
	return fmt.Sprintf("<< /Length %d %s >>\nstream\n%s\nendstream", len(data), dictExtra, data)
}

// pdfFix6Doc builds a document with one page per content. Font F1 carries the
// ToUnicode program tu (when non-empty; otherwise no /ToUnicode), font F2 is a
// plain Helvetica. contentDict is added to every content stream dictionary.
func pdfFix6Doc(tu string, contents [][]byte, contentDict string) []byte {
	font1 := "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>"
	tuStream := "<< >>"
	if tu != "" {
		font1 = "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /ToUnicode 4 0 R >>"
		tuStream = pdfFix6Stream("", []byte(tu))
	}
	bodies := append(make([]string, 0, 5+2*len(contents)), "", "", font1, tuStream, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")
	kids := make([]string, 0, len(contents))
	for i, c := range contents {
		pageNum := 6 + 2*i
		kids = append(kids, fmt.Sprintf("%d 0 R", pageNum))
		bodies = append(bodies,
			fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 3 0 R /F2 5 0 R >> >> /Contents %d 0 R >>", pageNum+1),
			pdfFix6Stream(contentDict, c))
	}
	bodies[0] = "<< /Type /Catalog /Pages 2 0 R >>"
	bodies[1] = fmt.Sprintf("<< /Type /Pages /Count %d /Kids [%s] >>", len(contents), strings.Join(kids, " "))
	return pdfFix6Assemble(bodies)
}

const pdfFix6CodespaceAll = "1 begincodespacerange <00> <FF> endcodespacerange\n"

// pdfFix6Identity maps every code to the same code point.
const pdfFix6Identity = pdfFix6CodespaceAll + "1 beginbfrange\n<00> <FF> <0000>\nendbfrange\n"

// pdfFix6MapA maps code 'A' to repl (hex, UTF-16BE).
func pdfFix6MapA(replHex string) string {
	return pdfFix6CodespaceAll + "1 beginbfchar\n<41> <" + replHex + ">\nendbfchar\n"
}

func pdfFix6ThreePageControl() []byte {
	cs := make([][]byte, 0, 3)
	for i := 1; i <= 3; i++ {
		cs = append(cs, []byte(fmt.Sprintf("BT /F2 12 Tf 72 720 Td (PAGE %d text) Tj ET", i)))
	}
	return pdfFix6Doc("", cs, "")
}

// pdfFix6XrefTableOverflow: one xref subsection starting at MaxInt64 with one
// syntactically valid entry (start+count wraps negative in int64).
func pdfFix6XrefTableOverflow() []byte {
	head := "%PDF-1.4\n1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n"
	return []byte(head + fmt.Sprintf("xref\n9223372036854775807 1\n0000000009 00000 n \ntrailer\n<< /Size 3 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(head)))
}

// pdfFix6XrefStreamOverflow: /Size 2, /Index [MaxInt64 1], ordinary /W and one
// complete 3-byte row.
func pdfFix6XrefStreamOverflow() []byte {
	head := "%PDF-1.5\n"
	return []byte(fmt.Sprintf("%s1 0 obj\n<< /Type /XRef /Size 2 /Index [9223372036854775807 1] /W [1 1 1] /Length 3 /Root 2 0 R >>\nstream\n\x01\x09\x00\nendstream\nendobj\nstartxref\n%d\n%%%%EOF\n", head, len(head)))
}

// pdfFix6CmapOverflow: a ToUnicode program whose count is hostile, with no
// pairs on the operand stack. program is the cmap text after the codespace.
func pdfFix6CmapOverflow(program string) []byte {
	return pdfFix6Doc(pdfFix6CodespaceAll+program, [][]byte{[]byte("BT /F1 12 Tf (AAAA) Tj ET")}, "")
}

func pdfFix6CodespaceTU(misses int, hit bool) string {
	var b strings.Builder
	n := misses
	if hit {
		n++
	}
	fmt.Fprintf(&b, "%d begincodespacerange\n", n)
	for i := 0; i < misses; i++ {
		b.WriteString("<01> <01>\n")
	}
	if hit {
		b.WriteString("<41> <41>\n")
	}
	b.WriteString("endcodespacerange\n1 beginbfchar\n<41> <0041>\nendbfchar\n")
	return b.String()
}

// pdfFix6A85Chain is a content stream for /Filter [/FlateDecode /ASCII85Decode]:
// inflating yields lead whitespace bytes then the ASCII85 text of body.
func pdfFix6A85Chain(t *testing.T, lead int, body string) []byte {
	t.Helper()
	var z bytes.Buffer
	w, err := zlib.NewWriterLevel(&z, zlib.BestSpeed)
	if err != nil {
		t.Fatal(err)
	}
	chunk := bytes.Repeat([]byte{' '}, 1<<20)
	for left := lead; left > 0; {
		n := min(left, len(chunk))
		if _, err := w.Write(chunk[:n]); err != nil {
			t.Fatal(err)
		}
		left -= n
	}
	enc := make([]byte, ascii85.MaxEncodedLen(len(body)))
	enc = enc[:ascii85.Encode(enc, []byte(body))]
	if _, err := w.Write(enc); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return z.Bytes()
}

// pdfFix6Fixture returns the PDF for a named resource kind. Kinds prefixed
// "control-" are valid documents next to the hostile ones.
func pdfFix6Fixture(t *testing.T, kind string) []byte {
	t.Helper()
	switch kind {
	case "control-three-pages":
		return pdfFix6ThreePageControl()
	case "xref-table-overflow":
		return pdfFix6XrefTableOverflow()
	case "xref-stream-overflow":
		return pdfFix6XrefStreamOverflow()
	case "cmap-bfchar-1<<62":
		return pdfFix6CmapOverflow("4611686018427387904 beginbfchar\nendbfchar\n")
	case "cmap-bfrange-1<<62":
		return pdfFix6CmapOverflow("4611686018427387904 beginbfrange\nendbfrange\n")
	case "cmap-bfrange-3n-wraps-to-2":
		// 3 * 6148914691236517206 = 2^64 + 2: the product wraps to 2, which
		// an overflowing admission check compares as "fits two operands".
		return pdfFix6CmapOverflow("6148914691236517206 beginbfrange\n<41> <42>\nendbfrange\n")
	case "control-codespace":
		return pdfFix6Doc(pdfFix6CodespaceTU(30000, true), [][]byte{[]byte("BT /F1 12 Tf (" + strings.Repeat("A", 1024) + ") Tj ET")}, "")
	case "codespace-miss-1MiB":
		return pdfFix6Doc(pdfFix6CodespaceTU(30000, false), [][]byte{[]byte("BT /F1 12 Tf (" + strings.Repeat("A", 1<<20) + ") Tj ET")}, "")
	case "control-filter-chain":
		return pdfFix6Doc(pdfFix6Identity, [][]byte{pdfFix6A85Chain(t, 1<<20, "BT /F1 12 Tf (hello) Tj ET")}, "/Filter [/FlateDecode /ASCII85Decode]")
	case "filter-chain-whitespace":
		// Inflates to pdfWorkBudget + 64 MiB of spaces (over the work budget)
		// and decodes to nothing: the final output is empty.
		return pdfFix6Doc(pdfFix6Identity, [][]byte{pdfFix6A85Chain(t, pdfWorkBudget+64<<20, "BT /F1 12 Tf (hello) Tj ET")}, "/Filter [/FlateDecode /ASCII85Decode]")
	}
	t.Fatalf("unknown fix6 fixture %q", kind)
	return nil
}
