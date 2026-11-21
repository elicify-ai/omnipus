package runner

import (
	"fmt"
	"slices"
	"testing"
)

// R2 item 1 / FR-043: the plain capture announces this native conversation in
// top-level sessionID on every line. Never seed the driver's private nativeID.
func TestOpencode226R2_RunThenResumeUsesCapturedSessionID(t *testing.T) {
	const nativeID = "ses_e213436bfffeJySy9JV8Q7sNf3" // plain capture lines 1-3
	const firstInstruction = "round two first instruction"
	const nextInstruction = "round two new instruction"
	for _, method := range []string{"Resume", "ResumeWith"} {
		t.Run(method, func(t *testing.T) {
			data := opencode226R2Read(t, "opencode-2.2.6", "plain.stdout.ndjson")
			opts, argsLog := opencode226R2Stub(t, data, 0, "opencode v2.2.6")
			opts.Input = firstInstruction
			driver := NewOpencodeDriver(nil)
			initial := opencode226R2Run(t, driver, opts)
			opencode226R2AssertKinds(t, initial, []EventKind{EventKindStart, EventKindOutput, EventKindEnd})
			opencode226R2AssertAnswer(t, initial, "CAPTURE-OK")
			firstCalls := opencode226R2Argv(t, argsLog)
			if len(firstCalls) != 1 {
				t.Fatalf("fresh run invocations = %q, want exactly one", firstCalls)
			}
			if got := opencode226R2FlagValues(t, firstCalls[0], "--session"); len(got) != 0 {
				t.Fatalf("fresh Run unexpectedly carried a native session: %q", got)
			}

			ctx := opencode226R2Context(t)
			next := opts
			next.RunID = "oc226-r2-follow-up" // dispatch id deliberately differs from the native id
			next.Input = nextInstruction
			var ch <-chan RunEvent
			var err error
			if method == "Resume" {
				ch, err = driver.Resume(ctx, next.RunID, nextInstruction)
			} else {
				ch, err = driver.ResumeWith(ctx, next)
			}
			if err != nil {
				t.Fatalf("%s after 2.2.6 capture announced sessionID %q refused the new instruction: %v", method, nativeID, err)
			}
			resumed := opencode226R2Collect(t, ctx, ch, next.RunID)
			opencode226R2AssertKinds(t, resumed, []EventKind{EventKindStart, EventKindOutput, EventKindEnd})
			opencode226R2AssertAnswer(t, resumed, "CAPTURE-OK")
			calls := opencode226R2Argv(t, argsLog)
			if len(calls) != 2 {
				t.Fatalf("Run + %s invocations = %q, want exactly two", method, calls)
			}
			if got := opencode226R2FlagValues(t, calls[1], "--session"); !slices.Equal(got, []string{nativeID}) {
				t.Errorf("%s native session arguments = %q, want exactly [%q]; argv=%q", method, got, nativeID, calls[1])
			}
			separator := slices.Index(calls[1], "--")
			if separator < 0 || !slices.Equal(calls[1][separator+1:], []string{nextInstruction}) {
				t.Errorf("%s must deliver only the new instruction as one positional argument; argv=%q", method, calls[1])
			}
			if slices.Contains(calls[1], firstInstruction) {
				t.Errorf("%s replayed the first instruction; argv=%q", method, calls[1])
			}
		})
	}
}

// R2 item 2: tool capture lines 1 and 4 open two steps. Test below, exactly at,
// and above that count; cap=1 must cancel on step 2 before emitting DONE or End.
func TestOpencode226R2_StepStartTurnCap(t *testing.T) {
	for _, turnLimit := range []int{1, 2, 3} {
		t.Run(fmt.Sprintf("cap_%d", turnLimit), func(t *testing.T) {
			data := opencode226R2Read(t, "opencode-2.2.6", "tool.stdout.ndjson")
			opts, _ := opencode226R2Stub(t, data, 0, "opencode v2.2.6")
			opts.MaxTurns = turnLimit
			events := opencode226R2Run(t, NewOpencodeDriver(nil), opts)
			if turnLimit == 1 {
				opencode226R2AssertKinds(t, events, []EventKind{EventKindStart, EventKindToolCall, EventKindToolResult, EventKindError})
				opencode226R2AssertError(t, events[len(events)-1], "turn cap exceeded: 2 turns (max 1)")
			} else {
				opencode226R2AssertKinds(t, events, []EventKind{EventKindStart, EventKindToolCall, EventKindToolResult, EventKindOutput, EventKindEnd})
				opencode226R2AssertAnswer(t, events, "DONE")
			}
		})
	}
}

// R2 item 3: unlike the offline parser tests, these observe Run through process
// exit and channel close, so the waiter must synthesize exactly one success End
// and must not synthesize success or a duplicate error on provider exit 1.
func TestOpencode226R2_LiveRunTerminalOutcome(t *testing.T) {
	for _, tc := range []struct {
		name, capture string
		exitCode      int
	}{
		{"clean_exit", "plain.stdout.ndjson", 0},
		{"provider_exit_1", "badmodel.stdout.ndjson", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := opencode226R2Read(t, "opencode-2.2.6", tc.capture)
			opts, _ := opencode226R2Stub(t, data, tc.exitCode, "opencode v2.2.6")
			events := opencode226R2Run(t, NewOpencodeDriver(nil), opts)
			if tc.exitCode == 0 {
				opencode226R2AssertKinds(t, events, []EventKind{EventKindStart, EventKindOutput, EventKindEnd})
				opencode226R2AssertAnswer(t, events, "CAPTURE-OK")
			} else {
				opencode226R2AssertKinds(t, events, []EventKind{EventKindStart, EventKindError})
				opencode226R2AssertError(t, events[1], "Model unavailable: openrouter/no-such-vendor/no-such-model",
					"provider.no-route") // #1857 spec 13.8.2 row C2: nested error.type is carried
			}
		})
	}
}

// R2 item 4 / CHECK M6: the existing legacy fixture already ends inline with
// session.complete (line 5). Check the full sequence, not only hasEnd=true.
func TestOpencode226R2_LegacyInlineEndIsNotDuplicated(t *testing.T) {
	data := opencode226R2Read(t, "fixtures", "opencode_stream_json.ndjson")
	want := []EventKind{EventKindOutput, EventKindPermissionRequest, EventKindOutput, EventKindEnd}
	t.Run("offline", func(t *testing.T) {
		events := ParseOpencodeStreamJSON(data, "oc226-r2-legacy", 2)
		opencode226R2AssertKinds(t, events, want)
	})
	t.Run("live", func(t *testing.T) {
		opts, _ := opencode226R2Stub(t, data, 0, "opencode v2.2.6")
		events := opencode226R2Run(t, NewOpencodeDriver(nil), opts)
		liveWant := append([]EventKind{EventKindStart}, want...)
		opencode226R2AssertKinds(t, events, liveWant)
	})
}

// R2 item 5 / CHECK M7: Run itself selects knownOpencodeVersionPrefixes. Never
// pass a test-written prefix list. The unknown control must still degrade.
func TestOpencode226R2_RunRecognizesShippedVersionPrefix(t *testing.T) {
	for _, tc := range []struct {
		version string
		known   bool
	}{
		{"2.2.6", true},
		{"9.9.9", false},
	} {
		t.Run(tc.version, func(t *testing.T) {
			data := opencode226R2Read(t, "opencode-2.2.6", "plain.stdout.ndjson")
			opts, _ := opencode226R2Stub(t, data, 0, "opencode v"+tc.version)
			events := opencode226R2Run(t, NewOpencodeDriver(nil), opts)
			opencode226R2AssertKinds(t, events, []EventKind{EventKindStart, EventKindOutput, EventKindEnd})
			want := StartEvent{CLI: "opencode", Version: tc.version, VersionKnown: tc.known}
			if events[0].Start == nil || *events[0].Start != want {
				t.Errorf("Run start metadata = %+v, want exactly %+v from the driver's shipped prefix list", events[0].Start, want)
			}
		})
	}
}

func opencode226R2AssertKinds(t *testing.T, events []RunEvent, want []EventKind) {
	t.Helper()
	got := make([]EventKind, len(events))
	for i, ev := range events {
		got[i] = ev.Kind
	}
	if !slices.Equal(got, want) {
		t.Fatalf("public event sequence through channel close = %q, want exactly %q", got, want)
	}
}

func opencode226R2AssertAnswer(t *testing.T, events []RunEvent, want string) {
	t.Helper()
	var chunks []string
	for _, ev := range events {
		if ev.Kind == EventKindOutput {
			if ev.Output == nil {
				t.Fatal("output event has no assistant answer payload")
			}
			chunks = append(chunks, ev.Output.Text)
		}
	}
	if !slices.Equal(chunks, []string{want}) {
		t.Errorf("assistant answer chunks = %q, want exactly [%q]", chunks, want)
	}
}

func opencode226R2AssertError(t *testing.T, event RunEvent, message string, errType ...string) {
	t.Helper()
	// errType is the structured error.type the stream carried, when it carried one.
	want := ErrorEvent{Message: message, Fatal: true}
	if len(errType) > 0 {
		want.Type = errType[0]
	}
	if event.Err == nil || *event.Err != want {
		t.Errorf("terminal error = %+v, want exactly %+v", event.Err, want)
	}
}

func opencode226R2FlagValues(t *testing.T, args []string, flag string) []string {
	t.Helper()
	var values []string
	for i, arg := range args {
		if arg == "--" {
			break
		}
		if arg == flag {
			if i+1 >= len(args) {
				t.Fatalf("%s is missing its value: argv=%q", flag, args)
			}
			values = append(values, args[i+1])
		}
	}
	return values
}
