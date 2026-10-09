package session

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

// populatedMessage is a fully-populated admitted provider message: every field
// a real turn can carry, including the three providers.ToolCall members the
// provider type tags json:"-" (Name, Arguments, ThoughtSignature) and the
// separate raw function-argument string. session-core C-ARCHIVE / U2 Decision A.
func populatedMessage() providers.Message {
	return providers.Message{
		Role:             "assistant",
		Content:          "calling a tool",
		Media:            []string{"media://a.png", "media://b.png"},
		ReasoningContent: "the model reasoned thus",
		SystemParts: []providers.ContentBlock{
			{Type: "text", Text: "sys one", CacheControl: &providers.CacheControl{Type: "ephemeral"}},
			{Type: "text", Text: "sys two"},
		},
		ToolCalls: []providers.ToolCall{
			{
				ID:               "call_0",
				Type:             "function",
				Name:             "read_file",
				ThoughtSignature: "sig-from-json-dash",
				Arguments:        map[string]any{"path": "/tmp/x", "count": float64(2)},
				Function: &providers.FunctionCall{
					Name:             "read_file",
					Arguments:        `{"path":"/tmp/x","count":2}`,
					ThoughtSignature: "func-sig",
				},
				ExtraContent: &providers.ExtraContent{
					Google: &providers.GoogleExtra{ThoughtSignature: "gemini-opaque-sig"},
				},
			},
		},
	}
}

// TestModelPayload_RoundTripPreservesProviderOnlyMembers is the core Decision A
// assertion: a decode(encode(msg)) reproduces EVERY field, including the three
// members the provider struct tags json:"-". A struct-tag-only codec would drop
// Name/Arguments/ThoughtSignature and fail here.
func TestModelPayload_RoundTripPreservesProviderOnlyMembers(t *testing.T) {
	orig := populatedMessage()
	enc, err := EncodeModelPayload(orig)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	dec, err := DecodeModelPayload(enc)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !reflect.DeepEqual(dec, orig) {
		t.Fatalf("round trip mismatch\n got: %#v\nwant: %#v", dec, orig)
	}
	// The provider-only members must be present, not zeroed.
	tc := dec.ToolCalls[0]
	if tc.Name != "read_file" || tc.ThoughtSignature != "sig-from-json-dash" {
		t.Errorf("provider-only ToolCall.Name/ThoughtSignature lost: %#v", tc)
	}
	if tc.Arguments["path"] != "/tmp/x" {
		t.Errorf("provider-only ToolCall.Arguments lost: %#v", tc.Arguments)
	}
}

// TestModelPayload_SurvivesDiskJSONRoundTrip proves the persisted JSON shape is
// itself lossless: encoding to bytes and back (what the archive does) preserves
// every field through DecodeModelPayload.
func TestModelPayload_SurvivesDiskJSONRoundTrip(t *testing.T) {
	orig := populatedMessage()
	enc, err := EncodeModelPayload(orig)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	raw, err := json.Marshal(enc)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	var onDisk ModelPayload
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	dec, err := DecodeModelPayload(onDisk)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !reflect.DeepEqual(dec, orig) {
		t.Fatalf("disk round trip mismatch\n got: %#v\nwant: %#v", dec, orig)
	}
}

// TestModelPayload_PreservesArgumentRepresentationsIndependently: the raw
// Function.Arguments string and the parsed Arguments object are two distinct
// values and neither may be derived from or normalized into the other.
func TestModelPayload_PreservesArgumentRepresentationsIndependently(t *testing.T) {
	// A raw string whose formatting the parsed map cannot reproduce.
	const rawString = `{"b":2, "a":1}`
	msg := providers.Message{
		Role:    "assistant",
		Content: "",
		ToolCalls: []providers.ToolCall{{
			ID:        "call_0",
			Name:      "t",
			Arguments: map[string]any{"a": float64(1), "b": float64(2)},
			Function:  &providers.FunctionCall{Name: "t", Arguments: rawString},
		}},
	}
	enc, err := EncodeModelPayload(msg)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if enc.ToolCalls[0].Function.Arguments != rawString {
		t.Errorf("raw Function.arguments string not preserved verbatim: %q", enc.ToolCalls[0].Function.Arguments)
	}
	dec, err := DecodeModelPayload(enc)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if dec.ToolCalls[0].Function.Arguments != rawString {
		t.Errorf("raw Function.arguments string lost on decode: %q", dec.ToolCalls[0].Function.Arguments)
	}
	if !reflect.DeepEqual(dec.ToolCalls[0].Arguments, map[string]any{"a": float64(1), "b": float64(2)}) {
		t.Errorf("parsed arguments lost: %#v", dec.ToolCalls[0].Arguments)
	}
}

// TestModelPayload_EmptyButValidContentPreserved: a role "tool" message with an
// empty content string is structurally valid and must survive as "empty", not
// be dropped or fabricated.
func TestModelPayload_EmptyButValidContentPreserved(t *testing.T) {
	msg := providers.Message{Role: "tool", Content: "", ToolCallID: "call_0"}
	enc, err := EncodeModelPayload(msg)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	dec, err := DecodeModelPayload(enc)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if dec.Role != "tool" || dec.Content != "" || dec.ToolCallID != "call_0" {
		t.Fatalf("empty-but-valid content/intent lost: %#v", dec)
	}
}

// TestModelPayload_OrderPreserved: media and system_parts array order is part
// of the value and must not be reordered.
func TestModelPayload_OrderPreserved(t *testing.T) {
	msg := populatedMessage()
	enc, err := EncodeModelPayload(msg)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	dec, err := DecodeModelPayload(enc)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if dec.Media[0] != "media://a.png" || dec.Media[1] != "media://b.png" {
		t.Errorf("media order not preserved: %#v", dec.Media)
	}
	if dec.SystemParts[0].Text != "sys one" || dec.SystemParts[1].Text != "sys two" {
		t.Errorf("system_parts order not preserved: %#v", dec.SystemParts)
	}
	if dec.SystemParts[0].CacheControl == nil || dec.SystemParts[0].CacheControl.Type != "ephemeral" {
		t.Errorf("cache_control value not preserved: %#v", dec.SystemParts[0].CacheControl)
	}
	if dec.SystemParts[1].CacheControl != nil {
		t.Errorf("absent cache_control must stay absent: %#v", dec.SystemParts[1].CacheControl)
	}
}

// TestModelPayload_NilOptionalStaysNil: absent optional members must not be
// fabricated into empty slices/maps.
func TestModelPayload_NilOptionalStaysNil(t *testing.T) {
	msg := providers.Message{Role: "user", Content: "hi"}
	enc, err := EncodeModelPayload(msg)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if enc.Media != nil || enc.SystemParts != nil || enc.ToolCalls != nil {
		t.Fatalf("nil optional members became non-nil: %#v", enc)
	}
	dec, err := DecodeModelPayload(enc)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if dec.Media != nil || dec.SystemParts != nil || dec.ToolCalls != nil {
		t.Fatalf("nil optional members fabricated on decode: %#v", dec)
	}
}

// TestModelPayload_EncodeFailsVisiblyOnNonJSONArguments: a parsed Arguments map
// holding a value JSON cannot represent is a visible encode error, never a
// silent drop.
func TestModelPayload_EncodeFailsVisiblyOnNonJSONArguments(t *testing.T) {
	msg := providers.Message{
		Role: "assistant",
		ToolCalls: []providers.ToolCall{{
			ID:        "call_0",
			Arguments: map[string]any{"bad": func() {}},
		}},
	}
	if _, err := EncodeModelPayload(msg); err == nil {
		t.Fatal("expected an encode error for non-JSON arguments, got nil")
	}
}

// TestModelPayload_DecodeFailsVisiblyOnInvalidStoredArguments: a stored payload
// whose Arguments bytes are not a JSON object fails visibly rather than
// substituting {}.
func TestModelPayload_DecodeFailsVisiblyOnInvalidStoredArguments(t *testing.T) {
	enc := ModelPayload{
		Role: "assistant",
		ToolCalls: []ModelToolCall{{
			ID:        "call_0",
			Arguments: json.RawMessage(`not-json`),
		}},
	}
	if _, err := DecodeModelPayload(enc); err == nil {
		t.Fatal("expected a decode error for invalid stored arguments, got nil")
	}
}

// TestModelPayload_DecodeFailsVisiblyOnNonObjectArguments: a JSON array or
// scalar is not the map shape the provider expects; it must fail, not coerce.
func TestModelPayload_DecodeFailsVisiblyOnNonObjectArguments(t *testing.T) {
	enc := ModelPayload{
		Role: "assistant",
		ToolCalls: []ModelToolCall{{
			ID:        "call_0",
			Arguments: json.RawMessage(`[1,2,3]`),
		}},
	}
	if _, err := DecodeModelPayload(enc); err == nil {
		t.Fatal("expected a decode error for a non-object arguments value, got nil")
	}
}
