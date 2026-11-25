package common

// Spec #1968 (read-file native PDF) — T20 and T21, the Chat Completions rows
// of the FR-18 transport matrix (§5.5): every request carries a PDF data URL
// inline as a `file` part with exact bytes, user and tool, in order beside
// images; tool messages carry no file parts, so tool PDFs travel in a
// trailing tool-evidence user message; a malformed PDF data URL becomes a
// visible "PDF omitted: malformed data" text part, never silent; no numeric
// limit drops a PDF here.
//
// Oracles (§5.5 FR-18): user PDFs already serialize as
// {"type":"file","file":{...}} (keep-green guards); tool PDFs are silently
// dropped today — serializeMessages' pendingToolImages forwards only
// data:image/ — so the tool arms fail by assertion until implemented.

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	pdf1968CCPNG            = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="
	pdf1968CCMalformedURL   = "data:application/pdf;base64,%%%%%"
	pdf1968CCMalformedBytes = "%%%%%"
	pdf1968CCNote           = "PDF omitted: malformed data"
)

// pdf1968CCPDF builds a distinct, exact-size PDF-shaped byte sequence.
func pdf1968CCPDF(t *testing.T, size int, tag byte) []byte {
	t.Helper()
	head := fmt.Sprintf("%%PDF-1.4\n%% omnipus 1968 wire tag=%c\n", tag)
	tail := "\n%%EOF\n"
	require.GreaterOrEqual(t, size, len(head)+len(tail), "wire PDF size too small")
	buf := make([]byte, 0, size)
	buf = append(buf, head...)
	buf = append(buf, repeatBytes(tag, size-len(head)-len(tail))...)
	buf = append(buf, tail...)
	return buf
}

func repeatBytes(tag byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = tag
	}
	return out
}

func pdf1968CCURL(data []byte) string {
	return "data:application/pdf;base64," + base64.StdEncoding.EncodeToString(data)
}

var pdf1968CCPDFRe = regexp.MustCompile(`data:application/pdf;base64,([A-Za-z0-9+/=]+)`)

func pdf1968CCPayloads(t *testing.T, raw string) [][]byte {
	t.Helper()
	matches := pdf1968CCPDFRe.FindAllStringSubmatch(raw, -1)
	out := make([][]byte, 0, len(matches))
	for _, m := range matches {
		data, err := base64.StdEncoding.DecodeString(m[1])
		require.NoError(t, err, "wire PDF payload must be valid base64")
		out = append(out, data)
	}
	return out
}

// pdf1968CCSerialize runs SerializeMessages and returns the marshalled wire
// JSON plus the per-message maps, in order.
func pdf1968CCSerialize(t *testing.T, msgs []Message) (string, []map[string]any) {
	t.Helper()
	out := SerializeMessages(msgs)
	raw, err := json.Marshal(out)
	require.NoError(t, err)
	var items []map[string]any
	require.NoError(t, json.Unmarshal(raw, &items))
	return string(raw), items
}

// pdf1968CCParts returns the ordered {type, part-map} of a message's
// content array — the structured walk F13 requires: an ordering check built
// on it fails loudly when a part is dropped, instead of passing through a
// strings.Index of −1.
func pdf1968CCParts(t *testing.T, msg map[string]any) []map[string]any {
	t.Helper()
	rawContent, _ := msg["content"].([]any)
	parts := make([]map[string]any, 0, len(rawContent))
	for _, raw := range rawContent {
		part, ok := raw.(map[string]any)
		require.True(t, ok, "content part must be an object")
		parts = append(parts, part)
	}
	return parts
}

func pdf1968CCPartType(part map[string]any) string {
	typ, _ := part["type"].(string)
	return typ
}

func pdf1968CCPartImageURL(part map[string]any) string {
	img, _ := part["image_url"].(map[string]any)
	url, _ := img["url"].(string)
	return url
}

// pdf1968CCFileParts returns the "file" parts of a message's content array.
func pdf1968CCFileParts(msg map[string]any) []map[string]any {
	rawContent, _ := msg["content"].([]any)
	parts := make([]map[string]any, 0, len(rawContent))
	for _, raw := range rawContent {
		part, ok := raw.(map[string]any)
		if !ok || part["type"] != "file" {
			continue
		}
		if file, ok := part["file"].(map[string]any); ok {
			parts = append(parts, file)
		}
	}
	return parts
}

// pdf1968CCText joins a message's text parts.
func pdf1968CCText(msg map[string]any) string {
	rawContent, _ := msg["content"].([]any)
	texts := make([]string, 0, len(rawContent))
	for _, raw := range rawContent {
		part, ok := raw.(map[string]any)
		if !ok || part["type"] != "text" {
			continue
		}
		if text, ok := part["text"].(string); ok {
			texts = append(texts, text)
		}
	}
	return strings.Join(texts, "\n")
}

// TestChatCompletionsPDFWire — T20 (user) and T21 (tool).
func TestChatCompletionsPDFWire(t *testing.T) {
	pdf := pdf1968CCPDF(t, 4096, 'a')
	url := pdf1968CCURL(pdf)

	t.Run("user_valid_beside_image", func(t *testing.T) { // G today
		// F13: a size above known provider caps (Bedrock's 4.5 MB) rides
		// along — the transport itself applies no numeric limit.
		big := pdf1968CCPDF(t, 5*1024*1024, 'b')
		raw, items := pdf1968CCSerialize(t, []Message{{
			Role:    "user",
			Content: "summarise these",
			Media:   []string{pdf1968CCPNG, url, pdf1968CCURL(big)},
		}})
		require.Len(t, items, 1, "one user message on the wire")
		// Assert on the MEDIA parts in order; the message text may itself
		// serialize as a text part, which is not what this guard pins.
		var mediaParts []map[string]any
		for _, part := range pdf1968CCParts(t, items[0]) {
			switch pdf1968CCPartType(part) {
			case "image_url", "file":
				mediaParts = append(mediaParts, part)
			}
		}
		require.Len(t, mediaParts, 3, "image plus both PDFs must travel, in media order")
		assert.Equal(t, "image_url", pdf1968CCPartType(mediaParts[0]),
			"the image must still travel (a dropped image cannot satisfy an ordering check)")
		assert.Equal(t, "file", pdf1968CCPartType(mediaParts[1]))
		assert.Equal(t, "file", pdf1968CCPartType(mediaParts[2]))
		// F13: image bytes preserved — the image part must carry the same
		// data URL that went in.
		assert.Equal(t, pdf1968CCPNG, pdf1968CCPartImageURL(mediaParts[0]),
			"image bytes must be preserved")
		files := pdf1968CCFileParts(items[0])
		require.Len(t, files, 2, "both user PDFs must travel as file parts")
		assert.Equal(t, url, files[0]["file_data"],
			"file_data must carry the full PDF data URL with exact bytes")
		assert.Equal(t, pdf1968CCURL(big), files[1]["file_data"],
			"the over-4.5MB PDF must travel byte-exact too")
		// F13: the filename field is required (presence, not a pinned
		// literal — the spec does not mandate a filename value here).
		for i, file := range files {
			filename, _ := file["filename"].(string)
			assert.NotEmpty(t, filename, "user file part %d must carry a filename field", i+1)
		}
		payloads := pdf1968CCPayloads(t, raw)
		require.Len(t, payloads, 2)
		assert.True(t, string(payloads[0]) == string(pdf), "payload byte-exact")
		assert.True(t, string(payloads[1]) == string(big), "large payload byte-exact")
	})

	t.Run("user_malformed_note", func(t *testing.T) { // A
		raw, items := pdf1968CCSerialize(t, []Message{{
			Role:    "user",
			Content: "read this",
			Media:   []string{pdf1968CCMalformedURL},
		}})
		assert.Contains(t, raw, pdf1968CCNote,
			"a malformed PDF data URL must surface a visible note, never silence")
		assert.NotContains(t, raw, pdf1968CCMalformedBytes,
			"the malformed payload must not travel as a file part")
		for _, msg := range items {
			assert.Empty(t, pdf1968CCFileParts(msg),
				"no file part may be built from malformed data")
		}
	})

	t.Run("tool_valid_trailing_user_message", func(t *testing.T) { // A (T21)
		raw, items := pdf1968CCSerialize(t, []Message{
			{Role: "assistant", ToolCalls: []ToolCall{{ID: "call-1", Name: "read_file"}}},
			// F13: the positive tool fixture places a PDF BESIDE an image so
			// mixed media must survive the trailing tool-evidence path.
			{Role: "tool", ToolCallID: "call-1", Content: "pdf read", Media: []string{pdf1968CCPNG, url}},
		})
		toolIdx := -1
		for i, msg := range items {
			if id, _ := msg["tool_call_id"].(string); id == "call-1" {
				toolIdx = i
			}
		}
		require.NotEqual(t, -1, toolIdx, "the tool message must be on the wire")
		var trailing map[string]any
		for i := toolIdx + 1; i < len(items); i++ {
			if role, _ := items[i]["role"].(string); role != "user" {
				continue
			}
			for _, file := range pdf1968CCFileParts(items[i]) {
				if file["file_data"] == url {
					trailing = items[i]
				}
			}
		}
		require.NotNil(t, trailing,
			"the tool PDF must travel in a trailing tool-evidence user message (FR-18); "+
				"tool messages carry no file parts")
		assert.Contains(t, pdf1968CCText(trailing), "call-1",
			"the trailing user message must cite the tool call id")
		// F13: the tool image must travel in the same trailing message, in
		// media order before the file part — presence asserted, not implied.
		trailingParts := pdf1968CCParts(t, trailing)
		imgIdx, fileIdx := -1, -1
		for i, part := range trailingParts {
			switch pdf1968CCPartType(part) {
			case "image_url":
				if imgIdx < 0 {
					imgIdx = i
				}
			case "file":
				if fileIdx < 0 {
					fileIdx = i
				}
			}
		}
		require.NotEqual(t, -1, imgIdx, "the tool image must travel in the trailing user message")
		require.NotEqual(t, -1, fileIdx, "the tool PDF file part must travel in the trailing user message")
		assert.Less(t, imgIdx, fileIdx, "image before file, in media order")
		assert.Equal(t, pdf1968CCPNG, pdf1968CCPartImageURL(trailingParts[imgIdx]),
			"tool image bytes must be preserved")
		// F13: the tool file part must carry a filename field too.
		for _, file := range pdf1968CCFileParts(trailing) {
			filename, _ := file["filename"].(string)
			assert.NotEmpty(t, filename, "the tool file part must carry a filename field")
		}
		require.Len(t, pdf1968CCPayloads(t, raw), 1, "exactly one PDF on the wire")
	})

	t.Run("tool_malformed_note", func(t *testing.T) { // A (T21 malformed)
		raw, _ := pdf1968CCSerialize(t, []Message{
			{Role: "assistant", ToolCalls: []ToolCall{{ID: "call-1", Name: "read_file"}}},
			{Role: "tool", ToolCallID: "call-1", Content: "pdf read", Media: []string{pdf1968CCMalformedURL}},
		})
		assert.Contains(t, raw, pdf1968CCNote,
			"a malformed tool PDF data URL must surface a visible note")
		assert.NotContains(t, raw, pdf1968CCMalformedBytes)
	})

	t.Run("count_and_size_user_not_dropped", func(t *testing.T) { // G guard
		// F13: EIGHT documents — a forbidden transport cap of five drops
		// three and fails — and the sweep still runs past the known Bedrock
		// 4.5 MB document cap: the transport itself applies no numeric limit.
		sizes := []int{32 * 1024, 64 * 1024, 128 * 1024, 256 * 1024, 512 * 1024, 1024 * 1024, 5 * 1024 * 1024, 6 * 1024 * 1024}
		media := make([]string, 0, len(sizes))
		want := make([][]byte, 0, len(sizes))
		for i, size := range sizes {
			data := pdf1968CCPDF(t, size, byte('a'+i))
			want = append(want, data)
			media = append(media, pdf1968CCURL(data))
		}
		raw, items := pdf1968CCSerialize(t, []Message{{Role: "user", Content: "many docs", Media: media}})
		require.Len(t, items, 1)
		files := pdf1968CCFileParts(items[0])
		require.Len(t, files, len(sizes),
			"no count or size may drop a user PDF at the transport (FR-18)")
		payloads := pdf1968CCPayloads(t, raw)
		require.Len(t, payloads, len(sizes))
		for i := range payloads {
			assert.True(t, string(payloads[i]) == string(want[i]),
				"user PDF %d must arrive byte-exact in order", i+1)
		}
	})

	t.Run("count_and_size_tool_not_dropped", func(t *testing.T) { // A
		// F13: eight documents and past the known Bedrock 4.5 MB cap here
		// too — a forbidden cap of five cannot survive this sweep.
		sizes := []int{32 * 1024, 64 * 1024, 128 * 1024, 256 * 1024, 512 * 1024, 1024 * 1024, 5 * 1024 * 1024, 6 * 1024 * 1024}
		msgs := make([]Message, 1, 1+len(sizes))
		msgs[0] = Message{Role: "assistant"}
		for i, size := range sizes {
			msgs = append(msgs, Message{
				Role:       "tool",
				ToolCallID: fmt.Sprintf("call-%d", i+1),
				Content:    fmt.Sprintf("result %d", i+1),
				Media:      []string{pdf1968CCURL(pdf1968CCPDF(t, size, byte('a'+i)))},
			})
		}
		raw, _ := pdf1968CCSerialize(t, msgs)
		payloads := pdf1968CCPayloads(t, raw)
		require.Len(t, payloads, len(sizes),
			"no count or size may drop a tool PDF at the transport (FR-18)")
		for i, size := range sizes {
			want := pdf1968CCPDF(t, size, byte('a'+i))
			assert.True(t, string(payloads[i]) == string(want),
				"tool PDF %d must arrive byte-exact in order", i+1)
		}
	})
}
