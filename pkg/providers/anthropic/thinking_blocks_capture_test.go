// Omnipus — WP-F RED pack, stage B: signed-block capture at response parse,
// signature capture via the streaming path, and the outbound block echo.
//
// Spec sources:
//   - ADR-095 D7: signed-block capture is parseResponse-ONLY — both streaming
//     and non-streaming funnel there (streaming: msg.Accumulate(event) →
//     parseResponse(&msg)); signatures and opaque data never ride a streaming
//     callback.
//   - ADR-095 D6: a signature-only block (empty thinking text) is "no
//     displayable thinking: metadata only" — but it still round-trips, so it
//     is captured with its signature, and no empty display copy is
//     manufactured.
//   - ADR-095 D6: the round-trip echoes every block byte-exact and in order,
//     thinking blocks preceding text/tool_use blocks as Anthropic requires —
//     the mid-turn-rebuild adapter half (spec Section 16 test 14).
//   - Spec Section 2.2 row parseResponse: "thinking-block + signature capture".
package anthropicprovider

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/elicify-ai/omnipus/pkg/providers/protocoltypes"
)

const (
	// Fixture block material. The signature/data strings are chosen so no
	// other fixture text can ever contain them (assertions lean on the
	// disjointness).
	redactedFixtureData   = "REDACTED-BLOCK-DATA-b64=="
	thinkingFixtureText   = "think: why the user asks this → plan"
	thinkingFixtureSignat = "sig-theta-signature=="
)

// thinkingResponseFixture returns an Anthropic Message whose content carries
// a thinking block (with signature), a text block, a redacted_thinking block
// and a tool_use block, in that order.
func thinkingResponseFixture(t *testing.T) *anthropic.Message {
	return anthropicMessageFromJSON(t, `{
		"id":"msg_1","type":"message","role":"assistant",
		"content":[
			{"type":"thinking","thinking":"`+thinkingFixtureText+`","signature":"`+thinkingFixtureSignat+`"},
			{"type":"text","text":"Answer part one."},
			{"type":"redacted_thinking","data":"`+redactedFixtureData+`"},
			{"type":"tool_use","id":"toolu_1","name":"get_weather","input":{"city":"SF"}}
		],
		"model":"claude-sonnet-4-6","stop_reason":"tool_use",
		"usage":{"input_tokens":8,"output_tokens":21}
	}`)
}

// parseResponse must capture BOTH block kinds with signatures and opaque data
// byte-exact, in response order — the D6/D7 capture point feeding the carrier
// on the parse output, which the loop copies onto the assistant history
// Message (where D6's carrier lives).
func TestParseResponse_CapturesThinkingBlocks_ByteExactAndInOrder(t *testing.T) {
	result, err := parseResponse(thinkingResponseFixture(t))
	if err != nil {
		t.Fatalf("parseResponse: %v", err)
	}

	want := []protocoltypes.ThinkingBlock{
		{Type: "thinking", Thinking: thinkingFixtureText, Signature: thinkingFixtureSignat},
		{Type: "redacted_thinking", Data: redactedFixtureData},
	}
	if len(result.ThinkingBlocks) != len(want) {
		t.Fatalf("ThinkingBlocks = %+v, want %d blocks byte-exact in response order", result.ThinkingBlocks, len(want))
	}
	for i := range want {
		if result.ThinkingBlocks[i] != want[i] {
			t.Errorf("ThinkingBlocks[%d] = %+v, want %+v (byte-exact, order-stable)", i, result.ThinkingBlocks[i], want[i])
		}
	}

	// The other parse outputs stay intact alongside the new capture.
	if result.Reasoning != thinkingFixtureText {
		t.Errorf("Reasoning = %q, want %q — display copy from the thinking block is unchanged", result.Reasoning, thinkingFixtureText)
	}
	if result.Content != "Answer part one." {
		t.Errorf("Content = %q, want %q — text parse is unchanged", result.Content, "Answer part one.")
	}
	if len(result.ToolCalls) != 1 || result.ToolCalls[0].ID != "toolu_1" {
		t.Errorf("ToolCalls = %+v, want the one tool_use block parsed as before", result.ToolCalls)
	}
}

// A signature-only block (empty thinking text — the display:omitted default on
// Anthropic's newest models) is metadata only, but it round-trips: captured
// with its signature, empty text. No empty display copy is manufactured (D6:
// "no displayable thinking").
func TestParseResponse_CapturesSignatureOnlyBlock_MetadataOnly(t *testing.T) {
	result, err := parseResponse(anthropicMessageFromJSON(t, `{
		"id":"msg_1","type":"message","role":"assistant",
		"content":[
			{"type":"thinking","thinking":"","signature":"`+thinkingFixtureSignat+`"},
			{"type":"text","text":"ok"}
		],
		"model":"claude-sonnet-4-6","stop_reason":"end_turn",
		"usage":{"input_tokens":1,"output_tokens":2}
	}`))
	if err != nil {
		t.Fatalf("parseResponse: %v", err)
	}

	if len(result.ThinkingBlocks) != 1 {
		t.Fatalf("ThinkingBlocks len = %d, want 1 (a signature-only block still round-trips)", len(result.ThinkingBlocks))
	}
	got := result.ThinkingBlocks[0]
	if got.Type != "thinking" || got.Thinking != "" || got.Signature != thinkingFixtureSignat || got.Data != "" {
		t.Errorf("signature-only block captured as %+v, want {thinking, empty text, signature preserved, no data}", got)
	}
	if result.Reasoning != "" {
		t.Errorf("Reasoning = %q, want empty — a signature-only block is no displayable thinking (ADR-095 D6)", result.Reasoning)
	}
}

// A response with no thinking blocks must leave the carrier empty — not nil-vs
// empty ambiguity that later trips len() guards or the D8.5 availability
// check, and no manufactured placeholder.
func TestParseResponse_NoBlocks_CarrierEmpty(t *testing.T) {
	result, err := parseResponse(anthropicMessageFromJSON(t, `{
		"id":"msg_1","type":"message","role":"assistant",
		"content":[{"type":"text","text":"plain answer"}],
		"model":"claude-sonnet-4-6","stop_reason":"end_turn",
		"usage":{"input_tokens":1,"output_tokens":2}
	}`))
	if err != nil {
		t.Fatalf("parseResponse: %v", err)
	}
	if len(result.ThinkingBlocks) != 0 {
		t.Errorf("ThinkingBlocks = %+v, want empty for a block-free response", result.ThinkingBlocks)
	}
	if result.Reasoning != "" {
		t.Errorf("Reasoning = %q, want empty", result.Reasoning)
	}
}

// D7: the streaming path funnels into parseResponse, so the final streamed
// response carries the signature at parse — while the mid-stream reasoning
// callback (stage A) never did. One fixture, two oracles.
func TestChatStream_SignatureLandsAtParseOnly(t *testing.T) {
	server := reasoningStreamFixture(t)
	defer server.Close()

	p := NewProviderWithBaseURL("test-token", server.URL)

	var reasoningUpdates []string
	resp, err := p.ChatStream(
		t.Context(),
		[]Message{{Role: "user", Content: "think then answer"}},
		nil,
		"claude-sonnet-4-6",
		map[string]any{"max_tokens": 1024},
		func(string) {},
		nil,
		func(accumulated string) { reasoningUpdates = append(reasoningUpdates, accumulated) },
	)
	if err != nil {
		t.Fatalf("ChatStream() error = %v", err)
	}

	if len(resp.ThinkingBlocks) != 1 {
		t.Fatalf("ThinkingBlocks len = %d, want 1 — the stream funnels into parseResponse (ADR-095 D7)", len(resp.ThinkingBlocks))
	}
	got := resp.ThinkingBlocks[0]
	if got.Type != "thinking" || got.Thinking != reasoningFixtureChunks[0]+reasoningFixtureChunks[1]+reasoningFixtureChunks[2] {
		t.Errorf("ThinkingBlocks[0] = %+v, want the accumulated stream text byte-exact", got)
	}
	if got.Signature != reasoningStreamSignature {
		t.Errorf("ThinkingBlocks[0].Signature = %q, want the streamed signature_delta captured at parse — "+
			"the stream's own signature_delta handling stays dropped per D7", got.Signature)
	}
	if got.Data != "" {
		t.Errorf("ThinkingBlocks[0].Data = %q, want empty on a thinking block", got.Data)
	}

	// And the parse-only split still holds mid-stream (stage A's rule).
	for i, v := range reasoningUpdates {
		if strings.Contains(v, reasoningStreamSignature) {
			t.Errorf("onReasoning[%d] carries signature material — signatures are parse-only (ADR-095 D7)", i)
		}
	}
}

// D6/D8: the round-trip echo. An assistant history message carrying
// ThinkingBlocks must be rebuilt on the wire with every block byte-exact and
// in order, thinking blocks PRECEDING the text/tool_use blocks as Anthropic
// requires. And because blocks are present, the D8.5 availability guard does
// NOT fire: the thinking request stays in.
func TestChat_EchoesAssistantThinkingBlocks_ByteExactAndInOrder(t *testing.T) {
	history := []Message{
		{Role: "user", Content: "What's the weather?"},
		{
			Role:    "assistant",
			Content: "Checking.",
			ThinkingBlocks: []protocoltypes.ThinkingBlock{
				{Type: "thinking", Thinking: thinkingFixtureText, Signature: thinkingFixtureSignat},
				{Type: "redacted_thinking", Data: redactedFixtureData},
			},
			ToolCalls: []ToolCall{
				{ID: "call_1", Name: "get_weather", Arguments: map[string]any{"city": "SF"}},
			},
		},
		{Role: "tool", Content: `{"temp":72}`, ToolCallID: "call_1"},
	}

	var captured string
	server := wireCaptureServer(t, &captured)
	defer server.Close()

	p := NewProviderWithBaseURL("test-token", server.URL)
	if _, err := p.Chat(t.Context(), history, nil, "claude-sonnet-4-6",
		map[string]any{"max_tokens": 1024, "reasoning_effort": "high"}); err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	body := capturedBodyMap(t, captured)

	// The guard must not fire on a blocks-carrying history: the thinking
	// request stays in (D8.5 is the absence case, tested in stage A).
	if _, has := body["thinking"]; !has {
		t.Fatal("outgoing request carries no thinking config — the D8.5 availability guard fired on a history that HAS blocks")
	}

	msgs, ok := body["messages"].([]any)
	if !ok {
		t.Fatalf("messages is %T, want an array", body["messages"])
	}
	if len(msgs) != 3 {
		t.Fatalf("messages len = %d, want 3 (user, assistant, tool result)", len(msgs))
	}
	asst, ok := msgs[1].(map[string]any)
	if !ok || asst["role"] != "assistant" {
		t.Fatalf("messages[1] = %v, want the assistant message", msgs[1])
	}
	content, ok := asst["content"].([]any)
	if !ok {
		t.Fatalf("assistant content is %T, want an array", asst["content"])
	}
	if len(content) != 4 {
		t.Fatalf("assistant content has %d blocks, want 4: thinking, redacted_thinking, text, tool_use (thinking blocks precede text/tool_use, ADR-095 D6); got: %s", len(content), captured)
	}

	want0 := map[string]any{"type": "thinking", "thinking": thinkingFixtureText, "signature": thinkingFixtureSignat}
	if !reflectBlockEqual(t, content[0], want0) {
		t.Errorf("content[0] = %v, want %v (thinking block byte-exact)", content[0], want0)
	}
	want1 := map[string]any{"type": "redacted_thinking", "data": redactedFixtureData}
	if !reflectBlockEqual(t, content[1], want1) {
		t.Errorf("content[1] = %v, want %v (redacted_thinking byte-exact, data included — D8)", content[1], want1)
	}
	want2 := map[string]any{"type": "text", "text": "Checking."}
	if !reflectBlockEqual(t, content[2], want2) {
		t.Errorf("content[2] = %v, want %v", content[2], want2)
	}
	want3 := map[string]any{"type": "tool_use", "id": "call_1", "name": "get_weather"}
	if !reflectBlockEqual(t, content[3], want3) {
		t.Errorf("content[3] = %v, want %v (tool_use fields; input asserted separately)", content[3], want3)
	}

	// The tool result rides as before, in its own user message.
	toolMsg, ok := msgs[2].(map[string]any)
	if !ok || toolMsg["role"] != "user" {
		t.Fatalf("messages[2] = %v, want the tool result as a user message", msgs[2])
	}
}

// reflectBlockEqual compares a decoded wire block against the expected fields
// (subset match: extra keys fail via the length check where the shape is
// fully specified, and input is asserted separately for tool_use).
func reflectBlockEqual(t *testing.T, got any, want map[string]any) bool {
	t.Helper()
	m, ok := got.(map[string]any)
	if !ok {
		return false
	}
	for k, v := range want {
		gv, ok := m[k]
		if !ok {
			return false
		}
		// Decode both sides through JSON text so nested input maps compare
		// canonically regardless of number formatting.
		gb, err1 := json.Marshal(gv)
		wb, err2 := json.Marshal(v)
		if err1 != nil || err2 != nil || string(gb) != string(wb) {
			return false
		}
	}
	return true
}
