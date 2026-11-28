package docextract

// PORT-SEC-1 regression (#1968): InspectPDF (read_file, library_read, chat
// PDF attachments) and extractPDF (Extract, ExtractBytes) must share ONE
// parse admission and timeout boundary. Oracle: the finding, not the code
// output. With both parse slots held by stuck parses, an inspection must be
// refused at once with a typed busy error, must not be reported as a
// malformed file, and must not start a third parse.

import (
	"bytes"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const pdfVenueText = "Venue: Grand Hotel Amsterdam, 12 March"

type heldParseResult struct {
	ok     bool
	reason string
}

// holdAllPDFParseSlots starts two extractPDF parses behind gatedReaderAt that
// stay blocked until the returned release function runs, waits for both to
// time out (150ms budget) so both slots are stuck, and registers cleanup that
// releases the gate, waits for the late parses to hand their slots back and
// resets the stuck state. It returns the shared start counter.
func holdAllPDFParseSlots(t *testing.T, venue []byte) (release func(), starts *atomic.Int32) {
	t.Helper()
	orig := pdfExtractTimeout
	pdfExtractTimeout = 150 * time.Millisecond
	t.Cleanup(func() { pdfExtractTimeout = orig })
	resetPDFStuckForTest()
	waitForAllSlotsReleased(t, 2*time.Second)

	gate := make(chan struct{})
	var once sync.Once
	release = func() { once.Do(func() { close(gate) }) }
	starts = &atomic.Int32{}
	t.Cleanup(func() {
		release()
		waitForStuckValue(t, 0, 5*time.Second)
		waitForAllSlotsReleased(t, 2*time.Second)
		resetPDFStuckForTest()
	})

	results := make(chan heldParseResult, 2)
	for i := 0; i < 2; i++ {
		go func() {
			_, ok, reason := extractPDF(&gatedReaderAt{
				data: bytes.NewReader(venue), release: gate, starts: starts,
			}, int64(len(venue)))
			results <- heldParseResult{ok, reason}
		}()
	}
	for i := 0; i < 2; i++ {
		select {
		case r := <-results:
			if r.ok || r.reason != "pdf extraction timed out (the file may be malformed)" {
				t.Fatalf("held parse %d: expected the exact timeout reason, got ok=%v reason=%q", i, r.ok, r.reason)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("held parse %d did not return within 10s", i)
		}
	}
	waitForStuckValue(t, 2, 2*time.Second)
	if got := starts.Load(); got != 2 {
		t.Fatalf("expected exactly 2 parses started behind the gate, got %d", got)
	}
	return release, starts
}

// TestInspectPDF_SharesAdmissionBoundaryWithExtractor - PORT-SEC-1: with both
// slots held by stuck extractor parses, InspectPDF is refused immediately
// (under 50ms) with ErrPDFBusy, not ErrPDFMalformed, returns an empty PDFInfo,
// starts no third parse and takes no slot. After release, inspection works
// again and returns the exact page count and text.
func TestInspectPDF_SharesAdmissionBoundaryWithExtractor(t *testing.T) {
	venue := makeSyntheticPDF(t, true, pdfVenueText)
	release, starts := holdAllPDFParseSlots(t, venue)

	// RR-SEC-2: no stopwatch oracle. The refusal is proved by its cause: the
	// stuck-parse refusal is decided before any slot wait, timer or worker
	// goroutine exists (pdf_admission.go::runPDFParse), while the other busy
	// cause (errPDFSlotWait) waits for a slot first. With the outer timeout
	// raised to an hour, an inspection that waited or parsed instead of being
	// refused would hang, and only a generous watchdog (hang detector, not a
	// speed assertion) reports that.
	origTimeout := pdfExtractTimeout
	pdfExtractTimeout = time.Hour
	type inspectOutcome struct {
		info PDFInfo
		err  error
	}
	outcome := make(chan inspectOutcome, 1)
	go func() {
		info, err := InspectPDF(venue)
		outcome <- inspectOutcome{info, err}
	}()
	var info PDFInfo
	var err error
	select {
	case o := <-outcome:
		info, err = o.info, o.err
	case <-time.After(20 * time.Second):
		pdfExtractTimeout = origTimeout
		t.Fatalf("InspectPDF did not return within the 20s hang watchdog while both slots were stuck: it waited or parsed instead of refusing")
	}
	pdfExtractTimeout = origTimeout
	if err == nil || !errors.Is(err, errPDFStuckRefusal) {
		t.Fatalf("expected the stuck-parse refusal (decided before any wait or parser start), got %v", err)
	}
	if errors.Is(err, errPDFSlotWait) {
		t.Fatalf("the refusal must not be the slot-wait cause: %v", err)
	}
	if !errors.Is(err, ErrPDFBusy) {
		t.Fatalf("expected ErrPDFBusy while both slots are stuck, got %v", err)
	}
	if errors.Is(err, ErrPDFMalformed) {
		t.Fatalf("a busy parser must not be reported as a malformed file: %v", err)
	}
	if info != (PDFInfo{}) {
		t.Fatalf("a refused inspection must return an empty PDFInfo, got %+v", info)
	}
	if got := starts.Load(); got != 2 {
		t.Fatalf("no third parse may start: starts = %d, want 2", got)
	}
	if got := len(pdfSlots); got != 2 {
		t.Fatalf("the refused inspection must not take or free a slot: len(pdfSlots) = %d, want 2", got)
	}
	if got := pdfStuck.Load(); got != 2 {
		t.Fatalf("pdfStuck = %d, want 2", got)
	}

	release()
	waitForStuckValue(t, 0, 5*time.Second)
	waitForAllSlotsReleased(t, 2*time.Second)

	info, err = InspectPDF(venue)
	if err != nil {
		t.Fatalf("inspection after the held parses ended must succeed: %v", err)
	}
	if info.Pages != 1 || info.Text != pdfVenueText {
		t.Fatalf("inspection after release: pages=%d text=%q, want 1 page and %q", info.Pages, info.Text, pdfVenueText)
	}
}
