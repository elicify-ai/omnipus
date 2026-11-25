package anthropicprovider

// Spec #1968 (read-file native PDF) — T18 and T19, the Anthropic rows of the
// FR-18 transport matrix (§5.5): every request carries a PDF data URL inline
// as a document with exact bytes, user and tool, in order beside images; a
// malformed PDF data URL becomes a visible "PDF omitted: malformed data" text
// part, never silent; no numeric limit drops a PDF here.
//
// Oracles (§5.5 FR-18): user PDFs travel as OfDocument document blocks
// ("base64" source, "application/pdf" media type) — already true today, so
// those arms are keep-green guards; tool PDFs must travel INSIDE the
// tool_result block (anthropicToolResult today counts them as unsupported
// media type and omits them); malformed data URLs must surface the fixed
// notice instead of being forwarded or dropped silently.
//
// The wire is observed through a recording Anthropic-compatible server, the
// same way tool_image_transport_test.go observes tool images.

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	pdf1968WirePNG            = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="
	pdf1968WireMalformedURL   = "data:application/pdf;base64,%%%%%"
	pdf1968WireMalformedBytes = "%%%%%"
	pdf1968WireNote           = "PDF omitted: malformed data"
)

// pdf1968WirePDF builds a distinct, exact-size PDF-shaped byte sequence.
// The transports carry bytes verbatim, so nothing here needs to be a
// parseable document — only stable, distinct and base64-encodable.
func pdf1968WirePDF(t *testing.T, size int, tag byte) []byte {
	t.Helper()
	head := fmt.Sprintf("%%PDF-1.4\n%% omnipus 1968 wire tag=%c\n", tag)
	tail := "\n%%EOF\n"
	require.GreaterOrEqual(t, size, len(head)+len(tail), "wire PDF size too small")
	buf := make([]byte, 0, size)
	buf = append(buf, head...)
	buf = append(buf, bytes.Repeat([]byte{tag}, size-len(head)-len(tail))...)
	buf = append(buf, tail...)
	return buf
}

func pdf1968WireURL(data []byte) string {
	return "data:application/pdf;base64," + base64.StdEncoding.EncodeToString(data)
}

// pdf1968WireChat sends messages through the non-streaming Anthropic
// transport against a recording server and returns the decoded request body.
func pdf1968WireChat(t *testing.T, messages []Message) map[string]any {
	t.Helper()
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"id":"msg","type":"message","role":"assistant","model":"model","stop_reason":"end_turn","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	defer server.Close()

	p := NewProviderWithClient(createAnthropicTestClient(server.URL, "token"))
	_, err := p.Chat(t.Context(), messages, nil, "model", nil)
	require.NoError(t, err)
	return body
}

// pdf1968WireAnthropicDocs collects the PDF document blocks of the request
// body, split into user-message documents and tool_result documents (with
// their tool_use_id), in wire order.
func pdf1968WireAnthropicDocs(t *testing.T, body map[string]any) (userDocs []map[string]any, toolDocs []map[string]any, toolIDs []string) {
	t.Helper()
	rawMessages, ok := body["messages"].([]any)
	require.True(t, ok, "body must carry a messages array")
	for _, rawMessage := range rawMessages {
		message, ok := rawMessage.(map[string]any)
		require.True(t, ok, "message must be an object")
		rawContent, ok := message["content"].([]any)
		require.True(t, ok, "message content must be an array")
		for _, rawBlock := range rawContent {
			block, ok := rawBlock.(map[string]any)
			require.True(t, ok, "content block must be an object")
			switch block["type"] {
			case "document":
				userDocs = append(userDocs, block)
			case "tool_result":
				rawChildren, ok := block["content"].([]any)
				require.True(t, ok, "tool_result content must be an array")
				for _, rawChild := range rawChildren {
					child, ok := rawChild.(map[string]any)
					require.True(t, ok, "tool_result child must be an object")
					if child["type"] == "document" {
						toolDocs = append(toolDocs, child)
						id, _ := block["tool_use_id"].(string)
						toolIDs = append(toolIDs, id)
					}
				}
			}
		}
	}
	return userDocs, toolDocs, toolIDs
}

// pdf1968WireAnthropicBlocks collects, inside tool_result blocks only, the
// document blocks and the image blocks in order — F13: mixed media inside
// tool results must be asserted for presence and bytes, not just documents.
func pdf1968WireAnthropicBlocks(t *testing.T, body map[string]any) (toolDocs []map[string]any, toolImgs []map[string]any) {
	t.Helper()
	rawMessages, ok := body["messages"].([]any)
	require.True(t, ok, "body must carry a messages array")
	for _, rawMessage := range rawMessages {
		message, ok := rawMessage.(map[string]any)
		require.True(t, ok, "message must be an object")
		rawContent, ok := message["content"].([]any)
		require.True(t, ok, "message content must be an array")
		for _, rawBlock := range rawContent {
			block, ok := rawBlock.(map[string]any)
			require.True(t, ok, "content block must be an object")
			if block["type"] != "tool_result" {
				continue
			}
			rawChildren, ok := block["content"].([]any)
			require.True(t, ok, "tool_result content must be an array")
			for _, rawChild := range rawChildren {
				child, ok := rawChild.(map[string]any)
				require.True(t, ok, "tool_result child must be an object")
				switch child["type"] {
				case "document":
					toolDocs = append(toolDocs, child)
				case "image":
					toolImgs = append(toolImgs, child)
				}
			}
		}
	}
	return toolDocs, toolImgs
}

// pdf1968WireAnthropicToolResultChildren returns, for every tool_result in
// the body, its tool_use_id and the ordered kinds of its children
// ("image"/"document"/"text"/"other") — F13: image-before-document order
// INSIDE the tool result must be asserted on the children sequence itself;
// separate per-kind lists lose the relative order.
func pdf1968WireAnthropicToolResultChildren(t *testing.T, body map[string]any) ([]string, [][]string) {
	t.Helper()
	var ids []string
	var kindLists [][]string
	rawMessages, ok := body["messages"].([]any)
	require.True(t, ok, "body must carry a messages array")
	for _, rawMessage := range rawMessages {
		message, ok := rawMessage.(map[string]any)
		require.True(t, ok, "message must be an object")
		rawContent, ok := message["content"].([]any)
		require.True(t, ok, "message content must be an array")
		for _, rawBlock := range rawContent {
			block, ok := rawBlock.(map[string]any)
			require.True(t, ok, "content block must be an object")
			if block["type"] != "tool_result" {
				continue
			}
			rawChildren, ok := block["content"].([]any)
			require.True(t, ok, "tool_result content must be an array")
			kinds := make([]string, 0, len(rawChildren))
			for _, rawChild := range rawChildren {
				child, ok := rawChild.(map[string]any)
				require.True(t, ok, "tool_result child must be an object")
				kind, isString := child["type"].(string)
				require.True(t, isString, "tool_result child type must be a string")
				switch kind {
				case "image", "document", "text":
					kinds = append(kinds, kind)
				default:
					kinds = append(kinds, "other")
				}
			}
			id, _ := block["tool_use_id"].(string)
			ids = append(ids, id)
			kindLists = append(kindLists, kinds)
		}
	}
	return ids, kindLists
}

// pdf1968WireImageDataURL reconstructs the data URL of an Anthropic image
// block so its bytes can be compared against the fixture data URL (F13).
func pdf1968WireImageDataURL(t *testing.T, img map[string]any) string {
	t.Helper()
	source, ok := img["source"].(map[string]any)
	require.True(t, ok, "image source must be an object, got %#v", img["source"])
	mediaType, _ := source["media_type"].(string)
	data, _ := source["data"].(string)
	require.NotEmpty(t, data, "image source data must be a non-empty base64 string")
	return "data:" + mediaType + ";base64," + data
}

func pdf1968WireDocData(t *testing.T, doc map[string]any) []byte {
	t.Helper()
	source, ok := doc["source"].(map[string]any)
	require.True(t, ok, "document source must be an object, got %#v", doc["source"])
	assert.Equal(t, "base64", source["type"], "FR-18: native document source type")
	assert.Equal(t, "application/pdf", source["media_type"], "FR-18: native document media type")
	data, ok := source["data"].(string)
	require.True(t, ok, "document source data must be a string")
	decoded, err := base64.StdEncoding.DecodeString(data)
	require.NoError(t, err, "document data must be valid base64")
	return decoded
}

// pdf1968RawBody re-marshals the decoded body for string-level assertions
// (notice sentences are plain ASCII and survive JSON escaping).
func pdf1968RawBody(t *testing.T, body map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	return string(raw)
}

// TestAnthropicPDFWire — T18 (user) and T19 (tool).
func TestAnthropicPDFWire(t *testing.T) {
	pdf := pdf1968WirePDF(t, 4096, 'a')
	url := pdf1968WireURL(pdf)

	t.Run("user_valid_beside_image", func(t *testing.T) { // G today
		body := pdf1968WireChat(t, []Message{{
			Role:    "user",
			Content: "summarise these",
			Media:   []string{pdf1968WirePNG, url},
		}})
		userDocs, _, _ := pdf1968WireAnthropicDocs(t, body)
		require.Len(t, userDocs, 1, "the user PDF must travel as a document block")
		assert.True(t, bytes.Equal(pdf1968WireDocData(t, userDocs[0]), pdf),
			"document bytes must be exact")
		// Order beside the image: the document block follows the image block.
		rawMessages, ok := body["messages"].([]any)
		require.True(t, ok, "messages must be an array")
		require.NotEmpty(t, rawMessages, "user message must be present")
		message, ok := rawMessages[0].(map[string]any)
		require.True(t, ok, "user message must be an object")
		rawContent, ok := message["content"].([]any)
		require.True(t, ok, "user content must be an array")
		imageIdx, docIdx := -1, -1
		for i, rawBlock := range rawContent {
			block, isObject := rawBlock.(map[string]any)
			require.True(t, isObject, "user content block must be an object")
			switch block["type"] {
			case "image":
				imageIdx = i
			case "document":
				docIdx = i
			}
		}
		require.NotEqual(t, -1, imageIdx, "the image must still travel")
		require.NotEqual(t, -1, docIdx, "the document must still travel")
		assert.Less(t, imageIdx, docIdx, "image before document, in media order")
		imageBlock, ok := rawContent[imageIdx].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, pdf1968WirePNG, pdf1968WireImageDataURL(t, imageBlock),
			"the user image bytes must survive beside the PDF")
	})

	t.Run("user_malformed_note", func(t *testing.T) { // A
		body := pdf1968WireChat(t, []Message{{
			Role:    "user",
			Content: "read this",
			Media:   []string{pdf1968WireMalformedURL},
		}})
		raw := pdf1968RawBody(t, body)
		assert.Contains(t, raw, pdf1968WireNote,
			"a malformed PDF data URL must surface a visible note, never silence")
		assert.NotContains(t, raw, pdf1968WireMalformedBytes,
			"the malformed payload must not travel as a document")
		userDocs, _, _ := pdf1968WireAnthropicDocs(t, body)
		assert.Empty(t, userDocs, "no document block may be built from malformed data")
	})

	t.Run("tool_valid_inside_tool_result", func(t *testing.T) { // A (T19)
		body := pdf1968WireChat(t, []Message{
			{Role: "assistant", ToolCalls: []ToolCall{{ID: "call-1", Name: "read_file"}}},
			// F13: the positive tool fixture places a PDF BESIDE an image so
			// mixed media must survive the tool_result path in order.
			{Role: "tool", ToolCallID: "call-1", Content: "pdf read", Media: []string{pdf1968WirePNG, url}},
		})
		_, toolDocs, toolIDs := pdf1968WireAnthropicDocs(t, body)
		require.Len(t, toolDocs, 1,
			"the tool PDF must travel as a document block INSIDE its tool_result (FR-18)")
		assert.Equal(t, []string{"call-1"}, toolIDs, "call id correlation preserved")
		assert.True(t, bytes.Equal(pdf1968WireDocData(t, toolDocs[0]), pdf),
			"tool document bytes must be exact")
		// F13: the tool image must travel inside the SAME tool_result, before
		// the document — asserted on the ordered children sequence of the
		// call-1 tool_result, which separate per-kind lists cannot express.
		ids, kindLists := pdf1968WireAnthropicToolResultChildren(t, body)
		require.Len(t, ids, 1, "exactly one tool_result on the wire")
		assert.Equal(t, "call-1", ids[0], "call id correlation preserved")
		imgIdx, docIdx := -1, -1
		for i, kind := range kindLists[0] {
			switch kind {
			case "image":
				if imgIdx < 0 {
					imgIdx = i
				}
			case "document":
				if docIdx < 0 {
					docIdx = i
				}
			}
		}
		require.NotEqual(t, -1, imgIdx, "the image must travel inside the call-1 tool_result")
		require.NotEqual(t, -1, docIdx, "the document must travel inside the call-1 tool_result")
		assert.Less(t, imgIdx, docIdx,
			"inside the tool_result: image before document, in media order")
		// F13: and the image bytes must be preserved.
		_, toolImgs := pdf1968WireAnthropicBlocks(t, body)
		require.Len(t, toolImgs, 1, "the tool image must travel inside its tool_result")
		assert.Equal(t, pdf1968WirePNG, pdf1968WireImageDataURL(t, toolImgs[0]),
			"tool image bytes must be preserved")
	})

	t.Run("tool_malformed_note", func(t *testing.T) { // A (T19 malformed)
		body := pdf1968WireChat(t, []Message{
			{Role: "assistant", ToolCalls: []ToolCall{{ID: "call-1", Name: "read_file"}}},
			{Role: "tool", ToolCallID: "call-1", Content: "pdf read", Media: []string{pdf1968WireMalformedURL}},
		})
		raw := pdf1968RawBody(t, body)
		assert.Contains(t, raw, pdf1968WireNote,
			"a malformed tool PDF data URL must surface a visible note")
		assert.NotContains(t, raw, pdf1968WireMalformedBytes)
		_, toolDocs, _ := pdf1968WireAnthropicDocs(t, body)
		assert.Empty(t, toolDocs, "no document block may be built from malformed data")
	})

	t.Run("count_and_size_user_not_dropped", func(t *testing.T) { // G guard
		// F13: EIGHT documents — a forbidden transport cap of five drops
		// three and fails — and the sweep still runs past the known Bedrock
		// 4.5 MB document cap: the transport itself applies no numeric limit.
		sizes := []int{32 * 1024, 64 * 1024, 128 * 1024, 256 * 1024, 512 * 1024, 1024 * 1024, 5 * 1024 * 1024, 6 * 1024 * 1024}
		media := make([]string, 0, len(sizes))
		want := make([][]byte, 0, len(sizes))
		for i, size := range sizes {
			data := pdf1968WirePDF(t, size, byte('a'+i))
			want = append(want, data)
			media = append(media, pdf1968WireURL(data))
		}
		body := pdf1968WireChat(t, []Message{{Role: "user", Content: "many docs", Media: media}})
		userDocs, _, _ := pdf1968WireAnthropicDocs(t, body)
		require.Len(t, userDocs, len(sizes),
			"no count or size may drop a user PDF at the transport (FR-18)")
		for i, doc := range userDocs {
			assert.True(t, bytes.Equal(pdf1968WireDocData(t, doc), want[i]),
				"user PDF %d must arrive byte-exact in order", i+1)
		}
	})

	t.Run("count_and_size_tool_not_dropped", func(t *testing.T) { // A
		// F13: eight documents and past the known Bedrock 4.5 MB cap here
		// too — a forbidden cap of five cannot survive this sweep.
		sizes := []int{32 * 1024, 64 * 1024, 128 * 1024, 256 * 1024, 512 * 1024, 1024 * 1024, 5 * 1024 * 1024, 6 * 1024 * 1024}
		calls := make([]ToolCall, 0, len(sizes))
		msgs := []Message{}
		for i, size := range sizes {
			id := fmt.Sprintf("call-%d", i+1)
			calls = append(calls, ToolCall{ID: id, Name: "read_file"})
			msgs = append(msgs, Message{
				Role:       "tool",
				ToolCallID: id,
				Content:    fmt.Sprintf("result %d", i+1),
				Media:      []string{pdf1968WireURL(pdf1968WirePDF(t, size, byte('a'+i)))},
			})
		}
		msgs = append([]Message{{Role: "assistant", ToolCalls: calls}}, msgs...)
		body := pdf1968WireChat(t, msgs)
		_, toolDocs, toolIDs := pdf1968WireAnthropicDocs(t, body)
		require.Len(t, toolDocs, len(sizes),
			"no count or size may drop a tool PDF at the transport (FR-18)")
		for i, id := range toolIDs {
			assert.Equal(t, fmt.Sprintf("call-%d", i+1), id, "tool PDF %d call id", i+1)
		}
		for i, doc := range toolDocs {
			want := pdf1968WirePDF(t, sizes[i], byte('a'+i))
			assert.True(t, bytes.Equal(pdf1968WireDocData(t, doc), want),
				"tool PDF %d must arrive byte-exact in order", i+1)
		}
	})
}
