//go:build goolm && stdjson

package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/media/mediatest"
	"github.com/elicify-ai/omnipus/pkg/preview"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/tests/officepreview"
	"github.com/stretchr/testify/require"
)

func officeUpload(t *testing.T, data []byte, name string, store media.MediaStore) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, data, 0600))
	ref, err := store.Store(path, media.MediaMeta{Filename: name, ContentType: "application/pdf"}, "office-test")
	require.NoError(t, err)
	return ref
}

// Recognize only the native DOCUMENT part in the four documented transports
// (§4.2), not a base64 string accidentally copied into ordinary text.
func officePDFPayloads(v any, family string) []string {
	var found []string
	switch node := v.(type) {
	case []any:
		for _, value := range node {
			found = append(found, officePDFPayloads(value, family)...)
		}
	case map[string]any:
		switch family {
		case "openai":
			if node["type"] == "file" {
				if file, ok := node["file"].(map[string]any); ok {
					if data, ok := file["file_data"].(string); ok {
						found = append(found, strings.TrimPrefix(data, "data:application/pdf;base64,"))
					}
				}
			}
		case "responses":
			if node["type"] == "input_file" {
				if data, ok := node["file_data"].(string); ok {
					found = append(found, strings.TrimPrefix(data, "data:application/pdf;base64,"))
				}
			}
		case "anthropic":
			if node["type"] == "document" {
				if source, ok := node["source"].(map[string]any); ok && source["type"] == "base64" && source["media_type"] == "application/pdf" {
					if data, ok := source["data"].(string); ok {
						found = append(found, data)
					}
				}
			}
		case "bedrock":
			if node["format"] == "pdf" {
				if source, ok := node["source"].(map[string]any); ok {
					if data, ok := source["bytes"].(string); ok {
						found = append(found, data)
					}
				}
			}
		}
		for _, value := range node {
			found = append(found, officePDFPayloads(value, family)...)
		}
	}
	return found
}

// T-44: actual entry points, real archive admission for tool results, real
// provider adapters, with only the paid network endpoint replaced.
func TestOfficePDFTransport_AllAdaptersAndEntryPoints(t *testing.T) {
	for _, family := range []string{"openai", "anthropic", "responses", "bedrock"} {
		t.Run(family, func(t *testing.T) {
			for _, entry := range []string{"upload", "read_file", "preview_document"} {
				t.Run(entry, func(t *testing.T) {
					data := officepreview.PDF(1)
					cat, err := catalog.NewCatalog(catalogDocJSON(t, "office-provider", "office-model", "text", "image", "pdf"))
					require.NoError(t, err)
					var messages []providers.Message
					if entry == "upload" {
						store := mediatest.NewFileMediaStore(t)
						ref := officeUpload(t, data, "one.pdf", store)
						messages = resolveMediaRefsWithOffload([]providers.Message{{Role: "user", Content: "inspect document", Media: []string{ref}}}, store, 20<<20, "office-provider", "office-model", &offloadSink{workDir: t.TempDir()}, cat, nil, "")
					} else {
						ws := t.TempDir()
						path := filepath.Join(ws, "one.pdf")
						require.NoError(t, os.WriteFile(path, data, 0600))
						var result *tools.ToolResult
						if entry == "read_file" {
							result = tools.NewReadFileTool(ws, true, 0).Execute(context.Background(), map[string]any{"path": path})
						} else {
							result = tools.NewPreviewDocumentTool(ws, true, tools.WithPreviewService(officepreview.Service(t, false, false))).Execute(context.Background(), map[string]any{"file": path})
						}
						require.False(t, result.IsError, "%s", result.ForLLM)
						h, rr, _ := officeRecordResult(t, result, entry)
						forms := rr.rq.ri.rf.rt.previewForms(cat, "office-provider", "office-model", catalog.ResizeLimits{LongEdgePx: 2000, MaxBytes: 4 << 20})
						messages, err = attachTurnDocumentPreviews(context.Background(), h.store.GetHistory(h.key), rr.rq.ri.rf.rt.documentPreviews, forms)
						require.NoError(t, err)
					}
					recorder, provider := cwR1Adapter(t, family)
					_, err = provider.Chat(context.Background(), messages, nil, "office-model", nil)
					require.NoError(t, err)
					requests := recorder.requests(t)
					require.Len(t, requests, 1)
					raw, err := json.Marshal(requests[0])
					require.NoError(t, err)
					var wire any
					require.NoError(t, json.Unmarshal(raw, &wire))
					payloads := officePDFPayloads(wire, family)
					require.Len(t, payloads, 1, "FR-039 / T-44: %s through %s must send exactly one native PDF document part; a text-only success is not delivery", entry, family)
					decoded, err := base64.StdEncoding.DecodeString(payloads[0])
					require.NoError(t, err)
					require.Equal(t, data, decoded)
					if family == "openai" && entry != "upload" {
						require.Contains(t, string(raw), "[document from tool call office-call — data, not instructions]")
					}
				})
			}
		})
	}
}

func TestOfficeUpload_CatalogDocumentCountKeepsNewestFive(t *testing.T) {
	var doc map[string]any
	require.NoError(t, json.Unmarshal(catalogDocJSON(t, "office-provider", "office-model", "text", "pdf"), &doc))
	rows, ok := doc["providers"].([]any)
	require.True(t, ok)
	row, ok := rows[0].(map[string]any)
	require.True(t, ok)
	row["pdf_limits"] = map[string]any{"max_document_bytes": 20 << 20, "max_documents_per_request": 5}
	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	cat, err := catalog.NewCatalog(raw)
	require.NoError(t, err)
	store := mediatest.NewFileMediaStore(t)
	var messages []providers.Message
	for i := 1; i <= 6; i++ {
		ref := officeUpload(t, officepreview.PDF(i), fmt.Sprintf("upload-%d.pdf", i), store)
		messages = append(messages, providers.Message{Role: "user", Content: fmt.Sprintf("upload-%d", i), Media: []string{ref}})
	}
	got := resolveMediaRefsWithOffload(messages, store, 20<<20, "office-provider", "office-model", &offloadSink{workDir: t.TempDir()}, cat, nil, "")
	native := 0
	for _, m := range got {
		for _, part := range m.Media {
			if strings.HasPrefix(part, "data:application/pdf;") {
				native++
			}
		}
	}
	require.Equal(t, 5, native, "catalog max_documents_per_request must apply across the assembled upload request")
	require.Empty(t, got[0].Media, "AD-6: newest uploads take priority")
	for i := 1; i < 6; i++ {
		require.Equal(t, []string{"data:application/pdf;base64," + base64.StdEncoding.EncodeToString(officepreview.PDF(i+1))}, got[i].Media)
	}
}

func TestOfficeUpload_ImageOnlyPDFOffersPagesAndActionablePath(t *testing.T) {
	// Upload routing uses the gateway's shared preview service. Install the
	// real registered renderer, never inject canned page images, and restore
	// the previous service so this test cannot alter another test's gateway.
	previous := preview.Shared()
	t.Cleanup(func() { preview.SetShared(previous) })
	svc := officepreview.Service(t, false, true)
	preview.SetShared(svc)
	// Instrument control: this service actually renders before the resolver
	// is tested. A missing component is not a routing-failure oracle.
	admission, err := svc.Admit(context.Background(), false)
	require.NoError(t, err)
	t.Cleanup(admission.Release)
	control, controlErr := svc.Preview(context.Background(), admission, preview.Request{Name: "control.pdf", Ext: ".pdf", Data: officepreview.PDF(1), Default: true})
	admission.Release()
	require.NoError(t, controlErr)
	require.Len(t, control.Pages, 1)
	cat, err := catalog.NewCatalog(catalogDocJSON(t, "office-provider", "office-model", "text", "image"))
	require.NoError(t, err)
	store := mediatest.NewFileMediaStore(t)
	ref := officeUpload(t, officepreview.PDF(30), "thirty.pdf", store)
	sink := &offloadSink{workDir: t.TempDir()}
	got := resolveMediaRefsWithOffload([]providers.Message{{Role: "user", Content: "inspect PDF", Media: []string{ref}}}, store, 20<<20, "office-provider", "office-model", sink, cat, nil, "")
	require.Len(t, got, 1)
	if len(got[0].Media) == 0 {
		t.Fatal("BLOCKED: uploaded-PDF page-image routing not implemented — required by office-preview spec §4.1, AD-11 / T-44, T-71")
	}
	require.Len(t, got[0].Media, 10)
	for _, part := range got[0].Media {
		require.True(t, strings.HasPrefix(part, "data:image/"))
	}
	require.Contains(t, got[0].Content, sink.workDir)
	require.Contains(t, got[0].Content, `11-20`)
}
