package docextract

// #1968 FIX7 (gate 3: SEC3-1, SEC3-2, SF3-2, G3 section 5). Public-entry
// regression tests. Expected values are fixture arithmetic and the extract.go
// contract (text budget maxExtractRunes*4+1 = 400,001 bytes, 100,000-rune cap,
// marker "\n…[truncated]"), not recorded output.

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Section 5: U+200B is a format character the final filter removes but the
// parser budget counts as 3 UTF-8 bytes. 150,000 of them are 450,000 bytes,
// over the 400,001 budget, while the visible text is 5 runes: the 100,000-rune
// cap is NOT hit, only the budget stop can mark the result.
func TestPDFFix7_BudgetStopWithoutRuneCap(t *testing.T) {
	zw := strings.Repeat("A", 150_000)
	tu := pdfFix6MapA("200B")

	t.Run("visible prefix survives and the result is marked", func(t *testing.T) {
		content := []byte("BT /F2 12 Tf (HELLO) Tj /F1 12 Tf (" + zw + ") Tj ET")
		for name, run := range pdfFix6Extractors(t, pdfFix6Doc(tu, [][]byte{content}, "")) {
			text, ok, reason := run()
			if !ok {
				t.Fatalf("%s: visible text HELLO must be kept: reason=%q", name, reason)
			}
			if !strings.HasPrefix(text, "HELLO") {
				t.Fatalf("%s: text must start with HELLO, got %.40q", name, text)
			}
			if got := len([]rune(strings.TrimSuffix(text, "\n"+pdfFix6Marker))); got > 100 {
				t.Fatalf("%s: rune cap must not be involved (%d runes)", name, got)
			}
			if !strings.HasSuffix(text, "\n"+pdfFix6Marker) {
				t.Fatalf("%s: a budget stop with the rune cap unreached must still be marked truncated, got %q", name, text)
			}
		}
	})
	t.Run("no visible prefix reports the safety-limit reason", func(t *testing.T) {
		content := []byte("BT /F1 12 Tf (" + zw + ") Tj ET")
		for name, run := range pdfFix6Extractors(t, pdfFix6Doc(tu, [][]byte{content}, "")) {
			text, ok, reason := run()
			if ok || reason != "pdf exceeds the parser's safety limits before any text" {
				t.Fatalf("%s: want ok=false with the safety-limit reason, got ok=%t reason=%q text=%.40q", name, ok, reason, text)
			}
		}
	})
	t.Run("control: 100,000 zero-width characters stay under budget, no marker", func(t *testing.T) {
		// 300,000 bytes < 400,001: no stop, text is just HELLO.
		content := []byte("BT /F2 12 Tf (HELLO) Tj /F1 12 Tf (" + strings.Repeat("A", 100_000) + ") Tj ET")
		for name, run := range pdfFix6Extractors(t, pdfFix6Doc(tu, [][]byte{content}, "")) {
			text, ok, reason := run()
			if !ok || strings.TrimSpace(text) != "HELLO" {
				t.Fatalf("%s: ok=%t reason=%q text=%.40q, want HELLO unmarked", name, ok, reason, text)
			}
		}
	})
}

// SEC3-1 through the public entry point: a billion zero-width xref rows must
// end quickly with a refusal, never a hang.
func TestPDFFix7_ZeroWidthXrefStreamEndsQuickly(t *testing.T) {
	head := "%PDF-1.5\n"
	data := []byte(fmt.Sprintf("%s1 0 obj\n<< /Type /XRef /Size 1048576 /Index [%s] /W [0 0 0] /Length 0 /Root 2 0 R >>\nstream\n\nendstream\nendobj\nstartxref\n%d\n%%%%EOF\n",
		head, strings.Repeat("0 1048576 ", 1024), len(head)))
	type outcome struct {
		ok       bool
		reason   string
		panicked any
	}
	done := make(chan outcome, 1)
	go func() {
		var o outcome
		defer func() {
			// An escaping panic is a defect of the entry point: it is
			// reported, never turned into a refusal.
			o.panicked = recover()
			done <- o
		}()
		_, o.ok, o.reason = ExtractBytes(data, "application/pdf", "x.pdf")
	}()
	select {
	case o := <-done:
		if o.panicked != nil {
			t.Fatalf("SEC3-1: ExtractBytes panicked instead of returning a refusal: %v", o.panicked)
		}
		if o.ok || o.reason == "" {
			t.Fatalf("SEC3-1: a document with no pages must be refused with a reason, got ok=%t reason=%q", o.ok, o.reason)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("SEC3-1: ExtractBytes did not return within 3s on a billion-row xref stream")
	}
	t.Run("control: a real document still extracts", func(t *testing.T) {
		d := pdfFix6Doc(pdfFix6MapA("0042"), [][]byte{[]byte("BT /F1 12 Tf (AA) Tj ET")}, "")
		text, ok, reason := ExtractBytes(d, "application/pdf", "c.pdf")
		if !ok || strings.TrimSpace(text) != "BB" {
			t.Fatalf("ok=%t reason=%q text=%q", ok, reason, text)
		}
	})
}

// SEC3-2 through the public entry point: 100,000 filter stages on an empty
// stream must be refused or stay small, and return promptly.
func TestPDFFix7_LongFilterChainEntry(t *testing.T) {
	chain := "/Filter [" + strings.Repeat("/ASCII85Decode ", 100_000) + "]"
	data := pdfFix6Doc(pdfFix6Identity, [][]byte{nil}, chain)
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	text, ok, reason := ExtractBytes(data, "application/pdf", "x.pdf")
	runtime.ReadMemStats(&after)
	if ok && strings.TrimSpace(text) != "" {
		t.Fatalf("an empty stream cannot produce text, got %q", text)
	}
	// 100,000 decoders retain about 2 KiB each (about 200 MB): refusing the
	// chain (reason %q) or bounding it must keep allocation under 64 MiB.
	if alloc := after.TotalAlloc - before.TotalAlloc; alloc > 64<<20 {
		t.Fatalf("SEC3-2: a 100,000-stage chain allocated %d bytes (ok=%t reason=%q)", alloc, ok, reason)
	}
}

// SF3-2 through the public entry point: a rejected CMap must never return the
// raw codes as a successful extraction.
func TestPDFFix7_RejectedCmapNeverReturnsRawText(t *testing.T) {
	tu := pdfFix6CodespaceAll + "4611686018427387904 beginbfchar\nendbfchar\n"
	content := []byte("BT /F1 12 Tf (AAAA) Tj ET")
	for name, run := range pdfFix6Extractors(t, pdfFix6Doc(tu, [][]byte{content}, "")) {
		text, ok, reason := run()
		if ok {
			t.Fatalf("SF3-2: %s returned success with %q for a document whose CMap was rejected", name, text)
		}
		if reason == "" {
			t.Fatalf("SF3-2: %s failed without a reason", name)
		}
	}
}
