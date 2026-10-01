//go:build goolm && stdjson

package memory

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

// cwTwoLineFaultReader is a test-only io.Reader fixture for F1 of the R2
// independent CHECK (/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-qa-evidence/1081-r2-check-20261001-46bc47d3b/original-audit-report.md).
// It delivers both fixture JSONL lines, including the final selected line's
// own newline, AND a non-nil, non-EOF error IN THE SAME Read call — the
// (n > 0, err != nil) shape documented at archive_range.go::ScanJSONLRange's
// defer (lines 116-122): bufio.Reader.ReadSlice can resolve a buffered '\n'
// from data a fill() call already received before that fill()'s own stored
// error is ever surfaced to a caller, so a genuine disk fault arriving
// alongside the bytes that complete the last wanted record would otherwise
// vanish. This is deliberately NOT a separate later Read call returning the
// error on its own — that would be the early/late-read-error shape the
// existing cwRangeReadErrors subtests already cover, not the gap F1 names.
type cwTwoLineFaultReader struct {
	data []byte
	err  error
	sent bool
}

func (r *cwTwoLineFaultReader) Read(p []byte) (int, error) {
	if r.sent {
		return 0, io.EOF
	}
	r.sent = true
	n := copy(p, r.data)
	if n < len(r.data) {
		// The fixture is tiny and archiveReadChunk is 64 KiB; a short copy
		// here means the test's own destination buffer unexpectedly
		// truncated the fixture, not that production code did anything
		// wrong. Fail loudly rather than silently exercising a different,
		// unintended shape (a separate-call fault).
		panic("cwTwoLineFaultReader: destination buffer smaller than fixture data")
	}
	return n, r.err
}

var errCwSimulatedDiskFault = errors.New("cw-simulated-disk-fault: F1 regression probe")

// TestScanJSONLRange_BufferedNonEOFFaultOnFinalLineSurvives is the committed
// repository regression for F1: a buffered non-EOF read fault arriving on the
// exact read that completes the final selected JSONL line must not silently
// disappear, and the scan must not be reported as a clean, complete success
// merely because every selected record was already handed to the callback.
func TestScanJSONLRange_BufferedNonEOFFaultOnFinalLineSurvives(t *testing.T) {
	const line0 = `{"role":"user","content":"first selected record"}` + "\n"
	const line1 = `{"role":"assistant","content":"valid last page"}` + "\n"
	reader := &cwTwoLineFaultReader{data: []byte(line0 + line1), err: errCwSimulatedDiskFault}

	type seen struct {
		idx     int
		raw     string
		role    string
		content string
	}
	var got []seen
	scanErr := ScanJSONLRange(context.Background(), reader, 0, 1, func(idx int, raw []byte, msg ArchivedMessage) error {
		got = append(got, seen{idx: idx, raw: string(raw), role: msg.Role, content: msg.Content})
		return nil
	})

	// Requirement 3: both already-collected valid records (idx 0, the final
	// selected idx 1) must still reach the caller, with their literal raw
	// JSONL bytes preserved exactly as the ArchiveRangeScanner contract
	// requires — the fault must not erase or truncate what was already read.
	want := []seen{
		{idx: 0, raw: line0, role: "user", content: "first selected record"},
		{idx: 1, raw: line1, role: "assistant", content: "valid last page"},
	}
	if len(got) != len(want) {
		t.Fatalf("callback invocations = %d, want %d (both selected records must still be delivered): %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Fatalf("callback[%d] = %+v, want %+v", i, got[i], w)
		}
	}

	// Requirement 2 + the core of requirement 3: despite every selected
	// record arriving cleanly, the scan as a whole must NOT be reported as a
	// successful, complete page — scanErr must be non-nil and must wrap the
	// injected cause, not merely "an error occurred".
	if scanErr == nil {
		t.Fatal("F1 regression: ScanJSONLRange returned a nil error for a scan whose underlying reader " +
			"reported a non-EOF fault on the read that completed the final selected line (idx=1); " +
			"a genuine disk fault silently vanished and the scan was mistaken for a complete, successful page")
	}
	if !errors.Is(scanErr, errCwSimulatedDiskFault) {
		t.Fatalf("scanErr must wrap the injected read fault (errors.Is), got %v", scanErr)
	}
	const wantWrapPrefix = "memory: read archive:"
	if !strings.Contains(scanErr.Error(), wantWrapPrefix) {
		t.Fatalf("scanErr must carry ScanJSONLRange's own wrap prefix %q, got %q", wantWrapPrefix, scanErr.Error())
	}
	if !strings.Contains(scanErr.Error(), errCwSimulatedDiskFault.Error()) {
		t.Fatalf("scanErr must preserve the injected cause's own message, got %q", scanErr.Error())
	}
}
