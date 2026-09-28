// Omnipus — WP-F RED pack, stage A: the Anthropic adapter's thinking-request
// mapping, availability guard, usage wiring and live reasoning callback.
//
// Spec sources (docs/internal/specs/thinking-reasoning-spec.md):
//   - Section 2.2 row "pkg/providers/anthropic/provider.go::parseResponse" and
//     the Adapter coverage matrix row "Anthropic native SDK
//     — buildParams / parseResponse": named effort → provider-native thinking
//     request + named-level mapping; Default omits both (D9/D24).
//   - ADR-095 D7/D18: whenever thinking is requested the adapter pins
//     ThinkingConfigAdaptiveParam.Display = "summarized" — unconditional, never
//     gated by the per-login show_thinking toggle (the toggle gates display,
//     not the upstream request; it never enters the adapter's options at all).
//   - ADR-095 D8.5: when a request would carry thinking enabled but the
//     immediately-preceding assistant tool_use message carries no thinking
//     blocks (pre-feature session, cross-provider fallback switch, hook edit),
//     the adapter omits thinking AND logs the omission. Spec Section 16 test 13:
//     "asserts no thinking config is sent plus one named omission log field".
//     The field name pinned here — `thinking_omitted` — mirrors the adapter's
//     existing snake_case log-field convention (unsupported_media_type_count,
//     budget_tokens, ...).
//   - Section 1 C7: Anthropic OutputTokensDetails.ThinkingTokens →
//     UsageInfo.ThinkingTokens, a subset of CompletionTokens, never added on
//     top; 0 when unreported — never a guessed default.
//   - Section 16 test 13 / BDD "Anthropic always requests the summary": the
//     oracle for every request-shape assertion below is the OUTGOING request
//     body captured at the HTTP boundary of the mock server — never the mock's
//     echo and never an in-process struct only.
//
// Named-level mapping shape (the dispatch asks for "no adaptive/budget
// control"): the spec fixes what that means — US-6 Acceptance Scenario 8 rules
// the BANNED control is "a raw token-budget input, not the effort mapping —
// the named-level presentation that maps to Anthropic's adaptive thinking type
// is not a budget field". ADR-095 D7 names the SDK type explicitly
// (ThinkingConfigAdaptiveParam.Display = "summarized"). anthropic-sdk-go
// v1.48.0's only non-budget thinking config is the adaptive type, and the only
// named-level carrier on MessageNewParams is OutputConfigParam.Effort (the
// same output_config.effort the coverage matrix's Bedrock-Anthropic row maps).
// These tests therefore pin: thinking {type: "adaptive", display:
// "summarized"} + output_config.effort = <named level>, budget_tokens never
// present, and complete omission at Default.
package anthropicprovider

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/elicify-ai/omnipus/pkg/logger"
)

const (
	// reasoningStreamSignature is this pack's fixture signature material. It is
	// chosen so no thinking-text chunk below can ever contain it (assertions
	// lean on that disjointness).
	reasoningStreamSignature = "c2lnbmF0dXJlLW1hdGVyaWFsLW9ubHk="
)

// reasoningFixtureChunks are the thinking display text the fixture streams,
// one thinking_delta per chunk.
var reasoningFixtureChunks = []string{"Chain step one. ", "Chain step two. ", "Chain step three."}

// wireCaptureServer returns a mock Anthropic Messages server that records the
// raw request body delivered to the HTTP boundary and answers with a minimal
// successful non-streaming response.
func wireCaptureServer(t *testing.T, captured *string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading captured request body: %v", err)
			http.Error(w, "read error", http.StatusInternalServerError)
			return
		}
		*captured = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"model":"claude-sonnet-4-6","stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
}

// capturedBodyMap decodes a captured request body into a generic map so
// assertions can address wire keys directly ("the oracle is the wire request").
func capturedBodyMap(t *testing.T, raw string) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("captured request body is not JSON: %v\nbody: %s", err, raw)
	}
	return body
}

// captureFileLog runs run with file logging enabled at DEBUG level into a
// temp file and returns the log lines. This is the public pkg/logger seam
// (EnableFileLogging); no production code is touched. Every level change and
// sink change is restored via t.Cleanup.
func captureFileLog(t *testing.T, run func()) []string {
	t.Helper()

	prevLevel := logger.GetLevel()
	logger.SetLevel(logger.DEBUG)
	t.Cleanup(func() { logger.SetLevel(prevLevel) })

	logPath := filepath.Join(t.TempDir(), "omnipus.log")
	if err := logger.EnableFileLogging(logPath); err != nil {
		t.Fatalf("EnableFileLogging: %v", err)
	}
	t.Cleanup(logger.DisableFileLogging)

	run()

	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("reading captured log file: %v", err)
	}
	var lines []string
	for _, l := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

// logLinesWithField returns the log lines whose record carries key with a true
// value.
func logLinesWithField(t *testing.T, lines []string, key string) []string {
	t.Helper()
	var hits []string
	for _, l := range lines {
		var rec map[string]any
		if err := json.Unmarshal([]byte(l), &rec); err != nil {
			continue
		}
		if b, ok := rec[key].(bool); ok && b {
			hits = append(hits, l)
		}
	}
	return hits
}

// toolUseHistory builds the message tail of a tool-using turn whose assistant
// tool_use message carries NO thinking blocks — the shape a pre-feature
// session presents (ADR-095 D8.5's first guard trigger).
func toolUseHistory() []Message {
	return []Message{
		{Role: "user", Content: "What's the weather?"},
		{
			Role: "assistant",
			ToolCalls: []ToolCall{
				{ID: "call_1", Name: "get_weather", Arguments: map[string]any{"city": "SF"}},
			},
		},
		{Role: "tool", Content: `{"temp":72}`, ToolCallID: "call_1"},
	}
}

// anthropicMessageFromJSON decodes a raw Anthropic Messages API response body
// into the SDK type parseResponse consumes.
func anthropicMessageFromJSON(t *testing.T, raw string) *anthropic.Message {
	t.Helper()
	var msg anthropic.Message
	if err := json.Unmarshal([]byte(raw), &msg); err != nil {
		t.Fatalf("decoding fixture response: %v", err)
	}
	return &msg
}

// --- D9/D24: Default sends no thinking configuration at all -----------------

func TestBuildParams_EffortUnset_SendsNoThinkingAndNoOutputConfig(t *testing.T) {
	for name, options := range map[string]map[string]any{
		"key absent":            {"max_tokens": 1024},
		"empty string":          {"max_tokens": 1024, "reasoning_effort": ""},
		"unset token (default)": {"max_tokens": 1024, "reasoning_effort": "default"},
	} {
		t.Run(name, func(t *testing.T) {
			var captured string
			server := wireCaptureServer(t, &captured)
			defer server.Close()

			p := NewProviderWithBaseURL("test-token", server.URL)
			if _, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, "claude-sonnet-4-6", options); err != nil {
				t.Fatalf("Chat() error = %v", err)
			}

			body := capturedBodyMap(t, captured)
			if _, has := body["thinking"]; has {
				t.Errorf("Default effort: outgoing request carries a %q key = %v, want complete absence (D9/D24)", "thinking", body["thinking"])
			}
			if _, has := body["output_config"]; has {
				t.Errorf("Default effort: outgoing request carries a %q key = %v, want complete absence (D9/D24)", "output_config", body["output_config"])
			}
		})
	}
}

// A non-string reasoning_effort must be treated as absent — the adapter maps
// the plain string the C5 resolver sets (reasoning_effort.go), never guesses.
func TestBuildParams_NonStringEffort_SendsNoThinkingConfig(t *testing.T) {
	var captured string
	server := wireCaptureServer(t, &captured)
	defer server.Close()

	p := NewProviderWithBaseURL("test-token", server.URL)
	if _, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, "claude-sonnet-4-6",
		map[string]any{"max_tokens": 1024, "reasoning_effort": 42}); err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	body := capturedBodyMap(t, captured)
	if _, has := body["thinking"]; has {
		t.Errorf("non-string effort: outgoing request carries %q = %v, want absence", "thinking", body["thinking"])
	}
}

// --- D18: a named level requests adaptive thinking with the summarized pin --

func TestBuildParams_EffortHigh_RequestsAdaptiveThinkingWithSummarizedDisplay(t *testing.T) {
	var captured string
	server := wireCaptureServer(t, &captured)
	defer server.Close()

	p := NewProviderWithBaseURL("test-token", server.URL)
	if _, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, "claude-sonnet-4-6",
		map[string]any{"max_tokens": 1024, "reasoning_effort": "high"}); err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	body := capturedBodyMap(t, captured)

	thinking, ok := body["thinking"].(map[string]any)
	if !ok {
		t.Fatalf("outgoing request %q = %v, want an object (D18: the request asks for thinking whenever a named level is set)", "thinking", body["thinking"])
	}
	if got := thinking["type"]; got != "adaptive" {
		t.Errorf("thinking.type = %v, want %q (the SDK's only non-budget thinking type)", got, "adaptive")
	}
	if got, has := thinking["display"]; !has || got != "summarized" {
		t.Errorf("thinking.display = %v (present=%v), want the explicit %q pin — the summary must be requested on the wire, not inherited from an API default (ADR-095 D7/D18)", got, has, "summarized")
	}

	outputConfig, ok := body["output_config"].(map[string]any)
	if !ok {
		t.Fatalf("outgoing request %q = %v, want an object carrying the named level", "output_config", body["output_config"])
	}
	if got := outputConfig["effort"]; got != "high" {
		t.Errorf("output_config.effort = %v, want %q (the named level reaches the provider request)", got, "high")
	}

	if strings.Contains(captured, "budget_tokens") {
		t.Errorf("outgoing request mentions %q — the raw token-budget mode is banned; named levels only (spec US-6 AS 8)", "budget_tokens")
	}
}

// Each catalog level passes through to its own named effort value.
func TestBuildParams_NamedLevels_CarryTheirOwnEffortValue(t *testing.T) {
	for _, level := range []string{"low", "medium", "high", "xhigh"} {
		t.Run(level, func(t *testing.T) {
			var captured string
			server := wireCaptureServer(t, &captured)
			defer server.Close()

			p := NewProviderWithBaseURL("test-token", server.URL)
			if _, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, "claude-sonnet-4-6",
				map[string]any{"max_tokens": 1024, "reasoning_effort": level}); err != nil {
				t.Fatalf("Chat() error = %v", err)
			}

			body := capturedBodyMap(t, captured)
			outputConfig, ok := body["output_config"].(map[string]any)
			if !ok || outputConfig["effort"] != level {
				t.Errorf("output_config = %v, want effort %q", body["output_config"], level)
			}
			thinking, ok := body["thinking"].(map[string]any)
			if !ok || thinking["display"] != "summarized" {
				t.Errorf("thinking = %v, want display %q for level %q (D18 pin is level-independent)", body["thinking"], "summarized", level)
			}
		})
	}
}

// --- ADR-095 D8.5: the availability guard ------------------------------------

// A thinking-enabled request over a history whose assistant tool_use message
// carries no thinking blocks (a pre-feature session's shape) must OMIT the
// thinking config and log the omission under one named field — not fail the
// turn, not send a request Anthropic would reject.
func TestBuildParams_EffortHigh_PreFeatureAssistantWithoutBlocks_OmitsThinkingAndLogsOmission(t *testing.T) {
	var captured string
	server := wireCaptureServer(t, &captured)
	defer server.Close()

	p := NewProviderWithBaseURL("test-token", server.URL)

	var lines []string
	lines = captureFileLog(t, func() {
		if _, err := p.Chat(t.Context(), toolUseHistory(), nil, "claude-sonnet-4-6",
			map[string]any{"max_tokens": 1024, "reasoning_effort": "high"}); err != nil {
			t.Errorf("Chat() error = %v", err)
		}
	})

	body := capturedBodyMap(t, captured)
	if _, has := body["thinking"]; has {
		t.Errorf("pre-feature history: outgoing request carries %q = %v, want the thinking config omitted (ADR-095 D8.5)", "thinking", body["thinking"])
	}
	if hits := logLinesWithField(t, lines, "thinking_omitted"); len(hits) == 0 {
		t.Errorf("pre-feature history: no log line carries the named omission field %q — the omission must be logged, not silent (spec Section 16 test 13); captured log lines: %d", "thinking_omitted", len(lines))
	}
}

// --- Section 1 C7: usage thinking tokens --------------------------------------

func TestParseResponse_UsageThinkingTokens_WiredFromOutputTokensDetails(t *testing.T) {
	resp := anthropicMessageFromJSON(t, `{
		"id":"msg_1","type":"message","role":"assistant",
		"content":[{"type":"text","text":"done"}],
		"model":"claude-sonnet-4-6","stop_reason":"end_turn",
		"usage":{"input_tokens":10,"output_tokens":50,"output_tokens_details":{"thinking_tokens":42}}
	}`)

	result, err := parseResponse(resp)
	if err != nil {
		t.Fatalf("parseResponse: %v", err)
	}
	if result.Usage == nil {
		t.Fatal("result.Usage is nil, want a populated UsageInfo")
	}
	if result.Usage.ThinkingTokens != 42 {
		t.Errorf("Usage.ThinkingTokens = %d, want 42 (Anthropic OutputTokensDetails.ThinkingTokens, a subset of CompletionTokens)", result.Usage.ThinkingTokens)
	}
	if result.Usage.CompletionTokens != 50 {
		t.Errorf("Usage.CompletionTokens = %d, want 50 — thinking tokens are a subset, never added on top", result.Usage.CompletionTokens)
	}
}

func TestParseResponse_UsageThinkingTokens_AbsentDetailsStayZero(t *testing.T) {
	resp := anthropicMessageFromJSON(t, `{
		"id":"msg_1","type":"message","role":"assistant",
		"content":[{"type":"text","text":"done"}],
		"model":"claude-sonnet-4-6","stop_reason":"end_turn",
		"usage":{"input_tokens":10,"output_tokens":50}
	}`)

	result, err := parseResponse(resp)
	if err != nil {
		t.Fatalf("parseResponse: %v", err)
	}
	if result.Usage.ThinkingTokens != 0 {
		t.Errorf("Usage.ThinkingTokens = %d, want 0 — never a guessed default when the provider does not report one", result.Usage.ThinkingTokens)
	}
}

// --- WP-B's half-finished callback: onReasoning (display text, nil-safe) -----

// reasoningStreamFixture serves the standard thinking-then-answer SSE shape
// with this pack's own signature material. The signature_delta arrives after
// the thinking text, exactly as Anthropic sends it.
func reasoningStreamFixture(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		write := func(s string) {
			if _, err := w.Write([]byte(s)); err != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		write("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\"," +
			"\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-sonnet-4-6\"," +
			"\"stop_reason\":null,\"usage\":{\"input_tokens\":5,\"output_tokens\":0}}}\n\n")
		write("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0," +
			"\"content_block\":{\"type\":\"thinking\",\"thinking\":\"\",\"signature\":\"\"}}\n\n")
		for _, c := range reasoningFixtureChunks {
			b, err := json.Marshal(c)
			if err != nil {
				t.Errorf("marshalling thinking chunk: %v", err)
				return
			}
			write(fmt.Sprintf("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,"+
				"\"delta\":{\"type\":\"thinking_delta\",\"thinking\":%s}}\n\n", b))
		}
		write("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0," +
			"\"delta\":{\"type\":\"signature_delta\",\"signature\":\"" + reasoningStreamSignature + "\"}}\n\n")
		write("event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
		write("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":1," +
			"\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
		write("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1," +
			"\"delta\":{\"type\":\"text_delta\",\"text\":\"Answer.\"}}\n\n")
		write("event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":1}\n\n")
		write("event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}," +
			"\"usage\":{\"output_tokens\":9}}\n\n")
		write("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
}

// Each thinking delta must deliver the ACCUMULATED display text so far — the
// same growth-only contract the text callback has — with one call per delta.
func TestChatStream_OnReasoning_ReceivesAccumulatedDisplayTextPerDelta(t *testing.T) {
	server := reasoningStreamFixture(t)
	defer server.Close()

	p := NewProviderWithBaseURL("test-token", server.URL)

	var got []string
	resp, err := p.ChatStream(
		t.Context(),
		[]Message{{Role: "user", Content: "think then answer"}},
		nil,
		"claude-sonnet-4-6",
		map[string]any{"max_tokens": 1024},
		func(string) {},
		nil,
		func(accumulated string) { got = append(got, accumulated) },
	)
	if err != nil {
		t.Fatalf("ChatStream() error = %v", err)
	}
	if resp.Content != "Answer." {
		t.Errorf("resp.Content = %q, want %q", resp.Content, "Answer.")
	}

	// Oracle: the accumulated prefixes of the fixture's own chunk list.
	want := make([]string, 0, len(reasoningFixtureChunks))
	acc := ""
	for _, c := range reasoningFixtureChunks {
		acc += c
		want = append(want, acc)
	}
	if len(got) != len(want) {
		t.Fatalf("onReasoning delivered %d updates (%v), want exactly %d — one per thinking delta: %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("onReasoning[%d] = %q, want accumulated %q", i, got[i], want[i])
		}
	}
}

// The callback carries DISPLAY TEXT only. The signature arrives in the same
// stream (signature_delta) but must never ride the reasoning callback —
// signatures are captured at response parse only (ADR-095 D7).
func TestChatStream_OnReasoning_NeverCarriesSignatureMaterial(t *testing.T) {
	server := reasoningStreamFixture(t)
	defer server.Close()

	p := NewProviderWithBaseURL("test-token", server.URL)

	var got []string
	_, err := p.ChatStream(
		t.Context(),
		[]Message{{Role: "user", Content: "think then answer"}},
		nil,
		"claude-sonnet-4-6",
		map[string]any{"max_tokens": 1024},
		func(string) {},
		nil,
		func(accumulated string) { got = append(got, accumulated) },
	)
	if err != nil {
		t.Fatalf("ChatStream() error = %v", err)
	}
	if len(got) == 0 {
		t.Fatal("onReasoning was never called — the display-text callback is not implemented; signature-absence is vacuous")
	}
	for i, v := range got {
		if strings.Contains(v, reasoningStreamSignature) {
			t.Fatalf("onReasoning[%d] carries the signature material — signatures are parse-only, never mid-stream (ADR-095 D7): %q", i, v)
		}
	}
}

// Successive reasoning updates must grow as prefixes of one another — the
// consumer computes deltas by slicing the accumulated string, so a reorder or
// shrink panics the turn (the same growth-only rule the text callback has).
func TestChatStream_OnReasoning_GrowsMonotonicallyAsPrefix(t *testing.T) {
	server := reasoningStreamFixture(t)
	defer server.Close()

	p := NewProviderWithBaseURL("test-token", server.URL)

	var got []string
	_, err := p.ChatStream(
		t.Context(),
		[]Message{{Role: "user", Content: "think then answer"}},
		nil,
		"claude-sonnet-4-6",
		map[string]any{"max_tokens": 1024},
		func(string) {},
		nil,
		func(accumulated string) { got = append(got, accumulated) },
	)
	if err != nil {
		t.Fatalf("ChatStream() error = %v", err)
	}
	if len(got) < 2 {
		t.Fatalf("onReasoning delivered %d updates, want ≥2 to demonstrate growth", len(got))
	}
	for i := 1; i < len(got); i++ {
		if !strings.HasPrefix(got[i], got[i-1]) {
			t.Fatalf("onReasoning[%d] = %q is not a prefix-extension of onReasoning[%d] = %q — the accumulated value must only grow", i, got[i], i-1, got[i-1])
		}
	}
}

// A nil onReasoning must be free — the watchdog path (all callbacks nil) must
// stay untouched, and the final response still carries the display text.
func TestChatStream_OnReasoning_NilCallbackDoesNotPanic(t *testing.T) {
	server := reasoningStreamFixture(t)
	defer server.Close()

	p := NewProviderWithBaseURL("test-token", server.URL)

	resp, err := p.ChatStream(
		t.Context(),
		[]Message{{Role: "user", Content: "think then answer"}},
		nil,
		"claude-sonnet-4-6",
		map[string]any{"max_tokens": 1024},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("ChatStream() error = %v", err)
	}
	if resp.Content != "Answer." {
		t.Errorf("resp.Content = %q, want %q", resp.Content, "Answer.")
	}
	if resp.Reasoning == "" {
		t.Errorf("resp.Reasoning is empty, want the accumulated thinking display text even with every callback nil")
	}
}
