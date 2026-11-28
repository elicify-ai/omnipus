package docextract

// #1968 FIX10 (fix round 5 through the public entry points ExtractBytes and
// InspectPDF, plus the XLSX parts of the round). Each probe that can run long
// is a case of one parent test that re-executes the test binary as a
// disposable child process with a hard deadline: a timeout kills the child, so
// no parser keeps running inside the test binary. The child runs the case body
// and fails by itself; the parent checks that the child reached the case and
// exited cleanly. Fixture sizes and expected values come from the findings'
// arithmetic, not from recorded output.

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const (
	pdfFix10ChildEnv = "PDF1968_FIX10_CASE"
	xlsxMIME         = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
)

func pdfFix10Page(font, content string) []byte {
	return pdfFix6Assemble([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Count 1 /Kids [4 0 R] >>",
		font,
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 3 0 R >> >> /Contents 5 0 R >>",
		pdfFix6Stream("", []byte(content)),
	})
}

const pdfFix10PlainFont = "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>"

// pdfFix10Stopped asserts that both public entry points end data with a typed
// parser-limit failure and no text, and that the end is finite.
//
// Scope (RR2/K1, #1968): this is the public entry-point half of the proof.
// The default parser budgets are a 2^29 work allowance and a sampled 10 s
// clock; which of the two ends a pathological file first depends on the CPU,
// and both end it with the same typed class (ErrWorkLimit, "work limit" in the
// ExtractBytes reason, ErrPDFMalformed from InspectPDF). So this check does
// not demand one safety bound beat the other: it demands the typed failure,
// empty text, and containment inside watchdog, a generous hang detector that
// is far above both bounds (the shared boundary itself answers "timed out"
// after 15 s, a different reason this check rejects). The work-meter proof,
// that reuse is charged, is the internal parser tests
// TestFix10_ReusedTJArrayStopsWithTheWorkLimit and
// TestFix10_ReusedLongDefKeyStopsWithTheWorkLimit, which use a small explicit
// work budget and an hour-long clock so the meter alone decides.
func pdfFix10Stopped(t *testing.T, label string, data []byte, watchdog time.Duration) {
	t.Helper()
	start := time.Now()
	text, ok, reason := ExtractBytes(data, "application/pdf", "x.pdf")
	if elapsed := time.Since(start); elapsed > watchdog {
		t.Fatalf("%s ExtractBytes: not contained, it took %v (watchdog %v)", label, elapsed, watchdog)
	}
	if ok || !strings.Contains(reason, "work limit") || text != "" {
		t.Fatalf("%s ExtractBytes: want a work-limit failure with no text, got ok=%v text=%.40q reason=%.120q", label, ok, text, reason)
	}
	start = time.Now()
	info, err := InspectPDF(data)
	if elapsed := time.Since(start); elapsed > watchdog {
		t.Fatalf("%s InspectPDF: not contained, it took %v (watchdog %v)", label, elapsed, watchdog)
	}
	if !errors.Is(err, ErrPDFMalformed) || info.Text != "" {
		t.Fatalf("%s InspectPDF: want ErrPDFMalformed with no text, got err=%v text=%.40q", label, err, info.Text)
	}
}

// pdfFix10Extracts asserts that data extracts to want through both entry points.
func pdfFix10Extracts(t *testing.T, label string, data []byte, want string) {
	t.Helper()
	text, ok, reason := ExtractBytes(data, "application/pdf", "x.pdf")
	if !ok || strings.TrimSpace(text) != want {
		t.Fatalf("%s ExtractBytes: want %.40q, got ok=%v text=%.40q reason=%.120q", label, want, ok, text, reason)
	}
	info, err := InspectPDF(data)
	if err != nil || strings.TrimSpace(info.Text) != want {
		t.Fatalf("%s InspectPDF: want %.40q, got err=%v text=%.40q", label, want, err, info.Text)
	}
}

func pdfFix10Xlsx(t *testing.T, sheetCellXML string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(name, body string) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	add("[Content_Types].xml", `<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/></Types>`)
	add("xl/sharedStrings.xml", `<?xml version="1.0"?><sst><si><t>first</t></si><si><t>second</t></si></sst>`)
	add("xl/worksheets/sheet1.xml", `<?xml version="1.0"?><worksheet><sheetData><row>`+sheetCellXML+`</row></sheetData></worksheet>`)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

type pdfFix10Case struct {
	name     string
	deadline time.Duration // hard child deadline
	run      func(t *testing.T)
}

var pdfFix10Cases = []pdfFix10Case{
	{"tj-control", 20 * time.Second, func(t *testing.T) {
		data := pdfFix10Page(pdfFix10PlainFont, "1 dict begin /h [(Hel) -20 (lo)] def BT /F1 12 Tf h TJ h TJ [(!)] TJ ET end\n")
		pdfFix10Extracts(t, "control", data, "HelloHello!")
	}},
	{"tj-reused-array", 40 * time.Second, func(t *testing.T) {
		// 200,000 numbers shown 50,000 times is 1e10 element visits for about
		// 650 KB of file (400,000 bytes of array plus 250,000 of shows); the 2^29 work budget is spent after about 1,300 shows.
		var c strings.Builder
		c.WriteString("1 dict begin /a [")
		c.WriteString(strings.Repeat("0 ", 200_000))
		c.WriteString("] def\n")
		c.WriteString(strings.Repeat("a TJ\n", 50_000))
		c.WriteString("end BT /F1 12 Tf (OK) Tj ET\n")
		pdfFix10Stopped(t, "SEC5-1", pdfFix10Page(pdfFix10PlainFont, c.String()), 20*time.Second)
	}},
	{"def-control", 20 * time.Second, func(t *testing.T) {
		data := pdfFix10Page(pdfFix10PlainFont, "1 dict begin /K /Short def K 0 def K 1 def end BT /F1 12 Tf (OK) Tj ET\n")
		pdfFix10Extracts(t, "control", data, "OK")
	}},
	{"def-reused-long-key", 30 * time.Second, func(t *testing.T) {
		// An 8 MiB name bound to K, then "K 0 def" 400,000 times: 3 TiB of
		// hashing without a charge per use.
		var c strings.Builder
		c.WriteString("1 dict begin /K /")
		c.WriteString(strings.Repeat("N", 8<<20))
		c.WriteString(" def\n")
		c.WriteString(strings.Repeat("K 0 def\n", 400_000))
		c.WriteString("end BT /F1 12 Tf (OK) Tj ET\n")
		pdfFix10Stopped(t, "SEC5-2", pdfFix10Page(pdfFix10PlainFont, c.String()), 20*time.Second)
	}},
	{"differences-control", 20 * time.Second, func(t *testing.T) {
		// 65 is unknownglyph (keeps 'A'), 66 is /B then /C is the second name
		// for 66 (ignored), /D is 67: (AB) -120 (CD) reads A B D D.
		font := "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding << /Differences [65 /unknownglyph /B 66 /C /D] >> >>"
		pdfFix10Extracts(t, "control", pdfFix10Page(font, "BT /F1 12 Tf [(AB) -120 (CD)] TJ ( A) Tj ET\n"), "ABDD A")
	}},
	{"differences-long-name", 30 * time.Second, func(t *testing.T) {
		font := "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding << /Differences [65 /" + strings.Repeat("g", 8<<20) + "] >> >>"
		data := pdfFix10Page(font, "BT /F1 12 Tf ("+strings.Repeat("A", 100_000)+") Tj ET\n")
		start := time.Now()
		pdfFix10Extracts(t, "G5-R1", data, strings.Repeat("A", 100_000))
		if elapsed := time.Since(start); elapsed > 6*time.Second {
			t.Fatalf("G5-R1: both reads took %v; the glyph name must not be hashed for every byte", elapsed)
		}
	}},
	{"nested-error-message", 30 * time.Second, func(t *testing.T) {
		// A 16 MiB string inside 250 nested arrays sits where the xref table
		// should be; the error message renders it.
		inner := "(" + strings.Repeat("A", (16<<20)-16) + ")"
		obj := strings.Repeat("[", 250) + inner + strings.Repeat("]", 250)
		var buf bytes.Buffer
		buf.WriteString("%PDF-1.4\n")
		off := buf.Len()
		fmt.Fprintf(&buf, "1 0 obj\n%s\nendobj\nstartxref\n%d\n%%%%EOF\n", obj, off)
		data := buf.Bytes()
		_, ok, reason := ExtractBytes(data, "application/pdf", "x.pdf")
		if ok || !strings.Contains(reason, "cross-reference table not found") {
			t.Fatalf("fixture must fail at the xref with its message, got ok=%v reason=%.200q", ok, reason)
		}
		if len(reason) > 1300 {
			t.Fatalf("the reason carries %d bytes of the object; messages stay at or under about 1 KiB", len(reason))
		}
		_, err := InspectPDF(data)
		if !errors.Is(err, ErrPDFMalformed) || err == nil || len(err.Error()) > 1300 {
			n := 0
			if err != nil {
				n = len(err.Error())
			}
			t.Fatalf("InspectPDF must fail typed with a message of at most about 1 KiB, got err=%v (%d bytes)", errors.Is(err, ErrPDFMalformed), n)
		}
	}},
	{"text-cap-marked-on-reused-string", 30 * time.Second, func(t *testing.T) {
		// Replacement for the removed text-budget checks: a string bound with
		// def and shown 3 times is 180,000 characters, over the 100,000-rune
		// cap; the result keeps the prefix and says it was cut.
		content := "1 dict begin /s (" + strings.Repeat("A", 60_000) + ") def BT /F1 12 Tf s Tj s Tj s Tj ET end\n"
		data := pdfFix10Page(pdfFix10PlainFont, content)
		text, ok, reason := ExtractBytes(data, "application/pdf", "x.pdf")
		if !ok {
			t.Fatalf("readable text must not be dropped: %q", reason)
		}
		if !strings.HasSuffix(text, "\n"+pdfFix6Marker) {
			t.Fatalf("180,000 characters over a 100,000-rune cap must end with the truncation marker, tail %q", text[max(0, len(text)-30):])
		}
		if n, onlyA := pdfFix6OnlyA(text); !onlyA || n != 100_000 {
			t.Fatalf("kept text must be 100,000 'A', got %d (only A: %v)", n, onlyA)
		}
	}},
	{"xlsx-join-linear", 30 * time.Second, func(t *testing.T) {
		// One cell whose text arrives as 400,000 character-data chunks (a
		// comment splits each chunk); repeated string concatenation copies
		// the whole prefix every time.
		cell := `<c><v>` + strings.Repeat("abc<!---->", 400_000) + `</v></c>`
		data := pdfFix10Xlsx(t, cell)
		start := time.Now()
		text, ok, reason := ExtractBytes(data, xlsxMIME, "x.xlsx")
		if !ok || !strings.HasPrefix(text, "abcabcabc") {
			t.Fatalf("a large cell must extract, got ok=%v text=%.40q reason=%q", ok, text, reason)
		}
		if elapsed := time.Since(start); elapsed > 5*time.Second {
			t.Fatalf("joining 400,000 chunks took %v; the join must be linear", elapsed)
		}
	}},
}

func TestPDFFix10_Probes(t *testing.T) {
	if name := os.Getenv(pdfFix10ChildEnv); name != "" {
		for _, c := range pdfFix10Cases {
			if c.name == name {
				t.Log("PDF_FIX10_STARTED", name)
				c.run(t)
				return
			}
		}
		t.Fatalf("unknown case %q", name)
	}
	for _, c := range pdfFix10Cases {
		t.Run(c.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), c.deadline)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPDFFix10_Probes$", "-test.v")
			cmd.Env = append(os.Environ(), pdfFix10ChildEnv+"="+c.name, "GOTRACEBACK=none")
			out, err := cmd.CombinedOutput()
			if ctx.Err() != nil {
				t.Fatalf("%s did not finish within %v (child killed): the work is not bounded", c.name, c.deadline)
			}
			if !bytes.Contains(out, []byte("PDF_FIX10_STARTED")) {
				t.Fatalf("the child never reached the case: %v\n%s", err, tail(out))
			}
			if err != nil {
				t.Fatalf("%s failed: %v\n%s", c.name, err, tail(out))
			}
		})
	}
}

// SEC5-X: digits that wrap the shared-string index negative (or to a valid
// small index) must not panic, and an index outside the table shows the raw
// cell value like any other out-of-range index. The sheet has two cells: the
// probe cell and an in-range control "1". The table holds "first", "second".
func TestExtractBytes_XlsxSharedStringIndexWrapDoesNotPanic(t *testing.T) {
	cases := []struct{ name, v, want string }{
		{"control: index 0", "0", "first"},
		{"control: index 1", "1", "second"},
		{"control: out of range 2 shows the raw value", "2", "2"},
		{"control: leading zeros keep their value", "0000000000000000000000000001", "second"},
		{"2^63 wraps to MinInt64", "9223372036854775808", "9223372036854775808"},
		{"2^64-1 wraps to -1", "18446744073709551615", "18446744073709551615"},
		{"2^64 wraps to 0", "18446744073709551616", "18446744073709551616"},
		{"2^64+1 wraps to 1", "18446744073709551617", "18446744073709551617"},
		{"5,000 nines", strings.Repeat("9", 5000), strings.Repeat("9", 5000)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			data := pdfFix10Xlsx(t, `<c t="s"><v>`+c.v+`</v></c><c t="s"><v>1</v></c>`)
			defer func() {
				if p := recover(); p != nil {
					t.Fatalf("SEC5-X: panic on shared-string index %.30q: %v", c.v, p)
				}
			}()
			text, ok, reason := ExtractBytes(data, xlsxMIME, "x.xlsx")
			if !ok {
				t.Fatalf("must extract, got reason %q", reason)
			}
			if want := c.want + "\tsecond"; text != want {
				t.Fatalf("text = %.60q, want %.60q", text, want)
			}
		})
	}
}
