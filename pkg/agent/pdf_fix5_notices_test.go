package agent

// #1968 FIX5: spec §5.6 N1/N3/L1/L2 and S10. Oracles are the notice
// content and fixture limits in the spec, never production format helpers.
// N1/L1 are parity guards, not proof of a source-code deduplication refactor.

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

func TestPDFLimitNotice_ReadablePagesPerFile(t *testing.T) {
	for _, pages := range []int{100, 101} { // S10: inclusive default-row boundary.
		t.Run(fmt.Sprintf("pages=%d", pages), func(t *testing.T) {
			peer := newPdf1968Peer(t, pdf1968Step{content: "received"})
			p, err := providers.NewHTTPProviderWithTimeouts("test-placeholder", peer.server.URL, "", "max_tokens", 10, 10, nil)
			require.NoError(t, err)
			f := pdfFix5Flow(t, p, "fx/readable", "fx/pdf", nil)
			pdf1968SetLimits(t, f.al, map[string]any{"max_pages_per_file": 100}, nil)
			data := pdf1968TextPDF(t, pages, true)
			messages := []providers.Message{{Role: "user", Content: "inspect pages", Media: []string{"data:application/pdf;base64," + base64.StdEncoding.EncodeToString(data)}}}
			_, err = f.rt.callProviderOnce(messages, nil)
			require.NoError(t, err)
			bodies := peer.Bodies()
			require.Len(t, bodies, 1)
			if pages == 100 {
				assert.Equal(t, [][]byte{data}, pdf1968BodyPDFs(t, bodies[0]))
				assert.NotContains(t, bodies[0], "PDF sent as text")
				return
			}
			assert.Empty(t, pdf1968BodyPDFs(t, bodies[0]))
			assert.Contains(t, bodies[0], "PAGE 1 text")
			assert.Contains(t, bodies[0], "PDF sent as text")
			assert.Contains(t, bodies[0], "fx/readable/fx/pdf", "L2 names the actual limit owner")
			assert.Regexp(t, regexp.MustCompile(`(?i)100\s+pages\s+per\s+(file|PDF)`), bodies[0], "S10: L2 must explain 100 pages per file, not expose a data key")
			assert.NotContains(t, bodies[0], "max_pages_per_file")
			assert.False(t, f.rt.ts.mediaRetryDone.Load())
		})
	}
}

func TestChatPDF_UnsupportedVersionPresentation(t *testing.T) {
	// Change only the same-width version header of an otherwise readable text
	// PDF. The known text proves a legacy fallback must not override FR-6's
	// unsupported-version classification and invent a successful local read.
	data := pdf1968TextPDF(t, 1, true)
	require.True(t, strings.HasPrefix(string(data), "%PDF-1.4"))
	data = append([]byte("%PDF-2.0"), data[len("%PDF-1.4"):]...)
	for _, capable := range []bool{true, false} {
		t.Run(fmt.Sprintf("capable=%t", capable), func(t *testing.T) {
			peer := newPdf1968Peer(t, pdf1968Step{content: "received"})
			p, err := providers.NewHTTPProviderWithTimeouts("test-placeholder", peer.server.URL, "", "max_tokens", 10, 10, nil)
			require.NoError(t, err)
			f := pdfFix5Flow(t, p, "fx/version", "fx/pdf", nil)
			row := pdf1968CapableRow("fx/version", "fx/pdf", nil)
			if !capable {
				row = pdf1968TextOnlyRow("fx/version", "fx/pdf", nil)
			}
			f.al.SetCapabilityCatalog(pdf1968Catalog(t, []pdf1968ProviderRow{row}))
			url := "data:application/pdf;base64," + base64.StdEncoding.EncodeToString(data)
			f.rt.chatPDFs = &chatPDFRegistry{}
			f.rt.chatPDFs.register(url, "future.pdf")
			_, err = f.rt.callProviderOnce([]providers.Message{{Role: "user", Content: "inspect future PDF", Media: []string{url}}}, nil)
			require.NoError(t, err)
			bodies := peer.Bodies()
			require.Len(t, bodies, 1)
			if capable {
				assert.Equal(t, [][]byte{data}, pdf1968BodyPDFs(t, bodies[0]), "FR-8: local version rejection must not prevent native viewing")
				return
			}
			assert.Empty(t, pdf1968BodyPDFs(t, bodies[0]))
			assert.Contains(t, bodies[0], "future.pdf")
			assert.Contains(t, bodies[0], "could not be read locally (unsupported version)", "N3 must retain the typed local reason in chat presentation")
			assert.Contains(t, bodies[0], "sent as is to PDF-capable models")
			assert.Contains(t, bodies[0], "this model does not accept PDF input here")
			assert.NotContains(t, bodies[0], "PAGE 1 text", "unsupported version is not successful text extraction")
		})
	}
}

func pdfFix5NoticeLine(t *testing.T, text, prefix string) string {
	t.Helper()
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, prefix) {
			return line
		}
	}
	t.Fatalf("notice %q missing from %q", prefix, text)
	return ""
}

func TestPDFNotices_ReaderChatParity(t *testing.T) {
	t.Run("N1 scanned", func(t *testing.T) {
		data := pdf1968NoTextPDF(t, 1, true)
		root := t.TempDir()
		path := filepath.Join(root, "scanned.pdf")
		require.NoError(t, os.WriteFile(path, data, 0o600))
		got := tools.NewReadFileTool(root, false, tools.MaxReadFileSize).Execute(context.Background(), map[string]any{"path": path})
		require.False(t, got.IsError, "%s", got.ForLLM)
		require.Len(t, got.InspectionPDFs, 1)
		chat := &chatPDF{name: "scanned.pdf"}
		chat.inspect("data:application/pdf;base64," + base64.StdEncoding.EncodeToString(data))
		expected := "[document: scanned.pdf | no extractable text (likely scanned) | readable natively by PDF-capable models; otherwise convert pages to PNG/JPEG]"
		toolNotice := pdfFix5NoticeLine(t, got.ForLLM, "[document: scanned.pdf | no extractable text")
		chatNotice := pdfFix5NoticeLine(t, strings.TrimSpace(chat.injection), "[document: scanned.pdf | no extractable text")
		assert.Equal(t, expected, toolNotice)
		assert.Equal(t, expected, chatNotice)
		assert.Equal(t, toolNotice, chatNotice, "N1 content must not drift across persisted and chat paths")
		assert.False(t, chat.hasText)
	})
	t.Run("L1 operator size", func(t *testing.T) {
		data := pdf1968TextPDF(t, 1, true)
		root := t.TempDir()
		path := filepath.Join(root, "sized.pdf")
		require.NoError(t, os.WriteFile(path, data, 0o600))
		limit := len(data) - 1 // Operator fixture setting: raw file is one byte over.
		reader := tools.NewReadFileTool(root, false, tools.MaxReadFileSize)
		reader.SetMaxInspectionImageBytes(limit)
		got := reader.Execute(context.Background(), map[string]any{"path": path})
		require.False(t, got.IsError, "%s", got.ForLLM)
		assert.Empty(t, got.InspectionPDFs)
		url, chat := encodeChatPDF(path, "application/pdf", "sized.pdf", int64(len(data)), limit, &chatPDFRegistry{})
		assert.Empty(t, url)
		toolNotice := pdfFix5NoticeLine(t, got.ForLLM, "[PDF over the ")
		chatNotice := pdfFix5NoticeLine(t, chat, "[PDF over the ")
		assert.Equal(t, toolNotice, chatNotice, "L1 content must not drift across the two callers")
		assert.Contains(t, toolNotice, fmt.Sprint(limit))
		assert.Contains(t, toolNotice, "media size setting")
		assert.Contains(t, toolNotice, "not sent natively")
		assert.Contains(t, got.ForLLM, "PAGE 1 text")
		assert.Contains(t, chat, "PAGE 1 text")
	})
}
