package docextract

// pdf_admission.go — the one process-wide admission and timeout boundary for
// every PDF parse in this package (#1959, PORT-SEC-1). Both PDF entry points,
// the extractor (extractPDF, behind Extract and ExtractBytes) and the
// inspector (InspectPDF, behind read_file, library_read and chat PDF
// attachments), run their parse through runPDFParse, so they share one set
// of parse slots, one stuck-parse count and one outer timeout. runPDFParse is
// called exactly once per entry point and the parse functions it runs never
// call back into it: no path holds a slot while it asks for another.

import (
	"errors"
	"fmt"
	"sync/atomic"
	"time"
)

// Typed admission failures. They describe this process's load, never the
// file: a caller must not report them as a malformed or text-less PDF.
var (
	// ErrPDFBusy: the parse was refused before it started because the PDF
	// parser was fully occupied (every slot busy for a whole wait budget,
	// or earlier parses still stuck after their timeout).
	ErrPDFBusy = errors.New("pdf parser busy")
	// ErrPDFTimedOut: the parse started but did not finish within
	// pdfExtractTimeout; it was abandoned and keeps its slot until it ends.
	ErrPDFTimedOut = errors.New("pdf parse timed out")
)

// The two refusal causes, both ErrPDFBusy; extractPDF tells them apart to
// keep its released reason texts.
var (
	errPDFStuckRefusal = fmt.Errorf("%w: earlier parses are still stuck", ErrPDFBusy)
	errPDFSlotWait     = fmt.Errorf("%w: too many parses are running", ErrPDFBusy)
)

// pdfSlots bounds the PDF parses in flight (running or stuck) at pdfMaxStuck.
var pdfSlots = make(chan struct{}, pdfMaxStuck)

// pdfExtractTimeout bounds one PDF parse; a variable so tests can shorten it.
var pdfExtractTimeout = 15 * time.Second

// pdfMaxStuck is how many timed-out parses may still be running before
// further PDF parses are refused; it is also the number of parse slots.
const pdfMaxStuck = 2

// pdfStuck counts timed-out parses whose goroutines have not yet returned.
var pdfStuck atomic.Int32

// runPDFParse runs parse under the shared admission and timeout boundary and
// returns its result, or the zero T and an error wrapping ErrPDFBusy or
// ErrPDFTimedOut. parse must recover its own panics (it runs on a separate
// goroutine, where an unrecovered panic would end the process) and must not
// call runPDFParse.
//
// A malformed file can make a parser loop (#1959), and a deferred recover
// cannot stop a loop. The parse therefore runs in a goroutine and the caller
// waits at most pdfExtractTimeout. A timed-out goroutine cannot be killed, so
// at most pdfMaxStuck of them may be spinning at once; beyond that new parses
// are refused immediately instead of piling up.
func runPDFParse[T any](parse func() T) (T, error) {
	var zero T
	if pdfStuck.Load() >= pdfMaxStuck {
		return zero, errPDFStuckRefusal
	}
	timer := time.NewTimer(pdfExtractTimeout)
	defer timer.Stop()
	// Reserve a parse slot before starting: slots bound the goroutines in
	// flight (running or stuck), so a burst of concurrent calls cannot start
	// more than pdfMaxStuck parses. A caller waits for a slot at most one budget.
	select {
	case pdfSlots <- struct{}{}:
	case <-timer.C:
		return zero, errPDFSlotWait
	}
	var state atomic.Int32 // 0 running, 1 finished in time, 2 abandoned (timed out)
	done := make(chan T, 1)
	go func() {
		res := parse()
		// The slot is released only when the parse really returns.
		defer func() {
			select {
			case <-pdfSlots:
			default:
			}
		}()
		if !state.CompareAndSwap(0, 1) {
			// Abandoned earlier: the stuck parse has finally ended.
			pdfStuck.Add(-1)
			return
		}
		done <- res
	}()
	// Once a slot is won, the parse gets its own full budget.
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(pdfExtractTimeout)
	select {
	case res := <-done:
		return res, nil
	case <-timer.C:
		// Count before abandoning so the worker's decrement cannot run first.
		pdfStuck.Add(1)
		if state.CompareAndSwap(0, 2) {
			return zero, ErrPDFTimedOut
		}
		pdfStuck.Add(-1)
		return <-done, nil
	}
}
