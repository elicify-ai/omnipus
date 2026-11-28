package pdf

// PR #1984 CodeQL alerts (go/incorrect-integer-conversion, go/index-out-of-bounds).
// Each test feeds a number just inside and just outside the range of the type
// the parser converts it to. A value out of range must take the existing
// malformed-number path of its spot, never wrap into a valid-looking value.
// Expected values come from the PDF syntax of the fixture and the type ranges
// (uint16 0..65535, uint32 0..4294967295, int32 -2147483648..2147483647).

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
)

// codeqlOpen opens data, turning any escaping panic into an error.
func codeqlOpen(data []byte, lim Limits) (r *Reader, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("panic: %v", p)
		}
	}()
	return NewReader(bytes.NewReader(data), int64(len(data)), lim)
}

// codeqlText reads all text, turning any escaping panic into an error.
func codeqlText(r *Reader) (text string, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("panic: %v", p)
		}
	}()
	rd, err := r.GetPlainText()
	if rd != nil {
		b, readErr := io.ReadAll(rd)
		if readErr != nil && err == nil {
			err = readErr
		}
		text = string(b)
	}
	return text, err
}

var codeqlLim = Limits{Work: 1 << 26, Text: 1 << 20, Time: time.Hour}

// codeqlXrefTablePDF is a one-page document with a classic xref table whose
// row for object 1 (the catalog) carries the generation field gen1; the
// catalog is defined as "1 defGen obj" and the trailer names it "1 defGen R".
func codeqlXrefTablePDF(gen1 string, defGen int) []byte {
	bodies := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Count 1 /Kids [3 0 R] >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>",
		fix6Stream("", []byte("BT /F1 12 Tf (gen) Tj ET")),
		fix10PlainFont,
	}
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n")
	offs := make([]int, len(bodies))
	for i, b := range bodies {
		offs[i] = buf.Len()
		gen := 0
		if i == 0 {
			gen = defGen
		}
		fmt.Fprintf(&buf, "%d %d obj\n%s\nendobj\n", i+1, gen, b)
	}
	xref := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n0000000000 65535 f \n", len(bodies)+1)
	for i, o := range offs {
		gen := "00000"
		if i == 0 {
			gen = gen1
		}
		fmt.Fprintf(&buf, "%010d %s n \n", o, gen)
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 %d R >>\nstartxref\n%d\n%%%%EOF\n", len(bodies)+1, defGen, xref)
	return buf.Bytes()
}

// read.go readXrefTableData (alert at the uint16(gen) conversion): an xref
// table generation outside 0..65535 is a malformed row, like a non-integer
// one. Before the fix 65536 wrapped to 0 (so "1 0 R" resolved to it) and -1
// wrapped to 65535.
func TestCodeQL1984_XrefTableGenerationRange(t *testing.T) {
	t.Run("control: generation 0 opens and extracts", func(t *testing.T) {
		r, err := codeqlOpen(codeqlXrefTablePDF("00000", 0), codeqlLim)
		if err != nil {
			t.Fatalf("control must open: %v", err)
		}
		if text, err := codeqlText(r); err != nil || strings.TrimSpace(text) != "gen" {
			t.Fatalf("control text=%q err=%v, want %q", text, err, "gen")
		}
	})
	t.Run("boundary: generation 65535 is kept and resolves end to end", func(t *testing.T) {
		// Row, definition ("1 65535 obj") and reference ("1 65535 R") all
		// use 65535, so the catalog must resolve and the text must read.
		r, err := codeqlOpen(codeqlXrefTablePDF("65535", 65535), codeqlLim)
		if err != nil {
			t.Fatalf("generation 65535 is in range and must be accepted: %v", err)
		}
		if got, want := r.xref[1].ptr, (objptr{1, 65535}); got != want {
			t.Fatalf("xref[1].ptr = %v, want %v", got, want)
		}
		if text, err := codeqlText(r); err != nil || strings.TrimSpace(text) != "gen" {
			t.Fatalf("1 65535 R: text=%q err=%v, want %q", text, err, "gen")
		}
	})
	for _, gen := range []string{"65536", "70000", "-1", "1099511627776"} {
		t.Run("generation "+gen+" is a malformed xref table", func(t *testing.T) {
			r, err := codeqlOpen(codeqlXrefTablePDF(gen, 0), codeqlLim)
			if err == nil {
				t.Fatalf("generation %s is outside 0..65535 and must be rejected; the row was stored as %v", gen, r.xref[1].ptr)
			}
			if !strings.Contains(err.Error(), "malformed xref table") {
				t.Fatalf("must take the malformed-xref-table path, got %v", err)
			}
		})
	}
}

// lex.go readObject (alerts at the uint16(t2) conversions): "a b R" is an
// object reference only when a fits uint32 and b fits uint16; otherwise the
// numbers stay plain integers.
func TestCodeQL1984_ObjectReferenceRange(t *testing.T) {
	cases := []struct {
		src  string
		want object
	}{
		{"1 65535 R", objptr{1, 65535}},
		{"1 65536 R", int64(1)},
		{"1 70000 R", int64(1)},
		{"1 -1 R", int64(1)},
		{"4294967295 0 R", objptr{4294967295, 0}},
		{"4294967296 0 R", int64(4294967296)},
		{"-1 0 R", int64(-1)},
	}
	for _, tc := range cases {
		t.Run(tc.src, func(t *testing.T) {
			b := newBuffer(strings.NewReader(tc.src+" "), 0, nil)
			b.allowEOF = true
			if got := b.readObject(); got != tc.want {
				t.Fatalf("readObject(%q) = %#v, want %#v", tc.src, got, tc.want)
			}
		})
	}
	t.Run("in-range definition: 1 65535 obj", func(t *testing.T) {
		b := newBuffer(strings.NewReader("1 65535 obj 5 endobj "), 0, nil)
		b.allowEOF = true
		def, ok := b.readObject().(objdef)
		if !ok || def.ptr != (objptr{1, 65535}) || def.obj != int64(5) {
			t.Fatalf("got %#v, want objdef{1 65535, 5}", def)
		}
	})
}

// read.go readXrefStreamData (alerts at int(n), int(start), and the
// index[1] read in the section loop): start and count are bounded by /Size
// (itself at most maxXrefEntries) and /Index must have an even length.
func TestCodeQL1984_XrefStreamIndexRange(t *testing.T) {
	rows := []byte{1, 0, 0, 0, 0, 0}
	cases := []struct {
		name, index, fragment string
		structural            bool
	}{
		{"odd Index length", "0", "invalid Index array", false},
		{"odd Index length 3", "0 1 0", "invalid Index array", false},
		{"start 2^40", "1099511627776 1", "outside Size", true},
		{"count 2^40", "0 1099511627776", "exceed Size", true},
		{"start past Size", "2 1", "outside Size", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := codeqlOpen(fix7XrefStreamPDF(tc.index, "1 4 1", 2, rows), codeqlLim)
			if err == nil || strings.HasPrefix(err.Error(), "panic: runtime error") {
				t.Fatalf("Index [%s] must be rejected as an error, got %v", tc.index, err)
			}
			if tc.structural && !errors.Is(err, ErrStructureLimit) {
				t.Fatalf("Index [%s] must end with ErrStructureLimit, got %v", tc.index, err)
			}
			if !strings.Contains(err.Error(), tc.fragment) {
				t.Fatalf("Index [%s] must name %q, got %v", tc.index, tc.fragment, err)
			}
		})
	}
	t.Run("control: a real xref stream still opens and extracts", func(t *testing.T) {
		r, err := codeqlOpen(fix7ValidXrefStreamPDF(), codeqlLim)
		if err != nil {
			t.Fatalf("control must open: %v", err)
		}
		if text, err := codeqlText(r); err != nil || strings.TrimSpace(text) != "ctl" {
			t.Fatalf("control text=%q err=%v", text, err)
		}
	})
}

// read.go readXrefTableData (alert at int(n)): a section count of 2^40 is
// refused by the maxXrefEntries bound, not converted.
func TestCodeQL1984_XrefTableSectionRange(t *testing.T) {
	for _, sec := range []string{"0 1099511627776", "1099511627776 1", "0 1048577"} {
		t.Run(sec, func(t *testing.T) {
			// The reader reads the last 100 bytes for %%EOF, so the comment
			// line pads the file past that; startxref points after it.
			head := "%PDF-1.4\n%" + strings.Repeat("x", 100) + "\n"
			data := []byte(fmt.Sprintf("%sxref\n%s\n0000000000 65535 f \ntrailer\n<< /Size 1 >>\nstartxref\n%d\n%%%%EOF\n", head, sec, len(head)))
			_, err := codeqlOpen(data, codeqlLim)
			if !errors.Is(err, ErrStructureLimit) {
				t.Fatalf("section %q must end with ErrStructureLimit, got %v", sec, err)
			}
		})
	}
}

// page.go FirstChar/LastChar (alerts at int(...Int64())): a character code is
// at most a few bytes wide, so a value outside int32 is malformed and reads as
// 0, the value of a missing or non-integer entry.
func TestCodeQL1984_FontCharRange(t *testing.T) {
	cases := []struct {
		v    int64
		want int
	}{
		{32, 32},
		{2147483647, 2147483647},
		{-2147483648, -2147483648},
		{2147483648, 0},
		{-2147483649, 0},
		{1 << 40, 0},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprint(tc.v), func(t *testing.T) {
			f := Font{V: Value{data: dict{name("FirstChar"): tc.v, name("LastChar"): tc.v}}}
			if got := f.FirstChar(); got != tc.want {
				t.Fatalf("FirstChar() = %d, want %d", got, tc.want)
			}
			if got := f.LastChar(); got != tc.want {
				t.Fatalf("LastChar() = %d, want %d", got, tc.want)
			}
		})
	}
}

// page.go newDictEncoder (alert at n = int(x.Int64())): a /Differences code
// outside 0..255 names no byte, whatever its low bits. 4294967361 is
// 2^32 + 65: truncated to 32 bits it would be 65 ('A') and remap A to B.
func TestCodeQL1984_DifferencesCodeRange(t *testing.T) {
	differencesFont := func(diffs string) string {
		return "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding << /Differences [" + diffs + "] >> >>"
	}
	cases := []struct{ diffs, content, want string }{
		{"65 /B", "(A)", "B"},                 // control: an in-range code remaps
		{"255 /B", "(\\377A)", "BA"},          // boundary: the last byte code
		{"256 /B", "(A)", "A"},                // boundary + 1: names no byte
		{"4294967361 /B", "(A)", "A"},         // 2^32 + 65 must not become 65
		{"-4294967231 /B", "(A)", "A"},        // -2^32 + 65 must not become 65
		{"4294967361 /B 66 /C", "(AB)", "AC"}, // a later in-range code still applies
	}
	for _, tc := range cases {
		t.Run(tc.diffs, func(t *testing.T) {
			r, err := codeqlOpen(fix10Doc(differencesFont(tc.diffs), "BT /F1 12 Tf "+tc.content+" Tj ET\n"), codeqlLim)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			text, err := codeqlText(r)
			if err != nil || strings.TrimSpace(text) != tc.want {
				t.Fatalf("Differences [%s]: text=%q err=%v, want %q", tc.diffs, text, err, tc.want)
			}
		})
	}
}

// codeqlObjStmDoc describes a one-page document indexed by an xref stream
// (/W [1 5 3]) whose font (object 5) lives in an object stream (object 6) as
// the SECOND of two entries.
type codeqlObjStmDoc struct {
	n             string // the object stream's /N
	fontHeaderID  string // the id the object stream header gives the font
	catalogGen    uint64 // generation field of the catalog's (type 1) xref row
	catalogDefGen int    // "1 <gen> obj" and the trailer's "/Root 1 <gen> R"
	fontStream    uint64 // object stream field of the font's (type 2) xref row
}

// codeqlObjStmPDF is the document with /N n and every other field valid.
func codeqlObjStmPDF(n string) []byte {
	return codeqlObjStmDoc{n: n, fontHeaderID: "5", fontStream: 6}.build()
}

func (d codeqlObjStmDoc) build() []byte {
	const font = "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>"
	const other = "<< /Unused 1 >>"
	header := fmt.Sprintf("7 0 %s %d ", d.fontHeaderID, len(other)+1)
	objstm := header + other + " " + font
	bodies := map[int]string{
		1: "<< /Type /Catalog /Pages 2 0 R >>",
		2: "<< /Type /Pages /Count 1 /Kids [3 0 R] >>",
		3: "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>",
		4: fix6Stream("", []byte("BT /F1 12 Tf (objstm) Tj ET")),
		6: fmt.Sprintf("<< /Type /ObjStm /N %s /First %d /Length %d >>\nstream\n%s\nendstream", d.n, len(header), len(objstm), objstm),
	}
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.5\n")
	offs := map[int]int{}
	for _, id := range []int{1, 2, 3, 4, 6} {
		offs[id] = buf.Len()
		gen := 0
		if id == 1 {
			gen = d.catalogDefGen
		}
		fmt.Fprintf(&buf, "%d %d obj\n%s\nendobj\n", id, gen, bodies[id])
	}
	const xrefObj = 8
	offs[xrefObj] = buf.Len()
	row := func(typ byte, f2, f3 uint64) []byte {
		return []byte{typ, byte(f2 >> 32), byte(f2 >> 24), byte(f2 >> 16), byte(f2 >> 8), byte(f2), byte(f3 >> 16), byte(f3 >> 8), byte(f3)}
	}
	rows := row(0, 0, 0xFFFF)
	for id := 1; id <= xrefObj; id++ {
		switch id {
		case 1:
			rows = append(rows, row(1, uint64(offs[id]), d.catalogGen)...)
		case 5:
			rows = append(rows, row(2, d.fontStream, 1)...)
		case 7:
			rows = append(rows, row(2, 6, 0)...)
		default:
			rows = append(rows, row(1, uint64(offs[id]), 0)...)
		}
	}
	fmt.Fprintf(&buf, "%d 0 obj\n<< /Type /XRef /Size %d /W [1 5 3] /Length %d /Root 1 %d R >>\nstream\n%s\nendstream\nendobj\n",
		xrefObj, xrefObj+1, len(rows), d.catalogDefGen, rows)
	fmt.Fprintf(&buf, "startxref\n%d\n%%%%EOF\n", offs[xrefObj])
	return buf.Bytes()
}

// read.go resolve (alert at n := int(strm.Key("N").Int64())): an object
// stream's /N bounds the header scan, which also ends at the first non-pair.
// 4294967297 is 2^32 + 1: truncated to 32 bits it would be 1 and stop the scan
// before the font, the second entry.
func TestCodeQL1984_ObjectStreamCountRange(t *testing.T) {
	for _, n := range []string{"2", "2147483647", "2147483648", "4294967297", "1099511627776"} {
		t.Run("N "+n, func(t *testing.T) {
			r, err := codeqlOpen(codeqlObjStmPDF(n), codeqlLim)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			if text, err := codeqlText(r); err != nil || strings.TrimSpace(text) != "objstm" {
				t.Fatalf("N %s: text=%q err=%v, want %q", n, text, err, "objstm")
			}
		})
	}
	t.Run("control: N 1 stops before the font", func(t *testing.T) {
		r, err := codeqlOpen(codeqlObjStmPDF("1"), codeqlLim)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		if text, err := codeqlText(r); err == nil && strings.TrimSpace(text) == "objstm" {
			t.Fatalf("N 1 lists only object 7; the font (second entry) must not be found, got text %q", text)
		}
	})
}

// read.go readXrefStreamData (review follow-up to the alert class): an xref
// stream field is up to 8 bytes wide, so a type 1 generation or a type 2
// object stream number can exceed uint16 / uint32. Such a row is malformed and
// is skipped like a row of unknown type, instead of wrapping: generation 65536
// would become 0 (so "1 0 R" resolved to it) and stream 4294967302 (2^32 + 6)
// would become 6.
func TestCodeQL1984_XrefStreamFieldRange(t *testing.T) {
	valid := codeqlObjStmDoc{n: "2", fontHeaderID: "5", fontStream: 6}
	t.Run("control: valid fields open and extract", func(t *testing.T) {
		r, err := codeqlOpen(valid.build(), codeqlLim)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		if text, err := codeqlText(r); err != nil || strings.TrimSpace(text) != "objstm" {
			t.Fatalf("control text=%q err=%v, want %q", text, err, "objstm")
		}
	})
	t.Run("boundary: generation 65535 resolves end to end", func(t *testing.T) {
		d := valid
		d.catalogGen, d.catalogDefGen = 65535, 65535
		r, err := codeqlOpen(d.build(), codeqlLim)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		if got, want := r.xref[1].ptr, (objptr{1, 65535}); got != want {
			t.Fatalf("xref[1].ptr = %v, want %v", got, want)
		}
		if text, err := codeqlText(r); err != nil || strings.TrimSpace(text) != "objstm" {
			t.Fatalf("1 65535 R: text=%q err=%v, want %q", text, err, "objstm")
		}
	})
	for _, gen := range []uint64{65536, 65536 + 65535, 1<<24 - 1} {
		t.Run(fmt.Sprintf("generation %d is not stored", gen), func(t *testing.T) {
			d := valid
			d.catalogGen = gen
			r, err := codeqlOpen(d.build(), codeqlLim)
			if err != nil {
				t.Fatalf("a malformed row is skipped like an unknown-type row, so the file still opens: %v", err)
			}
			if got := r.xref[1].ptr; got != (objptr{}) {
				t.Fatalf("generation %d must not be stored, got xref[1].ptr = %v", gen, got)
			}
			if text, err := codeqlText(r); err == nil && strings.TrimSpace(text) == "objstm" {
				t.Fatalf("generation %d: \"1 0 R\" must not resolve to that row, got text %q", gen, text)
			}
		})
	}
	t.Run("boundary: object stream 4294967295 is stored", func(t *testing.T) {
		d := valid
		d.fontStream = 4294967295
		r, err := codeqlOpen(d.build(), codeqlLim)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		if got := r.xref[5]; !got.inStream || got.stream != (objptr{4294967295, 0}) {
			t.Fatalf("xref[5] = %+v, want in stream 4294967295", got)
		}
	})
	for _, strm := range []uint64{4294967296 + 6, 1<<40 - 1} {
		t.Run(fmt.Sprintf("object stream %d is not stored", strm), func(t *testing.T) {
			d := valid
			d.fontStream = strm
			r, err := codeqlOpen(d.build(), codeqlLim)
			if err != nil {
				t.Fatalf("a malformed row is skipped like an unknown-type row, so the file still opens: %v", err)
			}
			if got := r.xref[5]; got.inStream || got.ptr != (objptr{}) {
				t.Fatalf("object stream %d must not be stored, got xref[5] = %+v", strm, got)
			}
			// A missing font still shows its text (the default encoding),
			// so the oracle is object 5 itself: it must not resolve.
			if v := r.resolve(objptr{}, objptr{5, 0}); v.Kind() != Null {
				t.Fatalf("object stream %d: object 5 must not resolve through stream 6, got %v", strm, v)
			}
		})
	}
}

// read.go resolve (review follow-up): an object stream header id is compared
// as lexed, not modulo 2^32. 4294967301 is 2^32 + 5 and -4294967291 is
// -2^32 + 5; neither names object 5.
func TestCodeQL1984_ObjectStreamHeaderID(t *testing.T) {
	t.Run("control: header id 5 resolves", func(t *testing.T) {
		r, err := codeqlOpen(codeqlObjStmDoc{n: "2", fontHeaderID: "5", fontStream: 6}.build(), codeqlLim)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		if text, err := codeqlText(r); err != nil || strings.TrimSpace(text) != "objstm" {
			t.Fatalf("control text=%q err=%v, want %q", text, err, "objstm")
		}
	})
	for _, id := range []string{"4294967301", "-4294967291"} {
		t.Run("header id "+id, func(t *testing.T) {
			r, err := codeqlOpen(codeqlObjStmDoc{n: "2", fontHeaderID: id, fontStream: 6}.build(), codeqlLim)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			if text, err := codeqlText(r); err == nil && strings.TrimSpace(text) == "objstm" {
				t.Fatalf("header id %s must not resolve as object 5, got text %q", id, text)
			}
		})
	}
}
