package runner

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func opencode226R3Lines(t *testing.T, capture string) [][]byte {
	t.Helper()
	data := opencode226R2Read(t, "opencode-2.2.6", capture)
	return bytes.Split(bytes.TrimSpace(data), []byte{'\n'})
}

func opencode226R3Replace(t *testing.T, data []byte, old, replacement string) []byte {
	t.Helper()
	if n := bytes.Count(data, []byte(old)); n != 1 {
		t.Fatalf("capture derivation needs exactly one %q, found %d", old, n)
	}
	derived := bytes.Replace(data, []byte(old), []byte(replacement), 1)
	for _, line := range bytes.Split(bytes.TrimSpace(derived), []byte{'\n'}) {
		if !json.Valid(line) {
			t.Fatal("derived event must remain valid JSON; this is not the malformed-JSON policy")
		}
	}
	return derived
}

func opencode226R3Events(t *testing.T, data []byte, live bool) []RunEvent {
	t.Helper()
	const runID = "oc226-r3"
	if live {
		opts, _ := opencode226R2Stub(t, data, 0, "opencode v2.2.6")
		opts.RunID = runID
		return opencode226R2Run(t, NewOpencodeDriver(nil), opts)
	}
	events := ParseOpencodeStreamJSON(data, runID, 2)
	for _, ev := range events {
		if ev.RunID != runID {
			t.Errorf("%s event run id = %q, want %q", ev.Kind, ev.RunID, runID)
		}
	}
	return events
}

// F2: repeat the real plain-text line, then inject a valid-JSON text line
// larger than the documented scanner instrument's 1 MiB limit (review F2).
// Two MiB is the stressor, not a claimed real captured answer or product limit.
// Retain the real start/stop lines: a known read failure, not an absent stop,
// is what must prevent a successful End.
func opencode226R3OversizedStream(t *testing.T, prefixCount int) []byte {
	t.Helper()
	lines := opencode226R3Lines(t, "plain.stdout.ndjson")
	large := opencode226R3Replace(t, lines[1], `"text":"CAPTURE-OK"`, `"text":"`+strings.Repeat("x", 2<<20)+`"`)
	var stream bytes.Buffer
	stream.Write(lines[0])
	stream.WriteByte('\n')
	for range prefixCount {
		stream.Write(lines[1])
		stream.WriteByte('\n')
	}
	stream.Write(large)
	stream.WriteByte('\n')
	stream.Write(lines[2])
	stream.WriteByte('\n')
	return stream.Bytes()
}

// Exact diagnostic prose is not specified in F2/F3. Require the known root
// cause/event discriminator, a fatal public error, and no successful End.
func opencode226R3AssertFatal(t *testing.T, events []RunEvent, prefix []EventKind, cause string) {
	t.Helper()
	want := append(append([]EventKind(nil), prefix...), EventKindError)
	opencode226R2AssertKinds(t, events, want)
	last := events[len(events)-1]
	if last.Err == nil {
		t.Fatal("detected failure has no public diagnostic payload")
	}
	if !last.Err.Fatal {
		t.Errorf("detected read/decode failure must be fatal, got %+v", last.Err)
	}
	if !strings.Contains(strings.ToLower(last.Err.Message), strings.ToLower(cause)) {
		t.Errorf("public diagnostic = %q, want the detected cause/event %q", last.Err.Message, cause)
	}
}

type opencode226R3BrokenReader struct{ err error }

func (r opencode226R3BrokenReader) Read([]byte) (int, error) { return 0, r.err }
