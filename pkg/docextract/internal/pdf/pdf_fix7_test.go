package pdf

// #1968 FIX7 (gate 3: SEC3-1, SEC3-2, SEC3-3, SF3-2). Parser-level regression
// tests. They reuse the FIX6 helpers of this package (fix6Assemble, fix6OnePage,
// fix6Read, fix6A85Chain). Work budgets are small so that a missing meter or a
// precharge shows as a work-limit error (or its absence) by arithmetic, not by
// a stopwatch; where memory is the subject the test counts allocated bytes.
// Expected values come from fixture arithmetic and the Limits contract.

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fix7Open opens data and returns the error, converting an escaping panic that
// carries an error (the parser's budget stops) into that error.
func fix7Open(data []byte, lim Limits) (r *Reader, err error) {
	defer func() {
		if p := recover(); p != nil {
			if e, ok := p.(error); ok {
				err = e
				return
			}
			panic(p)
		}
	}()
	return NewReader(bytes.NewReader(data), int64(len(data)), lim)
}

func fix7Stopped(err error) bool {
	return errors.Is(err, ErrWorkLimit) || errors.Is(err, ErrStructureLimit)
}

// fix7XrefStreamPDF is a file whose only object is an xref stream.
func fix7XrefStreamPDF(index, w string, size int, rows []byte) []byte {
	head := "%PDF-1.5\n"
	return []byte(fmt.Sprintf("%s1 0 obj\n<< /Type /XRef /Size %d /Index [%s] /W [%s] /Length %d /Root 2 0 R >>\nstream\n%s\nendstream\nendobj\nstartxref\n%d\n%%%%EOF\n",
		head, size, index, w, len(rows), rows, len(head)))
}

// fix7ValidXrefStreamPDF is a real one-page document indexed by an xref stream
// (W [1 4 1], one row per object), the positive control for SEC3-1.
func fix7ValidXrefStreamPDF() []byte {
	bodies := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Count 1 /Kids [3 0 R] >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>",
		fix6Stream("", []byte("BT /F1 12 Tf (ctl) Tj ET")),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.5\n")
	offs := make([]int, 0, len(bodies)+1)
	for i, b := range bodies {
		offs = append(offs, buf.Len())
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", i+1, b)
	}
	xrefObj := len(bodies) + 1
	offs = append(offs, buf.Len())
	row := func(typ byte, off int) []byte {
		return []byte{typ, byte(off >> 24), byte(off >> 16), byte(off >> 8), byte(off), 0}
	}
	rows := row(0, 0)
	rows[5] = 0xFF
	for _, o := range offs {
		rows = append(rows, row(1, o)...)
	}
	fmt.Fprintf(&buf, "%d 0 obj\n<< /Type /XRef /Size %d /Index [0 %d] /W [1 4 1] /Length %d /Root 1 0 R >>\nstream\n%s\nendstream\nendobj\n",
		xrefObj, xrefObj+1, xrefObj+1, len(rows), rows)
	fmt.Fprintf(&buf, "startxref\n%d\n%%%%EOF\n", offs[len(offs)-1])
	return buf.Bytes()
}

// SEC3-1 / G3-1 / SF3-1: /Size 1048576, /W [0 0 0], /Length 0 and /Index
// [0 1048576] repeated 1,024 times is 1,073,741,824 zero-width rows from 10 KiB
// of metadata; each row must cost work. Against an 8 MiB budget the parse must
// end with a work or structure error.
func TestFix7_ZeroWidthXrefRowsAreMetered(t *testing.T) {
	lim := Limits{Work: 8 << 20, Text: 1 << 20}

	t.Run("control: a real xref stream still opens and extracts", func(t *testing.T) {
		data := fix7ValidXrefStreamPDF()
		r, err := fix7Open(data, lim)
		if err != nil {
			t.Fatalf("control must open: %v", err)
		}
		rd, err := r.GetPlainText()
		if err != nil {
			t.Fatalf("control text: %v", err)
		}
		b, _ := io.ReadAll(rd)
		if got := strings.TrimSpace(string(b)); got != "ctl" {
			t.Fatalf("control text = %q, want %q", got, "ctl")
		}
	})
	t.Run("a billion zero-width rows end in a work or structure error", func(t *testing.T) {
		data := fix7XrefStreamPDF(strings.Repeat("0 1048576 ", 1024), "0 0 0", 1048576, nil)
		if len(data) > 12_000 {
			t.Fatalf("fixture should be about 10 KiB of metadata, got %d bytes", len(data))
		}
		done := make(chan error, 1)
		go func() { _, err := fix7Open(data, lim); done <- err }()
		select {
		case err := <-done:
			if !fix7Stopped(err) {
				t.Fatalf("SEC3-1: 1,073,741,824 zero-width rows against an 8 MiB work budget must end with ErrWorkLimit or ErrStructureLimit, got %v", err)
			}
		case <-time.After(30 * time.Second):
			t.Fatal("SEC3-1: the zero-width row loop did not stop within 30s")
		}
	})
}

// SEC3-2 / G3-2: 100,000 /ASCII85Decode stages on an empty content stream. A
// decoder retains two 1 KiB arrays, so building the chain costs about 200 MB.
// Construction must be refused (work or structure error) or stay small (under
// 64 MiB allocated). A normal two-stage chain stays the control.
func TestFix7_LongFilterChainIsBoundedBeforeConstruction(t *testing.T) {
	const tu = "1 begincodespacerange <00> <FF> endcodespacerange\n1 beginbfrange\n<00> <FF> <0000>\nendbfrange\n"
	lim := Limits{Work: 1 << 29, Text: 1 << 20}

	t.Run("control: Flate then ASCII85 still extracts", func(t *testing.T) {
		data := fix6OnePage(tu, fix6A85Chain(t, 1024, "BT /F1 12 Tf (hello) Tj ET"), "/Filter [/FlateDecode /ASCII85Decode]")
		text, err := fix6Read(t, data, lim)
		if err != nil || strings.TrimSpace(text) != "hello" {
			t.Fatalf("control: text=%q err=%v", text, err)
		}
	})
	t.Run("100,000 stages", func(t *testing.T) {
		chain := "/Filter [" + strings.Repeat("/ASCII85Decode ", 100_000) + "]"
		data := fix6OnePage(tu, nil, chain)
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		text, err := fix6Read(t, data, lim)
		runtime.ReadMemStats(&after)
		alloc := after.TotalAlloc - before.TotalAlloc
		if !fix7Stopped(err) && alloc > 64<<20 {
			t.Fatalf("SEC3-2: a 100,000-stage filter chain must be rejected (err=%v, text=%q) or stay under 64 MiB, but allocated %d bytes", err, text, alloc)
		}
	})
}

// SEC3-3 / G3-3 / SF3-3: 30,000 distinct two-byte singleton codespace ranges, the
// matching range <0063> examined first, 20,000 glyphs <0063>. One range is
// examined per glyph, so the honest cost is about 40,000 units against an 8 MiB
// budget; a charge for all 30,000 ranges per glyph (6e8) is an overcharge that
// loses readable text.
func TestFix7_CodespaceChargeIsForVisitedRanges(t *testing.T) {
	tu := fix7CodespaceTU(30000)
	lim := Limits{Work: 8 << 20, Text: 1 << 20}

	t.Run("early match: 20,000 glyphs extract in full", func(t *testing.T) {
		content := []byte("BT /F1 12 Tf <" + strings.Repeat("0063", 20_000) + "> Tj ET")
		text, err := fix6Read(t, fix6OnePage(tu, content, ""), lim)
		if err != nil {
			t.Fatalf("SEC3-3: readable text must extract: %v", err)
		}
		if got, want := strings.TrimSpace(text), strings.Repeat("A", 20_000); got != want {
			t.Fatalf("text has %d characters, want %d 'A'", len(got), len(want))
		}
	})
	t.Run("miss-heavy: 5,000 codes outside every codespace range consume one byte each", func(t *testing.T) {
		// The declared codespace is 0x0063 and 0x1000..0x1000+n-2 (0x852E
		// for n = 30,000), so <FFFF> lies in no range: a genuine codespace
		// miss, which emits U+FFFD and consumes ONE byte. 5,000 repeated
		// codes are 10,000 bytes, hence 10,000 U+FFFD. (The cost of a miss is
		// judged in pdf_fix8_test.go by elapsed time, not by the work counter.)
		const top = 0x1000 + 30000 - 2
		if 0xFFFF <= top || 0xFFFF == 0x0063 {
			t.Fatal("fixture precondition: FFFF must lie outside the declared codespace")
		}
		content := []byte("BT /F1 12 Tf <" + strings.Repeat("FFFF", 5_000) + "> Tj ET")
		text, err := fix6Read(t, fix6OnePage(tu, content, ""), lim)
		switch {
		case errors.Is(err, ErrWorkLimit):
			// a metered linear scan may stop here; it must not be silent
		case err != nil:
			t.Fatalf("unexpected error %v", err)
		default:
			if got, want := strings.TrimPrefix(text, "\n"), strings.Repeat("�", 10_000); got != want {
				t.Fatalf("a genuine miss must give one U+FFFD per consumed byte: %d runes, want 10,000", len([]rune(got)))
			}
		}
	})
}

// fix7CodespaceTU declares n distinct two-byte singleton ranges. The matching
// range <0063> is listed LAST: readCmap pops ranges from the end of the
// program, so it is the first examined. It maps <0063> to U+0041.
func fix7CodespaceTU(n int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d begincodespacerange\n", n)
	for i := 0; i < n-1; i++ {
		fmt.Fprintf(&b, "<%04X> <%04X>\n", 0x1000+i, 0x1000+i)
	}
	b.WriteString("<0063> <0063>\nendcodespacerange\n1 beginbfchar\n<0063> <0041>\nendbfchar\n")
	return b.String()
}

// SF3-2: a CMap the parser rejects (count that cannot be honoured) must be a
// parse error, never a silent fall back to treating the raw codes as text.
func TestFix7_RejectedCmapIsAnErrorNotRawText(t *testing.T) {
	cases := map[string]string{
		"bfchar 1<<62":          "4611686018427387904 beginbfchar\nendbfchar\n",
		"bfrange 1<<62":         "4611686018427387904 beginbfrange\nendbfrange\n",
		"bfrange 3n wraps to 2": "6148914691236517206 beginbfrange\n<41> <42>\nendbfrange\n",
		"codespace count 1<<62": "4611686018427387904 begincodespacerange\nendcodespacerange\n",
	}
	content := []byte("BT /F1 12 Tf (AAAA) Tj ET")
	for name, program := range cases {
		t.Run(name, func(t *testing.T) {
			tu := "1 begincodespacerange <00> <FF> endcodespacerange\n" + program
			text, err := fix6Read(t, fix6OnePage(tu, content, ""), Limits{Work: 1 << 28, Text: 1 << 20})
			if err == nil {
				t.Fatalf("SF3-2: a rejected CMap must surface an error; got success with %q", text)
			}
			if strings.Contains(text, "AAAA") {
				t.Fatalf("SF3-2: the raw codes %q must not be returned as text beside the error %v", text, err)
			}
		})
	}
	t.Run("control: a valid CMap decodes", func(t *testing.T) {
		tu := "1 begincodespacerange <00> <FF> endcodespacerange\n1 beginbfchar\n<41> <0042>\nendbfchar\n"
		text, err := fix6Read(t, fix6OnePage(tu, content, ""), Limits{Work: 1 << 28, Text: 1 << 20})
		if err != nil || strings.TrimSpace(text) != "BBBB" {
			t.Fatalf("control: text=%q err=%v", text, err)
		}
	})
}

// Limits.Time is a wall-clock ceiling checked every 1,024 charges (limits.go
// contract: past the ceiling the parse ends with an error wrapping
// ErrWorkLimit; a negative Time is refused at open).
func TestFix7_WallClockCeiling(t *testing.T) {
	// Each decoded glyph charges at least one unit, so 5,000 glyphs are over
	// 1,024 charges and a 1ns ceiling has passed at the first clock read.
	data := fix6OnePage(fix7CodespaceTU(2), []byte("BT /F1 12 Tf <"+strings.Repeat("0063", 5_000)+"> Tj ET"), "")

	t.Run("1ns ceiling ends the parse with ErrWorkLimit", func(t *testing.T) {
		r, err := fix7Open(data, Limits{Work: 1 << 29, Text: 1 << 20, Time: time.Nanosecond})
		if err == nil {
			_, err = fix7PlainText(r)
		}
		if !errors.Is(err, ErrWorkLimit) {
			t.Fatalf("a 1ns ceiling on a document needing over 1,024 charges must wrap ErrWorkLimit, got %v", err)
		}
	})
	t.Run("negative Time is refused", func(t *testing.T) {
		if _, err := fix7Open(data, Limits{Work: 1 << 29, Text: 1 << 20, Time: -time.Second}); err == nil {
			t.Fatal("a negative Limits.Time must be refused at open")
		}
	})
	t.Run("control: zero Time uses the default ceiling and extracts", func(t *testing.T) {
		r, err := fix7Open(data, Limits{Work: 1 << 29, Text: 1 << 20})
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		text, err := fix7PlainText(r)
		if err != nil || strings.TrimSpace(text) != strings.Repeat("A", 5_000) {
			t.Fatalf("text has %d bytes, err=%v, want 5,000 'A'", len(text), err)
		}
	})
}

// fix7PlainText reads all text, turning a budget panic into its error.
func fix7PlainText(r *Reader) (text string, err error) {
	defer func() {
		if p := recover(); p != nil {
			if e, ok := p.(error); ok {
				err = e
				return
			}
			panic(p)
		}
	}()
	rd, err := r.GetPlainText()
	if rd != nil {
		b, _ := io.ReadAll(rd)
		text = string(b)
	}
	return text, err
}
