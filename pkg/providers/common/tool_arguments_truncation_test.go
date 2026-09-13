// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package common

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// The payloads below are SHAPES OBSERVED IN THE WILD, not invented malformed
// JSON. Each one is what a client actually receives when a generation is cut
// off at the output cap partway through a tool call:
//
//   - `{"query`             a Hermes-dialect call cut mid-key. This is the
//     shape that motivated the fix: vLLM's streaming path
//     marks the choice "tool_calls" with no completeness
//     check and discards the engine's real "length", so
//     the fragment arrives claiming to be a finished call
//     (vllm#47903).
//   - `{`                   the llama.cpp shape (llama.cpp#21771). A client
//     that appends this to history then gets HTTP 500 on
//     every subsequent request — one truncated call wedges
//     the conversation permanently.
//   - `{"path":"a.txt","content":"hello`
//     a realistic write_file cut mid-value: enough parsed
//     to look plausible, still unusable.
//   - `"{\"query"`          the same fault one level up, in the double-
//     encoded form the OpenAI wire format specifies
//     (`arguments` is a JSON-encoded STRING). The outer
//     decode SUCCEEDS here and only the inner one fails,
//     so this covers the second decode pass specifically.
var truncatedArgumentPayloads = []struct {
	name    string
	payload string
}{
	{"hermes fragment cut mid-key", `{"query`},
	{"llama.cpp bare open brace", `{`},
	{"write_file cut mid-value", `{"path":"a.txt","content":"hello`},
	{"double-encoded fragment", `"{\"query"`},
}

// TestDecodeToolCallArguments_TruncatedPayloadRefused is the regression test
// for the silent-degradation gap.
//
// Before the fix every one of these payloads returned a populated map
// carrying a "raw" key and a nil error, so the tool was dispatched with a key
// it does not understand standing in for its real parameters. The assertions
// here fail against that behaviour on all four counts: it returned no error,
// it returned a non-nil map, and that map carried "raw".
func TestDecodeToolCallArguments_TruncatedPayloadRefused(t *testing.T) {
	for _, tc := range truncatedArgumentPayloads {
		t.Run(tc.name, func(t *testing.T) {
			args, err := DecodeToolCallArguments(json.RawMessage(tc.payload), "write_file")

			if err == nil {
				t.Fatalf("payload %q decoded without error; a truncated call must be refused, not dispatched", tc.payload)
			}
			if !errors.Is(err, ErrToolArgumentsUndecodable) {
				t.Errorf("error %v does not wrap ErrToolArgumentsUndecodable", err)
			}
			// The map must be nil, not merely empty: an empty map is still a
			// dispatchable value, and bedrock's old policy proved a caller
			// will happily run a tool with one.
			if args != nil {
				t.Errorf("args = %v, want nil so there is nothing to dispatch", args)
			}
		})
	}
}

// TestDecodeToolCallArguments_NoStandInKeySurvives pins the specific defect
// by name. The old code invented a parameter — "raw" in five places, "_raw"
// in a sixth — and handed it to a tool as if the model had asked for it.
//
// Asserting on the KEYS rather than on the error keeps this test meaningful
// even if the error plumbing is later reshaped: whatever else changes, a
// fabricated parameter must never reach a tool.
func TestDecodeToolCallArguments_NoStandInKeySurvives(t *testing.T) {
	forbidden := []string{"raw", "_raw"}

	for _, tc := range truncatedArgumentPayloads {
		t.Run(tc.name, func(t *testing.T) {
			args, _ := DecodeToolCallArguments(json.RawMessage(tc.payload), "write_file")
			for _, key := range forbidden {
				if _, present := args[key]; present {
					t.Errorf("args carries fabricated parameter %q (= %v); "+
						"a tool must never be dispatched with a stand-in for its arguments",
						key, args[key])
				}
			}
		})
	}
}

// TestDecodeToolCallArguments_AbsentArgumentsAccepted guards the other side
// of the distinction, and is the test that stops the fix from breaking real
// calls.
//
// Zero-parameter tools exist and are common — list_mounts, browser_snapshot,
// and the sysagent's parameterless tools all send nothing at all. "Absent"
// and "present but undecodable" both used to fall through the same branch;
// only the second is a defect. Conflating them would turn every
// zero-parameter tool call into a hard turn failure.
func TestDecodeToolCallArguments_AbsentArgumentsAccepted(t *testing.T) {
	cases := []struct {
		name    string
		payload json.RawMessage
	}{
		{"nil payload", nil},
		{"empty payload", json.RawMessage(``)},
		{"whitespace payload", json.RawMessage(`   `)},
		{"explicit null", json.RawMessage(`null`)},
		{"empty object", json.RawMessage(`{}`)},
		{"empty encoded string", json.RawMessage(`""`)},
		{"whitespace encoded string", json.RawMessage(`"  "`)},
		{"encoded empty object", json.RawMessage(`"{}"`)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args, err := DecodeToolCallArguments(tc.payload, "list_mounts")
			if err != nil {
				t.Fatalf("absent arguments must decode cleanly for a zero-parameter tool, got: %v", err)
			}
			if args == nil {
				t.Fatal("args = nil, want an empty non-nil map a tool can be dispatched with")
			}
			if len(args) != 0 {
				t.Errorf("args = %v, want empty", args)
			}
		})
	}
}

// TestDecodeToolCallArguments_NonObjectRefused covers valid JSON of the wrong
// shape — a bare array, number or boolean where named parameters belong.
// These are not truncation, but they are equally undispatchable, and the old
// code funnelled them into the same "raw" stand-in.
func TestDecodeToolCallArguments_NonObjectRefused(t *testing.T) {
	for _, payload := range []string{`[1,2,3]`, `42`, `true`, `"plain string"`} {
		t.Run(payload, func(t *testing.T) {
			args, err := DecodeToolCallArguments(json.RawMessage(payload), "write_file")
			if err == nil {
				t.Fatalf("payload %q is not a JSON object and must be refused", payload)
			}
			if !errors.Is(err, ErrToolArgumentsUndecodable) {
				t.Errorf("error %v does not wrap ErrToolArgumentsUndecodable", err)
			}
			if args != nil {
				t.Errorf("args = %v, want nil", args)
			}
		})
	}
}

// TestToolArgumentsErrorIsDiagnostic requires the error to carry the two
// things an operator needs to tell truncation apart from malformation: which
// tool was being called, and the actual fragment that arrived.
//
// Without the fragment the message is indistinguishable from an ordinary
// schema complaint, which is precisely the confusion the fix exists to end.
func TestToolArgumentsErrorIsDiagnostic(t *testing.T) {
	_, err := DecodeToolCallArguments(json.RawMessage(`{"query`), "web_search")
	if err == nil {
		t.Fatal("expected an error")
	}

	// Assert on Body, not Error(). Body is the canonical message and the
	// string the classifier actually reads; Error() wraps it in the
	// provider-error envelope and re-quotes it, which escapes the fragment a
	// second time and makes a substring match on it meaningless.
	var pe *ProviderError
	if !errors.As(err, &pe) {
		t.Fatalf("error is not a *ProviderError: %v", err)
	}

	if !strings.Contains(pe.Body, "web_search") {
		t.Errorf("error does not name the tool: %s", pe.Body)
	}
	if !strings.Contains(pe.Body, `{\"query`) {
		t.Errorf("error does not quote the offending fragment: %s", pe.Body)
	}
}

// TestToolArgumentsErrorClassifiesAsToolArgs pins the contract with the agent
// loop's error classifier WITHOUT importing pkg/agent (which would be an
// import cycle).
//
// Two independent things are asserted, both of which have bitten before:
//
//  1. The error must be a *ProviderError with a populated Body. pkg/agent's
//     errorToProviderError synthesises an EMPTY *ProviderError for anything
//     that is not already one, and then classifies on that empty Body — so a
//     bare fmt.Errorf reaches the user as generic "unknown error" copy no
//     matter how well it is worded.
//
//  2. Status must be 0. A non-zero status would send the classifier down the
//     HTTP ladder, and there was no failing HTTP request here: the upstream
//     returned a healthy 200 whose content was cut off.
func TestToolArgumentsErrorClassifiesAsToolArgs(t *testing.T) {
	_, err := DecodeToolCallArguments(json.RawMessage(`{"query`), "web_search")
	if err == nil {
		t.Fatal("expected an error")
	}

	var pe *ProviderError
	if !errors.As(err, &pe) {
		t.Fatalf("error is not a *ProviderError, so pkg/agent will classify it as CodeUnknown: %v", err)
	}
	if pe.Status != 0 {
		t.Errorf("Status = %d, want 0 — no HTTP request failed", pe.Status)
	}
	// "invalid tool arguments" is the substring pinned by ADR-051 Rev 4
	// FR-018 (translate_error.go's toolArgsSubstrings). Matching is
	// case-insensitive there; assert on the lowered form.
	if !strings.Contains(strings.ToLower(pe.Body), "invalid tool arguments") {
		t.Errorf("Body %q lacks the pinned CodeToolArgs substring %q", pe.Body, "invalid tool arguments")
	}
}

// TestToolArgumentsErrorAvoidsClassifierTraps stops a future reword from
// silently re-labelling this fault as something else.
//
// The classifier matches on substrings, so an innocuous-looking wording
// change can hijack the classification:
//
//   - "token limit" / "too many tokens" → contextOverflowPatterns, which
//     would report a context-window overflow. That is a different fault with
//     different advice (shorten the conversation, not the call).
//   - "timeout" / "timed out" / "connection closed" / "unexpected eof" →
//     timeout and connectionDropPatterns, which are RETRIABLE. That would put
//     a deterministic, non-transient fault onto the inline retry arm and
//     re-issue the same oversized call up to three times.
func TestToolArgumentsErrorAvoidsClassifierTraps(t *testing.T) {
	_, err := DecodeToolCallArguments(json.RawMessage(`{"query`), "web_search")
	if err == nil {
		t.Fatal("expected an error")
	}
	msg := strings.ToLower(err.Error())

	for _, trap := range []string{
		"token limit", "too many tokens", "maximum context length", "request too large",
		"timeout", "timed out", "deadline exceeded",
		"connection closed", "connection reset by peer", "unexpected eof", "broken pipe",
	} {
		if strings.Contains(msg, trap) {
			t.Errorf("error message contains classifier trap %q, which would mis-label this fault: %s", trap, msg)
		}
	}
}

// TestToolArgumentsErrorCapsQuotedFragment checks the quoted fragment is
// bounded. The fragment is the diagnostic, but a runaway or hostile payload
// must not flood a log line, and the message must say how much it cut so the
// reader knows it is looking at a prefix.
func TestToolArgumentsErrorCapsQuotedFragment(t *testing.T) {
	// Unterminated string literal: long, and genuinely undecodable.
	oversized := `{"content":"` + strings.Repeat("A", 4096)

	_, err := DecodeToolCallArguments(json.RawMessage(oversized), "write_file")
	if err == nil {
		t.Fatal("expected an error")
	}
	msg := err.Error()

	if len(msg) > maxUndecodableArgumentsQuoted*2 {
		t.Errorf("error message is %d bytes, want it bounded near the %d-byte quote cap",
			len(msg), maxUndecodableArgumentsQuoted)
	}
	if !strings.Contains(msg, "bytes total") {
		t.Errorf("truncated quote must report the true payload length: %s", msg)
	}
}

// TestParseResponse_RefusesResponseWithTruncatedToolCall exercises the
// refusal through the real non-streaming entry point, on a body shaped like
// an actual OpenAI-compatible reply.
//
// Note what the fixture asserts beyond the error: the response also contains
// a perfectly VALID tool call, and the whole response is still refused. That
// is deliberate. A reply cut off mid-call is incomplete as a whole, so the
// calls that did parse are a partial view of a plan the model never finished
// expressing — running them commits side effects for a decision that was
// never fully stated. It also keeps the fragment out of session history,
// where pkg/agent appends the assistant message BEFORE dispatching, and from
// which it would be re-sent on every subsequent request.
func TestParseResponse_RefusesResponseWithTruncatedToolCall(t *testing.T) {
	body := `{
	  "choices": [{
	    "message": {
	      "content": "",
	      "tool_calls": [
	        {"id":"call_1","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"ok.txt\"}"}},
	        {"id":"call_2","type":"function","function":{"name":"write_file","arguments":"{\"query"}}
	      ]
	    },
	    "finish_reason": "tool_calls"
	  }],
	  "usage": {"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}
	}`

	resp, err := ParseResponse(strings.NewReader(body))
	if err == nil {
		t.Fatalf("ParseResponse accepted a response carrying a truncated tool call: %+v", resp)
	}
	if !errors.Is(err, ErrToolArgumentsUndecodable) {
		t.Errorf("error %v does not wrap ErrToolArgumentsUndecodable", err)
	}
	if resp != nil {
		t.Errorf("resp = %+v, want nil so no tool call from a truncated reply can be dispatched", resp)
	}
}

// TestToolArgumentsError_TruncatedOnlyWithEvidence pins the ADR-087 D5
// Truncated rule directly: true iff the finish reason is one of
// length/max_tokens/truncated, OR the refused fragment is the unclosed
// prefix of a JSON object — and a well-formed non-object (`42`, `true`,
// `[1]`) is NEVER truncated by shape alone, though an explicit truncating
// finish reason still wins over it (the finish reason is real evidence the
// generation was cut off; the model's LAST thing said just happened to
// still be valid JSON).
func TestToolArgumentsError_TruncatedOnlyWithEvidence(t *testing.T) {
	cases := []struct {
		name          string
		finishReason  string
		fragment      string
		wantTruncated bool
	}{
		{"finish length + EOF-shaped fragment", "length", `{"q`, true},
		{"finish stop + EOF-shaped fragment: shape alone is enough", "stop", `{"q`, true},
		{"finish stop + well-formed non-object: never truncated by shape", "stop", `42`, false},
		{"finish length + well-formed non-object: finish reason wins", "length", `42`, true},
		{"finish tool_calls + bare open brace: shape alone is enough", "tool_calls", `{`, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DecodeToolCallArguments(json.RawMessage(tc.fragment), "write_file")
			if err == nil {
				t.Fatalf("payload %q was accepted, want a refusal to evaluate", tc.fragment)
			}
			err = AttachToolArgumentsEvidence(err, tc.finishReason, nil)

			var tae *ToolArgumentsError
			if !errors.As(err, &tae) {
				t.Fatalf("error is not a *ToolArgumentsError: %v", err)
			}
			if tae.Truncated != tc.wantTruncated {
				t.Errorf("Truncated = %v, want %v (finish_reason=%q fragment=%q)",
					tae.Truncated, tc.wantTruncated, tc.finishReason, tc.fragment)
			}
		})
	}
}

// TestParseResponse_AcceptsZeroParameterToolCall is the companion guard: the
// same entry point must still accept a reply whose tool call legitimately
// carries no arguments, including the `null` spelling some providers emit.
func TestParseResponse_AcceptsZeroParameterToolCall(t *testing.T) {
	body := `{
	  "choices": [{
	    "message": {
	      "content": "",
	      "tool_calls": [
	        {"id":"call_1","type":"function","function":{"name":"list_mounts","arguments":"{}"}},
	        {"id":"call_2","type":"function","function":{"name":"browser_snapshot","arguments":null}}
	      ]
	    },
	    "finish_reason": "tool_calls"
	  }],
	  "usage": {"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}
	}`

	resp, err := ParseResponse(strings.NewReader(body))
	if err != nil {
		t.Fatalf("zero-parameter tool calls must still parse, got: %v", err)
	}
	if len(resp.ToolCalls) != 2 {
		t.Fatalf("got %d tool calls, want 2", len(resp.ToolCalls))
	}
	for _, tc := range resp.ToolCalls {
		if tc.Arguments == nil {
			t.Errorf("tool %q: Arguments = nil, want an empty non-nil map", tc.Name)
		}
		if len(tc.Arguments) != 0 {
			t.Errorf("tool %q: Arguments = %v, want empty", tc.Name, tc.Arguments)
		}
	}
}
