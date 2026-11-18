package runner

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"testing"
)

func TestOpencode226R3_FailedToolRetainsFailure(t *testing.T) {
	const diagnostic = "write failed: permission denied: probe.txt"
	capture := opencode226R2Read(t, "opencode-2.2.6", "tool.stdout.ndjson")
	// F1 derivation: real tool capture line 2, changing only state.status and
	// adding state.error. The installed 2.2.6 emitter's session.tool.failed
	// branch emits status:"error", error:i.state.error.message. The diagnostic
	// above is injected test input, not a claimed captured failure. All other
	// fields, including the old output, stay untouched as requested: the failed
	// status must prevail over any stale successful output. The later real DONE
	// and stop also remain, proving tool failure need not make recovery fatal.
	data := opencode226R3Replace(t, capture, `"status":"completed"`, `"status":"error","error":"`+diagnostic+`"`)
	for _, live := range []bool{false, true} {
		t.Run(fmt.Sprintf("live_%t", live), func(t *testing.T) {
			events := opencode226R3Events(t, data, live)
			want := []EventKind{EventKindToolCall, EventKindToolResult, EventKindOutput, EventKindEnd}
			resultIndex := 1
			if live {
				want = append([]EventKind{EventKindStart}, want...)
				resultIndex++
			}
			opencode226R2AssertKinds(t, events, want)
			result := events[resultIndex].ToolResult
			if result == nil {
				t.Fatal("failed write has no ToolResultEvent payload")
			}
			const callID = "chatcmpl-tool-b72ea6b992f3d991" // real tool capture line 2
			if result.CallID != callID || result.ToolName != "write" {
				t.Errorf("failed tool result identity = %q/%q, want %q/write", result.CallID, result.ToolName, callID)
			}
			if !result.IsError {
				t.Errorf("terminal failed-tool result must set IsError, got %+v", result)
			}
			var output string
			if err := json.Unmarshal(result.Output, &output); err != nil {
				t.Fatalf("failed-tool output must carry the diagnostic as a JSON string: %q: %v", result.Output, err)
			}
			if output != diagnostic {
				t.Errorf("failed-tool diagnostic = %q, want exactly %q, not empty or stale success output", output, diagnostic)
			}
			opencode226R2AssertAnswer(t, events, "DONE")
		})
	}
}

func TestOpencode226R3_StreamReadFailureIsNeverSuccess(t *testing.T) {
	// Review F2 identifies a 256-event offline buffer. At 255 preceding text
	// events its error still fits; at 256 the old nonblocking send loses it.
	// Zero is the no-prior-output control. Do not add a 257th ordinary event:
	// that would block the old synchronous helper before reaching the failure.
	for _, count := range []int{0, 255, 256} {
		t.Run(fmt.Sprintf("offline_prefix_%d", count), func(t *testing.T) {
			events := opencode226R3Events(t, opencode226R3OversizedStream(t, count), false)
			prefix := make([]EventKind, count)
			for i := range prefix {
				prefix[i] = EventKindOutput
			}
			opencode226R3AssertFatal(t, events, prefix, bufio.ErrTooLong.Error())
			for i := range count {
				if events[i].Output == nil || *events[i].Output != (OutputEvent{Text: "CAPTURE-OK"}) {
					t.Errorf("already-delivered text event %d = %+v, want exact CAPTURE-OK", i, events[i].Output)
				}
			}
		})
	}
	t.Run("reader_error_preserves_cause", func(t *testing.T) {
		// A real io.Reader fails after the first real answer, with no oversized
		// token or subprocess. Keep the actual driver parseLine callback, not a
		// stand-in decoder; the fake is solely the stream's I/O boundary.
		lines := opencode226R3Lines(t, "plain.stdout.ndjson")
		prefix := append(bytes.Join(lines[:2], []byte{'\n'}), '\n')
		cause := errors.New("fixture transport disconnected after CAPTURE-OK")
		reader := io.MultiReader(bytes.NewReader(prefix), opencode226R3BrokenReader{err: cause})
		driver := NewOpencodeDriver(nil)
		turns := 0
		const runID = "oc226-r3-reader-error"
		out := make(chan RunEvent, 2) // one captured answer plus its required diagnostic
		emittedFatal := streamParserMulti(opencode226R2Context(t), reader, runID, func(raw []byte) ([]RunEvent, bool) {
			return driver.parseLine(raw, runID, &turns, 2)
		}, out)
		close(out)
		var events []RunEvent
		for event := range out {
			if event.RunID != runID {
				t.Errorf("reader-error event run id = %q, want %q", event.RunID, runID)
			}
			events = append(events, event)
		}
		opencode226R3AssertFatal(t, events, []EventKind{EventKindOutput}, cause.Error())
		opencode226R2AssertAnswer(t, events, "CAPTURE-OK")
		if !emittedFatal {
			t.Error("known reader failure must report emittedFatal=true so callers cannot synthesize success")
		}
	})
}

func TestOpencode226R3_UndecodableKnownEventIsVisible(t *testing.T) {
	for _, tc := range []struct {
		name, capture, old, replacement, discriminator string
		line                                           int
		want                                           []EventKind
		answer                                         string
	}{
		{
			name: "text", capture: "plain.stdout.ndjson", line: 1,
			old: `"text":"CAPTURE-OK"`, replacement: `"text":42`, discriminator: "text",
			want: []EventKind{EventKindOutput, EventKindEnd}, answer: "CAPTURE-OK",
		},
		{
			name: "tool", capture: "tool.stdout.ndjson", line: 1,
			old: `"id":"chatcmpl-tool-b72ea6b992f3d991"`, replacement: `"id":42`, discriminator: "tool",
			want: []EventKind{EventKindToolCall, EventKindToolResult, EventKindOutput, EventKindEnd}, answer: "DONE",
		},
	} {
		t.Run(tc.name+"_valid_capture_control", func(t *testing.T) {
			data := opencode226R2Read(t, "opencode-2.2.6", tc.capture)
			events := opencode226R3Events(t, data, false)
			opencode226R2AssertKinds(t, events, tc.want)
			opencode226R2AssertAnswer(t, events, tc.answer)
		})
		for _, live := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s_live_%t", tc.name, live), func(t *testing.T) {
				// F3 derivation: select the real recognized text/tool line, then
				// change exactly one string field to a JSON number. The envelope
				// remains valid JSON with its real type, session and other fields.
				// Replay that line alone: there is no explicit recovery to excuse
				// lost content, only the injected decode failure and clean exit 0.
				lines := opencode226R3Lines(t, tc.capture)
				data := opencode226R3Replace(t, lines[tc.line], tc.old, tc.replacement)
				data = append(data, '\n')
				events := opencode226R3Events(t, data, live)
				var prefix []EventKind
				if live {
					prefix = []EventKind{EventKindStart}
				}
				opencode226R3AssertFatal(t, events, prefix, tc.discriminator)
			})
		}
	}
}
