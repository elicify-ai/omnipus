package docextract

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// pdfFlateEncode compresses b with zlib for use as a PDF FlateDecode stream.
func pdfFlateEncode(b []byte) []byte {
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	_, _ = zw.Write(b)
	_ = zw.Close()
	return buf.Bytes()
}

// makeSyntheticPDF builds a one-page PDF that displays text via a
// Type0/Identity-H font with a ToUnicode CMap and FlateDecode streams, so the
// raw bytes never contain the readable text and an extractor can only return
// the text by actually decoding the document.
//
// nulFreeHeader pads the header comment line so the first 512 bytes of the
// file contain no NUL byte — the real-world shape issue #1959 is about (ASCII
// header and objects before any compressed stream). nulFreeHeader=false writes
// exactly one literal NUL inside the header comment line instead, so the
// sniffed 512-byte prefix does contain a NUL.
//
// Spec source: issue #1959 required behaviour items 1, 2 and 7.
func makeSyntheticPDF(t *testing.T, nulFreeHeader bool, text string) []byte {
	t.Helper()

	runes := []rune(text)
	// glyph ids 1..n for distinct runes
	gid := map[rune]int{}
	var order []rune
	for _, r := range runes {
		if _, ok := gid[r]; !ok {
			gid[r] = len(gid) + 1
			order = append(order, r)
		}
	}
	var hex strings.Builder
	for _, r := range runes {
		fmt.Fprintf(&hex, "%04X", gid[r])
	}
	content := []byte(fmt.Sprintf("BT /F1 12 Tf 72 700 Td <%s> Tj ET", hex.String()))

	var cm strings.Builder
	cm.WriteString("/CIDInit /ProcSet findresource begin 12 dict begin begincmap\n")
	cm.WriteString("/CMapName /Adobe-Identity-UCS def /CMapType 2 def\n")
	cm.WriteString("1 begincodespacerange <0000> <FFFF> endcodespacerange\n")
	fmt.Fprintf(&cm, "%d beginbfchar\n", len(order))
	for _, r := range order {
		fmt.Fprintf(&cm, "<%04X> <%04X>\n", gid[r], r)
	}
	cm.WriteString("endbfchar\nendcmap CMapName currentdict /CMap defineresource pop end end\n")

	var header string
	if nulFreeHeader {
		// 700 ASCII comment bytes: the first 512 bytes of the file are all
		// comment and contain no NUL.
		header = "%" + strings.Repeat("x", 700) + "\n"
	} else {
		// One literal NUL at byte offset 9+1+256=266, inside the comment line
		// (comment bytes up to end-of-line are ignored by a PDF reader).
		header = "%" + strings.Repeat("x", 256) + "\x00" + strings.Repeat("x", 256) + "\n"
	}

	var out bytes.Buffer
	var offs []int
	out.WriteString("%PDF-1.7\n" + header)
	obj := func(body string) {
		offs = append(offs, out.Len())
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", len(offs), body)
	}
	stream := func(dict string, data []byte) {
		offs = append(offs, out.Len())
		fmt.Fprintf(&out, "%d 0 obj\n<<%s/Filter/FlateDecode/Length %d>>\nstream\n", len(offs), dict, len(data))
		out.Write(data)
		out.WriteString("\nendstream\nendobj\n")
	}
	obj("<</Type/Catalog/Pages 2 0 R>>")
	obj("<</Type/Pages/Count 1/Kids[3 0 R]>>")
	obj("<</Type/Page/Parent 2 0 R/MediaBox[0 0 612 792]/Contents 4 0 R/Resources<</Font<</F1 5 0 R>>>>>>")
	stream("", pdfFlateEncode(content))
	obj("<</Type/Font/Subtype/Type0/BaseFont/AAAAAA+Test/Encoding/Identity-H/DescendantFonts[6 0 R]/ToUnicode 7 0 R>>")
	obj("<</Type/Font/Subtype/CIDFontType2/BaseFont/AAAAAA+Test/CIDSystemInfo<</Registry(Adobe)/Ordering(Identity)/Supplement 0>>/DW 600>>")
	stream("", pdfFlateEncode([]byte(cm.String())))
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(offs)+1)
	for _, o := range offs {
		fmt.Fprintf(&out, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&out, "trailer\n<</Size %d/Root 1 0 R>>\nstartxref\n%d\n%%%%EOF\n", len(offs)+1, xref)
	return out.Bytes()
}

// requireHeaderNULs is an instrument guard: it verifies the fixture actually
// has (or has not) a NUL byte within the sniffed 512-byte prefix, so a test
// can never silently exercise the wrong detection branch.
func requireHeaderNULs(t *testing.T, pdf []byte, wantNUL bool) {
	t.Helper()
	head := pdf[:min(len(pdf), 512)]
	if got := bytes.Contains(head, []byte{0}); got != wantNUL {
		t.Fatalf("fixture broken: NUL in first 512 bytes = %v, want %v", got, wantNUL)
	}
}

// TestExtractBytes_PDFTextWithoutNULInHeader — issue #1959 item 7: ExtractBytes
// must decode the synthetic PDF and return exactly the displayed text when no
// NUL byte appears in the first 512 bytes. The expected string comes from the
// issue text, not from running the extractor.
func TestExtractBytes_PDFTextWithoutNULInHeader(t *testing.T) {
	pdf := makeSyntheticPDF(t, true, "Venue: Grand Hotel Amsterdam, 12 March")
	requireHeaderNULs(t, pdf, false)

	txt, ok, reason := ExtractBytes(pdf, "", "invitation.pdf")
	if !ok {
		t.Fatalf("expected extraction to succeed, got ok=false reason=%q", reason)
	}
	if txt != "Venue: Grand Hotel Amsterdam, 12 March" {
		t.Fatalf("extracted text mismatch:\n got %q\nwant %q", txt, "Venue: Grand Hotel Amsterdam, 12 March")
	}
}

// TestExtractBytes_PDFTextWithNULInHeader — issue #1959 item 7: the same
// extraction result when the first 512 bytes DO contain a NUL. The NUL-based
// binary sniff belongs to the read_file routing layer, never to the extractor:
// a NUL check added inside ExtractBytes (or magic routing dropped for
// NUL-bearing input) makes this die.
func TestExtractBytes_PDFTextWithNULInHeader(t *testing.T) {
	pdf := makeSyntheticPDF(t, false, "Venue: Grand Hotel Amsterdam, 12 March")
	requireHeaderNULs(t, pdf, true)

	txt, ok, reason := ExtractBytes(pdf, "", "invitation.pdf")
	if !ok {
		t.Fatalf("expected extraction to succeed, got ok=false reason=%q", reason)
	}
	if txt != "Venue: Grand Hotel Amsterdam, 12 March" {
		t.Fatalf("extracted text mismatch:\n got %q\nwant %q", txt, "Venue: Grand Hotel Amsterdam, 12 March")
	}
}

// TestExtractBytes_PDFUnicodeViaToUnicode — issue #1959 item 7: non-ASCII text
// must survive extraction through the ToUnicode CMap (no U+FFFD replacement
// runes, no mojibake).
func TestExtractBytes_PDFUnicodeViaToUnicode(t *testing.T) {
	pdf := makeSyntheticPDF(t, true, "Café Zürich 12 März")
	requireHeaderNULs(t, pdf, false)

	txt, ok, reason := ExtractBytes(pdf, "", "einladung.pdf")
	if !ok {
		t.Fatalf("expected extraction to succeed, got ok=false reason=%q", reason)
	}
	if txt != "Café Zürich 12 März" {
		t.Fatalf("unicode text mismatch:\n got %q\nwant %q", txt, "Café Zürich 12 März")
	}
	if strings.ContainsRune(txt, '�') {
		t.Fatalf("U+FFFD replacement rune in extracted text: %q", txt)
	}
}

// TestExtractBytes_PDFCorruptGarbageFailsClosed — extractor-level ground truth
// for issue #1959 item 5: bytes carrying the %PDF- signature but no valid
// objects must yield ok=false with a reason, never a plausible partial text.
func TestExtractBytes_PDFCorruptGarbageFailsClosed(t *testing.T) {
	garbage := []byte("%PDF-1.7\n" + strings.Repeat("not a pdf object at all ", 32))
	requireHeaderNULs(t, garbage, false)

	txt, ok, reason := ExtractBytes(garbage, "", "corrupt.pdf")
	if ok {
		t.Fatalf("expected corrupt PDF extraction to fail, got ok=true text=%q", txt)
	}
	if txt != "" {
		t.Fatalf("expected no text from a corrupt PDF, got %q", txt)
	}
	if reason == "" {
		t.Fatal("expected a non-empty failure reason for the corrupt PDF")
	}
}

// trailingPDFSuffixes are the boundary suffixes the trailing-whitespace
// regression pins. Exactly 100 trailing LF bytes made the underlying PDF
// parser panic with "index out of range [-1]" in its end-of-file trim loop
// (CHECK finding on issue #1959); 99/101 and the CR variant pin the boundary
// so a one-off guard cannot sneak past.
var trailingPDFSuffixes = []struct {
	name   string
	suffix []byte
}{
	{"exactly 100 trailing LF", bytes.Repeat([]byte{'\n'}, 100)},
	{"99 trailing LF", bytes.Repeat([]byte{'\n'}, 99)},
	{"101 trailing LF", bytes.Repeat([]byte{'\n'}, 101)},
	{"100 trailing CR", bytes.Repeat([]byte{'\r'}, 100)},
}

// makeSyntheticPDFWithSuffix builds the NUL-free synthetic venue PDF with a
// trailing whitespace run appended, asserting both instrument properties: the
// suffix really is the last bytes of the file, and the first 512 bytes stay
// NUL-free (so the file exercises the signature-routing branch, not the NUL
// branch).
func makeSyntheticPDFWithSuffix(t *testing.T, suffix []byte) []byte {
	t.Helper()
	pdf := append(makeSyntheticPDF(t, true, "Venue: Grand Hotel Amsterdam, 12 March"), suffix...)
	if !bytes.HasSuffix(pdf, suffix) {
		t.Fatal("fixture broken: trailing suffix is not the file's tail")
	}
	requireHeaderNULs(t, pdf, false)
	return pdf
}

// TestExtractBytes_PDFTrailingNewlineRunMustNotPanic — trailing-whitespace
// regression on issue #1959: ExtractBytes must return a definitive outcome for
// a PDF carrying a trailing whitespace run — ok=true with exactly the
// displayed text, or ok=false with a non-empty reason — and must never panic
// ("index out of range [-1]" in the parser's EOF trim loop at exactly 100
// trailing LF bytes). The deferred recover does NOT hide the panic: it
// converts it into a test failure carrying the panic value.
func TestExtractBytes_PDFTrailingNewlineRunMustNotPanic(t *testing.T) {
	for _, tc := range trailingPDFSuffixes {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("ExtractBytes panicked (%s): %v", tc.name, r)
				}
			}()

			pdf := makeSyntheticPDFWithSuffix(t, tc.suffix)
			txt, ok, reason := ExtractBytes(pdf, "", "trailing.pdf")
			if ok {
				if txt != "Venue: Grand Hotel Amsterdam, 12 March" {
					t.Fatalf("extracted text mismatch:\n got %q\nwant %q", txt, "Venue: Grand Hotel Amsterdam, 12 March")
				}
				return
			}
			if reason == "" {
				t.Fatalf("expected a non-empty failure reason when extraction fails, got ok=false text=%q", txt)
			}
		})
	}
}

// --- cyclic page tree (parser hang, CHECK round-2 finding on #1959) ----------

// makeCyclicPageTreePDF builds a small valid-structure PDF whose page tree is
// self-referencing: the Pages node's /Kids lists the node itself — the shape
// that makes the underlying parser's page walk loop forever inside
// GetPlainText (extract.go::extractPDF). The graph is mirrored from the audit
// reproducer; the bytes are built here, never read from a file at test time.
// Header stays NUL-free (padded comment) so the file exercises the
// signature-routing branch, not the NUL branch.
func makeCyclicPageTreePDF(t *testing.T) []byte {
	t.Helper()
	var out bytes.Buffer
	var offs []int
	out.WriteString("%PDF-1.7\n%" + strings.Repeat("x", 700) + "\n")
	obj := func(body string) {
		offs = append(offs, out.Len())
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", len(offs), body)
	}
	obj("<</Type/Catalog/Pages 2 0 R>>")
	// The hazard: the Pages node lists ITSELF as its only kid.
	obj("<</Type/Pages/Count 1/Kids [2 0 R]>>")
	obj("<</Type/Page/Parent 2 0 R/MediaBox [0 0 612 792]/Resources<</Font<</F1 4 0 R>>>>/Contents 5 0 R>>")
	obj("<</Type/Font/Subtype/Type1/BaseFont/Helvetica/Encoding/WinAnsiEncoding>>")
	content := "BT /F1 12 Tf 72 700 Td (Cyclic page tree text) Tj ET"
	obj(fmt.Sprintf("<</Length %d>>\nstream\n%s\nendstream", len(content), content))
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(offs)+1)
	for _, o := range offs {
		fmt.Fprintf(&out, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&out, "trailer\n<</Size %d/Root 1 0 R>>\nstartxref\n%d\n%%%%EOF\n", len(offs)+1, xref)

	pdf := out.Bytes()
	// Instrument guard: the self-reference IS the hazard. A de-cycled fixture
	// would fail fast with a plain parse error and the timeout tests below
	// would pass vacuously.
	if !bytes.Contains(pdf, []byte("/Kids [2 0 R]")) {
		t.Fatal("fixture broken: page tree is not self-referencing")
	}
	requireHeaderNULs(t, pdf, false)
	return pdf
}

// pdfTimeoutReason is the exact reason of a parse that outlives its budget.
const pdfTimeoutReason = "pdf extraction timed out (the file may be malformed)"

// holdParseInFlight runs one extraction of a real, valid PDF behind a
// gatedReaderAt that stays blocked, so the parse never finishes inside the
// budget: the deterministic way to reach the timeout, no-leak and stuck-cap
// paths now that the bounded #1968 parser rejects the cyclic page tree before
// any parse time is spent. The gate is released (and the late parse allowed to
// finish and hand back its slot) when the test ends.
func holdParseInFlight(t *testing.T, venue []byte) (string, bool, string) {
	t.Helper()
	release := make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	return extractPDF(&gatedReaderAt{
		data: bytes.NewReader(venue), release: release, starts: &atomic.Int32{},
	}, int64(len(venue)))
}

// settleHeldParses runs as the LAST cleanup of a test that held parses: the
// holds are released by then, so it waits until the late parses have handed
// their slots back (waitForStuckZero, waitForAllSlotsReleased) before the next
// test starts from full capacity.
func settleHeldParses(t *testing.T) {
	t.Helper()
	waitForStuckZero(t, 5*time.Second)
	waitForAllSlotsReleased(t, 2*time.Second)
	resetPDFStuckForTest()
}

// TestExtractBytes_PDFCyclicPageTreeIsRejectedFastBySafetyLimits - #1968: the
// bounded parser refuses the self-referencing page tree before parsing any
// further, so ExtractBytes returns at once (no budget wait, no goroutine left
// stuck, no slot held) with ok=false, no text and the typed reason naming the
// limit. This replaces the #1959 expectation that the same file ends in the
// timeout reason.
func TestExtractBytes_PDFCyclicPageTreeIsRejectedFastBySafetyLimits(t *testing.T) {
	resetPDFStuckForTest()
	t.Cleanup(resetPDFStuckForTest)
	pdf := makeCyclicPageTreePDF(t)

	start := time.Now()
	txt, ok, reason := ExtractBytes(pdf, "", "cyclic.pdf")
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("cyclic page tree must be rejected fast, took %v", elapsed)
	}
	if ok || txt != "" {
		t.Fatalf("expected rejection with no text, got ok=%v text=%q", ok, txt)
	}
	const want = "pdf exceeds the parser's safety limits: pdf: structure exceeds parser safety bounds: page tree deeper than 256"
	if reason != want {
		t.Fatalf("expected the safety-limit reason\n%q\ngot\n%q", want, reason)
	}
	if got := pdfStuck.Load(); got != 0 {
		t.Fatalf("a fast rejection must leave no stuck parse, pdfStuck = %d", got)
	}
	// The worker sends its result before its deferred slot release runs, so a
	// bare len(pdfSlots) check can race with correct cleanup (PORT-SEC-3):
	// wait for the release, and still fail if a token is never returned.
	waitForAllSlotsReleased(t, 2*time.Second)
	if got := len(pdfSlots); got != 0 {
		t.Fatalf("a fast rejection must hold no slot, %d token(s) present", got)
	}
}

// TestExtractBytes_PDFDefaultBudgetTimesOut - behavioural proof that a parse
// which never finishes ends on the DEFAULT budget with the exact timeout
// reason and no text. The never-finishing parse is a valid PDF behind a gated
// reader (the cyclic file no longer gets that far, see the fast-rejection
// test). Takes up to the default budget (15s), so it skips under -short.
func TestExtractBytes_PDFDefaultBudgetTimesOut(t *testing.T) {
	if testing.Short() {
		t.Skip("default-budget wait (up to 15s) - skipped under -short")
	}
	resetPDFStuckForTest()
	t.Cleanup(func() { settleHeldParses(t) })

	venue := makeSyntheticPDF(t, true, "Venue: Grand Hotel Amsterdam, 12 March")
	txt, ok, reason := holdParseInFlight(t, venue)
	if ok {
		t.Fatalf("expected the gated parse to fail on the default budget, got ok=true text=%q", txt)
	}
	if reason != pdfTimeoutReason {
		t.Fatalf("expected the exact timeout reason, got %q", reason)
	}
	if txt != "" {
		t.Fatalf("expected no text from a timed-out extraction, got %q", txt)
	}
}

// TestExtractBytes_PDFTimesOutWithinBudget - with pdfExtractTimeout shortened
// to 300ms, an extraction whose parse never finishes must return well within
// 5s with ok=false, no text and the exact timeout reason. The never-finishing
// parse is held by a gatedReaderAt.
func TestExtractBytes_PDFTimesOutWithinBudget(t *testing.T) {
	orig := pdfExtractTimeout
	pdfExtractTimeout = 300 * time.Millisecond
	t.Cleanup(func() { pdfExtractTimeout = orig })
	resetPDFStuckForTest()
	t.Cleanup(func() { settleHeldParses(t) })

	venue := makeSyntheticPDF(t, true, "Venue: Grand Hotel Amsterdam, 12 March")
	start := time.Now()
	txt, ok, reason := holdParseInFlight(t, venue)
	elapsed := time.Since(start)
	if elapsed > 5*time.Second {
		t.Fatalf("extraction returned in %v, expected the 300ms budget to end it well within 5s", elapsed)
	}
	if elapsed < 250*time.Millisecond {
		t.Fatalf("extraction returned in %v, before the 300ms budget: the gate did not hold the parse", elapsed)
	}
	if ok {
		t.Fatalf("expected the gated parse to fail, got ok=true text=%q", txt)
	}
	if reason != pdfTimeoutReason {
		t.Fatalf("expected the exact timeout reason, got %q", reason)
	}
	if txt != "" {
		t.Fatalf("expected no text from a timed-out extraction, got %q", txt)
	}
}

// TestExtractBytes_PDFTimeoutDoesNotLeakIntoNextExtraction - requirement (b):
// right after a timed-out parse, a valid NUL-free PDF must still extract its
// exact text: the timeout leaves no state that corrupts or refuses the next
// extraction. The timed-out parse is held by a gatedReaderAt.
func TestExtractBytes_PDFTimeoutDoesNotLeakIntoNextExtraction(t *testing.T) {
	orig := pdfExtractTimeout
	pdfExtractTimeout = 300 * time.Millisecond
	t.Cleanup(func() { pdfExtractTimeout = orig })
	resetPDFStuckForTest()
	t.Cleanup(func() { settleHeldParses(t) })

	venue := makeSyntheticPDF(t, true, "Venue: Grand Hotel Amsterdam, 12 March")
	if _, ok, reason := holdParseInFlight(t, venue); ok || reason != pdfTimeoutReason {
		t.Fatalf("expected the gated parse to time out first, got ok=%v reason=%q", ok, reason)
	}

	start := time.Now()
	txt, ok, reason := ExtractBytes(venue, "", "venue.pdf")
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("valid extraction after a timeout took %v - timed-out state leaked into the next parse", elapsed)
	}
	if !ok {
		t.Fatalf("expected the valid PDF to extract right after a timeout, got ok=false reason=%q", reason)
	}
	if txt != "Venue: Grand Hotel Amsterdam, 12 March" {
		t.Fatalf("extracted text mismatch after a timed-out parse:\n got %q\nwant %q", txt, "Venue: Grand Hotel Amsterdam, 12 March")
	}
}

// TestExtractBytes_PDFStuckParseCapRefusesFurtherExtractions - requirement
// (c): with a 100ms budget, two gated parses time out and stay stuck; the
// third extraction must be refused immediately (under 50ms, no new goroutine)
// with the exact stuck-cap reason ("pdf extraction unavailable: earlier
// extractions are still stuck"). The third call is a perfectly valid PDF, so
// only the cap can refuse it. resetPDFStuckForTest gives the three-call script
// a clean slate.
func TestExtractBytes_PDFStuckParseCapRefusesFurtherExtractions(t *testing.T) {
	orig := pdfExtractTimeout
	pdfExtractTimeout = 100 * time.Millisecond
	t.Cleanup(func() { pdfExtractTimeout = orig })
	resetPDFStuckForTest()
	t.Cleanup(func() { settleHeldParses(t) })

	venue := makeSyntheticPDF(t, true, "Venue: Grand Hotel Amsterdam, 12 March")
	for i := 1; i <= 2; i++ {
		start := time.Now()
		_, ok, reason := holdParseInFlight(t, venue)
		if elapsed := time.Since(start); elapsed > 5*time.Second {
			t.Fatalf("stuck parse %d returned in %v, expected the 100ms budget to end it", i, elapsed)
		}
		if ok || reason != pdfTimeoutReason {
			t.Fatalf("stuck parse %d: expected the exact timeout reason, got ok=%v reason=%q", i, ok, reason)
		}
	}

	start := time.Now()
	_, ok, reason := ExtractBytes(venue, "", "venue.pdf")
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("cap refusal took %v, expected an immediate refusal under 50ms", elapsed)
	}
	if ok || reason != "pdf extraction unavailable: earlier extractions are still stuck" {
		t.Fatalf("expected the exact stuck-cap refusal, got ok=%v reason=%q", ok, reason)
	}
}

// makeASCIIPDFBytes builds a classic minimal single-page PDF with an
// UNCOMPRESSED content stream (Helvetica Type1), so every byte is plain
// ASCII — the real-world shape the round-4 CHECK probe used to demonstrate
// the text-MIME bypass, and the shape in which the text extractor accepts the
// whole file as valid UTF-8 text. Unlike the FlateDecode fixture the raw
// bytes DO contain the displayed text, so assertions on these rows must use
// exact equality to discriminate a raw dump from a real extraction.
func makeASCIIPDFBytes(t *testing.T, text string) []byte {
	t.Helper()
	var out bytes.Buffer
	var offs []int
	out.WriteString("%PDF-1.7\n%" + strings.Repeat("x", 700) + "\n")
	obj := func(body string) {
		offs = append(offs, out.Len())
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", len(offs), body)
	}
	obj("<</Type/Catalog/Pages 2 0 R>>")
	obj("<</Type/Pages/Count 1/Kids[3 0 R]>>")
	obj("<</Type/Page/Parent 2 0 R/MediaBox[0 0 612 792]/Contents 4 0 R/Resources<</Font<</F1 5 0 R>>>>>>")
	content := fmt.Sprintf("BT /F1 12 Tf 72 700 Td (%s) Tj ET", text)
	obj(fmt.Sprintf("<</Length %d>>\nstream\n%s\nendstream", len(content), content))
	obj("<</Type/Font/Subtype/Type1/BaseFont/Helvetica/Encoding/WinAnsiEncoding>>")
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(offs)+1)
	for _, o := range offs {
		fmt.Fprintf(&out, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&out, "trailer\n<</Size %d/Root 1 0 R>>\nstartxref\n%d\n%%%%EOF\n", len(offs)+1, xref)
	return out.Bytes()
}

// --- round-4 CHECK findings: concurrent admission, late finish, text MIME ----

// gatedReaderAt is the process-edge hook for parser admission tests: the FIRST
// ReadAt counts the parse as started, every read then blocks until the release
// channel closes, and only afterwards is the real data served. A gated parse
// can therefore be held in flight deterministically and released on demand —
// the parser and extraction wrapper stay real.
type gatedReaderAt struct {
	data    *bytes.Reader
	release <-chan struct{}
	starts  *atomic.Int32 // +1 per reader instance, on first read
	once    sync.Once
}

func (r *gatedReaderAt) ReadAt(p []byte, off int64) (int, error) {
	r.once.Do(func() { r.starts.Add(1) })
	<-r.release
	return r.data.ReadAt(p, off)
}

// waitForStuckValue polls pdfStuck until it equals want, failing with the
// observed value if the deadline passes.
func waitForStuckValue(t *testing.T, want int32, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for pdfStuck.Load() != want && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if got := pdfStuck.Load(); got != want {
		t.Fatalf("pdfStuck = %d, want %d (within %v)", got, want, within)
	}
}

// waitForStuckZero is the release-side form: all late parses must eventually
// return and hand their capacity back.
func waitForStuckZero(t *testing.T, within time.Duration) {
	t.Helper()
	waitForStuckValue(t, 0, within)
}

// waitForAllSlotsReleased waits until no slot token remains in pdfSlots, i.e.
// every admitted worker has returned and released. The worker decrements
// pdfStuck BEFORE its deferred slot release runs, so counter zero alone is
// not a cleanup barrier (CHECK round-4 finding N3); a token still present
// after its worker has returned is a leaked slot (finding N2). Accounting:
// the channel is FILLED by acquiring and DRAINED by releasing, so tokens
// present means capacity is occupied — zero tokens means full capacity is
// free (and a reset drain also leaves it at zero).
func waitForAllSlotsReleased(t *testing.T, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for len(pdfSlots) != 0 && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if got := len(pdfSlots); got != 0 {
		t.Fatalf("pdfSlots still holds %d unreleased token(s) after %v — a late worker leaked or still holds its slot", got, within)
	}
}

// extractPDFResult is one concurrent call's outcome.
type extractPDFResult struct {
	text   string
	ok     bool
	reason string
}

// TestExtractPDF_ConcurrentAdmissionBoundsStartedParses — round-4 CHECK
// finding F1: admission is a capacity reservation, not a load. Four PDF parses
// released behind gated readers must not all start: at most pdfMaxStuck (2)
// parse goroutines may be in flight; the rest wait for a slot at most
// pdfExtractTimeout and are refused ("busy"/"still stuck" family), every call
// returns ok=false, and once the released parses finish, capacity is back and
// a valid PDF extracts normally. Fails today: all four calls read pdfStuck<2
// and start parsing (started=4) because admission does not reserve a slot.
func TestExtractPDF_ConcurrentAdmissionBoundsStartedParses(t *testing.T) {
	orig := pdfExtractTimeout
	pdfExtractTimeout = 150 * time.Millisecond
	t.Cleanup(func() { pdfExtractTimeout = orig })
	resetPDFStuckForTest()
	waitForAllSlotsReleased(t, 2*time.Second)

	const venueText = "Venue: Grand Hotel Amsterdam, 12 March"
	venue := makeSyntheticPDF(t, true, venueText)
	var starts atomic.Int32
	release := make(chan struct{})

	const calls = 4
	results := make(chan extractPDFResult, calls)
	for i := 0; i < calls; i++ {
		go func() {
			txt, ok, reason := extractPDF(&gatedReaderAt{
				data: bytes.NewReader(venue), release: release, starts: &starts,
			}, int64(len(venue)))
			results <- extractPDFResult{txt, ok, reason}
		}()
	}

	got := make([]extractPDFResult, 0, calls)
	for len(got) < calls {
		select {
		case r := <-results:
			got = append(got, r)
		case <-time.After(10 * time.Second):
			t.Fatalf("only %d of %d concurrent calls returned within 10s", len(got), calls)
		}
	}

	// The core invariant: distinct parses started. Read before releasing so a
	// refused call cannot retroactively appear as started.
	if started := starts.Load(); started > pdfMaxStuck {
		t.Fatalf("concurrent admission started %d parses; at most %d may be in flight (pdfMaxStuck=%d)", started, pdfMaxStuck, pdfMaxStuck)
	}
	// With a size-2 semaphore and no slot freed during the gated window,
	// exactly two calls are admitted (they time out) and two are refused.
	admitted, refused := 0, 0
	for i, r := range got {
		if r.ok {
			t.Fatalf("call %d succeeded on a gated parse that was never released: text=%q", i, r.text)
		}
		switch {
		case r.reason == "pdf extraction timed out (the file may be malformed)":
			admitted++
		case strings.Contains(r.reason, "busy") || strings.Contains(r.reason, "still stuck"):
			refused++
		default:
			t.Fatalf("call %d: unexpected reason %q, want the timeout or refusal family", i, r.reason)
		}
	}
	if admitted != 2 || refused != 2 {
		t.Fatalf("expected 2 admitted (timed out) + 2 refused, got %d admitted + %d refused", admitted, refused)
	}

	close(release) // the gated (valid) parses now finish late
	waitForStuckZero(t, 5*time.Second)
	waitForAllSlotsReleased(t, 2*time.Second)
	resetPDFStuckForTest()

	txt, ok, reason := ExtractBytes(venue, "", "venue.pdf")
	if !ok || txt != venueText {
		t.Fatalf("valid extraction after concurrent gated parses must work normally: ok=%v reason=%q text=%q", ok, reason, txt)
	}
}

// TestExtractPDF_ConcurrentValidExtractionsAllSucceed — the other side of the
// F1 fix: waiting for a slot must be transient, not a refusal. Eight
// legitimate concurrent extractions of a valid PDF all succeed with the exact
// text (they may queue for a slot; the parses are fast). Green today; kills a
// fix that refuses or times out legitimate concurrent load.
func TestExtractPDF_ConcurrentValidExtractionsAllSucceed(t *testing.T) {
	resetPDFStuckForTest()
	waitForAllSlotsReleased(t, 2*time.Second)

	const venueText = "Venue: Grand Hotel Amsterdam, 12 March"
	venue := makeSyntheticPDF(t, true, venueText)

	const calls = 8
	results := make(chan extractPDFResult, calls)
	for i := 0; i < calls; i++ {
		go func() {
			txt, ok, reason := ExtractBytes(venue, "", "venue.pdf")
			results <- extractPDFResult{txt, ok, reason}
		}()
	}
	for i := 0; i < calls; i++ {
		select {
		case r := <-results:
			if !r.ok || r.text != venueText {
				t.Fatalf("concurrent valid extraction %d failed: ok=%v reason=%q text=%q", i, r.ok, r.reason, r.text)
			}
		case <-time.After(30 * time.Second):
			t.Fatalf("only %d of %d concurrent valid extractions finished within 30s", i, calls)
		}
	}
	if got := pdfStuck.Load(); got != 0 {
		t.Fatalf("valid concurrent load left pdfStuck=%d, want 0", got)
	}
}

// TestExtractPDF_LateFinishReleasesStuckSlot — round-4 CHECK finding F2: a
// parse abandoned by the timeout must hand its capacity back when it finally
// returns. The gated parse is released AFTER the timeout fired, so the finish
// is late but deterministic: pdfStuck must read 1 while the parse is abandoned
// and 0 after it completes, and the freed capacity must serve the next
// extraction immediately. Kills the survived mutant that removes the
// late-finish decrement (extract.go, pdfStuck.Add(-1) in the abandoned-CAS
// branch): without it pdfStuck stays 1 forever and waitForStuckZero fails.
func TestExtractPDF_LateFinishReleasesStuckSlot(t *testing.T) {
	orig := pdfExtractTimeout
	pdfExtractTimeout = 150 * time.Millisecond
	t.Cleanup(func() { pdfExtractTimeout = orig })
	resetPDFStuckForTest()
	waitForAllSlotsReleased(t, 2*time.Second)

	const venueText = "Venue: Grand Hotel Amsterdam, 12 March"
	venue := makeSyntheticPDF(t, true, venueText)
	var starts atomic.Int32
	release := make(chan struct{})
	g := &gatedReaderAt{data: bytes.NewReader(venue), release: release, starts: &starts}

	results := make(chan extractPDFResult, 1)
	go func() {
		txt, ok, reason := extractPDF(g, int64(len(venue)))
		results <- extractPDFResult{txt, ok, reason}
	}()

	select {
	case r := <-results:
		if r.ok || r.reason != "pdf extraction timed out (the file may be malformed)" {
			t.Fatalf("expected the exact timeout reason while the parse is gated, got ok=%v reason=%q", r.ok, r.reason)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the timed-out wait did not return within 10s")
	}

	// The abandoned parse is still gated: exactly one stuck slot is held.
	waitForStuckValue(t, 1, 2*time.Second)

	close(release) // the parse now finishes late
	waitForStuckZero(t, 5*time.Second)
	waitForAllSlotsReleased(t, 2*time.Second)

	// The slot is freed: the next extraction runs immediately and succeeds.
	startTime := time.Now()
	txt, ok, reason := ExtractBytes(venue, "", "venue.pdf")
	if elapsed := time.Since(startTime); elapsed > 5*time.Second {
		t.Fatalf("next extraction took %v after the late finish released its slot", elapsed)
	}
	if !ok || txt != venueText {
		t.Fatalf("next extraction after a late finish failed: ok=%v reason=%q text=%q", ok, reason, txt)
	}
}

// TestExtractBytes_PDFRoutingBeatsTextMIME — round-4 CHECK finding F3: a
// content that starts with "%PDF-" or a name ending .pdf is routed to PDF
// extraction regardless of a text-ish or opaque MIME; only an explicit OOXML
// MIME or package still wins over the PDF signature. Today text/plain and
// text/markdown short-circuit into extractTextBytes and return the raw
// "%PDF-1.7 ..." bytes as ok=true — those subtests fail.
func TestExtractBytes_PDFRoutingBeatsTextMIME(t *testing.T) {
	const venueText = "Venue: Grand Hotel Amsterdam, 12 March"
	venue := makeSyntheticPDF(t, true, venueText)

	t.Run("text MIME and opaque MIME route to PDF extraction", func(t *testing.T) {
		cases := []struct {
			name     string
			mime     string
			filename string
		}{
			{"text/plain MIME", "text/plain", "upload.pdf"},
			{"text/markdown MIME", "text/markdown", "upload.pdf"},
			{"application/octet-stream MIME", "application/octet-stream", "upload.pdf"},
			{"empty MIME", "", "upload.pdf"},
			{"content decides without a .pdf name", "text/plain", "upload.dat"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				txt, ok, reason := ExtractBytes(venue, tc.mime, tc.filename)
				if !ok {
					t.Fatalf("expected PDF extraction to win over MIME %q, got ok=false reason=%q", tc.mime, reason)
				}
				if txt != venueText {
					t.Fatalf("raw bytes leaked as text (MIME %q, name %q):\n got %q\nwant %q", tc.mime, tc.filename, txt, venueText)
				}
			})
		}
	})

	t.Run("ASCII uncompressed PDF under text/plain extracts (the demonstrated leak shape)", func(t *testing.T) {
		// CHECK round-4 demonstrated the raw leak with all-ASCII PDF bytes: the
		// text extractor accepts them as valid UTF-8 and returns the whole
		// syntax as ok=true. Exact-text equality (not Contains) is what
		// discriminates the raw dump from the extracted page.
		ascii := makeASCIIPDFBytes(t, venueText)
		txt, ok, reason := ExtractBytes(ascii, "text/plain", "upload.pdf")
		if !ok {
			t.Fatalf("expected PDF extraction to win over text/plain, got ok=false reason=%q", reason)
		}
		if txt != venueText {
			t.Fatalf("raw ASCII PDF bytes returned as extracted text:\n got %q\nwant %q", txt, venueText)
		}
	})

	t.Run("text-less PDF with text MIME is a failed extraction, not raw text", func(t *testing.T) {
		empty := makeSyntheticPDF(t, true, "")
		txt, ok, reason := ExtractBytes(empty, "text/plain", "empty.pdf")
		if ok {
			t.Fatalf("expected the text-less PDF to fail extraction under text/plain, got ok=true text=%q", txt)
		}
		// The failure must come from PDF extraction ("no extractable text"),
		// not from a generic text-path rejection: the required behaviour is
		// that the .pdf content is ROUTED to PDF extraction.
		if reason != "pdf contains no extractable text" {
			t.Fatalf("expected the routed-PDF failure reason, got %q", reason)
		}
		if strings.Contains(txt, "%PDF-") {
			t.Fatalf("raw PDF bytes returned as extracted text: %q", txt)
		}
	})

	t.Run("OOXML package wins over a .pdf name", func(t *testing.T) {
		docx := makeDocx(t, "Quarterly summary")
		txt, ok, reason := ExtractBytes(docx, "", "report.pdf")
		if !ok {
			t.Fatalf("expected the OOXML package to win over the .pdf name, got ok=false reason=%q", reason)
		}
		if !strings.Contains(txt, "Quarterly summary") {
			t.Fatalf("expected the OOXML text, got: %q", txt)
		}
	})

	t.Run("explicit OOXML MIME wins over a .pdf name", func(t *testing.T) {
		docx := makeDocx(t, "Quarterly summary")
		const docxMIME = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
		txt, ok, reason := ExtractBytes(docx, docxMIME, "report.pdf")
		if !ok {
			t.Fatalf("expected the explicit OOXML MIME to win, got ok=false reason=%q", reason)
		}
		if !strings.Contains(txt, "Quarterly summary") {
			t.Fatalf("expected the OOXML text, got: %q", txt)
		}
	})

	t.Run("plain text that merely starts with the PDF signature is not text-extracted", func(t *testing.T) {
		notes := []byte("%PDF- notes for tomorrow\nbuy milk\n")
		txt, ok, _ := ExtractBytes(notes, "text/plain", "notes.txt")
		if ok {
			t.Fatalf("expected a structured failure (signature says PDF, parse fails), got ok=true text=%q", txt)
		}
		if strings.Contains(txt, "buy milk") {
			t.Fatalf("raw text leaked back under a failed PDF extraction: %q", txt)
		}
	})

	t.Run("a real text file stays text under text/plain", func(t *testing.T) {
		notes := []byte("just meeting notes, nothing binary\n")
		txt, ok, reason := ExtractBytes(notes, "text/plain", "notes.txt")
		if !ok {
			t.Fatalf("expected ordinary text to stay text, got ok=false reason=%q", reason)
		}
		// Content must survive; the text path strips trailing newlines, which
		// is its own (unchanged) behaviour and not this finding's subject.
		if txt != "just meeting notes, nothing binary" {
			t.Fatalf("ordinary text was mangled by the PDF routing: %q", txt)
		}
	})
}

// TestExtractPDF_LateFinishesFreeSlotsForNewConcurrentParses — round-5 CHECK
// finding N2: slot release must happen for ABANDONED workers too, not only
// for in-time finishes. Two gated parses time out while gated and are then
// released late; without any reset (capacity conservation is observed
// directly), the same two slots must serve two NEW gated parses concurrently:
// both reach their first read and both end on the exact timeout reason — a
// busy here means a late worker leaked its slot. Kills the surviving mutant
// that skips the slot release for abandoned workers (`if state.Load() == 2
// { return }` inside the release defer): both tokens stay leaked, so
// waitForAllSlotsReleased already fails before the new parses can start.
func TestExtractPDF_LateFinishesFreeSlotsForNewConcurrentParses(t *testing.T) {
	orig := pdfExtractTimeout
	pdfExtractTimeout = 150 * time.Millisecond
	t.Cleanup(func() { pdfExtractTimeout = orig })
	resetPDFStuckForTest()
	waitForAllSlotsReleased(t, 2*time.Second)

	const venueText = "Venue: Grand Hotel Amsterdam, 12 March"
	venue := makeSyntheticPDF(t, true, venueText)

	// Stage 1: two gated parses time out while gated, then finish late.
	gateA := make(chan struct{})
	gateB := make(chan struct{})
	results := make(chan extractPDFResult, 2)
	for _, gate := range []chan struct{}{gateA, gateB} {
		go func(release <-chan struct{}) {
			txt, ok, reason := extractPDF(&gatedReaderAt{
				data: bytes.NewReader(venue), release: release, starts: &atomic.Int32{},
			}, int64(len(venue)))
			results <- extractPDFResult{txt, ok, reason}
		}(gate)
	}
	for i := 0; i < 2; i++ {
		select {
		case r := <-results:
			if r.ok || r.reason != "pdf extraction timed out (the file may be malformed)" {
				t.Fatalf("stage-1 call %d: expected the exact timeout reason, got ok=%v reason=%q", i, r.ok, r.reason)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("stage-1 call %d did not return within 10s", i)
		}
	}
	waitForStuckValue(t, 2, 2*time.Second)

	close(gateA)
	close(gateB)
	// No reset here — capacity conservation is the assertion.
	waitForStuckZero(t, 5*time.Second)
	waitForAllSlotsReleased(t, 2*time.Second)

	// Stage 2: two NEW gated parses must both start concurrently on the
	// returned slots — with one leaked slot only one could.
	var newStarts atomic.Int32
	release2 := make(chan struct{})
	for i := 0; i < 2; i++ {
		go func() {
			txt, ok, reason := extractPDF(&gatedReaderAt{
				data: bytes.NewReader(venue), release: release2, starts: &newStarts,
			}, int64(len(venue)))
			results <- extractPDFResult{txt, ok, reason}
		}()
	}
	deadline := time.Now().Add(1 * time.Second)
	for newStarts.Load() < 2 && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if started := newStarts.Load(); started != 2 {
		t.Fatalf("only %d of 2 new parses started after both late finishes returned — leaked slot capacity", started)
	}
	for i := 0; i < 2; i++ {
		select {
		case r := <-results:
			if r.ok || r.reason != "pdf extraction timed out (the file may be malformed)" {
				t.Fatalf("stage-2 call %d: expected admission and the exact timeout reason, got ok=%v reason=%q (a busy here is a leaked slot)", i, r.ok, r.reason)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("stage-2 call %d did not return within 10s", i)
		}
	}

	close(release2)
	waitForStuckZero(t, 5*time.Second)
	waitForAllSlotsReleased(t, 2*time.Second)
}

// makeSVGBytes builds a tiny one-line SVG — the source-text fallback an
// image/svg+xml attachment must keep on a text-only model (CHECK round-4
// finding N1).
func makeSVGBytes(t *testing.T) []byte {
	t.Helper()
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg"><rect width="1" height="1"/></svg>`)
	if !bytes.HasPrefix(svg, []byte("<svg")) {
		t.Fatal("fixture broken: not SVG source")
	}
	return svg
}

// TestExtractBytes_SVGNamedPDFKeepsSourceTextFallback — round-5 CHECK finding
// N1: the .pdf FILE NAME alone must not override an image/* MIME (an SVG
// named diagram.pdf keeps its source-text fallback), while the %PDF-
// SIGNATURE still wins over an image MIME.
func TestExtractBytes_SVGNamedPDFKeepsSourceTextFallback(t *testing.T) {
	const svgSource = `<svg xmlns="http://www.w3.org/2000/svg"><rect width="1" height="1"/></svg>`

	t.Run("image/svg+xml MIME with a .pdf name keeps the SVG source", func(t *testing.T) {
		txt, ok, reason := ExtractBytes(makeSVGBytes(t), "image/svg+xml", "diagram.pdf")
		if !ok {
			t.Fatalf("expected the SVG source-text fallback for image/svg+xml + .pdf name, got ok=false reason=%q", reason)
		}
		if txt != svgSource {
			t.Fatalf("SVG source lost to PDF routing:\n got %q\nwant %q", txt, svgSource)
		}
	})

	t.Run("image/svg+xml MIME with a .svg name stays text", func(t *testing.T) {
		txt, ok, reason := ExtractBytes(makeSVGBytes(t), "image/svg+xml", "diagram.svg")
		if !ok {
			t.Fatalf("expected the SVG fallback for its own extension, got ok=false reason=%q", reason)
		}
		if txt != svgSource {
			t.Fatalf("SVG source mangled:\n got %q\nwant %q", txt, svgSource)
		}
	})

	t.Run("image/png MIME cannot override the PDF signature", func(t *testing.T) {
		const venueText = "Venue: Grand Hotel Amsterdam, 12 March"
		venue := makeSyntheticPDF(t, true, venueText)
		txt, ok, reason := ExtractBytes(venue, "image/png", "x.pdf")
		if !ok {
			t.Fatalf("expected the PDF signature to win over an image MIME, got ok=false reason=%q", reason)
		}
		if txt != venueText {
			t.Fatalf("expected PDF-extracted text, got %q", txt)
		}
	})
}
