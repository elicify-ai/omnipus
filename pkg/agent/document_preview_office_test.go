//go:build goolm && stdjson

package agent

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/tests/officepreview"
	"github.com/stretchr/testify/require"
)

func officeAgentPNG(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	require.NoError(t, png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 2))))
	return b.Bytes()
}
func officeAgentDoc(t *testing.T) *tools.DocumentPreview {
	t.Helper()
	return &tools.DocumentPreview{PDF: officepreview.PDF(1), Pages: []tools.DocumentPreviewPage{{Number: 1, PNG: officeAgentPNG(t)}}, Text: "MODEL_TEXT_ONLY", Reauthorize: func(context.Context) error { return nil }, Thumbnails: []tools.DocumentPreviewThumbnail{{Ref: "media://DISPLAY_ONLY_REF", PageNumber: 1, Caption: "display-only"}}, Record: generated.MediaPreview{SourceName: "one.pdf", SourceVersion: "a1b2c3d4e5f6", PageCount: 1, FirstPage: 1, LastPage: 1, Outcome: "complete"}}
}

// T-16: the same real preview is assembled differently for each model form.
func TestOfficePreview_ModelFormsAndReauthorization(t *testing.T) {
	for _, kind := range []string{"pdf", "images", "text", "revoked"} {
		t.Run(kind, func(t *testing.T) {
			doc := officeAgentDoc(t)
			calls := 0
			doc.Reauthorize = func(context.Context) error {
				calls++
				if kind == "revoked" {
					return errors.New("source revoked")
				}
				return nil
			}
			previews := &turnDocumentPreviews{}
			previews.add("call", doc)
			defer previews.release()
			forms := previewModelForms{pdf: kind == "pdf" || kind == "revoked", images: kind == "images", maxPDF: 20 << 20, resize: catalog.ResizeLimits{LongEdgePx: 2000, MaxBytes: 4 << 20}}
			original := []providers.Message{{Role: "tool", ToolCallID: "call", Content: tools.DocumentPreviewNotRetained}}
			got, err := attachTurnDocumentPreviews(context.Background(), original, previews, forms)
			require.NoError(t, err)
			require.Equal(t, 1, calls)
			require.Empty(t, original[0].Media, "live attachment must not mutate stored messages")
			require.NotContains(t, got[0].Content, "media://DISPLAY_ONLY_REF")
			switch kind {
			case "pdf":
				require.Equal(t, []string{"data:application/pdf;base64," + base64.StdEncoding.EncodeToString(doc.PDF)}, got[0].Media)
			case "images":
				require.Len(t, got[0].Media, 1)
				require.True(t, strings.HasPrefix(got[0].Media[0], "data:image/"))
				require.Contains(t, got[0].Content, "page images 1–1")
			case "text":
				require.Empty(t, got[0].Media)
				require.Contains(t, got[0].Content, "MODEL_TEXT_ONLY")
			case "revoked":
				require.Empty(t, got[0].Media)
				require.Contains(t, got[0].Content, "content withheld: access to the source was denied: source revoked")
				require.NotContains(t, got[0].Content, "MODEL_TEXT_ONLY")
			}
		})
	}
}

func TestOfficePreview_TurnBudgetFreesOldestBytesAndPreservesDisplay(t *testing.T) {
	before := previewGatewayBytes.Load()
	require.Zero(t, before, "fixture begins with no other live previews")
	previews := &turnDocumentPreviews{}
	defer previews.release()
	old := &tools.DocumentPreview{PDF: make([]byte, 32<<20), Thumbnails: []tools.DocumentPreviewThumbnail{{Ref: "media://old-display"}}}
	newest := &tools.DocumentPreview{PDF: make([]byte, 33<<20)}
	previews.add("old", old)
	previews.add("new", newest)
	require.Empty(t, old.PDF, "AD-6: eviction actually frees bytes")
	require.Len(t, old.Thumbnails, 1, "display records survive model-media eviction")
	require.Contains(t, previews.markers["old"], "evicted")
	require.Equal(t, 33<<20, previews.bytes)
	require.Equal(t, int64(33<<20), previewGatewayBytes.Load())
	previews.release()
	require.Empty(t, newest.PDF)
	require.Zero(t, previews.bytes)
	require.Equal(t, before, previewGatewayBytes.Load())
	oversized := &tools.DocumentPreview{PDF: make([]byte, (64<<20)+1)}
	previews.add("oversized", oversized)
	require.Empty(t, oversized.PDF)
	require.Contains(t, previews.markers["oversized"], "larger than the 64 MiB per-turn media budget")
	require.Equal(t, before, previewGatewayBytes.Load())
}

func TestOfficePreview_GatewayBudgetNeverExceeds256MiB(t *testing.T) {
	before := previewGatewayBytes.Load()
	require.Zero(t, before)
	turns := make([]*turnDocumentPreviews, 5)
	// The shared backing is only a fixture allocation optimization. Accounting
	// must count each turn's independent live batch, not unique backing arrays.
	data := make([]byte, 64<<20)
	for i := range turns {
		turns[i] = &turnDocumentPreviews{}
		defer turns[i].release()
		turns[i].add("call", &tools.DocumentPreview{PDF: data})
	}
	require.Equal(t, int64(256<<20), previewGatewayBytes.Load())
	require.Zero(t, turns[4].bytes)
	require.Empty(t, turns[4].byID["call"].PDF)
	require.Contains(t, turns[4].markers["call"], "evicted")
	for _, turn := range turns {
		turn.release()
	}
	require.Equal(t, before, previewGatewayBytes.Load())
}

func TestOfficePreview_RequestBudgetKeepsNewestAndMarksOmissions(t *testing.T) {
	previews := &turnDocumentPreviews{}
	defer previews.release()
	old := &tools.DocumentPreview{PDF: bytes.Repeat([]byte("a"), 20<<20), Reauthorize: func(context.Context) error { return nil }}
	newest := &tools.DocumentPreview{PDF: bytes.Repeat([]byte("b"), 20<<20), Reauthorize: func(context.Context) error { return nil }}
	previews.add("old", old)
	previews.add("new", newest)
	msgs := []providers.Message{{Role: "tool", ToolCallID: "old"}, {Role: "tool", ToolCallID: "new"}}
	got, err := attachTurnDocumentPreviews(context.Background(), msgs, previews, previewModelForms{pdf: true, maxPDF: 20 << 20})
	require.NoError(t, err)
	require.Empty(t, got[0].Media)
	require.Contains(t, got[0].Content, "[pages not re-sent; call preview_document again]")
	require.Len(t, got[1].Media, 1)
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(got[1].Media[0], "data:application/pdf;base64,"))
	require.NoError(t, err)
	require.Equal(t, newest.PDF, decoded)
	require.LessOrEqual(t, len(decoded), 32<<20)
}

func officeRecordResult(t *testing.T, result *tools.ToolResult, name string) (*cwR1Harness, *agentLoopRunTurnResponse, *agentLoopRunTurnToolsExecute) {
	t.Helper()
	h := cwR1New(t, 64000)
	ts := h.turn("inspect document")
	call := providers.ToolCall{ID: "office-call", Type: "function", Function: &providers.FunctionCall{Name: name, Arguments: `{"file":"one.pdf"}`}, Name: name, Arguments: map[string]any{"file": "one.pdf"}}
	h.append(t, providers.Message{Role: "user", Content: "inspect document"}, providers.Message{Role: "assistant", ToolCalls: []providers.ToolCall{call}})
	rr := cwR1Flow(h, ts, h.store.GetHistory(h.key), nil)
	rr.rq.ri.rf.rt.documentPreviews = &turnDocumentPreviews{}
	t.Cleanup(rr.rq.ri.rf.rt.documentPreviews.release)
	rr.normalizedToolCalls = []providers.ToolCall{call}
	ex := &agentLoopRunTurnToolsExecute{rx: &agentLoopRunTurnTools{ctx: context.Background(), rr: rr}, toolName: name, toolCallID: call.ID, toolResult: result, toolArgs: call.Arguments}
	require.NoError(t, ex.recordToolResult(call))
	require.Equal(t, agentLoopRunTurnToolsExecuteNext, ex.finishCall(0))
	return h, rr, ex
}

// T-63: real recording/choke point, real archive/window, and actual HTTP
// provider request. The display transcript is deliberately separate.
func TestOfficePreview_DisplayOnlyNeverEntersArchiveWindowOrNextRequest(t *testing.T) {
	doc := officeAgentDoc(t)
	thumb := base64.StdEncoding.EncodeToString(doc.Pages[0].PNG)
	h, rr, ex := officeRecordResult(t, &tools.ToolResult{ForLLM: tools.DocumentPreviewNotRetained, DocumentPreview: doc}, "preview_document")
	for label, value := range map[string]any{"archive": h.archive(t), "window": h.store.GetHistory(h.key)} {
		raw, err := json.Marshal(value)
		require.NoError(t, err)
		require.NotContains(t, string(raw), "DISPLAY_ONLY_REF", label)
		require.NotContains(t, string(raw), thumb, label)
		require.Contains(t, string(raw), "not retained; call preview_document again", label)
	}
	display, err := json.Marshal(ex.tcRecord.Result)
	require.NoError(t, err)
	require.Contains(t, string(display), "DISPLAY_ONLY_REF", "control: display transcript really retains thumbnail metadata")
	recorder, provider := cwR1OpenAI(t, 0)
	messages, err := attachTurnDocumentPreviews(context.Background(), h.store.GetHistory(h.key), rr.rq.ri.rf.rt.documentPreviews, previewModelForms{pdf: true, maxPDF: 20 << 20})
	require.NoError(t, err)
	_, err = provider.Chat(context.Background(), messages, nil, "fixture-model", nil)
	require.NoError(t, err)
	first, err := json.Marshal(recorder.requests(t))
	require.NoError(t, err)
	require.Contains(t, string(first), base64.StdEncoding.EncodeToString(doc.PDF), "control: native live PDF really reaches outbound bytes")
	require.NotContains(t, string(first), "DISPLAY_ONLY_REF")
	require.NotContains(t, string(first), thumb)
	rr.rq.ri.rf.rt.documentPreviews.release()
	_, err = provider.Chat(context.Background(), h.store.GetHistory(h.key), nil, "fixture-model", nil)
	require.NoError(t, err)
	requests := recorder.requests(t)
	require.Len(t, requests, 2)
	next, err := json.Marshal(requests[1])
	require.NoError(t, err)
	require.NotContains(t, string(next), "DISPLAY_ONLY_REF")
	require.NotContains(t, string(next), thumb)
	require.NotContains(t, string(next), base64.StdEncoding.EncodeToString(officepreview.PDF(1)))
}
