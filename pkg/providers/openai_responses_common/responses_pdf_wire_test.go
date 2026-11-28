package openai_responses_common

// Spec #1968 (read-file native PDF) — T22 and T23, the OpenAI Responses rows
// of the FR-18 transport matrix (§5.5): every request carries a PDF data URL
// inline as an input_file part with file_data, user and tool, in order beside
// images; tool PDFs travel inside their function_call_output item list; a
// malformed PDF data URL becomes a visible "PDF omitted: malformed data" text
// part, never silent; no numeric limit drops a PDF here.
//
// Oracles (§5.5 FR-18): BuildMultipartContent today handles only
// data:image/ and data:audio/, and the tool branch only data:image/ — PDFs
// are silently dropped on every path, so every arm here fails by assertion
// until implemented (audio already produces OfInputFile; PDF file parts do
// not exist on any path).

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers/protocoltypes"
)

const (
	pdf1968RSPNG            = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="
	pdf1968RSMalformedURL   = "data:application/pdf;base64,%%%%%"
	pdf1968RSMalformedBytes = "%%%%%"
	pdf1968RSNote           = "PDF omitted: malformed data"
)

// pdf1968RSPDF builds a distinct, exact-size PDF-shaped byte sequence.
func pdf1968RSPDF(t *testing.T, size int, tag byte) []byte {
	t.Helper()
	head := fmt.Sprintf("%%PDF-1.4\n%% omnipus 1968 wire tag=%c\n", tag)
	tail := "\n%%EOF\n"
	require.GreaterOrEqual(t, size, len(head)+len(tail), "wire PDF size too small")
	buf := make([]byte, 0, size)
	buf = append(buf, head...)
	for i := len(head) + len(tail); i < size; i++ {
		buf = append(buf, tag)
	}
	buf = append(buf, tail...)
	return buf
}

func pdf1968RSURL(data []byte) string {
	return "data:application/pdf;base64," + base64.StdEncoding.EncodeToString(data)
}

var pdf1968RSPDFRe = regexp.MustCompile(`data:application/pdf;base64,([A-Za-z0-9+/=]+)`)

func pdf1968RSPayloads(t *testing.T, raw string) [][]byte {
	t.Helper()
	matches := pdf1968RSPDFRe.FindAllStringSubmatch(raw, -1)
	out := make([][]byte, 0, len(matches))
	for _, m := range matches {
		data, err := base64.StdEncoding.DecodeString(m[1])
		require.NoError(t, err, "wire PDF payload must be valid base64")
		out = append(out, data)
	}
	return out
}

// pdf1968RSTranslate runs TranslateMessages and returns the marshalled wire
// JSON of the Responses input plus the per-item maps, in order.
func pdf1968RSTranslate(t *testing.T, msgs []protocoltypes.Message) (string, []map[string]any) {
	t.Helper()
	input, _ := TranslateMessages(msgs)
	raw, err := json.Marshal(input)
	require.NoError(t, err)
	var items []map[string]any
	require.NoError(t, json.Unmarshal(raw, &items))
	return string(raw), items
}

// pdf1968RSPartsOf returns the ordered parts of a Responses input item,
// recognized STRUCTURALLY (N3): a content array (message items — with or
// without the optional "type" field the marshaller legitimately omits) and
// a function_call_output's output, array or plain string alike. A string
// output/content becomes one synthetic text part, so text-preservation
// asserts run on every shape. A dropped part fails the media count, not an
// ordering check that passes at index −1.
func pdf1968RSPartsOf(t *testing.T, item map[string]any) []map[string]any {
	t.Helper()
	parts := make([]map[string]any, 0, 4)
	if rawContent, ok := item["content"].([]any); ok {
		for _, raw := range rawContent {
			part, ok := raw.(map[string]any)
			require.True(t, ok, "input part must be an object")
			parts = append(parts, part)
		}
		return parts
	}
	switch out := item["output"].(type) {
	case []any:
		for _, raw := range out {
			part, ok := raw.(map[string]any)
			require.True(t, ok, "output part must be an object")
			parts = append(parts, part)
		}
	case string:
		parts = append(parts, map[string]any{"type": "output_text", "text": out})
	}
	return parts
}

// pdf1968RSMediaParts filters the media parts (input_image / input_file) in
// order — N3: media counts and order are asserted on the filtered media
// parts, never on the total part count that preserved text inflates.
func pdf1968RSMediaParts(parts []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(parts))
	for _, part := range parts {
		if typ, _ := part["type"].(string); typ == "input_image" || typ == "input_file" {
			out = append(out, part)
		}
	}
	return out
}

// pdf1968RSTextOf joins the text a part list carries regardless of the part
// type discriminator (input_text / output_text / a plain string output) —
// N3: preserved message text is asserted separately from the media parts.
func pdf1968RSTextOf(t *testing.T, parts []map[string]any) string {
	t.Helper()
	texts := make([]string, 0, len(parts))
	for _, part := range parts {
		if text, ok := part["text"].(string); ok {
			texts = append(texts, text)
		}
	}
	return strings.Join(texts, "\n")
}

// N8: filenames belong to the identified file parts, never to an image
// or to an unfiltered positional slot inflated by ordinary text.
func pdf1968RSAssertFilenames(t *testing.T, parts []map[string]any, want int) {
	t.Helper()
	files := 0
	for _, part := range parts {
		if part["type"] != "input_file" {
			continue
		}
		files++
		filename, _ := part["filename"].(string)
		assert.NotEmpty(t, filename, "input_file part %d must carry a filename field", files)
	}
	require.Equal(t, want, files, "check EVERY expected file's filename")
}

// A guard for the filename instrument, not a substitute for the real wire tests.
func TestResponses1968FilenameOracle(t *testing.T) {
	for _, output := range []bool{false, true} {
		t.Run(fmt.Sprintf("tool=%v", output), func(t *testing.T) {
			parts := []any{
				map[string]any{"type": "input_text", "text": "ordinary text"},
				map[string]any{"type": "input_image", "image_url": pdf1968RSPNG},
				map[string]any{"type": "input_file", "filename": "a.pdf", "file_data": pdf1968RSURL([]byte("%PDF-a"))},
				map[string]any{"type": "input_file", "filename": "b.pdf", "file_data": pdf1968RSURL([]byte("%PDF-b"))},
			}
			item := map[string]any{"content": parts}
			if output {
				item = map[string]any{"type": "function_call_output", "call_id": "call-1", "output": parts}
			}
			decoded := pdf1968RSPartsOf(t, item)
			require.Equal(t, "ordinary text", pdf1968RSTextOf(t, decoded))
			media := pdf1968RSMediaParts(decoded)
			require.Len(t, media, 3)
			require.Equal(t, "input_image", media[0]["type"])
			pdf1968RSAssertFilenames(t, media, 2)
		})
	}
}

// TestResponsesPDFWire — T22 (user) and T23 (tool).
func TestResponsesPDFWire(t *testing.T) {
	pdf := pdf1968RSPDF(t, 4096, 'a')
	url := pdf1968RSURL(pdf)

	t.Run("user_valid_beside_image", func(t *testing.T) { // A (T22)
		// F13: a size above known provider caps (Bedrock's 4.5 MB) rides
		// along — the transport itself applies no numeric limit.
		big := pdf1968RSPDF(t, 5*1024*1024, 'b')
		raw, items := pdf1968RSTranslate(t, []protocoltypes.Message{{
			Role:    "user",
			Content: "summarise these",
			Media:   []string{pdf1968RSPNG, url, pdf1968RSURL(big)},
		}})
		require.Len(t, items, 1, "one user message item on the wire")
		parts := pdf1968RSPartsOf(t, items[0])
		// N3: the message's normal text is preserved — asserted separately
		// from the media parts, whose count and order the media filter pins.
		assert.Contains(t, pdf1968RSTextOf(t, parts), "summarise these",
			"the message's normal text must survive beside the media parts")
		media := pdf1968RSMediaParts(parts)
		require.Len(t, media, 3, "image plus both PDFs must travel, in media order")
		assert.Equal(t, "input_image", media[0]["type"],
			"the image must still travel (a dropped image cannot satisfy an ordering check)")
		assert.Equal(t, "input_file", media[1]["type"])
		assert.Equal(t, "input_file", media[2]["type"])
		// F13: image bytes preserved.
		imgURL, _ := media[0]["image_url"].(string)
		assert.Equal(t, pdf1968RSPNG, imgURL, "image bytes must be preserved")
		assert.Equal(t, url, media[1]["file_data"],
			"file_data must carry the full PDF data URL with exact bytes")
		assert.Equal(t, pdf1968RSURL(big), media[2]["file_data"],
			"the over-4.5MB PDF must travel byte-exact too")
		// F13: the filename field is required (presence, not a pinned
		// literal — the spec does not mandate a filename value here).
		pdf1968RSAssertFilenames(t, media, 2)
		payloads := pdf1968RSPayloads(t, raw)
		require.Len(t, payloads, 2)
		assert.True(t, string(payloads[0]) == string(pdf), "payload byte-exact")
		assert.True(t, string(payloads[1]) == string(big), "large payload byte-exact")
	})

	t.Run("user_malformed_note", func(t *testing.T) { // A (T22 malformed)
		raw, _ := pdf1968RSTranslate(t, []protocoltypes.Message{{
			Role:    "user",
			Content: "read this",
			Media:   []string{pdf1968RSMalformedURL},
		}})
		assert.Contains(t, raw, pdf1968RSNote,
			"a malformed PDF data URL must surface a visible note, never silence")
		assert.NotContains(t, raw, pdf1968RSMalformedBytes,
			"the malformed payload must not travel")
	})

	t.Run("tool_valid_inside_function_call_output", func(t *testing.T) { // A (T23)
		raw, items := pdf1968RSTranslate(t, []protocoltypes.Message{
			{Role: "assistant", ToolCalls: []protocoltypes.ToolCall{{ID: "call-1", Name: "read_file"}}},
			// F13: the positive tool fixture places a PDF BESIDE an image so
			// mixed media must survive the function_call_output path in order.
			{Role: "tool", ToolCallID: "call-1", Content: "pdf read", Media: []string{pdf1968RSPNG, url}},
		})
		var foundOutput bool
		for _, item := range items {
			if id, _ := item["call_id"].(string); id != "call-1" {
				continue
			}
			if kind, _ := item["type"].(string); kind != "function_call_output" {
				continue
			}
			parts := pdf1968RSPartsOf(t, item)
			// N3: the tool result's normal text is preserved — asserted
			// separately from the media parts (which the filter pins to
			// exactly two, in order).
			assert.Contains(t, pdf1968RSTextOf(t, parts), "pdf read",
				"the tool result's normal text must survive beside its media parts")
			media := pdf1968RSMediaParts(parts)
			require.Len(t, media, 2, "the tool result's image and PDF must both travel, in media order")
			assert.Equal(t, "input_image", media[0]["type"])
			assert.Equal(t, "input_file", media[1]["type"])
			imgURL, _ := media[0]["image_url"].(string)
			assert.Equal(t, pdf1968RSPNG, imgURL, "tool image bytes must be preserved")
			fileData, _ := media[1]["file_data"].(string)
			assert.Equal(t, url, fileData, "tool PDF file_data exact")
			pdf1968RSAssertFilenames(t, media, 1)
			foundOutput = true
		}
		assert.True(t, foundOutput,
			"the tool PDF must travel inside its function_call_output item list as input_file (FR-18)")
		require.Len(t, pdf1968RSPayloads(t, raw), 1, "exactly one PDF on the wire")
		assert.True(t, string(pdf1968RSPayloads(t, raw)[0]) == string(pdf), "payload byte-exact")
	})

	t.Run("tool_malformed_note", func(t *testing.T) { // A (T23 malformed)
		raw, _ := pdf1968RSTranslate(t, []protocoltypes.Message{
			{Role: "assistant", ToolCalls: []protocoltypes.ToolCall{{ID: "call-1", Name: "read_file"}}},
			{Role: "tool", ToolCallID: "call-1", Content: "pdf read", Media: []string{pdf1968RSMalformedURL}},
		})
		assert.Contains(t, raw, pdf1968RSNote,
			"a malformed tool PDF data URL must surface a visible note")
		assert.NotContains(t, raw, pdf1968RSMalformedBytes)
	})

	t.Run("count_and_size_user_not_dropped", func(t *testing.T) { // A
		// F13: EIGHT documents — a forbidden transport cap of five drops
		// three and fails — and the sweep still runs past the known Bedrock
		// 4.5 MB document cap: the transport itself applies no numeric limit.
		sizes := []int{32 * 1024, 64 * 1024, 128 * 1024, 256 * 1024, 512 * 1024, 1024 * 1024, 5 * 1024 * 1024, 6 * 1024 * 1024}
		media := make([]string, 0, len(sizes))
		want := make([][]byte, 0, len(sizes))
		for i, size := range sizes {
			data := pdf1968RSPDF(t, size, byte('a'+i))
			want = append(want, data)
			media = append(media, pdf1968RSURL(data))
		}
		raw, _ := pdf1968RSTranslate(t, []protocoltypes.Message{{Role: "user", Content: "many docs", Media: media}})
		payloads := pdf1968RSPayloads(t, raw)
		require.Len(t, payloads, len(sizes),
			"no count or size may drop a user PDF at the transport (FR-18)")
		for i := range payloads {
			assert.True(t, string(payloads[i]) == string(want[i]),
				"user PDF %d must arrive byte-exact in order", i+1)
		}
	})

	t.Run("count_and_size_tool_not_dropped", func(t *testing.T) { // A
		// F13: eight documents and past the known Bedrock 4.5 MB cap here
		// too — a forbidden cap of five cannot survive this sweep.
		sizes := []int{32 * 1024, 64 * 1024, 128 * 1024, 256 * 1024, 512 * 1024, 1024 * 1024, 5 * 1024 * 1024, 6 * 1024 * 1024}
		msgs := make([]protocoltypes.Message, 0, len(sizes))
		for i, size := range sizes {
			msgs = append(msgs, protocoltypes.Message{
				Role:       "tool",
				ToolCallID: fmt.Sprintf("call-%d", i+1),
				Content:    fmt.Sprintf("result %d", i+1),
				Media:      []string{pdf1968RSURL(pdf1968RSPDF(t, size, byte('a'+i)))},
			})
		}
		raw, _ := pdf1968RSTranslate(t, msgs)
		payloads := pdf1968RSPayloads(t, raw)
		require.Len(t, payloads, len(sizes),
			"no count or size may drop a tool PDF at the transport (FR-18)")
		for i, size := range sizes {
			want := pdf1968RSPDF(t, size, byte('a'+i))
			assert.True(t, string(payloads[i]) == string(want),
				"tool PDF %d must arrive byte-exact in order", i+1)
		}
	})
}
