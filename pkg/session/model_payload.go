// model_payload.go: the PRIVATE, lossless persistence representation of one
// admitted providers.Message on the single append-only session archive
// (session-core C-ARCHIVE / U2, Decision A; FR-004).
//
// Decision A's rule is "use an explicit lossless codec, not default provider
// JSON tags or UI-call conversion". providers.ToolCall carries three
// json:"-" members (Name, Arguments, ThoughtSignature) that the default
// serializer silently drops, and providers.Message has no field for the raw
// function-argument STRING kept beside the parsed Arguments map. A faithful
// round trip therefore cannot be `json.Marshal(providers.Message)` — it needs
// a typed shape that tags every member explicitly, plus explicit Encode/Decode
// functions that fail loudly instead of silently substituting a zero value.
//
// SCOPE (deliberately narrow — this is a data-shape foundation, not the
// cutover): this file defines the private payload object and its codec only.
// It is disk-only and MUST NEVER cross the gateway/SPA boundary: the public
// Message/ToolCall/replay wire shapes stay generated CHAT PROJECTIONS
// (contracts/components/schemas/Message.yaml, ToolCall.yaml,
// ReplayMessageFrame.yaml). Attaching ModelPayload to TranscriptEntry, the
// DEL-12 one-backend read/write cutover (`pkg/session/unified.go::newUnifiedStore`
// no longer creating `.context`), the addressed bounded reads (Decision C) and
// the faithful CONV publication (Decision D) are separate, coupled steps.
package session

import (
	"encoding/json"
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

// ModelPayload is the lossless, private, disk-only representation of one
// admitted providers.Message. "Lossless" here means preserved field values,
// array order, string bytes and JSON values — not Go pointer identity or map
// iteration order. Nil/absent optional members stay absent rather than being
// fabricated into empty content or signatures.
//
// Field source: pkg/providers/protocoltypes/types.go::Message / ToolCall /
// FunctionCall / ExtraContent / GoogleExtra / ContentBlock / CacheControl.
// This is an internal persistence contract, not a wire type — it is NOT added
// to contracts/ and never appears in the REST/WS/SPA projection.
type ModelPayload struct {
	// Role and Content match the admitted providers.Message, including
	// role "tool" and an empty content string when structurally valid.
	// Content is preserved as text, never regenerated from a UI result map,
	// narration or status.
	Role    string `json:"role"`
	Content string `json:"content"`

	// Media is the ordered string references matching Message.Media; not a
	// conversion from UI Attachment. Existing media admission/resolution
	// checks still apply upstream.
	Media []string `json:"media,omitempty"`

	// ReasoningContent is the exact stored Message.ReasoningContent; it is
	// never promoted to ordinary chat content by the archive reader.
	ReasoningContent string `json:"reasoning_content,omitempty"`

	// SystemParts preserves Message.SystemParts / ContentBlock order and each
	// block's CacheControl value. Only messages already belonging to stored
	// model history use this codec — fresh pinned prompts and request-only
	// notes are not newly archived here.
	SystemParts []ModelContentBlock `json:"system_parts,omitempty"`

	// ToolCalls is the ordered provider-call list (see ModelToolCall).
	ToolCalls []ModelToolCall `json:"tool_calls,omitempty"`

	// ToolCallID is the exact result correlation on a role "tool" message.
	ToolCallID string `json:"tool_call_id,omitempty"`
}

// ModelContentBlock mirrors providers.ContentBlock with an explicit encoding
// of the nested cache-control value.
type ModelContentBlock struct {
	Type         string             `json:"type"`
	Text         string             `json:"text"`
	CacheControl *ModelCacheControl `json:"cache_control,omitempty"`
}

// ModelCacheControl mirrors providers.CacheControl (currently only
// "ephemeral" is produced by the Anthropic adapter).
type ModelCacheControl struct {
	Type string `json:"type"`
}

// ModelToolCall mirrors providers.ToolCall EXACTLY, including the three
// members the provider type tags json:"-" (Name, Arguments, ThoughtSignature)
// and the nested Function/ExtraContent shapes. The two argument
// representations are preserved independently: Function.Arguments stays the
// exact raw string, and Arguments stays the parsed JSON object; a storage
// round trip must not normalize either or silently replace a failed
// encode/decode with {}.
type ModelToolCall struct {
	ID               string             `json:"id"`
	Type             string             `json:"type,omitempty"`
	Function         *ModelFunctionCall `json:"function,omitempty"`
	Name             string             `json:"name,omitempty"`
	Arguments        json.RawMessage    `json:"arguments,omitempty"`
	ThoughtSignature string             `json:"thought_signature,omitempty"`
	ExtraContent     *ModelExtraContent `json:"extra_content,omitempty"`
}

// ModelFunctionCall mirrors providers.FunctionCall. Arguments is the raw
// function-argument string, preserved byte-for-byte.
type ModelFunctionCall struct {
	Name             string `json:"name"`
	Arguments        string `json:"arguments"`
	ThoughtSignature string `json:"thought_signature,omitempty"`
}

// ModelExtraContent mirrors providers.ExtraContent.
type ModelExtraContent struct {
	Google *ModelGoogleExtra `json:"google,omitempty"`
}

// ModelGoogleExtra mirrors providers.GoogleExtra — the opaque Gemini
// thought signature that must survive the round trip.
type ModelGoogleExtra struct {
	ThoughtSignature string `json:"thought_signature,omitempty"`
}

// EntryTypeModelRef is the private envelope discriminator of a body-free
// consumption reference (session-core C-ARCHIVE / U2, Decision B): a
// model-only effect that places an already-saved input payload once in model
// order at the moment it is consumed, carrying the source entry ID and its
// exact disk address and NO copied body. It is NOT an addition to the public
// Message.type enum — model-only records never reach the chat projection.
const EntryTypeModelRef EntryType = "model_ref"

// ModelRef is the body-free consumption reference payload of an
// EntryTypeModelRef record. Reference targets are same-session, already
// accepted payload records, never another reference.
type ModelRef struct {
	EntryID      string `json:"entry_id"`
	PartitionKey string `json:"partition_key"`
	ByteOffset   int64  `json:"byte_offset"`
}

// ToolResultFor names the exact assistant content occurrence that issued a
// tool call, carried privately on a role "tool" payload's envelope
// (session-core C-ARCHIVE / U2, Decision A). Identity is the producing
// assistant entry plus call occurrence, NOT tool_call_id alone: a repeated
// call_0 in a later step/turn remains a different result. Its ToolCallID must
// equal the envelope's model_message.tool_call_id.
type ToolResultFor struct {
	AssistantEntryID string `json:"assistant_entry_id"`
	ToolCallID       string `json:"tool_call_id"`
}

// EncodeModelPayload converts one admitted providers.Message into its lossless
// private representation. It fails visibly on a value that cannot be encoded
// (e.g. a parsed Arguments map holding a non-JSON value) rather than dropping
// or substituting content.
func EncodeModelPayload(msg providers.Message) (ModelPayload, error) {
	out := ModelPayload{
		Role:             msg.Role,
		Content:          msg.Content,
		ReasoningContent: msg.ReasoningContent,
		ToolCallID:       msg.ToolCallID,
	}
	if msg.Media != nil {
		out.Media = append([]string(nil), msg.Media...)
	}
	if msg.SystemParts != nil {
		out.SystemParts = make([]ModelContentBlock, len(msg.SystemParts))
		for i, p := range msg.SystemParts {
			out.SystemParts[i] = encodeContentBlock(p)
		}
	}
	if msg.ToolCalls != nil {
		out.ToolCalls = make([]ModelToolCall, len(msg.ToolCalls))
		for i, tc := range msg.ToolCalls {
			enc, err := encodeToolCall(tc)
			if err != nil {
				return ModelPayload{}, fmt.Errorf("model_payload: encode tool call %d (%s): %w", i, tc.ID, err)
			}
			out.ToolCalls[i] = enc
		}
	}
	return out, nil
}

func encodeContentBlock(p providers.ContentBlock) ModelContentBlock {
	block := ModelContentBlock{Type: p.Type, Text: p.Text}
	if p.CacheControl != nil {
		block.CacheControl = &ModelCacheControl{Type: p.CacheControl.Type}
	}
	return block
}

func encodeToolCall(tc providers.ToolCall) (ModelToolCall, error) {
	out := ModelToolCall{
		ID:               tc.ID,
		Type:             tc.Type,
		Name:             tc.Name,
		ThoughtSignature: tc.ThoughtSignature,
	}
	if tc.Function != nil {
		out.Function = &ModelFunctionCall{
			Name:             tc.Function.Name,
			Arguments:        tc.Function.Arguments,
			ThoughtSignature: tc.Function.ThoughtSignature,
		}
	}
	if tc.ExtraContent != nil && tc.ExtraContent.Google != nil {
		out.ExtraContent = &ModelExtraContent{
			Google: &ModelGoogleExtra{ThoughtSignature: tc.ExtraContent.Google.ThoughtSignature},
		}
	}
	if tc.Arguments != nil {
		raw, err := json.Marshal(tc.Arguments)
		if err != nil {
			return ModelToolCall{}, fmt.Errorf("marshal parsed arguments: %w", err)
		}
		out.Arguments = raw
	}
	return out, nil
}

// DecodeModelPayload reconstructs the providers.Message an envelope's private
// payload represents. It fails visibly on a stored payload that cannot be
// decoded rather than returning a partial or zero value.
func DecodeModelPayload(p ModelPayload) (providers.Message, error) {
	out := providers.Message{
		Role:             p.Role,
		Content:          p.Content,
		ReasoningContent: p.ReasoningContent,
		ToolCallID:       p.ToolCallID,
	}
	if p.Media != nil {
		out.Media = append([]string(nil), p.Media...)
	}
	if p.SystemParts != nil {
		out.SystemParts = make([]providers.ContentBlock, len(p.SystemParts))
		for i, b := range p.SystemParts {
			block := providers.ContentBlock{Type: b.Type, Text: b.Text}
			if b.CacheControl != nil {
				block.CacheControl = &providers.CacheControl{Type: b.CacheControl.Type}
			}
			out.SystemParts[i] = block
		}
	}
	if p.ToolCalls != nil {
		out.ToolCalls = make([]providers.ToolCall, len(p.ToolCalls))
		for i, tc := range p.ToolCalls {
			dec, err := decodeToolCall(tc)
			if err != nil {
				return providers.Message{}, fmt.Errorf("model_payload: decode tool call %d (%s): %w", i, tc.ID, err)
			}
			out.ToolCalls[i] = dec
		}
	}
	return out, nil
}

func decodeToolCall(tc ModelToolCall) (providers.ToolCall, error) {
	out := providers.ToolCall{
		ID:               tc.ID,
		Type:             tc.Type,
		Name:             tc.Name,
		ThoughtSignature: tc.ThoughtSignature,
	}
	if tc.Function != nil {
		out.Function = &providers.FunctionCall{
			Name:             tc.Function.Name,
			Arguments:        tc.Function.Arguments,
			ThoughtSignature: tc.Function.ThoughtSignature,
		}
	}
	if tc.ExtraContent != nil && tc.ExtraContent.Google != nil {
		out.ExtraContent = &providers.ExtraContent{
			Google: &providers.GoogleExtra{ThoughtSignature: tc.ExtraContent.Google.ThoughtSignature},
		}
	}
	if len(tc.Arguments) > 0 {
		var args map[string]any
		if err := json.Unmarshal(tc.Arguments, &args); err != nil {
			return providers.ToolCall{}, fmt.Errorf("unmarshal parsed arguments: %w", err)
		}
		out.Arguments = args
	}
	return out, nil
}
