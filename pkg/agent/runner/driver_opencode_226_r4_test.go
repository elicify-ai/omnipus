package runner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/synctest"
)

func TestOpencode226R4_LiveOversizedStreamClosesWithFatalError(t *testing.T) {
	// Item 2: the saved probe-liveness-red.log hangs without the early stdout
	// reader close. Reuse the real-text-derived 2 MiB line, but this time run
	// an actual fixture process, not just the offline parser. The helper's
	// deadline only detects a hang; successful closure and the exact terminal
	// error shape are the oracle. Mutation proof belongs to independent CHECK.
	data := opencode226R3OversizedStream(t, 0)
	events := opencode226R3Events(t, data, true)
	opencode226R3AssertFatal(t, events, []EventKind{EventKindStart}, "stream read error")
	if message := events[len(events)-1].Err.Message; !strings.Contains(message, bufio.ErrTooLong.Error()) {
		t.Errorf("live oversized-stream diagnostic = %q, want scanner root cause %q", message, bufio.ErrTooLong.Error())
	}
}

func TestOpencode226R4_LegacyTopLevelErrorIsVerbatim(t *testing.T) {
	// Item 3: the older top-level message-only shape is explicitly required
	// by the round-four brief; this is not the 2.x nested error capture.
	data := []byte(`{"type":"error","message":"quota exceeded"}` + "\n")
	events := ParseOpencodeStreamJSON(data, "oc226-r4-legacy-error", 2)
	opencode226R2AssertKinds(t, events, []EventKind{EventKindError})
	opencode226R2AssertError(t, events[0], "quota exceeded")
}

func TestOpencode226R4_UnfinishedToolHasNoResult(t *testing.T) {
	for _, status := range []string{"running", "pending"} {
		t.Run(status, func(t *testing.T) {
			// Item 4: derive from real tool capture line 2, changing only the
			// state status and removing output. These are synthetic unfinished
			// states required by the brief, not claimed additional captures.
			line := opencode226R3Lines(t, "tool.stdout.ndjson")[1]
			line = opencode226R3Replace(t, line, `"status":"completed"`, `"status":"`+status+`"`)
			line = opencode226R3Replace(t, line, `"output":"Created file successfully: probe.txt",`, "")
			turns := 0
			events, ok := NewOpencodeDriver(nil).parseLine(line, "oc226-r4-unfinished", &turns, 2)
			if !ok {
				t.Fatal("recognized unfinished tool line was silently skipped")
			}
			opencode226R2AssertKinds(t, events, []EventKind{EventKindToolCall})
			call := events[0].ToolCall
			if call == nil {
				t.Fatal("unfinished tool event has no call payload")
			}
			const callID = "chatcmpl-tool-b72ea6b992f3d991" // real capture line 2
			if call.CallID != callID || call.ToolName != "write" {
				t.Errorf("unfinished tool identity = %q/%q, want %q/write", call.CallID, call.ToolName, callID)
			}
			var input map[string]string
			if err := json.Unmarshal(call.ToolInput, &input); err != nil {
				t.Fatalf("decode unfinished write input: %v", err)
			}
			if len(input) != 2 || input["path"] != "probe.txt" || input["content"] != "OK" {
				t.Errorf("unfinished tool input = %v, want exactly path=probe.txt and content=OK", input)
			}
			if events[0].ToolResult != nil {
				t.Errorf("unfinished tool must have no result payload: %+v", events[0].ToolResult)
			}
		})
	}
}

func TestOpencode226R4_ScannerErrorWaitsForFullBufferConsumer(t *testing.T) {
	lines := opencode226R3Lines(t, "plain.stdout.ndjson")
	prefix := append(bytes.Join(lines[:2], []byte{'\n'}), '\n')
	synctest.Test(t, func(t *testing.T) {
		const runID = "oc226-r4-full-buffer"
		cause := errors.New("fixture read failed with a full event buffer")
		observed := make(chan struct{})
		reader := io.MultiReader(bytes.NewReader(prefix), opencode226R4ObservedReadError{cause: cause, observed: observed})
		out := make(chan RunEvent, 1) // the one real CAPTURE-OK fills this slot
		finished := make(chan bool, 1)
		driver := NewOpencodeDriver(nil)
		turns := 0
		go func() {
			emittedFatal := streamParserMulti(context.Background(), reader, runID, func(raw []byte) ([]RunEvent, bool) {
				return driver.parseLine(raw, runID, &turns, 2)
			}, out)
			finished <- emittedFatal
			close(out)
		}()

		// Item 5: no consumer runs before this quiescence barrier. The parser
		// has filled out and reached the read error, then either blocks trying
		// to deliver the fatal (correct) or returns after dropping it (mutant).
		// Unlike the old 256-event helper case, concurrent draining cannot race
		// the error send. No sleep, elapsed-time assertion or production mock.
		synctest.Wait()
		select {
		case <-observed:
		default:
			t.Error("parser did not reach the injected read error before consumption")
		}
		if n := len(out); n != cap(out) {
			t.Errorf("paused consumer must leave the event buffer full: len=%d capacity=%d", n, cap(out))
		}
		if len(finished) != 0 {
			t.Error("parser returned before its fatal error could reach the paused consumer")
		}

		var events []RunEvent
		for event := range out {
			if event.RunID != runID {
				t.Errorf("full-buffer event run id = %q, want %q", event.RunID, runID)
			}
			events = append(events, event)
		}
		if !<-finished {
			t.Error("lost scanner failure: emittedFatal=false would let callers synthesize success")
		}
		opencode226R3AssertFatal(t, events, []EventKind{EventKindOutput}, cause.Error())
		opencode226R2AssertAnswer(t, events, "CAPTURE-OK")
	})
}

// A stream-edge reader, not a decoder mock. Its signal proves scanner failure
// was reached while the consumer was paused inside the scheduling bubble.
type opencode226R4ObservedReadError struct {
	cause    error
	observed chan struct{}
}

func (r opencode226R4ObservedReadError) Read([]byte) (int, error) {
	close(r.observed)
	return 0, r.cause
}
