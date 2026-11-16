package runner_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
)

// Q71 C / Q83 A, R1: plain capture line 2 is the whole answer and line 3
// finishes with reason=stop. Expectations come from the capture, not the driver.
func TestOpencode226_PlainCaptureDeliversAnswerAndCompletes(t *testing.T) {
	events := opencode226ParseCapture(t, "plain.stdout.ndjson")
	opencode226AssertKinds(t, events, []runner.EventKind{
		runner.EventKindOutput, runner.EventKindEnd,
	})
	opencode226AssertAnswer(t, events, "CAPTURE-OK")
}

// R2: tool capture line 2 specifies a completed write (including its result).
// Line 3 ends only the tool-calls step; the answer and real stop are lines 5-6.
func TestOpencode226_ToolCapturePreservesCallResultAndCompletes(t *testing.T) {
	events := opencode226ParseCapture(t, "tool.stdout.ndjson")
	opencode226AssertKinds(t, events, []runner.EventKind{
		runner.EventKindToolCall, runner.EventKindToolResult,
		runner.EventKindOutput, runner.EventKindEnd,
	})
	opencode226AssertAnswer(t, events, "DONE")

	// All values below are from tool capture line 2; compare the whole input
	// object semantically so JSON whitespace and key order are not oracles.
	const callID = "chatcmpl-tool-b72ea6b992f3d991"
	wantInput := map[string]any{"path": "probe.txt", "content": "OK"}
	calls, results := 0, 0
	for _, ev := range events {
		switch ev.Kind {
		case runner.EventKindToolCall:
			calls++
			if ev.ToolCall == nil {
				t.Error("write tool-call event has no ToolCall payload")
				continue
			}
			call := ev.ToolCall
			if call.CallID != callID || call.ToolName != "write" {
				t.Errorf("tool call = (id=%q, name=%q), want (%q, write)", call.CallID, call.ToolName, callID)
			}
			var input map[string]any
			if err := json.Unmarshal(call.ToolInput, &input); err != nil {
				t.Errorf("write input %q is not a JSON object: %v", call.ToolInput, err)
			} else if !reflect.DeepEqual(input, wantInput) {
				t.Errorf("write input = %#v, want exactly %#v", input, wantInput)
			}
		case runner.EventKindToolResult:
			results++
			if ev.ToolResult == nil {
				t.Error("completed write tool-result event has no ToolResult payload")
				continue
			}
			result := ev.ToolResult
			if result.CallID != callID || result.ToolName != "write" || result.IsError {
				t.Errorf("tool result = (id=%q, name=%q, is_error=%v), want (%q, write, false)",
					result.CallID, result.ToolName, result.IsError, callID)
			}
			// ToolResultEvent.Output is JSON-encoded; the recorded value is a string.
			var output string
			if err := json.Unmarshal(result.Output, &output); err != nil {
				t.Errorf("write output %q is not a JSON string: %v", result.Output, err)
			} else if output != "Created file successfully: probe.txt" {
				t.Errorf("write output = %q, want %q", output, "Created file successfully: probe.txt")
			}
		}
	}
	if calls != 1 || results != 1 {
		t.Errorf("completed write yielded %d call(s) and %d result(s), want exactly 1 of each before DONE", calls, results)
	}
	// RunEvent has no usage/token/cost slot. The final step_finish counters are
	// retained in the fixture but cannot be asserted through this event model.
}

// R3: badmodel capture line 1 carries error.message; its real process exited 1.
// A parser result must preserve that diagnostic as a fatal error, never an End.
func TestOpencode226_ProviderErrorIsVisibleAndNotSuccessful(t *testing.T) {
	events := opencode226ParseCapture(t, "badmodel.stdout.ndjson")
	opencode226AssertKinds(t, events, []runner.EventKind{runner.EventKindError})
	if len(events) != 1 {
		t.Fatalf("provider failure produced %d events, want exactly one fatal error and no successful completion", len(events))
	}
	want := runner.ErrorEvent{
		Message: "Model unavailable: openrouter/no-such-vendor/no-such-model",
		Fatal:   true,
	}
	if events[0].Err == nil {
		t.Fatalf("provider failure has no visible error payload; want %+v", want)
	}
	if got := *events[0].Err; got != want {
		t.Errorf("visible provider error = %+v, want exactly %+v", got, want)
	}
}

// R4: both T11 control captures pass this exact nested --model value and exit 0.
// The bare-name negative control is required by the dispatch, not inferred from
// the guard's current implementation. It is checked even when the positive fails.
func TestOpencode226_NestedModelIsPassedWithoutOmissionWarning(t *testing.T) {
	const model = "openrouter/deepseek/deepseek-v4.1-flash"
	if !runner.LooksLikeProviderModel(model) {
		t.Errorf("captured nested model %q was rejected as not provider/model", model)
	}
	args, warnings := opencode226BuildArgsWithWarnings(model)
	var modelValues []string
	for i, arg := range args {
		if arg == "--" {
			break
		}
		if arg == "--model" {
			if i+1 >= len(args) {
				t.Error("--model has no following value")
			} else {
				modelValues = append(modelValues, args[i+1])
			}
		}
	}
	if !slices.Equal(modelValues, []string{model}) {
		t.Errorf("--model values before the argument terminator = %q, want exactly [%q]; argv=%q", modelValues, model, args)
	}
	if warnings != "" {
		t.Errorf("valid captured nested model emitted a warning, want none: %s", warnings)
	}

	const bareModel = "deepseek-v4.1-flash"
	bareQualified := runner.LooksLikeProviderModel(bareModel)
	if bareQualified {
		t.Errorf("bare model %q was accepted as provider/model, want false", bareModel)
	}
	bareArgs, _ := opencode226BuildArgsWithWarnings(bareModel)
	if slices.Contains(bareArgs, "--model") {
		t.Errorf("bare model must not be passed as provider/model; argv=%q", bareArgs)
	} else if !bareQualified {
		t.Logf("negative control: %q is not provider/model and --model remains omitted", bareModel)
	}
}

func opencode226ParseCapture(t *testing.T, name string) []runner.RunEvent {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "opencode-2.2.6", name))
	if err != nil {
		t.Fatalf("read captured OpenCode 2.2.6 stdout %q: %v", name, err)
	}
	runID := "opencode-226-" + name
	// The longest supplied capture has two step_start events (tool lines 1, 4).
	const maxTurns = 2
	events := runner.ParseOpencodeStreamJSON(data, runID, maxTurns)
	for _, ev := range events {
		if ev.RunID != runID {
			t.Errorf("%s event run id = %q, want %q", ev.Kind, ev.RunID, runID)
		}
	}
	return events
}

func opencode226AssertKinds(t *testing.T, events []runner.RunEvent, want []runner.EventKind) {
	t.Helper()
	got := make([]runner.EventKind, len(events))
	for i, ev := range events {
		got[i] = ev.Kind
	}
	if !slices.Equal(got, want) {
		t.Errorf("public event sequence = %q, want exactly %q (including one terminal outcome)", got, want)
	}
}

func opencode226AssertAnswer(t *testing.T, events []runner.RunEvent, want string) {
	t.Helper()
	var chunks []string
	for _, ev := range events {
		if ev.Kind == runner.EventKindOutput {
			if ev.Output == nil {
				t.Error("assistant output event has no Output payload")
			} else {
				chunks = append(chunks, ev.Output.Text)
			}
		}
	}
	if !slices.Equal(chunks, []string{want}) {
		t.Errorf("assistant answer chunks = %q, want exactly [%q] delivered as output", chunks, want)
	}
}

func opencode226BuildArgsWithWarnings(model string) ([]string, string) {
	var warnings bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&warnings, &slog.HandlerOptions{Level: slog.LevelWarn})))
	defer slog.SetDefault(previous)
	args := runner.BuildOpencodeArgs(runner.RunOptions{Input: "task", Model: model})
	return args, warnings.String()
}
