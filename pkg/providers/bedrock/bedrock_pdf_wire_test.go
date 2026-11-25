package bedrock

// Spec #1968 (read-file native PDF) — T24 and T25, the Bedrock rows of the
// FR-18 transport matrix (§5.5): every request carries a PDF inline as a
// document block (format pdf, source bytes) with exact bytes, user and tool,
// in order beside images, named document-<n>; a caption-less user message
// that carries a PDF gains the neutral text block "Attached document." (and
// only then); a malformed PDF data URL becomes a visible
// "PDF omitted: malformed data" text part, never silent; no numeric limit
// drops a PDF here.
//
// Oracles (§5.5 FR-18): bedrockImageBlock today accepts only data:image/, so
// user PDFs are silently skipped (buildUserContent) and tool PDFs are
// counted into the "[Tool-result media omitted for Bedrock: ...]" warning
// (makeToolResultBlock) — every arm here fails by assertion until a document
// block path exists. The Converse body is observed through JSON rather than
// struct fields so the test compiles before the document block type exists.

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	pdf1968BRPNG            = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="
	pdf1968BRMalformedURL   = "data:application/pdf;base64,%%%%%"
	pdf1968BRMalformedBytes = "%%%%%"
	pdf1968BRNote           = "PDF omitted: malformed data"
	pdf1968BRCaption        = "Attached document."
)

var pdf1968BRNameRe = regexp.MustCompile(`^document-[0-9]+$`)

// pdf1968BRPNGData decodes the fixture PNG data URL once, so image-block
// assertions can compare decoded bytes (F13).
func pdf1968BRPNGData(t *testing.T) []byte {
	t.Helper()
	const b64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="
	data, err := base64.StdEncoding.DecodeString(b64)
	require.NoError(t, err)
	return data
}

// pdf1968BRPDF builds a distinct, exact-size PDF-shaped byte sequence.
func pdf1968BRPDF(t *testing.T, size int, tag byte) []byte {
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

func pdf1968BRURL(data []byte) string {
	return "data:application/pdf;base64," + base64.StdEncoding.EncodeToString(data)
}

// pdf1968BRConverse builds the Converse request and returns its marshalled
// JSON plus the per-message maps, in order.
func pdf1968BRConverse(t *testing.T, msgs []Message) (string, []map[string]any) {
	t.Helper()
	req := buildConverseRequest(msgs, nil, nil)
	raw, err := json.Marshal(req)
	require.NoError(t, err)
	var body map[string]any
	require.NoError(t, json.Unmarshal(raw, &body))
	rawMessages, ok := body["messages"].([]any)
	require.True(t, ok, "Converse body must carry messages")
	out := make([]map[string]any, 0, len(rawMessages))
	for _, rawMessage := range rawMessages {
		message, ok := rawMessage.(map[string]any)
		require.True(t, ok, "message must be an object")
		out = append(out, message)
	}
	return string(raw), out
}

// pdf1968BRDocs collects the document blocks of one message's content array
// (top level or inside toolResult children), in order.
func pdf1968BRDocs(t *testing.T, msg map[string]any) []map[string]any {
	t.Helper()
	rawContent, ok := msg["content"].([]any)
	require.True(t, ok, "message content must be an array")
	docs := make([]map[string]any, 0, 1)
	for _, rawBlock := range rawContent {
		block, ok := rawBlock.(map[string]any)
		require.True(t, ok, "content block must be an object")
		if doc, ok := block["document"].(map[string]any); ok {
			docs = append(docs, doc)
			continue
		}
		if result, ok := block["toolResult"].(map[string]any); ok {
			rawChildren, ok := result["content"].([]any)
			require.True(t, ok, "toolResult content must be an array")
			for _, rawChild := range rawChildren {
				child, ok := rawChild.(map[string]any)
				require.True(t, ok, "toolResult child must be an object")
				if doc, ok := child["document"].(map[string]any); ok {
					docs = append(docs, doc)
				}
			}
		}
	}
	return docs
}

func pdf1968BRDocBytes(t *testing.T, doc map[string]any) []byte {
	t.Helper()
	source, ok := doc["source"].(map[string]any)
	require.True(t, ok, "document source must be an object, got %#v", doc["source"])
	rawBytes, ok := source["bytes"].(string)
	require.True(t, ok, "document source bytes must be a base64 string")
	data, err := base64.StdEncoding.DecodeString(rawBytes)
	require.NoError(t, err, "document bytes must be valid base64")
	return data
}

// pdf1968BRImages collects the image blocks of one message (top level or
// inside toolResult children), in order — F13: image presence and bytes must
// be asserted, an image dropped by the transport must fail the test.
func pdf1968BRImages(t *testing.T, msg map[string]any) []map[string]any {
	t.Helper()
	rawContent, ok := msg["content"].([]any)
	require.True(t, ok, "message content must be an array")
	out := make([]map[string]any, 0, 1)
	for _, rawBlock := range rawContent {
		block, ok := rawBlock.(map[string]any)
		if !ok {
			continue
		}
		if img, ok := block["image"].(map[string]any); ok {
			out = append(out, img)
			continue
		}
		if result, ok := block["toolResult"].(map[string]any); ok {
			rawChildren, _ := result["content"].([]any)
			for _, rawChild := range rawChildren {
				child, ok := rawChild.(map[string]any)
				if !ok {
					continue
				}
				if img, ok := child["image"].(map[string]any); ok {
					out = append(out, img)
				}
			}
		}
	}
	return out
}

func pdf1968BRImageBytes(t *testing.T, img map[string]any) []byte {
	t.Helper()
	source, ok := img["source"].(map[string]any)
	require.True(t, ok, "image source must be an object, got %#v", img["source"])
	rawBytes, ok := source["bytes"].(string)
	require.True(t, ok, "image source bytes must be a base64 string")
	data, err := base64.StdEncoding.DecodeString(rawBytes)
	require.NoError(t, err, "image bytes must be valid base64")
	return data
}

// pdf1968BRBlockKinds returns the ordered kinds of one message's content
// blocks (image/document/text/toolResult) for structured order assertions —
// never a raw-string index that passes with -1 when a part is dropped.
func pdf1968BRBlockKinds(t *testing.T, msg map[string]any) []string {
	t.Helper()
	rawContent, ok := msg["content"].([]any)
	require.True(t, ok, "message content must be an array")
	kinds := make([]string, 0, len(rawContent))
	for _, rawBlock := range rawContent {
		block, ok := rawBlock.(map[string]any)
		require.True(t, ok, "content block must be an object")
		switch {
		case block["image"] != nil:
			kinds = append(kinds, "image")
		case block["document"] != nil:
			kinds = append(kinds, "document")
		case block["text"] != nil:
			kinds = append(kinds, "text")
		case block["toolResult"] != nil:
			kinds = append(kinds, "toolResult")
		default:
			kinds = append(kinds, "other")
		}
	}
	return kinds
}

// pdf1968BRToolResultChildKinds returns the ordered child kinds
// ("image"/"document"/"text"/"other") of the toolResult whose toolUseId is
// wantID — F13: image-before-document order INSIDE the toolResult must be
// asserted on the children sequence itself; separate per-kind lists lose
// the relative order.
func pdf1968BRToolResultChildKinds(t *testing.T, msgs []map[string]any, wantID string) []string {
	t.Helper()
	for _, msg := range msgs {
		rawContent, _ := msg["content"].([]any)
		for _, rawBlock := range rawContent {
			block, ok := rawBlock.(map[string]any)
			if !ok {
				continue
			}
			result, ok := block["toolResult"].(map[string]any)
			if !ok {
				continue
			}
			if id, _ := result["toolUseId"].(string); id != wantID {
				continue
			}
			rawChildren, ok := result["content"].([]any)
			require.True(t, ok, "toolResult content must be an array")
			kinds := make([]string, 0, len(rawChildren))
			for _, rawChild := range rawChildren {
				child, ok := rawChild.(map[string]any)
				require.True(t, ok, "toolResult child must be an object")
				switch {
				case child["image"] != nil:
					kinds = append(kinds, "image")
				case child["document"] != nil:
					kinds = append(kinds, "document")
				case child["text"] != nil:
					kinds = append(kinds, "text")
				default:
					kinds = append(kinds, "other")
				}
			}
			return kinds
		}
	}
	t.Fatalf("no toolResult with toolUseId %q on the wire", wantID)
	return nil
}

func pdf1968BRHasText(t *testing.T, msg map[string]any, want string) bool {
	t.Helper()
	rawContent, ok := msg["content"].([]any)
	require.True(t, ok, "message content must be an array")
	for _, rawBlock := range rawContent {
		block, ok := rawBlock.(map[string]any)
		if !ok {
			continue
		}
		if text, ok := block["text"].(string); ok && text == want {
			return true
		}
	}
	return false
}

// TestBedrockPDFWire — T24 (user) and T25 (tool).
func TestBedrockPDFWire(t *testing.T) {
	pdf := pdf1968BRPDF(t, 4096, 'a')
	url := pdf1968BRURL(pdf)

	t.Run("user_valid_beside_image", func(t *testing.T) { // A (T24)
		raw, msgs := pdf1968BRConverse(t, []Message{{
			Role:    "user",
			Content: "summarise these",
			Media:   []string{pdf1968BRPNG, url},
		}})
		require.Len(t, msgs, 1, "one user message on the wire")
		docs := pdf1968BRDocs(t, msgs[0])
		require.Len(t, docs, 1, "the user PDF must travel as a document block (FR-18)")
		assert.Equal(t, "pdf", docs[0]["format"], "document format must be pdf")
		assert.Regexp(t, pdf1968BRNameRe, docs[0]["name"],
			"document name must follow document-<n>")
		assert.True(t, bytes.Equal(pdf1968BRDocBytes(t, docs[0]), pdf),
			"document bytes must be exact")
		assert.NotContains(t, raw, pdf1968BRCaption,
			"the neutral caption must not be added when the user message has text")
		// F13: image presence, bytes and order — asserted structurally so a
		// transport that drops the image can never satisfy the order check.
		imgs := pdf1968BRImages(t, msgs[0])
		require.Len(t, imgs, 1, "the image must still travel beside the document")
		assert.True(t, bytes.Equal(pdf1968BRImageBytes(t, imgs[0]), pdf1968BRPNGData(t)),
			"image bytes must be preserved")
		kinds := pdf1968BRBlockKinds(t, msgs[0])
		imageIdx, docIdx := -1, -1
		for i, k := range kinds {
			switch k {
			case "image":
				if imageIdx < 0 {
					imageIdx = i
				}
			case "document":
				if docIdx < 0 {
					docIdx = i
				}
			}
		}
		require.NotEqual(t, -1, imageIdx, "an image block must be present in wire order")
		require.NotEqual(t, -1, docIdx, "a document block must be present in wire order")
		assert.Less(t, imageIdx, docIdx, "image before document, in media order")
	})

	t.Run("user_captionless_text_block", func(t *testing.T) { // A (T24 caption arm)
		_, msgs := pdf1968BRConverse(t, []Message{{
			Role:    "user",
			Content: "",
			Media:   []string{url},
		}})
		require.Len(t, msgs, 1)
		docs := pdf1968BRDocs(t, msgs[0])
		require.Len(t, docs, 1, "the caption-less user PDF must still travel")
		assert.True(t, pdf1968BRHasText(t, msgs[0], pdf1968BRCaption),
			"a caption-less user message must gain the neutral %q text block", pdf1968BRCaption)
	})

	t.Run("tool_valid_inside_tool_result", func(t *testing.T) { // A (T25)
		_, msgs := pdf1968BRConverse(t, []Message{
			{Role: "assistant", ToolCalls: []ToolCall{{ID: "call-1", Name: "read_file"}}},
			// F13: the positive tool fixture places a PDF BESIDE an image, so
			// mixed media must survive the toolResult path in order.
			{Role: "tool", ToolCallID: "call-1", Content: "pdf read", Media: []string{pdf1968BRPNG, url}},
		})
		var toolResultID string
		found := false
		for _, msg := range msgs {
			rawContent, _ := msg["content"].([]any)
			for _, rawBlock := range rawContent {
				block, ok := rawBlock.(map[string]any)
				if !ok {
					continue
				}
				result, ok := block["toolResult"].(map[string]any)
				if !ok {
					continue
				}
				if id, _ := result["toolUseId"].(string); id == "call-1" {
					toolResultID = id
					for _, doc := range pdf1968BRDocs(t, msg) {
						if bytes.Equal(pdf1968BRDocBytes(t, doc), pdf) {
							found = true
						}
					}
				}
			}
		}
		assert.Equal(t, "call-1", toolResultID, "call id correlation preserved")
		assert.True(t, found,
			"the tool PDF must travel as a document block inside its toolResult (FR-18)")
		// F13: inside the SAME toolResult (call-1), the image must precede
		// the document — asserted on the ordered children sequence, not on
		// separate per-kind lists that lose the relative order.
		kinds := pdf1968BRToolResultChildKinds(t, msgs, "call-1")
		imgIdx, docIdx := -1, -1
		for i, k := range kinds {
			switch k {
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
		require.NotEqual(t, -1, imgIdx, "the image must travel inside the call-1 toolResult")
		require.NotEqual(t, -1, docIdx, "the document must travel inside the call-1 toolResult")
		assert.Less(t, imgIdx, docIdx,
			"inside the toolResult: image before document, in media order")
		// F13: and the image bytes must be preserved.
		require.Len(t, pdf1968BRImages(t, msgs[len(msgs)-1]), 1,
			"the tool result's image must travel beside the document")
		assert.True(t, bytes.Equal(pdf1968BRImageBytes(t, pdf1968BRImages(t, msgs[len(msgs)-1])[0]), pdf1968BRPNGData(t)),
			"tool image bytes must be preserved")
	})

	t.Run("user_malformed_note", func(t *testing.T) { // A (T24 malformed)
		raw, _ := pdf1968BRConverse(t, []Message{{
			Role:    "user",
			Content: "read this",
			Media:   []string{pdf1968BRMalformedURL},
		}})
		assert.Contains(t, raw, pdf1968BRNote,
			"a malformed PDF data URL must surface a visible note, never silence")
		assert.NotContains(t, raw, pdf1968BRMalformedBytes,
			"the malformed payload must not travel")
	})

	t.Run("tool_malformed_note", func(t *testing.T) { // A (T25 malformed)
		raw, _ := pdf1968BRConverse(t, []Message{
			{Role: "assistant", ToolCalls: []ToolCall{{ID: "call-1", Name: "read_file"}}},
			{Role: "tool", ToolCallID: "call-1", Content: "pdf read", Media: []string{pdf1968BRMalformedURL}},
		})
		assert.Contains(t, raw, pdf1968BRNote,
			"a malformed tool PDF data URL must surface a visible note")
		assert.NotContains(t, raw, pdf1968BRMalformedBytes)
	})

	t.Run("count_and_size_user_not_dropped", func(t *testing.T) { // A
		// F13: EIGHT documents — a forbidden transport cap of five drops
		// three and fails — and the sweep still runs past the known Bedrock
		// 4.5 MB document cap: the transport itself applies no numeric limit.
		sizes := []int{32 * 1024, 64 * 1024, 128 * 1024, 256 * 1024, 512 * 1024, 1024 * 1024, 5 * 1024 * 1024, 6 * 1024 * 1024}
		media := make([]string, 0, len(sizes))
		want := make([][]byte, 0, len(sizes))
		for i, size := range sizes {
			data := pdf1968BRPDF(t, size, byte('a'+i))
			want = append(want, data)
			media = append(media, pdf1968BRURL(data))
		}
		_, msgs := pdf1968BRConverse(t, []Message{{Role: "user", Content: "many docs", Media: media}})
		require.Len(t, msgs, 1)
		docs := pdf1968BRDocs(t, msgs[0])
		require.Len(t, docs, len(sizes),
			"no count or size may drop a user PDF at the transport (FR-18)")
		seen := map[string]bool{}
		for i, doc := range docs {
			assert.Regexp(t, pdf1968BRNameRe, doc["name"], "document %d name", i+1)
			name, _ := doc["name"].(string)
			assert.False(t, seen[name], "document names must be unique within the message")
			seen[name] = true
			assert.True(t, bytes.Equal(pdf1968BRDocBytes(t, doc), want[i]),
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
				Media:      []string{pdf1968BRURL(pdf1968BRPDF(t, size, byte('a'+i)))},
			})
		}
		_, wireMsgs := pdf1968BRConverse(t, msgs)
		docs := make([]map[string]any, 0, len(sizes))
		for _, msg := range wireMsgs {
			docs = append(docs, pdf1968BRDocs(t, msg)...)
		}
		require.Len(t, docs, len(sizes),
			"no count or size may drop a tool PDF at the transport (FR-18)")
		for i, size := range sizes {
			want := pdf1968BRPDF(t, size, byte('a'+i))
			assert.True(t, bytes.Equal(pdf1968BRDocBytes(t, docs[i]), want),
				"tool PDF %d must arrive byte-exact in order", i+1)
		}
	})
}
