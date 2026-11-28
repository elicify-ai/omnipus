//go:build goolm && stdjson

package agent

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/preview"
	"github.com/elicify-ai/omnipus/pkg/preview/component"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
	"github.com/elicify-ai/omnipus/pkg/providers/common"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/require"
)

// G5 / CR F4, SF-06, PTA F1. AD-6: one request carries at most 32 MiB of
// decoded preview media and at most the catalog's native-document count.
// An uploaded PDF and a tool PDF are one request, not two budgets.
func TestOfficePreview_UploadAndToolShareRequestBudget(t *testing.T) {
	require.Zero(t, previewGatewayBytes.Load(), "instrument: gateway counter must start at 0")
	const chunk = 20 << 20 // each side is under 32 MiB; together they are not
	upload := bytes.Repeat([]byte("U"), chunk)
	toolPDF := bytes.Repeat([]byte("T"), chunk)
	uploadURL := "data:application/pdf;base64," + base64.StdEncoding.EncodeToString(upload)
	const toolID = "gatefix-tool-pdf"
	previews := &turnDocumentPreviews{}
	previews.add(toolID, &tools.DocumentPreview{
		PDF:         toolPDF,
		Reauthorize: func(context.Context) error { return nil },
	})
	t.Cleanup(previews.release)
	messages := []providers.Message{
		{Role: "user", Content: "the upload", Media: []string{uploadURL}},
		{Role: "assistant", ToolCalls: []providers.ToolCall{{ID: toolID, Name: "preview_document", Function: &providers.FunctionCall{Name: "preview_document"}}}},
		{Role: "tool", ToolCallID: toolID, Content: "preview recorded"},
	}
	// maxPDFs 1 is the catalog per-request document count. maxPDF is large
	// enough that neither document is dropped for size on its own.
	got, err := attachTurnDocumentPreviews(context.Background(), messages, previews, previewModelForms{
		pdf: true, maxPDF: 50 << 20, maxPDFs: 1,
	})
	require.NoError(t, err)
	count, decoded := gatefixNativePDFTotals(t, got)
	require.LessOrEqual(t, count, 1, "AD-6: a request may carry at most 1 native PDF when the catalog max is 1; upload and tool PDFs are the same count")
	require.LessOrEqual(t, decoded, previewRequestBudget, "AD-6: decoded preview bytes in one request are at most 32 MiB; 20 MiB plus 20 MiB is over")
}

// G6 / CR F3, SF-07. AD-6: an uploaded PDF gets 20 s, then text. The wait
// uses the caller's Stop, not a fresh 120 s background context.
func TestOfficePreview_UploadRenderHonorsTwentySecondBudget(t *testing.T) {
	previous := preview.Shared()
	t.Cleanup(func() { preview.SetShared(previous) })
	self, err := os.Executable()
	require.NoError(t, err)
	home := t.TempDir()
	svc, err := preview.New(preview.Config{Home: home, Self: self, Resolver: &component.Resolver{Home: home, Executable: self}})
	require.NoError(t, err)
	preview.SetShared(svc)
	first, err := svc.Admit(context.Background(), false)
	require.NoError(t, err)
	t.Cleanup(first.Release)
	second, err := svc.Admit(context.Background(), false)
	require.NoError(t, err)
	t.Cleanup(second.Release)
	pdfPath := filepath.Join(t.TempDir(), "upload.pdf")
	require.NoError(t, os.WriteFile(pdfPath, makeMiniPDF("GATEFIX_UPLOAD"), 0o600))

	data, err := os.ReadFile(pdfPath)
	require.NoError(t, err)
	done := make(chan uploadPages, 1)
	started := time.Now()
	go func() {
		done <- uploadPDFPageImagesFrom(context.Background(), data, "upload.pdf", catalog.ResizeLimits{})
	}()
	select {
	case got := <-done:
		elapsed := time.Since(started)
		if elapsed < 15*time.Second {
			t.Fatalf("upload returned in %s, before the 20s budget (problem %q); both renderer slots were held, so this is not the admission wait", elapsed, got.problem)
		}
		require.Empty(t, got.images, "the held slots must stop the upload before any page is sent")
		require.NotContains(t, got.problem, "120", "AD-6 upload budget is 20s, not the 120s whole-call budget; problem %q", got.problem)
		require.NotEmpty(t, got.problem)
	case <-time.After(25 * time.Second):
		t.Fatal("upload still blocked after 25s; AD-6 gives an uploaded PDF 20s and Stop must end the work, not a 120s background context")
	}
}

// G7 / CR F5, SF-10, PTA F3. AD-6 live bytes are the PDF, the text and the
// page images. Images rendered on first send still count.
func TestOfficePreview_LazyPagesCountTowardLiveBytes(t *testing.T) {
	require.Zero(t, previewGatewayBytes.Load(), "instrument: gateway counter must start at 0")
	pngBytes := gatefixPNG()
	doc := &tools.DocumentPreview{
		PDF:         []byte("%PDF-1.4 gatefix"),
		Reauthorize: func(context.Context) error { return nil },
		RenderPages: func(context.Context) ([]tools.DocumentPreviewPage, string, string) {
			return []tools.DocumentPreviewPage{{Number: 1, PNG: pngBytes}}, "", ""
		},
	}
	previews := &turnDocumentPreviews{}
	previews.add("gatefix-lazy", doc)
	t.Cleanup(previews.release)
	msg := providers.Message{Role: "tool", ToolCallID: "gatefix-lazy", Content: "read the pdf"}
	_, err := attachDocumentPreview(context.Background(), &msg, doc, previewModelForms{images: true, resize: catalog.DefaultResizeLimits}, previewRequestBudget)
	require.NoError(t, err)
	require.Equal(t, int64(doc.LiveBytes()), previewGatewayBytes.Load(), "AD-6: page images rendered after the preview was recorded still count toward the live-byte budgets")
}

// D3 / SF W-01. The 256 MiB gateway budget is one reservation. Many turns,
// released together, each try to take the last free chunk. Exactly one may
// succeed, and the reserved total must never pass the cap. A plain load,
// check and add lets more than one through; compare-and-swap does not.
func TestOfficePreview_GatewayReservationIsAtomic(t *testing.T) {
	require.Zero(t, previewGatewayBytes.Load(), "instrument: gateway counter must start at 0")
	t.Cleanup(func() { previewGatewayBytes.Store(0) })
	if runtime.GOMAXPROCS(0) < 2 {
		t.Fatal("BLOCKED: D3 needs at least two processors; one processor hides the load/check/add race")
	}
	// Premise, through the real paths (AD-6): a turn's real preview store
	// charges the gateway counter when it admits (turnDocumentPreviews.add →
	// reserveLocked → reservePreviewGatewayBytes), and releasing the turns
	// returns exactly what was charged. Setting the counter by hand, as the
	// contention round below does, cannot see an admission that never
	// charges or a release that never returns.
	blob := bytes.Repeat([]byte{7}, previewTurnBudget)
	var held []*turnDocumentPreviews
	for n := 0; n < 3; n++ {
		p := &turnDocumentPreviews{}
		p.add(fmt.Sprintf("gatefix-d3-held-%d", n), gatefixLiveDoc(blob))
		held = append(held, p)
	}
	require.Equal(t, int64(3*previewTurnBudget), previewGatewayBytes.Load(),
		"instrument: three real 64 MiB preview turns must hold 192 MiB of the gateway budget; an admission that never charges it leaves the counter at 0")
	for _, p := range held {
		p.release()
	}
	require.Zero(t, previewGatewayBytes.Load(),
		"instrument: releasing the held preview turns must return the gateway counter to zero; a no-op release leaves 192 MiB held")
	const (
		goroutines = 64
		rounds     = 500
		n          = int64(1 << 20) // 1 MiB; the counter starts with room for exactly one
	)
	startAt := int64(previewGatewayBudget) - n
	for round := 0; round < rounds; round++ {
		previewGatewayBytes.Store(startAt)
		var goNow atomic.Bool
		var ready atomic.Int32
		var won atomic.Int32
		var done sync.WaitGroup
		done.Add(goroutines)
		for i := 0; i < goroutines; i++ {
			go func() {
				defer done.Done()
				ready.Add(1)
				// Yield often enough that the starter can set the flag, but
				// stay on a core so the goroutines already running enter the
				// reservation together. A blocking wait staggers them and
				// hides the load/check/add gap.
				var spins int
				for !goNow.Load() {
					spins++
					if spins&63 == 0 {
						runtime.Gosched()
					}
				}
				if reservePreviewGatewayBytes(n) {
					won.Add(1)
				}
			}()
		}
		for ready.Load() < goroutines {
			runtime.Gosched()
		}
		goNow.Store(true)
		done.Wait()
		got := previewGatewayBytes.Load()
		winners := int64(won.Load())
		if winners != 1 || got != startAt+n || got > int64(previewGatewayBudget) {
			t.Fatalf("round %d: %d reservations of %d bytes succeeded and the gateway holds %d; the cap is %d and exactly one fits (AD-6)", round, winners, n, got, previewGatewayBudget)
		}
	}
}

// G12 / CR W1, PTA F2, B-10. A provider that rejects a PDF attached from a
// live tool result (preview_document or read_file) must be retried with the
// document's text. The same PDF must not be sent again. A bad-model error
// is not that recovery.
func TestOfficePreview_RejectedLiveToolPDFRecoversAsText(t *testing.T) {
	rejection := &common.ProviderError{Status: 400, Body: `{"error":{"message":"PDF input for model 'm' is not available."}}`}
	badModel := &providers.FailoverError{Reason: "bad_model", Provider: "openrouter", Model: "m", Status: 400}
	for _, toolName := range []string{"preview_document", "read_file"} {
		t.Run(toolName, func(t *testing.T) {
			gatefixAssertLivePDFRecovery(t, toolName, rejection, true)
		})
	}
	t.Run("bad_model_does_not_recover", func(t *testing.T) {
		gatefixAssertLivePDFRecovery(t, "preview_document", badModel, false)
	})
}

func gatefixAssertLivePDFRecovery(t *testing.T, toolName string, first error, wantText bool) {
	t.Helper()
	require.Zero(t, previewGatewayBytes.Load(), "instrument: gateway counter must start at 0")
	const callID = "gatefix-live-pdf"
	const sentinel = "GATEFIX_LIVE_TOOL_TEXT"
	client := &routing1340RecoveryClient{first: first}
	agent := &AgentInstance{ID: "gatefix", Model: "m", Candidates: []providers.FallbackCandidate{{Provider: "openrouter", Model: "m"}}}
	agent.StoreProviderPool(map[string]providers.LLMProvider{"openrouter": client})
	f := newRouting1340ReviewFixture(t, &config.Config{}, agent)
	// Rewritten 2026-11-28 (one PDF mechanism): the base fixture had no
	// catalogue and relied on modelSupportsPDF's optimistic nil answer.
	// The one PDF decision (#1968 FR-3) sends a native PDF only when the
	// catalogue says the exact model reads PDFs, so the fixture says so.
	cat, err := catalog.NewCatalog(catalogDocJSON(t, "openrouter", "m", "text", "pdf"))
	require.NoError(t, err)
	f.al.SetCapabilityCatalog(cat)
	f.selectProvider(t)
	toolContent := "tool result recorded"
	if toolName == "read_file" {
		// read_file's PDF is a turn-private InspectionPDF from the real tool.
		ws := t.TempDir()
		path := filepath.Join(ws, "live.pdf")
		require.NoError(t, os.WriteFile(path, makeMiniPDF(sentinel), 0o600))
		result := tools.NewReadFileTool(ws, true, 0).Execute(context.Background(), map[string]any{"path": path})
		require.False(t, result.IsError, "%s", result.ForLLM)
		require.Len(t, result.InspectionPDFs, 1, "premise: read_file returned its PDF for the presenter")
		f.rt.inspectionPDFs = map[string][]tools.InspectionPDF{callID: result.InspectionPDFs}
		toolContent = result.ForLLM
	} else {
		previews := &turnDocumentPreviews{}
		previews.add(callID, &tools.DocumentPreview{
			PDF:         makeMiniPDF(sentinel),
			Reauthorize: func(context.Context) error { return nil },
		})
		t.Cleanup(previews.release)
		f.rt.documentPreviews = previews
	}
	messages := []providers.Message{
		{Role: "user", Content: "read the attached document"},
		{Role: "assistant", ToolCalls: []providers.ToolCall{{ID: callID, Name: toolName, Function: &providers.FunctionCall{Name: toolName}}}},
		{Role: "tool", ToolCallID: callID, Content: toolContent},
	}
	f.rf.callMessages = messages
	f.ri.messages = messages
	if flow := f.rr.callLLMWithRetries(); flow != agentLoopRunTurnResponseNext {
		t.Fatalf("provider-call flow=%v err=%v", flow, f.rf.err)
	}
	require.NotEmpty(t, client.seen, "the provider must be called")
	require.True(t, gatefixHasPDF(client.seen[0]), "the live tool PDF must be on the first provider request, or this is not the rejection path")
	if !wantText {
		require.Len(t, client.seen, 1, "a bad-model rejection must not be retried as text")
		return
	}
	require.Len(t, client.seen, 2, "B-10: a provider that rejects the live tool PDF must be retried with text, not failed after one call")
	require.False(t, gatefixHasPDF(client.seen[1]), "the retry must not send the same PDF again")
	require.Contains(t, gatefixContents(client.seen[1]), sentinel, "the retry must carry the document's extracted text")
	require.NotNil(t, f.rf.response)
	require.Equal(t, "recovered-answer", f.rf.response.Content)
}

func gatefixHasPDF(messages []providers.Message) bool {
	for _, msg := range messages {
		for _, ref := range msg.Media {
			if strings.HasPrefix(ref, "data:application/pdf;") {
				return true
			}
		}
	}
	return false
}

func gatefixContents(messages []providers.Message) string {
	var b strings.Builder
	for _, msg := range messages {
		b.WriteString(msg.Content)
		b.WriteByte('\n')
	}
	return b.String()
}

func gatefixPNG() []byte {
	img := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: 200, G: 20, B: 20, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func gatefixNativePDFTotals(t *testing.T, messages []providers.Message) (count int, decoded int) {
	t.Helper()
	const prefix = "data:application/pdf;base64,"
	for _, msg := range messages {
		for _, ref := range msg.Media {
			if !strings.HasPrefix(ref, prefix) {
				continue
			}
			raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(ref, prefix))
			require.NoError(t, err)
			count++
			decoded += len(raw)
		}
	}
	return count, decoded
}
